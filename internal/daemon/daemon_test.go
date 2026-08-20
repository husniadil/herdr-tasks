package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/husniadil/herdr-tasks/internal/codes"
	"github.com/husniadil/herdr-tasks/internal/config"
	"github.com/husniadil/herdr-tasks/internal/herdrclient"
	"github.com/husniadil/herdr-tasks/internal/protocol"
	"github.com/husniadil/herdr-tasks/internal/store"
	"github.com/husniadil/herdr-tasks/internal/tasks"
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
	t.Setenv("TASKS_STATE_DIR", dir)
	t.Setenv("TASKS_CONFIG_DIR", t.TempDir())
	// The XDG bases too, not just the overrides: anything that works out a
	// path from the layout rather than the override — OrphanStoreDirs does —
	// would otherwise read the operator's real home (§12.3).
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "xdg-state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg-config"))
	s, err := store.Open(filepath.Join(dir, "tasks.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if cfg == nil {
		cfg = &config.Config{LeaseSeconds: 900, SweepSeconds: 60, Path: filepath.Join(dir, "tasks.toml")}
	}
	d := New(s, cfg, herdrclient.New(testenv.FakeHerdr(t)))
	// Atomic: the clock is read from every goroutine a concurrency test
	// starts, and a fixture that races itself would drown the race it exists
	// to look for.
	tick := new(atomic.Int64)
	tick.Store(1_700_000_000_000)
	d.Now = func() int64 { return tick.Add(1) }
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

// §9.3: resolving re-runs the verb under the ORIGINAL subject. A cron subject
// that came back as `human` would be a promotion: human is exempt from §6.6
// recusal and is the only principal that may decide a note.
func TestParkedResolveKeepsANonAgentSubject(t *testing.T) {
	d := newDaemon(t, nil)
	id, err := d.Store.Park(store.Parked{Project: proj, Subject: "cron:nightly",
		Verb: "tasks.create", Payload: `{"title":"from cron"}`}, d.Now())
	if err != nil {
		t.Fatalf("Park: %v", err)
	}
	mustCall(t, d, protocol.Request{Verb: "parked.resolve", Args: map[string]any{"id": id}})
	list, err := d.Store.ListTasks(store.TaskFilter{Project: proj})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("tasks = %d", len(list))
	}
	if list[0].CreatedBy != "cron:nightly" {
		t.Fatalf("created_by = %q, want cron:nightly — the resolver's principal leaked in", list[0].CreatedBy)
	}
}

// §5.5: `note discuss --question` is two state changes, so the trail carries
// two events. A mutation with no event is a hole in the audit trail.
func TestNoteDiscussWithQuestionWritesBothEvents(t *testing.T) {
	d := newDaemon(t, nil)
	mustCall(t, d, protocol.Request{Verb: "note.add", PaneID: "wF:p1", Args: map[string]any{"body": "an idea"}})
	mustCall(t, d, protocol.Request{Verb: "note.discuss", PaneID: "wF:p1",
		Args: map[string]any{"id": "1", "question": "ours or Herdr's?"}})
	raw := mustCall(t, d, protocol.Request{Verb: "events", Args: map[string]any{"entity": "note"}})
	var res EventsResult
	json.Unmarshal(raw, &res)
	got := []string{}
	for _, e := range res.Events {
		got = append(got, e.Kind)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != "added,discussing,needs_input" {
		t.Fatalf("events = %v, want added, discussing and needs_input", got)
	}
}

// §11.5: a swept lease says it was swept. Repeating a previous claimer's
// release note would tell the next claimer that work nobody did is done.
func TestSweepDoesNotRepeatAnOldReleaseNote(t *testing.T) {
	d := newDaemon(t, &config.Config{LeaseSeconds: 1, SweepSeconds: 60})
	task := createTask(t, d, "handed around")
	id := task.Task.ID
	mustCall(t, d, protocol.Request{Verb: "task.claim", PaneID: "wF:p1", Args: map[string]any{"id": id}})
	mustCall(t, d, protocol.Request{Verb: "task.release", PaneID: "wF:p1",
		Args: map[string]any{"id": id, "note": "step one done, step two is left"}})
	mustCall(t, d, protocol.Request{Verb: "task.claim", PaneID: "wF:p2", Args: map[string]any{"id": id}})
	base := d.Now()
	d.Now = func() int64 { return base + 10_000 }
	if swept := d.Sweep(); len(swept) != 1 {
		t.Fatalf("swept = %v", swept)
	}
	raw := mustCall(t, d, protocol.Request{Verb: "task.get", Args: map[string]any{"id": id}})
	if strings.Contains(string(raw), "step two is left") {
		t.Fatalf("the sweep repeated the previous claimer's note: %s", raw)
	}
	if !strings.Contains(string(raw), "lease expired") {
		t.Fatalf("the sweep did not say what it did: %s", raw)
	}
}

// §10.1: config is re-read on SIGHUP, which happens while requests are in
// flight. Under -race this fails outright when the swap is unsynchronised.
func TestReloadIsSafeWhileVerbsAreInFlight(t *testing.T) {
	d := newDaemon(t, nil)
	createTask(t, d, "in flight")

	stop := make(chan struct{})
	done := make(chan struct{}, 3)

	go func() {
		defer func() { done <- struct{}{} }()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			d.Reload(&config.Config{LeaseSeconds: int64(60 + i%600), SweepSeconds: int64(1 + i%90)})
		}
	}()
	for w := 0; w < 2; w++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for {
				select {
				case <-stop:
					return
				default:
				}
				// A gated verb, so the gate pointer is read too, and a verb
				// that reads the lease length off the config.
				if resp := d.Answer(protocol.Request{Verb: "task.claim", Project: proj,
					PaneID: "wF:p1", Args: map[string]any{"id": "1"}}); resp.Error != nil &&
					resp.Error.Code != codes.Conflict {
					t.Errorf("unexpected error: %s %s", resp.Error.Code, resp.Error.Message)
					return
				}
				d.Answer(protocol.Request{Verb: "doctor", Project: proj})
				d.Sweep()
			}
		}()
	}
	time.Sleep(250 * time.Millisecond)
	close(stop)
	for w := 0; w < 3; w++ {
		<-done
	}
}

