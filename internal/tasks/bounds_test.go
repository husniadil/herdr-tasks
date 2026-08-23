package tasks

import (
	"strings"
	"testing"

	"github.com/husniadil/herdr-tasks/internal/codes"
)

// §5.9: a verb that accepts free text enforces a documented length bound with
// USAGE at write time. The numbers live in the README; these tests hold the
// behaviour, and name a number only where the number is the point.

func over(n int) string    { return strings.Repeat("x", n+1) }
func atLimit(n int) string { return strings.Repeat("x", n) }

// §5.9: the message names the field and the limit, so a caller can fix the
// call without reading the source.
func TestTitleBoundNamesTheFieldAndTheLimit(t *testing.T) {
	_, _, err := New(NewTaskInput{Project: "/p", Title: over(MaxTitle)}, human, t0)
	if got := codeOf(t, err); got != codes.Usage {
		t.Fatalf("code = %s, want %s", got, codes.Usage)
	}
	msg := err.Error()
	if !strings.Contains(msg, "title") {
		t.Fatalf("the message must name the field: %q", msg)
	}
	if !strings.Contains(msg, "200") {
		t.Fatalf("the message must name the limit: %q", msg)
	}
}

// §5.9: generous for real use. When these numbers were chosen the longest
// title in real use was 86 characters, the longest description 747, the
// longest report 2,225 and the longest verdict reason 7,902.
func TestBoundsAcceptWhatRealUseLooksLike(t *testing.T) {
	task, _, err := New(NewTaskInput{Project: "/p", Title: atLimit(MaxTitle),
		Description: atLimit(MaxText), Validation: []Criterion{{Text: atLimit(MaxItem)}}}, human, t0)
	if err != nil {
		t.Fatalf("input at the limit was refused: %v", err)
	}
	if _, err := Claim(task, agent("wF:p1", "claude"), t0+1, 900_000); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := Submit(task, agent("wF:p1", "claude"), atLimit(MaxText),
		[]string{atLimit(MaxItem)}, nil, t0+2); err != nil {
		t.Fatalf("a submission at the limit was refused: %v", err)
	}
}

// §5.9 across every free-text field a task verb accepts, so a verb that
// forgets one is the odd one out rather than the norm.
func TestEveryTaskFreeTextFieldIsBounded(t *testing.T) {
	claimed := func() *Task {
		task := newTask()
		if _, err := Claim(task, agent("wF:p1", "claude"), t0+1, 900_000); err != nil {
			t.Fatalf("claim: %v", err)
		}
		return task
	}
	submitted := func() *Task {
		task := claimed()
		if _, err := Submit(task, agent("wF:p1", "claude"), "done", nil, nil, t0+2); err != nil {
			t.Fatalf("submit: %v", err)
		}
		return task
	}
	for name, call := range map[string]func() error{
		"title": func() error {
			_, _, err := New(NewTaskInput{Project: "/p", Title: over(MaxTitle)}, human, t0)
			return err
		},
		"description": func() error {
			_, _, err := New(NewTaskInput{Project: "/p", Title: "t", Description: over(MaxText)}, human, t0)
			return err
		},
		"validation text": func() error {
			_, _, err := New(NewTaskInput{Project: "/p", Title: "t",
				Validation: []Criterion{{Text: over(MaxItem)}}}, human, t0)
			return err
		},
		"validation count": func() error {
			_, _, err := New(NewTaskInput{Project: "/p", Title: "t",
				Validation: make([]Criterion, MaxItems+1)}, human, t0)
			return err
		},
		"update title": func() error {
			s := over(MaxTitle)
			_, err := Update(newTask(), human, UpdatePatch{Title: &s}, t0+1)
			return err
		},
		"update description": func() error {
			s := over(MaxText)
			_, err := Update(newTask(), human, UpdatePatch{Description: &s}, t0+1)
			return err
		},
		"update validation": func() error {
			v := []Criterion{{Text: over(MaxItem)}}
			_, err := Update(newTask(), human, UpdatePatch{Validation: &v}, t0+1)
			return err
		},
		"report": func() error {
			_, err := Submit(claimed(), agent("wF:p1", "claude"), over(MaxText), nil, nil, t0+2)
			return err
		},
		"evidence text": func() error {
			_, err := Submit(claimed(), agent("wF:p1", "claude"), "r", []string{over(MaxItem)}, nil, t0+2)
			return err
		},
		"evidence count": func() error {
			_, err := Submit(claimed(), agent("wF:p1", "claude"), "r", make([]string, MaxItems+1), nil, t0+2)
			return err
		},
		"evidence-for text": func() error {
			_, err := Submit(claimed(), agent("wF:p1", "claude"), "r", nil, []string{"1: " + over(MaxItem)}, t0+2)
			return err
		},
		"evidence-for count": func() error {
			_, err := Submit(claimed(), agent("wF:p1", "claude"), "r", nil, make([]string, MaxItems+1), t0+2)
			return err
		},
		"feedback": func() error {
			_, err := Reject(submitted(), human, over(MaxText), t0+3, lease)
			return err
		},
		"release note": func() error {
			_, err := Release(claimed(), agent("wF:p1", "claude"), over(MaxText), t0+2, KindReleased)
			return err
		},
		"cancel reason": func() error {
			_, err := Cancel(newTask(), human, over(MaxText), t0+1)
			return err
		},
	} {
		err := call()
		if err == nil {
			t.Errorf("%s: over-long input was accepted", name)
			continue
		}
		if got := codeOf(t, err); got != codes.Usage {
			t.Errorf("%s: code = %s, want %s", name, got, codes.Usage)
		}
	}
}

