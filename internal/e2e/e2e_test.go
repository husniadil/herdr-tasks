//go:build e2e

package e2e

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// §3.2, §3.3: a caller's principal is derived, never declared. A door running
// in a Herdr-managed pane is `agent:<pane id>` because the pane put
// HERDR_PANE_ID in its environment, and a door outside one is `human`.
func TestPrincipalIsDerivedInsideAManagedPane(t *testing.T) {
	w := startWorld(t)
	pane := w.pane("principal")

	doc := w.mustInPane(pane, "task", "create", "written from a pane")
	task, _ := doc["task"].(map[string]any)
	if task == nil {
		t.Fatalf("create in a pane returned %v", doc)
	}
	if got, want := task["created_by"], "agent:"+pane; got != want {
		t.Fatalf("created_by = %v, want %v", got, want)
	}

	// The same binary, the same daemon, outside the pane: `human` (§3.6).
	outside := w.htask("task", "create", "written from a terminal")
	t2, _ := outside["task"].(map[string]any)
	if got := t2["created_by"]; got != "human" {
		t.Fatalf("created_by outside a pane = %v, want human", got)
	}
}

// §3.4: an agent principal carries name, harness and native session, and they
// are snapshotted from `herdr agent get` at the moment they matter — here, the
// claim. A harness Herdr can answer for must never come back "unknown".
func TestAgentGetSnapshotIsTakenFromRealHerdrAtClaim(t *testing.T) {
	w := startWorld(t)
	pane := w.pane("snapshot")
	w.beAgent(pane, "codex", "reviewer")

	created := w.htask("task", "create", "claim me")
	task, _ := created["task"].(map[string]any)
	id, _ := task["id"].(string)

	claimed := w.mustInPane(pane, "task", "claim", id)
	got, _ := claimed["task"].(map[string]any)
	if got["claimed_by"] != "agent:"+pane {
		t.Fatalf("claimed_by = %v, want agent:%s", got["claimed_by"], pane)
	}
	if got["claimed_by_harness"] != "codex" {
		t.Fatalf("claimed_by_harness = %v, want codex — the snapshot did not reach Herdr",
			got["claimed_by_harness"])
	}
	if got["claimed_by_name"] != "reviewer" {
		t.Fatalf("claimed_by_name = %v, want the name Herdr holds", got["claimed_by_name"])
	}
	// §3.4's third fact is the native session reference "if Herdr has one".
	// This Herdr reports none for an agent declared over the CLI, so the
	// snapshot must be empty — never invented (docs/contract-notes.md).
	if s, ok := got["claimed_by_session"].(string); ok && s != "" {
		t.Fatalf("claimed_by_session = %q, but Herdr reported no agent_session", s)
	}
	if got["lease_until"] == nil {
		t.Fatal("a claim with no lease is not a claim (§14)")
	}
}

// §12.1 layer 3, §6.6: one whole lifecycle through the real Herdr — created by
// the operator, claimed and submitted by an agent in a pane, approved by the
// operator, who is the principal exempt from recusal.
func TestLifecycleCreateClaimSubmitApproveThroughRealHerdr(t *testing.T) {
	w := startWorld(t)
	pane := w.pane("lifecycle")
	w.beAgent(pane, "claude", "builder")

	created := w.htask("task", "create", "wire the door", "--validation", "make test: ok")
	id, _ := created["task"].(map[string]any)["id"].(string)

	w.mustInPane(pane, "task", "claim", id)
	if s := w.task(id)["status"]; s != "doing" {
		t.Fatalf("after claim the task is %v, want doing", s)
	}

	w.mustInPane(pane, "task", "submit", id, "--report", "wired it", "--evidence", "make test: ok")
	submitted := w.task(id)
	if submitted["status"] != "review" {
		t.Fatalf("after submit the task is %v, want review", submitted["status"])
	}
	if submitted["submitted_by"] != "agent:"+pane {
		t.Fatalf("submitted_by = %v", submitted["submitted_by"])
	}

	// Recusal (§6.6) is by principal and by agent session, and the operator is
	// exempt: the same approve from the claiming pane must be refused, and
	// from a terminal must go through.
	if _, err := w.htInPane(pane, "task", "approve", id); err == nil {
		t.Fatal("the claiming pane approved its own work (§6.6)")
	} else if !strings.Contains(err.Error(), "FORBIDDEN") {
		t.Fatalf("self-review was refused with %v, want FORBIDDEN (§6.3)", err)
	}

	w.htask("task", "approve", id)
	done := w.task(id)
	if done["status"] != "done" {
		t.Fatalf("after approve the task is %v, want done", done["status"])
	}
	if done["reviewed_by"] != "human" {
		t.Fatalf("reviewed_by = %v, want human", done["reviewed_by"])
	}

	// §5.5: the trail says all of it happened, in order.
	events := w.htask("events", "--entity", "task")
	blob, kinds := "", []string{"created", "claimed", "submitted", "approved"}
	for _, e := range events["events"].([]any) {
		blob += e.(map[string]any)["kind"].(string) + " "
	}
	for _, k := range kinds {
		if !strings.Contains(blob, k) {
			t.Fatalf("the event trail is missing %q: %s", k, blob)
		}
	}
}

