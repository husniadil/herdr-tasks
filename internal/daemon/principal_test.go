package daemon

import (
	"errors"
	"strings"
	"testing"

	"github.com/husniadil/herdr-tasks/internal/codes"
	"github.com/husniadil/herdr-tasks/internal/protocol"
	"github.com/husniadil/herdr-tasks/internal/tasks"
)

// TestPaneMayNotDeclareAPluginPrincipal pins the narrowing of the plugin arm
// of §3.2: a pane already HAS a derived principal, and a principal you can
// derive is not one you may declare. Task 81: a caller that claimed as
// plugin:hdis from a pane matched every holder guard, so the board credited a
// plugin with an agent's work.
func TestPaneMayNotDeclareAPluginPrincipal(t *testing.T) {
	d := &Daemon{}
	_, err := d.actor(protocol.Request{PaneID: "wF:p1", As: "plugin:hdis"})
	if err == nil {
		t.Fatal("a request carrying a pane and --as plugin:hdis was accepted")
	}
	var ce *codes.Error
	if !errors.As(err, &ce) || ce.Code != codes.Forbidden {
		t.Fatalf("error = %v, want a %s", err, codes.Forbidden)
	}
}

// TestPanelessPluginPrincipalStaysAccepted holds the case task 67 protected:
// a plugin declaring its principal from a process with no pane is how a
// sibling plugin calls the board at all. This test fails if the refusal above
// widens onto it.
func TestPanelessPluginPrincipalStaysAccepted(t *testing.T) {
	d := &Daemon{}
	a, err := d.actor(protocol.Request{As: "plugin:hdis"})
	if err != nil {
		t.Fatalf("paneless --as plugin:hdis was refused: %v", err)
	}
	if a.Principal != tasks.Principal("plugin:hdis") {
		t.Fatalf("principal = %s, want plugin:hdis", a.Principal)
	}
	// cron and trigger are declared the same way and from the same kind of
	// paneless process; neither is narrowed by this change.
	for _, as := range []string{"cron:nightly", "trigger:webhook"} {
		if _, err := d.actor(protocol.Request{As: as}); err != nil {
			t.Fatalf("paneless --as %s was refused: %v", as, err)
		}
	}
}

// TestPaneDeclaringAPluginNamesWhatToDoInstead is the shape task 80 gave
// notHolder. The worker that started this reached for --as because a
// FORBIDDEN named a principal and it read that as an instruction; a refusal
// that only says no is what produces the next one.
func TestPaneDeclaringAPluginNamesWhatToDoInstead(t *testing.T) {
	d := &Daemon{}
	_, err := d.actor(protocol.Request{PaneID: "wF:p1", As: "plugin:hdis"})
	if err == nil {
		t.Fatal("want a refusal")
	}
	msg := err.Error()
	for _, want := range []string{
		"wF:p1",       // who the caller actually is
		"plugin:hdis", // what they tried to declare
		"agent:wF:p1", // the principal they already have
		"drop `--as`", // what to do instead
		"§3.2",
	} {
		if !strings.Contains(strings.ToLower(msg), strings.ToLower(want)) {
			t.Errorf("refusal does not name %q: %s", want, msg)
		}
	}
}

// TestClaimAndSubmitAsAPluginFromAPaneNoLongerReproduces drives BOTH verbs of
// the original incident. The hole was that neither guard fired alone: a caller
// that claimed as plugin:X and submitted as plugin:X was the holder by the
// guard's own test, so notHolder was never reached.
func TestClaimAndSubmitAsAPluginFromAPaneNoLongerReproduces(t *testing.T) {
	d := newDaemon(t, nil)
	id := createTask(t, d, "work with an owner").Task.ID

	claim := mustFail(t, d, protocol.Request{Verb: "task.claim",
		PaneID: "wF:p1", As: "plugin:hdis", Args: map[string]any{"id": id}}, codes.Forbidden)
	if !strings.Contains(claim.Message, "agent:wF:p1") {
		t.Errorf("claim refusal does not name the derived principal: %s", claim.Message)
	}

	// The pane claims as itself, which is the only shape left, and then the
	// same pane tries the plugin's name on submit.
	mustCall(t, d, protocol.Request{Verb: "task.claim",
		PaneID: "wF:p1", Args: map[string]any{"id": id}})
	sub := mustFail(t, d, protocol.Request{Verb: "task.submit",
		PaneID: "wF:p1", As: "plugin:hdis",
		Args: map[string]any{"id": id, "report": "done", "evidence": "make test"}}, codes.Forbidden)
	if !strings.Contains(sub.Message, "agent:wF:p1") {
		t.Errorf("submit refusal does not name the derived principal: %s", sub.Message)
	}

	got := unmarshalTask(t, mustCall(t, d, protocol.Request{Verb: "task.get",
		PaneID: "wF:p1", Args: map[string]any{"id": id}}))
	if got.Task.ClaimedBy != "agent:wF:p1" {
		t.Fatalf("claimed_by = %s, want agent:wF:p1 — a plugin was credited with an agent's work",
			got.Task.ClaimedBy)
	}
}
