package tasks

import (
	"testing"

	"github.com/husniadil/herdr-tasks/internal/codes"
)

// Fixed clock values: the state machine is pure, so time is an argument.
const (
	t0    = int64(1_700_000_000_000)
	lease = int64(15 * 60 * 1000)
)

func agent(pane, harness string) Actor {
	return Actor{Principal: Principal("agent:" + pane), Name: "peer-" + pane, Harness: harness, Session: "s-" + pane}
}

var human = Actor{Principal: PrincipalHuman}

func newTask() *Task {
	task, _, err := New(NewTaskInput{ID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Project: "/p", Title: "ship it"}, human, t0)
	if err != nil {
		panic(err)
	}
	return task
}

func codeOf(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		t.Fatalf("want a coded error, got nil")
	}
	ce, ok := err.(*codes.Error)
	if !ok {
		t.Fatalf("want *codes.Error, got %T: %v", err, err)
	}
	return ce.Code
}

// §16.1: a new task starts in todo, unclaimed, with its criteria kept as given.
func TestNewTaskStartsTodo(t *testing.T) {
	task, ev := mustNew(t, NewTaskInput{
		ID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Project: "/p", Title: "ship it",
		Validation: []Criterion{{Text: "make test-full passes", Required: true}},
	}, human, t0)
	if task.Status != StatusTodo {
		t.Fatalf("status = %q, want todo", task.Status)
	}
	if task.EverClaimed {
		t.Fatal("a fresh task must not be marked ever-claimed")
	}
	if ev.Kind != "created" {
		t.Fatalf("event kind = %q, want created", ev.Kind)
	}
	if len(task.Validation) != 1 || !task.Validation[0].Required {
		t.Fatalf("validation not preserved: %+v", task.Validation)
	}
}

func TestNewTaskRejectsEmptyTitle(t *testing.T) {
	_, _, err := New(NewTaskInput{ID: "x", Project: "/p", Title: "   "}, human, t0)
	if got := codeOf(t, err); got != codes.Usage {
		t.Fatalf("code = %q, want USAGE", got)
	}
}

// §5.6: claim is one conditional transition; the second one loses with CONFLICT.
func TestClaimIsCAS(t *testing.T) {
	task := newTask()
	a := agent("wF:p1", "claude")
	if _, err := Claim(task, a, t0, lease); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if task.Status != StatusDoing {
		t.Fatalf("status = %q, want doing", task.Status)
	}
	if task.LeaseUntil != t0+lease {
		t.Fatalf("lease_until = %d, want %d", task.LeaseUntil, t0+lease)
	}
	if task.ClaimedByHarness != "claude" || task.ClaimedByName != "peer-wF:p1" || task.ClaimedBySession != "s-wF:p1" {
		t.Fatalf("§3.4 snapshot missing: %+v", task)
	}
	if !task.EverClaimed {
		t.Fatal("claim must set ever-claimed")
	}
	_, err := Claim(task, agent("wF:p2", "codex"), t0+1, lease)
	if got := codeOf(t, err); got != codes.Conflict {
		t.Fatalf("second claim code = %q, want CONFLICT", got)
	}
}

// §5.6: a claim by the same principal that already holds it is a renewal, not
// a conflict — an agent that lost its reply must be able to retry.
func TestClaimByHolderRenews(t *testing.T) {
	task := newTask()
	a := agent("wF:p1", "claude")
	mustClaim(t, task, a)
	if _, err := Claim(task, a, t0+5, lease); err != nil {
		t.Fatalf("re-claim by holder: %v", err)
	}
	if task.LeaseUntil != t0+5+lease {
		t.Fatalf("lease not renewed: %d", task.LeaseUntil)
	}
}

func TestClaimBlockedTaskConflicts(t *testing.T) {
	task := newTask()
	task.Blocked = true
	if got := codeOf(t, mustErr(Claim(task, agent("wF:p1", "claude"), t0, lease))); got != codes.Conflict {
		t.Fatalf("code = %q, want CONFLICT", got)
	}
}

