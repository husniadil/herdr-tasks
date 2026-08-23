package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/husniadil/herdr-tasks/internal/client"
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

// §11.6: the prompt is what the operator is typing into, so it is the last
// thing the layout gives up. On a pane short enough that only one chrome row
// survives, the budget was spent from the top — which kept the blank line
// that separates the prompt from the body and dropped the prompt itself. The
// operator then typed into a line that was not on the screen.
func TestAShortPaneKeepsThePromptAndDropsTheBlankAboveIt(t *testing.T) {
	// From 6 up: below that panelRows leaves the prompt no row at all, which
	// is the deliberate floor that keeps the body one row, not this bug.
	for height := 6; height <= 10; height++ {
		m := New(ViewBoard, "/repo")
		m.Height = height
		m.Prompt = &Prompt{Label: "reason", Value: "because"}
		f := frameOf(m, 0)
		if len(f.prompt) == 0 {
			t.Errorf("height %d: the prompt row was dropped entirely", height)
			continue
		}
		last := f.prompt[len(f.prompt)-1]
		if !strings.Contains(last, "reason") {
			t.Errorf("height %d: the chrome rows %q carry no prompt", height, f.prompt)
		}
		for _, line := range f.prompt[:len(f.prompt)-1] {
			if strings.TrimSpace(line) != "" {
				t.Errorf("height %d: %q sits above the prompt and is not the separator", height, line)
			}
		}
	}
}

// §11.6 with §13.3: a build-skew warning is worth saying and must not be said
// onto a screen the pane owns. Written straight to the terminal under the
// alternate screen it lands inside a frame and is gone at the next redraw:
// the operator sees a corrupted pane and never sees the warning. It is held
// while the pane is up and said on the way out, where a shell can show it.
func TestTheWarningIsHeldWhileThePaneOwnsTheScreen(t *testing.T) {
	was := client.Warnings()
	var out strings.Builder
	release := holdWarnings(&out)
	if client.Warnings() == was {
		t.Fatal("the pane left the client writing to the terminal it owns")
	}
	fmt.Fprint(client.Warnings(), "htask: this door speaks a different surface\n")
	if out.String() != "" {
		t.Fatal("the warning reached the terminal while the pane held the screen")
	}
	release()
	if !strings.Contains(out.String(), "different surface") {
		t.Errorf("the held warning was dropped rather than said on the way out: %q", out.String())
	}
	if client.Warnings() != was {
		t.Error("leaving the pane did not put the previous sink back")
	}
}
