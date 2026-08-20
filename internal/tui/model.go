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
	// Cursor is where the next rune goes, as an index into Value's RUNES —
	// not its bytes, so a prompt behaves the same in every language. It is
	// what makes this an editor rather than an append-only field: the first
	// word could not be fixed without deleting everything after it.
	Cursor int
	// Required says an empty answer cannot be confirmed: `task reject` has no
	// meaning without feedback.
	Required bool
	// call is the verb the answer completes, and Field the argument the typed
	// text fills in.
	call  Call
	field string
	// filter says the answer is a live search rather than a one-off argument:
	// it is kept on the model so the poll goes on applying it.
	filter bool
}

// newPrompt opens a prompt with the cursor after whatever it is pre-filled
// with, which is where an operator expects to carry on typing. Every prompt is
// built through here so a future pre-filled one cannot forget.
func newPrompt(p Prompt) *Prompt {
	p.Cursor = len([]rune(p.Value))
	return &p
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

	// BoardFilter and NotesFilter are the live search over each view, sent to
	// the daemon as the `query` both list verbs take. They are kept apart
	// because the two views are different rows: a filter typed on one that
	// silently narrowed the other would hide work with nothing to say why.
	BoardFilter string
	NotesFilter string

	// BoardElsewhere and NotesElsewhere are how many rows the same filter
	// matches in OTHER projects, which only the empty state reads: a popup
	// takes its project from the focused pane (§4.2), so an empty board is
	// either a project with no work or a pane the operator did not mean.
	BoardElsewhere int
	NotesElsewhere int

	// Edit is a note whose body the operator asked to open in $EDITOR. The
	// model asks; the runtime runs the process (§12.1: Update stays pure, so
	// the tests never start a terminal — or an editor).
	Edit *Edit

	// Detail says the detail panel is open on the selection.
	Detail bool
	Prompt *Prompt
	Status string
	Err    string
	Quit   bool

	// homed says the cursor has been placed once. The board opens on the first
	// column that has work in it, but only once: after that the cursor is the
	// operator's, and a poll that finds their column empty must leave them
	// there.
	homed bool
}

// New is the model a fresh `ht tui` starts in.
func New(view View, project string) Model {
	return Model{View: view, Project: project, Width: 80, Height: 24}
}

