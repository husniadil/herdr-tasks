package tasks

import (
	"strings"
	"testing"

	"github.com/husniadil/herdr-tasks/internal/codes"
)

func newNote(t *testing.T) *Note {
	t.Helper()
	n, _, err := NewNote(NewNoteInput{ID: "01ARZ3NDEKTSV4RRFFQ69G5FB0", Project: "/p", Body: "the sweep logs nothing"}, agent("wF:p1", "claude"), t0)
	if err != nil {
		t.Fatalf("NewNote: %v", err)
	}
	return n
}

func noteErr(_ Event, err error) error { return err }

func TestNewNoteStartsInInbox(t *testing.T) {
	n := newNote(t)
	if n.Status != NoteInbox {
		t.Fatalf("status = %q, want inbox", n.Status)
	}
	if n.Author != "agent:wF:p1" {
		t.Fatalf("author = %q", n.Author)
	}
}

func TestNewNoteRejectsEmptyBody(t *testing.T) {
	_, _, err := NewNote(NewNoteInput{ID: "x", Project: "/p", Body: "\n"}, human, t0)
	if got := codeOf(t, err); got != codes.Usage {
		t.Fatalf("code = %q, want USAGE", got)
	}
}

// inbox → discussing → needs_input → proposed, and a verdict may be amended
// until the operator acts.
func TestNoteDiscussionFlow(t *testing.T) {
	n := newNote(t)
	a := agent("wF:p1", "claude")
	if _, err := NoteDiscuss(n, a, t0+1); err != nil {
		t.Fatalf("discuss: %v", err)
	}
	if n.Status != NoteDiscussing {
		t.Fatalf("status = %q", n.Status)
	}
	if _, err := NoteAskInput(n, a, "which project owns this?", t0+2); err != nil {
		t.Fatalf("needs_input: %v", err)
	}
	if n.Status != NoteNeedsInput || n.Question != "which project owns this?" {
		t.Fatalf("bad needs_input: %+v", n)
	}
	if _, err := NoteVerdict(n, a, VerdictTask, "worth a task", t0+3); err != nil {
		t.Fatalf("verdict: %v", err)
	}
	if n.Status != NoteProposed || n.Verdict != VerdictTask {
		t.Fatalf("bad verdict: %+v", n)
	}
	// Amend while the operator has not acted.
	if _, err := NoteVerdict(n, a, VerdictDrop, "on reflection, no", t0+4); err != nil {
		t.Fatalf("amend: %v", err)
	}
	if n.Verdict != VerdictDrop {
		t.Fatalf("verdict = %q", n.Verdict)
	}
}

func TestNoteVerdictRequiresDiscussion(t *testing.T) {
	n := newNote(t)
	err := noteErr(NoteVerdict(n, agent("wF:p1", "claude"), VerdictKeep, "", t0+1))
	if got := codeOf(t, err); got != codes.Conflict {
		t.Fatalf("code = %q, want CONFLICT — a verdict comes out of a live discussion", got)
	}
}

func TestNoteVerdictRejectsUnknownValue(t *testing.T) {
	n := newNote(t)
	if _, err := NoteDiscuss(n, agent("wF:p1", "claude"), t0+1); err != nil {
		t.Fatalf("discuss: %v", err)
	}
	err := noteErr(NoteVerdict(n, agent("wF:p1", "claude"), "maybe", "", t0+2))
	if got := codeOf(t, err); got != codes.Usage {
		t.Fatalf("code = %q, want USAGE", got)
	}
}

// markedAgent asserts §3.7's two halves on one operator-verb event: the actor
// recorded is the agent that called, and the event says an operator verb was
// reached by someone who is not the operator. A mutation that writes
// PrincipalHuman instead of by.Principal fails the first half; one that drops
// the mark fails the second.
func markedAgent(t *testing.T, e Event, want Principal) {
	t.Helper()
	if e.Actor != want {
		t.Fatalf("actor = %q, want %q: the trail names who acted, never the operator (§3.7)", e.Actor, want)
	}
	if e.Actor.Kind() == "human" {
		t.Fatalf("actor = %q: an agent's operator verb must never be filed under the operator", e.Actor)
	}
	if e.Detail[OnBehalfOfOperator] != true {
		t.Fatalf("detail = %v, want %s: an operator verb an agent performed labels itself", e.Detail, OnBehalfOfOperator)
	}
}

