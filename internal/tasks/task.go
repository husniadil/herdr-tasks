// Package tasks is the state machine: tasks and notes, their transitions, and
// the rules that guard them. It is pure — no daemon, no socket, no SQLite, no
// Herdr. Every function takes the current value, an actor, and a clock reading,
// and returns the event the caller must append (§5.5) or a coded error (§6.3).
package tasks

import (
	"strings"

	"github.com/husniadil/herdr-tasks/internal/codes"
)

// Status is a task's place in todo → doing → review → done | cancelled.
type Status string

const (
	StatusTodo      Status = "todo"
	StatusDoing     Status = "doing"
	StatusReview    Status = "review"
	StatusDone      Status = "done"
	StatusCancelled Status = "cancelled"
)

// Terminal reports whether a status admits no further transitions.
func (s Status) Terminal() bool { return s == StatusDone || s == StatusCancelled }

// Principal names who acts, `<kind>:<id>` (§3.1).
type Principal string

const (
	// PrincipalHuman is the operator: a call with no HERDR_PANE_ID (§3.2).
	PrincipalHuman Principal = "human"
	// PrincipalPlugin is the plugin acting on its own behalf: sweeps, hooks.
	PrincipalPlugin Principal = "plugin:tasks"
)

// Kind is the part of a principal before the colon.
func (p Principal) Kind() string {
	if i := strings.IndexByte(string(p), ':'); i >= 0 {
		return string(p)[:i]
	}
	return string(p)
}

// Actor is a principal plus the three Herdr facts snapshotted at the moment
// they matter (§3.4). For a human the snapshot is empty.
type Actor struct {
	Principal Principal
	Name      string
	Harness   string
	Session   string
}

// IsHuman reports whether the actor is the operator, who is exempt from
// recusal (§6.6) and is the only principal that may promote a note.
func (a Actor) IsHuman() bool { return a.Principal.Kind() == "human" }

// Criterion is one acceptance criterion: a proof an evaluator can check from a
// transcript (§16.1).
type Criterion struct {
	Text     string `json:"text"`
	Required bool   `json:"required"`
}

// Event is the append-only record of a state change (§5.5). The store writes
// it in the same transaction as the mutation; the state machine only says what
// happened.
type Event struct {
	Kind   string         `json:"kind"`
	Actor  Principal      `json:"actor"`
	At     int64          `json:"at"`
	Detail map[string]any `json:"detail,omitempty"`
}

// Event kinds. Released and swept are both a released lease; they differ in
// who let go, and the trail must say which (§11.5).
const (
	KindCreated   = "created"
	KindClaimed   = "claimed"
	KindTouched   = "touched"
	KindReleased  = "released"
	KindSwept     = "swept"
	KindSubmitted = "submitted"
	KindApproved  = "approved"
	KindRejected  = "rejected"
	KindCancelled = "cancelled"
	KindArchived  = "archived"
	KindUpdated   = "updated"
)

// Task is a unit of work with a lifecycle and a claim (§14).
type Task struct {
	ID          string      `json:"id"`
	Seq         int64       `json:"seq"`
	Project     string      `json:"project"`
	Title       string      `json:"title"`
	Description string      `json:"description,omitempty"`
	Status      Status      `json:"status"`
	Priority    int64       `json:"priority"`
	Validation  []Criterion `json:"validation,omitempty"`
	// DiscoveredFrom is the task this one was found while working on.
	// Provenance only: it has no bearing on blocked/ready.
	DiscoveredFrom string   `json:"discovered_from,omitempty"`
	Deps           []string `json:"deps,omitempty"`
	// Blocked is derived by the store from Deps (a dep that is not done) and
	// handed to the state machine, which does no I/O of its own.
	Blocked bool `json:"blocked"`

	CreatedBy Principal `json:"created_by"`
	CreatedAt int64     `json:"created_at"`
	UpdatedAt int64     `json:"updated_at"`

	ClaimedBy        Principal `json:"claimed_by,omitempty"`
	ClaimedByName    string    `json:"claimed_by_name,omitempty"`
	ClaimedByHarness string    `json:"claimed_by_harness,omitempty"`
	ClaimedBySession string    `json:"claimed_by_session,omitempty"`
	ClaimedAt        int64     `json:"claimed_at,omitempty"`
	LeaseUntil       int64     `json:"lease_until,omitempty"`
	// EverClaimed survives a release: it is what makes a task un-deletable
	// forever after (§5.7).
	EverClaimed bool `json:"ever_claimed"`

	ReleaseNote string `json:"release_note,omitempty"`
	ReleasedAt  int64  `json:"released_at,omitempty"`

	Report             string    `json:"report,omitempty"`
	Evidence           []string  `json:"evidence,omitempty"`
	SubmittedBy        Principal `json:"submitted_by,omitempty"`
	SubmittedByHarness string    `json:"submitted_by_harness,omitempty"`
	SubmittedAt        int64     `json:"submitted_at,omitempty"`

	Feedback   string    `json:"feedback,omitempty"`
	ReviewedBy Principal `json:"reviewed_by,omitempty"`

	CompletedAt int64 `json:"completed_at,omitempty"`
	CancelledAt int64 `json:"cancelled_at,omitempty"`
	ArchivedAt  int64 `json:"archived_at,omitempty"`

	// PaneID, TabID and WorkspaceID are Herdr context for display and
	// navigation, never a partition key (§4.3).
	PaneID      string `json:"pane_id,omitempty"`
	TabID       string `json:"tab_id,omitempty"`
	WorkspaceID string `json:"workspace_id,omitempty"`
}

