package daemon

import (
	"strings"
	"testing"

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
			{Text: "`ht events --json` shows a tasks.task.swept entry after a sweep", Required: true},
		},
	}
}

// §16.2: the condition is paste-ready, which means it fits.
func TestGoalIsUnder4000Characters(t *testing.T) {
	got := BuildGoal(goalTask())
	if len(got) == 0 {
		t.Fatal("goal is empty")
	}
	if len(got) >= GoalLimit {
		t.Fatalf("goal is %d characters, want under %d", len(got), GoalLimit)
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
	if !strings.Contains(got, "ht task submit 7") {
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
	if !strings.Contains(got, "ht task release 7 --note") {
		t.Fatalf("goal has no release stop clause:\n%s", got)
	}
	if !strings.Contains(got, "ht note add") || !strings.Contains(got, "--discovered-from 7") {
		t.Fatal("the stop clause must send out-of-scope findings to a note or a discovered-from task")
	}
}

// §16.3: the goal tells the agent to renew its lease each turn.
func TestGoalTellsTheAgentToTouch(t *testing.T) {
	if got := BuildGoal(goalTask()); !strings.Contains(got, "ht task touch 7") {
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
