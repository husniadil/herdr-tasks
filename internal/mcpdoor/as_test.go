package mcpdoor

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/husniadil/herdr-tasks/internal/protocol"
	"github.com/husniadil/herdr-tasks/internal/verbs"
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

// §7.3: "A plugin MUST pin the totality with a test that fails when any verb
// its CLI serves is absent from its door." Nothing did. The audit behind task
// 86 dropped a verb from the loop in New that adds the tools and every §7.3
// test stayed green, because TestEveryCLIVerbReachesTheMCPDoor reads
// verbs.MCPTools() towards verbs.All — two registry-side lists, neither of
// them the door. A registry entry is the INTENT to serve a verb; what a
// harness can reach is the served tool list, and only that answers the MUST.
//
// So this reads the same live session as the --as pin above, and names every
// verb it cannot find rather than counting: a count that matches for two
// compensating edits says nothing about which verb went missing.
func TestEveryCLIVerbIsServedByTheDoor(t *testing.T) {
	unreachable := func(protocol.Request) (json.RawMessage, error) {
		t.Error("the daemon was called; this test only reads the served surface")
		return nil, nil
	}
	sess := mcpSession(t, unreachable)

	tools, err := sess.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	served := map[string]bool{}
	for _, tl := range tools.Tools {
		served[tl.Name] = true
	}
	if len(verbs.All) == 0 {
		t.Fatal("the verb registry is empty; this test would pass on an empty door")
	}
	for _, v := range verbs.All {
		if !served[v.MCP] {
			t.Errorf("the CLI serves %q and the door does not serve %q; §7.3 admits no CLI-only verb",
				v.Name, v.MCP)
		}
	}
	if len(served) != len(verbs.All) {
		t.Errorf("the door serves %d tools for %d CLI verbs; parity is over the whole surface",
			len(served), len(verbs.All))
	}
}

// §7.3 put every verb on this door, and the served instructions kept telling
// agents the opposite: "the CLI (`htask`) carries every verb, including the
// ones missing here". That sentence is prose an agent READS and acts on — a
// harness with only this door would believe some work needed a shell it does
// not have — so it needs a pin, not a comment. The audit named it and left it
// for this task; this is the test that keeps it corrected.
//
// It reads the instructions off the live session rather than the constant,
// because what a caller receives is the initialize result, and it asserts on
// the CLAIM rather than on an exact sentence: any wording saying verbs are
// missing from this door fails, whatever it is spelled like.
func TestTheInstructionsDoNotSendAgentsToTheCLIForMissingVerbs(t *testing.T) {
	unreachable := func(protocol.Request) (json.RawMessage, error) {
		t.Error("the daemon was called; this test only reads the served surface")
		return nil, nil
	}
	sess := mcpSession(t, unreachable)

	served := sess.InitializeResult().Instructions
	if served == "" {
		t.Fatal("the door serves no instructions; the test would pass on an empty string")
	}
	if served != Instructions {
		t.Fatalf("the served instructions are not mcpdoor.Instructions; this test would be "+
			"reading the wrong text\nserved: %q", served)
	}
	for _, stale := range []string{"missing here", "missing from this door", "including the ones"} {
		if strings.Contains(served, stale) {
			t.Errorf("the served instructions say %q; every verb is on this door (§7.3) and "+
				"TestEveryCLIVerbIsServedByTheDoor holds it, so telling an agent otherwise is "+
				"a false claim in text it acts on", stale)
		}
	}
}
