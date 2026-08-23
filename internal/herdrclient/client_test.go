package herdrclient

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/husniadil/herdr-tasks/internal/codes"
	"github.com/husniadil/herdr-tasks/internal/testenv"
)

// §3.4: the three facts of an agent principal come from Herdr, through
// HERDR_BIN_PATH — never from argv or a process name.
func TestAgentGetSnapshotsHerdrFacts(t *testing.T) {
	testenv.SkipUnlessFull(t)
	c := New(testenv.FakeHerdr(t))
	got, err := c.AgentGet("wF:p1")
	if err != nil {
		t.Fatalf("AgentGet: %v", err)
	}
	if got.Name != "builder" || got.Harness != "claude" || got.Session != "sess-wF:p1" {
		t.Fatalf("snapshot = %+v", got)
	}
}

// §3.4: harness is "unknown" when Herdr has no answer, never a guess.
func TestAgentGetUnknownPaneIsUnknownHarness(t *testing.T) {
	testenv.SkipUnlessFull(t)
	c := New(testenv.FakeHerdr(t))
	got, err := c.AgentGet("wF:gone")
	if err != nil {
		t.Fatalf("a pane Herdr does not know is not an error here: %v", err)
	}
	if got.Harness != "unknown" {
		t.Fatalf("harness = %q, want unknown", got.Harness)
	}
}

// §11.2: feature-detect by reading the schema once; never pin a protocol
// number.
func TestSchemaListsCapabilities(t *testing.T) {
	testenv.SkipUnlessFull(t)
	c := New(testenv.FakeHerdr(t))
	sc, err := c.Schema()
	if err != nil {
		t.Fatalf("Schema: %v", err)
	}
	if !sc.Has("agent.get") {
		t.Fatalf("schema = %+v, want agent.get", sc)
	}
	// Herdr spells its event kinds with underscores; the contract writes dots.
	// Both must match — see docs/contract-notes.md.
	if !sc.Has("pane.exited") || !sc.Has("pane_exited") {
		t.Fatalf("schema = %+v, want the pane.exited event under either spelling", sc)
	}
	if sc.Protocol != 19 {
		t.Fatalf("protocol = %d; read for doctor only, never decided on (§11.2)", sc.Protocol)
	}
	if sc.Has("agent.teleport") {
		t.Fatal("a capability Herdr did not list must not be claimed")
	}
}

// §11.1 / §6.3: no herdr binary is UNAVAILABLE, named.
func TestMissingBinaryIsUnavailable(t *testing.T) {
	testenv.SkipUnlessFull(t)
	c := New(testenv.MissingHerdr(t))
	_, err := c.Schema()
	ce, ok := err.(*codes.Error)
	if !ok || ce.Code != codes.Unavailable {
		t.Fatalf("err = %v, want UNAVAILABLE", err)
	}
}

// §11.2: a verb needing a capability Herdr lacks is UNSUPPORTED, with the
// capability named — not a refusal to start.
func TestRequireNamesTheMissingCapability(t *testing.T) {
	testenv.SkipUnlessFull(t)
	c := New(testenv.FakeHerdr(t))
	if err := c.Require("agent.get"); err != nil {
		t.Fatalf("Require: %v", err)
	}
	err := c.Require("agent.teleport")
	ce, ok := err.(*codes.Error)
	if !ok || ce.Code != codes.Unsupported {
		t.Fatalf("err = %v, want UNSUPPORTED", err)
	}
	if !contains(ce.Message, "agent.teleport") {
		t.Fatalf("message %q does not name the capability", ce.Message)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// flatHerdr is a Herdr that prints the agent object bare, with agent_session
// as a plain string. Nothing ships that shape today; the parser carries a
// fallback for it (§11.2 is feature detection, not shape pinning), and a
// fallback that has never been exercised is not a fallback.
func flatHerdr(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "herdr")
	script := `#!/bin/sh
case "$1 $2" in
  "agent get") printf '{"pane_id":"%s","name":"builder","agent":"claude","agent_status":"working","agent_session":"sess-%s"}\n' "$3" "$3" ;;
  *) echo "unsupported: $*" >&2; exit 64 ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write flat herdr: %v", err)
	}
	return path
}

// §3.4: the bare object is read too, and its string agent_session survives.
// The struct-typed session field made this decode fail as a type error, which
// the caller turned into harness "unknown" — a silent fallback over a fact
// Herdr had actually answered.
func TestAgentGetReadsTheBareObjectWithAStringSession(t *testing.T) {
	testenv.SkipUnlessFull(t)
	got, err := New(flatHerdr(t)).AgentGet("wF:p1")
	if err != nil {
		t.Fatalf("AgentGet: %v", err)
	}
	if got.Harness != "claude" {
		t.Fatalf("harness = %q, want claude — the bare shape was dropped", got.Harness)
	}
	if got.Name != "builder" || got.Session != "sess-wF:p1" {
		t.Fatalf("snapshot = %+v", got)
	}
}

// harnesslessHerdr answers with the name and the session but no `agent`: a
// pane whose harness Herdr has no answer for yet.
func harnesslessHerdr(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "herdr")
	script := `#!/bin/sh
case "$1 $2" in
  "agent get") printf '{"result":{"agent":{"pane_id":"%s","name":"builder","agent_status":"working","agent_session":{"value":"sess-%s"}}}}\n' "$3" "$3" ;;
  *) echo "unsupported: $*" >&2; exit 64 ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write herdr: %v", err)
	}
	return path
}

// §3.4: "unknown" is what Herdr could not say about the HARNESS, not an
// instruction to forget the name and the session it did say. Discarding them
// left the board unable to name the peer that claimed, and CheckRecusal's
// session rule with nothing to compare.
func TestAgentGetKeepsTheNameAndSessionOfAnUnknownHarness(t *testing.T) {
	testenv.SkipUnlessFull(t)
	got, err := New(harnesslessHerdr(t)).AgentGet("wF:p1")
	if err != nil {
		t.Fatalf("AgentGet: %v", err)
	}
	if got.Harness != "unknown" {
		t.Fatalf("harness = %q, want unknown", got.Harness)
	}
	if got.Name != "builder" || got.Session != "sess-wF:p1" || got.PaneID != "wF:p1" {
		t.Fatalf("an unknown harness threw away what Herdr did answer: %+v", got)
	}
}
