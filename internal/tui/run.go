package tui

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/husniadil/herdr-tasks/internal/client"
	"github.com/husniadil/herdr-tasks/internal/codes"
	"github.com/husniadil/herdr-tasks/internal/protocol"
	"github.com/husniadil/herdr-tasks/internal/store"
	"github.com/husniadil/herdr-tasks/internal/tasks"
)

// refreshEvery is how often the board re-reads. §8.2's `events --follow` is
// the subscription primitive; a small poll is what a view that must also
// notice its own writes lands on, and it is bounded and local.
const refreshEvery = 2 * time.Second

// Sender is how the runtime reaches the daemon. The TUI never opens the
// SQLite file: the daemon is the only writer (§2.2) and this is the same
// socket every other door uses.
type Sender interface {
	Call(protocol.Request) (json.RawMessage, error)
}

type socketSender struct{}

func (socketSender) Call(req protocol.Request) (json.RawMessage, error) { return client.Call(req) }

// Run opens the TUI on a view and blocks until the operator leaves it.
func Run(view View, base protocol.Request) error {
	// The pane owns the alternate screen for as long as this runs, so the
	// client's warnings are held rather than written into a frame they would
	// corrupt and be redrawn out of. They are said on the way out, where
	// there is a shell to read them: held, never dropped.
	release := holdWarnings(os.Stderr)
	defer release()
	p := tea.NewProgram(&program{
		model: New(view, base.Project),
		send:  socketSender{},
		base:  base,
	}, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}

// holdWarnings redirects the client's out-of-band warnings into a buffer for
// as long as the pane holds the screen. The function it returns restores the
// previous sink and writes whatever was held to w.
func holdWarnings(w io.Writer) func() {
	held := &syncBuffer{}
	restore := client.SetWarnings(held)
	return func() {
		restore()
		if s := held.String(); s != "" {
			fmt.Fprint(w, s)
		}
	}
}

// syncBuffer is a buffer several goroutines may write: the reads that produce
// a warning run on bubbletea's command goroutines, and the flush runs on the
// one that called Run.
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// program is the thin bubbletea shell over the pure model: it translates
// bubbletea's messages into ours, runs the Call the model asks for, and draws
// what Render returns.
type program struct {
	model Model
	send  Sender
	base  protocol.Request
	// loading is a read already on its way to the daemon. The tick is a timer,
	// not an answer: a daemon that has stopped answering would otherwise get
	// one more goroutine and one more open socket every two seconds, for as
	// long as the operator leaves the pane open.
	loading bool
}

// startLoad issues the board read and records that one is in flight. Every
// path that reads goes through it, so the tick has one flag to consult.
func (p *program) startLoad() tea.Cmd {
	p.loading = true
	return p.load(p.model.Filters())
}

func (p *program) Init() tea.Cmd { return tea.Batch(p.startLoad(), tick()) }

func (p *program) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var in Msg
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		in = SizeMsg{Width: v.Width, Height: v.Height}
	case tea.KeyMsg:
		in = KeyMsg{Key: keyName(v), Paste: v.Paste, Alt: v.Alt}
	case tea.MouseMsg:
		m := tea.MouseEvent(v)
		if m.Action != tea.MouseActionPress {
			return p, nil
		}
		// The wheel is a press too, on a button of its own. Dropping
		// everything that was not the left one dropped it, so a panel taller
		// than the screen had nothing that could move it.
		switch m.Button {
		case tea.MouseButtonLeft:
			in = MouseMsg{X: m.X, Y: m.Y, At: time.Now().UnixMilli()}
		case tea.MouseButtonWheelUp:
			in = WheelMsg{X: m.X, Y: m.Y, Up: true, At: time.Now().UnixMilli()}
		case tea.MouseButtonWheelDown:
			in = WheelMsg{X: m.X, Y: m.Y, At: time.Now().UnixMilli()}
		default:
			return p, nil
		}
	case editedMsg:
		return p, p.afterEditor(v)
	case DataMsg, ErrMsg, DoneMsg, TaskMsg:
		// A read is over either way it ended, and only a read clears the flag.
		switch msg.(type) {
		case DataMsg, ErrMsg:
			p.loading = false
		}
		in = msg.(Msg)
	case tickMsg:
		if p.loading {
			// Keep the timer, skip the read: the previous one has not come
			// back yet.
			return p, tick()
		}
		return p, tea.Batch(p.startLoad(), tick())
	default:
		return p, nil
	}
	before := p.model.SelectedTaskID()
	next, call := Update(p.model, in)
	p.model = next
	if p.model.Quit {
		return p, tea.Quit
	}
	// The listing carries no bodies, so the panel's report, description and
	// evidence are read for the selection alone: when the cursor lands on
	// another task, and again on each refresh, because a submission that
	// landed since the last one is exactly what the operator is waiting for.
	detail := p.detailCmd(before, in)
	// An asked-for editor is taken once: the model records the request, the
	// runtime is what runs a process (§12.1).
	if e := p.model.Edit; e != nil {
		p.model.Edit = nil
		return p, p.edit(*e)
	}
	if call == nil {
		return p, detail
	}
	// A read verb IS the refresh; run() would throw its body away.
	if strings.HasSuffix(call.Verb, ".list") {
		return p, tea.Batch(p.startLoad(), detail)
	}
	// A mutation is only real to the operator once the board shows it, so the
	// write is followed by the read rather than waiting for the next tick.
	return p, tea.Batch(tea.Sequence(p.run(*call), p.startLoad()), detail)
}

func (p *program) View() string { return Render(p.model, time.Now().UnixMilli()) }