// §3.7 (0.10.0): note.promote is the operator's authority and an agent that
// confirmed with the user performs it. NotePromote note.go: the refusal
// this replaces returned FORBIDDEN to every agent.
func TestPromoteByAnAgentSucceedsAndIsMarked(t *testing.T) {
	n := newNote(t)
	ag := agent("wF:p1", "claude")
	e, err := NotePromote(n, ag, "01ARZ3NDEKTSV4RRFFQ69G5FT1", "", t0+5)
	if err != nil {
		t.Fatalf("agent promote: %v", err)
	}
	if n.Status != NoteTask || n.TaskID != "01ARZ3NDEKTSV4RRFFQ69G5FT1" {
		t.Fatalf("bad promote: %+v", n)
	}
	markedAgent(t, e, ag.Principal)
	if e.Detail["task_id"] != "01ARZ3NDEKTSV4RRFFQ69G5FT1" {
		t.Fatalf("detail = %v: the mark is added beside what the event already said", e.Detail)
	}
}

// The operator's own promotion carries no mark: the mark says "someone other
// than the operator did this", so writing it for the operator would say
// nothing.
func TestPromoteByTheOperatorIsNotMarked(t *testing.T) {
	n := newNote(t)
	e, err := NotePromote(n, human, "01ARZ3NDEKTSV4RRFFQ69G5FT1", "", t0+5)
	if err != nil {
		t.Fatalf("human promote: %v", err)
	}
	if _, ok := e.Detail[OnBehalfOfOperator]; ok {
		t.Fatalf("detail = %v: the operator acting on their own authority is unremarkable", e.Detail)
	}
}

func TestPromoteIsOncePerNote(t *testing.T) {
	n := newNote(t)
	if _, err := NotePromote(n, human, "01ARZ3NDEKTSV4RRFFQ69G5FT1", "", t0+6); err != nil {
		t.Fatalf("human promote: %v", err)
	}
	if n.Status != NoteTask || n.TaskID != "01ARZ3NDEKTSV4RRFFQ69G5FT1" {
		t.Fatalf("bad promote: %+v", n)
	}
	err := noteErr(NotePromote(n, human, "01ARZ3NDEKTSV4RRFFQ69G5FT2", "", t0+7))
	if got := codeOf(t, err); got != codes.Conflict {
		t.Fatalf("re-promote code = %q, want CONFLICT", got)
	}
}

// §3.7 (0.10.0): both verdicts `decide` carries — note keep and note drop —
// are reached by an agent and marked. The refusal this replaces sent every
// agent to `note verdict` instead.
func TestNoteKeepAndDropByAnAgentSucceedAndAreMarked(t *testing.T) {
	ag := agent("wF:p1", "claude")

	dropped := newNote(t)
	e, err := NoteDrop(dropped, ag, "nah", t0+1)
	if err != nil {
		t.Fatalf("agent drop: %v", err)
	}
	if dropped.Status != NoteDropped {
		t.Fatalf("status = %q, want %q", dropped.Status, NoteDropped)
	}
	markedAgent(t, e, ag.Principal)

	kept := newNote(t)
	e, err = NoteKeep(kept, ag, "later", t0+1)
	if err != nil {
		t.Fatalf("agent keep: %v", err)
	}
	if kept.Status != NoteKept {
		t.Fatalf("status = %q, want %q", kept.Status, NoteKept)
	}
	markedAgent(t, e, ag.Principal)
}

