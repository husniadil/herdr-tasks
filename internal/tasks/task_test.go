package tasks

import (
	"reflect"
	"strings"
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

// agentIn names the session explicitly, for the cases §6.6 now turns on.
func agentIn(pane, harness, session string) Actor {
	a := agent(pane, harness)
	a.Session = session
	return a
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
	if _, err := Submit(task, a, "done, gate green", []string{"make test-full: ok"}, nil, t0+10); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if task.Status != StatusReview {
		t.Fatalf("status = %q, want review", task.Status)
	}
	if task.SubmittedByHarness != "claude" || len(task.Evidence) != 1 {
		t.Fatalf("submit snapshot wrong: %+v", task)
	}
	if got := codeOf(t, mustErr(Submit(task, a, "again", nil, nil, t0+11))); got != codes.Conflict {
		t.Fatalf("double submit code = %q, want CONFLICT", got)
	}
}

func TestSubmitByStrangerForbidden(t *testing.T) {
	task := newTask()
	mustClaim(t, task, agent("wF:p1", "claude"))
	err := mustErr(Submit(task, agent("wF:p2", "codex"), "mine now", nil, nil, t0+10))
	if got := codeOf(t, err); got != codes.Forbidden {
		t.Fatalf("code = %q, want FORBIDDEN", got)
	}
}

// The three sites in this file §3.7 did NOT move: Release, Submit and Cancel
// each refuse a stranger while a task is claimed. The authority is the CLAIM
// HOLDER's, not the operator's, so no confirmation with the operator makes a
// rival's lease theirs to end — which is why they keep refusing where
// note.promote stopped. Removing any one of the three makes this fail.
func TestAClaimIsTheHoldersAndStillRefusesAStranger(t *testing.T) {
	holder := agent("wF:p1", "claude")
	stranger := agent("wF:p2", "codex")

	release := newTask()
	mustClaim(t, release, holder)
	if got := codeOf(t, mustErr(Release(release, stranger, "not yours", t0+10, KindReleased))); got != codes.Forbidden {
		t.Fatalf("release code = %q, want FORBIDDEN", got)
	}

	submit := newTask()
	mustClaim(t, submit, holder)
	if got := codeOf(t, mustErr(Submit(submit, stranger, "mine now", nil, nil, t0+10))); got != codes.Forbidden {
		t.Fatalf("submit code = %q, want FORBIDDEN", got)
	}

	cancel := newTask()
	mustClaim(t, cancel, holder)
	if got := codeOf(t, mustErr(Cancel(cancel, stranger, "done with it", t0+10))); got != codes.Forbidden {
		t.Fatalf("cancel code = %q, want FORBIDDEN", got)
	}

	// And the holder still reaches its own, so the refusal is about the claim
	// rather than about being an agent.
	if _, err := Release(release, holder, "handing back", t0+11, KindReleased); err != nil {
		t.Fatalf("holder release: %v", err)
	}
}

func TestSubmitRequiresReport(t *testing.T) {
	task := newTask()
	a := agent("wF:p1", "claude")
	mustClaim(t, task, a)
	if got := codeOf(t, mustErr(Submit(task, a, "", nil, nil, t0+10))); got != codes.Usage {
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

// §6.6 (0.6.0), the incident this rule was rewritten for: an agent on the same
// harness, in a different pane and a different session, reviews the work — and
// is recorded under its own principal instead of borrowing the operator's.
func TestApproveAllowsTheSameHarnessInADifferentSession(t *testing.T) {
	task := submitted(t)
	reviewer := agent("wF:p7", "claude")
	if _, err := Approve(task, reviewer, t0+20); err != nil {
		t.Fatalf("same harness, different session: %v", err)
	}
	if task.ReviewedBy != reviewer.Principal {
		t.Fatalf("reviewed_by = %q, want %q", task.ReviewedBy, reviewer.Principal)
	}
}

// §6.6: the same pane is the same principal, and never reviews itself.
func TestApproveRecusesTheSamePane(t *testing.T) {
	task := submitted(t)
	if got := codeOf(t, mustErr(Approve(task, agent("wF:p1", "claude"), t0+20))); got != codes.Forbidden {
		t.Fatalf("code = %q, want FORBIDDEN", got)
	}
}

// §6.6: the resume case. A different pane carrying the SAME agent_session is
// the same conversation, so it is still the work's own author.
func TestApproveRecusesTheSameSessionInADifferentPane(t *testing.T) {
	task := newTask()
	a := agentIn("wF:p1", "claude", "sess-7")
	mustClaim(t, task, a)
	mustSubmit(t, task, a)
	resumed := agentIn("wF:p9", "claude", "sess-7")
	err := mustErr(Approve(task, resumed, t0+20))
	if got := codeOf(t, err); got != codes.Forbidden {
		t.Fatalf("code = %q, want FORBIDDEN", got)
	}
	if !strings.Contains(err.Error(), "session") {
		t.Fatalf("the refusal does not say what matched: %v", err)
	}
}

// §6.6: the blip case, and the reason unknown matches unknown. A pane that
// claimed while Herdr could not answer must not approve its own work later.
// "Herdr could not answer" is a whole unanswered snapshot, so the harness of
// both actors here is "unknown" too — the stamp §3.4 requires when there is
// no reply to read, and the one signal sessionOf has that a session is
// unresolved rather than legitimately absent.
func TestApproveRecusesWhenBothSessionsAreUnknown(t *testing.T) {
	task := newTask()
	blind := Actor{Principal: "agent:wF:p1", Name: "peer", Harness: "unknown"}
	mustClaim(t, task, blind)
	mustSubmit(t, task, blind)
	if task.SubmittedBySession != "unknown" {
		t.Fatalf("precondition: submitted_by_session = %q", task.SubmittedBySession)
	}
	other := Actor{Principal: "agent:wF:p9", Name: "peer", Harness: "unknown"}
	if got := codeOf(t, mustErr(Approve(task, other, t0+20))); got != codes.Forbidden {
		t.Fatalf("code = %q, want FORBIDDEN", got)
	}
}

// §6.6: a declared principal has no pane and no session, so it recuses on its
// own principal alone — an unresolved agent session is not its session.
func TestDeclaredPrincipalReviewsAcrossAnUnknownSession(t *testing.T) {
	task := newTask()
	blind := Actor{Principal: "agent:wF:p1", Name: "peer", Harness: "claude"}
	mustClaim(t, task, blind)
	mustSubmit(t, task, blind)
	if _, err := Approve(task, Actor{Principal: "cron:nightly"}, t0+20); err != nil {
		t.Fatalf("cron approve: %v", err)
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
	if got := codeOf(t, mustErr(Reject(task, agent("wF:p1", "claude"), "no test", t0+20))); got != codes.Forbidden {
		t.Fatalf("self-review reject code = %q, want FORBIDDEN", got)
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
	if _, err := Submit(task, a, "report", nil, nil, t0+10); err != nil {
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

// §6.6: a pane whose Herdr facts could not be resolved at submit time must
// still not approve its own work once Herdr is answering again.
func TestRecusalCatchesSelfReviewAcrossAnUnknownHarness(t *testing.T) {
	task := newTask()
	blind := Actor{Principal: "agent:wF:p1", Name: "peer", Harness: ""}
	mustClaim(t, task, blind)
	if _, err := Submit(task, blind, "done", nil, nil, t0+10); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if task.SubmittedByHarness != "unknown" {
		t.Fatalf("precondition: submitted_by_harness = %q", task.SubmittedByHarness)
	}
	// Same pane, same principal, but Herdr now names the facts.
	seeing := agent("wF:p1", "claude")
	if got := codeOf(t, mustErr(Approve(task, seeing, t0+20))); got != codes.Forbidden {
		t.Fatalf("code = %q, want FORBIDDEN — this is the submitter approving itself", got)
	}
	// A genuinely different principal on a different harness is still fine.
	if _, err := Approve(task, agent("wF:p2", "codex"), t0+21); err != nil {
		t.Fatalf("third-party approve: %v", err)
	}
}

// §3.1: cancelling a claimed task is strictly more destructive than releasing
// it — it clears the claim AND ends the task — so it cannot be easier. Release
// and Submit both refuse a non-holder that is not the operator; Cancel took no
// principal at all.
func TestOnlyTheHolderOrTheOperatorCancelsAClaimedTask(t *testing.T) {
	task := newTask()
	holder := agent("wF:p1", "claude")
	mustClaim(t, task, holder)

	if _, err := Cancel(task, agent("wF:p2", "codex"), "not needed", t0+1); err == nil {
		t.Fatal("a rival agent may not cancel a claimed task")
	} else if got := codeOf(t, err); got != codes.Forbidden {
		t.Fatalf("code = %q, want FORBIDDEN", got)
	}
	if task.Status != StatusDoing {
		t.Fatalf("the refused cancel changed the task: %+v", task)
	}
	if _, err := Cancel(task, holder, "not needed", t0+2); err != nil {
		t.Fatalf("the holder may cancel: %v", err)
	}

	other := newTask()
	mustClaim(t, other, holder)
	if _, err := Cancel(other, human, "the operator says so", t0+3); err != nil {
		t.Fatalf("the operator may cancel: %v", err)
	}

	// An UNCLAIMED task is nobody's, and cancelling it stays as open as
	// release is — release refuses it for being unclaimed, not for who asked.
	free := newTask()
	if _, err := Cancel(free, agent("wF:p2", "codex"), "stale idea", t0+4); err != nil {
		t.Fatalf("an unclaimed task may be cancelled by anyone: %v", err)
	}
}

// §5.6: a claim by the principal that already holds it is a renewal, not a
// new claim — an agent that lost its reply must be able to retry. The Blocked
// check ran after the renewal case was admitted, so it applied to renewals
// too: anyone adding a dependency with `task update` while an agent was
// working made that agent's retry fail, on a task it still held. Touch, doing
// the same job through a different verb, kept working — so the two verbs
// disagreed about the same row in the same second.
func TestTheHoldersReClaimSurvivesADependencyAddedAfterTheClaim(t *testing.T) {
	task := newTask()
	holder := agent("wF:p1", "claude")
	mustClaim(t, task, holder)
	before := task.LeaseUntil

	// What `task update --depends-on` does to a task someone is working on:
	// Blocked is recomputed on every read, and Update refuses only terminal
	// tasks, so this is reachable through an ordinary verb.
	task.Blocked = true

	if _, err := Claim(task, holder, t0+50, lease); err != nil {
		t.Fatalf("the holder's re-claim was refused: %v", err)
	}
	if task.Status != StatusDoing {
		t.Fatalf("status = %q, want doing", task.Status)
	}
	if task.ClaimedBy != holder.Principal {
		t.Fatalf("claimed_by = %q, want %q", task.ClaimedBy, holder.Principal)
	}
	if task.LeaseUntil <= before {
		t.Fatalf("lease_until = %d, want it extended past %d", task.LeaseUntil, before)
	}
}

// The fix must not widen who may take blocked work.
func TestABlockedTaskIsStillRefusedToEveryoneElse(t *testing.T) {
	held := newTask()
	holder := agent("wF:p1", "claude")
	mustClaim(t, held, holder)
	held.Blocked = true
	_, err := Claim(held, agent("wF:p2", "codex"), t0+50, lease)
	if got := codeOf(t, err); got != codes.Conflict {
		t.Fatalf("code = %q, want CONFLICT", got)
	}
	if !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("a rival is told %q; the reason it cannot be worked is that it is blocked", err)
	}

	// And an unclaimed blocked task still refuses a first claim.
	free := newTask()
	free.Blocked = true
	_, err = Claim(free, holder, t0+1, lease)
	if got := codeOf(t, err); got != codes.Conflict {
		t.Fatalf("first claim on a blocked task: code = %q, want CONFLICT", got)
	}
	if !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("the refusal does not say why: %v", err)
	}
	if free.ClaimedBy != "" || free.Status != StatusTodo {
		t.Fatalf("the refused claim changed the task: %+v", free)
	}
}

// §5.6: claim and touch are the holder's two ways of saying "still mine", so
// wherever one works for the holder the other must. This is the contradiction
// the note was about, stated as a rule rather than as one case.
//
// `review` is deliberately not in this table: touch succeeds there (the claim
// is still set) and claim refuses (work in review is not work to take). That
// is a real third disagreement, and it is not this fix — a lease renewed on
// something already submitted is odd but harmless, while letting claim take a
// task out of review would not be.
func TestClaimAndTouchAgreeForTheHolder(t *testing.T) {
	holder := agent("wF:p1", "claude")
	states := map[string]func() *Task{
		"doing": func() *Task {
			x := newTask()
			mustClaim(t, x, holder)
			return x
		},
		"doing and blocked": func() *Task {
			x := newTask()
			mustClaim(t, x, holder)
			x.Blocked = true
			return x
		},
		"doing with a lapsed lease": func() *Task {
			x := newTask()
			mustClaim(t, x, holder)
			x.LeaseUntil = t0 + 1
			return x
		},
		"doing, blocked, and lapsed": func() *Task {
			x := newTask()
			mustClaim(t, x, holder)
			x.Blocked, x.LeaseUntil = true, t0+1
			return x
		},
	}
	for name, build := range states {
		_, touchErr := Touch(build(), holder, t0+99, lease)
		_, claimErr := Claim(build(), holder, t0+99, lease)
		if touchErr == nil && claimErr != nil {
			t.Errorf("%s: touch works for the holder and claim does not (%v)", name, claimErr)
		}
		if touchErr != nil && claimErr == nil {
			t.Errorf("%s: claim works for the holder and touch does not (%v)", name, touchErr)
		}
	}
}

// §6.1: UpdatePatch is the set of fields a door can edit, and DiscoveredFrom
// was in it with no door declaring the argument — editable in the domain,
// unreachable from anywhere. Dead surface is worse than a missing feature: it
// reads as a capability and behaves as nothing. Provenance is still set at
// create, where hTaskCreate validates that the origin exists; editing it later
// would need that validation written for a use nobody has stated, so the field
// went rather than a door being added for it.
func TestUpdateDoesNotOfferProvenance(t *testing.T) {
	patch := reflect.TypeOf(UpdatePatch{})
	for i := 0; i < patch.NumField(); i++ {
		if patch.Field(i).Name == "DiscoveredFrom" {
			t.Fatal("UpdatePatch offers DiscoveredFrom; no door declares the argument, so it cannot be reached")
		}
	}
	// And it is still recorded at create, which is the path that has one.
	task := newTask()
	if task.DiscoveredFrom != "" {
		t.Fatalf("a task made without provenance carries %q", task.DiscoveredFrom)
	}
}

// §16.1: a criterion is a proof an evaluator can check, so the evidence that
// proves it says which one it proves. The index is folded into the string
// because verbs knows four argument kinds and none of them is a pair.
func TestSubmitBindsEvidenceToTheCriterionItProves(t *testing.T) {
	task := withCriteria(t, Criterion{Text: "the gate is green", Required: true},
		Criterion{Text: "the docs say so", Required: true})
	a := agent("wF:p1", "claude")
	mustClaim(t, task, a)
	if _, err := Submit(task, a, "done", []string{"make build: ok"},
		[]string{"1: make test-full -> exit 0", " 2 :  htask task get 5 -> Done when"}, t0+10); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if len(task.EvidenceFor) != 2 {
		t.Fatalf("citations = %+v, want 2", task.EvidenceFor)
	}
	if task.EvidenceFor[0] != (Citation{Criterion: 1, Text: "make test-full -> exit 0"}) {
		t.Fatalf("first citation = %+v", task.EvidenceFor[0])
	}
	if task.EvidenceFor[1] != (Citation{Criterion: 2, Text: "htask task get 5 -> Done when"}) {
		t.Fatalf("second citation = %+v", task.EvidenceFor[1])
	}
	// Task-level evidence stays where it was: citations are a parallel field,
	// never a repurposing of the one that shipped.
	if len(task.Evidence) != 1 || task.Evidence[0] != "make build: ok" {
		t.Fatalf("evidence moved: %+v", task.Evidence)
	}
}

// A citation naming a criterion that is not there is a typo, and a typo must
// not half-submit: the task is still doing, with nothing written on it.
func TestACitationOutsideTheCriteriaListIsRefusedWholesale(t *testing.T) {
	task := withCriteria(t, Criterion{Text: "one", Required: true},
		Criterion{Text: "two", Required: true}, Criterion{Text: "three", Required: true})
	a := agent("wF:p1", "claude")
	mustClaim(t, task, a)
	err := mustErr(Submit(task, a, "done", nil, []string{"9: x"}, t0+10))
	if got := codeOf(t, err); got != codes.Usage {
		t.Fatalf("code = %q, want USAGE", got)
	}
	if !strings.Contains(err.Error(), "9") || !strings.Contains(err.Error(), "3") {
		t.Fatalf("the message says neither what was cited nor how many there are: %v", err)
	}
	if task.Status != StatusDoing || task.Report != "" || len(task.EvidenceFor) != 0 {
		t.Fatalf("a refused submit still wrote: status=%q report=%q cites=%+v",
			task.Status, task.Report, task.EvidenceFor)
	}
}

// A citation the doors cannot read at all — no number, no colon, nothing after
// it — is the same wholesale refusal.
func TestACitationThatIsNotACitationIsRefused(t *testing.T) {
	for _, raw := range []string{"make test-full -> ok", "one: x", "1:   ", ": x"} {
		task := withCriteria(t, Criterion{Text: "one", Required: true})
		a := agent("wF:p1", "claude")
		mustClaim(t, task, a)
		err := mustErr(Submit(task, a, "done", nil, []string{raw}, t0+10))
		if got := codeOf(t, err); got != codes.Usage {
			t.Fatalf("%q: code = %q, want USAGE", raw, got)
		}
		if task.Status != StatusDoing {
			t.Fatalf("%q: status = %q, want doing", raw, task.Status)
		}
	}
}

// Constraint 3 from triage: strictness is opt-in. Every task already on the
// board is all-required, so a submit that cites nothing behaves exactly as it
// always has; cite one and you are claiming coverage, so cite them all.
func TestCitingOneCriterionMeansCitingEveryRequiredOne(t *testing.T) {
	a := agent("wF:p1", "claude")

	quiet := withCriteria(t, Criterion{Text: "one", Required: true},
		Criterion{Text: "two", Required: true})
	mustClaim(t, quiet, a)
	if _, err := Submit(quiet, a, "done", []string{"make test: ok"}, nil, t0+10); err != nil {
		t.Fatalf("a submit that cites nothing must still work: %v", err)
	}
	if quiet.Status != StatusReview {
		t.Fatalf("status = %q, want review", quiet.Status)
	}

	partial := withCriteria(t, Criterion{Text: "one", Required: true},
		Criterion{Text: "two", Required: true})
	mustClaim(t, partial, a)
	err := mustErr(Submit(partial, a, "done", nil, []string{"1: make test -> ok"}, t0+10))
	if got := codeOf(t, err); got != codes.Usage {
		t.Fatalf("code = %q, want USAGE", got)
	}
	if !strings.Contains(err.Error(), "2") {
		t.Fatalf("the message does not name criterion 2: %v", err)
	}
	if partial.Status != StatusDoing {
		t.Fatalf("status = %q, want doing", partial.Status)
	}
}

// The (optional) marker is the one thing that ever read Criterion.Required.
// Now it is load-bearing: an optional criterion needs no citation.
func TestAnOptionalCriterionNeedsNoCitation(t *testing.T) {
	task := withCriteria(t, Criterion{Text: "the gate is green", Required: true},
		Criterion{Text: "a screenshot", Required: false})
	a := agent("wF:p1", "claude")
	mustClaim(t, task, a)
	if _, err := Submit(task, a, "done", nil, []string{"1: make test-full -> exit 0"}, t0+10); err != nil {
		t.Fatalf("an uncited optional criterion blocked the submit: %v", err)
	}
	if task.Status != StatusReview {
		t.Fatalf("status = %q, want review", task.Status)
	}
}

// A task with no criteria has nothing to cite, and saying so beats storing a
// citation that points at nothing.
func TestCitingATaskWithNoCriteriaIsRefused(t *testing.T) {
	task := newTask()
	a := agent("wF:p1", "claude")
	mustClaim(t, task, a)
	if got := codeOf(t, mustErr(Submit(task, a, "done", nil, []string{"1: x"}, t0+10))); got != codes.Usage {
		t.Fatalf("code = %q, want USAGE", got)
	}
}

// The submitted event counts what it carried, so the trail says a submission
// claimed coverage without anyone re-reading the row.
func TestTheSubmittedEventCountsCitations(t *testing.T) {
	task := withCriteria(t, Criterion{Text: "one", Required: true})
	a := agent("wF:p1", "claude")
	mustClaim(t, task, a)
	ev, err := Submit(task, a, "done", nil, []string{"1: make test -> ok"}, t0+10)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if ev.Detail["citation_count"] != 1 {
		t.Fatalf("detail = %+v, want citation_count 1", ev.Detail)
	}
}

func withCriteria(t *testing.T, cs ...Criterion) *Task {
	t.Helper()
	task, _, err := New(NewTaskInput{ID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Project: "/p",
		Title: "ship it", Validation: cs}, human, t0)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	return task
}

// §11.5 with the rule task 30 asked for: wherever touch works for the holder,
// claim works too. Submitting hands the work off, so the lease it was held
// under is over — and the holder is told that plainly rather than left
// renewing a lease on work it is no longer doing.
func TestTouchRefusesOnceTheWorkIsSubmitted(t *testing.T) {
	task := newTask()
	a := agent("wF:p1", "claude")
	mustClaim(t, task, a)
	if task.LeaseUntil == 0 {
		t.Fatal("precondition: a claim sets a lease")
	}
	if _, err := Submit(task, a, "done", nil, nil, t0+10); err != nil {
		t.Fatalf("submit: %v", err)
	}
	// The lease ends; the attribution does not. The board still says who
	// submitted this, and §6.6 still has a session to recuse.
	if task.LeaseUntil != 0 {
		t.Fatalf("lease_until = %d after submit, want 0", task.LeaseUntil)
	}
	if task.ClaimedBy != a.Principal {
		t.Fatalf("claimed_by = %q after submit, want %q", task.ClaimedBy, a.Principal)
	}
	if task.SubmittedByHarness != "claude" || task.SubmittedBySession != "s-wF:p1" {
		t.Fatalf("submitted_by = %q/%q, want claude/s-wF:p1", task.SubmittedByHarness, task.SubmittedBySession)
	}

	err := mustErr(Touch(task, a, t0+11, lease))
	if got := codeOf(t, err); got != codes.Conflict {
		t.Fatalf("touch code = %q, want CONFLICT", got)
	}
	if !strings.Contains(err.Error(), string(StatusReview)) {
		t.Fatalf("the refusal does not name the status: %v", err)
	}
	if !strings.Contains(err.Error(), "lease") {
		t.Fatalf("the refusal does not say what there is none of: %v", err)
	}
	if got := codeOf(t, mustErr(Claim(task, a, t0+11, lease))); got != codes.Conflict {
		t.Fatalf("claim code = %q, want CONFLICT: the two verbs must agree", got)
	}

	// And touch still renews where the work really is in hand.
	doing := newTask()
	mustClaim(t, doing, a)
	if _, err := Touch(doing, a, t0+12, lease); err != nil {
		t.Fatalf("touch on a doing task: %v", err)
	}
}

// §3.4: the third fact is "the native session reference if Herdr has one,
// otherwise null". Herdr answering with a harness and no `agent_session` is an
// answer, and absence is what it says: the row must be empty, not the string
// "unknown". "unknown" is §3.4's stamp for the fact Herdr could NOT answer,
// and writing it here would record absence as a value — the mistake §3.7
// removed for `human`. §6.6 recuses on this field, so a row that cannot tell
// "there is no session" from "we could not identify the session" is deciding
// who may review whom on a fiction.
func TestClaimRecordsAnAbsentAgentSessionAsAbsentNotUnknown(t *testing.T) {
	task := newTask()
	// Herdr answered: the harness is a fact, and this agent has no session.
	answered := agentIn("wF:p1", "codex", "")
	mustClaim(t, task, answered)
	if task.ClaimedBySession != "" {
		t.Fatalf("claimed_by_session = %q, want empty: Herdr reported no agent_session", task.ClaimedBySession)
	}
	mustSubmit(t, task, answered)
	if task.SubmittedBySession != "" {
		t.Fatalf("submitted_by_session = %q, want empty: Herdr reported no agent_session", task.SubmittedBySession)
	}
	// And absence must not recuse a different pane the way unknown does: two
	// agents Herdr answered for, neither carrying a session, are two
	// reviewers (§6.6, which recuses on the harness no more than on nothing).
	other := agentIn("wF:p9", "claude", "")
	if _, err := Approve(task, other, t0+20); err != nil {
		t.Fatalf("a different pane with no session may review: %v", err)
	}
}

// §3.4's snapshot is taken at the claim and it goes with the claim: all four
// fields move together. A row that lets go while one of them stays behind goes
// out over --json saying claimed_by "" and claimed_by_harness "claude" at once,
// which is the previous holder's harness on a row nobody holds. Clearing them
// one name at a time is how one gets overlooked, so this asks for the set.
func TestLettingGoClearsTheWholeClaimSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name string
		let  func(*Task, Actor) error
	}{
		{"release", func(task *Task, a Actor) error {
			_, err := Release(task, a, "migrations left", t0+9, KindReleased)
			return err
		}},
		{"sweep", func(task *Task, a Actor) error {
			_, err := Release(task, a, "the lease expired", t0+9, KindSwept)
			return err
		}},
		{"cancel", func(task *Task, a Actor) error {
			_, err := Cancel(task, a, "no longer needed", t0+9)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := newTask()
			a := agent("wF:p1", "claude")
			mustClaim(t, task, a)
			if task.ClaimedByHarness == "" {
				t.Fatal("the claim took no §3.4 snapshot, so this proves nothing")
			}
			if err := tc.let(task, a); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			for field, got := range map[string]string{
				"claimed_by":         string(task.ClaimedBy),
				"claimed_by_name":    task.ClaimedByName,
				"claimed_by_harness": task.ClaimedByHarness,
				"claimed_by_session": task.ClaimedBySession,
			} {
				if got != "" {
					t.Errorf("%s left %s = %q on a row nobody holds", tc.name, field, got)
				}
			}
			if task.ClaimedAt != 0 || task.LeaseUntil != 0 {
				t.Errorf("%s left claimed_at %d / lease_until %d", tc.name, task.ClaimedAt, task.LeaseUntil)
			}
		})
	}
}