// evidenceOf reads a task's evidence list without panicking when it is
// absent, so a broken amend fails with a message instead of an index panic.
func evidenceOf(task map[string]any) []string {
	raw, _ := task["evidence"].([]any)
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		s, _ := e.(string)
		out = append(out, s)
	}
	return out
}

// §12.1 layer 3, §6.5: the correction is part of the lifecycle a reviewer
// depends on. The holder submits, notices the report is wrong, amends it, and
// the operator approves — and what the approving read returns is the AMENDED
// report, not the one submit wrote.
//
// This sits BESIDE TestLifecycleCreateClaimSubmitApproveThroughRealHerdr
// rather than inside it. That test proves the sequence every task walks;
// amend is a branch off it that only a task whose report was wrong ever takes,
// so folding it in would leave the unamended path — the common one — with no
// layer-3 test of its own, and would make one red test ambiguous between "the
// lifecycle broke" and "amend broke".
func TestLifecycleAmendBeforeApproveThroughRealHerdr(t *testing.T) {
	w := startWorld(t)
	pane := w.pane("amend")
	w.beAgent(pane, "claude", "builder")

	created := w.htask("task", "create", "wire the door", "--validation", "make test: ok")
	id, _ := created["task"].(map[string]any)["id"].(string)

	w.mustInPane(pane, "task", "claim", id)
	w.mustInPane(pane, "task", "submit", id, "--report", "wired it", "--evidence", "make test: FAIL")
	submitted := w.task(id)
	if submitted["status"] != "review" {
		t.Fatalf("after submit the task is %v, want review", submitted["status"])
	}
	if submitted["amend_count"] != nil {
		t.Fatalf("a task that was never amended reports amend_count %v", submitted["amend_count"])
	}
	// Pin what submit wrote before amend touches it. Without this the later
	// assertions cannot tell "amend replaced the report" from "submit never
	// stored one and amend was the first writer".
	if got := submitted["report"]; got != "wired it" {
		t.Fatalf("report after submit = %v, want what submit was given", got)
	}
	if got := evidenceOf(submitted); len(got) != 1 || got[0] != "make test: FAIL" {
		t.Fatalf("evidence after submit = %v, want what submit was given", got)
	}
	submittedAt, submittedBy := submitted["submitted_at"], submitted["submitted_by"]

	// §6.5: the holder corrects the report while the row waits for a reviewer.
	w.mustInPane(pane, "task", "amend", id,
		"--report", "wired it, and the gate is green now",
		"--evidence", "make test: ok")

	amended := w.task(id)
	if amended["status"] != "review" {
		t.Fatalf("amend moved the task to %v, want it still in review", amended["status"])
	}
	if got := amended["report"]; got != "wired it, and the gate is green now" {
		t.Fatalf("report after amend = %v, want the corrected one", got)
	}
	if got := evidenceOf(amended); len(got) != 1 || got[0] != "make test: ok" {
		t.Fatalf("evidence after amend = %v, want the named list to have replaced the old one", got)
	}
	// The submission itself does not move (§6.5).
	if amended["submitted_at"] != submittedAt || amended["submitted_by"] != submittedBy {
		t.Fatalf("amend moved the submission: %v/%v, want %v/%v",
			amended["submitted_at"], amended["submitted_by"], submittedAt, submittedBy)
	}
	if amended["amend_count"] != float64(1) {
		t.Fatalf("amend_count = %v, want 1", amended["amend_count"])
	}

	// §6.5 again, from the other allowed principal: the operator may correct a
	// row it does not hold, and the submission still does not move to them.
	// Layer 1 pins this with a fabricated principal; here both principals are
	// real — one derived from a Herdr pane, one from a terminal.
	w.htask("task", "amend", id, "--report", "wired it, and the gate is green now")

	twice := w.task(id)
	if twice["submitted_by"] != submittedBy || twice["submitted_at"] != submittedAt {
		t.Fatalf("an operator amend moved the submission to %v/%v, want %v/%v",
			twice["submitted_by"], twice["submitted_at"], submittedBy, submittedAt)
	}
	if twice["amend_count"] != float64(2) {
		t.Fatalf("amend_count after a second amend = %v, want 2", twice["amend_count"])
	}
	// A list the caller did not name is left alone (§6.5): the operator passed
	// no --evidence, so the holder's corrected list survives.
	if got := evidenceOf(twice); len(got) != 1 || got[0] != "make test: ok" {
		t.Fatalf("evidence after an amend that named no list = %v, want it untouched", got)
	}

	// The whole point: the approving read sees the correction.
	approved := w.htask("task", "approve", id)
	done, _ := approved["task"].(map[string]any)
	if done == nil {
		t.Fatalf("approve returned %v", approved)
	}
	if done["status"] != "done" {
		t.Fatalf("after approve the task is %v, want done", done["status"])
	}
	if got := done["report"]; got != "wired it, and the gate is green now" {
		t.Fatalf("the approving read saw report %v, want the amended one", got)
	}
	if got := evidenceOf(done); len(got) != 1 || got[0] != "make test: ok" {
		t.Fatalf("the approving read saw evidence %v, want the amended list", got)
	}

	// §5.5: the trail carries the correction, so a reviewer is not told about
	// it in a message.
	events := w.htask("events", "--entity", "task")
	blob := ""
	for _, e := range events["events"].([]any) {
		blob += e.(map[string]any)["kind"].(string) + " "
	}
	for _, k := range []string{"created", "claimed", "submitted", "amended", "approved"} {
		if !strings.Contains(blob, k) {
			t.Fatalf("the event trail is missing %q: %s", k, blob)
		}
	}
}

