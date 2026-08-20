package main_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/husniadil/herdr-tasks/internal/codes"
	"github.com/husniadil/herdr-tasks/internal/testenv"
	"github.com/husniadil/herdr-tasks/internal/verbs"
)

// world is one throwaway installation: its own binary, state dir, config dir,
// project, and fake herdr. Nothing here touches the operator's Herdr, config
// or state (§12.3).
type world struct {
	t       *testing.T
	bin     string
	state   string
	config  string
	project string
	herdr   string
}

func newWorld(t *testing.T) *world {
	t.Helper()
	testenv.SkipUnlessFull(t)
	dir := testenv.ShortDir(t)
	bin := filepath.Join(dir, "htask")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	w := &world{
		t:       t,
		bin:     bin,
		state:   filepath.Join(dir, "state"),
		config:  filepath.Join(dir, "config"),
		project: filepath.Join(dir, "proj"),
		herdr:   testenv.FakeHerdr(t),
	}
	for _, d := range []string{w.state, w.config, w.project} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	t.Cleanup(w.stopDaemon)
	return w
}

func (w *world) env(extra ...string) []string {
	base := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + w.state,
		"TASKS_STATE_DIR=" + w.state,
		"TASKS_CONFIG_DIR=" + w.config,
		"HERDR_BIN_PATH=" + w.herdr,
	}
	return append(base, extra...)
}

// run invokes the CLI and returns stdout, stderr and the exit status.
func (w *world) run(env []string, args ...string) (string, string, int) {
	w.t.Helper()
	cmd := exec.Command(w.bin, args...)
	cmd.Dir = w.project
	cmd.Env = env
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	status := 0
	if ee, ok := err.(*exec.ExitError); ok {
		status = ee.ExitCode()
	} else if err != nil {
		w.t.Fatalf("run %v: %v", args, err)
	}
	return out.String(), errb.String(), status
}

func (w *world) json(env []string, args ...string) map[string]any {
	w.t.Helper()
	stdout, stderr, status := w.run(env, append(args, "--json")...)
	if status != 0 {
		w.t.Fatalf("%v exited %d: %s%s", args, status, stdout, stderr)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &doc); err != nil {
		w.t.Fatalf("%v printed something that is not one JSON document: %q", args, stdout)
	}
	return doc
}

func (w *world) stopDaemon() {
	exec.Command("pkill", "-f", w.bin+" daemon").Run()
}

// §2.2: a CLI call that finds no live socket starts the daemon and waits for
// it, bounded, rather than fail. §3.5: the socket is 0600.
func TestCLIAutostartsTheDaemon(t *testing.T) {
	w := newWorld(t)
	sock := filepath.Join(w.state, "tasks.sock")
	if _, err := os.Stat(sock); err == nil {
		t.Fatal("precondition: no socket yet")
	}
	doc := w.json(w.env(), "task", "list")
	if doc["count"] != float64(0) {
		t.Fatalf("count = %v", doc["count"])
	}
	info, err := os.Stat(sock)
	if err != nil {
		t.Fatalf("the daemon did not create the socket: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("socket mode = %o, want 600 (§3.5)", perm)
	}
	if state, err := os.Stat(w.state); err == nil && state.Mode().Perm() != 0o700 {
		t.Fatalf("state dir mode = %o, want 700 (§3.5)", state.Mode().Perm())
	}
}

// §6.2 and §6.3: --json prints exactly one document, and a failure prints the
// error envelope and exits with the code's status.
func TestJSONEnvelopeAndExitStatuses(t *testing.T) {
	w := newWorld(t)
	w.json(w.env(), "task", "create", "the first task")

	stdout, _, status := w.run(w.env(), "task", "get", "404", "--json")
	if status != codes.Exit(codes.NotFound) {
		t.Fatalf("exit = %d, want %d for NOT_FOUND", status, codes.Exit(codes.NotFound))
	}
	var doc struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &doc); err != nil {
		t.Fatalf("stdout is not the §6.2 envelope: %q", stdout)
	}
	if doc.Error.Code != codes.NotFound || doc.Error.Message == "" {
		t.Fatalf("envelope = %+v", doc.Error)
	}

	// USAGE / 2 for a caller-validatable input error.
	if _, _, status := w.run(w.env(), "task", "create", "--json"); status != codes.Exit(codes.Usage) {
		t.Fatalf("exit = %d, want 2 for USAGE", status)
	}
	// CONFLICT / 6 for a claim someone else holds.
	w.json(w.env("HERDR_PANE_ID=wF:p1"), "task", "claim", "1")
	if _, _, status := w.run(w.env("HERDR_PANE_ID=wF:p2"), "task", "claim", "1", "--json"); status != codes.Exit(codes.Conflict) {
		t.Fatalf("exit = %d, want 6 for CONFLICT", status)
	}
	// FORBIDDEN / 8 for recusal by harness.
	w.json(w.env("HERDR_PANE_ID=wF:p1"), "task", "submit", "1", "--report", "done")
	if _, _, status := w.run(w.env("HERDR_PANE_ID=wF:p9"), "task", "approve", "1", "--json"); status != codes.Exit(codes.Forbidden) {
		t.Fatalf("exit = %d, want 8 for FORBIDDEN (§6.6)", status)
	}
}

