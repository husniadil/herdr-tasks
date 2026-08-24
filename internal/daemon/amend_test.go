package daemon

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/husniadil/herdr-tasks/internal/codes"
	"github.com/husniadil/herdr-tasks/internal/protocol"
	"github.com/husniadil/herdr-tasks/internal/store"
	"github.com/husniadil/herdr-tasks/internal/tasks"
)

// submitted puts a task in review under one agent pane, which is the state the
// amend verb is defined for.
func submitted(t *testing.T, d *Daemon, pane, report string, evidence ...string) TaskResult {
	t.Helper()
	task := createTask(t, d, "correct me")
	mustCall(t, d, protocol.Request{Verb: "task.claim", PaneID: pane,
		Args: map[string]any{"id": task.Task.ID}})
	raw := mustCall(t, d, protocol.Request{Verb: "task.submit", PaneID: pane,
		Args: map[string]any{"id": task.Task.ID, "report": report, "evidence": evidence}})
	return unmarshalTask(t, raw)
}

func taskEvents(t *testing.T, d *Daemon, id string) []store.Event {
	t.Helper()
	raw := mustCall(t, d, protocol.Request{Verb: "events", Args: map[string]any{"entity": "task"}})
	var res EventsResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("unmarshal events: %v", err)
	}
	mine := []store.Event{}
	for _, e := range res.Events {
		if e.EntityID == id {
			mine = append(mine, e)
		}
	}
	return mine
}

// The verb doing its job through the daemon: the holder corrects the report of
// work in review, and what comes back names the newer head.
func TestAmendCorrectsARowInReviewThroughTheDaemon(t *testing.T) {
	d := newDaemon(t, nil)
	before := submitted(t, d, "wM:p1", "done at 07ce055", "make test-full at 07ce055: EXIT=0")
	raw := mustCall(t, d, protocol.Request{Verb: "task.amend", PaneID: "wM:p1",
		Args: map[string]any{"id": before.Task.ID, "report": "done at 9f738bf; three test-only commits followed the submit",
			"evidence": []string{"make test-full at 9f738bf: EXIT=0"}}})
	after := unmarshalTask(t, raw).Task
	if !strings.Contains(after.Report, "9f738bf") {
		t.Errorf("report = %q, want the corrected head", after.Report)
	}
	if len(after.Evidence) != 1 || !strings.Contains(after.Evidence[0], "9f738bf") {
		t.Errorf("evidence = %v, want the corrected head", after.Evidence)
	}
	if after.Status != tasks.StatusReview {
		t.Errorf("status = %q; amending does not move the task out of review", after.Status)
	}
	if after.AmendCount != 1 || after.AmendedAt == 0 {
		t.Errorf("amend_count %d, amended_at %d; the row must carry the marker", after.AmendCount, after.AmendedAt)
	}
}

// Criterion 4, through the daemon and its append-only trail: the `submitted`
// event does not move. There is exactly one of it, it keeps the time and the
// actor it was written with, and the amendment is a SECOND event beside it
// rather than a rewrite of the first.
func TestAmendLeavesTheSubmitEventWhereItWas(t *testing.T) {
	d := newDaemon(t, nil)
	task := submitted(t, d, "wM:p1", "the first report", "make test: ok")
	first := taskEvents(t, d, task.Task.ID)
	var submitEvent store.Event
	for _, e := range first {
		if e.Kind == tasks.KindSubmitted {
			submitEvent = e
		}
	}
	if submitEvent.Kind == "" {
		t.Fatalf("no submitted event in the trail: %+v", first)
	}
	mustCall(t, d, protocol.Request{Verb: "task.amend", PaneID: "wM:p1",
		Args: map[string]any{"id": task.Task.ID, "report": "the corrected report"}})

	after := taskEvents(t, d, task.Task.ID)
	submits, amends := 0, 0
	for _, e := range after {
		switch e.Kind {
		case tasks.KindSubmitted:
			submits++
			if e.At != submitEvent.At || e.Actor != submitEvent.Actor || e.ID != submitEvent.ID {
				t.Errorf("the submitted event moved:\n was %s %s at %d\n now %s %s at %d",
					submitEvent.ID, submitEvent.Actor, submitEvent.At, e.ID, e.Actor, e.At)
			}
		case tasks.KindAmended:
			amends++
			if e.Actor != submitEvent.Actor {
				t.Errorf("the amended event's actor = %q, want the amending principal %q", e.Actor, submitEvent.Actor)
			}
		}
	}
	if submits != 1 {
		t.Errorf("%d submitted events; a submission is made once", submits)
	}
	if amends != 1 {
		t.Errorf("%d amended events; the correction is recorded once", amends)
	}

	// And the row's own submission facts are untouched.
	raw := mustCall(t, d, protocol.Request{Verb: "task.get", Args: map[string]any{"id": task.Task.ID}})
	row := unmarshalTask(t, raw).Task
	if row.SubmittedAt != task.Task.SubmittedAt || row.SubmittedBy != task.Task.SubmittedBy ||
		row.SubmittedBySession != task.Task.SubmittedBySession {
		t.Errorf("the submission facts moved: %d/%s/%s -> %d/%s/%s",
			task.Task.SubmittedAt, task.Task.SubmittedBy, task.Task.SubmittedBySession,
			row.SubmittedAt, row.SubmittedBy, row.SubmittedBySession)
	}
}