// §12.1 layer 3, §5.6: the guard that refuses a decision built on a report
// that moved. A reviewer reads a report, decides, and approves with the
// updated_at they read; if an amendment landed in between, the approve must be
// REFUSED rather than land on words the reviewer never saw. Amend moves
// UpdatedAt for exactly this reason (the doc comment on tasks.Amend says so),
// and until now only layers 1 and 2 had seen it do that.
//
// This sits BESIDE TestLifecycleAmendBeforeApproveThroughRealHerdr rather than
// extending it, deliberately. That test's approve is the happy path — the
// reviewer sees the amended report — and it ends `done`. A refusal needs an
// approve that does not land, followed by one that does, so folding both in
// would make one test hold two different questions and make a failure
// ambiguous about which one broke.
func TestStaleApproveIsRefusedAfterAmendThroughRealHerdr(t *testing.T) {
	w := startWorld(t)
	pane := w.pane("stale")
	w.beAgent(pane, "claude", "builder")

	created := w.htask("task", "create", "a report a reviewer will read twice")
	id, _ := created["task"].(map[string]any)["id"].(string)

	w.mustInPane(pane, "task", "claim", id)
	w.mustInPane(pane, "task", "submit", id, "--report", "first draft", "--evidence", "make test: FAIL")

	// What the reviewer read. Everything below turns on this value being the
	// one the row carried BEFORE the amendment.
	submitted := w.task(id)
	stale, ok := submitted["updated_at"].(float64)
	if !ok || stale == 0 {
		t.Fatalf("no updated_at to guard on after submit: %v", submitted["updated_at"])
	}

	w.mustInPane(pane, "task", "amend", id,
		"--report", "second draft, the gate is green now",
		"--evidence", "make test: ok")

	fresh, _ := w.task(id)["updated_at"].(float64)
	if fresh == stale {
		t.Fatalf("amend left updated_at at %v; the guard below has nothing to catch", stale)
	}

	// Half one: the decision built on the replaced report is refused, with the
	// code §5.6 names and the §6.3 status that goes with it.
	doc, status := w.htStatus("task", "approve", id,
		"--base-updated-at", strconv.FormatInt(int64(stale), 10))
	e, _ := doc["error"].(map[string]any)
	if e == nil {
		t.Fatalf("an approve carrying the stale updated_at was accepted: %v", sprint(doc))
	}
	if e["code"] != "CONFLICT" {
		t.Fatalf("the stale approve failed with %v, want CONFLICT (§5.6)", e["code"])
	}
	if status != 6 {
		t.Fatalf("the stale approve exited %d, want 6 for CONFLICT (§6.3)", status)
	}
	if still := w.task(id); still["status"] != "review" {
		t.Fatalf("the refused approve still moved the task to %v, want review", still["status"])
	}

	// Half two: the row was approvable all along — the refusal was the guard
	// doing its job, not the task being stuck. The reviewer re-reads and
	// approves with the value the row carries now.
	doc, status = w.htStatus("task", "approve", id,
		"--base-updated-at", strconv.FormatInt(int64(fresh), 10))
	if status != 0 {
		t.Fatalf("the approve carrying the current updated_at exited %d: %v", status, sprint(doc))
	}
	done, _ := doc["task"].(map[string]any)
	if done == nil || done["status"] != "done" {
		t.Fatalf("after the fresh approve the task is %v, want done", sprint(doc))
	}
	if got := done["report"]; got != "second draft, the gate is green now" {
		t.Fatalf("the approving read saw report %v, want the amended one", got)
	}
}

