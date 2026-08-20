package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/husniadil/herdr-tasks/internal/tasks"
)

// The board's fixed rows. Row 0 is the view tabs, row 1 the column headings,
// and the cards start at row 2. Keeping the arithmetic here, pure, is what
// lets the mouse tests hit-test a click without a terminal.
const (
	tabsRow    = 0
	headingRow = 1
	firstCard  = 2
	footerRows = 2
)

// ColumnAt maps an x cell to a board column.
func ColumnAt(width, x int) int {
	if width <= 0 || x < 0 {
		return 0
	}
	w := width / len(Columns)
	if w <= 0 {
		return 0
	}
	col := x / w
	if col >= len(Columns) {
		col = len(Columns) - 1
	}
	return col
}

// RowAt maps a y cell to a card index, or -1 when the click was not on a card.
func RowAt(y int) int {
	if y < firstCard {
		return -1
	}
	return y - firstCard
}

// TabAt maps a click on the tabs row to a view, or "" when it hit neither.
func TabAt(x int) View {
	switch {
	case x < len(boardTab):
		return ViewBoard
	case x < len(boardTab)+len(notesTab):
		return ViewNotes
	}
	return ""
}

const (
	boardTab = "  board  "
	notesTab = "  notes  "
)

// click is the mouse-first half of §11.6: a click on the tabs switches view, a
// click on a card selects it and opens the detail panel, and a click on a
// footer verb runs that verb — the same Call the keystroke would have made.
func click(m Model, at MouseMsg) (Model, *Call) {
	if m.Prompt != nil {
		return m, nil
	}
	if at.Y == tabsRow {
		if v := TabAt(at.X); v != "" {
			m.View = v
			m.Detail = false
			return m.clampCursors(), nil
		}
		return m, nil
	}
	if at.Y >= m.Height-footerRows {
		return footerClick(m, at.X)
	}
	if m.View == ViewBoard {
		col := ColumnAt(m.Width, at.X)
		row := RowAt(at.Y)
		if row < 0 || row >= len(m.Column(col)) {
			return m, nil
		}
		m.Col, m.Row[col], m.Detail = col, row, true
		return m, nil
	}
	half := m.Width / 2
	row := RowAt(at.Y)
	if at.X < half {
		if row < 0 || row >= len(m.Notes) {
			return m, nil
		}
		m.Pane, m.NoteRow, m.Detail = PaneNotes, row, true
		return m, nil
	}
	if row < 0 || row >= len(m.Parked) {
		return m, nil
	}
	m.Pane, m.ParkedRow, m.Detail = PaneParked, row, true
	return m, nil
}

// Verb is one labelled action in the footer: the mouse target for a key.
type Verb struct {
	Key   string
	Label string
}

// Verbs is what the footer offers for the current selection — human verbs
// only. Claim, touch, submit and release are an agent's, and the operator's
// board does not offer them.
func (m Model) Verbs() []Verb {
	return append(m.verbs(), closeVerb)
}

// closeVerb is last in every footer. A popup has no close key of its own, so
// if the footer does not say how to leave, nothing does — and since
// footerClick maps a label back to its key, this is also the mouse way out.
var closeVerb = Verb{"esc", "close"}

func (m Model) verbs() []Verb {
	if m.View == ViewBoard {
		if t := m.SelectedTask(); t != nil && t.Status == "review" {
			return []Verb{{"a", "approve"}, {"x", "reject"}, {"enter", "detail"}, findVerb, {"tab", "notes"}}
		}
		return []Verb{{"enter", "detail"}, findVerb, {"tab", "notes"}}
	}
	if m.Pane == PaneParked {
		if m.SelectedParked() != nil {
			return []Verb{{"y", "resolve"}, {"n", "reject"}, {"tab", "board"}}
		}
		return []Verb{{"tab", "board"}}
	}
	if m.SelectedNote() != nil {
		return []Verb{{"v", "verdict"}, {"p", "promote"}, {"K", "keep"}, {"d", "drop"}, {"e", "edit"}, addVerb, findVerb, {"tab", "board"}}
	}
	// With nothing selected there is still an idea to file and a board to
	// search: neither needs a row under the cursor.
	return []Verb{addVerb, findVerb, {"tab", "board"}}
}