// §6.2: without --json, output is for humans and stdout carries no JSON.
func TestHumanOutputIsNotJSON(t *testing.T) {
	w := newWorld(t)
	w.json(w.env(), "task", "create", "readable")
	stdout, _, status := w.run(w.env(), "task", "list")
	if status != 0 {
		t.Fatalf("exit = %d", status)
	}
	if strings.HasPrefix(strings.TrimSpace(stdout), "{") {
		t.Fatalf("human output must not be JSON: %q", stdout)
	}
	if !strings.Contains(stdout, "readable") {
		t.Fatalf("human output does not show the task: %q", stdout)
	}
}

// §3.2: the principal is derived from HERDR_PANE_ID, and §3.4's facts come
// from herdr through HERDR_BIN_PATH.
func TestPrincipalAndHarnessComeFromTheEnvironment(t *testing.T) {
	w := newWorld(t)
	w.json(w.env(), "task", "create", "claimable")
	doc := w.json(w.env("HERDR_PANE_ID=wF:p1"), "task", "claim", "1")
	task := doc["task"].(map[string]any)
	if task["claimed_by"] != "agent:wF:p1" || task["claimed_by_harness"] != "claude" {
		t.Fatalf("task = %+v", task)
	}
}

// §4.1: every worktree of a repository is one project, and a task filed from a
// subdirectory is in the same backlog.
func TestProjectScopeIsTheRepository(t *testing.T) {
	w := newWorld(t)
	run := func(dir string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(w.project, "init", "-q")
	sub := filepath.Join(w.project, "internal", "deep")
	os.MkdirAll(sub, 0o755)

	w.json(w.env(), "task", "create", "from the root")
	cmd := exec.Command(w.bin, "task", "list", "--json")
	cmd.Dir, cmd.Env = sub, w.env()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("list from a subdirectory: %v", err)
	}
	if !strings.Contains(string(out), "from the root") {
		t.Fatalf("a subdirectory is the same project (§4.1): %s", out)
	}
}

// §10.3 and §5.8: doctor reports and never fails; dump prints the whole store.
func TestDoctorAndDump(t *testing.T) {
	w := newWorld(t)
	w.json(w.env(), "task", "create", "dumped")
	doctor := w.json(w.env(), "doctor")
	for _, key := range []string{"version", "contract", "state_dir", "config_dir", "socket_live",
		"herdr_reachable", "gate_configured", "hook_configured", "degraded", "gated_verbs"} {
		if _, ok := doctor[key]; !ok {
			t.Errorf("doctor omits %q (§10.3)", key)
		}
	}
	if doctor["socket_live"] != true || doctor["herdr_reachable"] != true {
		t.Fatalf("doctor = %+v", doctor)
	}
	dump := w.json(w.env(), "dump")
	if _, ok := dump["schema_version"]; !ok {
		t.Fatalf("dump omits the schema version: %+v", dump)
	}
	tasksOut, _ := dump["tasks"].([]any)
	if len(tasksOut) != 1 {
		t.Fatalf("dump tasks = %v", dump["tasks"])
	}
}

// §16.2: `task goal` prints a paste-ready condition under 4,000 characters.
func TestTaskGoalPrintsAPasteReadyCondition(t *testing.T) {
	w := newWorld(t)
	w.json(w.env(), "task", "create", "Teach the sweep to speak",
		"--description", "The sweep releases a lease silently.",
		"--validation", "`make test-full` passes and its output is shown")
	stdout, _, status := w.run(w.env(), "task", "goal", "1")
	if status != 0 {
		t.Fatalf("exit = %d", status)
	}
	if len(stdout) >= 4000 {
		t.Fatalf("goal is %d characters", len(stdout))
	}
	for _, want := range []string{"Teach the sweep to speak", "Done when:", "htask task submit 1", "htask task release 1 --note", "htask task touch 1"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("goal is missing %q:\n%s", want, stdout)
		}
	}
}

// §8.2: the event trail streams, and every state change is in it.
func TestEventsCarryEveryStateChange(t *testing.T) {
	w := newWorld(t)
	w.json(w.env(), "task", "create", "watched")
	w.json(w.env("HERDR_PANE_ID=wF:p1"), "task", "claim", "1")
	w.json(w.env("HERDR_PANE_ID=wF:p1"), "task", "release", "1", "--note", "handing back")
	doc := w.json(w.env(), "events")
	events := doc["events"].([]any)
	names := []string{}
	for _, e := range events {
		names = append(names, e.(map[string]any)["name"].(string))
	}
	want := "tasks.task.created,tasks.task.claimed,tasks.task.released"
	if strings.Join(names, ",") != want {
		t.Fatalf("events = %v, want %s", names, want)
	}
}

// The note board, end to end: an agent proposes, only the operator decides.
func TestNoteBoardIsAgentProposesOperatorDecides(t *testing.T) {
	w := newWorld(t)
	w.json(w.env("HERDR_PANE_ID=wF:p1"), "note", "add", "the sweep should log")
	w.json(w.env("HERDR_PANE_ID=wF:p1"), "note", "discuss", "1")
	w.json(w.env("HERDR_PANE_ID=wF:p1"), "note", "verdict", "1", "task", "--reason", "worth doing")
	if _, _, status := w.run(w.env("HERDR_PANE_ID=wF:p1"), "note", "promote", "1", "--json"); status != codes.Exit(codes.Forbidden) {
		t.Fatalf("an agent must not promote its own note: exit %d", status)
	}
	// §16.1: the criteria the verdict drafted travel with the promotion, in
	// the one command the operator runs. A task born without them prints a
	// `task goal` whose "Done when" has nothing to check.
	doc := w.json(w.env(), "note", "promote", "1",
		"--validation", "make test-full exits 0",
		"--validation", "htask note promote --help lists --validation")
	task, _ := doc["task"].(map[string]any)
	if task == nil {
		t.Fatalf("promote = %+v", doc)
	}
	got, _ := task["validation"].([]any)
	if len(got) != 2 {
		t.Fatalf("promote dropped the criteria: %+v", task["validation"])
	}
	for _, c := range got {
		if m, _ := c.(map[string]any); m["required"] != true {
			t.Fatalf("a criterion is required unless marked optional (§16.1): %+v", c)
		}
	}
	list := w.json(w.env(), "task", "list")
	if list["count"] != float64(1) {
		t.Fatalf("the promotion did not file a task: %+v", list)
	}
}

