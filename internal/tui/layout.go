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
	if m.View == ViewBoard {
		if t := m.SelectedTask(); t != nil && t.Status == "review" {
			return []Verb{{"a", "approve"}, {"x", "reject"}, {"enter", "detail"}, {"tab", "notes"}}
		}
		return []Verb{{"enter", "detail"}, {"tab", "notes"}}
	}
	if m.Pane == PaneParked {
		if m.SelectedParked() != nil {
			return []Verb{{"y", "resolve"}, {"n", "reject"}, {"tab", "board"}}
		}
		return []Verb{{"tab", "board"}}
	}
	if m.SelectedNote() != nil {
		return []Verb{{"v", "verdict"}, {"p", "promote"}, {"d", "drop"}, {"tab", "board"}}
	}
	return []Verb{{"tab", "board"}}
}

// footerClick turns a click on the footer into the keystroke it is labelled
// with, so no verb is keyboard-only.
func footerClick(m Model, x int) (Model, *Call) {
	at := 0
	for _, v := range m.Verbs() {
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
func Render(m Model, now int64) string {
	var b strings.Builder
	b.WriteString(tab(boardTab, m.View == ViewBoard))
	b.WriteString(tab(notesTab, m.View == ViewNotes))
	b.WriteString("  " + m.Project + "\n")
	if m.View == ViewBoard {
		renderBoard(&b, m, now)
	} else {
		renderNotes(&b, m)
	}
	if m.Prompt != nil {
		fmt.Fprintf(&b, "\n%s: %s_\n", m.Prompt.Label, m.Prompt.Value)
	}
	if m.Detail {
		b.WriteString("\n" + Detail(m, now))
	}
	b.WriteString("\n")
	for _, v := range m.Verbs() {
		b.WriteString(footerLabel(v))
	}
	b.WriteString("\n")
	if m.Err != "" {
		b.WriteString(m.Err + "\n")
	} else if m.Status != "" {
		b.WriteString(m.Status + "\n")
	}
	return b.String()
}

func tab(label string, on bool) string {
	if on {
		return "[" + strings.TrimSpace(label) + "]"
	}
	return label
}

func renderBoard(b *strings.Builder, m Model, now int64) {
	width := m.Width / len(Columns)
	for i, c := range Columns {
		head := fmt.Sprintf("%s (%d)", c, len(m.Column(i)))
		b.WriteString(pad(head, width))
	}
	b.WriteString("\n")
	deepest := 0
	for i := range Columns {
		if n := len(m.Column(i)); n > deepest {
			deepest = n
		}
	}
	for row := 0; row < deepest; row++ {
		for i := range Columns {
			col := m.Column(i)
			if row >= len(col) {
				b.WriteString(pad("", width))
				continue
			}
			t := col[row]
			mark := " "
			if i == m.Col && row == m.Row[i] {
				mark = ">"
			}
			b.WriteString(pad(fmt.Sprintf("%s#%d %s%s", mark, t.Seq, t.Title, lease(t, now)), width))
		}
		b.WriteString("\n")
	}
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

func renderNotes(b *strings.Builder, m Model) {
	half := m.Width / 2
	b.WriteString(pad("notes", half) + pad("parked", half) + "\n")
	rows := len(m.Notes)
	if len(m.Parked) > rows {
		rows = len(m.Parked)
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
		b.WriteString(pad(left, half) + pad(right, half) + "\n")
	}
}

func pad(s string, w int) string {
	r := []rune(s)
	if w <= 1 {
		return s
	}
	if len(r) > w-1 {
		return string(r[:w-1]) + " "
	}
	return s + strings.Repeat(" ", w-len(r))
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
