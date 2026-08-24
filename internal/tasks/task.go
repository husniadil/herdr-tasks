// Package tasks is the state machine: tasks and notes, their transitions, and
// the rules that guard them. It is pure — no daemon, no socket, no SQLite, no
// Herdr. Every function takes the current value, an actor, and a clock reading,
// and returns the event the caller must append (§5.5) or a coded error (§6.3).
package tasks

import (
	"strconv"
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

// Statuses is every value a task's status holds, in lifecycle order. It is
// here rather than at a caller because the vocabulary belongs to the state
// machine: a filter that names something outside it can never match, and an
// empty list is the answer to a different question.
var Statuses = []Status{StatusTodo, StatusDoing, StatusReview, StatusDone, StatusCancelled}

// Terminal reports whether a status admits no further transitions.
func (s Status) Terminal() bool { return s == StatusDone || s == StatusCancelled }

// Principal names who acts, `<kind>:<id>` (§3.1).
type Principal string

const (
	// PrincipalHuman is the operator: a paneless call the plugin can point at
	// a deliberate human act for — a CLI invocation, or a door declared with
	// --operator (§3.7).
	PrincipalHuman Principal = "human"
	// PrincipalNone is a caller the plugin cannot identify: a door standing
	// in no pane that was never declared (§3.7). It is written into the
	// ledger verbatim, because a row filed by nobody in particular saying so
	// is the whole point of it not being filed under the operator.
	PrincipalNone Principal = "none"
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
// recusal (§6.6). It no longer decides who may call an operator verb: since
// 0.10.0 operator-only is advice an agent confirms with the user before
// acting, not a refusal a door makes (§3.7). What IsHuman still decides is
// what an operator verb's event says about who performed it — see
// operatorVerb.
func (a Actor) IsHuman() bool { return a.Principal.Kind() == "human" }

// OnBehalfOfOperator is the detail key operatorVerb writes. It is a shipped
// --json field the moment it appears in an event, so it is added and never
// repurposed (§6.2).
const OnBehalfOfOperator = "on_behalf_of_operator"

// operatorVerb marks an event whose authority is the operator's when a
// principal other than the operator performed it (§3.7). The plugin does not
// check that the agent confirmed with the user — a verb demanding proof of
// confirmation would be the refusal wearing a different coat — so the trail is
// the whole accountability, and the trail is only honest if it says who acted.
//
// The actor recorded stays the calling principal, never `human`: every path
// that goes through this function labels itself, rather than each call site
// being trusted to remember that an agent reached an operator verb.
func operatorVerb(by Actor, detail map[string]any) map[string]any {
	if by.IsHuman() {
		return detail
	}
	if detail == nil {
		detail = map[string]any{}
	}
	detail[OnBehalfOfOperator] = true
	return detail
}

// Criterion is one acceptance criterion: a proof an evaluator can check from a
// transcript (§16.1).
type Criterion struct {
	Text     string `json:"text"`
	Required bool   `json:"required"`
}

// Citation binds one piece of evidence to the acceptance criterion it proves
// (§16.1). Criterion is 1-based, matching the position the criteria list
// prints. Citations live BESIDE Evidence rather than inside it: `evidence` is
// a shipped --json field and repurposing it would break a caller that already
// reads it. A citation is coverage bookkeeping written by the submitter, not a
// verification anyone else performed.
type Citation struct {
	Criterion int    `json:"criterion"`
	Text      string `json:"text"`
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
	KindAmended   = "amended"
	KindApproved  = "approved"
	KindRejected  = "rejected"
	KindCancelled = "cancelled"
	KindArchived  = "archived"
	KindUpdated   = "updated"
	// KindDeleted is the hard delete of §5.7. The row goes and this stays, so
	// the trail says the task was removed rather than leaving a created task
	// that silently stops existing.
	KindDeleted = "deleted"
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
	// Abandoned is the seq of every dependency that was CANCELLED. Only `done`
	// satisfies a dependency, so a cancelled one blocks its dependents for
	// good — and nothing said so, which turned a decision into a trap. Naming
	// them makes it a decision again: the way out is to edit the edge.
	Abandoned []int64 `json:"blocked_by_cancelled,omitempty"`

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

	Report             string     `json:"report,omitempty"`
	Evidence           []string   `json:"evidence,omitempty"`
	EvidenceFor        []Citation `json:"evidence_for,omitempty"`
	SubmittedBy        Principal  `json:"submitted_by,omitempty"`
	SubmittedByHarness string     `json:"submitted_by_harness,omitempty"`
	SubmittedBySession string     `json:"submitted_by_session,omitempty"`
	SubmittedAt        int64      `json:"submitted_at,omitempty"`
	// AmendedAt and AmendCount are the marker Amend leaves on the row. A
	// submission is made once and judged once; a correction to its report is a
	// second thing, and a reviewer who reads the row after one has landed sees
	// that it did without needing the worker to have sent mail about it.
	AmendedAt  int64 `json:"amended_at,omitempty"`
	AmendCount int64 `json:"amend_count,omitempty"`

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
	if err := boundTaskText(title, in.Description, in.Validation); err != nil {
		return nil, Event{}, err
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
	// A claim by the principal that already holds it is a RENEWAL, not a new
	// claim (§5.6): an agent whose reply was lost must be able to retry. The
	// checks below are about taking work, so they do not apply to keeping it —
	// a dependency added by `task update` while an agent is working would
	// otherwise break its retry on a task it still holds, while `touch`, doing
	// the same job, kept working. Blocked is a gate on taking work, not on
	// keeping it; the operator who added the dependency takes that up with the
	// agent, and `task get` still says the task is blocked.
	if t.ClaimedBy != by.Principal {
		if t.Blocked {
			return Event{}, codes.New(codes.Conflict, "task is blocked by an unfinished dependency")
		}
		if t.ClaimedBy != "" {
			return Event{}, alreadyClaimed(t, by)
		}
	}
	t.Status = StatusDoing
	t.ClaimedBy = by.Principal
	t.ClaimedByName = by.Name
	t.ClaimedByHarness = harnessOf(by)
	t.ClaimedBySession = sessionOf(by)
	t.ClaimedAt = now
	t.LeaseUntil = now + leaseMS
	t.EverClaimed = true
	t.UpdatedAt = now
	return Event{Kind: KindClaimed, Actor: by.Principal, At: now,
		Detail: map[string]any{"lease_until": t.LeaseUntil, "harness": t.ClaimedByHarness}}, nil
}

// alreadyClaimed is the refusal for taking a lease somebody else holds. It is
// CONFLICT rather than FORBIDDEN, and it stays that way - the code vocabulary
// is semver-bound - but its prose was the same bare "claimed by <holder>" that
// notHolder replaced, and this is the FIRST message a worker meets when it
// walks into someone else's lease. Terseness here is where the misreading
// that produced task 80 starts; notHolder only cleans it up afterwards.
func alreadyClaimed(t *Task, by Actor) error {
	return codes.Errorf(codes.Conflict,
		"you are %s and the lease on this task is already held by %s: it is not free to take. "+
			"Declaring a principal does not move a lease - `--as` says who is calling, it does not transfer a claim. "+
			"Ask the holder to release it, or wait for its lease to expire.",
		by.Principal, t.ClaimedBy)
}

// notHolder is the refusal a stranger gets from the four verbs a lease
// reserves for its holder. It names the caller as well as the holder, and
// says what a lease is not: task 80 recorded a worker that read the older
// "task is claimed by plugin:hdis" as a demand for that principal and
// re-ran the call with `--as plugin:hdis`, which went through and credited
// a plugin with an agent's work. The message it misread said who held the
// lease and nothing about who the reader was or what to do next.
func notHolder(t *Task, by Actor, verb string) error {
	return codes.Errorf(codes.Forbidden,
		"you are %s and the lease on this task is held by %s: only the holder may %s it. "+
			"Declaring a principal does not move a lease - `--as` says who is calling, it does not transfer a claim. "+
			"Ask the holder to release it, or wait for its lease to expire.",
		by.Principal, t.ClaimedBy, verb)
}

// Touch renews the lease. Only the holder may renew (§16.3).
func Touch(t *Task, by Actor, now, leaseMS int64) (Event, error) {
	if t.ClaimedBy == "" {
		return Event{}, codes.New(codes.Conflict, "task is not claimed")
	}
	if t.ClaimedBy != by.Principal {
		return Event{}, notHolder(t, by, "touch")
	}
	// Wherever touch works for the holder, claim works too — the rule task 30
	// asked for. Claim refuses a task in review; so does this, and for the
	// same reason: there is no work in hand to keep. Without it one touch
	// would put back the lease Submit just ended.
	if t.Status != StatusDoing {
		return Event{}, codes.Errorf(codes.Conflict,
			"task is %s, not doing; there is no lease to renew", t.Status)
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
		return Event{}, notHolder(t, by, "release")
	}
	if err := bound("note", note, MaxText); err != nil {
		return Event{}, err
	}
	if t.Status == StatusDoing {
		t.Status = StatusTodo
	}
	t.ClaimedBy, t.ClaimedByName, t.ClaimedByHarness, t.ClaimedBySession = "", "", "", ""
	t.ClaimedAt, t.LeaseUntil = 0, 0
	t.ReleaseNote = note
	t.ReleasedAt = now
	t.UpdatedAt = now
	return Event{Kind: kind, Actor: by.Principal, At: now,
		Detail: map[string]any{"note": note}}, nil
}

// Submit sends the work to review with a report and its evidence.
func Submit(t *Task, by Actor, report string, evidence, evidenceFor []string, now int64) (Event, error) {
	if strings.TrimSpace(report) == "" {
		return Event{}, codes.New(codes.Usage, "a report is required")
	}
	if t.Status != StatusDoing {
		return Event{}, codes.Errorf(codes.Conflict, "task is %s, not doing", t.Status)
	}
	if t.ClaimedBy != "" && t.ClaimedBy != by.Principal && !by.IsHuman() {
		return Event{}, notHolder(t, by, "submit")
	}
	if err := bound("report", report, MaxText); err != nil {
		return Event{}, err
	}
	if err := boundList("evidence", evidence, MaxItem, MaxItems); err != nil {
		return Event{}, err
	}
	if err := boundList("evidence-for", evidenceFor, MaxItem, MaxItems); err != nil {
		return Event{}, err
	}
	cites, err := parseCitations(evidenceFor, len(t.Validation))
	if err != nil {
		return Event{}, err
	}
	if err := coversRequired(t.Validation, cites); err != nil {
		return Event{}, err
	}
	t.Status = StatusReview
	// The lease ends here. It is the promise that work in hand is still being
	// done, and submitted work is not in hand: leaving it set would keep the
	// row inside the sweep's reach, so the daemon would eventually write a
	// swept event — "the lease expired" — about work that was handed off
	// rather than abandoned (§11.5). ClaimedBy stays, because it is the
	// board's answer to who submitted this and §6.6 still needs a session.
	t.LeaseUntil = 0
	t.Report = report
	t.Evidence = evidence
	t.EvidenceFor = cites
	t.SubmittedBy = by.Principal
	t.SubmittedByHarness = harnessOf(by)
	t.SubmittedBySession = sessionOf(by)
	t.SubmittedAt = now
	t.Feedback = ""
	t.UpdatedAt = now
	return Event{Kind: KindSubmitted, Actor: by.Principal, At: now,
		Detail: map[string]any{"evidence_count": len(evidence), "citation_count": len(cites)}}, nil
}

// Amend corrects the report and evidence of work already in review, without
// touching the submission itself. It exists because the self-review lane asks
// a worker to keep fixing what a mutation proves unpinned, so the lane
// deliberately produces commits AFTER the submit — and Submit's own
// StatusDoing guard, which is right, left the worker no way to say so on the
// row. Before this verb the only record of the newer head was a message in a
// different store, remembered by hand.
//
// What it does NOT move is the submission: status, submitted_at,
// submitted_by, the submitter's harness and session, and the ended lease all
// stay exactly as Submit left them. §6.6 recuses on the submitting session,
// so moving that field would move who is allowed to review the work.
//
// What a reviewer sees when the report changes under them is the row and the
// trail saying so: AmendCount and AmendedAt on the row, an `amended` event
// beside the `submitted` one, and UpdatedAt moved — which is what makes §5.6's
// --base-updated-at guard refuse an approve built on the report that was
// replaced.
func Amend(t *Task, by Actor, report string, evidence, evidenceFor *[]string, now int64) (Event, error) {
	if strings.TrimSpace(report) == "" {
		return Event{}, codes.New(codes.Usage, "a report is required")
	}
	if t.Status != StatusReview {
		return Event{}, codes.Errorf(codes.Conflict,
			"task is %s, not in review; a report is amended while it waits for a reviewer", t.Status)
	}
	// A claim reserves this the way it reserves touch, release, submit and
	// cancel. The claimless case is its own refusal rather than an open door:
	// a row in review with no holder was swept or released, and letting any
	// principal rewrite its report would put a stranger's words under the
	// submitter's name.
	if t.ClaimedBy == "" {
		if !by.IsHuman() {
			return Event{}, codes.New(codes.Conflict,
				"nobody holds this task, so there is no holder to amend its report")
		}
	} else if t.ClaimedBy != by.Principal && !by.IsHuman() {
		return Event{}, notHolder(t, by, "amend")
	}
	if err := bound("report", report, MaxText); err != nil {
		return Event{}, err
	}
	// A nil list is a list the caller did not name, and it is left alone. The
	// alternative — replace everything, every time — meant `amend --report`
	// silently emptied the evidence a reviewer was reading, which is the
	// quiet wrong record this verb exists to stop. An EMPTY list is still a
	// list: naming --evidence with nothing in it clears it, deliberately.
	if evidence != nil {
		if err := boundList("evidence", *evidence, MaxItem, MaxItems); err != nil {
			return Event{}, err
		}
	}
	if evidenceFor != nil {
		if err := boundList("evidence-for", *evidenceFor, MaxItem, MaxItems); err != nil {
			return Event{}, err
		}
	}
	cites := t.EvidenceFor
	if evidenceFor != nil {
		parsed, err := parseCitations(*evidenceFor, len(t.Validation))
		if err != nil {
			return Event{}, err
		}
		if err := coversRequired(t.Validation, parsed); err != nil {
			return Event{}, err
		}
		cites = parsed
	}
	t.Report = report
	if evidence != nil {
		t.Evidence = *evidence
	}
	t.EvidenceFor = cites
	t.AmendCount++
	t.AmendedAt = now
	t.UpdatedAt = now
	return Event{Kind: KindAmended, Actor: by.Principal, At: now,
		Detail: map[string]any{"amendment": t.AmendCount,
			"evidence_count": len(t.Evidence), "citation_count": len(cites)}}, nil
}

// parseCitations reads the "<criterion>: what it printed" form both doors
// accept. The index is folded into the string on purpose: §6.1 parity is built
// from one argument list, and that list knows string, int, bool and string[] —
// none of them a pair. Anything unreadable is refused whole, because a
// citation that half-parsed is a checked box nobody earned.
func parseCitations(list []string, criteria int) ([]Citation, error) {
	if len(list) == 0 {
		return nil, nil
	}
	out := make([]Citation, 0, len(list))
	for _, raw := range list {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		head, text, ok := strings.Cut(s, ":")
		if !ok {
			return nil, codes.Errorf(codes.Usage,
				"evidence-for wants \"<criterion>: what it printed\", got %q", raw)
		}
		n, err := strconv.Atoi(strings.TrimSpace(head))
		if err != nil {
			return nil, codes.Errorf(codes.Usage,
				"evidence-for wants a criterion number before the colon, got %q", strings.TrimSpace(head))
		}
		if criteria == 0 {
			return nil, codes.Errorf(codes.Usage,
				"this task has no acceptance criteria, so there is nothing for %q to cite", raw)
		}
		if n < 1 || n > criteria {
			return nil, codes.Errorf(codes.Usage,
				"no criterion %d: this task has %d", n, criteria)
		}
		if text = strings.TrimSpace(text); text == "" {
			return nil, codes.Errorf(codes.Usage,
				"criterion %d was cited with nothing to show for it", n)
		}
		out = append(out, Citation{Criterion: n, Text: text})
	}
	return out, nil
}

// coversRequired is the opt-in half of §16.1. Every task already on the board
// is all-required, so a submit that cites nothing is the submit this plugin has
// always accepted. Cite one criterion and you are claiming coverage — then
// every required one needs a citation, or the checklist a reviewer reads would
// show gaps the submitter never acknowledged.
func coversRequired(list []Criterion, cites []Citation) error {
	if len(cites) == 0 {
		return nil
	}
	cited := make(map[int]bool, len(cites))
	for _, c := range cites {
		cited[c.Criterion] = true
	}
	missing := []string{}
	for i, c := range list {
		if c.Required && !cited[i+1] {
			missing = append(missing, strconv.Itoa(i+1))
		}
	}
	if len(missing) == 0 {
		return nil
	}
	word := "criterion"
	if len(missing) > 1 {
		word = "criteria"
	}
	return codes.Errorf(codes.Usage, "no evidence cited for required %s %s",
		word, strings.Join(missing, ", "))
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
	// The whole §3.4 snapshot, not two of its five fields: a row whose
	// claimed_by is empty must not still carry the holder's name, harness,
	// session or claim time. SubmittedBy* stays — §6.6 recuses on it, and it
	// is the board's answer to who produced this work.
	t.ClaimedBy, t.ClaimedByName, t.ClaimedByHarness, t.ClaimedBySession = "", "", "", ""
	t.ClaimedAt, t.LeaseUntil = 0, 0
	t.UpdatedAt = now
	return Event{Kind: KindApproved, Actor: by.Principal, At: now}, nil
}

// Reject sends the work back with feedback. Recusal applies (§6.6). A rejected
// task whose claim was already swept returns to todo, because a doing row that
// nobody holds cannot be claimed.
func Reject(t *Task, by Actor, feedback string, now int64, leaseMS int64) (Event, error) {
	if strings.TrimSpace(feedback) == "" {
		return Event{}, codes.New(codes.Usage, "feedback is required to reject")
	}
	if t.Status != StatusReview {
		return Event{}, codes.Errorf(codes.Conflict, "task is %s, not in review", t.Status)
	}
	if err := CheckRecusal(t, by); err != nil {
		return Event{}, err
	}
	if err := bound("feedback", feedback, MaxText); err != nil {
		return Event{}, err
	}
	if t.ClaimedBy != "" {
		t.Status = StatusDoing
		// Submit ended the lease; holding the task again means holding a
		// lease again, or a worker that dies now is never swept (§6.5).
		t.LeaseUntil = now + leaseMS
	} else {
		t.Status = StatusTodo
	}
	t.Feedback = feedback
	t.ReviewedBy = by.Principal
	t.UpdatedAt = now
	return Event{Kind: KindRejected, Actor: by.Principal, At: now,
		Detail: map[string]any{"feedback": feedback}}, nil
}

// CheckRecusal enforces §6.6: a principal does not review work produced by
// itself, by its own pane, or by its own agent session. The human is exempt.
// The harness does not recuse — two panes running the same model in different
// sessions are two reviewers, which is the 0.6.0 amendment.
func CheckRecusal(t *Task, by Actor) error {
	if by.IsHuman() {
		return nil
	}
	// The principal is the pane: an agent principal is "agent:<pane_id>"
	// (§3.2), so this one comparison covers both self-review and same-pane
	// review.
	if by.Principal != "" && (by.Principal == t.SubmittedBy || by.Principal == t.ClaimedBy) {
		return codes.New(codes.Forbidden, "a principal may not review its own submission")
	}
	// The session catches the resume case: a different pane carrying the same
	// agent_session is the same conversation. A session Herdr could not
	// resolve is "unknown" (§3.4), and unknown matches unknown on purpose —
	// a pane that claimed during a Herdr blip and resumed elsewhere would
	// otherwise approve its own work. Declared principals have no session at
	// all (sessionOf returns ""), so they never match an unresolved one.
	producer := t.SubmittedBySession
	if producer == "" {
		producer = t.ClaimedBySession
	}
	if mine := sessionOf(by); mine != "" && producer == mine {
		return codes.Errorf(codes.Forbidden,
			"recusal (§6.6): %s may not review work produced by the same agent session", by.Principal)
	}
	return nil
}

// Cancel ends a task that will not be done. A CLAIMED task is the holder's or
// the operator's to end — the same rule Release and Submit apply, because
// cancelling is strictly more destructive than releasing: it clears the claim
// AND puts the task beyond reach. An unclaimed task is nobody's and stays as
// open as it was.
func Cancel(t *Task, by Actor, reason string, now int64) (Event, error) {
	if t.Status.Terminal() {
		return Event{}, codes.Errorf(codes.Conflict, "task is already %s", t.Status)
	}
	if t.ClaimedBy != "" && t.ClaimedBy != by.Principal && !by.IsHuman() {
		return Event{}, notHolder(t, by, "cancel")
	}
	if err := bound("reason", reason, MaxText); err != nil {
		return Event{}, err
	}
	t.Status = StatusCancelled
	t.ClaimedBy, t.ClaimedByName, t.ClaimedByHarness, t.ClaimedBySession = "", "", "", ""
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
	// Already hidden is not hidden again. Without this the second call moved
	// archived_at to now and appended a second `archived` event, so the row
	// answered "when was this hidden" with the last time anyone asked and the
	// trail said it happened twice.
	if t.ArchivedAt != 0 {
		return Event{}, codes.Errorf(codes.Conflict, "task is already archived")
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
	Title       *string
	Description *string
	Priority    *int64
	Validation  *[]Criterion
	Deps        *[]string
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
		if err := bound("title", title, MaxTitle); err != nil {
			return Event{}, err
		}
		t.Title, changed = title, append(changed, "title")
	}
	if p.Description != nil {
		if err := bound("description", *p.Description, MaxText); err != nil {
			return Event{}, err
		}
		t.Description, changed = *p.Description, append(changed, "description")
	}
	if p.Priority != nil {
		t.Priority, changed = *p.Priority, append(changed, "priority")
	}
	if p.Validation != nil {
		if err := boundCriteria(*p.Validation); err != nil {
			return Event{}, err
		}
		t.Validation, changed = *p.Validation, append(changed, "validation")
	}
	if p.Deps != nil {
		t.Deps, changed = *p.Deps, append(changed, "deps")
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

// sessionOf records §3.4's third fact for the field §6.6 turns on. Only an
// agent principal has an agent_session: a human and a declared principal
// (cron, trigger, plugin) have no pane and no session, and get "" so that
// CheckRecusal never compares them against an unresolved one.
//
// The two ways an agent can lack a session are NOT the same fact, and §3.4
// separates them. Herdr answering with no `agent_session` is an answer, and
// the third fact is then "null" — recorded as empty, because writing
// "unknown" there would put absence down as a value, which is exactly what
// §3.7 stopped doing for `human`. "unknown" is §3.4's stamp for a fact Herdr
// could NOT report, and the one signal that a snapshot never landed is the
// harness: `Daemon.actor` seeds "unknown" and only overwrites it from a reply
// Herdr actually gave. So an unresolved harness means an unresolved session,
// and §6.6's unknown-matches-unknown still recuses the pane that claimed
// during a Herdr blip and resumed elsewhere.
func sessionOf(a Actor) string {
	if a.Principal.Kind() != "agent" {
		return ""
	}
	if a.Session == "" {
		if a.Harness == "" || a.Harness == "unknown" {
			return "unknown"
		}
		return ""
	}
	return a.Session
}

// harnessOf is §3.4's "store unknown rather than guess": an agent principal
// whose harness Herdr could not report is recorded as unknown. It is a fact
// the row carries, not a recusal input — CheckRecusal compares the principal,
// the pane and the agent session, and never the harness (§6.6).
func harnessOf(a Actor) string {
	if a.IsHuman() {
		return ""
	}
	if a.Harness == "" {
		return "unknown"
	}
	return a.Harness
}

// boundTaskText applies §5.9 to the free text a task carries from creation.
func boundTaskText(title, description string, validation []Criterion) error {
	if err := bound("title", title, MaxTitle); err != nil {
		return err
	}
	if err := bound("description", description, MaxText); err != nil {
		return err
	}
	return boundCriteria(validation)
}

// boundCriteria applies §5.9 to an acceptance-criteria list.
func boundCriteria(list []Criterion) error {
	texts := make([]string, 0, len(list))
	for _, c := range list {
		texts = append(texts, c.Text)
	}
	return boundList("validation", texts, MaxItem, MaxItems)
}
