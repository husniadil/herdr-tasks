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
	// Only what was DRAWN is clickable, and what was drawn starts at the
	// frame's offset. The column can be far deeper than the screen: bounding
	// by the column's length instead selected a card nobody could see, and
	// ignoring the offset selected the card that WOULD have been on that row
	// had the operator never scrolled.
	f := frameOf(m, at.At)
	drawn := RowAt(at.Y)
	if drawn < 0 || drawn >= f.cards {
		return m, nil
	}
	row := drawn + f.off
	if m.View == ViewBoard {
		col := ColumnAt(m.Width, at.X)
		if row >= len(m.Column(col)) {
			return m, nil
		}
		m.Col, m.Row[col], m.Detail = col, row, true
		return m, nil
	}
	if at.X < m.Width/2 {
		if row >= len(m.Notes) {
			return m, nil
		}
		m.Pane, m.NoteRow, m.Detail = PaneNotes, row, true
		return m, nil
	}
	if row >= len(m.Parked) {
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
	f := frameOf(m, now)
	lines := []string{clampWidth(header(m), m.Width)}
	lines = append(lines, f.body...)
	lines = append(lines, f.prompt...)
	lines = append(lines, f.detail...)
	lines = append(lines, "")
	lines = append(lines, footerLine(m))
	lines = append(lines, clampWidth(statusLine(m), m.Width))
	return strings.Join(lines, "\n")
}

// frame is one screenful, divided. It exists so that the arithmetic deciding
// how many rows the body gets is done ONCE and read by everyone who needs it:
// Render draws it, and click() hit-tests against it. Two computations of the
// same number is two contracts, and the difference between them was a click on
// a blank row opening a card that was never on screen — with the footer's
// approve and reject then aimed at it.
type frame struct {
	body   []string
	prompt []string
	detail []string
	// cards is how many rows BELOW the body's heading carry data. Rows past
	// it are the padding fitLines added, and nothing is drawn on them.
	cards int
	// off is the list offset the body was drawn at: the first card row on the
	// screen is this far down the view's rows. click() adds it to the row it
	// hit-tests, so a click selects the card the operator is looking at.
	off int
	// win is how many rows the body has for cards whether or not there are
	// that many, and listMax the largest offset that still fills it. The
	// wheel stops there: past it the panel would scroll into blank rows.
	win     int
	listMax int
	// detailOff and detailMax are the same two numbers for the detail panel.
	detailOff int
	detailMax int
}

func frameOf(m Model, now int64) frame {
	free := freeRows(m)
	promptCap, detailCap := panelRows(m)

	// Chrome inside the budget is served before the body: a prompt the
	// operator is typing into, and the detail panel they opened, are what they
	// are looking at. Both are BOUNDED, and what does not fit is scrolled to.
	var f frame
	if m.Prompt != nil {
		f.prompt = clampLines([]string{"", promptLine(*m.Prompt, m.Width)}, promptCap)
	}
	if detailCap > 0 {
		// One of the panel's rows is the blank line that separates it from
		// the body, so the text gets the rest.
		shown := detailCap - 1
		full := wrapTo(Detail(m, now), m.Width)
		f.detailMax = max(0, len(full)-shown)
		f.detailOff = clampTo(m.DetailOffset, f.detailMax)
		if shown > 0 {
			f.detail = append([]string{""}, windowOf(full, f.detailOff, shown)...)
		}
	}

	budget := free - len(f.prompt) - len(f.detail)
	full := bodyLines(m, now)
	// The heading is row 0 of the body and always survives, so the rows that
	// can carry a card are what is left of the budget.
	if f.win = budget - 1; f.win < 0 {
		f.win = 0
	}
	f.listMax = max(0, len(full)-1-f.win)
	f.off = clampTo(m.listOffset(), f.listMax)
	f.body = fitLines(full, budget, f.off)
	if f.cards = f.win; f.cards > len(full)-1-f.off {
		f.cards = len(full) - 1 - f.off
	}
	if f.cards < 0 {
		f.cards = 0
	}
	return f
}

// freeRows is what the header and the fixed bottom leave for everything else.
func freeRows(m Model) int {
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
	return free
}

