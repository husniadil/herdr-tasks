package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/husniadil/herdr-tasks/internal/protocol"
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

// §11.6: the footer verbs are clickable where they are drawn. The hit-test
// reads the bottom rows of the screen, so a view whose content is shorter than
// the terminal must still put its footer there.
func TestFooterVerbsAreClickableWhereTheyAreDrawn(t *testing.T) {
	m := board(t, task(1, tasks.StatusReview, "a"))
	m.Width, m.Height = 80, 24
	rows := strings.Count(strings.TrimRight(Render(m, 0), "\n"), "\n")
	if rows+1 < m.Height-footerRows {
		t.Fatalf("the view drew %d rows of a %d-row screen: the footer is not at the bottom", rows+1, m.Height)
	}
	_, call := Update(m, MouseMsg{X: 2, Y: m.Height - 1})
	if call == nil || call.Verb != "task.approve" {
		t.Fatalf("clicking the drawn approve verb sent %#v", call)
	}
}

// A click on the empty middle of the board selects nothing and runs nothing.
// Approve and parked.resolve are mutating, gated verbs (§9.1); neither may be
// reachable by a stray click.
func TestAClickOnEmptySpaceRunsNothing(t *testing.T) {
	m := board(t, task(1, tasks.StatusReview, "a"))
	m.Width, m.Height = 80, 24
	for y := firstCard + 1; y < m.Height-footerRows; y++ {
		if _, call := Update(m, MouseMsg{X: 2, Y: y}); call != nil {
			t.Fatalf("a click on empty row %d ran %s", y, call.Verb)
		}
	}
	n := New(ViewNotes, "/repo")
	n, _ = Update(n, DataMsg{Parked: []store.Parked{{ID: "P1", Verb: "tasks.approve", Subject: "agent:wF:p1"}}})
	n.Width, n.Height = 80, 24
	n, _ = Update(n, KeyMsg{Key: "right"})
	for y := firstCard + 1; y < n.Height-footerRows; y++ {
		if _, call := Update(n, MouseMsg{X: 2, Y: y}); call != nil {
			t.Fatalf("a click on empty row %d of the notes view ran %s", y, call.Verb)
		}
	}
}

// §11.6: the tabs are hit-tested where they are drawn. The active tab renders
// differently from the inactive one, and the hit-test must follow it.
func TestTabHitTestMatchesTheDrawnTabs(t *testing.T) {
	for _, active := range []View{ViewBoard, ViewNotes} {
		wBoard := len(tab(boardTab, active == ViewBoard))
		wNotes := len(tab(notesTab, active == ViewNotes))
		row := strings.Split(Render(New(active, "proj"), 0), "\n")[0]
		for x := 0; x < wBoard; x++ {
			if got := TabAt(x); got != ViewBoard {
				t.Fatalf("with %s active, x=%d is drawn on the board tab but hit-tests as %q in %q", active, x, got, row)
			}
		}
		for x := wBoard; x < wBoard+wNotes; x++ {
			if got := TabAt(x); got != ViewNotes {
				t.Fatalf("with %s active, x=%d is drawn on the notes tab but hit-tests as %q in %q", active, x, got, row)
			}
		}
		if got := TabAt(wBoard + wNotes); got != "" {
			t.Fatalf("with %s active, the cell after the tabs hit-tests as %q in %q", active, got, row)
		}
	}
}

// A prompt must not trap the operator: ctrl+c leaves, whatever is open.
func TestCtrlCLeavesEvenWithAPromptOpen(t *testing.T) {
	m := board(t, task(1, tasks.StatusReview, "a"))
	m, _ = Update(m, KeyMsg{Key: "x"})
	if m.Prompt == nil {
		t.Fatal("the reject prompt did not open")
	}
	m, _ = Update(m, KeyMsg{Key: "ctrl+c"})
	if !m.Quit {
		t.Fatal("ctrl+c with a prompt open did not leave")
	}
}

