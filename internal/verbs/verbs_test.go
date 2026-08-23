package verbs

import "testing"

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
	if want := 12; ungated != want {
		t.Errorf("%d writing verbs are outside the policy gate, was %d — if that is intended, say so here", ungated, want)
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
