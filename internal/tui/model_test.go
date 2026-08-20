package tui

import (
	"strings"
	"testing"

	"github.com/husniadil/herdr-tasks/internal/store"
	"github.com/husniadil/herdr-tasks/internal/tasks"
)

// The model is pure: every test here runs with no daemon, no socket and no
// Herdr, which is the layer §12.1 calls mandatory.

func task(seq int64, status tasks.Status, title string) *tasks.Task {
	return &tasks.Task{ID: "T" + title, Seq: seq, Status: status, Title: title}
}

func board(t *testing.T, ts ...*tasks.Task) Model {
	t.Helper()
	m := New(ViewBoard, "/repo")
	m, _ = Update(m, DataMsg{Tasks: ts})
	return m
}

// §11.6: the board is the resolved project's tasks in state columns.
func TestBoardGroupsTasksIntoStateColumns(t *testing.T) {
	m := board(t,
		task(1, tasks.StatusTodo, "a"), task(2, tasks.StatusDoing, "b"),
		task(3, tasks.StatusReview, "c"), task(4, tasks.StatusDone, "d"),
		task(5, tasks.StatusTodo, "e"))
	want := []int{2, 1, 1, 1}
	for i, n := range want {
		if got := len(m.Column(i)); got != n {
			t.Fatalf("column %s: got %d tasks, want %d", Columns[i], got, n)
		}
	}
	if m.Column(0)[1].Title != "e" {
		t.Fatalf("column order is the daemon's, got %q", m.Column(0)[1].Title)
	}
}

// §11.6: the keyboard is the fallback the mouse-first board still owes.
func TestBoardCursorKeepsItsPlacePerColumn(t *testing.T) {
	m := board(t, task(1, tasks.StatusTodo, "a"), task(2, tasks.StatusTodo, "b"), task(3, tasks.StatusDoing, "c"))
	m, _ = Update(m, KeyMsg{Key: "down"})
	if m.Row[0] != 1 {
		t.Fatalf("down: row %d, want 1", m.Row[0])
	}
	m, _ = Update(m, KeyMsg{Key: "right"})
	m, _ = Update(m, KeyMsg{Key: "down"})
	if m.Row[1] != 0 {
		t.Fatalf("down at the end of a one-card column moved to %d", m.Row[1])
	}
	m, _ = Update(m, KeyMsg{Key: "left"})
	if m.SelectedTask().Title != "b" {
		t.Fatalf("coming back lost the place: %q", m.SelectedTask().Title)
	}
}

// §5.6 and §16.3: a claim and how much of its lease is left are on the card,
// because a lease nobody can see is a lease nobody renews.
func TestBoardCardShowsClaimAndLease(t *testing.T) {
	c := task(7, tasks.StatusDoing, "wire the door")
	c.ClaimedBy, c.ClaimedByName, c.LeaseUntil = "agent:wF:p1", "builder", 300_000
	m := board(t, c)
	m.Width = 200
	out := Render(m, 0)
	if !strings.Contains(out, "builder") || !strings.Contains(out, "5m") {
		t.Fatalf("card hid the claim or the lease:\n%s", out)
	}
}

// §11.6: a detail panel on select.
func TestEnterOpensTheDetailPanelOnTheSelection(t *testing.T) {
	c := task(3, tasks.StatusReview, "the door")
	c.Report, c.Evidence = "wired it", []string{"make test: ok"}
	m := board(t, c)
	m, _ = Update(m, KeyMsg{Key: "enter"})
	if !m.Detail {
		t.Fatal("enter did not open the detail panel")
	}
	d := Detail(m, 0)
	if !strings.Contains(d, "wired it") || !strings.Contains(d, "make test: ok") {
		t.Fatalf("detail hid the report or its evidence:\n%s", d)
	}
}

// §6.1: the TUI is a door like the others — it asks the daemon for the same
// verb, by the same name, with the same arguments.
func TestApproveAsksTheDaemonForTaskApprove(t *testing.T) {
	m := board(t, task(1, tasks.StatusReview, "a"))
	m, call := Update(m, KeyMsg{Key: "a"})
	if call == nil || call.Verb != "task.approve" {
		t.Fatalf("approve sent %#v", call)
	}
	if call.Args["id"] != "Ta" {
		t.Fatalf("approve named %v", call.Args["id"])
	}
	if m.Status == "" {
		t.Fatal("approve said nothing about what it was doing")
	}
}

