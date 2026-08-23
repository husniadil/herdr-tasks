package daemon

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"testing"
	"time"

	"github.com/husniadil/herdr-tasks/internal/codes"
	"github.com/husniadil/herdr-tasks/internal/config"
	"github.com/husniadil/herdr-tasks/internal/protocol"
	"github.com/husniadil/herdr-tasks/internal/testenv"
)

// §2.3 with §3.7: `stop` ends the one daemon this user has, and every pane is
// served by it. The refusal is not the operator-verb refusal §3.7 removed —
// it does not rest on the caller not being the operator — but the authority
// §3.7 keeps refusing: a pane ending the daemon takes the board away from
// panes that never asked, which no answer the operator gives makes this
// pane's to do.
func TestStopIsRefusedFromAPane(t *testing.T) {
	d := newDaemon(t, nil)
	resp := d.Answer(protocol.Request{Verb: "stop", Project: proj, PaneID: "pane-1"})
	if resp.Error == nil {
		t.Fatalf("stop from a pane succeeded: %s", resp.Result)
	}
	if resp.Error.Code != codes.Forbidden {
		t.Fatalf("code = %s (%s), want %s", resp.Error.Code, resp.Error.Message, codes.Forbidden)
	}
	select {
	case <-d.halt:
		t.Fatal("a refused stop still asked the daemon to end")
	default:
	}
}

// §3.7: `human` is never the fallback for knowing nothing, so a door with no
// pane and no declared operator does not get to end the daemon either.
func TestStopIsRefusedToADoorWithNoPrincipal(t *testing.T) {
	d := newDaemon(t, nil)
	resp := d.Answer(protocol.Request{Verb: "stop", Project: proj})
	if resp.Error == nil || resp.Error.Code != codes.Forbidden {
		t.Fatalf("stop from `none`: %+v", resp.Error)
	}
}

// §2.5: the daemon answers `stop` and then ends the way SIGTERM ends it — the
// answer is written first, the socket and the lock are given up, and the next
// daemon can take them. The answer arriving is the half a shutdown that raced
// its own reply would lose.
func TestStopAnswersAndThenEndsTheDaemon(t *testing.T) {
	d := newDaemon(t, nil)
	sock, lock := config.SocketPath(), config.LockPath()
	ln, err := Listen(sock, lock)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	served := make(chan error, 1)
	go func() { served <- d.Serve(context.Background(), ln) }()

	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if err := json.NewEncoder(conn).Encode(protocol.Request{
		Verb: "stop", Project: proj, Operator: true}); err != nil {
		t.Fatalf("encode: %v", err)
	}
	var resp protocol.Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	conn.Close()
	if resp.Error != nil {
		t.Fatalf("stop: %s: %s", resp.Error.Code, resp.Error.Message)
	}
	var res StopResult
	if err := json.Unmarshal(resp.Result, &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !res.Stopping || res.PID != os.Getpid() {
		t.Fatalf("stop answered %+v, want this process stopping", res)
	}

	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after stop")
	}
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Errorf("the socket file is still at %s after stop: %v", sock, err)
	}
	// The lock went with it, so the next daemon can be elected (§2.3).
	next, err := Listen(sock, lock)
	if err != nil {
		t.Fatalf("a second daemon could not take the socket after stop: %v", err)
	}
	next.Close()
}

// §12.3: nothing here may reach the operator's live socket. config.SocketPath
// is under the temp state dir newDaemon set, and the answer says so.
func TestStopNamesTheSocketItIsGivingUp(t *testing.T) {
	d := newDaemon(t, nil)
	testenv.SkipUnlessFull(t)
	raw := mustCall(t, d, protocol.Request{Verb: "stop"})
	var res StopResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if res.Socket != config.SocketPath() {
		t.Errorf("stop named %s, want %s", res.Socket, config.SocketPath())
	}
}
