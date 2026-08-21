// Package daemon is the socket server and the only writer of the store
// (§2.2). One daemon per user; a Herdr session is just a caller (§2.3).
package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode"

	"github.com/husniadil/herdr-tasks/internal/codes"
	"github.com/husniadil/herdr-tasks/internal/config"
	"github.com/husniadil/herdr-tasks/internal/gate"
	"github.com/husniadil/herdr-tasks/internal/herdrclient"
	"github.com/husniadil/herdr-tasks/internal/protocol"
	"github.com/husniadil/herdr-tasks/internal/store"
	"github.com/husniadil/herdr-tasks/internal/tasks"
	"github.com/husniadil/herdr-tasks/internal/verbs"
)

// Version is the plugin version `htask version` and doctor print (§13.3).
// The MCP tool list is semver-bound (§7.1) and 0.2.0 is the first release
// whose tools are named by the verb alone, which a client with the old names
// wired in cannot call — a major-shaped change carried in the minor, as a
// 0.x version is allowed to.
const Version = "0.2.0"

// ContractVersion is the revision of the shared plugin contract this binary
// satisfies. §13.4 requires a plugin to declare it in its README and in
// `doctor` output, which is what a reader compares a binary against.
const ContractVersion = "0.6.0"

// Daemon holds everything a verb needs to answer.
type Daemon struct {
	Store *store.Store
	Herdr *herdrclient.Client
	// StreamPolls counts how many times a follower's loop has re-read the
	// trail. A stream nobody is reading must stop moving it, and counting is
	// how a test can say so without waiting on a clock.
	StreamPolls atomic.Int64
	// Now is the clock in Unix milliseconds (§5.3), injectable for tests.
	Now func() int64

	// cfg and gate are swapped wholesale by Reload on SIGHUP (§10.1) while
	// requests are in flight, so they are read through Cfg and Policy under
	// cfgMu rather than touched directly. The structs they point at are
	// immutable once built, so holding the read lock for the pointer is
	// enough — callers do not need to hold it while they use the value.
	cfgMu sync.RWMutex
	cfg   *config.Config
	gate  *gate.Gate

	mu       sync.Mutex
	watchers map[chan store.Event]struct{}
}

// Cfg is the configuration as of now.
func (d *Daemon) Cfg() *config.Config {
	d.cfgMu.RLock()
	defer d.cfgMu.RUnlock()
	return d.cfg
}

// Policy is the gate as of now (§9).
func (d *Daemon) Policy() *gate.Gate {
	d.cfgMu.RLock()
	defer d.cfgMu.RUnlock()
	return d.gate
}

// New builds a daemon over an already-open store.
func New(s *store.Store, cfg *config.Config, h *herdrclient.Client) *Daemon {
	return &Daemon{
		Store:    s,
		Herdr:    h,
		cfg:      cfg,
		gate:     gate.New(cfg.GateCommand),
		Now:      func() int64 { return time.Now().UnixMilli() },
		watchers: map[chan store.Event]struct{}{},
	}
}

// lockedListener is a listener that also holds the daemon's exclusive lock.
// Closing it releases both, and so does the process ending: the kernel drops
// an flock with the descriptor, which is why a daemon killed outright leaves
// nothing for the next one to clean up.
type lockedListener struct {
	net.Listener
	lock *os.File
}

func (l *lockedListener) Close() error {
	err := l.Listener.Close()
	l.lock.Close()
	return err
}