// §13.3: version prints the version consumers check a floor against.
func TestVersion(t *testing.T) {
	w := newWorld(t)
	doc := w.json(w.env(), "version")
	if doc["version"] == "" || doc["contract"] == "" {
		t.Fatalf("version = %+v", doc)
	}
}

// §8.2: --follow is the subscription primitive other plugins and humans use.
// It streams what is already there, then keeps going as new events land.
func TestEventsFollowStreams(t *testing.T) {
	w := newWorld(t)
	w.json(w.env(), "task", "create", "first")

	cmd := exec.Command(w.bin, "events", "--follow", "--json")
	cmd.Dir, cmd.Env = w.project, w.env()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() {
		cmd.Process.Signal(os.Interrupt)
		cmd.Wait()
	}()

	lines := make(chan string, 8)
	go func() {
		dec := json.NewDecoder(stdout)
		for {
			var ev struct {
				Name string `json:"name"`
			}
			if err := dec.Decode(&ev); err != nil {
				close(lines)
				return
			}
			lines <- ev.Name
		}
	}()

	want := []string{"tasks.task.created", "tasks.task.created"}
	// The second one has to arrive after the stream is already open, which is
	// what makes this a subscription rather than a read.
	go func() {
		time.Sleep(300 * time.Millisecond)
		w.json(w.env(), "task", "create", "second")
	}()
	for i, expect := range want {
		select {
		case got, ok := <-lines:
			if !ok {
				t.Fatalf("the stream closed after %d events", i)
			}
			if got != expect {
				t.Fatalf("event %d = %q, want %q", i, got, expect)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("event %d never arrived", i)
		}
	}
}

// §4.2: the door resolves the project from HERDR_PLUGIN_CONTEXT_JSON before
// it falls back to its own working directory. Every verb shares this one
// resolution, and a plugin pane is where it matters: Herdr runs a pane command
// with the PLUGIN ROOT as its working directory, so a door that trusted cwd
// would file and read everything under the plugin's own directory instead of
// the project the operator is looking at.
func TestVerbsScopeToTheHerdrContextNotTheWorkingDirectory(t *testing.T) {
	w := newWorld(t)
	looking := t.TempDir()
	inPane := w.env(`HERDR_PLUGIN_CONTEXT_JSON={"workspace_id":"wM","workspace_cwd":"/nope",` +
		`"focused_pane_cwd":"` + looking + `"}`)

	// Run from the plugin root — w.run's directory — with that context.
	w.json(inPane, "task", "create", "filed from a plugin pane")

	// It landed in the project Herdr said was focused, not the working
	// directory the command happened to start in.
	there := w.json(w.env(), "task", "list", "--project", looking)
	if there["count"] != float64(1) {
		t.Fatalf("the focused project holds %v tasks, want 1", there["count"])
	}
	here := w.json(w.env(), "task", "list")
	if here["count"] != float64(0) {
		t.Fatalf("the working directory's project holds %v tasks, want 0", here["count"])
	}

	// And the pane door reads the same scope back without being told.
	back := w.json(inPane, "task", "list")
	if back["count"] != float64(1) {
		t.Fatalf("reading from the pane found %v tasks, want 1", back["count"])
	}
}

// A door newer than the daemon had the part the daemon did not understand
// dropped, and was told it succeeded. The daemon stamps every answer with the
// door surface it speaks; the door says so when that is not its own. Both
// halves are driven against a hand-rolled daemon so the door is the only real
// thing under test.
func TestSkewIsReportedByTheDoor(t *testing.T) {
	for name, tc := range map[string]struct {
		answer string
		want   string
	}{
		"a daemon speaking a different surface": {
			answer: `{"result":{"tasks":[],"count":0},"fingerprint":"0badc0de0badc0de"}`,
			want:   "restart the daemon",
		},
		"a daemon too old to have one at all": {
			answer: `{"result":{"tasks":[],"count":0}}`,
			want:   "restart the daemon",
		},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			ln := fakeDaemon(t, w, tc.answer)
			defer ln.Close()
			stdout, stderr, status := w.run(w.env(), "task", "list", "--json")
			if status != 0 {
				t.Fatalf("skew must warn, not fail: exit %d: %s%s", status, stdout, stderr)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Fatalf("stderr did not report the skew: %q", stderr)
			}
			if !strings.Contains(stdout, `"count":0`) {
				t.Fatalf("the answer must still reach stdout, unpolluted: %q", stdout)
			}
		})
	}
}

// buildOf is what a daemon running THIS binary reports about itself: the same
// executable path and the same stat (§13.3). The door compares on the path, so
// this is the answer that means "I am the binary you are".
func buildOf(t *testing.T, bin string) string {
	t.Helper()
	fi, err := os.Stat(bin)
	if err != nil {
		t.Fatalf("stat the door binary: %v", err)
	}
	raw, err := json.Marshal(verbs.Build{
		Exe:   bin,
		Stamp: fmt.Sprintf("%d-%d", fi.Size(), fi.ModTime().UnixMilli()),
	})
	if err != nil {
		t.Fatalf("marshal the build: %v", err)
	}
	return string(raw)
}

