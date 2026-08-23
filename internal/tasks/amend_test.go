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
	ev, err := Amend(task, worker, "the corrected report", &[]string{"make test: ok at bbb2222"}, nil, 3000)
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
	if _, err := Amend(task, worker, "ok", nil, &[]string{"1: it printed ok"}, 3000); err == nil {
		t.Error("an amendment cited one of two required criteria and was accepted")
	} else if codeOf(t, err) != codes.Usage {
		t.Errorf("code = %s, want USAGE", codeOf(t, err))
	}
	if _, err := Amend(task, worker, "ok", nil, &[]string{"1: a", "2: b"}, 3000); err != nil {
		t.Errorf("a fully cited amendment was refused: %v", err)
	}
	if len(task.EvidenceFor) != 2 {
		t.Errorf("evidence_for = %v, want both citations", task.EvidenceFor)
	}
}

// The refusal M6 proved unpinned: a row in review that NOBODY holds. It is
// reachable — a claim can be swept or released out from under work already
// submitted — and without this an amendment from any passing principal would
// put a stranger's words under the submitter's name, which is the one thing
// the holder rule exists to stop. The operator stays exempt, as it is for
// submit, release and cancel.
func TestAmendRefusesWhenNobodyHoldsTheRow(t *testing.T) {
	task, worker := reviewing(t)
	if _, err := Release(task, worker, "handing it back", 2500, KindSwept); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if task.Status != StatusReview {
		t.Fatalf("status = %q; the fixture wanted a claimless row still in review", task.Status)
	}
	stranger := Actor{Principal: "agent:wM:p9", Harness: "claude", Session: "s9"}
	_, err := Amend(task, stranger, "mine now", nil, nil, 3000)
	if err == nil {
		t.Fatal("a claimless row in review was amended by a principal that never held it")
	}
	if got := codeOf(t, err); got != codes.Conflict {
		t.Errorf("code = %s, want CONFLICT", got)
	}
	if !strings.Contains(err.Error(), "holds") {
		t.Errorf("the refusal does not say nobody holds the row:\n  %s", err)
	}
	if task.Report != "the first report" {
		t.Errorf("the refused amendment still landed: report = %q", task.Report)
	}
	if _, err := Amend(task, Actor{Principal: PrincipalHuman}, "the operator's correction", nil, nil, 3000); err != nil {
		t.Errorf("the operator was refused a claimless row: %v", err)
	}
}

// The bounds M16 proved unpinned. §5.9 caps the free text a task carries, and
// amend takes the same three arguments submit does: a report over the cap, an
// evidence entry over the item cap, and a list over the item-count cap are
// each USAGE. Without this, amend was the one door into the row with no
// ceiling on what it wrote.
func TestAmendBoundsItsOwnArguments(t *testing.T) {
	task, worker := reviewing(t)
	if _, err := Amend(task, worker, strings.Repeat("a", MaxText+1), nil, nil, 3000); err == nil {
		t.Error("a report over the §5.9 cap was accepted")
	} else if got := codeOf(t, err); got != codes.Usage {
		t.Errorf("an over-long report: code = %s, want USAGE", got)
	}
	if _, err := Amend(task, worker, "ok", &[]string{strings.Repeat("b", MaxItem+1)}, nil, 3000); err == nil {
		t.Error("an evidence entry over the §5.9 item cap was accepted")
	} else if got := codeOf(t, err); got != codes.Usage {
		t.Errorf("an over-long evidence entry: code = %s, want USAGE", got)
	}
	tooMany := make([]string, MaxItems+1)
	for i := range tooMany {
		tooMany[i] = "make test: ok"
	}
	if _, err := Amend(task, worker, "ok", &tooMany, nil, 3000); err == nil {
		t.Error("an evidence list over the §5.9 count cap was accepted")
	} else if got := codeOf(t, err); got != codes.Usage {
		t.Errorf("an over-long evidence list: code = %s, want USAGE", got)
	}
	longCite := []string{"1: " + strings.Repeat("c", MaxItem)}
	if _, err := Amend(task, worker, "ok", nil, &longCite, 3000); err == nil {
		t.Error("an evidence-for entry over the §5.9 item cap was accepted")
	} else if got := codeOf(t, err); got != codes.Usage {
		t.Errorf("an over-long evidence-for entry: code = %s, want USAGE", got)
	}
	if task.Report != "the first report" || task.AmendCount != 0 {
		t.Errorf("a refused amendment still landed: report %q, amend_count %d", task.Report, task.AmendCount)
	}
}

