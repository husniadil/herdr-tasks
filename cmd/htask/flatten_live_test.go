package main_test

import (
	"strings"
	"testing"

	"github.com/husniadil/herdr-tasks/internal/codes"
)

// §6.1: one registry, two spellings of the same path on one door. The
// transition window against a real daemon: the flat form is the one a
// reader is taught, the old form is the one the sibling adapters still send,
// and both reach the same verb on the socket.
func TestBothFormsAnswerTheSameDaemon(t *testing.T) {
	w := newWorld(t)
	w.json(w.env(), "create", "filed through the flat form")
	if doc := w.json(w.env(), "task", "list"); doc["count"] != float64(1) {
		t.Fatalf("`htask task list` saw count = %v; the alias must reach the same board", doc["count"])
	}

	// The old form still writes, not merely reads.
	w.json(w.env(), "task", "create", "filed through the alias")
	if doc := w.json(w.env(), "list"); doc["count"] != float64(2) {
		t.Fatalf("`htask list` saw count = %v after a write through the alias", doc["count"])
	}

	// A claim taken by one form is held against the other, which is the same
	// claim in the store rather than two commands that happen to look alike.
	w.json(w.env("HERDR_PANE_ID=wF:p1"), "claim", "1")
	if _, _, status := w.run(w.env("HERDR_PANE_ID=wF:p2"), "task", "claim", "1", "--json"); status != codes.Exit(codes.Conflict) {
		t.Fatalf("the alias did not see the claim the flat form took: exit %d, want %d",
			status, codes.Exit(codes.Conflict))
	}
}

// §6.1 again, the human half: --help teaches the flat form and says nothing about the alias.
func TestHelpTeachesTheFlatFormOnly(t *testing.T) {
	w := newWorld(t)
	stdout, stderr, status := w.run(w.env(), "--help")
	if status != 0 {
		t.Fatalf("--help exited %d: %s%s", status, stdout, stderr)
	}
	help := stdout + stderr
	for _, want := range []string{"claim", "submit", "approve", "note"} {
		if !strings.Contains(help, "\n  "+want) {
			t.Errorf("`htask --help` does not list %q as a command:\n%s", want, help)
		}
	}
	if strings.Contains(help, "\n  task ") || strings.Contains(help, "\n  task\n") {
		t.Errorf("`htask --help` lists the `task` alias group; it is hidden:\n%s", help)
	}
}
