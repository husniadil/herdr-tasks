//go:build e2e

// Package e2e is layer 3 of §12.1: the shipped `ht` binary against a REAL
// headless `herdr server`, in a throwaway named session on private socket
// paths. §12.3 is the hard rule here — nothing in this package may reach the
// operator's live Herdr session, config dir, or state dir, so the server runs
// with its own HOME, its own XDG dirs, its own HERDR_SOCKET_PATH and its own
// HERDR_SESSION, and the CLI under test is pointed at all of them.
package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// world is one throwaway Herdr and one throwaway plugin state dir.
type world struct {
	t         *testing.T
	root      string
	herdrPath string
	htPath    string
	socket    string
	session   string
	env       []string
	server    *exec.Cmd
	stopped   bool
}

// startWorld boots the throwaway Herdr and returns the world the tests drive.
// It skips loudly, naming what was missing, rather than passing on a machine
// where nothing was actually proved.
func startWorld(t *testing.T) *world {
	t.Helper()
	herdr := herdrBinary(t)
	ht := htBinary(t)

	// A Unix socket path has a hard length limit in the kernel, and macOS's
	// TMPDIR is long enough on its own to cross it.
	root, err := os.MkdirTemp("/tmp", "hte2e")
	if err != nil {
		t.Fatalf("temp root: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })

	w := &world{
		t: t, root: root, herdrPath: herdr, htPath: copyBinary(t, ht, filepath.Join(root, "ht")),
		socket:  filepath.Join(root, "h.sock"),
		session: fmt.Sprintf("ht-e2e-%d", os.Getpid()),
	}
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("temp home: %v", err)
	}
	w.env = append(os.Environ(),
		// §12.3: the operator's home, config, state and live session are all
		// out of reach for everything this suite runs.
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"XDG_STATE_HOME="+filepath.Join(home, ".state"),
		"XDG_CACHE_HOME="+filepath.Join(home, ".cache"),
		"HERDR_SOCKET_PATH="+w.socket,
		"HERDR_SESSION="+w.session,
		// And the plugin's own dirs (§5.1, §10.1) are temp dirs too, so the
		// daemon this suite starts never opens the operator's tasks.db.
		"TASKS_STATE_DIR="+filepath.Join(root, "state"),
		"TASKS_CONFIG_DIR="+filepath.Join(root, "config"),
		"HERDR_BIN_PATH="+herdr,
	)
	// The pane id a door reads from its own environment (§3.2) must come from
	// the pane, never from the shell that started the test.
	w.env = append(w.env, "HERDR_PANE_ID=", "HERDR_TAB_ID=", "HERDR_WORKSPACE_ID=")

	log, err := os.Create(filepath.Join(root, "server.log"))
	if err != nil {
		t.Fatalf("server log: %v", err)
	}
	w.server = exec.Command(herdr, "server")
	w.server.Env, w.server.Stdout, w.server.Stderr = w.env, log, log
	if err := w.server.Start(); err != nil {
		t.Skipf("layer 3 (§12.1) SKIPPED LOUDLY: cannot start `%s server`: %v", herdr, err)
	}
	t.Cleanup(w.stop)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if fi, err := os.Stat(w.socket); err == nil && fi.Mode()&os.ModeSocket != 0 {
			// The socket exists; wait until it answers before handing it out.
			if _, err := w.tryHerdr("workspace", "list"); err == nil {
				return w
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	out, _ := os.ReadFile(filepath.Join(root, "server.log"))
	t.Fatalf("the throwaway herdr never came up on %s:\n%s", w.socket, out)
	return nil
}

// stop tears down everything this world started, and is safe to call twice.
func (w *world) stop() {
	if w.stopped {
		return
	}
	w.stopped = true
	// The CLI autostarts a detached daemon and nothing else ever stops it
	// (§2.2, §2.4: Herdr has no shutdown hook). Without this the suite leaves
	// one daemon per test running for good, each holding a state dir the test
	// cleanup has already deleted.
	w.stopDaemons()
	if w.server == nil || w.server.Process == nil {
		return
	}
	// Only ever this process: `herdr server stop` would resolve a socket, and
	// the one it resolved on a bad day is the operator's.
	_ = w.server.Process.Kill()
	_, _ = w.server.Process.Wait()
}

// stopDaemons signals every daemon started from THIS world's binary. The
// binary is a per-world copy for exactly this reason: a pattern matching the
// repository's own bin/ht would also kill the daemon a developer is running.
func (w *world) stopDaemons() {
	_ = exec.Command("pkill", "-TERM", "-f", w.htPath+" daemon").Run()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(w.daemonPIDs()) == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// daemonPIDs is every live daemon started from this world's binary.
func (w *world) daemonPIDs() []string {
	out, err := exec.Command("pgrep", "-f", w.htPath+" daemon").Output()
	if err != nil {
		return nil
	}
	pids := []string{}
	for _, line := range strings.Fields(string(out)) {
		pids = append(pids, line)
	}
	return pids
}

// copyBinary gives this world its own copy of the shipped binary, so the
// daemons it starts can be told apart from anyone else's by their argv.
func copyBinary(t *testing.T, from, to string) string {
	t.Helper()
	body, err := os.ReadFile(from)
	if err != nil {
		t.Fatalf("read %s: %v", from, err)
	}
	if err := os.WriteFile(to, body, 0o755); err != nil {
		t.Fatalf("copy the binary under test: %v", err)
	}
	return to
}

// herdrBinary resolves the Herdr under test the way §11.1 says, and skips
// loudly when there is none to run.
func herdrBinary(t *testing.T) string {
	t.Helper()
	bin := os.Getenv("HERDR_BIN_PATH")
	if bin == "" {
		found, err := exec.LookPath("herdr")
		if err != nil {
			t.Skip("layer 3 (§12.1) SKIPPED LOUDLY: no `herdr` on PATH and no HERDR_BIN_PATH; " +
				"nothing here was proved. Install Herdr, or run `make test-full` for layers 1 and 2.")
		}
		bin = found
	}
	if err := exec.Command(bin, "--version").Run(); err != nil {
		t.Skipf("layer 3 (§12.1) SKIPPED LOUDLY: `%s --version` will not run (%v); nothing here was proved.", bin, err)
	}
	return bin
}

// htBinary is the SHIPPED binary — what `make build` produced — not a package
// under test. Layer 3 proves the thing that gets installed.
func htBinary(t *testing.T) string {
	t.Helper()
	if bin := os.Getenv("HT_BIN"); bin != "" {
		return bin
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("cwd: %v", err)
	}
	bin := filepath.Join(wd, "..", "..", "bin", "ht")
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("layer 3 (§12.1) SKIPPED LOUDLY: no built binary at %s; run `make build` first.", bin)
	}
	return bin
}

// herdr runs the real Herdr CLI against the throwaway server and returns its
// `result` object.
func (w *world) herdr(args ...string) map[string]any {
	w.t.Helper()
	out, err := w.tryHerdr(args...)
	if err != nil {
		w.t.Fatalf("herdr %s: %v", strings.Join(args, " "), err)
	}
	return out
}

func (w *world) tryHerdr(args ...string) (map[string]any, error) {
	cmd := exec.Command(w.herdrPath, args...)
	cmd.Env = w.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%v: %s", err, out)
	}
	// Some control verbs (pane run, pane report-agent) answer with nothing at
	// all; silence is success there.
	if len(bytes.TrimSpace(out)) == 0 {
		return map[string]any{}, nil
	}
	var env struct {
		Result map[string]any `json:"result"`
		Error  map[string]any `json:"error"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		return nil, fmt.Errorf("unreadable answer %q: %v", out, err)
	}
	if env.Error != nil {
		return nil, fmt.Errorf("%v", env.Error)
	}
	return env.Result, nil
}

// ht runs the shipped binary outside any pane: the `human` principal (§3.2).
func (w *world) ht(args ...string) map[string]any {
	w.t.Helper()
	out, err := w.tryHT(args...)
	if err != nil {
		w.t.Fatalf("ht %s: %v", strings.Join(args, " "), err)
	}
	return out
}

func (w *world) tryHT(args ...string) (map[string]any, error) {
	cmd := exec.Command(w.htPath, append(args, "--json")...)
	cmd.Env = w.env
	cmd.Dir = w.root
	var out []byte
	out, runErr := cmd.Output()
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil, fmt.Errorf("ht printed no JSON document (§6.2): %q (%v)", out, runErr)
	}
	if e, ok := doc["error"].(map[string]any); ok {
		return doc, fmt.Errorf("%v: %v", e["code"], e["message"])
	}
	return doc, nil
}

// pane opens a fresh workspace and returns its root pane id. The pane is a
// plain shell: Herdr injects HERDR_PANE_ID into it, which is the whole of what
// §3.2 needs for a door running there to be `agent:<pane>`.
func (w *world) pane(label string) string {
	w.t.Helper()
	res := w.herdr("workspace", "create", "--label", label, "--cwd", w.root)
	root, _ := res["root_pane"].(map[string]any)
	id, _ := root["pane_id"].(string)
	if id == "" {
		w.t.Fatalf("workspace create returned no root pane: %v", res)
	}
	return id
}

// beAgent gives a pane an agent identity through Herdr, the way a real harness
// reports itself. §3.4's three facts then come from Herdr rather than from a
// guess — which is the point of reading them there.
func (w *world) beAgent(pane, harness, name string) {
	w.t.Helper()
	w.herdr("pane", "report-agent", pane, "--source", "e2e", "--agent", harness, "--state", "working")
	if name != "" {
		w.herdr("agent", "rename", pane, name)
	}
}

// htInPane runs the shipped binary INSIDE a Herdr pane and returns the JSON it
// printed. The output is collected through a file because the pane is a
// terminal, not a pipe: what is being proved is the environment the pane gave
// the process, not how its bytes got back.
func (w *world) htInPane(pane string, args ...string) (map[string]any, error) {
	w.t.Helper()
	out := filepath.Join(w.root, fmt.Sprintf("pane-%d.json", time.Now().UnixNano()))
	done := out + ".done"
	quoted := make([]string, 0, len(args))
	for _, a := range args {
		quoted = append(quoted, "'"+strings.ReplaceAll(a, "'", `'\''`)+"'")
	}
	line := fmt.Sprintf("%s %s --json > %s 2>&1; echo $? > %s",
		w.htPath, strings.Join(quoted, " "), out, done)
	w.herdr("pane", "run", pane, line)
	if !waitForFile(done, paneTimeout) {
		// Carry the reason the wait ended. Reading the half-written file and
		// reporting "printed no JSON document" would blame the door for the
		// harness running out of patience, which is the kind of message that
		// makes a flake take an afternoon.
		return nil, timedOut(pane, args, paneTimeout, readSoFar(out))
	}
	body, err := os.ReadFile(out)
	if err != nil {
		return nil, fmt.Errorf("the pane produced no output for %v: %v", args, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("the pane printed no JSON document (§6.2): %q", body)
	}
	if e, ok := doc["error"].(map[string]any); ok {
		return doc, fmt.Errorf("%v: %v", e["code"], e["message"])
	}
	return doc, nil
}

// mustInPane is htInPane where a failure is the test's failure.
func (w *world) mustInPane(pane string, args ...string) map[string]any {
	w.t.Helper()
	doc, err := w.htInPane(pane, args...)
	if err != nil {
		w.t.Fatalf("ht %v in %s: %v", args, pane, err)
	}
	return doc
}

// task reads one task back as a map, from outside any pane.
func (w *world) task(ref string) map[string]any {
	w.t.Helper()
	doc := w.ht("task", "get", ref)
	t, _ := doc["task"].(map[string]any)
	if t == nil {
		w.t.Fatalf("task get %s returned %v", ref, doc)
	}
	return t
}

// sprint renders a decoded JSON document for an assertion message.
func sprint(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// paneTimeout bounds a command run inside a pane. It is generous: what it
// guards against is a hang, not slowness.
const paneTimeout = 30 * time.Second

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
	return fmt.Errorf("timed out after %s waiting for `ht %s` to finish in pane %s; it had written %q",
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
