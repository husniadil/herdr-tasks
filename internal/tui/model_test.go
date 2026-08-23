package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/husniadil/herdr-tasks/internal/codes"
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
//
// The board is DEEPER than the screen on purpose. Built on one card, every row
// this sweeps is out of bounds for the trivial reason that the column holds
// one element — which tests nothing about what was drawn. With forty cards the
// rows below the last drawn one are still empty on screen but are real
// elements of the column, and that is the case that matters.
func TestAClickOnEmptySpaceRunsNothing(t *testing.T) {
	deep := []*tasks.Task{}
	for i := 1; i <= 40; i++ {
		deep = append(deep, task(int64(i), tasks.StatusTodo, "todo"))
	}
	m := board(t, append(deep, task(41, tasks.StatusReview, "a"))...)
	m.Width, m.Height = 80, 24
	for y := firstCard + 1; y < m.Height-footerRows; y++ {
		got, call := Update(m, MouseMsg{X: 2, Y: y})
		if call != nil {
			t.Fatalf("a click on empty row %d ran %s", y, call.Verb)
		}
		if drawnAt(t, m, y) == "" && got.Detail {
			t.Fatalf("a click on row %d, where nothing is drawn, opened card #%d",
				y, m.Column(got.Col)[got.Row[got.Col]].Seq)
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

// drawnAt is what Render actually put on screen row y, trimmed. "" means the
// row is blank — either padding below the body or the gap above the footer.
func drawnAt(t *testing.T, m Model, y int) string {
	t.Helper()
	lines := screen(m)
	if y < 0 || y >= len(lines) {
		return ""
	}
	return strings.TrimSpace(lines[y])
}

// §11.6: the complement, so "bound the click" cannot be implemented as
// "refuse everything". Every row that IS drawn selects the card drawn on it,
// including the first and the last.
func TestAClickOnADrawnCardSelectsThatCard(t *testing.T) {
	deep := []*tasks.Task{}
	for i := 1; i <= 40; i++ {
		deep = append(deep, task(int64(i), tasks.StatusTodo, "todo"))
	}
	m := board(t, deep...)
	m.Width, m.Height = 80, 24

	drawn := []int{}
	for y := firstCard; y < m.Height-footerRows; y++ {
		if drawnAt(t, m, y) != "" {
			drawn = append(drawn, y)
		}
	}
	if len(drawn) < 2 {
		t.Fatalf("the board drew %d card rows; this test needs a deep board", len(drawn))
	}
	for _, y := range []int{drawn[0], drawn[len(drawn)-1]} {
		got, call := Update(m, MouseMsg{X: 2, Y: y})
		if call != nil {
			t.Fatalf("clicking a drawn card ran %s", call.Verb)
		}
		if !got.Detail {
			t.Fatalf("clicking drawn row %d selected nothing", y)
		}
		want := RowAt(y)
		if got.Col != 0 || got.Row[0] != want {
			t.Fatalf("clicking row %d selected column %d row %d, want column 0 row %d",
				y, got.Col, got.Row[got.Col], want)
		}
		// And it is the card whose text is on that row.
		if seq := got.Column(got.Col)[got.Row[got.Col]].Seq; !strings.Contains(drawnAt(t, m, y), fmt.Sprintf("#%d ", seq)) {
			t.Fatalf("row %d draws %q but the click selected #%d", y, drawnAt(t, m, y), seq)
		}
	}
}

// §11.6: with the detail panel open the body shrinks to what is left, so most
// of the column is no longer on screen — and a click there was opening cards
// the operator could not see, behind the panel they were reading.
func TestAClickBehindTheDetailPanelSelectsNothing(t *testing.T) {
	deep := []*tasks.Task{}
	for i := 1; i <= 40; i++ {
		deep = append(deep, task(int64(i), tasks.StatusTodo, "a todo with enough words to wrap the panel"))
	}
	m := board(t, deep...)
	m.Width, m.Height = 80, 24
	m.Detail = true
	m.Col, m.Row[0] = 0, 0

	for y := firstCard; y < m.Height-footerRows; y++ {
		if drawnAt(t, m, y) != "" {
			continue
		}
		got, call := Update(m, MouseMsg{X: 2, Y: y})
		if call != nil {
			t.Fatalf("a click on row %d ran %s", y, call.Verb)
		}
		if got.Row[0] != 0 {
			t.Fatalf("a click on row %d, where no card is drawn, moved the selection to #%d",
				y, got.Column(0)[got.Row[0]].Seq)
		}
	}
}

// §11.6: the notes view is clamped by the same arithmetic, in both panes.
func TestAClickPastTheDrawnNotesSelectsNothing(t *testing.T) {
	notes := []*tasks.Note{}
	parked := []store.Parked{}
	for i := 1; i <= 40; i++ {
		notes = append(notes, &tasks.Note{ID: fmt.Sprintf("N%d", i), Seq: int64(i), Status: "inbox", Body: "an idea"})
		parked = append(parked, store.Parked{ID: fmt.Sprintf("P%d", i), Verb: "tasks.approve", Subject: "agent:wF:p1"})
	}
	m := notesModel(t, notes, parked)
	m.Width, m.Height = 80, 24

	for _, x := range []int{2, 60} {
		for y := firstCard; y < m.Height-footerRows; y++ {
			if drawnAt(t, m, y) != "" {
				continue
			}
			got, call := Update(m, MouseMsg{X: x, Y: y})
			if call != nil {
				t.Fatalf("x=%d: a click on row %d ran %s", x, y, call.Verb)
			}
			if got.Detail {
				t.Fatalf("x=%d: a click on row %d, where nothing is drawn, opened a detail panel", x, y)
			}
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

// screen is what bubbletea will do with a view: it splits on "\n" and, when
// there are more lines than the terminal has rows, keeps only the LAST rows of
// them (standard_renderer.go:186 in v1.3.10, which drops from the top because
// it cannot move the cursor into scrollback). So the header is the first thing
// sacrificed, and "fits" means at most Height elements — not Height newlines.
func screen(m Model) []string { return strings.Split(Render(m, 0), "\n") }

// states is every shape the popup can be in that has ever changed its height.
func states(t *testing.T, height int) map[string]Model {
	t.Helper()
	base := func() Model {
		m := board(t, task(1, tasks.StatusReview, "a task"), task(2, tasks.StatusTodo, "another"))
		m.Width, m.Height = 80, height
		return m
	}
	filtered := base()
	filtered.BoardFilter, filtered.Status = "api", "filter: api"
	erred := base()
	erred.Prompt = &Prompt{Label: "Find in board"}
	erred.Err = "USAGE: Find in board"
	prompt := base()
	prompt.Prompt = &Prompt{Label: "Find in board", Value: "api"}
	detail := base()
	detail.Detail = true
	both := base()
	both.Detail, both.BoardFilter, both.Status = true, "api", "filter: api"
	return map[string]Model{
		"at rest": base(), "filter applied": filtered, "err showing": erred,
		"prompt open": prompt, "detail open": detail, "filter and detail": both,
	}
}

// §11.6: the header names the project the board is scoped to, which is the one
// thing an operator cannot work out from an empty board — and it is line 0, so
// it is the first line lost when the document is one row too tall. It was:
// the status line was written after the footer, outside the budget, so any
// applied filter or error pushed the header off the screen.
func TestRenderFitsTheScreenSoTheHeaderSurvives(t *testing.T) {
	for _, height := range []int{10, 24, 40} {
		for name, m := range states(t, height) {
			lines := screen(m)
			if len(lines) > height {
				t.Errorf("h=%d %s: %d lines for %d rows — bubbletea drops the top %d",
					height, name, len(lines), height, len(lines)-height)
			}
			head := lines[0]
			for _, want := range []string{"/repo", "board", "notes"} {
				if !strings.Contains(head, want) {
					t.Errorf("h=%d %s: line 0 does not carry %q: %q", height, name, want, head)
				}
			}
		}
	}
}

// When there is more to show than there are rows, the rows that go are the
// ones the operator can get back by scrolling a list — never the header that
// says which project this is, and never the footer that says how to leave.
func TestRenderClampsTheBodyNotTheChrome(t *testing.T) {
	deep := []*tasks.Task{}
	for i := 1; i <= 40; i++ {
		deep = append(deep, task(int64(i), tasks.StatusTodo, "a task"))
	}
	manyNotes := []*tasks.Note{}
	for i := 1; i <= 40; i++ {
		manyNotes = append(manyNotes, &tasks.Note{ID: "N", Seq: int64(i), Status: "inbox", Body: "an idea"})
	}
	long := notesModel(t, []*tasks.Note{{ID: "N1", Seq: 1, Status: "inbox", Body: strings.Repeat("x", 20_000)}}, nil)
	long.Detail = true

	for name, m := range map[string]Model{
		"40 tasks":             board(t, deep...),
		"40 notes":             notesModel(t, manyNotes, nil),
		"detail on a 20k body": long,
	} {
		m.Width, m.Height = 80, 24
		lines := screen(m)
		if len(lines) > 24 {
			t.Errorf("%s: %d lines for 24 rows", name, len(lines))
		}
		if !strings.Contains(lines[0], "/repo") {
			t.Errorf("%s: the header went instead of the body: %q", name, lines[0])
		}
		if !strings.Contains(Render(m, 0), "[esc] close") {
			t.Errorf("%s: the way out went instead of the body", name)
		}
		// Every line has to fit the width too, or the renderer cuts it and
		// the text past the cut is simply gone. Measured in CELLS: a rune is
		// not a column, and counting runes here is what let a wide-rune line
		// through at nearly twice the pane's width.
		for i, ln := range lines {
			if cells(ln) > m.Width {
				t.Errorf("%s: line %d is %d cells wide, which the renderer will cut: %.40q…",
					name, i, cells(ln), ln)
			}
		}
	}

	// What is hidden is still counted: the heading says how many there are.
	full := board(t, deep...)
	full.Width, full.Height = 80, 24
	out := Render(full, 0)
	if !strings.Contains(out, "todo (40)") {
		t.Fatalf("the column heading no longer reports the true total:\n%s", out)
	}
	if drawn := strings.Count(out, "#40 "); drawn != 0 {
		t.Fatalf("40 cards were drawn into 24 rows")
	}
}

// §11.6 is mouse-first, and click() hit-tests the DOCUMENT with fixed row
// constants while the operator clicks the SCREEN. If the two ever disagree, a
// click selects the row above the one it landed on and nothing says so.
func TestMouseRowsStayWhereClickHitTestsThem(t *testing.T) {
	for _, height := range []int{10, 24, 40} {
		for name, m := range states(t, height) {
			lines := screen(m)
			if !strings.Contains(lines[headingRow], "todo") {
				t.Errorf("h=%d %s: the column headings are not on row %d: %q", height, name, headingRow, lines[headingRow])
			}
			if !strings.Contains(lines[firstCard], "#1") {
				t.Errorf("h=%d %s: the first card is not on row %d: %q", height, name, firstCard, lines[firstCard])
			}
			at := -1
			for i, ln := range lines {
				if strings.Contains(ln, "[esc] close") {
					at = i
				}
			}
			if at < height-footerRows {
				t.Errorf("h=%d %s: the footer is on row %d, outside the %d rows click() treats as the footer",
					height, name, at, footerRows)
			}
		}
	}
}

// The filter's status line is what pushed the header off, and it also lied:
// an emptied filter left the words "filter:" with nothing after them, and esc
// leaves a term showing over a board that is not filtered by it.
func TestFilterStatusSaysWhatIsTrue(t *testing.T) {
	m := board(t, task(1, tasks.StatusTodo, "a"))
	m.Width, m.Height = 80, 24

	m, _ = Update(m, KeyMsg{Key: "/"})
	m = type_(m, "api")
	m, _ = Update(m, KeyMsg{Key: "enter"})
	if !strings.Contains(m.Status, "api") {
		t.Fatalf("an applied filter does not say so: %q", m.Status)
	}

	// Submitting an empty prompt drops the filter, so there is nothing to say.
	m, _ = Update(m, KeyMsg{Key: "/"})
	for range "api" {
		m, _ = Update(m, KeyMsg{Key: "backspace"})
	}
	m, _ = Update(m, KeyMsg{Key: "enter"})
	if m.Status != "" {
		t.Fatalf("an emptied filter left a status behind: %q", m.Status)
	}
	if m.BoardFilter != "" {
		t.Fatalf("an emptied filter is still applied: %q", m.BoardFilter)
	}

	// esc is the other way out of a filter, and it has to clear the same thing.
	m, _ = Update(m, KeyMsg{Key: "/"})
	m = type_(m, "api")
	m, _ = Update(m, KeyMsg{Key: "enter"})
	m, _ = Update(m, KeyMsg{Key: "esc"})
	if m.Status != "" {
		t.Fatalf("esc cleared the filter but left its status: %q", m.Status)
	}
	if m.Quit {
		t.Fatal("esc left the board instead of clearing the filter")
	}
	for _, s := range []string{"", "filter: api"} {
		x := board(t, task(1, tasks.StatusTodo, "a"))
		x.Width, x.Height, x.Status = 80, 24, s
		if lines := screen(x); len(lines) > 24 || !strings.Contains(lines[0], "/repo") {
			t.Fatalf("status %q: %d lines, line 0 = %q", s, len(lines), lines[0])
		}
	}
}

// promptOf opens a prompt and returns the model with it open.
func promptOf(t *testing.T, label, value string) Model {
	t.Helper()
	m := board(t, task(1, tasks.StatusTodo, "a"))
	m.Width, m.Height = 80, 24
	m.Prompt = newPrompt(Prompt{Label: label, Value: value, call: Call{Verb: "task.list", Args: map[string]any{}}, field: "query"})
	return m
}

// §11.6: the prompt was append-and-backspace only, so fixing the first word of
// what you typed meant deleting everything after it. A cursor is the whole of
// the difference, and every prompt shares this one — filter, note add, the
// verdict and drop reasons, reject feedback.
func TestPromptEditingMovesAndInsertsAtTheCursor(t *testing.T) {
	m := promptOf(t, "Find in board", "abcdef")
	if m.Prompt.Cursor != 6 {
		t.Fatalf("a pre-filled prompt starts the cursor at %d, want the end", m.Prompt.Cursor)
	}
	// left and right walk, and stop at each end rather than wrapping or
	// running off into a negative index.
	for i := 0; i < 8; i++ {
		m, _ = Update(m, KeyMsg{Key: "left"})
	}
	if m.Prompt.Cursor != 0 {
		t.Fatalf("left ran to %d, want 0", m.Prompt.Cursor)
	}
	for i := 0; i < 8; i++ {
		m, _ = Update(m, KeyMsg{Key: "right"})
	}
	if m.Prompt.Cursor != 6 {
		t.Fatalf("right ran to %d, want the end at 6", m.Prompt.Cursor)
	}
	for _, k := range []string{"home", "ctrl+a"} {
		m, _ = Update(m, KeyMsg{Key: "end"})
		m, _ = Update(m, KeyMsg{Key: k})
		if m.Prompt.Cursor != 0 {
			t.Fatalf("%s left the cursor at %d", k, m.Prompt.Cursor)
		}
	}
	for _, k := range []string{"end", "ctrl+e"} {
		m, _ = Update(m, KeyMsg{Key: "home"})
		m, _ = Update(m, KeyMsg{Key: k})
		if m.Prompt.Cursor != 6 {
			t.Fatalf("%s left the cursor at %d", k, m.Prompt.Cursor)
		}
	}

	// A rune goes in AT the cursor, which is the point of the whole thing.
	m, _ = Update(m, KeyMsg{Key: "home"})
	for _, k := range []string{"X", "Y"} {
		m, _ = Update(m, KeyMsg{Key: k})
	}
	if m.Prompt.Value != "XYabcdef" || m.Prompt.Cursor != 2 {
		t.Fatalf("typing at the start gave %q with the cursor at %d", m.Prompt.Value, m.Prompt.Cursor)
	}
	m, _ = Update(m, KeyMsg{Key: "space"})
	if m.Prompt.Value != "XY abcdef" || m.Prompt.Cursor != 3 {
		t.Fatalf("space gave %q with the cursor at %d", m.Prompt.Value, m.Prompt.Cursor)
	}

	// backspace takes the rune BEFORE the cursor and moves back; delete takes
	// the one AT it and stays put.
	m, _ = Update(m, KeyMsg{Key: "backspace"})
	if m.Prompt.Value != "XYabcdef" || m.Prompt.Cursor != 2 {
		t.Fatalf("backspace gave %q with the cursor at %d", m.Prompt.Value, m.Prompt.Cursor)
	}
	m, _ = Update(m, KeyMsg{Key: "delete"})
	if m.Prompt.Value != "XYbcdef" || m.Prompt.Cursor != 2 {
		t.Fatalf("delete gave %q with the cursor at %d", m.Prompt.Value, m.Prompt.Cursor)
	}
	m, _ = Update(m, KeyMsg{Key: "home"})
	m, _ = Update(m, KeyMsg{Key: "backspace"})
	if m.Prompt.Value != "XYbcdef" || m.Prompt.Cursor != 0 {
		t.Fatalf("backspace at the start changed something: %q at %d", m.Prompt.Value, m.Prompt.Cursor)
	}
	m, _ = Update(m, KeyMsg{Key: "end"})
	m, _ = Update(m, KeyMsg{Key: "delete"})
	if m.Prompt.Value != "XYbcdef" {
		t.Fatalf("delete at the end changed something: %q", m.Prompt.Value)
	}

	// Submitting sends the whole value, not the part before the cursor.
	m, _ = Update(m, KeyMsg{Key: "home"})
	m, call := Update(m, KeyMsg{Key: "enter"})
	if call == nil || call.Args["query"] != "XYbcdef" {
		t.Fatalf("enter with the cursor at the start sent %#v", call)
	}
}

// §11.6: task 15 clamps every drawn line to the pane, which made a defect
// visible that was there all along — a value longer than the line is typed
// blind, because what is dropped is the end, which is where the typing is.
// The window follows the cursor instead.
func TestPromptWindowFollowsTheCursor(t *testing.T) {
	value := make([]rune, 300)
	for i := range value {
		value[i] = rune('a' + i%26)
	}
	val := string(value)

	for _, width := range []int{40, 80} {
		for _, cursor := range []int{0, 150, 300} {
			m := promptOf(t, "Find in board", val)
			m.Width = width
			m.Prompt.Cursor = cursor

			line := ""
			for _, ln := range strings.Split(Render(m, 0), "\n") {
				if strings.Contains(ln, "Find in board:") {
					line = ln
				}
			}
			if line == "" {
				t.Fatalf("w=%d c=%d: the prompt was not drawn", width, cursor)
			}
			if cells(line) > width {
				t.Errorf("w=%d c=%d: the prompt line is %d cells", width, cursor, cells(line))
			}
			// The cursor marker has to be on screen, with the value's own
			// neighbours around it — that is what "you can see what you are
			// typing" means.
			r := []rune(line)
			at := -1
			for i, c := range r {
				if c == promptCursor {
					at = i
				}
			}
			if at < 0 {
				t.Fatalf("w=%d c=%d: no cursor in %q", width, cursor, line)
			}
			for k := 1; k <= 3; k++ {
				if cursor-k >= 0 && at-k >= 0 {
					if got, want := r[at-k], value[cursor-k]; got != want {
						t.Errorf("w=%d c=%d: %d before the cursor is %q, want %q", width, cursor, k, got, want)
					}
				}
				if cursor+k-1 < len(value) && at+k < len(r) {
					if got, want := r[at+k], value[cursor+k-1]; got != want {
						t.Errorf("w=%d c=%d: %d after the cursor is %q, want %q", width, cursor, k, got, want)
					}
				}
			}
		}
	}
}

// §11.6: the popup can decide a note and now it can fix one. The model asks
// for an editor and stops there — running a process is the runtime's job, and
// Update stays pure, which is the only reason any of this is testable without
// a terminal.
func TestEditKeyAsksForAnEditorOnlyWhileTheNoteStillMoves(t *testing.T) {
	live := &tasks.Note{ID: "N1", Seq: 13, Status: tasks.NoteInbox, Body: "the header disappears"}
	m := notesModel(t, []*tasks.Note{live}, nil)
	m2, call := Update(m, KeyMsg{Key: "e"})
	if call != nil {
		t.Fatalf("e sent a daemon call by itself: %#v", call)
	}
	if m2.Edit == nil {
		t.Fatal("e asked for nothing")
	}
	if m2.Edit.NoteID != "N1" || m2.Edit.Body != live.Body || m2.Edit.Seq != 13 {
		t.Fatalf("the edit intent is %+v", m2.Edit)
	}

	// Every state the daemon still accepts an edit in.
	for _, st := range []tasks.NoteStatus{tasks.NoteInbox, tasks.NoteDiscussing, tasks.NoteNeedsInput, tasks.NoteProposed} {
		n := notesModel(t, []*tasks.Note{{ID: "N1", Seq: 1, Status: st, Body: "b"}}, nil)
		if got, _ := Update(n, KeyMsg{Key: "e"}); got.Edit == nil {
			t.Errorf("a note in %s could not be edited", st)
		}
	}
	// And the three it does not: a decided note's wording is what was decided
	// on, so the popup says so rather than opening an editor on a refusal.
	for _, st := range []tasks.NoteStatus{tasks.NoteKept, tasks.NoteTask, tasks.NoteDropped} {
		n := notesModel(t, []*tasks.Note{{ID: "N1", Seq: 1, Status: st, Body: "b"}}, nil)
		got, call := Update(n, KeyMsg{Key: "e"})
		if got.Edit != nil || call != nil {
			t.Errorf("a %s note opened an editor", st)
		}
		if !strings.Contains(got.Status, string(st)) {
			t.Errorf("a %s note said %q, which does not say why", st, got.Status)
		}
	}
	// Nothing selected, nothing to edit.
	if got, call := Update(notesModel(t, nil, nil), KeyMsg{Key: "e"}); got.Edit != nil || call != nil {
		t.Fatalf("e with no note selected asked for %+v", got.Edit)
	}
	// The board's 'e' is not this: it belongs to the notes view only.
	if got, _ := Update(board(t, task(1, tasks.StatusTodo, "a")), KeyMsg{Key: "e"}); got.Edit != nil {
		t.Fatal("e on the board asked for an editor")
	}
}

// Which editor, and what to do when there is none. Falling back to vi is the
// tempting answer and the wrong one: a popup that drops an operator into a
// modal editor they do not know is the trap task 4 exists to have closed.
func TestEditorCommandPrefersVisualAndRefusesWhenThereIsNone(t *testing.T) {
	env := func(pairs map[string]string) func(string) string {
		return func(k string) string { return pairs[k] }
	}
	for name, tc := range map[string]struct {
		vars map[string]string
		want string
	}{
		"VISUAL wins":     {map[string]string{"VISUAL": "code -w", "EDITOR": "nano"}, "code -w"},
		"EDITOR alone":    {map[string]string{"EDITOR": "nano"}, "nano"},
		"VISUAL alone":    {map[string]string{"VISUAL": "vim"}, "vim"},
		"blanks are none": {map[string]string{"VISUAL": "  ", "EDITOR": ""}, ""},
		"neither":         {map[string]string{}, ""},
	} {
		argv, err := editorCommand(env(tc.vars), "/tmp/x.md")
		if tc.want == "" {
			if err == nil {
				t.Errorf("%s: built %v instead of refusing", name, argv)
				continue
			}
			if argv != nil {
				t.Errorf("%s: refused AND built %v", name, argv)
			}
			for _, v := range []string{"VISUAL", "EDITOR"} {
				if !strings.Contains(err.Error(), v) {
					t.Errorf("%s: the refusal does not name %s: %v", name, v, err)
				}
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		joined := strings.Join(argv, " ")
		if !strings.Contains(joined, tc.want) {
			t.Errorf("%s: argv %v does not run %q", name, argv, tc.want)
		}
		if !strings.Contains(joined, "/tmp/x.md") {
			t.Errorf("%s: argv %v does not name the file", name, argv)
		}
	}
}

// The whole trip, with the editor faked: what the operator typed has to come
// back, an untouched body must not be written for nothing, and the scratch
// file is only thrown away once the daemon has the text.
func TestEditRoundTripKeepsWhatTheOperatorWrote(t *testing.T) {
	e := Edit{NoteID: "N1", Seq: 13, Body: "the header disappears"}

	// The body reaches the editor as a file.
	path, err := startEdit(e)
	if err != nil {
		t.Fatalf("startEdit: %v", err)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != e.Body+"\n" {
		t.Fatalf("the editor was given %q (%v)", string(b), err)
	}

	// Unchanged: nothing to send.
	call, err := finishEdit(e, path, nil)
	if err != nil || call != nil {
		t.Fatalf("an untouched body sent %#v (%v)", call, err)
	}

	// Changed: note.update with the new body, and the trailing newline an
	// editor adds is not a change the operator made.
	if err := os.WriteFile(path, []byte("the header stays put\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	call, err = finishEdit(e, path, nil)
	if err != nil {
		t.Fatalf("finishEdit: %v", err)
	}
	if call == nil || call.Verb != "note.update" || call.Args["id"] != "N1" || call.Args["body"] != "the header stays put" {
		t.Fatalf("the edit sent %#v", call)
	}

	// An editor that failed sends nothing and says so.
	if call, err := finishEdit(e, path, errors.New("exit status 1")); call != nil || err == nil {
		t.Fatalf("a failed editor sent %#v (%v)", call, err)
	}
	// An emptied body is not an edit: §5.9 refuses it daemon-side, and losing
	// a note to a stray ctrl+A is not what the key is for.
	if err := os.WriteFile(path, []byte("   \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if call, err := finishEdit(e, path, nil); call != nil || err == nil {
		t.Fatalf("an emptied body sent %#v (%v)", call, err)
	}

	// The scratch file survives a failed write and its path is in the message,
	// because what it holds is something the operator just wrote.
	if err := os.WriteFile(path, []byte("kept\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	msg := afterUpdate(path, &codes.Error{Code: codes.Conflict, Message: "note moved"})
	em, ok := msg.(ErrMsg)
	if !ok {
		t.Fatalf("a failed update produced %T", msg)
	}
	if !strings.Contains(em.Message, path) {
		t.Fatalf("the failure does not say where the text is: %q", em.Message)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the operator's text was deleted after a failed update: %v", err)
	}
	// And it goes once the daemon has it.
	if msg := afterUpdate(path, nil); func() bool { _, ok := msg.(DoneMsg); return !ok }() {
		t.Fatalf("a successful update produced %T", msg)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the scratch file outlived the edit: %v", err)
	}
}

// keyTable pulls the first column out of README's popup key table, which is
// the one place a reader learns what to press once the popup is open.
func keyTable(t *testing.T) map[string]bool {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	keys, inTable := map[string]bool{}, false
	for _, line := range strings.Split(string(body), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			inTable = false
			continue
		}
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		first := strings.Trim(strings.TrimSpace(cells[0]), "`")
		if strings.EqualFold(first, "key") {
			inTable = true
			continue
		}
		if !inTable || strings.HasPrefix(first, "-") {
			continue
		}
		keys[first] = true
	}
	return keys
}

// §11.6: the popup is mouse-first, and the footer is where it advertises what
// it can do — but the footer is only visible once the popup is open, and a
// reader deciding whether to bind a key at all is reading the README. So the
// two are checked against each other, both ways: a key the TUI grew without a
// line in the table fails, and a line for a key the TUI does not have fails
// just as loudly.
func TestDocsKeysMatchTheFooter(t *testing.T) {
	note := &tasks.Note{ID: "N1", Seq: 1, Status: tasks.NoteInbox, Body: "an idea"}
	parked := store.Parked{ID: "P1", Verb: "tasks.create", Subject: "agent:wF:p1"}
	parkedPane := func(m Model) Model { m.Pane = PaneParked; return m }

	advertised := map[string]bool{}
	for _, m := range []Model{
		board(t, task(1, tasks.StatusReview, "a")),
		board(t, task(1, tasks.StatusTodo, "a")),
		board(t),
		notesModel(t, []*tasks.Note{note}, nil),
		notesModel(t, nil, nil),
		parkedPane(notesModel(t, nil, []store.Parked{parked})),
		parkedPane(notesModel(t, nil, nil)),
	} {
		for _, v := range m.Verbs() {
			advertised[v.Key] = true
		}
	}
	documented := keyTable(t)
	if len(documented) == 0 {
		t.Fatal("README has no popup key table: the popup half of the plugin is invisible to a reader")
	}
	for key := range advertised {
		if !documented[key] {
			t.Errorf("the footer offers %q and README does not mention it", key)
		}
	}
	for key := range documented {
		if !advertised[key] {
			t.Errorf("README documents %q, which no footer offers", key)
		}
	}
}

// cjk is a run of double-width characters: n runes, 2n cells. It is the shape
// that breaks any layout that counts characters instead of columns.
func cjk(n int) string { return strings.Repeat("状", n) }

// §11.6: a terminal measures in columns. bubbletea truncates every line it
// writes at the terminal width using its own display-width function, so a
// layout that clamps by rune count hands it lines it will silently cut — the
// text is gone and nothing on screen says so.
func TestEveryRenderedLineFitsTheWidthInCells(t *testing.T) {
	wide := board(t, task(1, tasks.StatusTodo, cjk(30)))
	notes := notesModel(t, []*tasks.Note{{ID: "N1", Seq: 1, Status: "inbox", Body: cjk(40)}}, nil)
	deep := []*tasks.Task{}
	for i := 1; i <= 40; i++ {
		deep = append(deep, task(int64(i), tasks.StatusTodo, cjk(30)))
	}

	for name, m := range map[string]Model{
		"a wide title":     wide,
		"a wide note":      notes,
		"forty wide cards": board(t, deep...),
	} {
		m.Width, m.Height = 80, 24
		for i, ln := range screen(m) {
			if cells(ln) > m.Width {
				t.Errorf("%s: line %d is %d cells wide (%d runes), which the renderer will cut: %.30q…",
					name, i, cells(ln), len([]rune(ln)), ln)
			}
		}
	}
}

// §11.6: the detail panel is where a note is READ. A body wrapped by rune
// count produces lines the renderer cuts at half their length, so a third of
// the text is on no line at all — the worst kind of loss, because the panel
// looks complete.
func TestDetailWrapsByCellsAndDropsNothing(t *testing.T) {
	body := cjk(60)
	lines := wrapTo(body, 80)
	for i, ln := range lines {
		if cells(ln) > 80 {
			t.Errorf("wrapped line %d is %d cells wide", i, cells(ln))
		}
	}
	if rejoined := strings.Join(lines, ""); rejoined != body {
		t.Errorf("wrapping dropped text: %d runes back from %d", len([]rune(rejoined)), len([]rune(body)))
	}

	// And through Render, which is what the operator actually sees.
	m := notesModel(t, []*tasks.Note{{ID: "N1", Seq: 1, Status: "inbox", Body: body}}, nil)
	m.Width, m.Height = 80, 24
	m.Detail = true
	shown := strings.Join(screen(m), "")
	if !strings.Contains(shown, cjk(30)) {
		t.Errorf("the detail panel lost the body: %q", shown)
	}
}

// §11.6: the board is two columns, and the right one starts where the layout
// says it starts. Padding by rune count pushes it right by one cell per wide
// character, and the renderer then cuts its title off the screen.
func TestTheRightColumnStartsAtTheSameCellWhateverTheLeftHolds(t *testing.T) {
	at := func(left string) (int, string) {
		t.Helper()
		m := board(t, task(1, tasks.StatusTodo, left), task(2, tasks.StatusReview, "review me"))
		m.Width, m.Height = 80, 24
		for _, ln := range screen(m) {
			if i := strings.Index(ln, "#2"); i >= 0 {
				return cells(ln[:i]), ln
			}
		}
		t.Fatalf("the review card was not drawn for %q", left)
		return 0, ""
	}
	plain, _ := at("an ascii title")
	wide, line := at(cjk(30))
	if plain != wide {
		t.Errorf("the right column starts at cell %d with a wide left column, %d with an ascii one", wide, plain)
	}
	if !strings.Contains(line, "review me") {
		t.Errorf("the right column's title was cut: %q", line)
	}
}

// §11.6: the prompt stays one row and keeps the cursor in view when what is
// typed is double-width. A prompt that grew to two rows would push the header
// off the screen — the regression task 15 closed.
func TestThePromptFitsInCellsWhileTypingWideRunes(t *testing.T) {
	for _, height := range []int{10, 24, 40} {
		for _, cursor := range []int{0, 45, 90} {
			m := promptOf(t, "Find in board", cjk(90))
			m.Width, m.Height = 80, height
			m.Prompt.Cursor = cursor

			lines := screen(m)
			if len(lines) != height {
				t.Fatalf("h=%d c=%d: %d lines for %d rows", height, cursor, len(lines), height)
			}
			if !strings.Contains(lines[0], "/repo") {
				t.Fatalf("h=%d c=%d: the header went: %q", height, cursor, lines[0])
			}
			prompt := ""
			for _, ln := range lines {
				if strings.ContainsRune(ln, promptCursor) {
					prompt = ln
				}
			}
			if prompt == "" {
				t.Fatalf("h=%d c=%d: the cursor is not on screen", height, cursor)
			}
			for i, ln := range lines {
				if cells(ln) > m.Width {
					t.Fatalf("h=%d c=%d: line %d is %d cells wide: %.30q…", height, cursor, i, cells(ln), ln)
				}
			}
		}
	}
}

// §11.6: Render and click() must divide the screen the same way. The proof is
// in two halves, and it needs both: that the frame is what Render actually
// draws, and that the frame is what click() bounds against. Drop either and
// the two can drift back apart — which is what let a click on a blank row open
// a card that was never on screen.
func TestClickAndRenderAgreeOnWhatWasDrawn(t *testing.T) {
	deep := []*tasks.Task{}
	for i := 1; i <= 40; i++ {
		deep = append(deep, task(int64(i), tasks.StatusTodo, "a todo with enough words to wrap a panel"))
	}
	notes := []*tasks.Note{}
	parked := []store.Parked{}
	for i := 1; i <= 40; i++ {
		notes = append(notes, &tasks.Note{ID: fmt.Sprintf("N%d", i), Seq: int64(i), Status: "inbox", Body: "an idea"})
		parked = append(parked, store.Parked{ID: fmt.Sprintf("P%d", i), Verb: "tasks.approve", Subject: "agent:wF:p1"})
	}

	for _, height := range []int{10, 24, 40} {
		withDetail := board(t, deep...)
		withDetail.Detail = true
		withPrompt := board(t, deep...)
		withPrompt.Prompt = &Prompt{Label: "Find in board", Value: "api"}
		shallow := board(t, task(1, tasks.StatusTodo, "only one"))
		for name, m := range map[string]Model{
			"deep board":              board(t, deep...),
			"deep board, detail open": withDetail,
			"deep board, prompt open": withPrompt,
			"deep notes":              notesModel(t, notes, parked),
			"a board with one card":   shallow,
		} {
			m.Width, m.Height = 80, height
			f := frameOf(m, 0)
			lines := screen(m)

			// Half one: the frame is what Render drew. Without this the frame
			// could say anything and both sides would agree on a fiction.
			for i, want := range f.body {
				if got := lines[1+i]; got != want {
					t.Fatalf("h=%d %s: Render drew %q on body row %d, the frame says %q",
						height, name, got, i, want)
				}
			}

			// Half two: click() bounds by the frame, in both directions. A
			// prompt swallows clicks whatever is drawn — a stray click must
			// not interrupt typing — so that state expects none of them.
			for y := firstCard; y < m.Height-footerRows; y++ {
				row := RowAt(y)
				depth := len(m.Notes)
				if m.View == ViewBoard {
					depth = len(m.Column(ColumnAt(m.Width, 2)))
				}
				want := m.Prompt == nil && row < f.cards && row < depth
				got, call := Update(m, MouseMsg{X: 2, Y: y})
				if call != nil {
					t.Fatalf("h=%d %s: a click on row %d ran %s", height, name, y, call.Verb)
				}
				picked := got.Detail && got.Col == 0 && got.Row[0] == row
				if m.View == ViewNotes {
					picked = got.Detail && got.Pane == PaneNotes && got.NoteRow == row
				}
				if picked != want {
					t.Errorf("h=%d %s: a click on row %d picked=%v, want %v (%d card rows drawn, %d in the column)",
						height, name, y, picked, want, f.cards, depth)
				}
				if !want && (got.Detail != m.Detail || got.Row != m.Row ||
					got.NoteRow != m.NoteRow || got.Pane != m.Pane) {
					t.Errorf("h=%d %s: a click on row %d, which draws no card, moved the selection",
						height, name, y)
				}
			}
		}
	}
}

// §11.6: a paste is one message carrying many characters. bubbletea has
// bracketed paste on unless it is switched off, and it is not switched off
// here, so a pasted note body, filter term or piece of feedback arrives whole
// — and being dropped for not being exactly one character is a silent loss of
// everything the operator meant to say.
func TestAPasteLandsInThePromptWholeAndAtTheCursor(t *testing.T) {
	m := promptOf(t, "Find in board", "")
	m, _ = Update(m, KeyMsg{Key: "sweep releases a lease", Paste: true})
	if m.Prompt.Value != "sweep releases a lease" {
		t.Fatalf("the paste did not land: %q", m.Prompt.Value)
	}
	if m.Prompt.Cursor != len([]rune("sweep releases a lease")) {
		t.Fatalf("cursor = %d, want after the pasted text", m.Prompt.Cursor)
	}

	// And into the middle of what is already there, which is where a
	// correction is pasted.
	mid := promptOf(t, "Find in board", "the sweep")
	mid.Prompt.Cursor = 4
	mid, _ = Update(mid, KeyMsg{Key: "lease ", Paste: true})
	if mid.Prompt.Value != "the lease sweep" {
		t.Fatalf("value = %q, want %q", mid.Prompt.Value, "the lease sweep")
	}
	if mid.Prompt.Cursor != len([]rune("the lease ")) {
		t.Fatalf("cursor = %d, want after the pasted text", mid.Prompt.Cursor)
	}
}

// §9.1: a paste is not a keystroke. approve and note.add are mutating, gated
// verbs, and "a" runs one of them on each view — so a one-character paste
// running the verb is the same class of accident as a stray click, and the
// boundary between "does nothing" and "approves a task" must not be the
// length of what was pasted.
func TestAPasteNeverRunsAVerb(t *testing.T) {
	b := board(t, task(1, tasks.StatusReview, "waiting on review"))
	if _, call := Update(b, KeyMsg{Key: "a"}); call == nil || call.Verb != "task.approve" {
		t.Fatalf("precondition: a typed 'a' should approve, got %#v", call)
	}
	if got, call := Update(b, KeyMsg{Key: "a", Paste: true}); call != nil {
		t.Fatalf("a one-character paste ran %s", call.Verb)
	} else if got.Prompt != nil {
		t.Fatalf("a paste with no prompt open opened one")
	}
	if _, call := Update(b, KeyMsg{Key: "approve it", Paste: true}); call != nil {
		t.Fatalf("a pasted phrase ran %s", call.Verb)
	}

	n := notesModel(t, []*tasks.Note{{ID: "N1", Seq: 1, Status: "inbox", Body: "an idea"}}, nil)
	if _, call := Update(n, KeyMsg{Key: "a"}); call != nil {
		t.Fatalf("precondition: a typed 'a' on the notes view returned a call before its prompt: %#v", call)
	}
	if got, _ := Update(n, KeyMsg{Key: "a"}); got.Prompt == nil {
		t.Fatal("precondition: a typed 'a' on the notes view opens the add prompt")
	}
	if got, call := Update(n, KeyMsg{Key: "a", Paste: true}); call != nil || got.Prompt != nil {
		t.Fatalf("a one-character paste on the notes view opened %v / ran %#v", got.Prompt, call)
	}
}

// §11.6: alt+<letter> is not the letter. bubbletea reports it as the same
// rune with a modifier, so dropping the modifier turns a chord nobody bound
// into a verb that changes the board.
func TestAltIsNotTheBareLetter(t *testing.T) {
	b := board(t, task(1, tasks.StatusReview, "waiting on review"))
	if _, call := Update(b, KeyMsg{Key: "a", Alt: true}); call != nil {
		t.Fatalf("alt+a ran %s", call.Verb)
	}
	n := notesModel(t, []*tasks.Note{{ID: "N1", Seq: 1, Status: "inbox", Body: "an idea"}}, nil)
	if got, call := Update(n, KeyMsg{Key: "a", Alt: true}); call != nil || got.Prompt != nil {
		t.Fatalf("alt+a on the notes view opened %v / ran %#v", got.Prompt, call)
	}
	p := promptOf(t, "Find in board", "sweep")
	if got, _ := Update(p, KeyMsg{Key: "a", Alt: true}); got.Prompt.Value != "sweep" {
		t.Fatalf("alt+a typed into the prompt: %q", got.Prompt.Value)
	}
}

// §11.6: the prompt is ONE row, and it stays one row whatever is pasted into
// it. A raw newline in the value would make Render two rows longer and push
// the header off the screen — the regression task 15 closed.
func TestAPastedNewlineDoesNotBreakThePromptRow(t *testing.T) {
	const height = 24
	before := promptOf(t, "Find in board", "")
	before.Width, before.Height = 80, height
	rows := len(screen(before))

	m, _ := Update(before, KeyMsg{Key: "one\ntwo\tthree", Paste: true})
	if strings.ContainsAny(m.Prompt.Value, "\n\t") {
		t.Fatalf("the prompt value carries a raw control character: %q", m.Prompt.Value)
	}
	if !strings.Contains(m.Prompt.Value, "one") || !strings.Contains(m.Prompt.Value, "three") {
		t.Fatalf("filtering ate the text: %q", m.Prompt.Value)
	}
	m.Width, m.Height = 80, height
	if got := len(screen(m)); got != rows {
		t.Fatalf("the pasted value made the view %d rows, was %d", got, rows)
	}
	if !strings.Contains(screen(m)[0], "/repo") {
		t.Fatalf("the header went: %q", screen(m)[0])
	}
}

// §5.8 on the board: a dependency that was CANCELLED will never be done, so
// its dependent is blocked until someone edits the edge. "blocked" alone reads
// as "wait"; this one needs a decision, and the panel says which task to make
// it about. It must say so inside the pane, in cells, at the heights task 15
// pins.
func TestTheDetailPanelNamesACancelledBlocker(t *testing.T) {
	waiting := task(7, tasks.StatusTodo, "waits on work that was dropped")
	waiting.Blocked, waiting.Abandoned = true, []int64{3, 4}
	m := board(t, waiting)
	m.Col, m.Row[0], m.Detail = 0, 0, true

	for _, height := range []int{10, 24, 40} {
		m.Width, m.Height = 80, height
		lines := screen(m)
		if len(lines) != height {
			t.Fatalf("h=%d: %d lines for %d rows", height, len(lines), height)
		}
		if !strings.Contains(lines[0], "/repo") {
			t.Fatalf("h=%d: the header went: %q", height, lines[0])
		}
		joined := strings.Join(lines, "\n")
		for _, want := range []string{"blocked", "cancelled", "#3", "#4"} {
			if !strings.Contains(joined, want) {
				t.Errorf("h=%d: the panel does not say %q:\n%s", height, want, joined)
			}
		}
		for i, ln := range lines {
			if cells(ln) > m.Width {
				t.Errorf("h=%d: line %d is %d cells wide: %.30q…", height, i, cells(ln), ln)
			}
		}
	}

	// A dependency that is merely not done yet reads differently.
	plain := task(8, tasks.StatusTodo, "waits on work still to come")
	plain.Blocked = true
	p := board(t, plain)
	p.Col, p.Row[0], p.Detail = 0, 0, true
	p.Width, p.Height = 80, 24
	shown := strings.Join(screen(p), "\n")
	if !strings.Contains(shown, "blocked") {
		t.Fatalf("an ordinary blocked task does not say so:\n%s", shown)
	}
	if strings.Contains(shown, "cancelled") {
		t.Fatalf("an ordinary blocked task was reported as abandoned:\n%s", shown)
	}
}

// §11.6: a temp file that was never written has nothing worth keeping, and
// leaving one behind on every failure fills the operator's temp dir with empty
// files nobody will ever look at. The file finishEdit keeps when the DAEMON
// refuses is a different thing entirely — it holds work the operator did — and
// that one must survive.
func TestAFailedWriteLeavesNoTempFileBehind(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "htask-note-*.md")
	if err != nil {
		t.Fatalf("temp: %v", err)
	}
	path := f.Name()
	f.Close() // so the write below really fails

	got, err := writeEditFile(f, "a body that never lands")
	if err == nil {
		t.Fatalf("writing to a closed file must fail; it returned %q", got)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the temp file survived a failed write: %v", err)
	}
}

// The other half of task 17's rule, so this fix cannot sweep it up: when the
// DAEMON call fails, the file stays and its path is named.
func TestAFailedDaemonCallKeepsTheEditedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kept.md")
	if err := os.WriteFile(path, []byte("what the operator wrote\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	msg := afterUpdate(path, errors.New("CONFLICT: note moved"))
	failed, ok := msg.(ErrMsg)
	if !ok {
		t.Fatalf("a refused update answered %#v", msg)
	}
	if !strings.Contains(failed.Message, path) {
		t.Fatalf("the failure does not name the file it kept: %q", failed.Message)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("the operator's work was thrown away: %v", statErr)
	}

	// And a successful update does clear it up, so the keeping is about
	// failure and not about never tidying.
	if _, ok := afterUpdate(path, nil).(DoneMsg); !ok {
		t.Fatal("a successful update must report done")
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("the temp file survived a successful update: %v", statErr)
	}
}

// longTask is a task whose detail panel is far taller than any screen: the
// case a bounded panel has to survive, and the one where an unbounded panel
// eats the board down to its heading row.
func longTask(seq int64, lines int) *tasks.Task {
	tk := task(seq, tasks.StatusTodo, "a task")
	body := make([]string, lines)
	for i := range body {
		body[i] = fmt.Sprintf("detail line %d", i+1)
	}
	tk.Description = strings.Join(body, "\n")
	return tk
}

// §11.6: the detail panel is a bounded bottom panel, never a cover. It is
// served before the body, so without a bound a description longer than the
// screen takes every row the body has and leaves its heading alone on the
// board — the operator cannot see a single card behind what they opened.
func TestTheDetailPanelIsBoundedToHalfTheBody(t *testing.T) {
	deep := []*tasks.Task{longTask(1, 400)}
	for i := 2; i <= 40; i++ {
		deep = append(deep, task(int64(i), tasks.StatusTodo, "another todo"))
	}
	for _, height := range []int{10, 12, 24} {
		m := board(t, deep...)
		m.Width, m.Height = 80, height
		m.Detail = true
		m.Col, m.Row[0] = 0, 0

		free := m.Height - 1 - 3
		want := free - free/2
		f := frameOf(m, 0)
		if len(f.body) < want {
			t.Errorf("h=%d: the detail took %d of %d free rows and left the body %d, want at least %d",
				height, len(f.detail), free, len(f.body), want)
		}
		if f.cards < 1 {
			t.Errorf("h=%d: no card row survived the detail panel", height)
		}
		lines := screen(m)
		if len(lines) > height {
			t.Errorf("h=%d: %d lines for %d rows", height, len(lines), height)
		}
		if !strings.Contains(lines[0], "/repo") {
			t.Errorf("h=%d: the header row did not survive: %q", height, lines[0])
		}
	}
}

// §11.6: what the panel cannot fit is scrolled to, not lost. clampLines cut
// the bottom off a long report and there was no gesture that reached it — the
// evidence a reviewer needs was on the screen the operator could not get to.
func TestEveryLineOfALongDetailCanBeScrolledTo(t *testing.T) {
	for _, height := range []int{10, 12, 24} {
		m := board(t, longTask(1, 200))
		m.Width, m.Height = 80, height
		m.Detail = true
		full := wrapTo(Detail(m, 0), m.Width)

		f := frameOf(m, 0)
		if len(f.detail) < 2 {
			t.Fatalf("h=%d: the detail panel drew no text at all", height)
		}
		// The pointer sits on the panel's first text row and stays there: the
		// panel does not change size while its text scrolls under it.
		y := 1 + len(f.body) + len(f.prompt) + 1
		seen := map[string]bool{}
		for i := 0; i <= len(full); i++ {
			f = frameOf(m, 0)
			for _, ln := range f.detail[1:] {
				seen[ln] = true
			}
			m, _ = Update(m, WheelMsg{X: 2, Y: y})
		}
		f = frameOf(m, 0)
		if f.detailOff != f.detailMax {
			t.Errorf("h=%d: scrolling stopped at %d of %d", height, f.detailOff, f.detailMax)
		}
		if last := full[len(full)-2]; !seen[last] {
			t.Errorf("h=%d: the last line of the detail was never drawn: %q", height, last)
		}
		for i, ln := range full {
			if !seen[ln] {
				t.Errorf("h=%d: line %d of the detail is not reachable: %q", height, i, ln)
			}
		}
		// And the way back is the same gesture.
		for i := 0; i <= len(full); i++ {
			m, _ = Update(m, WheelMsg{X: 2, Y: y, Up: true})
		}
		if got := frameOf(m, 0).detailOff; got != 0 {
			t.Errorf("h=%d: scrolling back up stopped at %d, not the top", height, got)
		}
	}
}

// §11.6: fitLines windowed the body from the TOP only, so a cursor moved past
// the budget was drawn nowhere — the operator was acting on a selection they
// could not see, with the footer's approve and reject aimed at it.
func TestTheCursorStaysInsideTheDrawnWindow(t *testing.T) {
	deep := []*tasks.Task{}
	notes := []*tasks.Note{}
	for i := 1; i <= 40; i++ {
		deep = append(deep, task(int64(i), tasks.StatusTodo, "a todo"))
		notes = append(notes, &tasks.Note{ID: fmt.Sprintf("N%d", i), Seq: int64(i), Status: "inbox", Body: "an idea"})
	}
	boardM := board(t, deep...)
	notesM := notesModel(t, notes, nil)

	for name, c := range map[string]struct {
		m    Model
		mark func(int) string
		off  func(Model) int
	}{
		"board": {boardM, func(n int) string { return fmt.Sprintf(">#%d ", n) }, func(m Model) int { return m.BoardOffset }},
		"notes": {notesM, func(n int) string { return fmt.Sprintf(">#%d [inbox]", n) }, func(m Model) int { return m.NotesOffset }},
	} {
		m := c.m
		m.Width, m.Height = 80, 12

		drawn := func(m Model, seq int) bool {
			return strings.Contains(strings.Join(frameOf(m, 0).body, "\n"), c.mark(seq))
		}
		// Down past the bottom of the window, one row at a time.
		for seq := 2; seq <= 40; seq++ {
			m, _ = Update(m, KeyMsg{Key: "down"})
			if !drawn(m, seq) {
				t.Fatalf("%s: after moving to #%d the selected row is not drawn:\n%s",
					name, seq, strings.Join(frameOf(m, 0).body, "\n"))
			}
		}
		if c.off(m) == 0 {
			t.Fatalf("%s: the cursor reached #40 without the list ever scrolling", name)
		}
		// And back up above the top of the window.
		for seq := 39; seq >= 1; seq-- {
			m, _ = Update(m, KeyMsg{Key: "up"})
			if !drawn(m, seq) {
				t.Fatalf("%s: after moving back to #%d the selected row is not drawn:\n%s",
					name, seq, strings.Join(frameOf(m, 0).body, "\n"))
			}
		}
		if c.off(m) != 0 {
			t.Fatalf("%s: back at #1 the list is still scrolled to %d", name, c.off(m))
		}
		// A wheel takes the window away from the cursor; the next key move
		// brings it back, because the selection is what the verbs act on.
		for i := 0; i < 20; i++ {
			m, _ = Update(m, WheelMsg{X: 2, Y: firstCard})
		}
		m, _ = Update(m, KeyMsg{Key: "down"})
		if !drawn(m, 2) {
			t.Fatalf("%s: after a wheel and a key move the selection is off the window:\n%s",
				name, strings.Join(frameOf(m, 0).body, "\n"))
		}
	}
}

// §11.6: a click selects the item the operator SEES. Hit-testing the row alone
// selected the card that would have been there had they never scrolled.
func TestAClickOnAScrolledListSelectsWhatIsDrawn(t *testing.T) {
	deep := []*tasks.Task{}
	for i := 1; i <= 40; i++ {
		deep = append(deep, task(int64(i), tasks.StatusTodo, "a todo"))
	}
	m := board(t, deep...)
	m.Width, m.Height = 80, 12
	for i := 0; i < 3; i++ {
		m, _ = Update(m, WheelMsg{X: 2, Y: firstCard})
	}
	f := frameOf(m, 0)
	if f.off == 0 {
		t.Fatal("the wheel did not scroll the list; this test needs a scrolled one")
	}

	for k := 0; k < f.cards; k++ {
		y := firstCard + k
		got, call := Update(m, MouseMsg{X: 2, Y: y})
		if call != nil {
			t.Fatalf("clicking drawn row %d ran %s", k, call.Verb)
		}
		if !got.Detail {
			t.Fatalf("clicking drawn row %d selected nothing", k)
		}
		if got.Row[0] != k+f.off {
			t.Fatalf("clicking drawn row %d selected item %d, want %d (offset %d)",
				k, got.Row[0], k+f.off, f.off)
		}
		seq := got.Column(0)[got.Row[0]].Seq
		if !strings.Contains(drawnAt(t, m, y), fmt.Sprintf("#%d ", seq)) {
			t.Fatalf("row %d draws %q but the click selected #%d", y, drawnAt(t, m, y), seq)
		}
	}

	// And the rows past the last drawn card are still nothing to click.
	for y := firstCard + f.cards; y < m.Height-footerRows; y++ {
		got, call := Update(m, MouseMsg{X: 2, Y: y})
		if call != nil {
			t.Fatalf("a click on row %d, past the last drawn card, ran %s", y, call.Verb)
		}
		if got.Detail || got.Row != m.Row {
			t.Fatalf("a click on row %d, past the last drawn card, selected #%d",
				y, got.Column(got.Col)[got.Row[got.Col]].Seq)
		}
	}
}

// §11.6: the wheel moves the panel under the pointer, and only that one.
// run.go dropped every mouse event that was not a left-click press, so a
// wheel never reached the model at all.
func TestTheWheelMovesOnlyTheRegionUnderThePointer(t *testing.T) {
	deep := []*tasks.Task{longTask(1, 200)}
	for i := 2; i <= 40; i++ {
		deep = append(deep, task(int64(i), tasks.StatusTodo, "a todo"))
	}
	m := board(t, deep...)
	m.Width, m.Height = 80, 24
	m.Detail = true
	f := frameOf(m, 0)
	if len(f.detail) < 2 || f.win < 1 {
		t.Fatalf("the frame drew %d detail rows and %d body rows", len(f.detail), f.win)
	}
	bodyY := 1 + len(f.body) - 1
	detailY := 1 + len(f.body) + len(f.prompt) + 1

	over, _ := Update(m, WheelMsg{X: 2, Y: detailY})
	if over.DetailOffset == 0 {
		t.Fatal("a wheel over the detail did not move it")
	}
	if over.BoardOffset != 0 {
		t.Fatalf("a wheel over the detail moved the list to %d", over.BoardOffset)
	}
	under, _ := Update(m, WheelMsg{X: 2, Y: bodyY})
	if under.BoardOffset == 0 {
		t.Fatal("a wheel over the body did not move the list")
	}
	if under.DetailOffset != 0 {
		t.Fatalf("a wheel over the body moved the detail to %d", under.DetailOffset)
	}

	// Both clamp at both ends: down until they stop, then up until they stop.
	for _, c := range []struct {
		name string
		y    int
		off  func(Model) int
		max  func(frame) int
	}{
		{"detail", detailY, func(m Model) int { return m.DetailOffset }, func(f frame) int { return f.detailMax }},
		{"body", bodyY, func(m Model) int { return m.BoardOffset }, func(f frame) int { return f.listMax }},
	} {
		x := m
		for i := 0; i < 500; i++ {
			x, _ = Update(x, WheelMsg{X: 2, Y: c.y})
		}
		if got, want := c.off(x), c.max(frameOf(x, 0)); got != want {
			t.Errorf("%s: scrolling down stopped at %d, want %d", c.name, got, want)
		}
		for i := 0; i < 500; i++ {
			x, _ = Update(x, WheelMsg{X: 2, Y: c.y, Up: true})
		}
		if got := c.off(x); got != 0 {
			t.Errorf("%s: scrolling up stopped at %d, not the top", c.name, got)
		}
	}

	// The two views scroll apart: a wheel on the notes body is the notes
	// offset, and the board's is where it was left.
	n := notesModel(t, []*tasks.Note{}, nil)
	notes := []*tasks.Note{}
	for i := 1; i <= 40; i++ {
		notes = append(notes, &tasks.Note{ID: fmt.Sprintf("N%d", i), Seq: int64(i), Status: "inbox", Body: "an idea"})
	}
	n, _ = Update(n, DataMsg{Notes: notes})
	n.Width, n.Height = 80, 12
	n, _ = Update(n, WheelMsg{X: 2, Y: firstCard})
	if n.NotesOffset == 0 {
		t.Fatal("a wheel over the notes body did not move the notes offset")
	}
	if n.BoardOffset != 0 {
		t.Fatalf("a wheel on the notes view moved the board offset to %d", n.BoardOffset)
	}

	// A wheel outside both panels — the footer — moves nothing.
	still, _ := Update(m, WheelMsg{X: 2, Y: m.Height - 1})
	if still.DetailOffset != 0 || still.BoardOffset != 0 {
		t.Fatalf("a wheel on the footer scrolled something: detail %d, list %d",
			still.DetailOffset, still.BoardOffset)
	}
}

// §11.6: the detail panel's scroll position belongs to the text that was in
// it. A tab switch is a different view of a different list, so it is a
// different text — and with the same cursor on both sides of the switch the
// selection looked unchanged, so a board scrolled thirty lines down opened the
// first note mid-body.
func TestTheDetailScrollDoesNotSurviveATabSwitch(t *testing.T) {
	body := make([]string, 200)
	for i := range body {
		body[i] = fmt.Sprintf("note line %d", i+1)
	}
	note := &tasks.Note{ID: "N1", Seq: 1, Status: "inbox", Body: strings.Join(body, "\n")}

	m := board(t, longTask(1, 200))
	m, _ = Update(m, DataMsg{Tasks: m.Tasks, Notes: []*tasks.Note{note}})
	m.Width, m.Height = 80, 14

	// The board's cursor and the notes cursor are both on the first row, which
	// is what made the two selections look like one.
	m, _ = Update(m, KeyMsg{Key: "enter"})
	f := frameOf(m, 0)
	y := 1 + len(f.body) + len(f.prompt) + 1
	for i := 0; i < 10; i++ {
		m, _ = Update(m, WheelMsg{X: 2, Y: y})
	}
	if m.DetailOffset == 0 {
		t.Fatal("the wheel did not scroll the task's detail; this test needs a scrolled one")
	}

	m, _ = Update(m, KeyMsg{Key: "esc"})
	m, _ = Update(m, KeyMsg{Key: "tab"})
	m, _ = Update(m, KeyMsg{Key: "enter"})
	if m.View != ViewNotes || !m.Detail {
		t.Fatalf("the test did not reach an open detail on the notes view: view %q, detail %v", m.View, m.Detail)
	}
	if m.DetailOffset != 0 {
		t.Errorf("the note's detail opened at offset %d, carried over from the board", m.DetailOffset)
	}
	f = frameOf(m, 0)
	if len(f.detail) < 2 {
		t.Fatal("the note's detail panel drew no text")
	}
	if want := wrapTo(Detail(m, 0), m.Width)[0]; f.detail[1] != want {
		t.Errorf("the note's detail opens on %q, want its first line %q", f.detail[1], want)
	}

	// And back the other way: the note's scroll is not the task's either.
	for i := 0; i < 10; i++ {
		m, _ = Update(m, WheelMsg{X: 2, Y: 1 + len(f.body) + 1})
	}
	if m.DetailOffset == 0 {
		t.Fatal("the wheel did not scroll the note's detail")
	}
	m, _ = Update(m, KeyMsg{Key: "tab"})
	m, _ = Update(m, KeyMsg{Key: "enter"})
	if m.DetailOffset != 0 {
		t.Errorf("the task's detail reopened at offset %d, carried over from the notes view", m.DetailOffset)
	}
}

// §16.1 and §11.6: the detail panel renders validation as a DERIVED checklist.
// The box is checked because a submitted evidence entry cites that criterion,
// never because anyone flipped it, and the citing lines sit under the
// criterion they prove rather than in the flat sibling list.
func TestValidationChecklistIsDerivedFromCitations(t *testing.T) {
	c := task(3, tasks.StatusReview, "the door")
	c.Validation = []tasks.Criterion{
		{Text: "the gate is green", Required: true},
		{Text: "the docs say so", Required: true},
		{Text: "a screenshot", Required: false},
	}
	c.Report = "wired it"
	c.Evidence = []string{"make build: ok"}
	c.EvidenceFor = []tasks.Citation{
		{Criterion: 1, Text: "make test-full -> exit 0"},
		{Criterion: 1, Text: "go vet ./... -> silent"},
	}
	m := board(t, c)
	m, _ = Update(m, KeyMsg{Key: "enter"})
	d := Detail(m, 0)

	lines := strings.Split(d, "\n")
	at := func(want string) int {
		for i, l := range lines {
			if strings.Contains(l, want) {
				return i
			}
		}
		t.Fatalf("detail has no line containing %q:\n%s", want, d)
		return -1
	}

	green, docs, shot := lines[at("the gate is green")], lines[at("the docs say so")], lines[at("a screenshot")]
	if !strings.Contains(green, "[x]") {
		t.Fatalf("a cited criterion is not checked: %q", green)
	}
	if !strings.Contains(docs, "[ ]") {
		t.Fatalf("an uncited criterion is not shown as a gap: %q", docs)
	}
	if !strings.Contains(shot, "(optional)") {
		t.Fatalf("the detail still drops Criterion.Required: %q", shot)
	}
	if strings.Contains(shot, "[x]") {
		t.Fatalf("an uncited optional criterion was checked: %q", shot)
	}

	// Both citing lines are nested under criterion 1, above criterion 2.
	first, second := at("make test-full -> exit 0"), at("go vet ./... -> silent")
	if first <= at("the gate is green") || second <= first || second >= at("the docs say so") {
		t.Fatalf("citations are not nested under the criterion they prove:\n%s", d)
	}
	for _, l := range []string{lines[first], lines[second]} {
		if !strings.HasPrefix(l, "    ") {
			t.Fatalf("a citing line is not indented under its criterion: %q", l)
		}
	}
	// A cited line is NOT also in the flat evidence list, and the task-level
	// entry still is.
	if strings.Contains(d, "evidence: make test-full") {
		t.Fatalf("a citation was repeated in the flat evidence list:\n%s", d)
	}
	if !strings.Contains(d, "make build: ok") {
		t.Fatalf("task-level evidence vanished:\n%s", d)
	}
}

// A criterion list edited after a submission can leave a citation pointing at
// nothing. Dropping it silently would let a reviewer read full coverage off a
// checklist that lost an item, so the panel says so instead.
func TestAnOrphanedCitationIsShownNotSwallowed(t *testing.T) {
	c := task(4, tasks.StatusReview, "the door")
	c.Validation = []tasks.Criterion{{Text: "the gate is green", Required: true}}
	c.EvidenceFor = []tasks.Citation{{Criterion: 7, Text: "make e2e -> ok"}}
	m := board(t, c)
	m, _ = Update(m, KeyMsg{Key: "enter"})
	d := Detail(m, 0)
	if !strings.Contains(d, "make e2e -> ok") {
		t.Fatalf("an orphaned citation was swallowed:\n%s", d)
	}
}

// §16.3, the same reason a lease is on the card: a wait nobody can see is a
// wait nobody ends. The doing card already prints a time; the review card
// printed none while submitted_at sat unread in the row.
func TestReviewAgeIsOnTheCardAndInTheDetail(t *testing.T) {
	const now = int64(1_700_000_000_000)
	const hour = int64(3_600_000)
	waiting := task(3, tasks.StatusReview, "wire the door")
	waiting.ClaimedBy, waiting.ClaimedByName = "agent:wF:p1", "builder"
	// UpdatedAt moved after the submission. The wait is counted from the
	// submission, so an edit does not restart it.
	waiting.SubmittedAt, waiting.UpdatedAt = now-2*hour, now-5*60_000
	working := task(4, tasks.StatusDoing, "hold the lease")
	working.ClaimedBy, working.ClaimedByName = "agent:wF:p2", "runner"
	working.LeaseUntil = now + 5*60_000

	m := board(t, waiting, working)
	m.Width = 200
	out := Render(m, now)
	// The columns share a line, so each card is read on its own: from its
	// title to the padding that separates it from the next column.
	card := func(title string) string {
		i := strings.Index(out, title)
		if i < 0 {
			t.Fatalf("no card for %q:\n%s", title, out)
		}
		seg := out[i:]
		if j := strings.Index(seg, "  "); j >= 0 {
			seg = seg[:j]
		}
		return seg
	}
	if seg := card("wire the door"); strings.Contains(seg, "5m ago") {
		t.Fatalf("the card's age came from UpdatedAt, not SubmittedAt — UpdatedAt "+
			"moves on reject and resubmit, restarting the clock on the task "+
			"waiting longest: %q", seg)
	} else if !strings.Contains(seg, "submitted 2h ago") {
		t.Fatalf("the review card does not say how long it has waited: %q", seg)
	}
	if seg := card("hold the lease"); !strings.Contains(seg, "5m") || strings.Contains(seg, "ago") {
		t.Fatalf("the doing card lost its lease countdown: %q", seg)
	}

	// Column 2 is review; the detail panel opens on the selection.
	m.Col, m.Row[2] = 2, 0
	if got := m.SelectedTask(); got == nil || got.Title != "wire the door" {
		t.Fatalf("selection is %#v", got)
	}
	d := Detail(m, now)
	if strings.Contains(d, "5m ago") {
		t.Fatalf("the detail's age came from UpdatedAt, not SubmittedAt:\n%s", d)
	}
	if !strings.Contains(d, "submitted 2h ago") {
		t.Fatalf("the detail panel does not say how long it has waited:\n%s", d)
	}
}

// colHead is one column's slice of the board's heading row, which is row 1 of
// the screen. The columns are padded to a fixed cell width and laid side by
// side, so a column is read at its own offset rather than by searching the
// line.
func colHead(m Model, now int64, col int) string {
	w := m.Width / len(Columns)
	r := []rune(screenAt(m, now, headingRow))
	lo, hi := col*w, (col+1)*w
	if lo > len(r) {
		lo = len(r)
	}
	if hi > len(r) {
		hi = len(r)
	}
	return strings.TrimRight(string(r[lo:hi]), " ")
}

func screenAt(m Model, now int64, row int) string {
	lines := strings.Split(Render(m, now), "\n")
	if row >= len(lines) {
		return ""
	}
	return lines[row]
}

func deepColumn(n int, status tasks.Status, prefix string) []*tasks.Task {
	out := make([]*tasks.Task, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, task(int64(i), status, fmt.Sprintf("%s%d", prefix, i)))
	}
	return out
}

// §11.6: the panels scroll, and until now nothing said how much was off the
// screen or where the window sat. The heading is the chrome that is already
// there — it survives clamping, it already carries the count, and click()
// already ignores it — so the position goes in it and costs no row and no
// column.
func TestViewportPositionIsInTheColumnHeadings(t *testing.T) {
	all := append(deepColumn(40, tasks.StatusTodo, "t"), deepColumn(3, tasks.StatusDone, "d")...)
	m := board(t, all...)
	m.Width, m.Height = 80, 24
	f := frameOf(m, 0)

	todo := colHead(m, 0, 0)
	// The count clause is untouched, so what a board that fits prints today
	// is a prefix of what a deep one prints now.
	if !strings.Contains(todo, "todo (40)") {
		t.Fatalf("the heading no longer reports the true total: %q", todo)
	}
	want := fmt.Sprintf("1-%d", f.cards)
	if !strings.Contains(todo, want) {
		t.Fatalf("the todo heading does not say which cards are drawn (want %q): %q", want, todo)
	}
	// The range is the cards ACTUALLY drawn, not the window's capacity.
	if n := strings.Count(Render(m, 0), "\nt1 ") + strings.Count(Render(m, 0), " t1 "); n == 0 {
		t.Fatalf("card t1 is not on the screen, so the range starting at 1 is a lie")
	}
	if strings.Contains(Render(m, 0), fmt.Sprintf("t%d ", f.cards+1)) {
		t.Fatalf("card t%d was drawn, so the range must not end at %d", f.cards+1, f.cards)
	}

	// A column that fits entirely reads exactly as it does today.
	if done := colHead(m, 0, 3); done != "done (3)" {
		t.Fatalf("a column with nothing off the screen gained chrome: %q", done)
	}

	// Scrolled past a short column, the heading says so: three cards exist and
	// none of them is on the screen, which is what tells exhausted from merely
	// shorter.
	m = m.setListOffset(f.listMax)
	if done := colHead(m, 0, 3); done == "done (3)" {
		t.Fatalf("scrolled past the done column, its heading still reads as if it were visible: %q", done)
	}
	if done := colHead(m, 0, 3); !strings.Contains(done, "(3)") {
		t.Fatalf("the heading lost the true total while scrolled past it: %q", done)
	}
}

// pad() gives a column its width minus one cell of gap and truncates anything
// longer, so a heading that does not fit is a heading the operator reads half
// of. It is measured in cells, the same way the renderer measures it.
func TestViewportPositionNeverOverflowsItsColumn(t *testing.T) {
	all := append(deepColumn(40, tasks.StatusTodo, "t"), deepColumn(40, tasks.StatusReview, "r")...)
	for _, width := range []int{60, 80, 120} {
		m := board(t, all...)
		m.Width, m.Height = width, 24
		budget := width/len(Columns) - 1
		for _, off := range []int{0, 5, frameOf(m, 0).listMax} {
			at := m.setListOffset(off)
			f := frameOf(at, 0)
			for i, c := range Columns {
				// What the heading is BEFORE pad() sees it. Measuring the
				// drawn row instead would measure pad()'s own truncation and
				// pass whatever it was given.
				built := columnHead(string(c), len(at.Column(i)), f.off, f.cards, budget)
				if cells(built) > budget {
					t.Fatalf("w=%d off=%d %s: heading is %d cells against a budget of %d: %q",
						width, off, c, cells(built), budget, built)
				}
				// And what reached the screen is that, whole: a heading pad()
				// had to cut is a number the operator reads half of.
				if drawn := colHead(at, 0, i); drawn != built {
					t.Fatalf("w=%d off=%d %s: the renderer cut the heading: %q against %q",
						width, off, c, drawn, built)
				}
			}
		}
	}
}

// frame is the single source of the layout arithmetic (a second copy of it was
// once a click on a blank row opening a card nobody could see). The heading
// reads frame's offset; it does not work it out again.
func TestViewportPositionTracksTheFrameOffset(t *testing.T) {
	all := deepColumn(40, tasks.StatusTodo, "t")
	m := board(t, all...)
	m.Width, m.Height = 80, 24

	check := func(what string, m Model) {
		t.Helper()
		f := frameOf(m, 0)
		head := colHead(m, 0, 0)
		want := fmt.Sprintf("%d-", f.off+1)
		if f.cards == 0 {
			return
		}
		if !strings.Contains(head, want) {
			t.Fatalf("%s: heading %q does not start at frame off+1 (%s)", what, head, want)
		}
	}
	check("at rest", m)

	// The wheel, over the body.
	wheeled := m
	for i := 0; i < 4; i++ {
		wheeled, _ = Update(wheeled, WheelMsg{X: 2, Y: 3, At: 0})
	}
	if frameOf(wheeled, 0).off == 0 {
		t.Fatal("the wheel did not move the body")
	}
	check("after the wheel", wheeled)

	// Cursor-follow, which drives the same offset from the keyboard.
	walked := m
	for i := 0; i < 30; i++ {
		walked, _ = Update(walked, KeyMsg{Key: "down"})
	}
	if frameOf(walked, 0).off == 0 {
		t.Fatal("walking the cursor past the window did not move the body")
	}
	check("after cursor-follow", walked)
}

// The detail panel's first row is a blank separator that carried nothing. It
// carries the panel's position now, so the panel gains no row for it.
func TestViewportPositionIsOnTheDetailSeparator(t *testing.T) {
	long := task(1, tasks.StatusReview, "a long report")
	long.Report = strings.TrimSpace(strings.Repeat("a line of the report that the panel has to scroll through\n", 60))
	m := board(t, long)
	m.Width, m.Height = 80, 24
	m, _ = Update(m, KeyMsg{Key: "enter"})
	if !m.Detail {
		t.Fatal("enter did not open the detail panel")
	}
	f := frameOf(m, 0)
	if f.detailMax == 0 {
		t.Fatalf("the detail fits, so this test proves nothing: %d rows", len(f.detail))
	}

	sep := 1 + len(f.body) + len(f.prompt)
	row := screenAt(m, 0, sep)
	shown := len(f.detail) - 1
	total := f.detailMax + shown
	for _, want := range []string{fmt.Sprintf("%d", f.detailOff+1), fmt.Sprintf("%d", f.detailOff+shown), fmt.Sprintf("%d", total)} {
		if !strings.Contains(row, want) {
			t.Fatalf("the separator row does not carry %s of the panel's position: %q", want, row)
		}
	}

	// The row was already there, so the document is exactly as tall as it was
	// with the row blank.
	was := strings.Count(Render(m, 0), "\n")
	short := board(t, task(1, tasks.StatusReview, "a short one"))
	short.Width, short.Height = 80, 24
	short, _ = Update(short, KeyMsg{Key: "enter"})
	if got := strings.Count(Render(short, 0), "\n"); got != was {
		t.Fatalf("the indicator changed the document's height: %d lines against %d", got, was)
	}
	// And a panel with nothing off the screen keeps the row blank.
	sf := frameOf(short, 0)
	if sf.detailMax != 0 {
		t.Fatalf("the short detail scrolls after all: max %d", sf.detailMax)
	}
	if line := screenAt(short, 0, 1+len(sf.body)+len(sf.prompt)); strings.TrimSpace(line) != "" {
		t.Fatalf("a panel with nothing off the screen gained chrome: %q", line)
	}
}

// paneHead is one of the notes view's two heading halves, read at its own cell
// offset the way colHead reads a board column.
func paneHead(m Model, now int64, pane int) string {
	half := m.Width / 2
	r := []rune(screenAt(m, now, headingRow))
	lo, hi := pane*half, (pane+1)*half
	if lo > len(r) {
		lo = len(r)
	}
	if hi > len(r) {
		hi = len(r)
	}
	return strings.TrimRight(string(r[lo:hi]), " ")
}

func manyNotes(n int) []*tasks.Note {
	out := make([]*tasks.Note, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, &tasks.Note{ID: fmt.Sprintf("N%d", i), Seq: int64(i),
			Status: "inbox", Body: fmt.Sprintf("note %d", i)})
	}
	return out
}

func manyParked(n int) []store.Parked {
	out := make([]store.Parked, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, store.Parked{ID: fmt.Sprintf("P%d", i),
			Verb: "tasks.approve", Subject: "agent:wF:p1", State: "parked"})
	}
	return out
}

// The notes view scrolls on an offset shared by its two panes, exactly as the
// board's four columns do, and its heading said nothing at all — not even how
// many rows there were. It carries the count now, and where the window sits
// when part of a pane is off the screen.
func TestNotesViewportPositionIsInTheHeadings(t *testing.T) {
	m := notesModel(t, manyNotes(40), manyParked(3))
	m.Width, m.Height = 80, 24
	f := frameOf(m, 0)
	if f.cards >= 40 {
		t.Fatalf("40 notes fit in the window, so this test proves nothing: %d rows", f.cards)
	}

	notes := paneHead(m, 0, 0)
	if !strings.Contains(notes, "notes (40)") {
		t.Fatalf("the notes heading does not carry its count: %q", notes)
	}
	if want := fmt.Sprintf("1-%d", f.cards); !strings.Contains(notes, want) {
		t.Fatalf("the notes heading does not say which rows are drawn (want %q): %q", want, notes)
	}
	// The shorter pane is entirely on the screen, so it says only how many.
	if parked := paneHead(m, 0, 1); parked != "parked (3)" {
		t.Fatalf("a pane with nothing off the screen gained a range: %q", parked)
	}

	// Scrolled past the short pane: three exist and none is drawn, which is
	// what tells exhausted from merely shorter.
	at := m.setListOffset(f.listMax)
	af := frameOf(at, 0)
	if parked := paneHead(at, 0, 1); parked != "parked (3) none" {
		t.Fatalf("scrolled past the parked pane, its heading reads %q", parked)
	}
	if want := fmt.Sprintf("%d-", af.off+1); !strings.Contains(paneHead(at, 0, 0), want) {
		t.Fatalf("the notes range does not start at frame off+1 (%s): %q", want, paneHead(at, 0, 0))
	}

	// Nothing is cut: what columnHead built is what reached the screen.
	budget := m.Width/2 - 1
	for pane, want := range map[int]string{
		0: columnHead("notes", 40, af.off, af.cards, budget),
		1: columnHead("parked", 3, af.off, af.cards, budget),
	} {
		if got := paneHead(at, 0, pane); got != want {
			t.Fatalf("pane %d: the renderer cut the heading: %q against %q", pane, got, want)
		}
	}
}

// A view that fits reads as a count and nothing else, and the range never
// costs a row: it is written on a heading that was always drawn.
func TestNotesViewportCostsNoRow(t *testing.T) {
	small := notesModel(t, manyNotes(2), manyParked(1))
	small.Width, small.Height = 80, 24
	if got := paneHead(small, 0, 0); got != "notes (2)" {
		t.Fatalf("a view that fits gained chrome: %q", got)
	}
	if got := paneHead(small, 0, 1); got != "parked (1)" {
		t.Fatalf("a view that fits gained chrome: %q", got)
	}

	deep := notesModel(t, manyNotes(40), manyParked(3))
	deep.Width, deep.Height = 80, 24
	if !strings.Contains(paneHead(deep, 0, 0), "-") {
		t.Fatal("the deep view has no range, so this comparison proves nothing")
	}
	if a, b := strings.Count(Render(small, 0), "\n"), strings.Count(Render(deep, 0), "\n"); a != b {
		t.Fatalf("the range changed the document's height: %d lines against %d", b, a)
	}
}

// A note promoted onto ANOTHER project's board says so in its detail: the id
// alone does not tell the operator which board to look on.
func TestNoteDetailNamesACrossProjectTask(t *testing.T) {
	m := notesModel(t, []*tasks.Note{{
		ID: "N1", Seq: 1, Status: "task", Body: "belongs elsewhere", Project: "/repo",
		TaskID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", TaskProject: "/other/sibling-repo",
	}}, nil)
	d := Detail(m, 0)
	if !strings.Contains(d, "01ARZ3NDEKTSV4RRFFQ69G5FAV") || !strings.Contains(d, "sibling-repo") {
		t.Fatalf("the detail does not name the task's board:\n%s", d)
	}
}

// And a promotion that stayed home reads exactly as it always did.
func TestNoteDetailSaysNothingAboutProjectWhenThePromotionStayedHome(t *testing.T) {
	m := notesModel(t, []*tasks.Note{{
		ID: "N1", Seq: 1, Status: "task", Body: "ordinary", Project: "/repo",
		TaskID: "01ARZ3NDEKTSV4RRFFQ69G5FAV",
	}}, nil)
	d := Detail(m, 0)
	if !strings.Contains(d, "became task 01ARZ3NDEKTSV4RRFFQ69G5FAV\n") {
		t.Fatalf("the detail changed for a same-project promotion:\n%s", d)
	}
}

// The operator approves from this panel, so what the prose door tells a
// reviewer the TUI has to tell them too: the report on the card is not the one
// that was submitted. Without this the plugin answered the same question two
// ways depending on which door the reviewer used, and the door the operator
// actually reviews from was the silent one.
func TestDetailSaysAReportWasAmended(t *testing.T) {
	c := task(3, tasks.StatusReview, "corrected after submitting")
	c.Report, c.Evidence = "done at 9f738bf", []string{"make test-full at 9f738bf: EXIT=0"}
	c.AmendCount, c.AmendedAt = 1, 1
	m := board(t, c)
	m, _ = Update(m, KeyMsg{Key: "enter"})
	d := Detail(m, 0)
	for _, want := range []string{"amended", "1 amendment", "submitted"} {
		if !strings.Contains(d, want) {
			t.Errorf("the detail panel does not say %q:\n%s", want, d)
		}
	}

	twice := task(4, tasks.StatusReview, "corrected twice")
	twice.Report, twice.AmendCount, twice.AmendedAt = "done", 2, 1
	m2 := board(t, twice)
	m2, _ = Update(m2, KeyMsg{Key: "enter"})
	if d := Detail(m2, 0); !strings.Contains(d, "2 amendments") {
		t.Errorf("the detail panel does not count two amendments:\n%s", d)
	}

	plain := task(5, tasks.StatusReview, "submitted once")
	plain.Report = "done"
	m3 := board(t, plain)
	m3, _ = Update(m3, KeyMsg{Key: "enter"})
	if d := Detail(m3, 0); strings.Contains(d, "amended") {
		t.Errorf("a report nobody amended was reported as amended:\n%s", d)
	}
}
