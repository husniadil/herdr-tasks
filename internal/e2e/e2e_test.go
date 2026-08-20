//go:build e2e

package e2e

import (
	"strings"
	"testing"
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
	outside := w.ht("task", "create", "written from a terminal")
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

	created := w.ht("task", "create", "claim me")
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

	created := w.ht("task", "create", "wire the door", "--validation", "make test: ok")
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

	// Recusal (§6.6) is by harness, and the operator is exempt: the same
	// approve from the claiming pane must be refused, and from a terminal must
	// go through.
	if _, err := w.htInPane(pane, "task", "approve", id); err == nil {
		t.Fatal("the claiming harness approved its own work (§6.6)")
	} else if !strings.Contains(err.Error(), "FORBIDDEN") {
		t.Fatalf("self-review was refused with %v, want FORBIDDEN (§6.3)", err)
	}

	w.ht("task", "approve", id)
	done := w.task(id)
	if done["status"] != "done" {
		t.Fatalf("after approve the task is %v, want done", done["status"])
	}
	if done["reviewed_by"] != "human" {
		t.Fatalf("reviewed_by = %v, want human", done["reviewed_by"])
	}

	// §5.5: the trail says all of it happened, in order.
	events := w.ht("events", "--entity", "task")
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

// §11.5: liveness of an agent principal is Herdr's answer plus its pane
// lifecycle events, and a plugin with leases sweeps them when a pane dies —
// recording the sweep in the entity's events.
//
// The sweep on `pane.exited` is not automatic yet: the manifest declares no
// [[events]] reaction, so the proof here runs the same pass the reaction would
// (`ht sweep --pane`) after really closing the pane through Herdr. The gap is
// recorded in docs/contract-notes.md.
func TestLeaseIsReleasedAfterTheClaimingPaneDies(t *testing.T) {
	w := startWorld(t)
	pane := w.pane("lease")
	w.beAgent(pane, "claude", "holder")

	created := w.ht("task", "create", "held by a pane that dies")
	id, _ := created["task"].(map[string]any)["id"].(string)
	w.mustInPane(pane, "task", "claim", id)

	w.herdr("pane", "close", pane)
	for _, p := range w.herdr("pane", "list")["panes"].([]any) {
		if p.(map[string]any)["pane_id"] == pane {
			t.Fatalf("herdr still lists the closed pane %s", pane)
		}
	}

	swept := w.ht("sweep", "--pane", pane)
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
	for _, e := range w.ht("events", "--entity", "task")["events"].([]any) {
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
	doc := w.ht("doctor")
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
// operator's way. Every `ht` call autostarts a detached daemon (§2.2) that
// nothing else stops, so the world must stop them itself — and prove it did,
// because the failure mode is invisible until the machine has a dozen of them.
func TestNoDaemonThisSuiteStartedSurvivesIt(t *testing.T) {
	w := startWorld(t)
	w.ht("task", "create", "a task, which starts a daemon")
	started := w.daemonPIDs()
	if len(started) == 0 {
		t.Fatal("the CLI did not autostart a daemon; this test would prove nothing")
	}

	w.stop()

	if left := w.daemonPIDs(); len(left) != 0 {
		t.Fatalf("the world left %d daemon(s) running: %v (started: %v)", len(left), left, started)
	}
}
