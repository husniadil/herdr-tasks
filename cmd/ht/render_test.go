package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/husniadil/herdr-tasks/internal/codes"
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

// §6.2 for the project-resolution failure at root.go:171. That path is not
// reachable from the command line — I could not construct an input that makes
// project.Resolve fail: filepath.Abs only fails for a relative path when the
// working directory has gone, and a process cannot be started in one that
// has. So the door's answer for it is pinned where it is built instead.
func TestAResolutionFailureIsOneUsageEnvelope(t *testing.T) {
	err := codes.Errorf(codes.Usage, "cannot resolve the project: %v", errors.New("getwd: no such file or directory"))
	out := captureStdout(t, func() { printErrorEnvelope(err, codes.Usage) })

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 {
		t.Fatalf("stdout carries %d documents, want one:\n%s", len(lines), out)
	}
	var doc struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if uerr := json.Unmarshal([]byte(lines[0]), &doc); uerr != nil {
		t.Fatalf("not one JSON document: %q (%v)", lines[0], uerr)
	}
	if doc.Error.Code != codes.Usage {
		t.Fatalf("code = %q, want USAGE", doc.Error.Code)
	}
	if !strings.Contains(doc.Error.Message, "cannot resolve the project") {
		t.Fatalf("the envelope does not say what failed: %q", doc.Error.Message)
	}
}

// §6.2: the door reads --json out of argv before cobra parses, because
// cobra's own parse failures have to answer with a document too.
func TestWantsJSONReadsTheRawArgv(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		want bool
	}{
		{[]string{"task", "list"}, false},
		{[]string{"task", "list", "--json"}, true},
		{[]string{"--json", "task", "list"}, true},
		{[]string{"task", "list", "--json=true"}, true},
		{[]string{"task", "list", "--json=false"}, false},
		{[]string{"task", "list", "--json=0"}, false},
		// A value cobra will refuse still counts as asking for a document:
		// the refusal is the answer, and it should arrive in the shape the
		// caller asked for.
		{[]string{"task", "list", "--json=maybe"}, true},
		{[]string{"task", "list", "--nonesuch", "--json"}, true},
		// After the terminator it is an argument, not this flag.
		{[]string{"note", "add", "--", "--json"}, false},
		{[]string{"note", "add", "--json", "--", "--json=false"}, true},
	} {
		if got := wantsJSON(tc.argv); got != tc.want {
			t.Errorf("wantsJSON(%v) = %v, want %v", tc.argv, got, tc.want)
		}
	}
}