var (
	addVerb  = Verb{"a", "add"}
	findVerb = Verb{"/", "find"}
)

// footerClick turns a click on the footer into the keystroke it is labelled
// with, so no verb is keyboard-only. It walks the verbs the footer DREW, not
// every verb the state has: on a narrow pane those are not the same list.
func footerClick(m Model, x int) (Model, *Call) {
	at := 0
	for _, v := range m.footerVerbs() {
		label := footerLabel(v)
		if x >= at && x < at+len(label) {
			return Update(m, KeyMsg{Key: v.Key})
		}
		at += len(label)
	}
	return m, nil
}

func footerLabel(v Verb) string { return " [" + v.Key + "] " + v.Label + " " }

// Render draws the whole view as plain text. It is pure, which is what makes
// the layout testable; run.go hands the string to bubbletea.
//
// It builds LINES and joins them, rather than writing newlines into a buffer,
// because the number that matters is the one bubbletea counts: it splits the
// view on "\n" and, when there are more pieces than the terminal has rows,
// keeps only the last Height of them (standard_renderer.go:186 — it drops from
// the TOP because it cannot move the cursor into scrollback). A document
// written as N newlines is N+1 pieces, so the old arithmetic was one over
// before anything was added to it, and the status line — written after the
// footer, outside the budget entirely — was one more. What went was the
// header: the only line that says which project this board is scoped to.
//
// So every fixed row is reserved here, including the status row when there is
// no status, so that filling it cannot move anything.
func Render(m Model, now int64) string {
	rows := m.Height
	if rows < minRows {
		rows = minRows
	}
	// The fixed bottom: a blank line, the footer, and the status row.
	const bottom = 3
	free := rows - 1 - bottom
	if free < 1 {
		free = 1
	}

	// Chrome inside the budget is served before the body: a prompt the
	// operator is typing into, and the detail panel they opened, are what they
	// are looking at. The body is the part that can be scrolled back to.
	var prompt []string
	if m.Prompt != nil {
		prompt = clampLines([]string{"", promptLine(*m.Prompt, m.Width)}, free-1)
	}
	var detail []string
	if m.Detail {
		if body := Detail(m, now); body != "" {
			detail = clampLines(append([]string{""}, wrapTo(body, m.Width)...), free-len(prompt)-1)
		}
	}

	lines := []string{clampWidth(header(m), m.Width)}
	lines = append(lines, fitLines(bodyLines(m, now), free-len(prompt)-len(detail))...)
	lines = append(lines, prompt...)
	lines = append(lines, detail...)
	lines = append(lines, "")
	lines = append(lines, footerLine(m))
	lines = append(lines, clampWidth(statusLine(m), m.Width))
	return strings.Join(lines, "\n")
}

// promptCursor marks where the next rune goes.
const promptCursor = '_'

// promptLine draws the prompt as one row, showing the part of the value the
// cursor is in. A value longer than the pane used to be drawn from the start
// and clipped, so everything past the edge — which is exactly where the typing
// happens — was invisible: the operator typed blind. The window follows the
// cursor instead, keeping it roughly centred so there is context on both
// sides, and it stays ONE row whatever the value holds, because a prompt that
// grew to two rows would push the header off the screen again.
func promptLine(p Prompt, width int) string {
	head := p.Label + ": "
	r := []rune(p.Value)
	cursor := p.Cursor
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(r) {
		cursor = len(r)
	}
	if width <= 0 {
		return head + string(r[:cursor]) + string(promptCursor) + string(r[cursor:])
	}
	// One column of the room is the cursor's own.
	avail := width - cells(head) - 1
	if avail < 1 {
		// A pane too narrow for the label keeps the value and loses the label:
		// what is being typed matters more than what it is called.
		head = ""
		if avail = width - 1; avail < 1 {
			avail = 1
		}
	}
	// Grow the window outwards from the cursor a character at a time, taking
	// each side in turn, until the CELLS run out. Sizing the window in
	// characters instead would let a value of double-width characters draw
	// twice the pane's width, and the renderer would cut it — at the end,
	// which is where the typing is.
	left, right, used := cursor, cursor, 0
	for {
		grew := false
		if left > 0 {
			if w := cells(string(r[left-1])); used+w <= avail {
				left, used, grew = left-1, used+w, true
			}
		}
		if right < len(r) {
			if w := cells(string(r[right])); used+w <= avail {
				right, used, grew = right+1, used+w, true
			}
		}
		if !grew {
			break
		}
	}
	window := string(r[left:cursor]) + string(promptCursor) + string(r[cursor:right])
	return clampWidth(head+window, width)
}

