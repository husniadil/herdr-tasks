package tasks

import (
	"strings"
	"testing"

	"github.com/husniadil/herdr-tasks/internal/codes"
)

// reviewing builds a task that one agent claimed and submitted, which is the
// only state an amendment is defined for.
func reviewing(t *testing.T) (*Task, Actor) {
	t.Helper()
	worker := Actor{Principal: "agent:wM:p1", Harness: "claude", Session: "s1"}
	task, _, err := New(NewTaskInput{Title: "a task", Project: "/p"}, worker, 1000)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := Claim(task, worker, 1000, 60_000); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, err := Submit(task, worker, "the first report", []string{"make test: ok at aaa1111"}, nil, 2000); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	return task, worker
}

// The whole point of the verb: the holder rewrites the report and the evidence
// of work already in review, and the row now names the head it actually
// reached.
func TestAmendRewritesTheReportOfATaskInReview(t *testing.T) {
	task, worker := reviewing(t)
	ev, err := Amend(task, worker, "the corrected report", []string{"make test: ok at bbb2222"}, nil, 3000)
	if err != nil {
		t.Fatalf("Amend: %v", err)
	}
	if task.Report != "the corrected report" {
		t.Errorf("report = %q, want the corrected one", task.Report)
	}
	if len(task.Evidence) != 1 || task.Evidence[0] != "make test: ok at bbb2222" {
		t.Errorf("evidence = %v, want the corrected one", task.Evidence)
	}
	if ev.Kind != KindAmended {
		t.Errorf("event kind = %q, want %q", ev.Kind, KindAmended)
	}
	if ev.Actor != worker.Principal {
		t.Errorf("event actor = %q, want the amending principal", ev.Actor)
	}
	if got := ev.Detail["amendment"]; got != int64(1) {
		t.Errorf("event detail amendment = %v, want 1", got)
	}
}

// Criterion 4, in the state machine: a submission is a thing you make once.
// Amending must not rewrite when it was submitted, by whom, or from which
// session — §6.6 recuses on that session, so moving it would move who may
// review the work.
func TestAmendLeavesTheSubmissionItself(t *testing.T) {
	task, worker := reviewing(t)
	before := *task
	if _, err := Amend(task, worker, "the corrected report", nil, nil, 3000); err != nil {
		t.Fatalf("Amend: %v", err)
	}
	if task.Status != StatusReview {
		t.Errorf("status = %q, want it left in review", task.Status)
	}
	if task.SubmittedAt != before.SubmittedAt {
		t.Errorf("submitted_at moved: %d -> %d", before.SubmittedAt, task.SubmittedAt)
	}
	if task.SubmittedBy != before.SubmittedBy {
		t.Errorf("submitted_by moved: %q -> %q", before.SubmittedBy, task.SubmittedBy)
	}
	if task.SubmittedByHarness != before.SubmittedByHarness || task.SubmittedBySession != before.SubmittedBySession {
		t.Errorf("the submitter's snapshot moved: %q/%q -> %q/%q",
			before.SubmittedByHarness, before.SubmittedBySession,
			task.SubmittedByHarness, task.SubmittedBySession)
	}
	if task.LeaseUntil != 0 {
		t.Errorf("lease_until = %d; an amendment does not put back the lease submit ended", task.LeaseUntil)
	}
}

// Criterion 2's marker half: the row itself says it was amended, and how many
// times, so a reviewer who reads it after the fact is not relying on the
// worker having sent mail about it.
func TestAmendMarksTheRowEveryTime(t *testing.T) {
	task, worker := reviewing(t)
	if task.AmendedAt != 0 || task.AmendCount != 0 {
		t.Fatalf("a freshly submitted row is already marked amended: %d/%d", task.AmendedAt, task.AmendCount)
	}
	if _, err := Amend(task, worker, "second", nil, nil, 3000); err != nil {
		t.Fatalf("Amend: %v", err)
	}
	if task.AmendedAt != 3000 || task.AmendCount != 1 {
		t.Errorf("after one amendment: amended_at %d, amend_count %d; want 3000/1", task.AmendedAt, task.AmendCount)
	}
	ev, err := Amend(task, worker, "third", nil, nil, 4000)
	if err != nil {
		t.Fatalf("Amend twice: %v", err)
	}
	if task.AmendedAt != 4000 || task.AmendCount != 2 {
		t.Errorf("after two amendments: amended_at %d, amend_count %d; want 4000/2", task.AmendedAt, task.AmendCount)
	}
	if got := ev.Detail["amendment"]; got != int64(2) {
		t.Errorf("the second event's amendment number = %v, want 2", got)
	}
	if task.UpdatedAt != 4000 {
		t.Errorf("updated_at = %d; an amendment must move it, because §5.6's guard is how a "+
			"reviewer who read the old report is refused a stale approve", task.UpdatedAt)
	}
}

