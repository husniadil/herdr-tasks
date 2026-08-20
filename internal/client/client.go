// Package client is the door side of the socket: dial the daemon, start it if
// it is not there, and carry one request (§2.2).
package client

import (
	"bufio"
	"encoding/json"
	"fmt"
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

// Call sends one request and returns the answer. It starts the daemon if none
// is listening.
func Call(req protocol.Request) (json.RawMessage, error) {
	conn, err := dialOrStart()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return nil, codes.Errorf(codes.Unavailable, "send %s: %v", req.Verb, err)
	}
	var resp protocol.Response
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&resp); err != nil {
		return nil, codes.Errorf(codes.Unavailable, "read the answer to %s: %v", req.Verb, err)
	}
	warnOnSkew(resp.Fingerprint)
	if resp.Error != nil {
		return nil, &Failure{Body: resp.Error}
	}
	return resp.Result, nil
}

// skewWarned keeps the warning to once per process. It is worth saying, but
// not once per call in a loop.
var skewWarned sync.Once

// warnOnSkew says so when the answering daemon does not speak this door's
// surface. It warns rather than refuses: the request has already been
// answered, and a door that failed here would break the upgrade it is meant
// to make visible. An empty fingerprint counts — a daemon predating the field
// is exactly the one that drops arguments it never learned.
func warnOnSkew(got string) {
	mine := verbs.Fingerprint()
	if got == mine {
		return
	}
	skewWarned.Do(func() {
		daemon := "reports " + got
		if got == "" {
			daemon = "is too old to say which"
		}
		fmt.Fprintf(os.Stderr,
			"ht: this door speaks %s and the daemon %s; parts of a request it does not know are dropped — restart the daemon\n",
			mine, daemon)
	})
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
			return nil
		}
		if resp.Error != nil {
			return &Failure{Body: resp.Error}
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
		"the daemon did not come up within %s; try `ht daemon` in a terminal to see why", StartTimeout)
}

// start launches `ht daemon` detached from this process, so the daemon
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