// §11.5: liveness of an agent principal is Herdr's answer plus its pane
// lifecycle events, and a plugin with leases sweeps them when a pane dies —
// recording the sweep in the entity's events.
//
// This is the MANUAL pass, `htask sweep --pane`, which is what the operator runs
// and what the manifest's own reaction runs for them. It is worth keeping
// separately from the automatic one below: the reaction script exits early
// when Herdr gives it no pane id, and then this is the only way the work comes
// back. The automatic path is
// TestClosingAPaneReleasesItsLeasesWithoutBeingAsked.
func TestLeaseIsReleasedAfterTheClaimingPaneDies(t *testing.T) {
	w := startWorld(t)
	pane := w.pane("lease")
	w.beAgent(pane, "claude", "holder")

	created := w.htask("task", "create", "held by a pane that dies")
	id, _ := created["task"].(map[string]any)["id"].(string)
	w.mustInPane(pane, "task", "claim", id)

	w.herdr("pane", "close", pane)
	for _, p := range w.herdr("pane", "list")["panes"].([]any) {
		if p.(map[string]any)["pane_id"] == pane {
			t.Fatalf("herdr still lists the closed pane %s", pane)
		}
	}

	swept := w.htask("sweep", "--pane", pane)
	released, _ := swept["released"].([]any)
	if len(released) != 1 || released[0] != id {
		t.Fatalf("the sweep released %v, want just %s", released, id)
	}
	back := w.task(id)
	if back["status"] != "todo" {
		t.Fatalf("the task is %v after its pane died, want todo", back["status"])
	}
	if back["claimed_by"] != nil {
		t.Fatalf("the lease survived the pane: claimed_by = %v", back["claimed_by"])
	}

	var kinds []string
	for _, e := range w.htask("events", "--entity", "task")["events"].([]any) {
		kinds = append(kinds, e.(map[string]any)["kind"].(string))
	}
	found := false
	for _, k := range kinds {
		if k == "swept" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the sweep left no trail: %v", kinds)
	}
}

// §11.2: feature detection reads what THIS Herdr says it can do, and never
// pins a protocol number. Against the real binary the two calls the plugin
// makes must both be listed.
func TestDoctorSeesTheRealHerdrAndItsSchema(t *testing.T) {
	w := startWorld(t)
	doc := w.htask("doctor")
	body, _ := doc["doctor"].(map[string]any)
	if body == nil {
		body = doc
	}
	blob := strings.ToLower(sprint(body))
	for _, want := range []string{"herdr", "contract"} {
		if !strings.Contains(blob, want) {
			t.Fatalf("doctor said nothing about %q: %s", want, blob)
		}
	}
}