// Listen opens the socket at 0600 (§3.5), holding an exclusive lock on lock
// across the whole clear-and-bind so that only one daemon per store can be in
// it (§2.3). Without the lock two starts could both find no daemon listening,
// and the second would unlink the socket the first had just bound: two live
// daemons on one store, one of them unreachable forever.
//
// A stale socket file from a daemon that died is removed only after the lock
// is ours AND a connect proves nobody is listening — the second check is what
// keeps this honest against a daemon from an older build that holds no lock.
func Listen(path, lock string) (net.Listener, error) {
	// A Unix socket path has a hard length limit in the kernel (104 bytes on
	// darwin, 108 on linux), and the failure it produces otherwise is a bare
	// "invalid argument" that says nothing about the cause.
	if len(path) > 100 {
		return nil, codes.Errorf(codes.Unavailable,
			"socket path is %d characters, past what a unix socket allows: set TASKS_STATE_DIR to something shorter (%s)",
			len(path), path)
	}
	held, err := os.OpenFile(lock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, codes.Errorf(codes.Unavailable, "open the daemon lock %s: %v", lock, err)
	}
	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		held.Close()
		return nil, codes.Errorf(codes.Conflict,
			"another daemon holds %s; that one is the daemon for this store", lock)
	}
	// The file may predate this build, or have been created with a looser
	// umask; the lock is only as private as the state dir it names.
	if err := os.Chmod(lock, 0o600); err != nil {
		held.Close()
		return nil, codes.Errorf(codes.Unavailable, "restrict %s to this user: %v", lock, err)
	}
	if conn, err := net.DialTimeout("unix", path, 300*time.Millisecond); err == nil {
		conn.Close()
		held.Close()
		return nil, codes.Errorf(codes.Conflict, "a daemon is already listening on %s", path)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		held.Close()
		return nil, codes.Errorf(codes.Unavailable, "clear stale socket %s: %v", path, err)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		held.Close()
		return nil, codes.Errorf(codes.Unavailable, "listen on %s: %v", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		held.Close()
		return nil, codes.Errorf(codes.Unavailable, "restrict %s to this user: %v", path, err)
	}
	return &lockedListener{Listener: ln, lock: held}, nil
}

// Serve accepts connections until ctx is done. Each connection carries one
// request and its answer, except `events --follow`, which streams.
func (d *Daemon) Serve(ctx context.Context, ln net.Listener) error {
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	go d.sweepLoop(ctx)
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go d.serveConn(ctx, conn)
	}
}

func (d *Daemon) serveConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	dec := json.NewDecoder(bufio.NewReader(conn))
	enc := json.NewEncoder(conn)
	var req protocol.Request
	if err := dec.Decode(&req); err != nil {
		enc.Encode(protocol.Response{Error: &protocol.ErrorBody{
			Code: codes.Usage, Message: "unreadable request: " + err.Error()}})
		return
	}
	if req.Verb == "events" && req.Follow {
		if _, _, err := d.admit(req); err != nil {
			enc.Encode(d.stamp(protocol.Response{Error: errorBody(err)}))
			return
		}
		// A follower that went away was only noticed when an event happened to
		// match its filter, because that is the only moment the stream wrote
		// anything. On a quiet project it never happened, and the goroutine,
		// the descriptor, the watcher registration and a 1 Hz read of the
		// trail stayed for the daemon's whole life. The client sends one
		// request and then only reads, so a read here returns exactly when it
		// is gone — no ping, and nothing to mistake for an idle client.
		gone, hangUp := context.WithCancel(ctx)
		go func() {
			defer hangUp()
			io.Copy(io.Discard, conn)
		}()
		d.streamEvents(gone, req, enc)
		hangUp()
		return
	}
	enc.Encode(d.Answer(req))
}

// Answer runs one request and renders the §6.2 envelope. It is the whole
// daemon as a function, which is what lets the verb tests skip the socket.
func (d *Daemon) Answer(req protocol.Request) protocol.Response {
	return d.stamp(d.answer(req))
}

// stamp puts this daemon's surface and build on an answer. Every answer, not
// only doctor's, and not only the one-shot ones: the door that needs to know
// it is talking to a stranger is the one making an ordinary call, and a
// follower holding a connection open for hours is the one most likely to
// outlive a daemon restart.
func (d *Daemon) stamp(resp protocol.Response) protocol.Response {
	resp.Fingerprint = verbs.Fingerprint()
	resp.Build = verbs.ThisBuild()
	return resp
}

