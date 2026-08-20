package client

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/husniadil/herdr-tasks/internal/protocol"
	"github.com/husniadil/herdr-tasks/internal/verbs"
)

// say runs one skew check from a clean slate: the warning is once per process
// by design, which would make every case after the first silent.
func say(fingerprint string, build verbs.Build) string {
	skewWarned = sync.Once{}
	var b strings.Builder
	warnOnSkew(&b, fingerprint, build)
	return b.String()
}

// §13.3: a door and a daemon can disagree in two different ways, and telling
// the operator the wrong one is worse than telling them nothing. A different
// SURFACE means arguments are being dropped. A different BUILD means the same
// request is answered by different code — nothing is dropped, the behaviour
// simply is not this binary's.
func TestBuildSkewIsSaidInItsOwnWords(t *testing.T) {
	mine, myBuild := verbs.Fingerprint(), verbs.ThisBuild()

	if got := say(mine, myBuild); got != "" {
		t.Fatalf("the same door and daemon warned: %q", got)
	}

	other := verbs.Build{Exe: myBuild.Exe, Stamp: "1-1", Revision: "8862a01fdeef"}
	got := say(mine, other)
	if got == "" {
		t.Fatal("a daemon running a different build said nothing")
	}
	if strings.Contains(got, "dropped") {
		t.Fatalf("a build difference was reported as an argument being dropped: %q", got)
	}
	for _, want := range []string{myBuild.Short(), other.Short(), "restart"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the warning does not name %q: %q", want, got)
		}
	}

	// A daemon predating the field carries none, which is the same retroactive
	// signal an empty fingerprint already is.
	if got := say(mine, verbs.Build{}); got == "" {
		t.Fatal("a daemon too old to carry a build said nothing")
	}

	// The surface sentence is untouched, and it is the one that gets said when
	// both differ: a different surface is necessarily a different build, and
	// dropped arguments are the more urgent half.
	surface := say("d4d7e932d7079509", other)
	if !strings.Contains(surface, "dropped") || !strings.Contains(surface, "d4d7e932d7079509") {
		t.Fatalf("the surface warning changed: %q", surface)
	}
	if strings.Count(surface, "\n") != 1 {
		t.Fatalf("a door that disagrees twice said it twice: %q", surface)
	}
	if got := say("", verbs.Build{}); !strings.Contains(got, "too old") {
		t.Fatalf("an empty fingerprint lost its wording: %q", got)
	}
}

// §13.3 / task 13: the skew warning belongs on the stream too. A follower
// holds one connection open for hours, so it is the door most likely to still
// be talking to a daemon that has since been replaced — and it heard nothing,
// because the warning lived only on the one-shot path.
func TestStreamWarnsAboutADifferentBuild(t *testing.T) {
	skewWarned = sync.Once{}
	var warned strings.Builder
	saved := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	done := make(chan struct{})
	go func() { io.Copy(&warned, r); close(done) }()

	path := filepath.Join(shortDir(t), "tasks.sock")
	t.Setenv("TASKS_STATE_DIR", filepath.Dir(path))
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		bufio.NewReader(conn).ReadString('\n')
		// This daemon speaks the same surface and runs different code.
		fmt.Fprintf(conn, `{"result":{"id":"E1"},"fingerprint":%q,"build":{"revision":"deadbeefdeadbeef"}}`+"\n",
			verbs.Fingerprint())
		fmt.Fprintln(conn, `{"done":true,"fingerprint":"`+verbs.Fingerprint()+`","build":{"revision":"deadbeefdeadbeef"}}`)
		conn.Close()
	}()

	seen := 0
	err = Stream(protocol.Request{Verb: "events", Follow: true}, func(json.RawMessage) error {
		seen++
		return nil
	})
	os.Stderr = saved
	w.Close()
	<-done
	r.Close()

	if err != nil {
		t.Fatalf("a stream that ended on purpose returned %v", err)
	}
	if seen != 1 {
		t.Fatalf("the stream delivered %d documents, want 1", seen)
	}
	if !strings.Contains(warned.String(), "different code") {
		t.Fatalf("the stream said nothing about the build: %q", warned.String())
	}
}

// shortDir is a temp dir short enough for a unix socket path, which macOS's
// TMPDIR is not.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "htclient")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}