// detailCmd reads the selected task in full when the selection has moved, or
// when the message that just landed was a fresh listing — nothing else can
// have changed what the panel draws. It answers nil when there is nothing
// selected, so a board view with an empty column costs no call.
func (p *program) detailCmd(before string, in Msg) tea.Cmd {
	id := p.model.SelectedTaskID()
	if id == "" {
		return nil
	}
	if _, refreshed := in.(DataMsg); !refreshed && id == before {
		return nil
	}
	return p.loadTask(id)
}

// loadTask reads one task in full, which is where the bodies `list` leaves out
// live (§6.1: the two verbs answer different shapes).
func (p *program) loadTask(id string) tea.Cmd {
	return func() tea.Msg {
		req := p.base
		req.Verb, req.Args = "task.get", map[string]any{"id": id}
		raw, err := p.send.Call(req)
		if err != nil {
			return errMsg(err)
		}
		var out struct {
			Task *tasks.Task `json:"task"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			return ErrMsg{Code: codes.Unexpected, Message: err.Error()}
		}
		if out.Task == nil {
			return nil
		}
		return TaskMsg{Task: out.Task}
	}
}

// run performs one daemon call off the draw loop and folds the answer back in
// as a message, so Update stays pure.
func (p *program) run(c Call) tea.Cmd {
	return func() tea.Msg {
		req := p.base
		req.Verb, req.Args = c.Verb, c.Args
		if _, err := p.send.Call(req); err != nil {
			return errMsg(err)
		}
		return DoneMsg{Status: c.Verb + " done"}
	}
}

// editedMsg is one editor session that has finished, for better or worse.
type editedMsg struct {
	edit Edit
	path string
	err  error
}

// edit hands the note's body to $EDITOR. tea.ExecProcess is what makes this
// possible at all: it pauses the Program and gives the editor the terminal,
// which is exactly what its own documentation is for. Measured in a throwaway
// Herdr session: the popup suspends into the editor and comes back usable.
func (p *program) edit(e Edit) tea.Cmd {
	path, err := startEdit(e)
	if err != nil {
		return func() tea.Msg { return errMsg(err) }
	}
	argv, err := editorCommand(os.Getenv, path)
	if err != nil {
		os.Remove(path)
		return func() tea.Msg { return errMsg(err) }
	}
	return tea.ExecProcess(exec.Command(argv[0], argv[1:]...), func(runErr error) tea.Msg {
		return editedMsg{edit: e, path: path, err: runErr}
	})
}

// afterEditor decides what came back. Nothing is sent for a body that did not
// change, and the scratch file only goes once the daemon has the text.
func (p *program) afterEditor(v editedMsg) tea.Cmd {
	call, err := finishEdit(v.edit, v.path, v.err)
	if err != nil {
		return func() tea.Msg { return afterUpdate(v.path, err) }
	}
	if call == nil {
		os.Remove(v.path)
		return func() tea.Msg { return DoneMsg{Status: "note unchanged"} }
	}
	send := func() tea.Msg {
		req := p.base
		req.Verb, req.Args = call.Verb, call.Args
		_, err := p.send.Call(req)
		return afterUpdate(v.path, err)
	}
	return tea.Sequence(send, p.startLoad())
}

// load reads the three lists the two views show, in one command. The filters
// are passed in rather than read off p.model inside the command: this runs on
// another goroutine, and the model belongs to the update loop.
func (p *program) load(filters map[string]string) tea.Cmd {
	return func() tea.Msg {
		var data DataMsg
		for _, verb := range []string{"task.list", "note.list", "parked.list"} {
			req := p.base
			req.Verb, req.Args = verb, map[string]any{}
			if q := filters[verb]; q != "" {
				req.Args["query"] = q
			}
			raw, err := p.send.Call(req)
			if err != nil {
				return errMsg(err)
			}
			// Each answer is read on its own: `task.list` and `note.list` both
			// carry an `elsewhere`, and one shared struct would keep whichever
			// replied last.
			var out struct {
				Tasks     []*tasks.Task  `json:"tasks"`
				Notes     []*tasks.Note  `json:"notes"`
				Parked    []store.Parked `json:"parked"`
				Elsewhere int            `json:"elsewhere"`
			}
			if err := json.Unmarshal(raw, &out); err != nil {
				return ErrMsg{Code: codes.Unexpected, Message: err.Error()}
			}
			switch verb {
			case "task.list":
				data.Tasks, data.BoardElsewhere = out.Tasks, out.Elsewhere
			case "note.list":
				data.Notes, data.NotesElsewhere = out.Notes, out.Elsewhere
			case "parked.list":
				data.Parked = out.Parked
			}
		}
		return data
	}
}

func errMsg(err error) ErrMsg {
	if f, ok := err.(*client.Failure); ok {
		return ErrMsg{Code: f.Code(), Message: f.Message()}
	}
	if ce, ok := err.(*codes.Error); ok {
		return ErrMsg{Code: ce.Code, Message: ce.Message}
	}
	return ErrMsg{Code: codes.Unexpected, Message: err.Error()}
}

type tickMsg struct{}

func tick() tea.Cmd {
	return tea.Tick(refreshEvery, func(time.Time) tea.Msg { return tickMsg{} })
}

// keyName maps bubbletea's key vocabulary onto the strings the pure model
// reads, which are bubbletea's own names for everything but a printable rune.
func keyName(k tea.KeyMsg) string {
	switch k.Type {
	case tea.KeyRunes:
		return string(k.Runes)
	case tea.KeySpace:
		return "space"
	}
	return k.String()
}
