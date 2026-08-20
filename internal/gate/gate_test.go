package gate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func script(t *testing.T, body string) []string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gate")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	return []string{path}
}

// §9.2: unconfigured allows.
func TestUnconfiguredAllows(t *testing.T) {
	d := New(nil).Check(Request{Subject: "agent:wF:p1", Verb: "tasks.claim", Target: "1"})
	if d.Decision != Allow {
		t.Fatalf("decision = %+v, want allow", d)
	}
}

// §9.2: the command receives {subject, verb, target} on stdin.
func TestCommandSeesTheRequestOnStdin(t *testing.T) {
	out := filepath.Join(t.TempDir(), "seen.json")
	g := New(script(t, "cat > "+out+"\necho '{\"decision\":\"allow\"}'\n"))
	if d := g.Check(Request{Subject: "agent:wF:p1", Verb: "tasks.approve", Target: "7"}); d.Decision != Allow {
		t.Fatalf("decision = %+v", d)
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, want := range []string{`"subject":"agent:wF:p1"`, `"verb":"tasks.approve"`, `"target":"7"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("stdin %q missing %s", body, want)
		}
	}
}

func TestDenyCarriesItsReason(t *testing.T) {
	g := New(script(t, `echo '{"decision":"deny","reason":"not during a freeze"}'`))
	d := g.Check(Request{Verb: "tasks.claim"})
	if d.Decision != Deny || d.Reason != "not during a freeze" {
		t.Fatalf("decision = %+v", d)
	}
}

func TestDeferIsItsOwnDecision(t *testing.T) {
	g := New(script(t, `echo '{"decision":"defer","reason":"ask the operator"}'`))
	if d := g.Check(Request{Verb: "tasks.approve"}); d.Decision != Defer {
		t.Fatalf("decision = %+v, want defer", d)
	}
}

// §9.2: any failure to get a well-formed answer is deny. The gate fails
// closed — this is the rule the whole section exists for, so every way of
// failing is covered here.
func TestEveryFailureIsDeny(t *testing.T) {
	cases := map[string][]string{
		"unreachable":      {filepath.Join(t.TempDir(), "no-such-binary")},
		"non-zero":         script(t, `echo '{"decision":"allow"}'; exit 1`),
		"malformed":        script(t, `echo 'yes, fine'`),
		"unknown decision": script(t, `echo '{"decision":"probably"}'`),
		"empty":            script(t, `true`),
		"oversized":        script(t, `head -c 200000 /dev/zero | tr '\0' 'a'`),
		"hangs":            script(t, `sleep 30`),
	}
	for name, cmd := range cases {
		t.Run(name, func(t *testing.T) {
			g := New(cmd)
			// The hang case proves the bound exists, not how long it is.
			g.Timeout = 300 * time.Millisecond
			d := g.Check(Request{Verb: "tasks.claim"})
			if d.Decision != Deny {
				t.Fatalf("%s: decision = %+v, want deny (§9.2 fails closed)", name, d)
			}
			if d.Reason == "" {
				t.Fatal("a denial must say why")
			}
		})
	}
}