// Criterion 3's refusal: a stranger amending someone else's row is FORBIDDEN,
// in the same words the other four holder-reserved verbs use. Task 78 kept
// these rules refusing and nothing here lifts them.
func TestAmendRefusesAnyoneButTheHolder(t *testing.T) {
	task, _ := reviewing(t)
	stranger := Actor{Principal: "agent:wM:p9", Harness: "claude", Session: "s9"}
	_, err := Amend(task, stranger, "mine now", nil, nil, 3000)
	if err == nil {
		t.Fatal("a stranger amended a row it does not hold")
	}
	if codeOf(t, err) != codes.Forbidden {
		t.Errorf("code = %s, want FORBIDDEN", codeOf(t, err))
	}
	for _, want := range []string{"agent:wM:p9", "agent:wM:p1", "amend", "--as"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q:\n  %s", want, err)
		}
	}
}

// An amendment is defined only while the work waits for a reviewer. Once a
// verdict landed, the report a reviewer judged is the record.
func TestAmendRefusesOutsideReview(t *testing.T) {
	worker := Actor{Principal: "agent:wM:p1", Harness: "claude", Session: "s1"}
	task, _, err := New(NewTaskInput{Title: "a task", Project: "/p"}, worker, 1000)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := Claim(task, worker, 1000, 60_000); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	_, err = Amend(task, worker, "early", nil, nil, 2000)
	if err == nil {
		t.Fatal("a task in doing was amended")
	}
	if codeOf(t, err) != codes.Conflict {
		t.Errorf("code = %s, want CONFLICT", codeOf(t, err))
	}
	if !strings.Contains(err.Error(), "doing") || !strings.Contains(err.Error(), "review") {
		t.Errorf("the refusal names neither the status it found nor the one it wants:\n  %s", err)
	}

	done, _ := reviewing(t)
	reviewer := Actor{Principal: "agent:wM:p2", Harness: "claude", Session: "s2"}
	if _, err := Approve(done, reviewer, 3000); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	holder := Actor{Principal: "agent:wM:p1", Harness: "claude", Session: "s1"}
	if _, err := Amend(done, holder, "too late", nil, nil, 4000); err == nil {
		t.Fatal("an approved task was amended after its verdict")
	}
}

// The report is the thing being amended, so an empty one is USAGE, exactly as
// it is on submit. Citations are parsed by the same rules for the same reason:
// a checklist a reviewer reads must not gain a checked box on amendment that
// submit would have refused.
func TestAmendHoldsSubmitsRulesForItsArguments(t *testing.T) {
	task, worker := reviewing(t)
	if _, err := Amend(task, worker, "   ", nil, nil, 3000); err == nil {
		t.Error("an empty report was accepted")
	} else if codeOf(t, err) != codes.Usage {
		t.Errorf("code = %s, want USAGE", codeOf(t, err))
	}
	task.Validation = []Criterion{{Text: "one", Required: true}, {Text: "two", Required: true}}
	if _, err := Amend(task, worker, "ok", nil, []string{"1: it printed ok"}, 3000); err == nil {
		t.Error("an amendment cited one of two required criteria and was accepted")
	} else if codeOf(t, err) != codes.Usage {
		t.Errorf("code = %s, want USAGE", codeOf(t, err))
	}
	if _, err := Amend(task, worker, "ok", nil, []string{"1: a", "2: b"}, 3000); err != nil {
		t.Errorf("a fully cited amendment was refused: %v", err)
	}
	if len(task.EvidenceFor) != 2 {
		t.Errorf("evidence_for = %v, want both citations", task.EvidenceFor)
	}
}