// The cursor is the operator's, not the poll's: a refresh that changes nothing
// must not move them off a column they chose to look at.
func TestARefreshDoesNotDragTheCursorOffAnEmptyColumn(t *testing.T) {
	ts := []*tasks.Task{task(1, tasks.StatusTodo, "a")}
	m := board(t, ts...)
	m, _ = Update(m, KeyMsg{Key: "right"})
	if m.Col != 1 {
		t.Fatalf("could not move to the empty doing column: col %d", m.Col)
	}
	m, _ = Update(m, DataMsg{Tasks: ts})
	if m.Col != 1 {
		t.Fatalf("a refresh moved the cursor to column %d", m.Col)
	}
}

// Herdr gives a popup no close key of its own, so esc is the key an operator
// reaches for first. It unwinds one layer at a time: a prompt, then a detail,
// then the board itself. Quitting straight out would throw away a half-typed
// reject reason, which is the opposite of the fix.
func TestEscUnwindsOneLayerAtATime(t *testing.T) {
	// Layer 1: an open prompt is cancelled, and the board stays.
	m := board(t, task(1, tasks.StatusReview, "a"))
	m, _ = Update(m, KeyMsg{Key: "x"})
	if m.Prompt == nil {
		t.Fatal("the reject prompt did not open")
	}
	m, _ = Update(m, KeyMsg{Key: "esc"})
	if m.Prompt != nil {
		t.Fatal("esc did not cancel the prompt")
	}
	if m.Quit {
		t.Fatal("esc closed the board while a prompt was open")
	}

	// Layer 2: an open detail is closed, and the board stays.
	m, _ = Update(m, KeyMsg{Key: "enter"})
	if !m.Detail {
		t.Fatal("enter did not open the detail")
	}
	m, _ = Update(m, KeyMsg{Key: "esc"})
	if m.Detail {
		t.Fatal("esc did not close the detail")
	}
	if m.Quit {
		t.Fatal("esc closed the board while a detail was open")
	}

	// Layer 3: with nothing open, esc leaves.
	m, _ = Update(m, KeyMsg{Key: "esc"})
	if !m.Quit {
		t.Fatal("esc at the top level did not leave the board")
	}
}

