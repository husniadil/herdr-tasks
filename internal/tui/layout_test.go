package tui

import (
	"testing"

	"github.com/husniadil/herdr-tasks/internal/protocol"
)

// §11.6: the pane is what the human reads, so wrapTo must not lose text to the
// escape sequences inside a line. The head it emits comes from ansi.Truncate,
// which re-emits a reset of its own, so the string handed back is NOT a prefix
// of the source and advancing by its byte length eats characters that were
// never shown.
func TestWrapToKeepsEveryCharacterOfAStyledLine(t *testing.T) {
	const line = "a\x1b[31mred\x1b[0mbcdef"
	got := wrapTo(line, 3)
	plain := func(s string) string {
		out := []rune{}
		esc := false
		for _, r := range s {
			switch {
			case r == 0x1b:
				esc = true
			case esc:
				if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
					esc = false
				}
			default:
				out = append(out, r)
			}
		}
		return string(out)
	}
	joined := ""
	for _, l := range got {
		if cells(l) > 3 {
			t.Fatalf("wrapped line %q is %d cells, over the 3 asked", l, cells(l))
		}
		joined += plain(l)
	}
	if want := plain(line); joined != want {
		t.Fatalf("wrapTo dropped text: got %q, want %q (lines %q)", joined, want, got)
	}
}

// §11.6: a tick is a timer, not an answer. A daemon that has stopped answering
// would otherwise get one more read — one more goroutine, one more socket —
// every two seconds for as long as the operator leaves the pane open.
func TestTickDoesNotStackReadsOnAWedgedDaemon(t *testing.T) {
	rec := &recorder{}
	p := &program{model: New(ViewBoard, "/repo"), send: rec, base: protocol.Request{Project: "/repo"}}
	p.Init()
	for i := 0; i < 5; i++ {
		if _, cmd := p.Update(tickMsg{}); cmd == nil {
			t.Fatal("the tick stopped re-arming itself")
		}
	}
	if !p.loading {
		t.Fatal("the first read was never marked in flight")
	}
	// The one read Init asked for has not come back, so no tick started
	// another; the answer arriving is what lets the next tick read again.
	if _, cmd := p.Update(DataMsg{}); cmd != nil {
		_ = cmd
	}
	if p.loading {
		t.Fatal("an answer did not clear the in-flight read")
	}
	_, cmd := p.Update(tickMsg{})
	if cmd == nil || !p.loading {
		t.Fatal("the tick after an answer did not read again")
	}
}