// panelRows is the most the prompt and the detail panel may take. The detail
// is capped at HALF of what is left after the prompt: it is a bottom panel,
// not a cover, so however long the text is the list keeps the other half.
//
// Nothing here reads the clock, which is what lets follow() do the same
// arithmetic without one. That is sound in one direction only: a detail
// SHORTER than its cap leaves the body more rows than this says, never fewer,
// so a cursor follow() keeps inside this window is inside the drawn one too.
func panelRows(m Model) (prompt, detail int) {
	free := freeRows(m)
	if m.Prompt != nil {
		if prompt = 2; prompt > free-1 {
			prompt = free - 1
		}
		if prompt < 0 {
			prompt = 0
		}
	}
	if m.Detail && m.hasDetail() {
		detail = (free - prompt) / 2
	}
	return prompt, detail
}

// region names which part of the screen a cell is in, so the wheel moves the
// panel under the pointer and only that one.
type region int

const (
	regionNone region = iota
	regionBody
	regionDetail
)

func (f frame) regionAt(y int) region {
	if y >= 1 && y < 1+len(f.body) {
		return regionBody
	}
	start := 1 + len(f.body) + len(f.prompt)
	if y >= start && y < start+len(f.detail) {
		return regionDetail
	}
	return regionNone
}

// wheelLines is one notch of the wheel, bounded by the window it moves: a
// notch longer than the panel would step over rows nobody ever saw.
const wheelLines = 3

func wheelStep(win int) int {
	if win < 1 {
		return 1
	}
	if win < wheelLines {
		return win
	}
	return wheelLines
}

// scroll moves the panel under the pointer by a notch, and only that panel.
func scroll(m Model, w WheelMsg) Model {
	f := frameOf(m, w.At)
	dir := 1
	if w.Up {
		dir = -1
	}
	switch f.regionAt(w.Y) {
	case regionDetail:
		m.DetailOffset = clampTo(f.detailOff+dir*wheelStep(len(f.detail)-1), f.detailMax)
	case regionBody:
		m = m.setListOffset(clampTo(f.off+dir*wheelStep(f.win), f.listMax))
	}
	return m
}

// windowOf is n lines of a panel from off, and nothing past its end.
func windowOf(lines []string, off, n int) []string {
	if off > len(lines) {
		off = len(lines)
	}
	out := lines[off:]
	if len(out) > n {
		out = out[:n]
	}
	return out
}

func clampTo(v, high int) int {
	if v > high {
		v = high
	}
	if v < 0 {
		v = 0
	}
	return v
}

// promptCursor marks where the next rune goes.
const promptCursor = '_'

// promptLine draws the prompt as one row, showing the part of the value the
// cursor is in. The window follows the cursor rather than starting at the
// beginning of the value: what is past the pane's edge is exactly where the
// typing happens, so a value drawn from the start and clipped leaves the
// operator typing blind. It is kept roughly centred so there is context on
// both sides, and it stays ONE row whatever the value holds, because a prompt that
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
// rows below it stay where click() expects them whatever the body holds. The
// heading is row 0 and never scrolls: it carries the counts, which is what
// says how much of the column is off the screen. off is where the rows under
// it start.
func fitLines(lines []string, n, off int) []string {
	if n < 1 {
		n = 1
	}
	out := make([]string, 0, n)
	if len(lines) > 0 {
		out = append(out, lines[0])
	}
	for i := 1 + off; i < len(lines) && len(out) < n; i++ {
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

// lease is the claim made visible on the card: who holds it, and the time that
// matters for what they hold — how much of the lease is left while the work is
// in hand, how long it has waited once it is submitted. A claim nobody can see
// is a claim nobody renews, and a wait nobody can see is a wait nobody ends
// (§16.3).
func lease(t *tasks.Task, now int64) string {
	if t.ClaimedBy == "" {
		return ""
	}
	who := string(t.ClaimedBy)
	if t.ClaimedByName != "" {
		who = t.ClaimedByName
	}
	if t.Status == tasks.StatusReview && t.SubmittedAt != 0 {
		// A review card prints a time too, and it is a different one: the
		// lease ended at the submission, and what the operator is deciding
		// about is how long it has waited since. Derived at render, so it is
		// right whenever it is read.
		return " · " + who + " submitted " + waited(t.SubmittedAt, now)
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

// waited says how long ago something happened, in the largest unit that still
// has a whole number in it. A stamp in the future is a clock disagreeing with
// itself, and the smallest true thing to say about it is that no time has
// passed.
func waited(at, now int64) string {
	d := time.Duration(now-at) * time.Millisecond
	switch {
	case d < time.Minute:
		if d < 0 {
			d = 0
		}
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours())/24)
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