// §16.3: touch renews the lease, and only the holder may renew it.
func TestTouchRenewsLeaseForHolderOnly(t *testing.T) {
	task := newTask()
	a := agent("wF:p1", "claude")
	mustClaim(t, task, a)
	if _, err := Touch(task, a, t0+60, lease); err != nil {
		t.Fatalf("touch: %v", err)
	}
	if task.LeaseUntil != t0+60+lease {
		t.Fatalf("lease_until = %d", task.LeaseUntil)
	}
	if got := codeOf(t, mustErr(Touch(task, agent("wF:p9", "claude"), t0+61, lease))); got != codes.Forbidden {
		t.Fatalf("stranger touch code = %q, want FORBIDDEN", got)
	}
}

// §16.2: release returns the task to todo and preserves the note.
func TestReleaseReturnsToTodoWithNote(t *testing.T) {
	task := newTask()
	a := agent("wF:p1", "claude")
	mustClaim(t, task, a)
	ev, err := Release(task, a, "migrations left", t0+9, KindReleased)
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if task.Status != StatusTodo || task.ClaimedBy != "" || task.LeaseUntil != 0 {
		t.Fatalf("not released cleanly: %+v", task)
	}
	if task.ReleaseNote != "migrations left" {
		t.Fatalf("note = %q", task.ReleaseNote)
	}
	if ev.Kind != "released" {
		t.Fatalf("event kind = %q", ev.Kind)
	}
	if !task.EverClaimed {
		t.Fatal("release must not erase the ever-claimed mark (§5.7)")
	}
}

// §11.5: a swept lease is a release that says so in the event trail.
func TestSweepReleaseRecordsItsOwnKind(t *testing.T) {
	task := newTask()
	mustClaim(t, task, agent("wF:p1", "claude"))
	ev, err := Release(task, Actor{Principal: PrincipalPlugin}, "lease expired", t0+lease+1, KindSwept)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if ev.Kind != "swept" {
		t.Fatalf("event kind = %q, want swept", ev.Kind)
	}
}

func TestSubmitMovesToReviewWithEvidence(t *testing.T) {
	task := newTask()
	a := agent("wF:p1", "claude")
	mustClaim(t, task, a)
	if _, err := Submit(task, a, "done, gate green", []string{"make test-full: ok"}, t0+10); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if task.Status != StatusReview {
		t.Fatalf("status = %q, want review", task.Status)
	}
	if task.SubmittedByHarness != "claude" || len(task.Evidence) != 1 {
		t.Fatalf("submit snapshot wrong: %+v", task)
	}
	if got := codeOf(t, mustErr(Submit(task, a, "again", nil, t0+11))); got != codes.Conflict {
		t.Fatalf("double submit code = %q, want CONFLICT", got)
	}
}

func TestSubmitByStrangerForbidden(t *testing.T) {
	task := newTask()
	mustClaim(t, task, agent("wF:p1", "claude"))
	err := mustErr(Submit(task, agent("wF:p2", "codex"), "mine now", nil, t0+10))
	if got := codeOf(t, err); got != codes.Forbidden {
		t.Fatalf("code = %q, want FORBIDDEN", got)
	}
}

func TestSubmitRequiresReport(t *testing.T) {
	task := newTask()
	a := agent("wF:p1", "claude")
	mustClaim(t, task, a)
	if got := codeOf(t, mustErr(Submit(task, a, "", nil, t0+10))); got != codes.Usage {
		t.Fatalf("code = %q, want USAGE", got)
	}
}

func TestApproveCompletesTask(t *testing.T) {
	task := submitted(t)
	if _, err := Approve(task, agent("wF:p2", "codex"), t0+20); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if task.Status != StatusDone || task.CompletedAt != t0+20 {
		t.Fatalf("not done: %+v", task)
	}
}

// §6.6: recusal is by harness, not by pane or session.
func TestApproveRecusesSameHarness(t *testing.T) {
	task := submitted(t)
	err := mustErr(Approve(task, agent("wF:p7", "claude"), t0+20))
	if got := codeOf(t, err); got != codes.Forbidden {
		t.Fatalf("code = %q, want FORBIDDEN", got)
	}
}