// Absent and empty are different answers. A worker correcting only the wording
// of its report must not lose the evidence a reviewer is reading — the run
// that found this showed two entries going to none on `amend --report` alone,
// which is the quiet wrong record this verb exists to stop. Naming the flag
// with nothing in it still clears, because that is a thing the caller said.
func TestAmendLeavesUnnamedListsAlone(t *testing.T) {
	task, worker := reviewing(t)
	task.Evidence = []string{"make test-full at 07ce055: EXIT=0", "go vet: clean"}
	task.Validation = []Criterion{{Text: "one", Required: true}}
	task.EvidenceFor = []Citation{{Criterion: 1, Text: "make test-full: EXIT=0"}}

	if _, err := Amend(task, worker, "the corrected report", nil, nil, 3000); err != nil {
		t.Fatalf("Amend: %v", err)
	}
	if len(task.Evidence) != 2 {
		t.Errorf("evidence = %v; an amendment that named no --evidence emptied it", task.Evidence)
	}
	if len(task.EvidenceFor) != 1 {
		t.Errorf("evidence_for = %v; an amendment that named no --evidence-for emptied it", task.EvidenceFor)
	}
	if task.Report != "the corrected report" {
		t.Errorf("report = %q, want the corrected one", task.Report)
	}

	empty := []string{}
	if _, err := Amend(task, worker, "cleared", &empty, nil, 4000); err != nil {
		t.Fatalf("Amend with an empty list: %v", err)
	}
	if len(task.Evidence) != 0 {
		t.Errorf("evidence = %v; naming --evidence with nothing in it is a deliberate clear", task.Evidence)
	}
	if len(task.EvidenceFor) != 1 {
		t.Errorf("evidence_for = %v; clearing one list cleared the other", task.EvidenceFor)
	}
}

// The aim M21 was missing. Re-snapshotting the submitter on amend is invisible
// while the amender IS the submitter, and it is not invisible when the
// operator corrects an agent's row: the session §6.6 recuses on would become
// the operator's empty one, and the agent that produced the work could then
// approve it. So the case that has to be pinned is the amender who is somebody
// else.
func TestAmendByTheOperatorLeavesTheSubmittersSnapshot(t *testing.T) {
	task, worker := reviewing(t)
	operator := Actor{Principal: PrincipalHuman}
	if _, err := Amend(task, operator, "the operator's correction", nil, nil, 3000); err != nil {
		t.Fatalf("Amend by the operator: %v", err)
	}
	if task.SubmittedBy != worker.Principal {
		t.Errorf("submitted_by = %q, want the agent that submitted it", task.SubmittedBy)
	}
	if task.SubmittedBySession != worker.Session {
		t.Errorf("submitted_by_session = %q, want %q: §6.6 recuses on this field, so moving it "+
			"would let the agent that produced the work review it", task.SubmittedBySession, worker.Session)
	}
	if task.SubmittedByHarness != worker.Harness {
		t.Errorf("submitted_by_harness = %q, want %q", task.SubmittedByHarness, worker.Harness)
	}
	// The consequence, stated as the rule it protects.
	if err := CheckRecusal(task, worker); err == nil {
		t.Error("after an operator amendment the submitting agent may review its own work (§6.6)")
	}
}
