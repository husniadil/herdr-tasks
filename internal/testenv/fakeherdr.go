// Package testenv builds the throwaway world layer-2 tests run in: a fake
// `herdr` on PATH and temp state and config dirs. Tests never touch the
// operator's live Herdr, config, or state (§12.3).
package testenv

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeHerdr answers the three calls the plugin makes: `agent get <pane>`
// (§3.4), `pane list` (§11.7) and `api schema --json` (§11.2). Everything else
// exits non-zero, so a test that reaches for an unmodelled call fails loudly
// rather than silently getting an empty answer.
//
// `pane list` lists wF:p1 and wF:p2 and does NOT list wF:gone, which is the
// same pane `agent get` answers "no such pane" for: one fake world where one
// pane is gone and the others are live.
const fakeHerdr = `#!/bin/sh
case "$1 $2" in
  "agent get")
    pane="$3"
    case "$pane" in
      wF:gone) echo '{"error":"no such pane"}' >&2; exit 3 ;;
      wF:p2)   harness=codex; name=reviewer ;;
      *)       harness=claude; name=builder ;;
    esac
    # The envelope real herdr prints, with agent_session as an object.
    printf '{"id":"cli:agent:get","result":{"type":"agent_info","agent":{"pane_id":"%s","name":"%s","agent":"%s","agent_status":"working","agent_session":{"kind":"id","value":"sess-%s"}}}}\n' \
      "$pane" "$name" "$harness" "$pane"
    ;;
  "pane list")
    # The envelope real herdr prints for pane.list, trimmed to the one field
    # this plugin reads. pane list takes no --json: it already prints JSON
    # and rejects the flag.
    cat <<'JSON'
{"id":"cli:pane:list","result":{"type":"pane_list","panes":[
  {"pane_id":"wF:p1","terminal_id":"t1","workspace_id":"wF","tab_id":"wF:t1","focused":true},
  {"pane_id":"wF:p2","terminal_id":"t2","workspace_id":"wF","tab_id":"wF:t1","focused":false}]}}
JSON
    ;;
  "api schema")
    # The shape real herdr prints: a JSON Schema whose request branch is a
    # oneOf over method constants and whose event branch is an EventKind enum.
    cat <<'JSON'
{"protocol":19,"schema_version":1,"schemas":{
  "request":{"oneOf":[
    {"properties":{"method":{"const":"agent.get"}}},
    {"properties":{"method":{"const":"agent.prompt"}}},
    {"properties":{"method":{"const":"pane.run"}}},
    {"properties":{"method":{"const":"pane.list"}}}]},
  "event":{"$defs":{"EventKind":{"enum":["pane_exited","pane_closed","pane_agent_status_changed"]}}}}}
JSON
    ;;
  *) echo "fake herdr: unsupported: $*" >&2; exit 64 ;;
esac
`

// FakeHerdr writes the fake into a temp dir and returns its path, for
// HERDR_BIN_PATH.
func FakeHerdr(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "herdr")
	if err := os.WriteFile(path, []byte(fakeHerdr), 0o755); err != nil {
		t.Fatalf("write fake herdr: %v", err)
	}
	return path
}

// MissingHerdr returns a path where no binary lives, for the UNAVAILABLE path.
func MissingHerdr(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "herdr-that-is-not-there")
}

// SkipUnlessFull skips a layer-2 test in the fast loop. `make test` runs
// -short and deliberately leaves out every case that starts the daemon, walks
// the socket, or drives the fake herdr; `make test-full` runs all of it.
func SkipUnlessFull(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("layer 2 (§12.1): runs in make test-full")
	}
}

// ShortDir is a temp dir with a short path. A Unix socket path has a hard
// length limit in the kernel, and t.TempDir()'s name is the test's own, which
// on a long test name is enough to cross it.
func ShortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "htask")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}
