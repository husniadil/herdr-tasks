// Package testenv builds the throwaway world layer-2 tests run in: a fake
// `herdr` on PATH and temp state and config dirs. Tests never touch the
// operator's live Herdr, config, or state (§12.3).
package testenv

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeHerdr answers the two calls the plugin makes: `agent get <pane> --json`
// (§3.4) and `api schema --json` (§11.2). Everything else exits non-zero, so a
// test that reaches for an unmodelled call fails loudly rather than silently
// getting an empty answer.
const fakeHerdr = `#!/bin/sh
case "$1 $2" in
  "agent get")
    pane="$3"
    case "$pane" in
      wF:gone) echo '{"error":"no such pane"}' >&2; exit 3 ;;
      wF:p2)   harness=codex; name=reviewer ;;
      *)       harness=claude; name=builder ;;
    esac
    printf '{"pane_id":"%s","name":"%s","agent":"%s","agent_status":"working","agent_session":"sess-%s"}\n' \
      "$pane" "$name" "$harness" "$pane"
    ;;
  "api schema")
    echo '{"requests":["agent.get","agent.prompt","pane.run"],"events":["pane.exited","pane.agent_status_changed"]}'
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
