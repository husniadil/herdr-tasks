package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/husniadil/herdr-tasks/internal/verbs"
)

// find walks the tree for one space-separated path.
func find(root *cobra.Command, path string) *cobra.Command {
	cmd := root
	for _, name := range strings.Fields(path) {
		var next *cobra.Command
		for _, sub := range cmd.Commands() {
			if sub.Name() == name {
				next = sub
				break
			}
		}
		if next == nil {
			return nil
		}
		cmd = next
	}
	return cmd
}

// taskVerbs is the task group as the REGISTRY declares it, not as this test
// remembers it: the operator's decision was "every verb of the task group",
// and a hand-written list here would stop covering the group the moment one
// is added.
func taskVerbs(t *testing.T) []verbs.Verb {
	t.Helper()
	var out []verbs.Verb
	for _, v := range verbs.All {
		if strings.HasPrefix(v.Name, "task.") {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		t.Fatal("no task verbs in the registry; this test would pass on an empty CLI")
	}
	return out
}

// The flattening itself: `htask claim 5`, not `htask task claim 5`.
func TestEveryTaskVerbIsATopLevelCommand(t *testing.T) {
	root := newRootCmd()
	for _, v := range taskVerbs(t) {
		name := strings.TrimPrefix(v.Name, "task.")
		cmd := find(root, name)
		if cmd == nil {
			t.Errorf("`htask %s` is not on the CLI; the task group's verbs hoist to the top level", name)
			continue
		}
		if cmd.Hidden {
			t.Errorf("`htask %s` is hidden; the new form is the one --help teaches", name)
		}
	}
}

// The transition window: the old form keeps answering for the sibling
// adapters and for muscle memory, and stays out of --help so nothing learns
// it from here.
func TestTheOldTaskFormsSurviveAsHiddenAliases(t *testing.T) {
	root := newRootCmd()
	group := find(root, "task")
	if group == nil {
		t.Fatal("`htask task` is gone; the old forms answer for one transition window")
	}
	if !group.Hidden {
		t.Error("the `task` alias group is in --help; it is a transition alias, not a taught form")
	}
	for _, v := range taskVerbs(t) {
		name := strings.TrimPrefix(v.Name, "task.")
		cmd := find(root, "task "+name)
		if cmd == nil {
			t.Errorf("`htask task %s` no longer answers; the alias window is not over", name)
			continue
		}
		if !cmd.Hidden {
			t.Errorf("`htask task %s` is in --help; aliases are hidden", name)
		}
	}
}

// The note group is deliberately NOT flattened: `htask note add` stays a
// group spelled with a space, and no underscore form appears on the CLI.
func TestTheNoteGroupStaysAGroup(t *testing.T) {
	root := newRootCmd()
	group := find(root, "note")
	if group == nil || group.Hidden {
		t.Fatal("`htask note` must stay a visible group")
	}
	if find(root, "note add") == nil {
		t.Error("`htask note add` is gone")
	}
	for _, sub := range root.Commands() {
		if strings.Contains(sub.Name(), "_") {
			t.Errorf("`htask %s` is an underscore form; note_add is the MCP tool name and only that", sub.Name())
		}
	}
}

// The MCP fingerprint does not move. §7.1's tool names are the agent surface's
// contract, and this change is the CLI's shape alone.
func TestFlatteningLeavesTheMCPToolNamesAlone(t *testing.T) {
	want := map[string]string{
		"task.create": "create", "task.get": "get", "task.list": "list",
		"task.claim": "claim", "task.touch": "touch", "task.release": "release",
		"task.submit": "submit", "task.amend": "amend", "task.approve": "approve",
		"task.reject": "reject", "task.cancel": "cancel", "task.update": "update",
		"task.delete": "delete", "task.archive": "archive", "task.goal": "goal",
		"note.add": "note_add", "note.promote": "note_promote",
	}
	got := map[string]string{}
	for _, v := range verbs.All {
		got[v.Name] = v.MCP
	}
	for name, mcp := range want {
		if got[name] != mcp {
			t.Errorf("the MCP tool for %s is %q, want %q; §7.1 names do not move with the CLI's shape", name, got[name], mcp)
		}
	}
}

// The operator's decision says "no collision exists with the system verbs;
// refuse at startup if one ever appears". newRootCmd is that startup.
func TestATaskVerbCollidingWithASystemVerbIsRefused(t *testing.T) {
	system := map[string]bool{}
	for _, v := range verbs.All {
		if len(v.CLI) == 1 && !strings.HasPrefix(v.Name, "task.") {
			system[v.CLI[0]] = true
		}
	}
	for _, name := range []string{"daemon", "mcp", "tui", "version", "note", "parked", "task"} {
		system[name] = true
	}
	for _, v := range taskVerbs(t) {
		name := strings.TrimPrefix(v.Name, "task.")
		if system[name] {
			t.Errorf("the task verb %q collides with a system name; newRootCmd refuses this at startup", name)
		}
	}
}
