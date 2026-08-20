package tasks

import (
	"strings"

	"github.com/husniadil/herdr-tasks/internal/codes"
)

// NoteStatus is a note's place on the board: a pre-decision idea that becomes
// a task, is kept, or is dropped (§14).
type NoteStatus string

const (
	NoteInbox      NoteStatus = "inbox"
	NoteDiscussing NoteStatus = "discussing"
	NoteNeedsInput NoteStatus = "needs_input"
	NoteProposed   NoteStatus = "proposed"
	NoteKept       NoteStatus = "keep"
	NoteTask       NoteStatus = "task"
	NoteDropped    NoteStatus = "dropped"
)

// Terminal reports whether the operator has already decided this note.
func (s NoteStatus) Terminal() bool {
	return s == NoteKept || s == NoteTask || s == NoteDropped
}

// Verdict is what a triaging agent proposes at the end of a discussion. It is
// a proposal, never the decision: only the operator moves a note off the board.
type Verdict string

const (
	VerdictTask Verdict = "task"
	VerdictKeep Verdict = "keep"
	VerdictDrop Verdict = "drop"
)

func (v Verdict) valid() bool {
	return v == VerdictTask || v == VerdictKeep || v == VerdictDrop
}

// Note event kinds.
const (
	KindNoteAdded      = "added"
	KindNoteDiscussing = "discussing"
	KindNoteNeedsInput = "needs_input"
	KindNoteProposed   = "proposed"
	KindNotePromoted   = "promoted"
	KindNoteKept       = "kept"
	KindNoteDropped    = "dropped"
)

// Note is a pre-decision idea awaiting a human call.
type Note struct {
	ID      string     `json:"id"`
	Seq     int64      `json:"seq"`
	Project string     `json:"project"`
	Body    string     `json:"body"`
	Status  NoteStatus `json:"status"`

	Author        Principal `json:"author"`
	AuthorName    string    `json:"author_name,omitempty"`
	AuthorHarness string    `json:"author_harness,omitempty"`

	Verdict Verdict `json:"verdict,omitempty"`
	Reason  string  `json:"reason,omitempty"`
	// Question is what the triaging agent is blocked on in needs_input.
	Question string `json:"question,omitempty"`
	// TaskID is the task this note became, once the operator promoted it.
	TaskID string `json:"task_id,omitempty"`

	CreatedAt int64 `json:"created_at"`
	UpdatedAt int64 `json:"updated_at"`

	PaneID string `json:"pane_id,omitempty"`
}

// NewNoteInput is everything a caller may set when filing a note.
type NewNoteInput struct {
	ID      string
	Seq     int64
	Project string
	Body    string
	PaneID  string
}

// NewNote files a note into the inbox.
func NewNote(in NewNoteInput, by Actor, now int64) (*Note, Event, error) {
	body := strings.TrimSpace(in.Body)
	if body == "" {
		return nil, Event{}, codes.New(codes.Usage, "body is required")
	}
	if in.Project == "" {
		return nil, Event{}, codes.New(codes.Usage, "project is required")
	}
	if err := bound("body", body, MaxText); err != nil {
		return nil, Event{}, err
	}
	n := &Note{
		ID:            in.ID,
		Seq:           in.Seq,
		Project:       in.Project,
		Body:          body,
		Status:        NoteInbox,
		Author:        by.Principal,
		AuthorName:    by.Name,
		AuthorHarness: harnessOf(by),
		CreatedAt:     now,
		UpdatedAt:     now,
		PaneID:        in.PaneID,
	}
	return n, Event{Kind: KindNoteAdded, Actor: by.Principal, At: now}, nil
}

// NoteDiscuss opens or re-opens triage. A note re-enters discussion from any
// non-terminal state, which is also how a discussion abandoned by a dead pane
// is recovered.
func NoteDiscuss(n *Note, by Actor, now int64) (Event, error) {
	if n.Status.Terminal() {
		return Event{}, codes.Errorf(codes.Conflict, "note is %s", n.Status)
	}
	n.Status = NoteDiscussing
	n.Verdict, n.Question = "", ""
	n.UpdatedAt = now
	return Event{Kind: KindNoteDiscussing, Actor: by.Principal, At: now}, nil
}