// §10.1: a reloaded sweep interval takes effect, rather than the daemon
// keeping the value it sampled once at start while doctor reports the new one.
func TestSweepIntervalFollowsAReload(t *testing.T) {
	d := newDaemon(t, &config.Config{LeaseSeconds: 900, SweepSeconds: 60})
	if got := d.sweepInterval(); got != 60*time.Second {
		t.Fatalf("sweep interval = %s, want 60s", got)
	}
	d.Reload(&config.Config{LeaseSeconds: 900, SweepSeconds: 5})
	if got := d.sweepInterval(); got != 5*time.Second {
		t.Fatalf("sweep interval = %s after reload, want 5s", got)
	}
}

// §10.1: a reloaded lease length is what the next claim gets.
func TestLeaseLengthFollowsAReload(t *testing.T) {
	d := newDaemon(t, &config.Config{LeaseSeconds: 900, SweepSeconds: 60})
	task := createTask(t, d, "leased")
	d.Reload(&config.Config{LeaseSeconds: 60, SweepSeconds: 60})
	raw := mustCall(t, d, protocol.Request{Verb: "task.claim", PaneID: "wF:p1",
		Args: map[string]any{"id": task.Task.ID}})
	var res TaskResult
	json.Unmarshal(raw, &res)
	if got := res.Task.LeaseUntil - res.Task.ClaimedAt; got != 60_000 {
		t.Fatalf("lease = %dms, want the reloaded 60000", got)
	}
}

