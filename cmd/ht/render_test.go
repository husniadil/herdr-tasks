package main

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/husniadil/herdr-tasks/internal/verbs"
)

// §9.3 / §6.1: both audiences see that a resolved action's verb failed. The
// --json half is pinned in the daemon's own tests; this is the prose half,
// which is the surface an operator reads when they wonder why the approve they
// resolved did not happen.
func TestParkedListSaysWhenAVerbFailed(t *testing.T) {
	raw := json.RawMessage(`{"parked":[
		{"id":"P1","project":"/repo","subject":"agent:wF:p1","verb":"tasks.approve","target":"#3","state":"parked"},
		{"id":"P2","project":"/repo","subject":"agent:wF:p1","verb":"tasks.approve","target":"#4","state":"failed",
		 "error":"CONFLICT: task is todo, not review"}],"count":2}`)
	v, ok := verbs.ByName("parked.list")
	if !ok {
		t.Fatal("no parked.list verb")
	}
	out := captureStdout(t, func() {
		if err := renderHuman(v, raw); err != nil {
			t.Fatalf("renderHuman: %v", err)
		}
	})
	if !strings.Contains(out, "P1") || !strings.Contains(out, "P2") {
		t.Fatalf("both rows must be listed:\n%s", out)
	}
	if !strings.Contains(out, "failed") || !strings.Contains(out, "task is todo, not review") {
		t.Fatalf("the failed row does not say what happened:\n%s", out)
	}
	// The waiting row is not decorated with a state it does not have.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "P1") && strings.Contains(line, "failed") {
			t.Fatalf("the waiting row was reported as failed: %q", line)
		}
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	saved := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var buf strings.Builder
		io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	os.Stdout = saved
	w.Close()
	out := <-done
	r.Close()
	return out
}