func TestNoteKeepAndDropStayTerminal(t *testing.T) {
	n := newNote(t)
	if _, err := NoteDrop(n, human, "not now", t0+2); err != nil {
		t.Fatalf("human drop: %v", err)
	}
	if n.Status != NoteDropped {
		t.Fatalf("status = %q", n.Status)
	}
	other := newNote(t)
	if _, err := NoteKeep(other, human, "later", t0+3); err != nil {
		t.Fatalf("keep: %v", err)
	}
	if other.Status != NoteKept {
		t.Fatalf("status = %q", other.Status)
	}
}

// §5.7: a note that never left inbox may be removed for good; a triaged one
// carries a decision and is only ever dropped.
func TestNoteHardDeleteOnlyFromInbox(t *testing.T) {
	n := newNote(t)
	if err := CanHardDeleteNote(n, human); err != nil {
		t.Fatalf("an inbox note is deletable: %v", err)
	}
	if _, err := NoteDiscuss(n, agent("wF:p1", "claude"), t0+1); err != nil {
		t.Fatalf("discuss: %v", err)
	}
	if got := codeOf(t, CanHardDeleteNote(n, human)); got != codes.Conflict {
		t.Fatalf("code = %q, want CONFLICT", got)
	}
}

// A terminal note is done being triaged; a stray agent cannot reopen it.
func TestTerminalNoteRejectsDiscussion(t *testing.T) {
	n := newNote(t)
	if _, err := NoteDrop(n, human, "no", t0+1); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if got := codeOf(t, noteErr(NoteDiscuss(n, agent("wF:p1", "claude"), t0+2))); got != codes.Conflict {
		t.Fatalf("code = %q, want CONFLICT", got)
	}
}

// §5.6 / §5.9: a note's wording can be fixed, by the person whose words they
// are or by the operator, until the operator has decided it. What is refused
// matters as much as what is allowed: one agent rewriting another agent's note
// is the case this rule exists for.
func TestNoteUpdateIsTheAuthorsOrTheOperators(t *testing.T) {
	author := agent("wF:p1", "claude")
	other := agent("wF:p2", "codex")

	n := newNote(t)
	ev, err := NoteUpdate(n, author, "the sweep logs nothing at all", t0+1)
	if err != nil {
		t.Fatalf("the author could not fix their own note: %v", err)
	}
	if n.Body != "the sweep logs nothing at all" {
		t.Fatalf("body = %q", n.Body)
	}
	if ev.Kind != KindNoteEdited || ev.Actor != author.Principal {
		t.Fatalf("event = %+v, want an %q by the author", ev, KindNoteEdited)
	}
	if n.UpdatedAt != t0+1 {
		t.Fatalf("updated_at = %d, want the edit's clock", n.UpdatedAt)
	}

	// The operator did not write it and edits it anyway: every note on a real
	// board is an agent's, so an author-only rule would lock them out of all
	// of them.
	if err := noteErr(NoteUpdate(newNote(t), human, "the operator's wording", t0+2)); err != nil {
		t.Fatalf("the operator could not fix an agent's note: %v", err)
	}
	// A different agent may not.
	if got := codeOf(t, noteErr(NoteUpdate(newNote(t), other, "not yours", t0+2))); got != codes.Forbidden {
		t.Fatalf("another agent's edit was %s, want FORBIDDEN", got)
	}

	// Decided is frozen: a promoted note's body is what the task was made
	// from, and a dropped one is the record of what was turned down.
	for _, decide := range []struct {
		to  NoteStatus
		run func(*Note) error
	}{
		{NoteKept, func(x *Note) error { return noteErr(NoteKeep(x, human, "not now", t0+1)) }},
		{NoteDropped, func(x *Note) error { return noteErr(NoteDrop(x, human, "no", t0+1)) }},
		{NoteTask, func(x *Note) error { return noteErr(NotePromote(x, human, "T1", "", t0+1)) }},
	} {
		x := newNote(t)
		if err := decide.run(x); err != nil {
			t.Fatalf("%s: %v", decide.to, err)
		}
		if got := codeOf(t, noteErr(NoteUpdate(x, human, "too late", t0+2))); got != codes.Conflict {
			t.Fatalf("editing a %s note was %s, want CONFLICT", decide.to, got)
		}
	}
	// Mid-triage is still open: a typo the operator spots while an agent reads
	// it is exactly when they want to fix it.
	x := newNote(t)
	if err := noteErr(NoteDiscuss(x, author, t0+1)); err != nil {
		t.Fatalf("discuss: %v", err)
	}
	if err := noteErr(NoteUpdate(x, human, "fixed mid-discussion", t0+2)); err != nil {
		t.Fatalf("editing a note under discussion: %v", err)
	}
}

