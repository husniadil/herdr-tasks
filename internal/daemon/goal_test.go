package daemon

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/husniadil/herdr-tasks/internal/tasks"
)

func goalTask() *tasks.Task {
	return &tasks.Task{
		ID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Seq: 7, Project: "/p",
		Title:       "Make the lease sweep write an event",
		Description: "The sweep releases the claim but the trail says nothing, so a human reading events cannot tell a release from a sweep.",
		Status:      tasks.StatusTodo,
		Validation: []tasks.Criterion{
			{Text: "`make test-full` passes and its output is shown", Required: true},
			{Text: "`htask events --json` shows a tasks.task.swept entry after a sweep", Required: true},
		},
	}
}

// §16.2: the condition is paste-ready, which means it fits. The limit is a
// MUST, so it holds for input chosen to break it, not only for a task someone
// wrote carefully.
func TestGoalIsUnder4000Characters(t *testing.T) {
	huge := strings.Repeat("x", 30_000)
	many := func(n, size int) []tasks.Criterion {
		out := make([]tasks.Criterion, n)
		for i := range out {
			out[i] = tasks.Criterion{Text: strings.Repeat("c", size), Required: true}
		}
		return out
	}
	cases := map[string]func(*tasks.Task){
		"an ordinary task":       func(*tasks.Task) {},
		"a huge description":     func(x *tasks.Task) { x.Description = huge },
		"huge feedback":          func(x *tasks.Task) { x.Feedback = huge },
		"a huge release note":    func(x *tasks.Task) { x.ReleaseNote = huge },
		"every context huge":     func(x *tasks.Task) { x.Description, x.Feedback, x.ReleaseNote = huge, huge, huge },
		"a hundred criteria":     func(x *tasks.Task) { x.Validation = many(100, 200) },
		"one enormous criterion": func(x *tasks.Task) { x.Validation = many(1, 30_000) },
		"everything at once": func(x *tasks.Task) {
			x.Description, x.Feedback, x.ReleaseNote = huge, huge, huge
			x.Validation = many(100, 200)
		},
		"a pathological title": func(x *tasks.Task) { x.Title = huge },
	}
	for name, mangle := range cases {
		t.Run(name, func(t *testing.T) {
			task := goalTask()
			mangle(task)
			got := BuildGoal(task)
			if len(got) == 0 {
				t.Fatal("goal is empty")
			}
			if len(got) > GoalLimit {
				t.Fatalf("goal is %d characters, past the %d the contract fixes", len(got), GoalLimit)
			}
			if !utf8.ValidString(got) {
				t.Fatal("trimming cut a character in half")
			}
		})
	}
}

// §16.2: context is what gives way when the budget is tight. The directive,
// the Done when block's submit obligation, and the stop clause are the point
// of the condition and survive every trim.
func TestGoalKeepsItsMandatoryPartsUnderPressure(t *testing.T) {
	task := goalTask()
	task.Description = strings.Repeat("context. ", 4000)
	task.Feedback = strings.Repeat("feedback. ", 4000)
	task.ReleaseNote = strings.Repeat("left off. ", 4000)
	for i := 0; i < 100; i++ {
		task.Validation = append(task.Validation, tasks.Criterion{Text: strings.Repeat("c", 200), Required: true})
	}
	got := BuildGoal(task)
	if len(got) > GoalLimit {
		t.Fatalf("goal is %d characters", len(got))
	}
	for _, want := range []string{
		"Make the lease sweep write an event.", // the directive
		"Done when:",
		"htask submit 7", // the submit obligation
		"its output is shown",
		"htask release 7 --note", // the stop clause
		"htask touch 7",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("trimming dropped %q:\n%s", want, got)
		}
	}
	// Context is the part that gave way.
	if strings.Count(got, "context. ") > 200 {
		t.Fatal("the description was not trimmed")
	}
}

// A criteria list too long to fit is truncated only after context is gone, and
// says so rather than trailing off mid-sentence.
func TestGoalSaysWhenItDroppedCriteria(t *testing.T) {
	task := goalTask()
	task.Validation = nil
	for i := 0; i < 100; i++ {
		task.Validation = append(task.Validation, tasks.Criterion{Text: strings.Repeat("c", 200), Required: true})
	}
	got := BuildGoal(task)
	if !strings.Contains(got, "further criteria") {
		t.Fatalf("a truncated criteria list must say so:\n%s", got)
	}
	if !strings.Contains(got, "htask get 7") {
		t.Fatal("a truncated criteria list must point at the full one")
	}
}