// §5.9 for the notes board, which carries more free text than tasks do: a
// triage verdict's reason is the longest field this plugin actually stores.
func TestEveryNoteFreeTextFieldIsBounded(t *testing.T) {
	discussing := func() *Note {
		n := newNote(t)
		if _, err := NoteDiscuss(n, agent("wF:p1", "claude"), t0+1); err != nil {
			t.Fatalf("discuss: %v", err)
		}
		return n
	}
	for name, call := range map[string]func() error{
		"body": func() error {
			_, _, err := NewNote(NewNoteInput{Project: "/p", Body: over(MaxText)}, human, t0)
			return err
		},
		"question": func() error {
			return noteErr(NoteAskInput(discussing(), agent("wF:p1", "claude"), over(MaxText), t0+2))
		},
		"verdict reason": func() error {
			return noteErr(NoteVerdict(discussing(), agent("wF:p1", "claude"), VerdictTask, over(MaxText), t0+2))
		},
		"keep reason": func() error {
			return noteErr(NoteKeep(discussing(), human, over(MaxText), t0+2))
		},
		"drop reason": func() error {
			return noteErr(NoteDrop(discussing(), human, over(MaxText), t0+2))
		},
	} {
		err := call()
		if err == nil {
			t.Errorf("%s: over-long input was accepted", name)
			continue
		}
		if got := codeOf(t, err); got != codes.Usage {
			t.Errorf("%s: code = %s, want %s", name, got, codes.Usage)
		}
	}
}

// §5.9's other half is a MUST and is not replaced by these caps: a consumer
// rendering stored text into a bounded artifact clamps at render time
// REGARDLESS. These bounds are a check the VERBS run, not an invariant of the
// type — a row loaded from a database written before they existed arrives as
// a struct nobody validated, so the render-time clamp stays load-bearing.
// internal/daemon's goal tests hold that half, with a single 30,000-character
// criterion this bound would now refuse at write time.
func TestBoundsAreAWriteCheckNotATypeInvariant(t *testing.T) {
	// Built as a struct, the way a row from an older database arrives.
	task := &Task{ID: "T1", Seq: 1, Project: "/p", Status: StatusTodo,
		Title: over(MaxTitle), Description: over(MaxText)}
	if len([]rune(task.Title)) <= MaxTitle {
		t.Fatal("precondition: the fixture must exceed the bound")
	}
	// Nothing rejected it, because no verb wrote it. That is the point: a
	// consumer cannot assume stored text is within bounds.
	if task.Title == "" || task.Description == "" {
		t.Fatal("the fixture lost its text")
	}
}