// §5.9: the body is bounded at write time, and emptying it is not an edit —
// it is a delete by other means, and §5.7 says how a note is deleted.
func TestNoteUpdateRefusesAnEmptyOrOversizedBody(t *testing.T) {
	for _, body := range []string{"", "   ", "\t\n "} {
		if got := codeOf(t, noteErr(NoteUpdate(newNote(t), human, body, t0+1))); got != codes.Usage {
			t.Fatalf("emptying the body with %q was %s, want USAGE", body, got)
		}
	}
	n := newNote(t)
	before := n.Body
	err := noteErr(NoteUpdate(n, human, "x", t0+1))
	if err != nil {
		t.Fatalf("a one-character body: %v", err)
	}
	n = newNote(t)
	err = noteErr(NoteUpdate(n, human, string(make([]rune, MaxText+1)), t0+1))
	if codeOf(t, err) != codes.Usage {
		t.Fatalf("an oversized body was %s, want USAGE", codeOf(t, err))
	}
	if msg := err.Error(); !strings.Contains(msg, "body") || !strings.Contains(msg, "20000") {
		t.Fatalf("the refusal does not name the field and the limit: %q", msg)
	}
	if n.Body != before {
		t.Fatalf("a refused edit changed the note anyway: %q", n.Body)
	}
}

// §5.7 with §3.1: deleting a note is more destructive than editing one, so it
// cannot be easier. note.update already refuses an agent that is not the
// author; hard delete took no principal at all, which meant any agent could
// destroy a rival's note and its whole event trail while being refused a typo
// fix on the same row.
func TestOnlyTheAuthorOrTheOperatorDeletesANote(t *testing.T) {
	n := newNote(t)
	rival := agent("wF:p2", "codex")
	if err := CanHardDeleteNote(n, rival); err == nil {
		t.Fatal("a rival agent may not delete another agent's note")
	} else if got := codeOf(t, err); got != codes.Forbidden {
		t.Fatalf("code = %q, want FORBIDDEN", got)
	}
	if err := CanHardDeleteNote(n, agent("wF:p1", "claude")); err != nil {
		t.Fatalf("the author may delete their own note: %v", err)
	}
	if err := CanHardDeleteNote(n, human); err != nil {
		t.Fatalf("the operator may delete any note: %v", err)
	}
	// The status rule still comes first for the author.
	n.Status = NoteTask
	if err := CanHardDeleteNote(n, human); err == nil {
		t.Fatal("only an inbox note is deleted (§5.7)")
	} else if got := codeOf(t, err); got != codes.Conflict {
		t.Fatalf("code = %q, want CONFLICT", got)
	}
}

// §14: a note folded into another note's task ends in `task`, the same state
// the promoted note reaches, and points at the task that carries it.
func TestNoteFoldEndsTheNoteOnTheTaskThatCarriesIt(t *testing.T) {
	n := newNote(t)
	if _, err := NoteFold(n, human, "01ARZ3NDEKTSV4RRFFQ69G5FT1", "", "", t0+1); err != nil {
		t.Fatalf("fold: %v", err)
	}
	if n.Status != NoteTask {
		t.Fatalf("status = %q, want task", n.Status)
	}
	if n.TaskID != "01ARZ3NDEKTSV4RRFFQ69G5FT1" {
		t.Fatalf("task_id = %q", n.TaskID)
	}
	if !n.Folded {
		t.Fatal("folded = false; a folded note is not the task's origin and says so")
	}
}