// §6.6: the human is exempt from recusal.
func TestApproveByHumanAlwaysAllowed(t *testing.T) {
	task := submitted(t)
	if _, err := Approve(task, human, t0+20); err != nil {
		t.Fatalf("human approve: %v", err)
	}
}

// §6.6: reject is a review verdict too, and recuses identically.
func TestRejectReturnsToDoingWithFeedback(t *testing.T) {
	task := submitted(t)
	if got := codeOf(t, mustErr(Reject(task, agent("wF:p8", "claude"), "no test", t0+20))); got != codes.Forbidden {
		t.Fatalf("same-harness reject code = %q, want FORBIDDEN", got)
	}
	if _, err := Reject(task, agent("wF:p2", "codex"), "no test cited", t0+21); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if task.Status != StatusDoing || task.Feedback != "no test cited" {
		t.Fatalf("bad reject result: %+v", task)
	}
}

// A submission whose claim was swept away comes back to todo, not to a doing
// row nobody holds — that row would be unclaimable.
func TestRejectOfSweptClaimReturnsToTodo(t *testing.T) {
	task := submitted(t)
	task.ClaimedBy, task.LeaseUntil = "", 0
	if _, err := Reject(task, human, "stale", t0+21); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if task.Status != StatusTodo {
		t.Fatalf("status = %q, want todo", task.Status)
	}
}

func TestRejectRequiresFeedback(t *testing.T) {
	task := submitted(t)
	if got := codeOf(t, mustErr(Reject(task, human, " ", t0+21))); got != codes.Usage {
		t.Fatalf("code = %q, want USAGE", got)
	}
}

func TestCancelFromAnyLiveState(t *testing.T) {
	task := newTask()
	mustClaim(t, task, agent("wF:p1", "claude"))
	if _, err := Cancel(task, human, "changed our minds", t0+30); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if task.Status != StatusCancelled || task.ClaimedBy != "" {
		t.Fatalf("bad cancel: %+v", task)
	}
	if got := codeOf(t, mustErr(Cancel(task, human, "again", t0+31))); got != codes.Conflict {
		t.Fatalf("code = %q, want CONFLICT", got)
	}
}

// §5.7: only a terminal task archives; live work stays visible.
func TestArchiveOnlyTerminal(t *testing.T) {
	task := newTask()
	if got := codeOf(t, mustErr(Archive(task, human, t0+40))); got != codes.Conflict {
		t.Fatalf("code = %q, want CONFLICT", got)
	}
	mustClaim(t, task, agent("wF:p1", "claude"))
	mustSubmit(t, task, agent("wF:p1", "claude"))
	if _, err := Approve(task, human, t0+41); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := Archive(task, human, t0+42); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if task.ArchivedAt != t0+42 {
		t.Fatalf("archived_at = %d", task.ArchivedAt)
	}
}

// §5.7: nothing is hard-deleted except a row that never left its initial state.
func TestHardDeleteOnlyNeverClaimed(t *testing.T) {
	task := newTask()
	if err := CanHardDelete(task); err != nil {
		t.Fatalf("a never-claimed todo task must be deletable: %v", err)
	}
	mustClaim(t, task, agent("wF:p1", "claude"))
	mustRelease(t, task, agent("wF:p1", "claude"))
	if task.Status != StatusTodo {
		t.Fatalf("precondition: status = %q", task.Status)
	}
	if got := codeOf(t, CanHardDelete(task)); got != codes.Conflict {
		t.Fatalf("code = %q, want CONFLICT — a once-claimed task is cancelled, not deleted", got)
	}
}

// §4.4 / dependency DAG: ready = unblocked todo, and only done satisfies a dep.
func TestReadyIsUnblockedTodo(t *testing.T) {
	task := newTask()
	if !Ready(task) {
		t.Fatal("an unclaimed unblocked todo task is ready")
	}
	task.Blocked = true
	if Ready(task) {
		t.Fatal("a blocked task is not ready")
	}
	task.Blocked = false
	mustClaim(t, task, agent("wF:p1", "claude"))
	if Ready(task) {
		t.Fatal("a claimed task is not ready")
	}
}