// minRows is the smallest screen the layout still draws something on: the
// header, one row of board, and the three fixed rows below it.
const minRows = 5

func header(m Model) string {
	return tab(boardTab, m.View == ViewBoard) + tab(notesTab, m.View == ViewNotes) + "  " + m.Project
}

func statusLine(m Model) string {
	if m.Err != "" {
		return m.Err
	}
	return m.Status
}

// footerLine draws the verbs, dropping from the right until they fit the pane.
// A footer wider than the pane wraps, and a wrapped footer costs a row the
// budget did not know about — the very thing that hid the header. Whatever
// goes, the close verb stays: it is the only way out of a popup (§11.6).
func footerLine(m Model) string {
	var b strings.Builder
	for _, v := range m.footerVerbs() {
		b.WriteString(footerLabel(v))
	}
	return b.String()
}

// footerVerbs is what the footer actually draws, and therefore what
// footerClick hit-tests: the two must be the same list or a click lands on the
// wrong verb.
func (m Model) footerVerbs() []Verb {
	all := m.Verbs()
	width := 0
	for _, v := range all {
		width += len(footerLabel(v))
	}
	if m.Width <= 0 || width <= m.Width {
		return all
	}
	// The last verb is the way out and is kept; the ones before it go, from
	// the right, until what is left fits.
	out, last := all[:len(all)-1], all[len(all)-1]
	for len(out) > 0 {
		width = len(footerLabel(last))
		for _, v := range out {
			width += len(footerLabel(v))
		}
		if width <= m.Width {
			break
		}
		out = out[:len(out)-1]
	}
	return append(out, last)
}

// bodyLines is the view's own rows, its heading first. The heading survives
// clamping because it carries the counts: "todo (40)" over twelve drawn cards
// says what is not on the screen, where twelve cards alone would not.
func bodyLines(m Model, now int64) []string {
	if m.View == ViewBoard {
		return boardLines(m, now)
	}
	return notesLines(m)
}

// fitLines clamps a body to n rows and pads it back out to n, so the fixed
// rows below it stay where click() expects them whatever the body holds.
func fitLines(lines []string, n int) []string {
	if n < 1 {
		n = 1
	}
	out := make([]string, 0, n)
	if len(lines) > 0 {
		out = append(out, lines[0])
	}
	for i := 1; i < len(lines) && len(out) < n; i++ {
		out = append(out, lines[i])
	}
	for len(out) < n {
		out = append(out, "")
	}
	return out[:n]
}

func clampLines(lines []string, n int) []string {
	if n < 0 {
		n = 0
	}
	if len(lines) > n {
		return lines[:n]
	}
	return lines
}

// clampWidth keeps a line inside the pane, measured in cells. bubbletea cuts
// anything wider at the same boundary, so a line this function lets through is
// a line that reaches the terminal whole.
func clampWidth(s string, w int) string {
	if w <= 0 {
		return s
	}
	if cells(s) > w {
		return truncateCells(s, w)
	}
	return s
}

// wrapTo breaks text into lines that fit the pane, so a long note body is
// readable down the panel instead of running off the side of it.
func wrapTo(s string, w int) []string {
	if w <= 0 {
		return strings.Split(s, "\n")
	}
	out := []string{}
	for _, line := range strings.Split(s, "\n") {
		if line == "" {
			out = append(out, "")
			continue
		}
		for cells(line) > w {
			head := truncateCells(line, w)
			if head == "" {
				// A single character wider than the whole pane. Emit it
				// anyway: one overflowing row the renderer trims is better
				// than a loop that never ends or text that never appears.
				head = string([]rune(line)[0])
			}
			out = append(out, head)
			line = line[len(head):]
		}
		out = append(out, line)
	}
	return out
}