// The matching case: a daemon speaking this door's own surface, running this
// door's own binary, says nothing.
func TestSkewIsSilentWhenTheSurfacesMatch(t *testing.T) {
	w := newWorld(t)
	ln := fakeDaemon(t, w,
		`{"result":{"tasks":[],"count":0},"fingerprint":"`+verbs.Fingerprint()+`","build":`+buildOf(t, w.bin)+`}`)
	defer ln.Close()
	_, stderr, status := w.run(w.env(), "task", "list", "--json")
	if status != 0 {
		t.Fatalf("exit %d: %s", status, stderr)
	}
	if strings.Contains(stderr, "restart the daemon") {
		t.Fatalf("a matching daemon must say nothing: %q", stderr)
	}
}

// fakeDaemon listens on the socket the door will dial and answers every
// request with one canned line, so the door never autostarts a real one.
func fakeDaemon(t *testing.T, w *world, answer string) net.Listener {
	t.Helper()
	if err := os.MkdirAll(w.state, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	ln, err := net.Listen("unix", filepath.Join(w.state, "tasks.sock"))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			bufio.NewReader(conn).ReadString('\n')
			fmt.Fprintln(conn, answer)
			conn.Close()
		}
	}()
	return ln
}

// §8.4 / §11.5: Herdr says a pane died and its work comes back at once. The
// shipped script is what runs, in a plugin root laid out the way Herdr sees
// one, so the test exercises the real reaction and not a paraphrase of it.
func TestPaneGoneReleasesThatPanesWorkAtOnce(t *testing.T) {
	w := newWorld(t)
	script := pluginRoot(t, w)

	w.json(w.env(), "task", "create", "held by a pane that dies")
	w.json(w.env("HERDR_PANE_ID=wF:p1"), "task", "claim", "1")

	out, errOut, status := run(t, script, w.env("HERDR_PANE_ID=wF:p1"))
	if status != 0 {
		t.Fatalf("the reaction exited %d: %s%s", status, out, errOut)
	}
	doc := w.json(w.env(), "task", "get", "1")
	task := doc["task"].(map[string]any)
	if task["status"] != "todo" {
		t.Fatalf("the dead pane's work did not come back: %v", task["status"])
	}
	if task["claimed_by"] != nil && task["claimed_by"] != "" {
		t.Fatalf("the claim outlived the pane: %v", task["claimed_by"])
	}
	events := w.json(w.env(), "events", "--entity", "task")
	if !strings.Contains(fmt.Sprint(events), "tasks.task.swept") {
		t.Fatalf("the release must be in the trail (§11.5): %v", events)
	}
}

// The other half, and the one that could do damage: Herdr reports a pane
// event with no pane id when the pane had already left its state. A reaction
// that treated that as "sweep everything" would take work off a pane that is
// alive and holding an unexpired lease.
func TestPaneGoneWithNoPaneIDTouchesNobody(t *testing.T) {
	w := newWorld(t)
	script := pluginRoot(t, w)

	w.json(w.env(), "task", "create", "held by a pane that is fine")
	w.json(w.env("HERDR_PANE_ID=wF:p2"), "task", "claim", "1")

	out, errOut, status := run(t, script, w.env())
	if status != 0 {
		t.Fatalf("a pane event with no id must not fail: exit %d: %s%s", status, out, errOut)
	}
	doc := w.json(w.env(), "task", "get", "1")
	task := doc["task"].(map[string]any)
	if task["status"] != "doing" || task["claimed_by"] != "agent:wF:p2" {
		t.Fatalf("a live pane's unexpired claim was taken: %v / %v", task["status"], task["claimed_by"])
	}
}

// pluginRoot lays out the shipped script over the world's binary the way the
// plugin ships: <root>/scripts/on-pane-gone.sh next to <root>/bin/htask.
func pluginRoot(t *testing.T, w *world) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(w.bin, filepath.Join(root, "bin", "htask")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	body, err := os.ReadFile(filepath.Join("..", "..", "scripts", "on-pane-gone.sh"))
	if err != nil {
		t.Fatalf("read the shipped script: %v", err)
	}
	path := filepath.Join(root, "scripts", "on-pane-gone.sh")
	if err := os.WriteFile(path, body, 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func run(t *testing.T, path string, env []string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(path)
	cmd.Env = env
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	status := 0
	if ee, ok := err.(*exec.ExitError); ok {
		status = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run %s: %v", path, err)
	}
	return out.String(), errb.String(), status
}

// §11.6: a key that opens the board must be safe to lean on. Herdr answers
// "popup already open" with its whole JSON response on stderr and exit 1, and
// to an operator that is the state they asked for — so the opener reports
// success. Everything else still fails, or the key would swallow a broken
// install. Driven with a stub herdr: opening a real popup needs an attached
// client, which no test has.
func TestOpenPaneTreatsAnAlreadyOpenPopupAsSuccess(t *testing.T) {
	script := filepath.Join("..", "..", "scripts", "open-pane.sh")
	stub := func(t *testing.T, body string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "herdr")
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatalf("write stub: %v", err)
		}
		return path
	}
	const alreadyOpen = `{"error":{"code":"popup_open_failed","message":"popup already open"}}`

	for name, tc := range map[string]struct {
		herdr string
		args  []string
		want  int
	}{
		"it opened": {
			herdr: "exit 0", args: []string{"board"}, want: 0,
		},
		"it was already open": {
			herdr: "echo '" + alreadyOpen + "' >&2; exit 1", args: []string{"board"}, want: 0,
		},
		"something else went wrong": {
			herdr: `echo '{"error":{"message":"no such plugin"}}' >&2; exit 1`,
			args:  []string{"board"}, want: 1,
		},
		"no pane id given": {
			herdr: "exit 0", args: nil, want: 2,
		},
	} {
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command(script, tc.args...)
			cmd.Env = append(os.Environ(), "HERDR_BIN_PATH="+stub(t, tc.herdr))
			var out, errb strings.Builder
			cmd.Stdout, cmd.Stderr = &out, &errb
			err := cmd.Run()
			status := 0
			if ee, ok := err.(*exec.ExitError); ok {
				status = ee.ExitCode()
			} else if err != nil {
				t.Fatalf("run: %v", err)
			}
			if status != tc.want {
				t.Fatalf("exit %d, want %d: %s%s", status, tc.want, out.String(), errb.String())
			}
		})
	}
}