// §16.2: a task with a very long description still produces a goal that fits.
func TestGoalClipsRatherThanOverflows(t *testing.T) {
	task := goalTask()
	task.Description = strings.Repeat("context that will not stop. ", 500)
	task.Feedback = strings.Repeat("the reviewer went on at length. ", 200)
	got := BuildGoal(task)
	if len(got) >= GoalLimit {
		t.Fatalf("goal is %d characters, want under %d", len(got), GoalLimit)
	}
	if !strings.Contains(got, "[…]") {
		t.Fatal("a clipped goal must say it was clipped")
	}
}

// §16.2: the directive comes from the title and the criteria become "Done
// when".
func TestGoalCarriesTitleAndCriteria(t *testing.T) {
	got := BuildGoal(goalTask())
	if !strings.HasPrefix(got, "Make the lease sweep write an event.") {
		t.Fatalf("goal does not open with the title:\n%s", got)
	}
	if !strings.Contains(got, "Done when:") {
		t.Fatal("goal has no Done when block")
	}
	for _, c := range goalTask().Validation {
		if !strings.Contains(got, c.Text) {
			t.Fatalf("criterion missing: %q", c.Text)
		}
	}
}

// §16.2: the "Done when" block carries the obligation to run task submit and
// show its output.
func TestGoalCarriesTheSubmitObligation(t *testing.T) {
	got := BuildGoal(goalTask())
	if !strings.Contains(got, "htask submit 7") {
		t.Fatalf("goal does not oblige a submit:\n%s", got)
	}
	if !strings.Contains(got, "--report") || !strings.Contains(got, "--evidence") {
		t.Fatal("the submit obligation must name the report and the evidence")
	}
	if !strings.Contains(got, "its output is shown") {
		t.Fatal("the submit obligation must require showing the output")
	}
}

// §16.2: the stop clause releases the claim with a note saying what is left,
// and sends out-of-scope findings to notes or a discovered-from task.
func TestGoalCarriesTheReleaseStopClause(t *testing.T) {
	got := BuildGoal(goalTask())
	if !strings.Contains(got, "htask release 7 --note") {
		t.Fatalf("goal has no release stop clause:\n%s", got)
	}
	if !strings.Contains(got, "htask note add") || !strings.Contains(got, "--discovered-from 7") {
		t.Fatal("the stop clause must send out-of-scope findings to a note or a discovered-from task")
	}
}

// §16.3: the goal tells the agent to renew its lease each turn.
func TestGoalTellsTheAgentToTouch(t *testing.T) {
	if got := BuildGoal(goalTask()); !strings.Contains(got, "htask touch 7") {
		t.Fatalf("goal does not teach touch:\n%s", got)
	}
}

// A rejected task's goal leads with what the reviewer said.
func TestGoalCarriesRejectFeedback(t *testing.T) {
	task := goalTask()
	task.Feedback = "no test cited for the sweep path"
	got := BuildGoal(task)
	if !strings.Contains(got, "no test cited for the sweep path") {
		t.Fatalf("goal drops the reject feedback:\n%s", got)
	}
}

// §13.1: the goal text is handed to ANOTHER AGENT, and it tells that agent
// which command to run. The binary is `htask`, because `ht` is tex4ht on a
// machine with TeX Live and a hex editor in Homebrew — so an agent that
// followed a goal naming `ht` would run someone else's program. This is the
// one place the binary's name is not documentation: it is an instruction the
// daemon emits at runtime.
func TestTheGoalNamesTheBinaryThatExists(t *testing.T) {
	got := BuildGoal(goalTask())
	// A bare `htask ` anywhere in the goal is the failure. Backtick or not: the
	// text is prose an agent reads, not a fenced block.
	for _, bad := range []string{"`ht ", " ht ", "\nht "} {
		if strings.Contains(got, bad) {
			t.Errorf("the goal names a binary that is not this one (%q):\n%s", bad, got)
		}
	}
	// And it really does teach the verbs, or the check above passes on an
	// empty goal.
	for _, want := range []string{
		"htask touch 7", "htask submit 7", "htask release 7 --note",
		"htask note add", "htask create",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the goal does not teach %q:\n%s", want, got)
		}
	}
}