// A task that is not in review has nothing to approve; saying so beats sending
// the daemon a call that can only come back CONFLICT.
func TestApproveOnWorkNotInReviewSendsNothing(t *testing.T) {
	m := board(t, task(1, tasks.StatusDoing, "a"))
	m, call := Update(m, KeyMsg{Key: "a"})
	if call != nil {
		t.Fatalf("approve on a doing task sent %#v", call)
	}
	if !strings.Contains(m.Status, "review") {
		t.Fatalf("no reason given: %q", m.Status)
	}
}

// §6.3 has no code for "sent back with nothing to change": reject prompts for
// feedback and refuses to send an empty one.
func TestRejectPromptsForFeedbackAndWillNotSendItEmpty(t *testing.T) {
	m := board(t, task(4, tasks.StatusReview, "a"))
	m, call := Update(m, KeyMsg{Key: "x"})
	if call != nil || m.Prompt == nil {
		t.Fatalf("reject sent %#v before asking for feedback", call)
	}
	m, call = Update(m, KeyMsg{Key: "enter"})
	if call != nil {
		t.Fatalf("empty feedback was sent: %#v", call)
	}
	if !strings.HasPrefix(m.Err, "USAGE") {
		t.Fatalf("empty feedback was not refused: %q", m.Err)
	}
	for _, k := range []string{"f", "i", "x", "space", "i", "t"} {
		m, _ = Update(m, KeyMsg{Key: k})
	}
	m, call = Update(m, KeyMsg{Key: "enter"})
	if call == nil || call.Verb != "task.reject" || call.Args["feedback"] != "fix it" {
		t.Fatalf("reject sent %#v", call)
	}
	if m.Prompt != nil {
		t.Fatal("the prompt stayed open after it was answered")
	}
}

// The TUI offers human verbs only: claim, touch, submit and release belong to
// the agent doing the work, and none of them has a key here.
func TestTheBoardOffersHumanVerbsOnly(t *testing.T) {
	m := board(t, task(1, tasks.StatusTodo, "a"), task(2, tasks.StatusReview, "b"))
	for _, k := range []string{"c", "s", "t", "e", "u"} {
		if _, call := Update(m, KeyMsg{Key: k}); call != nil {
			t.Fatalf("key %q reached for %s", k, call.Verb)
		}
	}
	m.Col, m.Row[2] = 2, 0
	got := []string{}
	for _, v := range m.Verbs() {
		got = append(got, v.Label)
	}
	for _, forbidden := range []string{"claim", "submit", "release", "touch"} {
		for _, label := range got {
			if label == forbidden {
				t.Fatalf("the footer offered %q", forbidden)
			}
		}
	}
}

func notesModel(t *testing.T, ns []*tasks.Note, ps []store.Parked) Model {
	t.Helper()
	m := New(ViewNotes, "/repo")
	m, _ = Update(m, DataMsg{Notes: ns, Parked: ps})
	return m
}

// §14: a note becomes a task, is kept, or is dropped — and only the operator
// decides. All three are on this view.
func TestNotesViewOffersVerdictPromoteAndDrop(t *testing.T) {
	m := notesModel(t, []*tasks.Note{{ID: "N1", Seq: 1, Status: "inbox", Body: "an idea"}}, nil)
	m2, call := Update(m, KeyMsg{Key: "p"})
	if call == nil || call.Verb != "note.promote" || call.Args["id"] != "N1" {
		t.Fatalf("promote sent %#v", call)
	}
	_ = m2
	m3, call := Update(m, KeyMsg{Key: "v"})
	if call != nil || m3.Prompt == nil {
		t.Fatalf("verdict sent %#v before asking which one", call)
	}
	for _, k := range []string{"k", "e", "e", "p"} {
		m3, _ = Update(m3, KeyMsg{Key: k})
	}
	m3, call = Update(m3, KeyMsg{Key: "enter"})
	if call == nil || call.Verb != "note.verdict" || call.Args["verdict"] != "keep" {
		t.Fatalf("verdict sent %#v", call)
	}
	m4, call := Update(m, KeyMsg{Key: "d"})
	if call != nil || m4.Prompt == nil {
		t.Fatalf("drop sent %#v before asking why", call)
	}
	m4, call = Update(m4, KeyMsg{Key: "enter"})
	if call == nil || call.Verb != "note.drop" {
		t.Fatalf("drop with no reason sent %#v; a reason is optional", call)
	}
}