// §4.2 / §6.2: a board scoped to a project the operator was not thinking of
// looks exactly like a board with nothing on it. Twice now that has been read
// as data loss. An empty answer therefore names the project it is empty FOR,
// and when the same filter matches somewhere else it says where to look —
// both on the line a human reads and in the document a program parses,
// because both are first-class callers.
func TestEmptyNamesTheProjectAndWhereTheWorkIs(t *testing.T) {
	w := newWorld(t)
	other := filepath.Join(filepath.Dir(w.project), "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	empty := filepath.Join(filepath.Dir(w.project), "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	w.json(w.env(), "task", "create", "the work is over here", "--project", other)
	w.json(w.env(), "note", "add", "and so is the idea", "--project", other)

	// The incident: a project of its own with nothing in it, while the store
	// holds work elsewhere.
	stdout, _, status := w.run(w.env(), "task", "list", "--project", w.project)
	if status != 0 {
		t.Fatalf("an empty list is not an error: exit %d", status)
	}
	if !strings.Contains(stdout, w.project) {
		t.Fatalf("the empty line does not name the project it is empty for: %q", stdout)
	}
	if !strings.Contains(stdout, "1") || !strings.Contains(stdout, "--all-projects") {
		t.Fatalf("the empty line does not say where the work is: %q", stdout)
	}
	// §4.1 resolves symlinks, so the answer is the canonical path, not the one
	// that was typed. /var is a symlink to /private/var on macOS.
	want, err := filepath.EvalSymlinks(w.project)
	if err != nil {
		t.Fatalf("canonical path: %v", err)
	}
	doc := w.json(w.env(), "task", "list", "--project", w.project)
	if doc["project"] != want {
		t.Fatalf(`--json "project" = %v, want %q`, doc["project"], want)
	}
	if doc["elsewhere"] != float64(1) {
		t.Fatalf(`--json "elsewhere" = %v, want 1`, doc["elsewhere"])
	}
	if doc["count"] != float64(0) {
		t.Fatalf("adding the fields moved count: %v", doc["count"])
	}

	// The notes board answers the same way: it is the same incident.
	stdout, _, _ = w.run(w.env(), "note", "list", "--project", w.project)
	if !strings.Contains(stdout, w.project) || !strings.Contains(stdout, "--all-projects") {
		t.Fatalf("the empty notes line says neither where it is nor where to look: %q", stdout)
	}

	// A hint that is not true must not appear. Nothing matches `--status
	// cancelled` anywhere, so there is nowhere to send the operator.
	stdout, _, _ = w.run(w.env(), "task", "list", "--project", empty, "--status", "cancelled")
	if strings.Contains(stdout, "--all-projects") {
		t.Fatalf("a hint appeared with nothing to point at: %q", stdout)
	}
	if !strings.Contains(stdout, empty) {
		t.Fatalf("the empty line still owes the project: %q", stdout)
	}
	// The count is of the SAME filter, not of the store: `--status todo`
	// matches the other project's task, `--status done` matches nothing.
	doc = w.json(w.env(), "task", "list", "--project", w.project, "--status", "done")
	if doc["elsewhere"] != nil && doc["elsewhere"] != float64(0) {
		t.Fatalf("elsewhere counted rows the suggested command would not show: %v", doc["elsewhere"])
	}

	// §4.4: an --all-projects answer was never scoped to a project, so an
	// empty one must not name the project that happened to resolve — that
	// would describe a scope the caller did not ask for.
	stdout, _, _ = w.run(w.env(), "task", "list", "--project", w.project, "--all-projects", "--status", "cancelled")
	if strings.Contains(stdout, w.project) {
		t.Fatalf("an --all-projects answer named a project it did not scope to: %q", stdout)
	}
	if doc := w.json(w.env(), "task", "list", "--project", w.project, "--all-projects"); doc["project"] != nil {
		t.Fatalf(`--all-projects still claimed "project": %v`, doc["project"])
	}

	// A board with rows on it explains nothing: the line is for the empty case.
	stdout, _, _ = w.run(w.env(), "task", "list", "--project", other)
	if strings.Contains(stdout, "--all-projects") {
		t.Fatalf("a full board carried the empty state: %q", stdout)
	}
}

// §2.3: the daemon lock is named where an operator debugging a wedged daemon
// will look — beside the socket, in both surfaces of `htask doctor`.
func TestDoctorNamesTheDaemonLock(t *testing.T) {
	w := newWorld(t)
	doc := w.json(w.env(), "doctor")
	lock, ok := doc["lock_path"].(string)
	if !ok || lock == "" {
		t.Fatalf("doctor --json does not name the lock: %v", doc)
	}
	want := filepath.Join(w.state, "tasks.lock")
	if lock != want {
		t.Fatalf("lock_path = %q, want %q", lock, want)
	}
	stdout, _, status := w.run(w.env(), "doctor")
	if status != 0 {
		t.Fatalf("doctor exited %d", status)
	}
	if !strings.Contains(stdout, want) {
		t.Fatalf("the prose doctor does not name the lock:\n%s", stdout)
	}
}

// §2.3: flock is released by the kernel when the holder dies, so a daemon
// killed outright must leave nothing for the next one to clear by hand — no
// stale-lock timeout, no manual unlink.
func TestAKilledDaemonDoesNotWedgeTheNextOne(t *testing.T) {
	w := newWorld(t)
	sock := filepath.Join(w.state, "tasks.sock")

	first := exec.Command(w.bin, "daemon")
	first.Dir, first.Env = w.project, w.env()
	if err := first.Start(); err != nil {
		t.Fatalf("start the daemon: %v", err)
	}
	if !waitForSocket(t, sock, 5*time.Second) {
		first.Process.Kill()
		t.Fatal("the first daemon never listened")
	}
	if err := first.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatalf("kill: %v", err)
	}
	first.Process.Wait()
	// SIGKILL leaves the socket file behind: that is the state the next
	// daemon has to survive.
	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("precondition: the killed daemon should have left its socket: %v", err)
	}

	doc := w.json(w.env(), "task", "list")
	if doc["count"] != float64(0) {
		t.Fatalf("the next daemon did not serve: %v", doc)
	}
}

