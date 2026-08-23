// Package client is the door side of the socket: dial the daemon, start it if
// it is not there, and carry one request (§2.2).
package client

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/husniadil/herdr-tasks/internal/codes"
	"github.com/husniadil/herdr-tasks/internal/config"
	"github.com/husniadil/herdr-tasks/internal/protocol"
	"github.com/husniadil/herdr-tasks/internal/verbs"
)

// StartTimeout bounds the autostart wait. §2.2: a CLI invocation that finds no
// live socket starts the daemon and waits for it, bounded, rather than fail.
const StartTimeout = 3 * time.Second

// CallTimeout bounds one request and its answer once the socket is connected.
// It is generous rather than tight: the answer can wait on a policy gate
// subprocess (§9.2), and this exists to end a wedged connection, not to
// second-guess a slow one. `events --follow` has no such bound, by design.
// It is a var so a test can shorten it; nothing else writes it.
var CallTimeout = 30 * time.Second

// Call sends one request and returns the answer. It starts the daemon if none
// is listening.
func Call(req protocol.Request) (json.RawMessage, error) {
	conn, err := dialOrStart()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	// A connected socket that never answers is not the same as an unreachable
	// one, and without a deadline it is indistinguishable from a call that
	// simply takes a while: the read blocks forever, and a caller that polls
	// — the TUI does, every two seconds — leaks a goroutine per attempt.
	if err := conn.SetDeadline(time.Now().Add(CallTimeout)); err != nil {
		return nil, codes.Errorf(codes.Unavailable, "set the deadline for %s: %v", req.Verb, err)
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return nil, codes.Errorf(codes.Unavailable, "send %s: %v", req.Verb, err)
	}
	var resp protocol.Response
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&resp); err != nil {
		return nil, codes.Errorf(codes.Unavailable, "read the answer to %s: %v", req.Verb, err)
	}
	warnOnSkew(os.Stderr, resp.Fingerprint, resp.Build)
	if resp.Error != nil {
		return nil, &Failure{Body: resp.Error}
	}
	return resp.Result, nil
}

// skewWarned keeps the warning to once per process. It is worth saying, but
// not once per call in a loop.
var skewWarned sync.Once

// warnOnSkew says so when the answering daemon is not this door's equal. It
// warns rather than refuses: the request has already been answered, and a
// door that failed here would break the upgrade it is meant to make visible.
//
// There are two ways to disagree and they are not the same news. A different
// SURFACE means the daemon is dropping arguments it never learned. A different
// BUILD means the same argument is answered by different code — nothing is
// dropped, the behaviour simply is not this binary's, which is how a stale
// daemon went on accepting text the bounds had already refused. The surface
// is said when both differ: a different surface is necessarily a different
// build, and dropped arguments are the more urgent half.
//
// An empty value on either counts as a mismatch. A daemon predating a field
// is exactly the daemon the field was added to catch.
func warnOnSkew(w io.Writer, got string, build verbs.Build) {
	mine := verbs.Fingerprint()
	switch {
	case got != mine:
		skewWarned.Do(func() {
			daemon := "reports " + got
			if got == "" {
				daemon = "is too old to say which"
			}
			fmt.Fprintf(w,
				"htask: this door speaks %s and the daemon %s; parts of a request it does not know are dropped — restart the daemon\n",
				mine, daemon)
		})
	case !verbs.ThisBuild().Same(build):
		skewWarned.Do(func() {
			daemon := "is " + build.Short()
			if build.Short() == "" {
				daemon = "is too old to say which"
			}
			fmt.Fprintf(w,
				"htask: this door is build %s and the daemon %s; it answers with different code — restart the daemon\n",
				verbs.ThisBuild().Short(), daemon)
		})
	}
}

// Stream sends one request and hands every answer to fn until the daemon stops
// or fn returns an error. This is `events --follow` (§8.2).
func Stream(req protocol.Request, fn func(json.RawMessage) error) error {
	conn, err := dialOrStart()
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return codes.Errorf(codes.Unavailable, "send %s: %v", req.Verb, err)
	}
	dec := json.NewDecoder(bufio.NewReader(conn))
	for {
		var resp protocol.Response
		if err := dec.Decode(&resp); err != nil {
			// A closed socket is not an ending. The daemon says when a stream
			// is over (Done); anything else that stops the decoder is the
			// daemon going away under us, and returning nil there made a
			// killed daemon and a finished stream the same thing — the caller
			// exited 0 having silently stopped watching.
			return codes.Errorf(codes.Unavailable,
				"the daemon stopped streaming without saying it had finished: %v", err)
		}
		warnOnSkew(os.Stderr, resp.Fingerprint, resp.Build)
		if resp.Error != nil {
			return &Failure{Body: resp.Error}
		}
		if resp.Done {
			return nil
		}
		if err := fn(resp.Result); err != nil {
			return err
		}
	}
}

// Failure is a daemon error carried back with its contract code, so the CLI
// can render the §6.2 envelope and exit with the §6.3 status.
type Failure struct {
	Body *protocol.ErrorBody
}

func (f *Failure) Error() string { return f.Body.Code + ": " + f.Body.Message }

// Code is the §6.3 code.
func (f *Failure) Code() string { return f.Body.Code }

// ParkedID is the §9.3 id of the action the policy gate deferred, empty when
// this failure is not a parked DENIED.
func (f *Failure) ParkedID() string { return f.Body.ParkedID }

// Message is the daemon's text for the failure, without the code prefix.
func (f *Failure) Message() string { return f.Body.Message }

func dialOrStart() (net.Conn, error) {
	path := config.SocketPath()
	if conn, err := net.DialTimeout("unix", path, 300*time.Millisecond); err == nil {
		return conn, nil
	}
	if err := start(); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(StartTimeout)
	wait := 20 * time.Millisecond
	for time.Now().Before(deadline) {
		time.Sleep(wait)
		if conn, err := net.DialTimeout("unix", path, 300*time.Millisecond); err == nil {
			return conn, nil
		}
		if wait < 200*time.Millisecond {
			wait *= 2
		}
	}
	return nil, codes.Errorf(codes.Unavailable,
		"the daemon did not come up within %s; try `htask daemon` in a terminal to see why", StartTimeout)
}

// start launches `htask daemon` detached from this process, so the daemon
// survives the pane that opened it being closed (§2.4).
func start() error {
	self, err := os.Executable()
	if err != nil {
		return codes.Errorf(codes.Unavailable, "cannot find this binary to start the daemon: %v", err)
	}
	if err := config.EnsureStateDir(); err != nil {
		return codes.Errorf(codes.Unavailable, "cannot create the state dir: %v", err)
	}
	cmd := exec.Command(self, "daemon")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	cmd.Env = os.Environ()
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return codes.Errorf(codes.Unavailable, "cannot start the daemon: %v", err)
	}
	// Release the child rather than leaving a zombie behind this short-lived
	// CLI process.
	go cmd.Process.Release()
	return nil
}
