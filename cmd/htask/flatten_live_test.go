package main_test

import (
	"strings"
	"testing"

	"github.com/husniadil/herdr-tasks/internal/codes"
)

// The transition window is over: `htask task <verb>` was a hidden alias for
// one release and is now an unknown command, refused the way every other
// unknown command is (§6.2, §6.3). The flat form is the only one.
func TestTheOldTaskFormIsRefused(t *testing.T) {
	w := newWorld(t)
	w.json(w.env(), "create", "filed through the flat form")

	stdout, stderr, status := w.run(w.env(), "task", "list", "--json")
	if status != codes.Exit(codes.Usage) {
		t.Fatalf("`htask task list --json` exited %d, want %d: %s%s",
			status, codes.Exit(codes.Usage), stdout, stderr)
	}
	if got := oneEnvelope(t, stdout); got != codes.Usage {
		t.Errorf("`htask task list --json` answered %q, want USAGE", got)
	}

	// The flat form still reads the board the refused call was aiming at.
	if doc := w.json(w.env(), "list"); doc["count"] != float64(1) {
		t.Fatalf("`htask list` saw count = %v, want 1", doc["count"])
	}
}

// --help teaches the flat form and knows nothing of a task group.
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
		t.Errorf("`htask --help` lists a `task` group; the verbs are flat:\n%s", help)
	}
}
