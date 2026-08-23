package tui

import "testing"

// wrapTo must not lose text to the escape sequences inside a line. The head it
// emits comes from ansi.Truncate, which re-emits a reset of its own, so the
// string handed back is NOT a prefix of the source and advancing by its byte
// length eats characters that were never shown.
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
