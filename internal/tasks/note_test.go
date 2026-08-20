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

// Promotion is the operator's call, not an agent's — the plugin exposes no way
// for a harness to turn its own idea into a commitment.
func TestPromoteIsHumanOnly(t *testing.T) {
	n := newNote(t)
	err := noteErr(NotePromote(n, agent("wF:p1", "claude"), "01ARZ3NDEKTSV4RRFFQ69G5FT1", t0+5))
	if got := codeOf(t, err); got != codes.Forbidden {
		t.Fatalf("agent promote code = %q, want FORBIDDEN", got)
	}
	if _, err := NotePromote(n, human, "01ARZ3NDEKTSV4RRFFQ69G5FT1", t0+6); err != nil {
		t.Fatalf("human promote: %v", err)
	}
	if n.Status != NoteTask || n.TaskID != "01ARZ3NDEKTSV4RRFFQ69G5FT1" {
		t.Fatalf("bad promote: %+v", n)
	}
	err = noteErr(NotePromote(n, human, "01ARZ3NDEKTSV4RRFFQ69G5FT2", t0+7))
	if got := codeOf(t, err); got != codes.Conflict {
		t.Fatalf("re-promote code = %q, want CONFLICT", got)
	}
}

func TestNoteKeepAndDropAreHumanOnly(t *testing.T) {
	n := newNote(t)
	if got := codeOf(t, noteErr(NoteDrop(n, agent("wF:p1", "claude"), "nah", t0+1))); got != codes.Forbidden {
		t.Fatalf("agent drop code = %q, want FORBIDDEN", got)
	}
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
	if err := CanHardDeleteNote(n); err != nil {
		t.Fatalf("an inbox note is deletable: %v", err)
	}
	if _, err := NoteDiscuss(n, agent("wF:p1", "claude"), t0+1); err != nil {
		t.Fatalf("discuss: %v", err)
	}
	if got := codeOf(t, CanHardDeleteNote(n)); got != codes.Conflict {
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
		{NoteTask, func(x *Note) error { return noteErr(NotePromote(x, human, "T1", t0+1)) }},
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