// §12.3: a suite that leaves processes behind has not stayed out of the
// operator's way. Every `htask` call autostarts a detached daemon (§2.2) that
// nothing else stops, so the world must stop them itself — and prove it did,
// because the failure mode is invisible until the machine has a dozen of them.
func TestNoDaemonThisSuiteStartedSurvivesIt(t *testing.T) {
	w := startWorld(t)
	w.htask("task", "create", "a task, which starts a daemon")
	started := w.daemonPIDs()
	if len(started) == 0 {
		t.Fatal("the CLI did not autostart a daemon; this test would prove nothing")
	}

	w.stop()

	if left := w.daemonPIDs(); len(left) != 0 {
		t.Fatalf("the world left %d daemon(s) running: %v (started: %v)", len(left), left, started)
	}
}

// §5.9 through the whole stack: the shipped binary, a real daemon and a real
// Herdr. The unit tests hold the state machine's refusal; this holds the part
// a caller actually sees — the §6.3 exit status — because a bound that
// answers USAGE in a Go test but exits 0 at the shell is not enforced.
func TestFreeTextBoundsAreEnforcedThroughTheRealStack(t *testing.T) {
	w := startWorld(t)
	pane := w.pane("bounds")
	w.beAgent(pane, "claude", "builder")

	huge := strings.Repeat("x", 100_000)
	doc, status := w.htStatus("task", "create", huge)
	if status != 2 {
		t.Fatalf("an over-long title exited %d, want 2 (USAGE, §6.3): %v", status, doc)
	}
	body, _ := doc["error"].(map[string]any)
	if body == nil || body["code"] != "USAGE" {
		t.Fatalf("error = %v, want a USAGE envelope (§6.2)", doc)
	}
	msg, _ := body["message"].(string)
	if !strings.Contains(msg, "title") || !strings.Contains(msg, "200") {
		t.Fatalf("the message must name the field and the limit: %q", msg)
	}

	// The bound must not have made ordinary work harder: everything the rest
	// of this suite creates is well inside it.
	created := w.htask("task", "create", "an ordinary title",
		"--validation", "make test-full exits 0")
	if created["task"] == nil {
		t.Fatalf("a normal create was refused: %v", created)
	}
	id, _ := created["task"].(map[string]any)["id"].(string)
	w.mustInPane(pane, "task", "claim", id)
	w.mustInPane(pane, "task", "submit", id, "--report", "did it", "--evidence", "make test-full: exit 0")
	if s := w.task(id)["status"]; s != "review" {
		t.Fatalf("a submission inside the bounds did not reach review: %v", s)
	}
}

// §8.4 / §11.5: the manifest promises that a pane going away gives its work
// back by itself, and until now nothing checked. This closes a pane through
// Herdr and waits for the claim to come back WITHOUT running `htask sweep --pane`
// — if it comes back, the reaction Herdr registered from the manifest is what
// brought it.
func TestClosingAPaneReleasesItsLeasesWithoutBeingAsked(t *testing.T) {
	w := startWorld(t)
	w.linkPlugin()
	pane := w.pane("automatic")
	w.beAgent(pane, "claude", "holder")

	created := w.htask("task", "create", "held by a pane that is about to close")
	id, _ := created["task"].(map[string]any)["id"].(string)
	w.mustInPane(pane, "task", "claim", id)
	if held := w.task(id); held["claimed_by"] != "agent:"+pane {
		t.Fatalf("precondition: the pane holds it; claimed_by = %v", held["claimed_by"])
	}

	w.herdr("pane", "close", pane)

	deadline := time.Now().Add(20 * time.Second)
	var back map[string]any
	for time.Now().Before(deadline) {
		back = w.task(id)
		if back["status"] == "todo" && back["claimed_by"] == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if back["status"] != "todo" || back["claimed_by"] != nil {
		logs, _ := w.tryHerdr("plugin", "log", "list")
		t.Fatalf("the manifest's pane reaction did not release the lease within 20s: %v\nplugin logs: %v", back, logs)
	}

	var kinds []string
	for _, e := range w.htask("events", "--entity", "task")["events"].([]any) {
		kinds = append(kinds, e.(map[string]any)["kind"].(string))
	}
	found := false
	for _, k := range kinds {
		if k == "swept" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the automatic release left no trail: %v", kinds)
	}
}