// NoteAskInput parks a discussion on the operator.
func NoteAskInput(n *Note, by Actor, question string, now int64) (Event, error) {
	if n.Status != NoteDiscussing {
		return Event{}, codes.Errorf(codes.Conflict, "note is %s, not discussing", n.Status)
	}
	if err := bound("question", question, MaxText); err != nil {
		return Event{}, err
	}
	n.Status = NoteNeedsInput
	n.Question = strings.TrimSpace(question)
	n.UpdatedAt = now
	return Event{Kind: KindNoteNeedsInput, Actor: by.Principal, At: now,
		Detail: map[string]any{"question": n.Question}}, nil
}

// NoteVerdict ends a discussion with a proposal. Accepting `proposed` again is
// the amend window: the triaging agent may correct itself right up until the
// operator acts, and every operator action moves the note off proposed.
func NoteVerdict(n *Note, by Actor, v Verdict, reason string, now int64) (Event, error) {
	if !v.valid() {
		return Event{}, codes.Errorf(codes.Usage, "unknown verdict %q; want task, keep, or drop", v)
	}
	if err := bound("reason", reason, MaxText); err != nil {
		return Event{}, err
	}
	switch n.Status {
	case NoteDiscussing, NoteNeedsInput, NoteProposed:
	default:
		return Event{}, codes.Errorf(codes.Conflict, "note is %s; a verdict comes out of a live discussion", n.Status)
	}
	n.Status = NoteProposed
	n.Verdict = v
	n.Reason = strings.TrimSpace(reason)
	n.Question = ""
	n.UpdatedAt = now
	return Event{Kind: KindNoteProposed, Actor: by.Principal, At: now,
		Detail: map[string]any{"verdict": string(v), "reason": n.Reason}}, nil
}

// NotePromote turns a note into a task. Human-only: a note becoming a
// commitment is the operator's decision, and an agent proposing its own
// promotion is exactly the loop this rule exists to break.
func NotePromote(n *Note, by Actor, taskID string, now int64) (Event, error) {
	if !by.IsHuman() {
		return Event{}, codes.New(codes.Forbidden, "only the operator promotes a note")
	}
	if n.Status.Terminal() {
		return Event{}, codes.Errorf(codes.Conflict, "note is %s", n.Status)
	}
	n.Status = NoteTask
	n.TaskID = taskID
	n.UpdatedAt = now
	return Event{Kind: KindNotePromoted, Actor: by.Principal, At: now,
		Detail: map[string]any{"task_id": taskID}}, nil
}

// NoteKeep files a note as approved but not now.
func NoteKeep(n *Note, by Actor, reason string, now int64) (Event, error) {
	return decide(n, by, NoteKept, KindNoteKept, reason, now)
}

// NoteDrop rejects a note.
func NoteDrop(n *Note, by Actor, reason string, now int64) (Event, error) {
	return decide(n, by, NoteDropped, KindNoteDropped, reason, now)
}

func decide(n *Note, by Actor, to NoteStatus, kind, reason string, now int64) (Event, error) {
	if !by.IsHuman() {
		return Event{}, codes.Errorf(codes.Forbidden, "only the operator decides a note; propose %s with note verdict instead", to)
	}
	if err := bound("reason", reason, MaxText); err != nil {
		return Event{}, err
	}
	if n.Status.Terminal() {
		return Event{}, codes.Errorf(codes.Conflict, "note is %s", n.Status)
	}
	n.Status = to
	if r := strings.TrimSpace(reason); r != "" {
		n.Reason = r
	}
	n.UpdatedAt = now
	return Event{Kind: kind, Actor: by.Principal, At: now,
		Detail: map[string]any{"reason": n.Reason}}, nil
}

// CanHardDeleteNote answers §5.7 for notes: only a note still in inbox may be
// removed for good.
func CanHardDeleteNote(n *Note) error {
	if n.Status != NoteInbox {
		return codes.Errorf(codes.Conflict, "note is %s; only an inbox note is deleted (§5.7)", n.Status)
	}
	return nil
}