// §3.7 (0.10.0): folding follows promotion onto the agent's side of the line.
func TestNoteFoldByAnAgentSucceedsAndIsMarked(t *testing.T) {
	n := newNote(t)
	ag := agent("wF:p1", "claude")
	e, err := NoteFold(n, ag, "01ARZ3NDEKTSV4RRFFQ69G5FT1", "", "", t0+1)
	if err != nil {
		t.Fatalf("agent fold: %v", err)
	}
	if n.Status != NoteTask || !n.Folded {
		t.Fatalf("bad fold: %+v", n)
	}
	markedAgent(t, e, ag.Principal)
}

// The decision this task had to take: a note whose own task exists is refused
// rather than silently repointed, and the refusal names the task holding it.
func TestNoteFoldRefusesANoteAnotherTaskAlreadyHolds(t *testing.T) {
	n := newNote(t)
	if _, err := NotePromote(n, human, "01ARZ3NDEKTSV4RRFFQ69G5FT1", "", t0+1); err != nil {
		t.Fatalf("promote: %v", err)
	}
	err := noteErr(NoteFold(n, human, "01ARZ3NDEKTSV4RRFFQ69G5FT2", "", "#42", t0+2))
	if got := codeOf(t, err); got != codes.Conflict {
		t.Fatalf("code = %q, want CONFLICT", got)
	}
	if !strings.Contains(err.Error(), "#42") {
		t.Fatalf("refusal %q does not name the task holding the note", err)
	}
	if n.TaskID != "01ARZ3NDEKTSV4RRFFQ69G5FT1" {
		t.Fatalf("task_id moved to %q; the refusal must leave the note where it was", n.TaskID)
	}
}

func TestNoteFoldRefusesADecidedNote(t *testing.T) {
	n := newNote(t)
	if _, err := NoteKeep(n, human, "later", t0+1); err != nil {
		t.Fatalf("keep: %v", err)
	}
	if got := codeOf(t, noteErr(NoteFold(n, human, "01ARZ3NDEKTSV4RRFFQ69G5FT1", "", "", t0+2))); got != codes.Conflict {
		t.Fatalf("code = %q, want CONFLICT", got)
	}
}

// The way back: a fold is undone without deleting the row, and the note is
// promotable on its own afterwards.
func TestNoteUnfoldReturnsTheNoteToTheBoard(t *testing.T) {
	n := newNote(t)
	if _, err := NoteVerdict(NoteDiscussed(t, n), human, VerdictTask, "worth doing", t0+2); err != nil {
		t.Fatalf("verdict: %v", err)
	}
	if _, err := NoteFold(n, human, "01ARZ3NDEKTSV4RRFFQ69G5FT1", "", "", t0+3); err != nil {
		t.Fatalf("fold: %v", err)
	}
	if _, err := NoteUnfold(n, human, t0+4); err != nil {
		t.Fatalf("unfold: %v", err)
	}
	if n.Status != NoteInbox || n.TaskID != "" || n.Folded {
		t.Fatalf("bad unfold: %+v", n)
	}
	if n.Verdict != VerdictTask {
		t.Fatal("unfold threw the triage away; only the fold is undone")
	}
	if _, err := NotePromote(n, human, "01ARZ3NDEKTSV4RRFFQ69G5FT2", "", t0+5); err != nil {
		t.Fatalf("promote after unfold: %v", err)
	}
}

