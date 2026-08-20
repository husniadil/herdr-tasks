// Package tui is the human door: a board of tasks in their state columns and
// a notes board with the parked gate actions beside it (§11.6). Everything in
// this file is pure — a model, messages, and the daemon call each keystroke or
// click asks for. It never opens SQLite and never dials a socket: the daemon
// is the only writer (§2.2), and the TUI is a client of it like every other
// door. The bubbletea wiring lives in run.go; this is what the tests drive.
package tui

import (
	"fmt"
	"strings"

	"github.com/husniadil/herdr-tasks/internal/store"
	"github.com/husniadil/herdr-tasks/internal/tasks"
)

// View names the two views `ht tui [<view>]` opens (§2.1).
type View string

const (
	// ViewBoard is the resolved project's tasks in their state columns.
	ViewBoard View = "board"
	// ViewNotes is the notes board, inbox through verdicts, with the parked
	// gate actions beside it (§9.3).
	ViewNotes View = "notes"
)

// ParseView reads the optional `ht tui <view>` argument.
func ParseView(s string) (View, error) {
	switch s {
	case "", string(ViewBoard):
		return ViewBoard, nil
	case string(ViewNotes):
		return ViewNotes, nil
	}
	return "", fmt.Errorf("no such view %q: board or notes", s)
}

// Columns are the board's state columns, in lifecycle order. Cancelled and
// archived tasks are not on the board; `ht task list --status cancelled` is
// where they are read.
var Columns = []tasks.Status{tasks.StatusTodo, tasks.StatusDoing, tasks.StatusReview, tasks.StatusDone}

// Pane is which list has the keyboard in the notes view.
type Pane int

const (
	// PaneNotes is the notes list.
	PaneNotes Pane = iota
	// PaneParked is the parked gate actions.
	PaneParked
)

// Call is one daemon verb the TUI wants run: the same verb name, the same
// arguments, the same result shape as the CLI and MCP doors (§6.1). Update
// returns it; the runtime sends it over the socket.
type Call struct {
	Verb string
	Args map[string]any
}

// Prompt is an open one-line question — reject feedback, a verdict reason —
// held until the operator confirms or cancels it.
type Prompt struct {
	// Label is what the operator is being asked for.
	Label string
	// Value is what they have typed so far.
	Value string
	// Required says an empty answer cannot be confirmed: `task reject` has no
	// meaning without feedback.
	Required bool
	// call is the verb the answer completes, and Field the argument the typed
	// text fills in.
	call  Call
	field string
}

// Model is the whole state of the TUI. Data comes from the daemon through
// Data; every field below it is what the operator is looking at.
type Model struct {
	View    View
	Project string
	Width   int
	Height  int

	Tasks  []*tasks.Task
	Notes  []*tasks.Note
	Parked []store.Parked

	// Col is the selected board column and Row the cursor within each one,
	// kept per column so moving sideways does not lose your place.
	Col int
	Row [4]int

	Pane      Pane
	NoteRow   int
	ParkedRow int

	// Detail says the detail panel is open on the selection.
	Detail bool
	Prompt *Prompt
	Status string
	Err    string
	Quit   bool
}

// New is the model a fresh `ht tui` starts in.
func New(view View, project string) Model {
	return Model{View: view, Project: project, Width: 80, Height: 24}
}

// Msg is anything that moves the model.
type Msg interface{ isMsg() }

// KeyMsg is one keypress, named the way bubbletea names them ("up", "enter",
// "ctrl+c") or the literal rune for a printable key.
type KeyMsg struct{ Key string }

// MouseMsg is one click at a cell. The TUI is mouse-first (§11.6): every verb
// the keyboard reaches is reachable by clicking, and the keyboard is the
// fallback rather than the other way round.
type MouseMsg struct{ X, Y int }

// SizeMsg is a resize.
type SizeMsg struct{ Width, Height int }

// DataMsg is a fresh read of the daemon's answer to the list verbs.
type DataMsg struct {
	Tasks  []*tasks.Task
	Notes  []*tasks.Note
	Parked []store.Parked
}

// ErrMsg is a failed call, carried with its §6.3 code so the footer can say
// which one it was.
type ErrMsg struct {
	Code    string
	Message string
}

// DoneMsg is a call that succeeded, with the line to show for it.
type DoneMsg struct{ Status string }

func (KeyMsg) isMsg()   {}
func (MouseMsg) isMsg() {}
func (SizeMsg) isMsg()  {}
func (DataMsg) isMsg()  {}
func (ErrMsg) isMsg()   {}
func (DoneMsg) isMsg()  {}

