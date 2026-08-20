package tasks

import (
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
