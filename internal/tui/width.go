package tui

import "github.com/charmbracelet/x/ansi"

// A terminal measures in CELLS, not in characters: a CJK ideograph or an emoji
// occupies two columns, a combining mark none. Everything in this package that
// decides "does this fit" therefore has to ask the same question the renderer
// asks, and these two functions are that question in one place.
//
// The functions are bubbletea's own: its standard renderer truncates every
// line it writes with ansi.Truncate at the terminal width
// (standard_renderer.go:233-242), so a width function that disagreed with this
// one would put the overflow straight back — the layout would believe a line
// fits, the renderer would cut it, and the difference would be silently
// missing text. Agreement here is structural rather than maintained.

// cells is how many columns s occupies.
func cells(s string) int { return ansi.StringWidth(s) }

// truncateCells cuts s to at most w columns, never splitting a grapheme. A cut
// that would land inside a double-width character drops it, so the result can
// be one column narrower than asked.
func truncateCells(s string, w int) string { return ansi.Truncate(s, w, "") }