// NewTaskInput is everything a caller may set at creation.
type NewTaskInput struct {
	ID             string
	Seq            int64
	Project        string
	Title          string
	Description    string
	Priority       int64
	Validation     []Criterion
	DiscoveredFrom string
	Deps           []string
	PaneID         string
	TabID          string
	WorkspaceID    string
}

// New builds a task in todo.
func New(in NewTaskInput, by Actor, now int64) (*Task, Event, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return nil, Event{}, codes.New(codes.Usage, "title is required")
	}
	if in.Project == "" {
		return nil, Event{}, codes.New(codes.Usage, "project is required")
	}
	t := &Task{
		ID:             in.ID,
		Seq:            in.Seq,
		Project:        in.Project,
		Title:          title,
		Description:    strings.TrimSpace(in.Description),
		Status:         StatusTodo,
		Priority:       in.Priority,
		Validation:     in.Validation,
		DiscoveredFrom: in.DiscoveredFrom,
		Deps:           in.Deps,
		CreatedBy:      by.Principal,
		CreatedAt:      now,
		UpdatedAt:      now,
		PaneID:         in.PaneID,
		TabID:          in.TabID,
		WorkspaceID:    in.WorkspaceID,
	}
	return t, Event{Kind: KindCreated, Actor: by.Principal, At: now,
		Detail: map[string]any{"title": title}}, nil
}

// Claim takes the lease. One winner: a claim on a task someone else holds is
// CONFLICT, never a silent no-op (§5.6). The holder re-claiming renews.
func Claim(t *Task, by Actor, now, leaseMS int64) (Event, error) {
	if t.Status.Terminal() || t.ArchivedAt != 0 {
		return Event{}, codes.Errorf(codes.Conflict, "task is %s", t.Status)
	}
	if t.Status == StatusReview {
		return Event{}, codes.New(codes.Conflict, "task is in review")
	}
	if t.ClaimedBy != "" && t.ClaimedBy != by.Principal {
		return Event{}, codes.Errorf(codes.Conflict, "claimed by %s", t.ClaimedBy)
	}
	if t.Blocked {
		return Event{}, codes.New(codes.Conflict, "task is blocked by an unfinished dependency")
	}
	t.Status = StatusDoing
	t.ClaimedBy = by.Principal
	t.ClaimedByName = by.Name
	t.ClaimedByHarness = harnessOf(by)
	t.ClaimedBySession = by.Session
	t.ClaimedAt = now
	t.LeaseUntil = now + leaseMS
	t.EverClaimed = true
	t.UpdatedAt = now
	return Event{Kind: KindClaimed, Actor: by.Principal, At: now,
		Detail: map[string]any{"lease_until": t.LeaseUntil, "harness": t.ClaimedByHarness}}, nil
}

// Touch renews the lease. Only the holder may renew (§16.3).
func Touch(t *Task, by Actor, now, leaseMS int64) (Event, error) {
	if t.ClaimedBy == "" {
		return Event{}, codes.New(codes.Conflict, "task is not claimed")
	}
	if t.ClaimedBy != by.Principal {
		return Event{}, codes.Errorf(codes.Forbidden, "task is claimed by %s", t.ClaimedBy)
	}
	t.LeaseUntil = now + leaseMS
	t.UpdatedAt = now
	return Event{Kind: KindTouched, Actor: by.Principal, At: now,
		Detail: map[string]any{"lease_until": t.LeaseUntil}}, nil
}