// §16.2's "paste-ready" has to hold for the channel §11.4 points a plugin at.
// Herdr refuses any newline in agent argv outright, so a condition with one in
// it cannot be delivered as the initial prompt of `herdr agent start` at all.
func TestTheOneLineGoalHasNoNewlineAnywhere(t *testing.T) {
	paragraphs := "First line.\n\nSecond line, after a blank one.\r\nThird, after a CRLF."
	cases := map[string]func(*tasks.Task){
		"an ordinary task":            func(*tasks.Task) {},
		"a title written over lines":  func(x *tasks.Task) { x.Title = paragraphs },
		"a description in paragraphs": func(x *tasks.Task) { x.Description = paragraphs },
		"feedback in paragraphs":      func(x *tasks.Task) { x.Feedback = paragraphs },
		"a release note over lines":   func(x *tasks.Task) { x.ReleaseNote = paragraphs },
		"a body broken with bare carriage returns": func(x *tasks.Task) {
			x.Description = "First line.\rSecond line."
		},
		"a criterion over lines": func(x *tasks.Task) {
			x.Validation = append(x.Validation, tasks.Criterion{Text: paragraphs, Required: true})
		},
		"everything long and broken": func(x *tasks.Task) {
			x.Description = strings.Repeat(paragraphs+" ", 40)
			x.Feedback, x.ReleaseNote = paragraphs, paragraphs
			for i := 0; i < 40; i++ {
				x.Validation = append(x.Validation, tasks.Criterion{Text: paragraphs, Required: true})
			}
		},
	}
	for name, mangle := range cases {
		t.Run(name, func(t *testing.T) {
			task := goalTask()
			mangle(task)
			got := BuildGoalOneLine(task)
			if got == "" {
				t.Fatal("one-line goal is empty")
			}
			if i := strings.IndexAny(got, "\n\r"); i >= 0 {
				t.Fatalf("one-line goal carries a line break at byte %d:\n%q", i, got)
			}
			if len(got) > GoalLimit {
				t.Fatalf("one-line goal is %d characters, past the %d the contract fixes", len(got), GoalLimit)
			}
		})
	}
}

// The one-line form is a rendering of the same condition, not a shorter and
// different one: with room to spare, putting the line breaks back gives the
// document `task goal` already printed.
func TestTheOneLineGoalSaysWhatTheMultiLineGoalSays(t *testing.T) {
	task := goalTask()
	task.Feedback = "The sweep event is there but it does not say which lease it released."
	multi, one := BuildGoal(task), BuildGoalOneLine(task)
	if back := strings.ReplaceAll(one, oneLineSep, "\n"); back != strings.TrimSuffix(multi, "\n") {
		t.Fatalf("one-line goal is not the multi-line one flattened.\nwant:\n%q\ngot:\n%q", strings.TrimSuffix(multi, "\n"), back)
	}
	for _, want := range []string{task.Title, "Context: ", "The reviewer said: ", "Done when:", "htask touch 7", "htask submit 7", "htask release 7 --note"} {
		if !strings.Contains(one, want) {
			t.Fatalf("one-line goal is missing %q:\n%s", want, one)
		}
	}
	for _, c := range task.Validation {
		if !strings.Contains(one, c.Text) {
			t.Fatalf("one-line goal is missing the criterion %q:\n%s", c.Text, one)
		}
	}
}

// A line break costs one byte on the way out and four on the argv path, so a
// condition that fits as a document can cross the ceiling as a line. The
// budget is spent on what is printed: the one-line form drops what it cannot
// afford, by the same order of sacrifice, and says it dropped criteria.
func TestTheOneLineGoalIsBudgetedOnWhatItPrints(t *testing.T) {
	task := goalTask()
	task.Description = strings.Repeat("the sweep says nothing about the lease it released. ", 6)
	for i := 0; i < 80; i++ {
		task.Validation = append(task.Validation, tasks.Criterion{
			Text: fmt.Sprintf("criterion %d: the trail names the lease it released", i), Required: true,
		})
	}
	multi := BuildGoal(task)
	if len(multi) > GoalLimit {
		t.Fatalf("the multi-line form is already %d characters; this case proves nothing", len(multi))
	}
	naive := strings.ReplaceAll(multi, "\n", oneLineSep)
	if len(naive) <= GoalLimit {
		t.Fatalf("flattening this goal costs %d characters, which still fits; this case proves nothing", len(naive))
	}
	one := BuildGoalOneLine(task)
	if len(one) > GoalLimit {
		t.Fatalf("one-line goal is %d characters, past the %d the contract fixes", len(one), GoalLimit)
	}
	for _, want := range []string{task.Title, "Done when:", "htask submit 7", "htask release 7 --note", "did not fit here"} {
		if !strings.Contains(one, want) {
			t.Fatalf("one-line goal dropped %q, which never gives way:\n%s", want, one)
		}
	}
}