// Update is the whole of the TUI's logic: a model and a message in, the next
// model and at most one daemon call out. Pure, so the tests below drive it
// with no terminal, no socket and no Herdr (§12.1 layer 1).
func Update(m Model, msg Msg) (Model, *Call) {
	switch v := msg.(type) {
	case SizeMsg:
		m.Width, m.Height = v.Width, v.Height
		return m, nil
	case DataMsg:
		m.Tasks, m.Notes, m.Parked = v.Tasks, v.Notes, v.Parked
		return m.clampCursors(), nil
	case ErrMsg:
		m.Err = v.Code + ": " + v.Message
		m.Status = ""
		return m, nil
	case DoneMsg:
		m.Status, m.Err = v.Status, ""
		return m, nil
	case MouseMsg:
		return click(m, v)
	case KeyMsg:
		if m.Prompt != nil {
			return promptKey(m, v.Key)
		}
		return key(m, v.Key)
	}
	return m, nil
}

func key(m Model, k string) (Model, *Call) {
	switch k {
	case "q", "ctrl+c":
		m.Quit = true
		return m, nil
	case "tab", "1", "2":
		if k == "1" {
			m.View = ViewBoard
		} else if k == "2" {
			m.View = ViewNotes
		} else if m.View == ViewBoard {
			m.View = ViewNotes
		} else {
			m.View = ViewBoard
		}
		m.Detail = false
		return m.clampCursors(), nil
	case "r", "R":
		// A refresh is a re-read, not a mutation: the board is a live view of
		// the daemon's answer, never a cache the operator edits.
		if m.View == ViewBoard {
			return m, &Call{Verb: "task.list", Args: map[string]any{}}
		}
		return m, &Call{Verb: "note.list", Args: map[string]any{}}
	case "esc":
		m.Detail = false
		return m, nil
	case "enter":
		m.Detail = !m.Detail
		return m, nil
	}
	if m.View == ViewBoard {
		return boardKey(m, k)
	}
	return notesKey(m, k)
}

func boardKey(m Model, k string) (Model, *Call) {
	switch k {
	case "left", "h":
		if m.Col > 0 {
			m.Col--
		}
		return m, nil
	case "right", "l":
		if m.Col < len(Columns)-1 {
			m.Col++
		}
		return m, nil
	case "up", "k":
		if m.Row[m.Col] > 0 {
			m.Row[m.Col]--
		}
		return m, nil
	case "down", "j":
		if m.Row[m.Col] < len(m.Column(m.Col))-1 {
			m.Row[m.Col]++
		}
		return m, nil
	case "a":
		return approve(m)
	case "x":
		return reject(m)
	}
	return m, nil
}

// approve is the operator accepting submitted work. The TUI offers human
// verbs only: it never claims, submits or touches a task, because those belong
// to the agent that is doing the work.
func approve(m Model) (Model, *Call) {
	t := m.SelectedTask()
	if t == nil {
		return m, nil
	}
	if t.Status != tasks.StatusReview {
		m.Status = fmt.Sprintf("#%d is %s: only work in review can be approved", t.Seq, t.Status)
		return m, nil
	}
	m.Status = fmt.Sprintf("approving #%d…", t.Seq)
	return m, &Call{Verb: "task.approve", Args: map[string]any{"id": t.ID}}
}

// reject opens the feedback prompt. §6.3's vocabulary has no code for "sent
// back with nothing to change": rejecting without feedback is not offered.
func reject(m Model) (Model, *Call) {
	t := m.SelectedTask()
	if t == nil {
		return m, nil
	}
	if t.Status != tasks.StatusReview {
		m.Status = fmt.Sprintf("#%d is %s: only work in review can be rejected", t.Seq, t.Status)
		return m, nil
	}
	m.Prompt = &Prompt{
		Label:    fmt.Sprintf("What must change in #%d?", t.Seq),
		Required: true,
		call:     Call{Verb: "task.reject", Args: map[string]any{"id": t.ID}},
		field:    "feedback",
	}
	return m, nil
}