// Criterion 2: what a reviewer sees when the report changes while they are
// reading it. The operator who read the row and then approves against what
// they read is REFUSED — §5.6's guard is what makes the amendment visible at
// the moment it matters, rather than after the verdict. Re-reading and
// approving then works.
func TestAnApproveBuiltOnTheReplacedReportIsRefused(t *testing.T) {
	d := newDaemon(t, nil)
	task := submitted(t, d, "wM:p1", "done at 07ce055", "make test-full at 07ce055: EXIT=0")
	asRead := task.Task.UpdatedAt

	mustCall(t, d, protocol.Request{Verb: "task.amend", PaneID: "wM:p1",
		Args: map[string]any{"id": task.Task.ID, "report": "done at 9f738bf"}})

	err := mustFail(t, d, protocol.Request{Verb: "task.approve", BaseUpdatedAt: asRead,
		Args: map[string]any{"id": task.Task.ID}}, codes.Conflict)
	if !strings.Contains(err.Message, "task moved") {
		t.Errorf("the refusal does not say the row moved under the reader:\n  %s", err.Message)
	}

	raw := mustCall(t, d, protocol.Request{Verb: "task.get", Args: map[string]any{"id": task.Task.ID}})
	reread := unmarshalTask(t, raw).Task
	if reread.AmendCount != 1 {
		t.Fatalf("amend_count = %d, want 1", reread.AmendCount)
	}
	mustCall(t, d, protocol.Request{Verb: "task.approve", BaseUpdatedAt: reread.UpdatedAt,
		Args: map[string]any{"id": task.Task.ID}})
}

// Criterion 3, through the daemon: another agent amending a row it does not
// hold is FORBIDDEN, and the refusal names the caller, the holder, and the
// fact that --as does not move a lease — the same words task 80 taught the
// other holder-reserved verbs.
func TestAmendRefusesAnAgentThatDoesNotHoldTheRow(t *testing.T) {
	d := newDaemon(t, nil)
	task := submitted(t, d, "wM:p1", "the first report")
	err := mustFail(t, d, protocol.Request{Verb: "task.amend", PaneID: "wM:p9",
		Args: map[string]any{"id": task.Task.ID, "report": "mine now"}}, codes.Forbidden)
	for _, want := range []string{"agent:wM:p9", "agent:wM:p1", "amend", "--as"} {
		if !strings.Contains(err.Message, want) {
			t.Errorf("the refusal does not name %q:\n  %s", want, err.Message)
		}
	}
	raw := mustCall(t, d, protocol.Request{Verb: "task.get", Args: map[string]any{"id": task.Task.ID}})
	if row := unmarshalTask(t, raw).Task; row.Report != "the first report" || row.AmendCount != 0 {
		t.Errorf("the refused amendment still landed: report %q, amend_count %d", row.Report, row.AmendCount)
	}
}

// Amending is only for work that is waiting on a reviewer. Once the verdict is
// in, the report the reviewer judged is the record — and a rejected task goes
// back to doing, where submit is the verb again.
func TestAmendIsRefusedOnceAVerdictIsIn(t *testing.T) {
	d := newDaemon(t, nil)
	approved := submitted(t, d, "wM:p1", "the first report")
	mustCall(t, d, protocol.Request{Verb: "task.approve", Args: map[string]any{"id": approved.Task.ID}})
	err := mustFail(t, d, protocol.Request{Verb: "task.amend", PaneID: "wM:p1",
		Args: map[string]any{"id": approved.Task.ID, "report": "one more thing"}}, codes.Conflict)
	if !strings.Contains(err.Message, "done") {
		t.Errorf("the refusal does not name the status it found:\n  %s", err.Message)
	}

	rejected := submitted(t, d, "wM:p2", "the first report")
	mustCall(t, d, protocol.Request{Verb: "task.reject",
		Args: map[string]any{"id": rejected.Task.ID, "feedback": "not yet"}})
	mustFail(t, d, protocol.Request{Verb: "task.amend", PaneID: "wM:p2",
		Args: map[string]any{"id": rejected.Task.ID, "report": "fixed"}}, codes.Conflict)
}

// The same rule at the door, where the distinction actually lives: `has` tells
// an absent flag from an empty one, so `htask amend 12 --report "…"`
// keeps the evidence the row already carries.
func TestAmendThroughTheDoorKeepsEvidenceTheCallerDidNotName(t *testing.T) {
	d := newDaemon(t, nil)
	task := submitted(t, d, "wM:p1", "done at 07ce055",
		"make test-full at 07ce055: EXIT=0", "go vet: clean")
	raw := mustCall(t, d, protocol.Request{Verb: "task.amend", PaneID: "wM:p1",
		Args: map[string]any{"id": task.Task.ID, "report": "done at 9f738bf"}})
	after := unmarshalTask(t, raw).Task
	if len(after.Evidence) != 2 {
		t.Errorf("evidence = %v; the door emptied a list the caller never named", after.Evidence)
	}
	if after.Report != "done at 9f738bf" {
		t.Errorf("report = %q, want the corrected one", after.Report)
	}

	raw = mustCall(t, d, protocol.Request{Verb: "task.amend", PaneID: "wM:p1",
		Args: map[string]any{"id": task.Task.ID, "report": "done, evidence withdrawn",
			"evidence": []string{}}})
	if ev := unmarshalTask(t, raw).Task.Evidence; len(ev) != 0 {
		t.Errorf("evidence = %v; naming --evidence empty is a deliberate clear", ev)
	}
}
