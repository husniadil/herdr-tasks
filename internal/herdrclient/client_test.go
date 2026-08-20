package herdrclient

import (
	"testing"

	"github.com/husniadil/herdr-tasks/internal/codes"
	"github.com/husniadil/herdr-tasks/internal/testenv"
)

// §3.4: the three facts of an agent principal come from Herdr, through
// HERDR_BIN_PATH — never from argv or a process name.
func TestAgentGetSnapshotsHerdrFacts(t *testing.T) {
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
	c := New(testenv.FakeHerdr(t))
	sc, err := c.Schema()
	if err != nil {
		t.Fatalf("Schema: %v", err)
	}
	if !sc.Has("agent.get") {
		t.Fatalf("schema = %+v, want agent.get", sc)
	}
	if sc.Has("agent.teleport") {
		t.Fatal("a capability Herdr did not list must not be claimed")
	}
}

// §11.1 / §6.3: no herdr binary is UNAVAILABLE, named.
func TestMissingBinaryIsUnavailable(t *testing.T) {
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