// errorBody renders one failure as the §6.2 error half.
func errorBody(err error) *protocol.ErrorBody {
	body := &protocol.ErrorBody{Code: codes.Unexpected, Message: err.Error()}
	var ce *codes.Error
	if errors.As(err, &ce) {
		body.Code, body.Message = ce.Code, ce.Message
	}
	var pe *parkedError
	if errors.As(err, &pe) {
		body.ParkedID = pe.id
	}
	return body
}

func (d *Daemon) answer(req protocol.Request) protocol.Response {
	result, err := d.Handle(req)
	if err != nil {
		return protocol.Response{Error: errorBody(err)}
	}
	raw, merr := json.Marshal(result)
	if merr != nil {
		return protocol.Response{Error: &protocol.ErrorBody{
			Code: codes.Unexpected, Message: "cannot render result: " + merr.Error()}}
	}
	return protocol.Response{Result: raw}
}

// parkedError is a DENIED that carries the id of the action the gate parked
// (§9.3).
type parkedError struct {
	err *codes.Error
	id  string
}

func (e *parkedError) Error() string { return e.err.Error() }

// Unwrap lets the envelope find the code without knowing about parking.
func (e *parkedError) Unwrap() error { return e.err }

// Handle resolves the principal, runs the gate, and dispatches the verb.
func (d *Daemon) Handle(req protocol.Request) (any, error) {
	v, actor, err := d.admit(req)
	if err != nil {
		return nil, err
	}
	return d.dispatch(v.Name, req, actor)
}

// admit is everything that happens to a request BEFORE its verb runs: the verb
// exists, every argument is one it declares, the principal is derived, and the
// policy gate has had its say. `events --follow` runs it too: routing a
// stream straight past these checks would let a door newer than the daemon
// have the part the daemon does not know silently dropped, and be told it was
// following. One function, called by both paths.
func (d *Daemon) admit(req protocol.Request) (verbs.Verb, tasks.Actor, error) {
	v, ok := verbs.ByName(req.Verb)
	if !ok {
		return verbs.Verb{}, tasks.Actor{}, codes.Errorf(codes.Usage, "unknown verb %q", req.Verb)
	}
	// An argument this verb does not declare is refused rather than dropped.
	// A door newer than the daemon would otherwise have the part the daemon
	// does not know silently removed and be told it succeeded.
	for _, name := range sortedKeys(req.Args) {
		if !v.Accepts(name) {
			return verbs.Verb{}, tasks.Actor{}, codes.Errorf(codes.Usage,
				"%s does not take %q; this door may be newer than the daemon, in which case restart the daemon",
				req.Verb, name)
		}
	}
	actor, err := d.actor(req)
	if err != nil {
		return verbs.Verb{}, tasks.Actor{}, err
	}
	if v.Gated != "" {
		target := argString(req.Args, "id")
		res := d.Policy().Check(gate.Request{Subject: string(actor.Principal), Verb: v.Gated, Target: target})
		switch res.Decision {
		case gate.Deny:
			return verbs.Verb{}, tasks.Actor{}, codes.Errorf(codes.Denied,
				"the policy gate refused %s: %s", v.Gated, res.Reason)
		case gate.Defer:
			payload, _ := json.Marshal(req.Args)
			id, perr := d.Store.Park(store.Parked{
				Project: req.Project, Subject: string(actor.Principal), Verb: v.Gated,
				Target: target, Payload: string(payload), Reason: res.Reason}, d.Now())
			if perr != nil {
				return verbs.Verb{}, tasks.Actor{}, perr
			}
			return verbs.Verb{}, tasks.Actor{}, &parkedError{
				err: codes.Errorf(codes.Denied, "the policy gate parked %s for the operator: %s", v.Gated, res.Reason),
				id:  id,
			}
		}
	}
	return v, actor, nil
}