func notesKey(m Model, k string) (Model, *Call) {
	switch k {
	case "left", "h":
		m.Pane = PaneNotes
		return m, nil
	case "right", "l":
		m.Pane = PaneParked
		return m, nil
	case "up", "k":
		if m.Pane == PaneNotes && m.NoteRow > 0 {
			m.NoteRow--
		} else if m.Pane == PaneParked && m.ParkedRow > 0 {
			m.ParkedRow--
		}
		return m, nil
	case "down", "j":
		if m.Pane == PaneNotes && m.NoteRow < len(m.Notes)-1 {
			m.NoteRow++
		} else if m.Pane == PaneParked && m.ParkedRow < len(m.Parked)-1 {
			m.ParkedRow++
		}
		return m, nil
	}
	if m.Pane == PaneParked {
		return parkedKey(m, k)
	}
	switch k {
	case "v":
		n := m.SelectedNote()
		if n == nil {
			return m, nil
		}
		m.Prompt = &Prompt{
			Label: fmt.Sprintf("Verdict on note #%d (task, keep or drop)", n.Seq),
			call:  Call{Verb: "note.verdict", Args: map[string]any{"id": n.ID}},
			field: "verdict", Required: true,
		}
		return m, nil
	case "p":
		n := m.SelectedNote()
		if n == nil {
			return m, nil
		}
		m.Status = fmt.Sprintf("promoting note #%d…", n.Seq)
		return m, &Call{Verb: "note.promote", Args: map[string]any{"id": n.ID}}
	case "d":
		n := m.SelectedNote()
		if n == nil {
			return m, nil
		}
		m.Prompt = &Prompt{
			Label: fmt.Sprintf("Why is note #%d dropped?", n.Seq),
			call:  Call{Verb: "note.drop", Args: map[string]any{"id": n.ID}},
			field: "reason",
		}
		return m, nil
	}
	return m, nil
}

// parkedKey is the operator side of §9.3: only a human resolves or rejects a
// parked action, and resolving re-runs the verb under the original subject.
func parkedKey(m Model, k string) (Model, *Call) {
	p := m.SelectedParked()
	if p == nil {
		return m, nil
	}
	switch k {
	case "y":
		m.Status = fmt.Sprintf("running %s for %s…", p.Verb, p.Subject)
		return m, &Call{Verb: "parked.resolve", Args: map[string]any{"id": p.ID}}
	case "n":
		m.Status = fmt.Sprintf("rejecting %s…", p.Verb)
		return m, &Call{Verb: "parked.resolve", Args: map[string]any{"id": p.ID, "reject": true}}
	}
	return m, nil
}

func promptKey(m Model, k string) (Model, *Call) {
	p := m.Prompt
	switch k {
	case "esc":
		m.Prompt = nil
		m.Status = "cancelled"
		return m, nil
	case "enter":
		if p.Required && strings.TrimSpace(p.Value) == "" {
			m.Err = "USAGE: " + p.Label
			return m, nil
		}
		call := p.call
		args := map[string]any{}
		for k, v := range call.Args {
			args[k] = v
		}
		if s := strings.TrimSpace(p.Value); s != "" {
			args[p.field] = s
		}
		call.Args = args
		m.Prompt, m.Err = nil, ""
		m.Status = call.Verb + "…"
		return m, &call
	case "backspace":
		if r := []rune(p.Value); len(r) > 0 {
			p.Value = string(r[:len(r)-1])
		}
		return m, nil
	}
	if len([]rune(k)) == 1 {
		p.Value += k
	} else if k == "space" {
		p.Value += " "
	}
	return m, nil
}

// Column returns the tasks in the i-th state column, in the order the daemon
// listed them (priority, then seq).
func (m Model) Column(i int) []*tasks.Task {
	if i < 0 || i >= len(Columns) {
		return nil
	}
	out := []*tasks.Task{}
	for _, t := range m.Tasks {
		if t.Status == Columns[i] {
			out = append(out, t)
		}
	}
	return out
}

// SelectedTask is the task under the board cursor, or nil in an empty column.
func (m Model) SelectedTask() *tasks.Task {
	col := m.Column(m.Col)
	if m.Row[m.Col] < 0 || m.Row[m.Col] >= len(col) {
		return nil
	}
	return col[m.Row[m.Col]]
}

// SelectedNote is the note under the notes cursor.
func (m Model) SelectedNote() *tasks.Note {
	if m.NoteRow < 0 || m.NoteRow >= len(m.Notes) {
		return nil
	}
	return m.Notes[m.NoteRow]
}

// SelectedParked is the parked action under the parked cursor.
func (m Model) SelectedParked() *store.Parked {
	if m.ParkedRow < 0 || m.ParkedRow >= len(m.Parked) {
		return nil
	}
	return &m.Parked[m.ParkedRow]
}

func (m Model) clampCursors() Model {
	// A board that opens with the cursor parked on an empty column asks the
	// operator to hunt for the work before they can act on it.
	if len(m.Column(m.Col)) == 0 {
		for i := range Columns {
			if len(m.Column(i)) > 0 {
				m.Col = i
				break
			}
		}
	}
	for i := range Columns {
		if n := len(m.Column(i)); m.Row[i] >= n {
			m.Row[i] = max(0, n-1)
		}
	}
	if m.NoteRow >= len(m.Notes) {
		m.NoteRow = max(0, len(m.Notes)-1)
	}
	if m.ParkedRow >= len(m.Parked) {
		m.ParkedRow = max(0, len(m.Parked)-1)
	}
	return m
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