func waitForSocket(t *testing.T, path string, within time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if conn, err := net.DialTimeout("unix", path, 200*time.Millisecond); err == nil {
			conn.Close()
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// oneEnvelope requires stdout to be exactly one §6.2 error document and
// returns its code. "Exactly one" is the contract: a machine caller reads one
// document or it reads nothing it can act on.
func oneEnvelope(t *testing.T, stdout string) string {
	t.Helper()
	lines := []string{}
	for _, ln := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if strings.TrimSpace(ln) != "" {
			lines = append(lines, ln)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("stdout carries %d documents, want exactly one:\n%s", len(lines), stdout)
	}
	var doc struct {
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document: %q (%v)", lines[0], err)
	}
	if doc.Error == nil {
		t.Fatalf("the document is not an error envelope: %q", lines[0])
	}
	if doc.Error.Message == "" {
		t.Fatalf("the envelope says nothing: %q", lines[0])
	}
	return doc.Error.Code
}

// §6.2: --json answers with one document whichever side refused. The door's
// own refusals — a missing positional, a flag cobra does not know — used to
// exit with the right status and an EMPTY stdout, so a machine caller got an
// envelope or an end-of-file depending on which side validated, which is not
// something it can see from where it stands.
func TestDoorSideJSONFailuresPrintOneEnvelope(t *testing.T) {
	w := newWorld(t)
	for name, args := range map[string][]string{
		"a missing required positional": {"task", "claim"},
		"a flag cobra does not know":    {"task", "list", "--nonesuch"},
	} {
		stdout, stderr, status := w.run(w.env(), append(args, "--json")...)
		if status != codes.Exit(codes.Usage) {
			t.Errorf("%s: exit %d, want %d", name, status, codes.Exit(codes.Usage))
		}
		if got := oneEnvelope(t, stdout); got != codes.Usage {
			t.Errorf("%s: code %q, want USAGE", name, got)
		}
		// One document, one stream: the envelope IS the report, so the prose
		// line must not also appear as if two things went wrong.
		if strings.Contains(stderr, "htask: ") {
			t.Errorf("%s: --json printed the envelope AND a prose line: %q", name, stderr)
		}
	}
}

// §6.2 / §6.3: the human half is unchanged — prose on stderr, nothing on
// stdout, the same status.
func TestWithoutJSONADoorFailureStillSpeaksProse(t *testing.T) {
	w := newWorld(t)
	stdout, stderr, status := w.run(w.env(), "task", "claim")
	if status != codes.Exit(codes.Usage) {
		t.Fatalf("exit %d, want %d", status, codes.Exit(codes.Usage))
	}
	if strings.TrimSpace(stdout) != "" {
		t.Fatalf("prose mode wrote to stdout: %q", stdout)
	}
	if !strings.Contains(stderr, "htask: ") {
		t.Fatalf("prose mode said nothing on stderr: %q", stderr)
	}
	if strings.Contains(stderr, `{"error"`) {
		t.Fatalf("prose mode printed an envelope: %q", stderr)
	}
}

// §6.2 for every verb, so a verb added later cannot reintroduce the gap: each
// one, invoked with --json and its required positional missing, answers with
// one envelope.
func TestEveryVerbWithAMissingPositionalAnswersWithAnEnvelope(t *testing.T) {
	w := newWorld(t)
	checked := 0
	for _, v := range verbs.All {
		required := false
		for _, a := range v.Args {
			if a.Positional && a.Required {
				required = true
			}
		}
		if !required {
			continue
		}
		checked++
		stdout, stderr, status := w.run(w.env(), append(append([]string{}, v.CLI...), "--json")...)
		if status != codes.Exit(codes.Usage) {
			t.Errorf("%s: exit %d, want %d (%s%s)", v.Name, status, codes.Exit(codes.Usage), stdout, stderr)
			continue
		}
		if got := oneEnvelope(t, stdout); got != codes.Usage {
			t.Errorf("%s: code %q, want USAGE", v.Name, got)
		}
	}
	if checked < 10 {
		t.Fatalf("only %d verbs take a required positional; the walk is not covering the surface", checked)
	}
}

// §6.2 for the streaming verb: an error that ends a stream is still one
// envelope, and it is the LAST document on stdout — nothing can retract what
// was already written, so the only honest place for it is the end.
func TestAStreamThatCannotStartAnswersWithAnEnvelope(t *testing.T) {
	w := newWorld(t)
	// A state dir whose socket path is past what a unix socket allows: the
	// daemon refuses to listen, so the client's start-and-wait gives up.
	long := filepath.Join(w.state, strings.Repeat("d", 120))
	if err := os.MkdirAll(long, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	stdout, stderr, status := w.run(w.env("TASKS_STATE_DIR="+long), "events", "--follow", "--json")
	if status == 0 {
		t.Fatalf("a stream that never started exited 0: %s%s", stdout, stderr)
	}
	if got := oneEnvelope(t, stdout); got != codes.Unavailable {
		t.Errorf("code %q, want UNAVAILABLE", got)
	}
	if strings.Contains(stderr, "htask: ") {
		t.Errorf("--json printed the envelope AND a prose line: %q", stderr)
	}
}

// §6.2 for a stream that fails PART WAY: the documents already written stay
// valid and untouched, and the envelope is the LAST document. Nothing can
// retract what is already on stdout, so the end is the only honest place for
// it.
//
// The daemon here is a fake that streams two events and then an error
// response, because the shapes that produce a real mid-stream failure are
// the follow path's own (a killed daemon is silently swallowed today — that
// is the events --follow task, not this one).
func TestAStreamThatFailsPartWayEndsWithTheEnvelope(t *testing.T) {
	w := newWorld(t)
	if err := os.MkdirAll(w.state, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	ln, err := net.Listen("unix", filepath.Join(w.state, "tasks.sock"))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			bufio.NewReader(conn).ReadString('\n')
			// The fake claims this door's own surface and build, so the only
			// thing on stderr can be the failure this test is about.
			stamp := `,"fingerprint":"` + verbs.Fingerprint() + `","build":` + buildOf(t, w.bin) + `}`
			fmt.Fprintln(conn, `{"result":{"id":"E1","name":"tasks.task.created"}`+stamp)
			fmt.Fprintln(conn, `{"result":{"id":"E2","name":"tasks.task.claimed"}`+stamp)
			fmt.Fprintln(conn, `{"error":{"code":"UNAVAILABLE","message":"the trail could not be read"}`+stamp)
			conn.Close()
		}
	}()

	stdout, stderr, status := w.run(w.env(), "events", "--follow", "--json")
	if status != codes.Exit(codes.Unavailable) {
		t.Fatalf("exit %d, want %d: %s%s", status, codes.Exit(codes.Unavailable), stdout, stderr)
	}
	lines := []string{}
	for _, ln := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if strings.TrimSpace(ln) != "" {
			lines = append(lines, ln)
		}
	}
	if len(lines) != 3 {
		t.Fatalf("stdout carries %d documents, want the two events and the envelope:\n%s", len(lines), stdout)
	}
	// The two events came through whole, in order, and were not rewritten.
	for i, want := range []string{"E1", "E2"} {
		var doc map[string]any
		if err := json.Unmarshal([]byte(lines[i]), &doc); err != nil {
			t.Fatalf("event %d is not a document: %q", i, lines[i])
		}
		if doc["id"] != want {
			t.Fatalf("document %d is %v, want %s", i, doc["id"], want)
		}
		if _, isErr := doc["error"]; isErr {
			t.Fatalf("document %d is an envelope, not an event: %q", i, lines[i])
		}
	}
	if got := oneEnvelope(t, lines[2]); got != codes.Unavailable {
		t.Fatalf("the last document's code is %q, want UNAVAILABLE", got)
	}
	if strings.Contains(stderr, "htask: ") {
		t.Fatalf("--json printed the envelope AND a prose line: %q", stderr)
	}
}

// §4.2 / §6.2: a context document the door cannot read is said out loud on
// STDERR, and stdout still carries exactly one document. A popup takes its
// project from that variable alone, so falling back silently scopes the board
// somewhere else and looks identical to an empty board.
func TestABrokenPluginContextWarnsWithoutSpoilingTheDocument(t *testing.T) {
	w := newWorld(t)
	broken := `{"focused_pane_cwd": "/tmp/marker-7c1e2b", `
	stdout, stderr, status := w.run(w.env("HERDR_PLUGIN_CONTEXT_JSON="+broken), "task", "list", "--json")
	if status != 0 {
		t.Fatalf("the verb did not answer: exit %d, %s%s", status, stdout, stderr)
	}
	// One document, still parseable, still the real answer.
	var doc map[string]any
	trimmed := strings.TrimSpace(stdout)
	if strings.Contains(trimmed, "\n") {
		t.Fatalf("stdout carries more than one document:\n%s", stdout)
	}
	if err := json.Unmarshal([]byte(trimmed), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document: %q", stdout)
	}
	if _, ok := doc["count"]; !ok {
		t.Fatalf("the answer is not a task list: %s", stdout)
	}
	// The warning is on stderr, names the variable, names the project used,
	// and does not echo the value.
	if !strings.Contains(stderr, "HERDR_PLUGIN_CONTEXT_JSON") {
		t.Fatalf("nothing warned about the broken context: %q", stderr)
	}
	if !strings.Contains(stderr, doc["project"].(string)) {
		t.Fatalf("the warning does not name the project the answer used: %q vs %v", stderr, doc["project"])
	}
	if strings.Contains(stderr, "marker-7c1e2b") {
		t.Fatalf("the warning echoed the value: %q", stderr)
	}

	// And a well-formed context says nothing at all.
	good := `{"workspace_id":"wM","focused_pane_cwd":` + strconv.Quote(w.project) + `}`
	_, stderr, status = w.run(w.env("HERDR_PLUGIN_CONTEXT_JSON="+good), "task", "list", "--json")
	if status != 0 {
		t.Fatalf("exit %d", status)
	}
	if strings.Contains(stderr, "HERDR_PLUGIN_CONTEXT_JSON") {
		t.Fatalf("a good context warned anyway: %q", stderr)
	}
}

// follower runs `htask events --follow --json` against the world's daemon and
// returns what it wrote and how it ended. The stream is read as it arrives, so
// the test can act between documents.
type follower struct {
	cmd    *exec.Cmd
	out    *syncBuffer
	errOut *syncBuffer
}

// syncBuffer collects a child's output while the test reads it. exec writes
// from its own goroutine, so an unguarded builder is a real race — caught by
// `go test -race`, which is exactly what the gate runs it for.
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func startFollower(t *testing.T, w *world, args ...string) *follower {
	t.Helper()
	cmd := exec.Command(w.bin, append([]string{"events", "--follow", "--json"}, args...)...)
	cmd.Dir, cmd.Env = w.project, w.env()
	f := &follower{cmd: cmd, out: &syncBuffer{}, errOut: &syncBuffer{}}
	cmd.Stdout, cmd.Stderr = f.out, f.errOut
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the follower: %v", err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	return f
}

// wait returns the follower's exit status once it ends, or fails the test.
func (f *follower) wait(t *testing.T, within time.Duration) int {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- f.cmd.Wait() }()
	select {
	case err := <-done:
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		if err != nil {
			t.Fatalf("the follower failed to run: %v", err)
		}
		return 0
	case <-time.After(within):
		t.Fatalf("the follower did not end within %s; it had written:\n%s%s", within, f.out, f.errOut)
		return -1
	}
}