// Release drops the lease and hands the task back to the queue with a note
// saying what is left. kind is KindReleased when the holder let go and
// KindSwept when the daemon took it back (§11.5).
func Release(t *Task, by Actor, note string, now int64, kind string) (Event, error) {
	if t.ClaimedBy == "" {
		return Event{}, codes.New(codes.Conflict, "task is not claimed")
	}
	if kind != KindSwept && !by.IsHuman() && t.ClaimedBy != by.Principal {
		return Event{}, codes.Errorf(codes.Forbidden, "task is claimed by %s", t.ClaimedBy)
	}
	if t.Status == StatusDoing {
		t.Status = StatusTodo
	}
	t.ClaimedBy, t.ClaimedByName, t.ClaimedBySession = "", "", ""
	t.ClaimedAt, t.LeaseUntil = 0, 0
	t.ReleaseNote = note
	t.ReleasedAt = now
	t.UpdatedAt = now
	return Event{Kind: kind, Actor: by.Principal, At: now,
		Detail: map[string]any{"note": note}}, nil
}

// Submit sends the work to review with a report and its evidence.
func Submit(t *Task, by Actor, report string, evidence []string, now int64) (Event, error) {
	if strings.TrimSpace(report) == "" {
		return Event{}, codes.New(codes.Usage, "a report is required")
	}
	if t.Status != StatusDoing {
		return Event{}, codes.Errorf(codes.Conflict, "task is %s, not doing", t.Status)
	}
	if t.ClaimedBy != "" && t.ClaimedBy != by.Principal && !by.IsHuman() {
		return Event{}, codes.Errorf(codes.Forbidden, "task is claimed by %s", t.ClaimedBy)
	}
	t.Status = StatusReview
	t.Report = report
	t.Evidence = evidence
	t.SubmittedBy = by.Principal
	t.SubmittedByHarness = harnessOf(by)
	t.SubmittedAt = now
	t.Feedback = ""
	t.UpdatedAt = now
	return Event{Kind: KindSubmitted, Actor: by.Principal, At: now,
		Detail: map[string]any{"evidence_count": len(evidence)}}, nil
}

// Approve closes the task. Recusal applies (§6.6).
func Approve(t *Task, by Actor, now int64) (Event, error) {
	if t.Status != StatusReview {
		return Event{}, codes.Errorf(codes.Conflict, "task is %s, not in review", t.Status)
	}
	if err := CheckRecusal(t, by); err != nil {
		return Event{}, err
	}
	t.Status = StatusDone
	t.ReviewedBy = by.Principal
	t.CompletedAt = now
	t.ClaimedBy, t.LeaseUntil = "", 0
	t.UpdatedAt = now
	return Event{Kind: KindApproved, Actor: by.Principal, At: now}, nil
}

// Reject sends the work back with feedback. Recusal applies (§6.6). A rejected
// task whose claim was already swept returns to todo, because a doing row that
// nobody holds cannot be claimed.
func Reject(t *Task, by Actor, feedback string, now int64) (Event, error) {
	if strings.TrimSpace(feedback) == "" {
		return Event{}, codes.New(codes.Usage, "feedback is required to reject")
	}
	if t.Status != StatusReview {
		return Event{}, codes.Errorf(codes.Conflict, "task is %s, not in review", t.Status)
	}
	if err := CheckRecusal(t, by); err != nil {
		return Event{}, err
	}
	if t.ClaimedBy != "" {
		t.Status = StatusDoing
	} else {
		t.Status = StatusTodo
	}
	t.Feedback = feedback
	t.ReviewedBy = by.Principal
	t.UpdatedAt = now
	return Event{Kind: KindRejected, Actor: by.Principal, At: now,
		Detail: map[string]any{"feedback": feedback}}, nil
}

// CheckRecusal enforces §6.6: a principal does not review work produced by its
// own harness. The human is exempt.
func CheckRecusal(t *Task, by Actor) error {
	if by.IsHuman() {
		return nil
	}
	producer := t.SubmittedByHarness
	if producer == "" {
		producer = t.ClaimedByHarness
	}
	if producer != "" && producer == harnessOf(by) {
		return codes.Errorf(codes.Forbidden,
			"recusal (§6.6): %s may not review work produced by the same harness", producer)
	}
	return nil
}

