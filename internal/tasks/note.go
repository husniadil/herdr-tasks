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
	KindNoteEdited     = "edited"
	KindNotePromoted   = "promoted"
	KindNoteFolded     = "folded"
	KindNoteUnfolded   = "unfolded"
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
	// Folded says the note did not become this task on its own: its content
	// shipped inside a task another note was promoted into, or one that
	// already existed. It is a modifier of `task`, not a state beside it —
	// the note DID become work, and the field records which half of §14's
	// two ways it took there, so a fold can be undone and an origin cannot.
	Folded bool `json:"folded,omitempty"`
	// TaskProject is the board that task lives on. Empty means the note's own
	// project: a promotion may cross projects, and the id alone does not say
	// where to look for it.
	TaskProject string `json:"task_project,omitempty"`

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

// NoteUpdate fixes a note's wording. The author may correct their own words
// and the operator may correct anyone's — every note on a real board is an
// agent's, so an author-only rule would lock the operator out of all of them —
// but one agent does not rewrite another's note.
//
// The window is any note the operator has not decided yet. Not inbox-only: a
// note reaches `discussing` seconds after it is filed, and a window that shut
// there would be shut in practice. Once decided the body is frozen, because a
// promoted note's body is what the task was made from.
//
// The trail records that the body changed, not what it was: this is the same
// detail `task update` writes, and a note is edited to fix it, not to keep a
// copy of the mistake.
func NoteUpdate(n *Note, by Actor, body string, now int64) (Event, error) {
	if !by.IsHuman() && by.Principal != n.Author {
		return Event{}, codes.Errorf(codes.Forbidden,
			"note #%d belongs to %s; its author or the operator edits it", n.Seq, n.Author)
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return Event{}, codes.New(codes.Usage, "body cannot be emptied; an unwanted note is dropped or deleted (§5.7)")
	}
	if err := bound("body", body, MaxText); err != nil {
		return Event{}, err
	}
	if n.Status.Terminal() {
		return Event{}, codes.Errorf(codes.Conflict, "note is %s; its wording is what was decided on", n.Status)
	}
	n.Body = body
	n.UpdatedAt = now
	return Event{Kind: KindNoteEdited, Actor: by.Principal, At: now,
		Detail: map[string]any{"changed": []string{"body"}}}, nil
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

// NotePromote turns a note into a task. The authority is the operator's, and
// since 0.10.0 that is advice rather than a refusal (§3.7): an agent confirms
// with the user and then promotes, and the event says the agent did it.
//
// taskProject is the board the task was created on. It may be another
// project's: the note stays where it was filed and points across, so the
// provenance survives work that belongs to a different repository. It is
// recorded only when it differs from the note's own project, so the common
// case reads the way it always did.
func NotePromote(n *Note, by Actor, taskID, taskProject string, now int64) (Event, error) {
	if n.Status.Terminal() {
		return Event{}, codes.Errorf(codes.Conflict, "note is %s", n.Status)
	}
	n.Status = NoteTask
	n.TaskID = taskID
	n.TaskProject = ""
	n.Folded = false
	detail := map[string]any{"task_id": taskID}
	if taskProject != "" && taskProject != n.Project {
		n.TaskProject = taskProject
		detail["task_project"] = taskProject
	}
	n.UpdatedAt = now
	return Event{Kind: KindNotePromoted, Actor: by.Principal, At: now,
		Detail: operatorVerb(by, detail)}, nil
}

// NoteFold points a note at a task that already exists, or at the task
// another note is being promoted into. Its authority is NotePromote's, and so
// is its advisory shape: a note becoming work is the operator's decision, and
// folding is that decision made about a second note.
//
// It reuses `task` rather than adding a seventh state. `task` already means
// "this became work"; a folded note became work too, in a task whose scope
// came from somewhere else, and a state beside it would overlap it in every
// way that matters to a reader of `note list`: both are decided, neither is
// undecided, and neither is rejected.
//
// holder is how the caller names the task a note is already on, for the
// refusal. A note whose own task exists is never silently repointed: the
// operator either meant a different note, or wants the fold undone first.
func NoteFold(n *Note, by Actor, taskID, taskProject, holder string, now int64) (Event, error) {
	if n.Status == NoteTask {
		if holder == "" {
			holder = n.TaskID
		}
		return Event{}, codes.Errorf(codes.Conflict,
			"note #%d is already task %s; unfold it before folding it elsewhere", n.Seq, holder)
	}
	if n.Status.Terminal() {
		return Event{}, codes.Errorf(codes.Conflict, "note is %s", n.Status)
	}
	n.Status = NoteTask
	n.TaskID = taskID
	n.TaskProject = ""
	n.Folded = true
	detail := map[string]any{"task_id": taskID}
	if taskProject != "" && taskProject != n.Project {
		n.TaskProject = taskProject
		detail["task_project"] = taskProject
	}
	n.UpdatedAt = now
	return Event{Kind: KindNoteFolded, Actor: by.Principal, At: now,
		Detail: operatorVerb(by, detail)}, nil
}

// NoteUnfold is the way back from a fold that was a mistake, without deleting
// the row: the note returns to the inbox, undecided again and promotable on
// its own. The triage it already had — its verdict and reason — stays, because
// the fold is the only thing being undone.
//
// A promoted note does not unfold. The task was made from its body, so there
// is nothing to return it to that would not leave the task without the note it
// came from; that mistake is undone by cancelling the task.
func NoteUnfold(n *Note, by Actor, now int64) (Event, error) {
	if n.Status != NoteTask {
		return Event{}, codes.Errorf(codes.Conflict, "note is %s, not on a task", n.Status)
	}
	if !n.Folded {
		return Event{}, codes.Errorf(codes.Conflict,
			"note #%d is what task %s was made from; cancel the task rather than unfolding its origin", n.Seq, n.TaskID)
	}
	was := n.TaskID
	n.Status = NoteInbox
	n.TaskID, n.TaskProject, n.Folded = "", "", false
	n.UpdatedAt = now
	return Event{Kind: KindNoteUnfolded, Actor: by.Principal, At: now,
		Detail: operatorVerb(by, map[string]any{"task_id": was})}, nil
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
		Detail: operatorVerb(by, map[string]any{"reason": n.Reason})}, nil
}

// CanHardDeleteNote answers §5.7 for notes: only a note still in inbox may be
// removed for good, and only its author or the operator may remove it.
//
// The principal half mirrors NoteUpdate's exactly, because destroying a note
// and its whole event trail cannot be easier than fixing a typo in it — and it
// was: hard delete took no principal at all, so any agent could delete a note
// it was FORBIDDEN from editing.
func CanHardDeleteNote(n *Note, by Actor) error {
	if !by.IsHuman() && by.Principal != n.Author {
		return codes.Errorf(codes.Forbidden,
			"note #%d belongs to %s; its author or the operator deletes it", n.Seq, n.Author)
	}
	if n.Status != NoteInbox {
		return codes.Errorf(codes.Conflict, "note is %s; only an inbox note is deleted (§5.7)", n.Status)
	}
	return nil
}