func (f *follower) documents() []string {
	lines := []string{}
	for _, ln := range strings.Split(strings.TrimSpace(f.out.String()), "\n") {
		if strings.TrimSpace(ln) != "" {
			lines = append(lines, ln)
		}
	}
	return lines
}

// §8.2: --limit is declared on the verb, so a follow that ignores it is a door
// promising something it does not do. It streams that many and stops.
func TestAFollowHonoursItsLimit(t *testing.T) {
	w := newWorld(t)
	w.json(w.env(), "task", "create", "first")
	f := startFollower(t, w, "--limit", "1")
	// More events than the limit, so an unbounded stream would show it.
	for i := 0; i < 3; i++ {
		w.json(w.env(), "task", "create", fmt.Sprintf("more %d", i))
	}
	if status := f.wait(t, 15*time.Second); status != 0 {
		t.Fatalf("a stream that reached its limit exited %d: %s%s", status, f.out, f.errOut)
	}
	if docs := f.documents(); len(docs) != 1 {
		t.Fatalf("the stream emitted %d documents for --limit 1:\n%s", len(docs), f.out)
	}
}

// §6.3: a daemon that dies under a stream is UNAVAILABLE, not success. The
// client returned nil on ANY decode error, so a killed daemon and a finished
// stream were the same thing and the caller silently stopped watching.
func TestAFollowerOutlivedByItsDaemonExitsUnavailable(t *testing.T) {
	w := newWorld(t)
	w.json(w.env(), "task", "create", "so the daemon is up")
	f := startFollower(t, w)
	// Let it connect and drain the backlog before the daemon goes.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && len(f.documents()) == 0 {
		time.Sleep(100 * time.Millisecond)
	}
	if len(f.documents()) == 0 {
		t.Fatalf("the follower never received anything: %s%s", f.out, f.errOut)
	}
	killDaemon(t, w)

	if status := f.wait(t, 15*time.Second); status != codes.Exit(codes.Unavailable) {
		t.Fatalf("exit %d, want %d (UNAVAILABLE): %s%s", status, codes.Exit(codes.Unavailable), f.out, f.errOut)
	}
	// And the envelope is the last document, as §6.2 requires of a stream.
	docs := f.documents()
	if got := oneEnvelope(t, docs[len(docs)-1]); got != codes.Unavailable {
		t.Fatalf("the last document's code is %q, want UNAVAILABLE", got)
	}
}

// killDaemon SIGKILLs the daemon this world started, so it cannot say goodbye.
func killDaemon(t *testing.T, w *world) {
	t.Helper()
	if err := exec.Command("pkill", "-9", "-f", w.bin+" daemon").Run(); err != nil {
		t.Fatalf("kill the daemon: %v", err)
	}
}

// §6.3: a stream that ends ON PURPOSE exits 0, so the UNAVAILABLE above is a
// real signal and not a blanket. --limit reaching its bound is that ending.
func TestAStreamThatEndsOnPurposeExitsZero(t *testing.T) {
	w := newWorld(t)
	f := startFollower(t, w, "--limit", "1")
	w.json(w.env(), "task", "create", "the one event")
	if status := f.wait(t, 15*time.Second); status != 0 {
		t.Fatalf("exit %d, want 0: %s%s", status, f.out, f.errOut)
	}
	if docs := f.documents(); len(docs) != 1 {
		t.Fatalf("%d documents, want 1:\n%s", len(docs), f.out)
	}
	if strings.Contains(f.out.String(), `"done"`) {
		t.Fatalf("the end marker reached the caller's stdout: %s", f.out)
	}
}