// Cancel ends a task that will not be done.
func Cancel(t *Task, by Actor, reason string, now int64) (Event, error) {
	if t.Status.Terminal() {
		return Event{}, codes.Errorf(codes.Conflict, "task is already %s", t.Status)
	}
	t.Status = StatusCancelled
	t.ClaimedBy, t.ClaimedByName, t.ClaimedBySession = "", "", ""
	t.ClaimedAt, t.LeaseUntil = 0, 0
	t.CancelledAt = now
	t.UpdatedAt = now
	return Event{Kind: KindCancelled, Actor: by.Principal, At: now,
		Detail: map[string]any{"reason": reason}}, nil
}

// Archive hides a finished task. Only a terminal task archives: active work
// stays visible (§5.7).
func Archive(t *Task, by Actor, now int64) (Event, error) {
	if !t.Status.Terminal() {
		return Event{}, codes.Errorf(codes.Conflict, "task is %s; only done or cancelled tasks archive", t.Status)
	}
	t.ArchivedAt = now
	t.UpdatedAt = now
	return Event{Kind: KindArchived, Actor: by.Principal, At: now}, nil
}

// CanHardDelete answers §5.7: a row may be removed for good only if it never
// left its initial state. A task that was ever claimed is cancelled, not
// deleted, however it looks now.
func CanHardDelete(t *Task) error {
	if t.EverClaimed || t.Status != StatusTodo {
		return codes.New(codes.Conflict,
			"only a never-claimed todo task is deleted (§5.7); cancel it instead")
	}
	return nil
}

// UpdatePatch is the set of editable fields. A nil pointer leaves the field
// alone, which is what makes "clear the description" expressible.
type UpdatePatch struct {
	Title          *string
	Description    *string
	Priority       *int64
	Validation     *[]Criterion
	Deps           *[]string
	DiscoveredFrom *string
}

// Update edits a live task.
func Update(t *Task, by Actor, p UpdatePatch, now int64) (Event, error) {
	if t.Status.Terminal() {
		return Event{}, codes.Errorf(codes.Conflict, "task is %s", t.Status)
	}
	changed := make([]string, 0, 6)
	if p.Title != nil {
		title := strings.TrimSpace(*p.Title)
		if title == "" {
			return Event{}, codes.New(codes.Usage, "title cannot be emptied")
		}
		t.Title, changed = title, append(changed, "title")
	}
	if p.Description != nil {
		t.Description, changed = *p.Description, append(changed, "description")
	}
	if p.Priority != nil {
		t.Priority, changed = *p.Priority, append(changed, "priority")
	}
	if p.Validation != nil {
		t.Validation, changed = *p.Validation, append(changed, "validation")
	}
	if p.Deps != nil {
		t.Deps, changed = *p.Deps, append(changed, "deps")
	}
	if p.DiscoveredFrom != nil {
		t.DiscoveredFrom, changed = *p.DiscoveredFrom, append(changed, "discovered_from")
	}
	t.UpdatedAt = now
	return Event{Kind: KindUpdated, Actor: by.Principal, At: now,
		Detail: map[string]any{"fields": changed}}, nil
}

// Ready reports whether a task is claimable right now: unblocked, unclaimed,
// still in todo.
func Ready(t *Task) bool {
	return t.Status == StatusTodo && !t.Blocked && t.ClaimedBy == "" && t.ArchivedAt == 0
}

// LeaseExpired reports whether a claim has outlived its lease and is due for a
// sweep (§11.5).
func LeaseExpired(t *Task, now int64) bool {
	return t.ClaimedBy != "" && t.LeaseUntil != 0 && now > t.LeaseUntil
}

// CheckCycle rejects a dependency set that would close a loop. edges is the
// current graph with taskID's own edges excluded; deps is the candidate set.
func CheckCycle(taskID string, deps []string, edges map[string][]string) error {
	stack := make([]string, 0, len(deps))
	for _, d := range deps {
		if d == taskID {
			return codes.New(codes.Usage, "a task cannot depend on itself")
		}
		stack = append(stack, d)
	}
	seen := make(map[string]bool, len(edges))
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur == taskID {
			return codes.Errorf(codes.Usage, "dependency cycle through %s", taskID)
		}
		if seen[cur] {
			continue
		}
		seen[cur] = true
		stack = append(stack, edges[cur]...)
	}
	return nil
}

// harnessOf is §3.4's "store unknown rather than guess": an agent principal
// whose harness Herdr could not report is recorded as unknown, and unknown
// never matches unknown for recusal purposes (see CheckRecusal, which compares
// non-empty strings — two unknowns do compare equal, which is the conservative
// direction: it recuses).
func harnessOf(a Actor) string {
	if a.IsHuman() {
		return ""
	}
	if a.Harness == "" {
		return "unknown"
	}
	return a.Harness
}
