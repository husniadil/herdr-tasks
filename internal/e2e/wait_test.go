// No build tag: these are layer 3's own helpers and their tests, and they need
// no Herdr. Behind the e2e tag they were compiled by the gate and executed by
// nothing, which is the worst place for a test to be — it is the harness that
// decides whether every OTHER layer-3 failure is legible, and it was itself
// unchecked. Untagged, `go test ./...` picks up anything added here without
// the Makefile having to be told.

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// waitForFile polls until path exists or the timeout elapses, and reports
// WHICH of the two happened. The return value is the whole point: a wait that
// cannot say why it stopped makes its caller guess, and the caller guesses
// wrong in the one case that matters.
func waitForFile(path string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// timedOut is the failure a pane command that never finished produces. It
// names the timeout, the command, and whatever the command had managed to
// write, because a partial line is usually the reason.
func timedOut(pane string, args []string, after time.Duration, partial string) error {
	if partial == "" {
		partial = "(nothing)"
	}
	return fmt.Errorf("timed out after %s waiting for `htask %s` to finish in pane %s; it had written %q",
		after, strings.Join(args, " "), pane, partial)
}

// readSoFar is whatever a command has written, for the timeout message. A
// missing file is not an error here: the command may not have started.
func readSoFar(path string) string {
	body, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(body))
}

// The harness's own failure reporting. A pane command that never finishes must
// be reported as the timeout it was. A wait that falls out of its loop
// silently leaves the half-written file to be read anyway, and a slow command
// then surfaces as "the pane printed no JSON document" — the door blamed for
// the harness running out of patience. Needs no Herdr, so it runs even where
// the rest of layer 3 skips.
func TestAPaneWaitThatRunsOutOfTimeSaysSoInsteadOfBlamingTheOutput(t *testing.T) {
	appeared := filepath.Join(t.TempDir(), "done")
	if err := os.WriteFile(appeared, []byte("0\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !waitForFile(appeared, time.Second) {
		t.Fatal("the wait missed a file that was already there")
	}

	missing := filepath.Join(t.TempDir(), "never")
	start := time.Now()
	if waitForFile(missing, 200*time.Millisecond) {
		t.Fatal("the wait claimed a file that never appears did")
	}
	if waited := time.Since(start); waited < 200*time.Millisecond {
		t.Fatalf("the wait gave up after %s, before its own timeout", waited)
	}

	err := timedOut("w1:p1", []string{"task", "claim", "01H"}, 200*time.Millisecond, `{"partial":`)
	for _, want := range []string{"timed out", "200ms", "task claim 01H", "w1:p1", `{\"partial\":`} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the timeout does not name %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "no JSON document") {
		t.Fatalf("a timeout is still reported as a parse failure: %v", err)
	}
}
