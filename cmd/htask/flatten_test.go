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
	// Every depth, not just the root: an underscore form nested under a group
	// is still an underscore form on the CLI, and `note note_add` would be a
	// second spelling of the same verb on the same surface.
	var walk func(cmd *cobra.Command, prefix string)
	walk = func(cmd *cobra.Command, prefix string) {
		for _, sub := range cmd.Commands() {
			path := strings.TrimSpace(prefix + " " + sub.Name())
			if strings.Contains(sub.Name(), "_") {
				t.Errorf("`htask %s` is an underscore form; note_add is the MCP tool name (§7.1) and only that", path)
			}
			walk(sub, path)
		}
	}
	walk(root, "")
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

// The refusal itself, driven by a table that HAS the collision the registry
// does not: cobra's own answer to two commands of one name is to accept both
// and serve whichever it finds first, so the second verb is unreachable and
// nothing says so. Building the tree must stop instead.
func TestBuildingTheTreeOnACollidingRegistryStops(t *testing.T) {
	// The two halves of the refusal are reached by ORDER, not by name: the
	// planted verb after the registry meets a name already taken, and the
	// planted verb before it makes the grouping parent the one that arrives
	// second. Both halves have to stop, or one of them is a branch nothing
	// runs.
	for _, tc := range []struct {
		name, why string
		first     bool
	}{
		{name: "doctor", why: "a flat verb against a system verb"},
		{name: "note", why: "a flat verb against a grouping parent already built"},
		{name: "note", why: "a grouping parent against a flat verb already built", first: true},
	} {
		t.Run(tc.why, func(t *testing.T) {
			planted := verbs.Verb{
				Name: "task.collide", CLI: []string{tc.name}, MCP: "collide",
				Short: "a verb planted to collide: " + tc.why,
			}
			var list []verbs.Verb
			if tc.first {
				list = append([]verbs.Verb{planted}, verbs.All...)
			} else {
				list = append(append([]verbs.Verb{}, verbs.All...), planted)
			}
			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("newRootCmdFrom accepted %q twice on the CLI; %s must stop at startup, "+
						"because cobra serves the first and the second is unreachable with nothing said", tc.name, tc.why)
				}
				if msg, ok := r.(string); !ok || !strings.Contains(msg, tc.name) {
					t.Errorf("the refusal does not name the colliding command: %v", r)
				}
			}()
			newRootCmdFrom(list)
		})
	}
}

// And the registry as it actually stands has no collision to refuse, which is
// the operator's "no collision exists with the system verbs".
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