// A store left behind by an older build is named, not deleted, and only when
// it is really there: an operator told about a database that does not exist
// would go looking for one, and one they are not told about is the split-brain
// all over again.
func TestDoctorNamesAnOrphanStoreOnlyWhenOneExists(t *testing.T) {
	base := testenv.ShortDir(t)
	orphan := filepath.Join(base, "herdr-plugin-state")
	t.Setenv("HERDR_PLUGIN_STATE_DIR", orphan)
	d := newDaemon(t, nil)

	report := d.Doctor(protocol.Request{Project: proj}, tasks.Actor{Principal: tasks.PrincipalHuman})
	if line := findLine(report.Degraded, orphan); line != "" {
		t.Fatalf("no tasks.db is there yet, so nothing to report: %q", line)
	}

	if err := os.MkdirAll(orphan, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(orphan, "tasks.db"), []byte("not really a database"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	report = d.Doctor(protocol.Request{Project: proj}, tasks.Actor{Principal: tasks.PrincipalHuman})
	line := findLine(report.Degraded, orphan)
	if line == "" {
		t.Fatalf("the orphan store was not named: %v", report.Degraded)
	}
	if !strings.Contains(line, "not the store in use") {
		t.Fatalf("the line must say it is not the one in use: %q", line)
	}
}

// The store the daemon is actually serving must never be named as an orphan.
func TestDoctorNeverCallsTheLiveStoreAnOrphan(t *testing.T) {
	d := newDaemon(t, nil)
	t.Setenv("HERDR_PLUGIN_STATE_DIR", config.StateDir())
	report := d.Doctor(protocol.Request{Project: proj}, tasks.Actor{Principal: tasks.PrincipalHuman})
	// Any orphan line at all is wrong here: the only store is the live one.
	if line := findLine(report.Degraded, "not the store in use"); line != "" {
		t.Fatalf("doctor pointed at the live store: %q", line)
	}
}

func findLine(lines []string, needle string) string {
	for _, l := range lines {
		if strings.Contains(l, needle) {
			return l
		}
	}
	return ""
}

// §6.2 vocabulary, applied one level down: the daemon already refuses an
// unknown VERB loudly, and an argument it does not know is the same mistake.
// A door newer than the daemon otherwise has its request silently narrowed —
// the promote that succeeded with no criteria at all.
func TestUnknownArgIsRefusedByName(t *testing.T) {
	d := newDaemon(t, nil)
	task := createTask(t, d, "strict")

	resp := call(t, d, protocol.Request{Verb: "task.update",
		Args: map[string]any{"id": task.Task.ID, "title": "fine", "nonesuch": "x"}})
	if resp.Error == nil {
		t.Fatalf("an undeclared argument was accepted: %s", resp.Result)
	}
	if resp.Error.Code != codes.Usage {
		t.Fatalf("code = %s, want %s", resp.Error.Code, codes.Usage)
	}
	if !strings.Contains(resp.Error.Message, "nonesuch") {
		t.Fatalf("the message must name the argument: %q", resp.Error.Message)
	}
	if !strings.Contains(resp.Error.Message, "task.update") {
		t.Fatalf("the message must name the verb: %q", resp.Error.Message)
	}
}

// The other half, so the check cannot be satisfied by refusing everything:
// every argument the verb declares still goes through.
func TestDeclaredArgsStillPass(t *testing.T) {
	d := newDaemon(t, nil)
	task := createTask(t, d, "declared")
	mustCall(t, d, protocol.Request{Verb: "task.update", Args: map[string]any{
		"id": task.Task.ID, "title": "renamed", "description": "why",
		"priority": 3, "validation": []any{"make test-full exits 0"},
	}})
}

// Every verb in the table must be reachable with the arguments it declares.
// A typo in an Arg name would otherwise make the verb unusable only once a
// caller tried that argument.
func TestEveryDeclaredArgIsAccepted(t *testing.T) {
	for _, v := range verbs.All {
		for _, a := range v.Args {
			if !v.Accepts(a.Name) {
				t.Fatalf("%s declares %q but the checker does not accept it", v.Name, a.Name)
			}
		}
	}
}

// The fingerprint is over the door surface, not the release number: a
// hand-bumped semver stayed 0.1.0 across the change that caused the incident,
// so a version check would have compared 0.1.0 against 0.1.0 and passed.
func TestFingerprintTracksTheDoorSurfaceNotTheVersion(t *testing.T) {
	first := verbs.Fingerprint()
	if first == "" {
		t.Fatal("the fingerprint must not be empty")
	}
	if first != verbs.Fingerprint() {
		t.Fatal("the fingerprint must be stable for one build")
	}
	// Adding an argument to any verb is exactly the change that broke promote.
	changed := verbs.FingerprintOf(append([]verbs.Verb{{
		Name: "task.create", Args: []verbs.Arg{{Name: "nonesuch"}},
	}}, verbs.All...))
	if changed == first {
		t.Fatal("a changed door surface must change the fingerprint")
	}
}

// §10.3: doctor reports it, so a door can compare and say so.
func TestDoctorReportsTheFingerprint(t *testing.T) {
	d := newDaemon(t, nil)
	report := d.Doctor(protocol.Request{Project: proj}, tasks.Actor{Principal: tasks.PrincipalHuman})
	if report.Fingerprint != verbs.Fingerprint() {
		t.Fatalf("doctor fingerprint = %q, want %q", report.Fingerprint, verbs.Fingerprint())
	}
}

// §13.3: `build` answers a question `fingerprint` cannot — which binary — and
// it is a SECOND field rather than a wider hash, because `fingerprint` is
// shipped and a shipped field is never repurposed. Both are stamped on every
// answer, not only doctor's: the door that needs to know it is talking to a
// stranger is the one making an ordinary call.
func TestDoctorAndEveryAnswerCarryTheBuildBesideTheFingerprint(t *testing.T) {
	d := newDaemon(t, nil)
	report := d.Doctor(protocol.Request{Project: proj}, tasks.Actor{Principal: tasks.PrincipalHuman})
	if report.Build != verbs.ThisBuild() {
		t.Fatalf("doctor build = %+v, want %+v", report.Build, verbs.ThisBuild())
	}
	if report.Fingerprint != verbs.Fingerprint() {
		t.Fatalf("adding the build moved the fingerprint: %q", report.Fingerprint)
	}
	resp := d.Answer(protocol.Request{Verb: "task.list", Project: proj})
	if resp.Build != verbs.ThisBuild() {
		t.Fatalf("an ordinary answer carried build %+v, want %+v", resp.Build, verbs.ThisBuild())
	}
	if resp.Fingerprint != verbs.Fingerprint() {
		t.Fatalf("an ordinary answer carried fingerprint %q, want %q", resp.Fingerprint, verbs.Fingerprint())
	}
}

// captureStderr runs fn with os.Stderr replaced by a pipe and returns what was
// written to it. No test in this package runs in parallel, so the swap is
// safe.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	saved := os.Stderr
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	os.Stderr = saved
	w.Close()
	out := <-done
	r.Close()
	return out
}

// §11.5: the sweep says only what it did. A batch that holds a lease renewed
// after the scan must not name that task on stderr or in the trail — a line
// claiming a lease was taken back is what sends the next agent to claim it.
func TestSweepNamesOnlyTheLeasesItReleased(t *testing.T) {
	d := newDaemon(t, &config.Config{LeaseSeconds: 1, SweepSeconds: 60})
	renewed := createTask(t, d, "renewed under the sweep").Task.ID
	abandoned := createTask(t, d, "abandoned").Task.ID
	for _, id := range []string{renewed, abandoned} {
		mustCall(t, d, protocol.Request{Verb: "task.claim", PaneID: "wF:p1", Args: map[string]any{"id": id}})
	}
	base := d.Now()
	d.Now = func() int64 { return base + 10_000 }
	d.Store.DuringSweep = func(id string) {
		if id == renewed {
			mustCall(t, d, protocol.Request{Verb: "task.touch", PaneID: "wF:p1", Args: map[string]any{"id": id}})
		}
	}

	var swept []string
	line := captureStderr(t, func() { swept = d.Sweep() })

	if len(swept) != 1 || swept[0] != abandoned {
		t.Fatalf("swept = %v, want only %s", swept, abandoned)
	}
	if !strings.Contains(line, abandoned) {
		t.Fatalf("the sweep did not name what it released: %q", line)
	}
	if strings.Contains(line, renewed) {
		t.Fatalf("the sweep named a lease it did not release: %q", line)
	}
	raw := mustCall(t, d, protocol.Request{Verb: "task.get", Args: map[string]any{"id": renewed}})
	if !strings.Contains(string(raw), `"doing"`) {
		t.Fatalf("the renewed task lost its claim: %s", raw)
	}
	trail := sweptEvents(t, d)
	if len(trail) != 1 || trail[0] != abandoned {
		t.Fatalf("the trail records a sweep for %v, want only %s", trail, abandoned)
	}
}

// §11.5: a sweep with nothing to release is silent. An operator reading the
// daemon's log must be able to take every swept line as a real event.
func TestASweepThatReleasesNothingSaysNothing(t *testing.T) {
	d := newDaemon(t, &config.Config{LeaseSeconds: 900, SweepSeconds: 60})
	id := createTask(t, d, "still being worked on").Task.ID
	mustCall(t, d, protocol.Request{Verb: "task.claim", PaneID: "wF:p1", Args: map[string]any{"id": id}})

	var swept []string
	line := captureStderr(t, func() { swept = d.Sweep() })

	if len(swept) != 0 {
		t.Fatalf("swept = %v, want nothing", swept)
	}
	if line != "" {
		t.Fatalf("a sweep that released nothing wrote %q", line)
	}
}

// sweptEvents is the entity ids the trail records a sweep for.
func sweptEvents(t *testing.T, d *Daemon) []string {
	t.Helper()
	evs, err := d.Store.Events(store.EventFilter{AllProjects: true, Entity: "task"})
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	out := []string{}
	for _, ev := range evs {
		if ev.Kind == "tasks.task.swept" || ev.Kind == "swept" {
			out = append(out, ev.EntityID)
		}
	}
	return out
}
