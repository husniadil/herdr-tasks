package daemon

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/husniadil/herdr-tasks/internal/codes"
	"github.com/husniadil/herdr-tasks/internal/config"
	"github.com/husniadil/herdr-tasks/internal/herdrclient"
	"github.com/husniadil/herdr-tasks/internal/protocol"
	"github.com/husniadil/herdr-tasks/internal/store"
	"github.com/husniadil/herdr-tasks/internal/testenv"
	"github.com/husniadil/herdr-tasks/internal/verbs"
)

const proj = "/tmp/project-a"

// newDaemon builds a daemon over a temp store, a temp config dir, and the fake
// herdr — never the operator's live Herdr, config, or state (§12.3).
func newDaemon(t *testing.T, cfg *config.Config) *Daemon {
	t.Helper()
	testenv.SkipUnlessFull(t)
	dir := testenv.ShortDir(t)
	t.Setenv("HERDR_PLUGIN_STATE_DIR", dir)
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", t.TempDir())
	s, err := store.Open(filepath.Join(dir, "tasks.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if cfg == nil {
		cfg = &config.Config{LeaseSeconds: 900, SweepSeconds: 60, Path: filepath.Join(dir, "tasks.toml")}
	}
	d := New(s, cfg, herdrclient.New(testenv.FakeHerdr(t)))
	var tick int64 = 1_700_000_000_000
	d.Now = func() int64 { tick++; return tick }
	return d
}

func call(t *testing.T, d *Daemon, req protocol.Request) protocol.Response {
	t.Helper()
	if req.Project == "" {
		req.Project = proj
	}
	return d.Answer(req)
}

func mustCall(t *testing.T, d *Daemon, req protocol.Request) json.RawMessage {
	t.Helper()
	resp := call(t, d, req)
	if resp.Error != nil {
		t.Fatalf("%s: %s: %s", req.Verb, resp.Error.Code, resp.Error.Message)
	}
	return resp.Result
}

func mustFail(t *testing.T, d *Daemon, req protocol.Request, wantCode string) *protocol.ErrorBody {
	t.Helper()
	resp := call(t, d, req)
	if resp.Error == nil {
		t.Fatalf("%s: want %s, got a result: %s", req.Verb, wantCode, resp.Result)
	}
	if resp.Error.Code != wantCode {
		t.Fatalf("%s: code = %s (%s), want %s", req.Verb, resp.Error.Code, resp.Error.Message, wantCode)
	}
	return resp.Error
}

func createTask(t *testing.T, d *Daemon, title string) TaskResult {
	t.Helper()
	raw := mustCall(t, d, protocol.Request{Verb: "task.create", Args: map[string]any{"title": title}})
	var res TaskResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return res
}

// §6.1: every verb in the registry has a handler and every handler is a verb.
// This is the check that makes the registry the single source both doors are
// built from rather than a list that drifts.
func TestEveryRegistryVerbHasAHandler(t *testing.T) {
	handled := map[string]bool{}
	for _, name := range HandledVerbs() {
		handled[name] = true
	}
	for _, v := range verbs.All {
		if !handled[v.Name] {
			t.Errorf("verb %q is in the registry with no handler", v.Name)
		}
		delete(handled, v.Name)
	}
	for name := range handled {
		t.Errorf("handler %q answers a verb the registry does not declare", name)
	}
}

// §3.2: a call carrying a pane id is agent:<pane>; one without is human.
// §3.4: the harness is snapshotted from Herdr, never inferred.
func TestPrincipalIsDerivedAndHarnessSnapshotted(t *testing.T) {
	d := newDaemon(t, nil)
	task := createTask(t, d, "claim me")
	raw := mustCall(t, d, protocol.Request{Verb: "task.claim", PaneID: "wF:p1",
		Args: map[string]any{"id": task.Task.ID}})
	var res TaskResult
	json.Unmarshal(raw, &res)
	if res.Task.ClaimedBy != "agent:wF:p1" {
		t.Fatalf("claimed_by = %q", res.Task.ClaimedBy)
	}
	if res.Task.ClaimedByHarness != "claude" || res.Task.ClaimedByName != "builder" {
		t.Fatalf("herdr snapshot missing: %+v", res.Task)
	}
	doctor := mustCall(t, d, protocol.Request{Verb: "doctor"})
	if !strings.Contains(string(doctor), `"principal":"human"`) {
		t.Fatalf("a call with no pane id is human (§3.2): %s", doctor)
	}
}

// §3.2: agent and human principals are derived, never declared.
func TestAsRefusesDerivedPrincipals(t *testing.T) {
	d := newDaemon(t, nil)
	for _, as := range []string{"agent:wF:p1", "human"} {
		mustFail(t, d, protocol.Request{Verb: "task.list", As: as}, codes.Forbidden)
	}
	// cron and trigger are exactly what --as is for.
	mustCall(t, d, protocol.Request{Verb: "task.list", As: "cron:nightly"})
}

// §6.6: recusal is by harness. The fake herdr gives wF:p1 claude and wF:p2
// codex, so the same-harness reviewer is a different pane with the same model.
func TestRecusalIsByHarnessAcrossPanes(t *testing.T) {
	d := newDaemon(t, nil)
	task := createTask(t, d, "reviewed work")
	id := task.Task.ID
	mustCall(t, d, protocol.Request{Verb: "task.claim", PaneID: "wF:p1", Args: map[string]any{"id": id}})
	mustCall(t, d, protocol.Request{Verb: "task.submit", PaneID: "wF:p1",
		Args: map[string]any{"id": id, "report": "done"}})
	// A different pane, same harness: refused.
	mustFail(t, d, protocol.Request{Verb: "task.approve", PaneID: "wF:p9", Args: map[string]any{"id": id}}, codes.Forbidden)
	// A different harness: allowed.
	mustCall(t, d, protocol.Request{Verb: "task.approve", PaneID: "wF:p2", Args: map[string]any{"id": id}})
}

// §6.3: the error vocabulary, end to end through the envelope.
func TestErrorEnvelopeUsesContractCodes(t *testing.T) {
	d := newDaemon(t, nil)
	task := createTask(t, d, "one")
	id := task.Task.ID
	mustFail(t, d, protocol.Request{Verb: "task.get", Args: map[string]any{"id": "9999"}}, codes.NotFound)
	mustFail(t, d, protocol.Request{Verb: "task.create", Args: map[string]any{"title": "  "}}, codes.Usage)
	mustFail(t, d, protocol.Request{Verb: "nope.nope"}, codes.Usage)
	mustCall(t, d, protocol.Request{Verb: "task.claim", PaneID: "wF:p1", Args: map[string]any{"id": id}})
	mustFail(t, d, protocol.Request{Verb: "task.claim", PaneID: "wF:p2", Args: map[string]any{"id": id}}, codes.Conflict)
	mustFail(t, d, protocol.Request{Verb: "task.submit", PaneID: "wF:p2",
		Args: map[string]any{"id": id, "report": "mine"}}, codes.Forbidden)
}

// §9.2: a gate that denies makes the verb DENIED, and the verb does not run.
func TestGateDenyStopsTheVerb(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gate")
	os.WriteFile(path, []byte("#!/bin/sh\necho '{\"decision\":\"deny\",\"reason\":\"frozen\"}'\n"), 0o755)
	d := newDaemon(t, &config.Config{LeaseSeconds: 900, SweepSeconds: 60, GateCommand: []string{path}})
	body := mustFail(t, d, protocol.Request{Verb: "task.create", Args: map[string]any{"title": "blocked"}}, codes.Denied)
	if !strings.Contains(body.Message, "frozen") {
		t.Fatalf("the denial must carry the gate's reason: %q", body.Message)
	}
	list := mustCall(t, d, protocol.Request{Verb: "task.list"})
	if !strings.Contains(string(list), `"count":0`) {
		t.Fatalf("a denied verb must not have run: %s", list)
	}
}

// §9.3: defer parks the action and returns DENIED with parked_id; only the
// operator resolves it, and the re-run happens under the original subject.
func TestGateDeferParksAndTheOperatorResolves(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gate")
	os.WriteFile(path, []byte("#!/bin/sh\necho '{\"decision\":\"defer\",\"reason\":\"ask first\"}'\n"), 0o755)
	cfg := &config.Config{LeaseSeconds: 900, SweepSeconds: 60, GateCommand: []string{path}}
	d := newDaemon(t, cfg)
	body := mustFail(t, d, protocol.Request{Verb: "task.create", PaneID: "wF:p1",
		Args: map[string]any{"title": "parked work"}}, codes.Denied)
	if body.ParkedID == "" {
		t.Fatal("a deferred verb must return its parked_id (§9.3)")
	}
	parked := mustCall(t, d, protocol.Request{Verb: "parked.list"})
	if !strings.Contains(string(parked), body.ParkedID) {
		t.Fatalf("parked list = %s", parked)
	}
	// An agent may not resolve it.
	mustFail(t, d, protocol.Request{Verb: "parked.resolve", PaneID: "wF:p1",
		Args: map[string]any{"id": body.ParkedID}}, codes.Forbidden)
	// The operator may, and the re-run runs as the original subject.
	raw := mustCall(t, d, protocol.Request{Verb: "parked.resolve", Args: map[string]any{"id": body.ParkedID}})
	if !strings.Contains(string(raw), `"state":"resolved"`) {
		t.Fatalf("resolve = %s", raw)
	}
	list := mustCall(t, d, protocol.Request{Verb: "task.list"})
	if !strings.Contains(string(list), "parked work") {
		t.Fatalf("the resolved verb must have run: %s", list)
	}
	if !strings.Contains(string(list), `"created_by":"agent:wF:p1"`) {
		t.Fatalf("the re-run must use the original subject, not the resolver's (§9.3): %s", list)
	}
}

// §9.2: an unconfigured gate allows.
func TestUnconfiguredGateAllows(t *testing.T) {
	d := newDaemon(t, nil)
	createTask(t, d, "allowed")
}

// §5.6: --base-updated-at that no longer matches is CONFLICT.
func TestBaseUpdatedAtGuard(t *testing.T) {
	d := newDaemon(t, nil)
	task := createTask(t, d, "raced")
	mustCall(t, d, protocol.Request{Verb: "task.update", BaseUpdatedAt: task.Task.UpdatedAt,
		Args: map[string]any{"id": task.Task.ID, "title": "renamed"}})
	mustFail(t, d, protocol.Request{Verb: "task.update", BaseUpdatedAt: task.Task.UpdatedAt,
		Args: map[string]any{"id": task.Task.ID, "title": "renamed again"}}, codes.Conflict)
}

// §11.5: the sweep releases an expired lease and records it.
func TestSweepReleasesExpiredLease(t *testing.T) {
	d := newDaemon(t, &config.Config{LeaseSeconds: 1, SweepSeconds: 60})
	task := createTask(t, d, "abandoned")
	mustCall(t, d, protocol.Request{Verb: "task.claim", PaneID: "wF:p1", Args: map[string]any{"id": task.Task.ID}})
	base := d.Now()
	d.Now = func() int64 { return base + 10_000 }
	swept := d.Sweep()
	if len(swept) != 1 || swept[0] != task.Task.ID {
		t.Fatalf("swept = %v", swept)
	}
	raw := mustCall(t, d, protocol.Request{Verb: "events", Args: map[string]any{"entity": "task"}})
	if !strings.Contains(string(raw), `"tasks.task.swept"`) {
		t.Fatalf("the sweep must be in the trail (§11.5): %s", raw)
	}
}

// §8.1: events are named tasks.<entity>.<verb> in past tense.
func TestEventNamesFollowTheContract(t *testing.T) {
	d := newDaemon(t, nil)
	task := createTask(t, d, "named")
	mustCall(t, d, protocol.Request{Verb: "task.claim", PaneID: "wF:p1", Args: map[string]any{"id": task.Task.ID}})
	mustCall(t, d, protocol.Request{Verb: "note.add", Args: map[string]any{"body": "an idea"}})
	raw := mustCall(t, d, protocol.Request{Verb: "events"})
	var res EventsResult
	json.Unmarshal(raw, &res)
	got := []string{}
	for _, e := range res.Events {
		got = append(got, e.Name)
	}
	sort.Strings(got)
	want := []string{"tasks.note.added", "tasks.task.claimed", "tasks.task.created"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("event names = %v, want %v", got, want)
	}
}

// §8.3: the event hook runs detached, with the event in its environment, and a
// hook that fails does not fail the write.
func TestEventHookRunsAndCannotFailTheWrite(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "fired")
	hook := filepath.Join(dir, "hook")
	os.WriteFile(hook, []byte("#!/bin/sh\nprintf '%s %s %s' \"$TASKS_EVENT\" \"$TASKS_ENTITY\" \"$TASKS_ACTOR\" > "+marker+"\nexit 7\n"), 0o755)
	d := newDaemon(t, &config.Config{LeaseSeconds: 900, SweepSeconds: 60, OnEvent: []string{hook}})
	createTask(t, d, "hooked")
	deadline := time.Now().Add(3 * time.Second)
	var body []byte
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(marker); err == nil {
			body = b
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := string(body); !strings.HasPrefix(got, "tasks.task.created task human") {
		t.Fatalf("hook environment = %q", got)
	}
	// The hook exited 7 and the write still stands.
	list := mustCall(t, d, protocol.Request{Verb: "task.list"})
	if !strings.Contains(string(list), "hooked") {
		t.Fatalf("a failing hook must not fail the write (§8.3): %s", list)
	}
}

// §2.2 / §3.5: the daemon listens on <state_dir>/tasks.sock at mode 0600 and
// answers one request per connection.
func TestSocketIsPrivateAndAnswers(t *testing.T) {
	d := newDaemon(t, nil)
	path := config.SocketPath()
	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("socket mode = %o, want 600 (§3.5)", perm)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Serve(ctx, ln)

	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	json.NewEncoder(conn).Encode(protocol.Request{Verb: "version", Project: proj})
	var resp protocol.Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// `version` is answered by the CLI without the daemon, so the daemon
	// rejects it as an unknown verb — which is itself the envelope check.
	if resp.Error == nil || resp.Error.Code != codes.Usage {
		t.Fatalf("resp = %+v", resp)
	}
}

// §2.2: a second daemon on a live socket refuses rather than stealing it.
func TestListenRefusesASecondDaemon(t *testing.T) {
	newDaemon(t, nil)
	path := config.SocketPath()
	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	if _, err := Listen(path); err == nil {
		t.Fatal("a second Listen on a live socket must fail")
	}
}

// §2.2: a socket file left by a daemon that died is cleared, not honoured.
func TestListenClearsAStaleSocket(t *testing.T) {
	newDaemon(t, nil)
	path := config.SocketPath()
	if err := os.WriteFile(path, []byte("stale"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen over a stale socket: %v", err)
	}
	ln.Close()
}
