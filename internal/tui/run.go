package tui

import (
	"encoding/json"
	"strings"
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
	p := tea.NewProgram(&program{
		model: New(view, base.Project),
		send:  socketSender{},
		base:  base,
	}, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}

// program is the thin bubbletea shell over the pure model: it translates
// bubbletea's messages into ours, runs the Call the model asks for, and draws
// what Render returns.
type program struct {
	model Model
	send  Sender
	base  protocol.Request
}

func (p *program) Init() tea.Cmd { return tea.Batch(p.load(p.model.Filters()), tick()) }

func (p *program) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var in Msg
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		in = SizeMsg{Width: v.Width, Height: v.Height}
	case tea.KeyMsg:
		in = KeyMsg{Key: keyName(v)}
	case tea.MouseMsg:
		m := tea.MouseEvent(v)
		if m.Action != tea.MouseActionPress || m.Button != tea.MouseButtonLeft {
			return p, nil
		}
		in = MouseMsg{X: m.X, Y: m.Y}
	case DataMsg, ErrMsg, DoneMsg:
		in = msg.(Msg)
	case tickMsg:
		return p, tea.Batch(p.load(p.model.Filters()), tick())
	default:
		return p, nil
	}
	next, call := Update(p.model, in)
	p.model = next
	if p.model.Quit {
		return p, tea.Quit
	}
	if call == nil {
		return p, nil
	}
	// A read verb IS the refresh; run() would throw its body away.
	if strings.HasSuffix(call.Verb, ".list") {
		return p, p.load(p.model.Filters())
	}
	// A mutation is only real to the operator once the board shows it, so the
	// write is followed by the read rather than waiting for the next tick.
	return p, tea.Sequence(p.run(*call), p.load(p.model.Filters()))
}

func (p *program) View() string { return Render(p.model, time.Now().UnixMilli()) }

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

// load reads the three lists the two views show, in one command. The filters
// are passed in rather than read off p.model inside the command: this runs on
// another goroutine, and the model belongs to the update loop.
func (p *program) load(filters map[string]string) tea.Cmd {
	return func() tea.Msg {
		var data DataMsg
		var out struct {
			Tasks  []*tasks.Task  `json:"tasks"`
			Notes  []*tasks.Note  `json:"notes"`
			Parked []store.Parked `json:"parked"`
		}
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
			if err := json.Unmarshal(raw, &out); err != nil {
				return ErrMsg{Code: codes.Unexpected, Message: err.Error()}
			}
		}
		data.Tasks, data.Notes, data.Parked = out.Tasks, out.Notes, out.Parked
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