func TestCheckCycleRejectsSelfAndLoops(t *testing.T) {
	edges := map[string][]string{"b": {"c"}, "c": {}}
	if got := codeOf(t, CheckCycle("a", []string{"a"}, edges)); got != codes.Usage {
		t.Fatalf("self-dep code = %q, want USAGE", got)
	}
	if err := CheckCycle("a", []string{"b"}, edges); err != nil {
		t.Fatalf("acyclic set rejected: %v", err)
	}
	edges["c"] = []string{"a"}
	if got := codeOf(t, CheckCycle("a", []string{"b"}, edges)); got != codes.Usage {
		t.Fatalf("cycle code = %q, want USAGE", got)
	}
}

func TestUpdateGuardsTerminalAndStampsTime(t *testing.T) {
	task := newTask()
	title := "ship it, properly"
	if _, err := Update(task, human, UpdatePatch{Title: &title}, t0+50); err != nil {
		t.Fatalf("update: %v", err)
	}
	if task.Title != title || task.UpdatedAt != t0+50 {
		t.Fatalf("bad update: %+v", task)
	}
	if _, err := Cancel(task, human, "", t0+51); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if got := codeOf(t, mustErr(Update(task, human, UpdatePatch{Title: &title}, t0+52))); got != codes.Conflict {
		t.Fatalf("code = %q, want CONFLICT", got)
	}
}

// §11.5: an expired lease is sweepable, a live one is not.
func TestLeaseExpired(t *testing.T) {
	task := newTask()
	mustClaim(t, task, agent("wF:p1", "claude"))
	if LeaseExpired(task, t0+lease-1) {
		t.Fatal("a live lease is not expired")
	}
	if !LeaseExpired(task, t0+lease+1) {
		t.Fatal("an elapsed lease is expired")
	}
}

func mustNew(t *testing.T, in NewTaskInput, by Actor, now int64) (*Task, Event) {
	t.Helper()
	task, ev, err := New(in, by, now)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return task, ev
}

func mustClaim(t *testing.T, task *Task, a Actor) {
	t.Helper()
	if _, err := Claim(task, a, t0, lease); err != nil {
		t.Fatalf("claim: %v", err)
	}
}

func mustRelease(t *testing.T, task *Task, a Actor) {
	t.Helper()
	if _, err := Release(task, a, "", t0+1, KindReleased); err != nil {
		t.Fatalf("release: %v", err)
	}
}

func mustSubmit(t *testing.T, task *Task, a Actor) {
	t.Helper()
	if _, err := Submit(task, a, "report", nil, t0+10); err != nil {
		t.Fatalf("submit: %v", err)
	}
}

func submitted(t *testing.T) *Task {
	t.Helper()
	task := newTask()
	a := agent("wF:p1", "claude")
	mustClaim(t, task, a)
	mustSubmit(t, task, a)
	return task
}

func mustErr(_ Event, err error) error { return err }

// §6.6, the hole the harness check alone leaves: a pane whose harness Herdr
// could not resolve at submit time must still not approve its own work once
// Herdr is answering again.
func TestRecusalCatchesSelfReviewAcrossAnUnknownHarness(t *testing.T) {
	task := newTask()
	blind := Actor{Principal: "agent:wF:p1", Name: "peer", Harness: ""}
	mustClaim(t, task, blind)
	if _, err := Submit(task, blind, "done", nil, t0+10); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if task.SubmittedByHarness != "unknown" {
		t.Fatalf("precondition: submitted_by_harness = %q", task.SubmittedByHarness)
	}
	// Same pane, same principal, but Herdr now names the harness.
	seeing := agent("wF:p1", "claude")
	if got := codeOf(t, mustErr(Approve(task, seeing, t0+20))); got != codes.Forbidden {
		t.Fatalf("code = %q, want FORBIDDEN — this is the submitter approving itself", got)
	}
	// A genuinely different principal on a different harness is still fine.
	if _, err := Approve(task, agent("wF:p2", "codex"), t0+21); err != nil {
		t.Fatalf("third-party approve: %v", err)
	}
}
