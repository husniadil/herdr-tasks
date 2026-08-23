package mcpdoor

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/husniadil/herdr-tasks/internal/protocol"
)

// TestAsStaysOffTheMCPDoor pins the surface §7.3 promises, not the intent
// behind it. §7.3 says --as is an identity claim carried BY a call, which a
// long-lived door may not read, so the word must not be reachable through this
// door at all: not as a tool, and not as an argument of one. The registry's
// Excluded reason records WHY, and cmd/htask/render_test.go asserts only that
// every global is either mapped or excluded — an edit that moved "as" from
// Excluded to Property would satisfy both and publish the argument anyway.
// This test reads the SERVED tool list off a live session, so what it inspects
// is what a caller would actually receive.
func TestAsStaysOffTheMCPDoor(t *testing.T) {
	unreachable := func(protocol.Request) (json.RawMessage, error) {
		t.Error("the daemon was called; this test only reads the served surface")
		return nil, nil
	}
	sess := mcpSession(t, unreachable)

	tools, err := sess.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools.Tools) == 0 {
		t.Fatal("no tools served; the test would pass on an empty door")
	}
	for _, tl := range tools.Tools {
		if tl.Name == "as" {
			t.Errorf("the door serves a tool named %q; §7.3 keeps the identity claim off this surface", tl.Name)
		}
		raw, err := json.Marshal(tl.InputSchema)
		if err != nil {
			t.Fatalf("marshal the schema of %q: %v", tl.Name, err)
		}
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("read the schema of %q: %v", tl.Name, err)
		}
		if len(schema.Properties) == 0 && !strings.Contains(string(raw), "properties") {
			t.Fatalf("tool %q publishes no properties; the schema is not being read", tl.Name)
		}
		if _, ok := schema.Properties["as"]; ok {
			t.Errorf("tool %q offers %q as an argument; §7.3 says a door's identity comes from "+
				"how it was started, never from a call", tl.Name, "as")
		}
	}
}