// The footer is the only place a popup can advertise its way out, and
// footerClick turns a label back into its key, so this is the mouse way out
// too. Every state must carry it — a new branch in Verbs() that forgets it
// reintroduces the trap.
func TestVerbsAlwaysAdvertiseAWayOut(t *testing.T) {
	note := &tasks.Note{ID: "N1", Seq: 1, Status: "inbox", Body: "an idea"}
	parked := store.Parked{ID: "P1", Verb: "tasks.create", Subject: "agent:wF:p1"}
	parkedPane := func(m Model) Model { m.Pane = PaneParked; return m }

	for name, m := range map[string]Model{
		"board, review task selected": board(t, task(1, tasks.StatusReview, "a")),
		"board, todo task selected":   board(t, task(1, tasks.StatusTodo, "a")),
		"board, nothing at all":       board(t),
		"notes, note selected":        notesModel(t, []*tasks.Note{note}, nil),
		"notes, nothing selected":     notesModel(t, nil, nil),
		"parked, row selected":        parkedPane(notesModel(t, nil, []store.Parked{parked})),
		"parked, nothing selected":    parkedPane(notesModel(t, nil, nil)),
	} {
		found := false
		for _, v := range m.Verbs() {
			if v.Label == "close" {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s: the footer offers no way out: %+v", name, m.Verbs())
		}
	}
}

// type presses one key per rune, the way an operator fills a prompt.
func type_(m Model, text string) Model {
	for _, r := range text {
		m, _ = Update(m, KeyMsg{Key: string(r)})
	}
	return m
}

// §11.6: the popup is the human's primary surface, so finding a row must not
// mean leaving it for the CLI. The filter is sent to the daemon as the same
// `query` both list verbs take, rather than sifting the rows already loaded:
// one search, one meaning, whichever door asked.
func TestTUIFilterSendsTheQueryToTheDaemon(t *testing.T) {
	m := board(t, task(1, tasks.StatusTodo, "a"))
	m, call := Update(m, KeyMsg{Key: "/"})
	if call != nil || m.Prompt == nil {
		t.Fatalf("/ sent %#v before asking what to search for", call)
	}
	m = type_(m, "api")
	m, call = Update(m, KeyMsg{Key: "enter"})
	if call == nil || call.Verb != "task.list" || call.Args["query"] != "api" {
		t.Fatalf("the filter sent %#v", call)
	}
	if m.BoardFilter != "api" {
		t.Fatalf("the filter was not remembered: %q", m.BoardFilter)
	}
	// It survives the poll, or it would last exactly one refresh.
	if _, call = Update(m, KeyMsg{Key: "r"}); call.Args["query"] != "api" {
		t.Fatalf("a refresh under a filter sent %#v", call)
	}

	// A filter belongs to the board it was typed on. The notes view is a
	// different list of different rows, and silently searching it for what was
	// typed on the board would hide notes with no way to tell why.
	n, _ := Update(m, KeyMsg{Key: "tab"})
	if n.View != ViewNotes {
		t.Fatalf("tab did not reach the notes view")
	}
	_, call = Update(n, KeyMsg{Key: "r"})
	if call == nil || call.Verb != "note.list" {
		t.Fatalf("refresh on the notes view sent %#v", call)
	}
	if _, ok := call.Args["query"]; ok {
		t.Fatalf("the board's filter leaked into the notes view: %#v", call.Args)
	}

	// esc unwinds the filter before it leaves, and says so to the daemon.
	m, call = Update(m, KeyMsg{Key: "esc"})
	if m.Quit {
		t.Fatal("esc closed the board instead of clearing the filter")
	}
	if m.BoardFilter != "" {
		t.Fatalf("esc left the filter at %q", m.BoardFilter)
	}
	if call == nil || call.Verb != "task.list" {
		t.Fatalf("clearing the filter sent %#v, not a re-read", call)
	}
	if _, ok := call.Args["query"]; ok {
		t.Fatalf("the cleared filter was still sent: %#v", call.Args)
	}
	if m, _ = Update(m, KeyMsg{Key: "esc"}); !m.Quit {
		t.Fatal("esc with nothing left open did not leave the board")
	}
}

// §3.2 / §11.6: a popup pane carries no HERDR_PANE_ID, so the operator's board
// is the `human` principal — and these are human verbs. Filing an idea and
// keeping one were the two the popup could not reach: promote and drop were
// there, keep was not, and there was no way to write a note down at all.
func TestTUIHumanWritesFileANoteAndKeepOne(t *testing.T) {
	m := notesModel(t, []*tasks.Note{{ID: "N1", Seq: 1, Status: "inbox", Body: "an idea"}}, nil)
	m2, call := Update(m, KeyMsg{Key: "a"})
	if call != nil || m2.Prompt == nil {
		t.Fatalf("add sent %#v before asking what the note says", call)
	}
	m2, call = Update(m2, KeyMsg{Key: "enter"})
	if call != nil {
		t.Fatalf("an empty note was filed: %#v", call)
	}
	if !strings.HasPrefix(m2.Err, "USAGE") {
		t.Fatalf("an empty body was not refused: %q", m2.Err)
	}
	m2 = type_(m2, "the sweep is quiet")
	m2, call = Update(m2, KeyMsg{Key: "enter"})
	if call == nil || call.Verb != "note.add" || call.Args["body"] != "the sweep is quiet" {
		t.Fatalf("add sent %#v", call)
	}
	if m2.Prompt != nil {
		t.Fatal("the prompt stayed open after it was answered")
	}

	// Keep is the third decision. It needs no reason: it is "yes, not now",
	// where a drop is a rejection and owes one.
	_, call = Update(m, KeyMsg{Key: "K"})
	if call == nil || call.Verb != "note.keep" || call.Args["id"] != "N1" {
		t.Fatalf("keep sent %#v", call)
	}
	if _, call = Update(notesModel(t, nil, nil), KeyMsg{Key: "K"}); call != nil {
		t.Fatalf("keep with nothing selected sent %#v", call)
	}
}

// §11.6 is mouse-first: a verb the footer does not show is a verb the mouse
// cannot reach, so every new key is checked in the footer AND through the
// click that footerClick maps back to it.
func TestVerbsAndFooterClickReachTheNewKeys(t *testing.T) {
	labelled := func(m Model, key string) bool {
		for _, v := range m.Verbs() {
			if v.Key == key {
				return true
			}
		}
		return false
	}
	notes := notesModel(t, []*tasks.Note{{ID: "N1", Seq: 1, Status: "inbox", Body: "an idea"}}, nil)
	for _, key := range []string{"a", "K", "/"} {
		if !labelled(notes, key) {
			t.Fatalf("the notes footer does not offer %q: %+v", key, notes.Verbs())
		}
	}
	if !labelled(board(t, task(1, tasks.StatusTodo, "a")), "/") {
		t.Fatal("the board footer does not offer the filter")
	}
	// Empty boards still need the two verbs that do not act on a selection.
	for _, key := range []string{"a", "/"} {
		if !labelled(notesModel(t, nil, nil), key) {
			t.Fatalf("the empty notes footer does not offer %q", key)
		}
	}

	// Clicking a label runs the key it is drawn with, at the x it is drawn at.
	notes.Width, notes.Height = 80, 24
	at := 0
	for _, v := range notes.Verbs() {
		label := footerLabel(v)
		if v.Key == "K" {
			_, call := Update(notes, MouseMsg{X: at + 2, Y: notes.Height - 1})
			if call == nil || call.Verb != "note.keep" {
				t.Fatalf("clicking %q at x=%d sent %#v", label, at+2, call)
			}
		}
		if v.Key == "a" {
			m2, call := Update(notes, MouseMsg{X: at + 2, Y: notes.Height - 1})
			if call != nil || m2.Prompt == nil {
				t.Fatalf("clicking %q at x=%d sent %#v instead of opening the prompt", label, at+2, call)
			}
		}
		at += len(label)
	}
}

// recorder is a Sender that answers every list verb with an empty document and
// keeps what it was asked, so the runtime's own wiring is testable without a
// daemon, a socket or a terminal (§12.1 layer 1).
type recorder struct {
	got []protocol.Request
	// answers is the document each verb replies with, when a test cares.
	answers map[string]string
}

func (r *recorder) Call(req protocol.Request) (json.RawMessage, error) {
	r.got = append(r.got, req)
	if a, ok := r.answers[req.Verb]; ok {
		return json.RawMessage(a), nil
	}
	return json.RawMessage(`{"tasks":[],"notes":[],"parked":[]}`), nil
}

// The pure model asking for a query is only half of it: the poll is what
// actually reads the board, and a filter the poll dropped would look like it
// worked and then vanish two seconds later. This drives the runtime's load.
func TestTUIFilterSurvivesThePollThatRedrawsTheBoard(t *testing.T) {
	m := New(ViewBoard, "/repo")
	m.BoardFilter, m.NotesFilter = "api", "sweep"
	rec := &recorder{}
	p := &program{model: m, send: rec, base: protocol.Request{Project: "/repo"}}
	if msg := p.load(p.model.Filters())(); msg == nil {
		t.Fatal("the load produced no message")
	}
	want := map[string]any{"task.list": "api", "note.list": "sweep", "parked.list": nil}
	if len(rec.got) != len(want) {
		t.Fatalf("the poll sent %d requests, want %d", len(rec.got), len(want))
	}
	for _, req := range rec.got {
		q, ok := want[req.Verb]
		if !ok {
			t.Fatalf("the poll sent an unexpected verb %q", req.Verb)
		}
		if q == nil {
			if _, sent := req.Args["query"]; sent {
				t.Fatalf("%s was sent a query it does not take: %#v", req.Verb, req.Args)
			}
			continue
		}
		if req.Args["query"] != q {
			t.Fatalf("%s was polled with query %#v, want %q", req.Verb, req.Args["query"], q)
		}
	}
}

// §4.2 / §11.6: the popup takes its project from the focused pane, so opening
// it from the wrong pane gives a board that is empty and correct. Nothing
// distinguished that from a board that lost its data, and it was read as data
// loss twice. The empty state says which project answered, and where the work
// is when it is somewhere else.
func TestTUIEmptyBoardSaysWhichProjectItIsEmptyFor(t *testing.T) {
	for name, m := range map[string]Model{
		"board": board(t),
		"notes": notesModel(t, nil, nil),
	} {
		out := Render(m, 0)
		if !strings.Contains(out, "/repo") {
			t.Errorf("%s: the empty view does not name its project:\n%s", name, out)
		}
		if !strings.Contains(out, "focused pane") {
			t.Errorf("%s: the empty view does not say what set the scope:\n%s", name, out)
		}
		if strings.Contains(out, "other project") {
			t.Errorf("%s: an empty store still pointed somewhere else:\n%s", name, out)
		}
	}

	// With work elsewhere, the empty state says so and how much.
	elsewhere := board(t)
	elsewhere.BoardElsewhere = 3
	if out := Render(elsewhere, 0); !strings.Contains(out, "3") || !strings.Contains(out, "other project") {
		t.Fatalf("the board did not say where the work is:\n%s", out)
	}
	notes := notesModel(t, nil, nil)
	notes.NotesElsewhere = 2
	if out := Render(notes, 0); !strings.Contains(out, "2") || !strings.Contains(out, "other project") {
		t.Fatalf("the notes board did not say where the notes are:\n%s", out)
	}

	// A board with rows explains nothing: the operator can see what it holds.
	full := board(t, task(1, tasks.StatusTodo, "a"))
	full.BoardElsewhere = 3
	if out := Render(full, 0); strings.Contains(out, "focused pane") {
		t.Fatalf("a board with work on it carried the empty state:\n%s", out)
	}
	withNote := notesModel(t, []*tasks.Note{{ID: "N1", Seq: 1, Status: "inbox", Body: "an idea"}}, nil)
	if out := Render(withNote, 0); strings.Contains(out, "focused pane") {
		t.Fatalf("a notes board with a note on it carried the empty state:\n%s", out)
	}
}

// The poll reads three lists into one model, and two of them answer with a
// project and a count of their own. Reading them into one shared field would
// make the notes board's answer overwrite the task board's.
func TestTUIEmptyCountsArriveFromTheirOwnList(t *testing.T) {
	rec := &recorder{answers: map[string]string{
		"task.list":   `{"tasks":[],"count":0,"project":"/repo","elsewhere":3}`,
		"note.list":   `{"notes":[],"count":0,"project":"/repo","elsewhere":7}`,
		"parked.list": `{"parked":[]}`,
	}}
	p := &program{model: New(ViewBoard, "/repo"), send: rec, base: protocol.Request{Project: "/repo"}}
	msg := p.load(p.model.Filters())()
	data, ok := msg.(DataMsg)
	if !ok {
		t.Fatalf("the poll produced %T: %v", msg, msg)
	}
	if data.BoardElsewhere != 3 {
		t.Fatalf("the board's count is %d, want 3", data.BoardElsewhere)
	}
	if data.NotesElsewhere != 7 {
		t.Fatalf("the notes count is %d, want 7 — the two lists share a key name", data.NotesElsewhere)
	}
}