// Edit is one note handed to an editor: which note, and what its body was
// when the operator asked. The body travels with it because what comes back
// is compared against it — an editor closed without a change writes nothing.
type Edit struct {
	NoteID string
	Seq    int64
	Body   string
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

// DataMsg is a fresh read of the daemon's answer to the list verbs. The two
// counts arrive from their own list: `task.list` and `note.list` both answer
// with a key named `elsewhere`, and one shared field would let the second
// answer overwrite the first.
type DataMsg struct {
	Tasks          []*tasks.Task
	Notes          []*tasks.Note
	Parked         []store.Parked
	BoardElsewhere int
	NotesElsewhere int
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
		m.BoardElsewhere, m.NotesElsewhere = v.BoardElsewhere, v.NotesElsewhere
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
		return m, m.listCall()
	case "/":
		m.Prompt = newPrompt(Prompt{
			Label:  "Find in " + string(m.View),
			Value:  m.filter(),
			call:   Call{Verb: listVerb(m.View), Args: map[string]any{}},
			field:  "query",
			filter: true,
		})
		return m, nil
	case "esc":
		// Herdr gives a popup no close key of its own, so esc is what an
		// operator presses first. It unwinds one layer at a time — a detail
		// here, a prompt in promptKey, a filter below — and only leaves when
		// nothing is open, so it can never discard something half-typed.
		if m.Detail {
			m.Detail = false
			return m, nil
		}
		if m.filter() != "" {
			// The other way out of a filter, and it clears the same things:
			// leaving the old term in the status describes a board that is no
			// longer filtered.
			m = m.setFilter("")
			m.Status = ""
			return m, m.listCall()
		}
		m.Quit = true
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
	m.Prompt = newPrompt(Prompt{
		Label:    fmt.Sprintf("What must change in #%d?", t.Seq),
		Required: true,
		call:     Call{Verb: "task.reject", Args: map[string]any{"id": t.ID}},
		field:    "feedback",
	})
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
	case "a":
		// Filing an idea is the one write here that needs no selection: it is
		// how the board gets its next row.
		m.Prompt = newPrompt(Prompt{
			Label:    "What is the note?",
			Required: true,
			call:     Call{Verb: "note.add", Args: map[string]any{}},
			field:    "body",
		})
		return m, nil
	case "e":
		// A decided note's wording is what was decided on, so the daemon
		// refuses it (§5.6) — and opening an editor on a refusal wastes the
		// operator's typing. The popup says so instead.
		n := m.SelectedNote()
		if n == nil {
			return m, nil
		}
		if n.Status.Terminal() {
			m.Status = fmt.Sprintf("note #%d is %s: a decided note's wording is what was decided on", n.Seq, n.Status)
			return m, nil
		}
		m.Edit = &Edit{NoteID: n.ID, Seq: n.Seq, Body: n.Body}
		m.Status = fmt.Sprintf("editing note #%d…", n.Seq)
		return m, nil
	case "K":
		// Keep is the third of the operator's decisions and the one the popup
		// could not reach. It asks for no reason: it is "yes, not now", where
		// a drop is a rejection and owes one.
		n := m.SelectedNote()
		if n == nil {
			return m, nil
		}
		m.Status = fmt.Sprintf("keeping note #%d…", n.Seq)
		return m, &Call{Verb: "note.keep", Args: map[string]any{"id": n.ID}}
	case "v":
		n := m.SelectedNote()
		if n == nil {
			return m, nil
		}
		m.Prompt = newPrompt(Prompt{
			Label: fmt.Sprintf("Verdict on note #%d (task, keep or drop)", n.Seq),
			call:  Call{Verb: "note.verdict", Args: map[string]any{"id": n.ID}},
			field: "verdict", Required: true,
		})
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
		m.Prompt = newPrompt(Prompt{
			Label: fmt.Sprintf("Why is note #%d dropped?", n.Seq),
			call:  Call{Verb: "note.drop", Args: map[string]any{"id": n.ID}},
			field: "reason",
		})
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
	case "ctrl+c":
		// A prompt must never be a trap: the way out of the TUI is the same
		// key whatever is open.
		m.Quit = true
		return m, nil
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
		if p.filter {
			// An emptied filter answers with no `query` at all, so pressing
			// enter on a cleared prompt is the other way to drop the search —
			// and it says nothing afterwards. A status is a fact about the
			// board; "filter: " with nothing after it was neither true nor
			// empty, and it held the status row open, which is what kept the
			// header off the screen.
			m = m.setFilter(strings.TrimSpace(p.Value))
			m.Status = ""
			if q := m.filter(); q != "" {
				m.Status = "filter: " + q
			}
			return m, &call
		}
		m.Status = call.Verb + "…"
		return m, &call
	case "left":
		if p.Cursor > 0 {
			p.Cursor--
		}
		return m, nil
	case "right":
		if p.Cursor < len([]rune(p.Value)) {
			p.Cursor++
		}
		return m, nil
	case "home", "ctrl+a":
		p.Cursor = 0
		return m, nil
	case "end", "ctrl+e":
		p.Cursor = len([]rune(p.Value))
		return m, nil
	case "backspace":
		// The rune BEFORE the cursor, and the cursor follows it back.
		if r := []rune(p.Value); p.Cursor > 0 && p.Cursor <= len(r) {
			p.Value = string(r[:p.Cursor-1]) + string(r[p.Cursor:])
			p.Cursor--
		}
		return m, nil
	case "delete":
		// The rune AT the cursor, which stays where it is.
		if r := []rune(p.Value); p.Cursor < len(r) {
			p.Value = string(r[:p.Cursor]) + string(r[p.Cursor+1:])
		}
		return m, nil
	}
	typed := ""
	if len([]rune(k)) == 1 {
		typed = k
	} else if k == "space" {
		typed = " "
	}
	if typed != "" {
		r := []rune(p.Value)
		if p.Cursor > len(r) {
			p.Cursor = len(r)
		}
		p.Value = string(r[:p.Cursor]) + typed + string(r[p.Cursor:])
		p.Cursor++
	}
	return m, nil
}

// listVerb is the verb that re-reads a view.
func listVerb(v View) string {
	if v == ViewBoard {
		return "task.list"
	}
	return "note.list"
}

// filter is the current view's search, and setFilter replaces it.
func (m Model) filter() string {
	if m.View == ViewBoard {
		return m.BoardFilter
	}
	return m.NotesFilter
}

func (m Model) setFilter(q string) Model {
	if m.View == ViewBoard {
		m.BoardFilter = q
	} else {
		m.NotesFilter = q
	}
	return m
}

// Filters is the query each list verb is read with. The runtime passes it to
// the daemon on every poll, so a filter holds until the operator clears it.
func (m Model) Filters() map[string]string {
	return map[string]string{"task.list": m.BoardFilter, "note.list": m.NotesFilter}
}

// listCall re-reads the current view under whatever filter is set.
func (m Model) listCall() *Call {
	args := map[string]any{}
	if q := m.filter(); q != "" {
		args["query"] = q
	}
	return &Call{Verb: listVerb(m.View), Args: args}
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
	if !m.homed && len(m.Column(m.Col)) == 0 {
		m.homed = true
		for i := range Columns {
			if len(m.Column(i)) > 0 {
				m.Col = i
				break
			}
		}
	}
	m.homed = m.homed || len(m.Tasks) > 0
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