// actor derives the caller's principal (§3.2) and, for an agent, snapshots the
// three Herdr facts at the moment they matter (§3.4).
func (d *Daemon) actor(req protocol.Request) (tasks.Actor, error) {
	if req.As != "" {
		kind, _, _ := strings.Cut(req.As, ":")
		switch kind {
		case "cron", "trigger", "plugin":
			// plugin:tasks is the board's own hand: it is what this daemon
			// writes with when it sweeps a lease, so a caller declaring it
			// would forge the plugin's signature in the event trail. §3.2
			// lets a plugin refuse the principals it owns, and this is the
			// one it owns. Sibling plugins stay declarable (§3.5).
			if tasks.Principal(req.As) == tasks.PrincipalPlugin {
				return tasks.Actor{}, codes.Errorf(codes.Forbidden,
					"--as %s is not accepted: that is this plugin's own principal (§3.2)", req.As)
			}
			// A principal is written verbatim into the event trail and
			// rendered into single-line prose, so an id carrying whitespace
			// or a control character is refused before it is recorded.
			if strings.IndexFunc(req.As, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
				return tasks.Actor{}, codes.Errorf(codes.Usage,
					"--as %q holds whitespace or control characters; a principal id is one printable word (§3.1)", req.As)
			}
			return tasks.Actor{Principal: tasks.Principal(req.As)}, nil
		default:
			return tasks.Actor{}, codes.Errorf(codes.Forbidden,
				"--as %s is not accepted: agent and human principals are derived, never declared (§3.2)", req.As)
		}
	}
	if req.PaneID == "" {
		return tasks.Actor{Principal: tasks.PrincipalHuman}, nil
	}
	a := tasks.Actor{Principal: tasks.Principal("agent:" + req.PaneID), Harness: "unknown"}
	snap, err := d.Herdr.AgentGet(req.PaneID)
	if err != nil {
		// Herdr being unreachable does not turn an agent into a human; it
		// turns its harness into "unknown", which §3.4 prefers to a guess.
		return a, nil
	}
	a.Name, a.Harness, a.Session = snap.Name, snap.Harness, snap.Session
	if a.Harness == "" {
		a.Harness = "unknown"
	}
	return a, nil
}

// sweepLoop is the bounded timer of §11.5. It releases expired leases and the
// sweep is recorded in the task's events by the store.
func (d *Daemon) sweepLoop(ctx context.Context) {
	// The interval is re-read every tick rather than sampled once: SIGHUP can
	// change it, and a daemon that reports one cadence in `doctor` while
	// running another is lying about itself.
	interval := d.sweepInterval()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.Sweep()
			if next := d.sweepInterval(); next != interval {
				interval = next
				t.Reset(interval)
			}
		}
	}
}

// sweepInterval is the configured cadence of the §11.5 timer.
func (d *Daemon) sweepInterval() time.Duration {
	seconds := d.Cfg().SweepSeconds
	if seconds <= 0 {
		seconds = config.DefaultSweepSeconds
	}
	return time.Duration(seconds) * time.Second
}

// Sweep runs one pass of the lease sweep and returns what it released.
func (d *Daemon) Sweep() []string {
	swept, err := d.Store.SweepLeases(d.Now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "tasks: lease sweep: %v\n", err)
		return nil
	}
	for _, id := range swept {
		fmt.Fprintf(os.Stderr, "tasks: swept the lease on %s\n", id)
	}
	if len(swept) > 0 {
		d.notify()
	}
	return swept
}

// notify wakes every `events --follow` stream. The stream re-reads from the
// store rather than being handed rows, so a watcher that fell behind catches
// up rather than missing events.
func (d *Daemon) notify() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for ch := range d.watchers {
		select {
		case ch <- store.Event{}:
		default:
		}
	}
}

func (d *Daemon) watch() chan store.Event {
	ch := make(chan store.Event, 1)
	d.mu.Lock()
	d.watchers[ch] = struct{}{}
	d.mu.Unlock()
	return ch
}