// A promoted note is the task's origin — the task was made from its body — so
// it does not unfold. Only a fold is undone.
func TestNoteUnfoldRefusesTheNoteTheTaskWasMadeFrom(t *testing.T) {
	n := newNote(t)
	if _, err := NotePromote(n, human, "01ARZ3NDEKTSV4RRFFQ69G5FT1", "", t0+1); err != nil {
		t.Fatalf("promote: %v", err)
	}
	if got := codeOf(t, noteErr(NoteUnfold(n, human, t0+2))); got != codes.Conflict {
		t.Fatalf("code = %q, want CONFLICT", got)
	}
}

// §3.7 (0.10.0): undoing a fold is the same authority as making one, so it
// moves with it.
func TestNoteUnfoldByAnAgentSucceedsAndIsMarked(t *testing.T) {
	n := newNote(t)
	ag := agent("wF:p1", "claude")
	if _, err := NoteFold(n, human, "01ARZ3NDEKTSV4RRFFQ69G5FT1", "", "", t0+1); err != nil {
		t.Fatalf("fold: %v", err)
	}
	e, err := NoteUnfold(n, ag, t0+2)
	if err != nil {
		t.Fatalf("agent unfold: %v", err)
	}
	if n.Status != NoteInbox {
		t.Fatalf("status = %q, want %q", n.Status, NoteInbox)
	}
	markedAgent(t, e, ag.Principal)
}

// The five sites §3.7 did NOT move, held here so removing one fails a test.
// note.go NoteUpdate and CanHardDeleteNote are AUTHORSHIP, not operator
// authority: an agent rewriting or destroying another agent's note is wrong
// however the operator answers, so no confirmation makes it right.
func TestAnotherAgentStillCannotEditOrDeleteANote(t *testing.T) {
	n := newNote(t)
	n.Author = Principal("agent:wF:p9")
	rival := agent("wF:p1", "claude")

	if got := codeOf(t, noteErr(NoteUpdate(n, rival, "my words instead", t0+1))); got != codes.Forbidden {
		t.Fatalf("rival edit code = %q, want FORBIDDEN: authorship is not the operator's to grant away", got)
	}
	if got := codeOf(t, CanHardDeleteNote(n, rival)); got != codes.Forbidden {
		t.Fatalf("rival delete code = %q, want FORBIDDEN: erasing a peer's idea is wrong however the operator answers", got)
	}
	// The author still reaches both, which is what makes the refusal about
	// authorship rather than about being an agent.
	if _, err := NoteUpdate(n, agent("wF:p9", "claude"), "my own words", t0+2); err != nil {
		t.Fatalf("author edit: %v", err)
	}
	if err := CanHardDeleteNote(n, agent("wF:p9", "claude")); err != nil {
		t.Fatalf("author delete: %v", err)
	}
}

// NoteDiscussed opens triage on a note so a verdict can be recorded on it.
func NoteDiscussed(t *testing.T, n *Note) *Note {
	t.Helper()
	if _, err := NoteDiscuss(n, human, t0+1); err != nil {
		t.Fatalf("discuss: %v", err)
	}
	return n
}

// §5: a re-opened discussion carries no verdict, and a reason is half of a
// verdict. NoteDiscuss cleared the verdict and the question and left the
// reason behind, so a note back in triage still held "not worth doing" with
// nothing that had proposed it — invisible in `note get`, which prints a
// reason only beside its verdict, but shipped over --json and still matched by
// `note list --query`, which searches the body and the verdict reason.
func TestReopeningTriageLeavesNoReasonBehind(t *testing.T) {
	n := newNote(t)
	if _, err := NoteVerdict(NoteDiscussed(t, n), human, VerdictDrop, "not worth doing", t0+2); err != nil {
		t.Fatalf("verdict: %v", err)
	}
	if n.Reason == "" {
		t.Fatal("the verdict recorded no reason, so this proves nothing")
	}
	if _, err := NoteDiscuss(n, human, t0+3); err != nil {
		t.Fatalf("discuss: %v", err)
	}
	if n.Verdict != "" || n.Question != "" || n.Reason != "" {
		t.Errorf("re-opened triage kept verdict %q question %q reason %q",
			n.Verdict, n.Question, n.Reason)
	}
}