// §9.3: only a human resolves or rejects a parked action, and the TUI is where
// they see one.
func TestParkedActionsResolveAndReject(t *testing.T) {
	m := notesModel(t, nil, []store.Parked{{ID: "P1", Verb: "tasks.approve", Subject: "agent:wF:p1", State: "parked"}})
	m, _ = Update(m, KeyMsg{Key: "right"})
	if m.Pane != PaneParked {
		t.Fatal("right did not reach the parked list")
	}
	_, call := Update(m, KeyMsg{Key: "y"})
	if call == nil || call.Verb != "parked.resolve" || call.Args["reject"] != nil {
		t.Fatalf("resolve sent %#v", call)
	}
	_, call = Update(m, KeyMsg{Key: "n"})
	if call == nil || call.Verb != "parked.resolve" || call.Args["reject"] != true {
		t.Fatalf("reject sent %#v", call)
	}
}

// §11.6: mouse-first. A click on the tabs switches view, a click on a card
// selects it, and a click on a footer verb runs that verb.
func TestMouseSelectsSwitchesAndRunsVerbs(t *testing.T) {
	m := board(t, task(1, tasks.StatusTodo, "a"), task(2, tasks.StatusReview, "b"))
	m.Width, m.Height = 80, 24
	m, _ = Update(m, MouseMsg{X: ColumnAt(80, 0)*20 + 2, Y: firstCard})
	if m.SelectedTask().Title != "a" || !m.Detail {
		t.Fatalf("clicking a card selected %#v", m.SelectedTask())
	}
	m, _ = Update(m, MouseMsg{X: 41, Y: firstCard})
	if m.Col != 2 || m.SelectedTask().Title != "b" {
		t.Fatalf("clicking the review column selected column %d", m.Col)
	}
	var call *Call
	m, call = Update(m, MouseMsg{X: 2, Y: m.Height - 1})
	if call == nil || call.Verb != "task.approve" {
		t.Fatalf("clicking the approve verb sent %#v", call)
	}
	m, _ = Update(m, MouseMsg{X: len(boardTab) + 1, Y: tabsRow})
	if m.View != ViewNotes {
		t.Fatalf("clicking the notes tab left the view on %s", m.View)
	}
}

// §2.1: `<name> tui [<view>]`.
func TestParseViewAcceptsTheTwoViewsAndNothingElse(t *testing.T) {
	for arg, want := range map[string]View{"": ViewBoard, "board": ViewBoard, "notes": ViewNotes} {
		got, err := ParseView(arg)
		if err != nil || got != want {
			t.Fatalf("ParseView(%q) = %v, %v", arg, got, err)
		}
	}
	if _, err := ParseView("kanban"); err == nil {
		t.Fatal("an unknown view was accepted")
	}
}

// A shorter list must not leave the cursor pointing past the end.
func TestRefreshClampsTheCursorToTheNewData(t *testing.T) {
	m := board(t, task(1, tasks.StatusTodo, "a"), task(2, tasks.StatusTodo, "b"))
	m, _ = Update(m, KeyMsg{Key: "down"})
	m, _ = Update(m, DataMsg{Tasks: []*tasks.Task{task(1, tasks.StatusTodo, "a")}})
	if m.Row[0] != 0 || m.SelectedTask() == nil {
		t.Fatalf("cursor left at row %d over %d cards", m.Row[0], len(m.Column(0)))
	}
}

// §6.3: a failed call names its code, and the next success clears it.
func TestFailureShowsItsContractCode(t *testing.T) {
	m := board(t)
	m, _ = Update(m, ErrMsg{Code: "CONFLICT", Message: "someone else won"})
	if !strings.Contains(Render(m, 0), "CONFLICT") {
		t.Fatal("the footer swallowed the code")
	}
	m, _ = Update(m, DoneMsg{Status: "approved #1"})
	if m.Err != "" {
		t.Fatalf("a success left the old error up: %q", m.Err)
	}
}