func (d *Daemon) unwatch(ch chan store.Event) {
	d.mu.Lock()
	delete(d.watchers, ch)
	d.mu.Unlock()
}

// runHook fires the configured event hook, detached with all three stdio
// closed. A hook that fails must not fail the write that caused it (§8.3).
func (d *Daemon) runHook(ev store.Event) {
	hook := d.Cfg().OnEvent
	if len(hook) == 0 {
		return
	}
	cmd := exec.Command(hook[0], hook[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	cmd.Env = append(os.Environ(),
		"TASKS_EVENT="+ev.Name,
		"TASKS_ENTITY="+ev.Entity,
		"TASKS_ID="+ev.EntityID,
		"TASKS_PROJECT="+ev.Project,
		"TASKS_ACTOR="+string(ev.Actor),
		"TASKS_KIND="+ev.Kind,
		"TASKS_AT="+fmt.Sprint(ev.At),
	)
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "tasks: event hook: %v\n", err)
		return
	}
	go cmd.Wait()
}

// emitted is called after a successful mutation: it fires the hook and wakes
// the followers.
func (d *Daemon) emitted(project, entity, entityID string) {
	evs, err := d.Store.Events(store.EventFilter{Project: project, Entity: entity, EntityID: entityID})
	if err == nil && len(evs) > 0 {
		d.runHook(evs[len(evs)-1])
	}
	d.notify()
}

// streamEvents is `events --follow`, the subscription primitive of §8.2. There
// is no push bus in the contract; this is a store read woken by a mutation.
func (d *Daemon) streamEvents(ctx context.Context, req protocol.Request, enc *json.Encoder) {
	f := store.EventFilter{
		Project:     req.Project,
		AllProjects: req.AllProjects,
		Entity:      argString(req.Args, "entity"),
	}
	// --limit is declared on the verb, so a stream that ignored it was a door
	// promising something it did not do. It bounds the WHOLE stream: `events
	// --follow --limit 1` waits for the next event, prints it, and ends.
	left := int(argInt(req.Args, "limit"))
	bounded := left > 0
	since := argString(req.Args, "since")
	if since != "" {
		if ms, ok := parseMS(since); ok {
			f.SinceAt = ms
		} else {
			f.SinceID = since
		}
	}
	ch := d.watch()
	defer d.unwatch(ch)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		d.StreamPolls.Add(1)
		evs, err := d.Store.Events(f)
		if err != nil {
			enc.Encode(d.stamp(protocol.Response{Error: errorBody(err)}))
			return
		}
		for _, ev := range evs {
			raw, merr := json.Marshal(ev)
			if merr != nil {
				continue
			}
			if err := enc.Encode(d.stamp(protocol.Response{Result: raw})); err != nil {
				return
			}
			f.SinceID, f.SinceAt = ev.ID, 0
			if bounded {
				if left--; left == 0 {
					// Said on purpose, so the follower can tell a stream that
					// finished from a daemon that died: at the socket both are
					// just a closed connection.
					enc.Encode(d.stamp(protocol.Response{Done: true}))
					return
				}
			}
		}
		select {
		case <-ctx.Done():
			enc.Encode(d.stamp(protocol.Response{Done: true}))
			return
		case <-ch:
		case <-tick.C:
		}
	}
}

// Reload swaps in a config re-read on SIGHUP (§10.1). The gate is rebuilt with
// it, so a policy command added to the config takes effect without a restart.
// Both move under the write lock, so a request in flight sees the old pair or
// the new one and never a half-applied mix.
func (d *Daemon) Reload(cfg *config.Config) {
	next := gate.New(cfg.GateCommand)
	d.cfgMu.Lock()
	defer d.cfgMu.Unlock()
	d.cfg, d.gate = cfg, next
}

// sortedKeys is the argument names in a stable order, so a request with two
// undeclared arguments names the same one every time.
func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
