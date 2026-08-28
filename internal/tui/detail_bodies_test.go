package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/husniadil/herdr-tasks/internal/protocol"
)

// msgsOf runs one command and flattens the batch it may be, so a test can see
// every message the runtime produced from one update.
func msgsOf(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		out := []tea.Msg{}
		for _, c := range batch {
			out = append(out, msgsOf(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

// `list` is a listing and carries no bodies, so the detail panel — the report,
// the evidence, the description, the checklist — cannot come from it. It comes
// from `get`, for the ONE task the operator has selected, and the row is
// filled in from that answer. Without this the panel would render a task whose
// bodies are simply absent, which reads as work submitted with no report.
func TestTheDetailPanelReadsTheBodiesFromGet(t *testing.T) {
	rec := &recorder{answers: map[string]string{
		"task.list": `{"tasks":[{"id":"T1","seq":3,"status":"review","title":"the door"}],"count":1}`,
		"task.get": `{"task":{"id":"T1","seq":3,"status":"review","title":"the door",` +
			`"description":"the long one","report":"wired it","evidence":["make test: ok"]}}`,
	}}
	p := &program{model: New(ViewBoard, "/repo"), send: rec, base: protocol.Request{Project: "/repo"}}

	// The poll, and the board it produces: a row with no bodies on it.
	_, cmd := p.Update(p.load(p.model.Filters())())
	if got := p.model.SelectedTask(); got == nil || got.Title != "the door" {
		t.Fatalf("the listing did not reach the board: %+v", p.model.Tasks)
	}

	// The refresh asks for the selection in full, and the answer fills the row.
	for _, msg := range msgsOf(cmd) {
		p.Update(msg)
	}
	var asked bool
	for _, req := range rec.got {
		if req.Verb == "task.get" && req.Args["id"] == "T1" {
			asked = true
		}
	}
	if !asked {
		t.Fatalf("nothing asked `get` for the selected task, so the panel has no bodies to draw: %+v", rec.got)
	}
	p.model.Detail = true
	d := Detail(p.model, 0)
	for _, want := range []string{"the long one", "wired it", "make test: ok"} {
		if !strings.Contains(d, want) {
			t.Fatalf("the detail panel lost %q, which `list` no longer carries:\n%s", want, d)
		}
	}
}

// Moving the cursor is a new selection, so it asks for that task rather than
// waiting for the next poll: the panel would otherwise show the previous
// task's report under the new task's title for as long as the poll takes.
func TestMovingTheCursorAsksForTheNewSelection(t *testing.T) {
	rec := &recorder{answers: map[string]string{
		"task.list": `{"tasks":[` +
			`{"id":"T1","seq":1,"status":"todo","title":"first"},` +
			`{"id":"T2","seq":2,"status":"todo","title":"second"}],"count":2}`,
		"task.get": `{"task":{"id":"T2","seq":2,"status":"todo","title":"second","description":"the second body"}}`,
	}}
	p := &program{model: New(ViewBoard, "/repo"), send: rec, base: protocol.Request{Project: "/repo"}}
	p.Update(p.load(p.model.Filters())())
	rec.got = nil

	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyDown})
	if p.model.SelectedTask().ID != "T2" {
		t.Fatalf("the cursor did not move: %+v", p.model.SelectedTask())
	}
	for _, msg := range msgsOf(cmd) {
		p.Update(msg)
	}
	var asked []string
	for _, req := range rec.got {
		if req.Verb == "task.get" {
			asked = append(asked, req.Args["id"].(string))
		}
	}
	if len(asked) != 1 || asked[0] != "T2" {
		t.Fatalf("moving the cursor asked `get` for %v, want exactly T2", asked)
	}
	p.model.Detail = true
	if d := Detail(p.model, 0); !strings.Contains(d, "the second body") {
		t.Fatalf("the panel is still on the previous selection:\n%s", d)
	}
}