// tab draws one view tab. The active one is bracketed, and padded back to the
// inactive width: TabAt hit-tests by fixed cell ranges, so a tab that shrinks
// when selected would leave clicks landing on the wrong view.
func tab(label string, on bool) string {
	if on {
		return fmt.Sprintf("%-*s", len(label), "["+strings.TrimSpace(label)+"]")
	}
	return label
}

// boardLines is the state columns: the headings, then a row per card depth.
func boardLines(m Model, now int64) []string {
	width := m.Width / len(Columns)
	head := ""
	deepest := 0
	for i, c := range Columns {
		head += pad(fmt.Sprintf("%s (%d)", c, len(m.Column(i))), width)
		if n := len(m.Column(i)); n > deepest {
			deepest = n
		}
	}
	out := []string{head}
	if deepest == 0 {
		return append(out, emptyState(m.Project, m.BoardElsewhere, "task")...)
	}
	for row := 0; row < deepest; row++ {
		line := ""
		for i := range Columns {
			col := m.Column(i)
			if row >= len(col) {
				line += pad("", width)
				continue
			}
			t := col[row]
			mark := " "
			if i == m.Col && row == m.Row[i] {
				mark = ">"
			}
			line += pad(fmt.Sprintf("%s#%d %s%s", mark, t.Seq, t.Title, lease(t, now)), width)
		}
		out = append(out, line)
	}
	return out
}

// emptyState is what a view with nothing on it says (§4.2). A popup takes its
// project from the focused pane, so an empty board has two very different
// causes — a project with no work, or a pane the operator did not mean — and
// nothing on the screen told them apart. Twice that was read as data loss.
//
// The scope is named, the cause of the scope is named, and where the work
// actually is is named when it is somewhere else. The way over is not a key:
// the board follows the pane, so the gesture is to focus the pane you meant.
func emptyState(project string, elsewhere int, what string) []string {
	out := []string{"", fmt.Sprintf("  Nothing here. This board is %s, which is the focused pane's project.", project)}
	if elsewhere > 0 {
		out = append(out, fmt.Sprintf("  %d %s(s) in other projects — focus a pane in the one you meant and reopen.",
			elsewhere, what))
	}
	return out
}

// lease is the claim made visible on the card: who holds it and how much of
// the lease is left, because a claim nobody can see is a claim nobody renews
// (§16.3).
func lease(t *tasks.Task, now int64) string {
	if t.ClaimedBy == "" {
		return ""
	}
	who := string(t.ClaimedBy)
	if t.ClaimedByName != "" {
		who = t.ClaimedByName
	}
	if t.LeaseUntil == 0 {
		return " · " + who
	}
	return " · " + who + " " + humanLeft(t.LeaseUntil-now)
}

// notesLines is the notes list beside the parked gate actions.
func notesLines(m Model) []string {
	half := m.Width / 2
	out := []string{pad("notes", half) + pad("parked", half)}
	rows := len(m.Notes)
	if len(m.Parked) > rows {
		rows = len(m.Parked)
	}
	if rows == 0 {
		return append(out, emptyState(m.Project, m.NotesElsewhere, "note")...)
	}
	for row := 0; row < rows; row++ {
		left := ""
		if row < len(m.Notes) {
			n := m.Notes[row]
			mark := " "
			if m.Pane == PaneNotes && row == m.NoteRow {
				mark = ">"
			}
			left = fmt.Sprintf("%s#%d [%s] %s", mark, n.Seq, n.Status, n.Body)
		}
		right := ""
		if row < len(m.Parked) {
			p := m.Parked[row]
			mark := " "
			if m.Pane == PaneParked && row == m.ParkedRow {
				mark = ">"
			}
			right = fmt.Sprintf("%s%s by %s", mark, p.Verb, p.Subject)
		}
		out = append(out, pad(left, half)+pad(right, half))
	}
	return out
}

// pad renders s in a column exactly w cells wide, keeping at least one cell of
// gap before the next column. Measured in cells, not characters: padding a
// double-width title by character count pushes the column beside it to the
// right by one cell per wide character, until the renderer cuts it off.
func pad(s string, w int) string {
	if w <= 1 {
		return s
	}
	if cells(s) > w-1 {
		s = truncateCells(s, w-1)
	}
	return s + strings.Repeat(" ", w-cells(s))
}

func humanLeft(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	if d < 0 {
		return "lapsed"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}
