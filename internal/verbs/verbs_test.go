package verbs

import (
	"strings"
	"testing"
)

// §9.4 with §3.1: every verb that writes states who may call it, and either
// carries a gate name or says why it does not. Before this, eleven Mutates
// verbs had an empty Gated under a comment claiming empty meant "changes
// nothing" — so the three most destructive verbs in the plugin were invisible
// to any policy the operator might write, and nothing in the registry said so.
func TestEveryWritingVerbStatesItsRuleAndItsGate(t *testing.T) {
	ungated := 0
	for _, v := range All {
		if !v.Mutates {
			if v.Gated != "" {
				t.Errorf("%s is gated but does not write: the gate is for writes (§9.4)", v.Name)
			}
			if v.Ungated != "" {
				t.Errorf("%s records why it is ungated but does not write", v.Name)
			}
			continue
		}
		if v.Who == "" {
			t.Errorf("%s writes and does not say who may call it (§3.1)", v.Name)
		}
		switch {
		case v.Gated != "" && v.Ungated != "":
			t.Errorf("%s is gated as %q AND says why it is not gated", v.Name, v.Gated)
		case v.Gated == "" && v.Ungated == "":
			t.Errorf("%s writes, is not offered to the policy gate, and does not say why (§9.4)", v.Name)
		case v.Gated == "":
			ungated++
		}
	}
	// The count is pinned so that adding an ungated writing verb is a
	// deliberate edit to this number, read by a reviewer, rather than a line
	// that slips in.
	if want := 11; ungated != want {
		t.Errorf("%d writing verbs are outside the policy gate, was %d — if that is intended, say so here", ungated, want)
	}
}

// §9.4: the two verbs that destroy a stored entity outright are offered to the
// gate. Everything else that writes either moves a task through the state
// machine or is reversible; a delete removes the entity and its history from
// the board and nothing puts it back. While these were Ungated no policy the
// operator could write was able to hold an agent back from hard-deleting a
// never-claimed task or an inbox note on any project's board — the gate was
// not consulted at all, so a deny it never saw could not have applied.
func TestTheDestructiveVerbsPassThroughTheGate(t *testing.T) {
	want := map[string]string{
		"task.delete": "tasks.delete",
		"note.delete": "tasks.note_delete",
	}
	for name, gated := range want {
		v, ok := ByName(name)
		if !ok {
			t.Fatalf("%s is not in the registry", name)
		}
		if v.Gated != gated {
			t.Errorf("%s is gated as %q, want %q: a hard delete no policy can see is a hard delete no policy can deny (§9.4)", name, v.Gated, gated)
		}
	}
}

// §9.4: an Ungated reason has to say why THIS verb carries no gate. A reason
// two verbs share is a reason about a class, and a class reason is disproved
// the moment a sibling in the same class is gated: note.keep and note.drop
// once both read "already the narrowest principal there is; a gate cannot
// narrow it further", which is equally true of note.promote — gated, because
// a deny-only policy can still hold a freeze over it.
func TestEachUngatedReasonIsAboutItsOwnVerb(t *testing.T) {
	seen := map[string]string{}
	for _, v := range All {
		if v.Ungated == "" {
			continue
		}
		if first, dup := seen[v.Ungated]; dup {
			t.Errorf("%s and %s give the same reason for carrying no gate (%q): a shared reason is about a class, and says nothing about why these two are outside the gate when a sibling is inside it", first, v.Name, v.Ungated)
			continue
		}
		seen[v.Ungated] = v.Name
	}
}

// §7.3 (0.10.0): there is no CLI-only verb left to explain. The eight that
// recorded a reason for their absence all gave a form of "this authority is
// the operator's", and §3.7 turned that authority into advice an agent
// confirms rather than a refusal a door makes — so the reasons lost their
// basis and the verbs came to the door. This replaces TestEveryCLIOnlyVerbSaysWhy,
// which asked every absent verb to say why: nothing may be absent now.
func TestEveryVerbIsOnBothDoors(t *testing.T) {
	for _, v := range All {
		if v.MCP == "" {
			t.Errorf("verb %q is on the CLI and not on the MCP door; §7.3 admits no class of verb that belongs to one door", v.Name)
		}
		if len(v.CLI) == 0 {
			t.Errorf("verb %q is on the MCP door and not on the CLI; parity fails in both directions", v.Name)
		}
	}
	if len(MCPTools()) != len(All) {
		t.Fatalf("%d tools for %d verbs; §7.3 wants every one", len(MCPTools()), len(All))
	}
}

// §3.7 (0.10.0): the principal rule has nowhere left to be enforced for an
// operator verb and everywhere to be read, so it is rendered rather than kept
// as registry metadata — in ONE text, because a human reading `--help` and an
// agent reading a tool description must be told the same thing. This fails if
// a door goes back to showing Short alone.
func TestHelpCarriesTheWhoRuleForEveryVerb(t *testing.T) {
	for _, v := range All {
		help := v.Help()
		if !strings.Contains(help, v.Short) {
			t.Errorf("%s: help drops the one-line summary", v.Name)
		}
		if v.Who != "" && !strings.Contains(help, v.Who) {
			t.Errorf("%s: help does not say who may call it; the rule is only useful where it is read", v.Name)
		}
		if v.Long != "" && !strings.Contains(help, v.Long) {
			t.Errorf("%s: help drops the longer prose", v.Name)
		}
		if strings.Contains(strings.ToLower(help), "operator only") {
			t.Errorf("%s: %q still reads as a refusal; an operator verb is advice an agent confirms (§3.7)", v.Name, help)
		}
	}
}
