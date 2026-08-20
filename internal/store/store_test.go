package store

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/husniadil/herdr-tasks/internal/codes"
	"github.com/husniadil/herdr-tasks/internal/tasks"
)

const proj = "/tmp/project-a"

var operator = tasks.Actor{Principal: tasks.PrincipalHuman}

func peer(pane, harness string) tasks.Actor {
	return tasks.Actor{Principal: tasks.Principal("agent:" + pane), Name: "peer", Harness: harness, Session: "s1"}
}

// open gives every test its own store in a temp dir: tests never touch the
// operator's state dir (§12.3).
func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func codeOf(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		t.Fatal("want a coded error, got nil")
	}
	ce, ok := err.(*codes.Error)
	if !ok {
		t.Fatalf("want *codes.Error, got %T: %v", err, err)
	}
	return ce.Code
}

func create(t *testing.T, s *Store, title string) *tasks.Task {
	t.Helper()
	task, err := s.CreateTask(tasks.NewTaskInput{Project: proj, Title: title}, operator, tick(t))
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	return task
}

var clock int64 = 1_700_000_000_000

func tick(t *testing.T) int64 {
	t.Helper()
	clock++
	return clock
}

// §5.1: WAL, busy_timeout, foreign keys on.
func TestOpenAppliesPragmas(t *testing.T) {
	s := open(t)
	var mode string
	if err := s.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
	var fk, busy int
	if err := s.db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign_keys = %d (%v), want 1", fk, err)
	}
	if err := s.db.QueryRow("PRAGMA busy_timeout").Scan(&busy); err != nil || busy != 3000 {
		t.Fatalf("busy_timeout = %d (%v), want 3000", busy, err)
	}
}

// §5.2: a meta table carries the schema version, and a store from the future
// refuses to open rather than downgrade.
func TestSchemaVersionAndRefusalToDowngrade(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	var v, created int64
	if err := s.db.QueryRow("SELECT schema_version, created_at FROM meta").Scan(&v, &created); err != nil {
		t.Fatalf("meta: %v", err)
	}
	if v != SchemaVersion {
		t.Fatalf("schema_version = %d, want %d", v, SchemaVersion)
	}
	if created < 1_600_000_000_000 {
		t.Fatalf("created_at = %d, not Unix milliseconds (§5.3)", created)
	}
	if _, err := s.db.Exec("UPDATE meta SET schema_version = ?", SchemaVersion+1); err != nil {
		t.Fatalf("bump: %v", err)
	}
	s.Close()
	if _, err := Open(path); codeOf(t, err) != codes.Unavailable {
		t.Fatalf("a newer schema must refuse to open with UNAVAILABLE, got %v", err)
	}
}

// §5.4: the ULID is identity; the per-project seq is the number a human types,
// and it starts at 1 in every project.
func TestSeqIsPerProject(t *testing.T) {
	s := open(t)
	a1 := create(t, s, "one")
	a2 := create(t, s, "two")
	b1, err := s.CreateTask(tasks.NewTaskInput{Project: "/tmp/project-b", Title: "other"}, operator, tick(t))
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if a1.Seq != 1 || a2.Seq != 2 || b1.Seq != 1 {
		t.Fatalf("seqs = %d, %d, %d; want 1, 2, 1", a1.Seq, a2.Seq, b1.Seq)
	}
	if len(a1.ID) != 26 {
		t.Fatalf("id = %q, want a 26-char ULID", a1.ID)
	}
}

// §5.4: a task is addressable by its ULID and by its seq, in its own project.
func TestGetTaskByULIDAndBySeq(t *testing.T) {
	s := open(t)
	task := create(t, s, "addressable")
	byID, err := s.GetTask(proj, task.ID)
	if err != nil {
		t.Fatalf("by ulid: %v", err)
	}
	bySeq, err := s.GetTask(proj, "1")
	if err != nil {
		t.Fatalf("by seq: %v", err)
	}
	if byID.ID != bySeq.ID {
		t.Fatalf("%q != %q", byID.ID, bySeq.ID)
	}
	if _, err := s.GetTask("/tmp/project-b", "1"); codeOf(t, err) != codes.NotFound {
		t.Fatalf("a seq from another project must be NOT_FOUND, got %v", err)
	}
}

// §5.5: the mutation and its event land in the same transaction.
func TestTransitionWritesEventInSameTx(t *testing.T) {
	s := open(t)
	task := create(t, s, "claimable")
	a := peer("wF:p1", "claude")
	if _, err := s.TaskTransition(proj, task.ID, 0, func(tk *tasks.Task) (tasks.Event, error) {
		return tasks.Claim(tk, a, tick(t), 60_000)
	}); err != nil {
		t.Fatalf("claim: %v", err)
	}
	evs, err := s.Events(EventFilter{Project: proj})
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(evs) != 2 || evs[0].Kind != "created" || evs[1].Kind != "claimed" {
		t.Fatalf("events = %+v", evs)
	}
	if evs[1].Actor != "agent:wF:p1" || evs[1].Entity != "task" {
		t.Fatalf("event payload wrong: %+v", evs[1])
	}
	if evs[1].At < 1_600_000_000_000 {
		t.Fatalf("event at = %d, not Unix milliseconds (§5.3)", evs[1].At)
	}
}

// §5.5: a failed transition writes neither the row nor an event.
func TestFailedTransitionRollsBack(t *testing.T) {
	s := open(t)
	task := create(t, s, "contested")
	claim := func(a tasks.Actor) error {
		_, err := s.TaskTransition(proj, task.ID, 0, func(tk *tasks.Task) (tasks.Event, error) {
			return tasks.Claim(tk, a, tick(t), 60_000)
		})
		return err
	}
	if err := claim(peer("wF:p1", "claude")); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if got := codeOf(t, claim(peer("wF:p2", "codex"))); got != codes.Conflict {
		t.Fatalf("code = %q, want CONFLICT", got)
	}
	evs, _ := s.Events(EventFilter{Project: proj})
	if len(evs) != 2 {
		t.Fatalf("a rejected transition must leave no event: %+v", evs)
	}
	got, _ := s.GetTask(proj, task.ID)
	if got.ClaimedBy != "agent:wF:p1" {
		t.Fatalf("claimed_by = %q", got.ClaimedBy)
	}
}

// §5.6: a mutation whose --base-updated-at no longer matches is CONFLICT.
func TestBaseUpdatedAtConflict(t *testing.T) {
	s := open(t)
	task := create(t, s, "raced")
	title := "renamed by the operator"
	if _, err := s.TaskTransition(proj, task.ID, task.UpdatedAt, func(tk *tasks.Task) (tasks.Event, error) {
		return tasks.Update(tk, operator, tasks.UpdatePatch{Title: &title}, tick(t))
	}); err != nil {
		t.Fatalf("in-sync update: %v", err)
	}
	other := "renamed again from a stale read"
	_, err := s.TaskTransition(proj, task.ID, task.UpdatedAt, func(tk *tasks.Task) (tasks.Event, error) {
		return tasks.Update(tk, operator, tasks.UpdatePatch{Title: &other}, tick(t))
	})
	if got := codeOf(t, err); got != codes.Conflict {
		t.Fatalf("code = %q, want CONFLICT", got)
	}
}

// §5.7 and the dependency DAG: only done satisfies a dep, and blocked is
// derived, never stored.
func TestBlockedIsDerivedFromDeps(t *testing.T) {
	s := open(t)
	dep := create(t, s, "first")
	task, err := s.CreateTask(tasks.NewTaskInput{Project: proj, Title: "second", Deps: []string{dep.ID}}, operator, tick(t))
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	got, _ := s.GetTask(proj, task.ID)
	if !got.Blocked {
		t.Fatal("a task with an unfinished dep is blocked")
	}
	ready, err := s.ListTasks(TaskFilter{Project: proj, Ready: true})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(ready) != 1 || ready[0].ID != dep.ID {
		t.Fatalf("ready = %+v, want only the unblocked one", ready)
	}
	finish(t, s, dep)
	got, _ = s.GetTask(proj, task.ID)
	if got.Blocked {
		t.Fatal("a task whose deps are done is not blocked")
	}
}

func TestCreateRejectsCycleAndUnknownDep(t *testing.T) {
	s := open(t)
	a := create(t, s, "a")
	b, err := s.CreateTask(tasks.NewTaskInput{Project: proj, Title: "b", Deps: []string{a.ID}}, operator, tick(t))
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	deps := []string{b.ID}
	_, err = s.TaskTransition(proj, a.ID, 0, func(tk *tasks.Task) (tasks.Event, error) {
		return tasks.Update(tk, operator, tasks.UpdatePatch{Deps: &deps}, tick(t))
	})
	if got := codeOf(t, err); got != codes.Usage {
		t.Fatalf("cycle code = %q, want USAGE", got)
	}
	_, err = s.CreateTask(tasks.NewTaskInput{Project: proj, Title: "c", Deps: []string{"01ARZ3NDEKTSV4RRFFQ69G5FZZ"}}, operator, tick(t))
	if got := codeOf(t, err); got != codes.NotFound {
		t.Fatalf("unknown dep code = %q, want NOT_FOUND", got)
	}
}

// §4.4: a dependency across projects is an error.
func TestCrossProjectDepRejected(t *testing.T) {
	s := open(t)
	a := create(t, s, "here")
	_, err := s.CreateTask(tasks.NewTaskInput{Project: "/tmp/project-b", Title: "there", Deps: []string{a.ID}}, operator, tick(t))
	if got := codeOf(t, err); got != codes.Usage {
		t.Fatalf("code = %q, want USAGE", got)
	}
}

// §5.7: hard delete reaches only a never-claimed task; everything else is
// cancelled or archived.
func TestDeleteTaskOnlyNeverClaimed(t *testing.T) {
	s := open(t)
	task := create(t, s, "typo")
	if err := s.DeleteTask(proj, task.ID); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}
	if _, err := s.GetTask(proj, task.ID); codeOf(t, err) != codes.NotFound {
		t.Fatal("deleted task must be gone")
	}
	other := create(t, s, "real work")
	if _, err := s.TaskTransition(proj, other.ID, 0, func(tk *tasks.Task) (tasks.Event, error) {
		return tasks.Claim(tk, peer("wF:p1", "claude"), tick(t), 60_000)
	}); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if got := codeOf(t, s.DeleteTask(proj, other.ID)); got != codes.Conflict {
		t.Fatalf("code = %q, want CONFLICT", got)
	}
}

// §11.5: the sweep releases an expired lease and says so in the event trail.
func TestSweepLeasesReleasesExpiredOnly(t *testing.T) {
	s := open(t)
	live := create(t, s, "live")
	stale := create(t, s, "stale")
	at := tick(t)
	for _, tk := range []*tasks.Task{live, stale} {
		lease := int64(60_000)
		if tk == stale {
			lease = 1
		}
		if _, err := s.TaskTransition(proj, tk.ID, 0, func(x *tasks.Task) (tasks.Event, error) {
			return tasks.Claim(x, peer("wF:p1", "claude"), at, lease)
		}); err != nil {
			t.Fatalf("claim: %v", err)
		}
	}
	swept, err := s.SweepLeases(at + 1000)
	if err != nil {
		t.Fatalf("SweepLeases: %v", err)
	}
	if len(swept) != 1 || swept[0] != stale.ID {
		t.Fatalf("swept = %v, want only %s", swept, stale.ID)
	}
	got, _ := s.GetTask(proj, stale.ID)
	if got.Status != tasks.StatusTodo || got.ClaimedBy != "" {
		t.Fatalf("stale task not released: %+v", got)
	}
	evs, _ := s.Events(EventFilter{Project: proj, EntityID: stale.ID})
	if evs[len(evs)-1].Kind != "swept" {
		t.Fatalf("last event = %q, want swept", evs[len(evs)-1].Kind)
	}
}

// §5.5: notes get their own append-only table, written the same way.
func TestNoteLifecycleAndEvents(t *testing.T) {
	s := open(t)
	n, err := s.CreateNote(tasks.NewNoteInput{Project: proj, Body: "the sweep is quiet"}, peer("wF:p1", "claude"), tick(t))
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	if n.Seq != 1 {
		t.Fatalf("seq = %d", n.Seq)
	}
	if _, err := s.NoteTransition(proj, "1", 0, func(x *tasks.Note) (tasks.Event, error) {
		return tasks.NoteDiscuss(x, peer("wF:p1", "claude"), tick(t))
	}); err != nil {
		t.Fatalf("discuss: %v", err)
	}
	evs, _ := s.Events(EventFilter{Project: proj, Entity: "note"})
	if len(evs) != 2 || evs[1].Kind != "discussing" || evs[1].Entity != "note" {
		t.Fatalf("note events = %+v", evs)
	}
}

// §8.2: events stream in order and --since resumes from an id.
func TestEventsSinceResumes(t *testing.T) {
	s := open(t)
	create(t, s, "one")
	create(t, s, "two")
	all, _ := s.Events(EventFilter{Project: proj})
	if len(all) != 2 {
		t.Fatalf("events = %d", len(all))
	}
	rest, _ := s.Events(EventFilter{Project: proj, SinceID: all[0].ID})
	if len(rest) != 1 || rest[0].ID != all[1].ID {
		t.Fatalf("since = %+v", rest)
	}
}

// §5.8: dump prints the whole store, so the data is readable without us.
func TestDumpIsCompleteJSON(t *testing.T) {
	s := open(t)
	task := create(t, s, "dumped")
	if _, err := s.CreateNote(tasks.NewNoteInput{Project: proj, Body: "noted"}, operator, tick(t)); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	raw, err := s.Dump()
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}
	blob, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back struct {
		SchemaVersion int64 `json:"schema_version"`
		Tasks         []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"tasks"`
		Notes  []json.RawMessage `json:"notes"`
		Events []json.RawMessage `json:"events"`
	}
	if err := json.Unmarshal(blob, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.SchemaVersion != SchemaVersion {
		t.Fatalf("schema_version = %d", back.SchemaVersion)
	}
	if len(back.Tasks) != 1 || back.Tasks[0].ID != task.ID {
		t.Fatalf("tasks = %+v", back.Tasks)
	}
	if len(back.Notes) != 1 || len(back.Events) != 2 {
		t.Fatalf("notes = %d, events = %d", len(back.Notes), len(back.Events))
	}
}

// §9.3: a deferred verb is parked, and only the operator resolves it.
func TestParkedActionRoundTrip(t *testing.T) {
	s := open(t)
	id, err := s.Park(Parked{Project: proj, Subject: "agent:wF:p1", Verb: "tasks.claim", Target: "1", Payload: `{"lease_ms":60000}`}, tick(t))
	if err != nil {
		t.Fatalf("Park: %v", err)
	}
	list, err := s.ListParked(proj)
	if err != nil {
		t.Fatalf("ListParked: %v", err)
	}
	if len(list) != 1 || list[0].ID != id || list[0].State != "parked" {
		t.Fatalf("parked = %+v", list)
	}
	if err := s.ResolveParked(proj, id, "resolved", tick(t)); err != nil {
		t.Fatalf("ResolveParked: %v", err)
	}
	list, _ = s.ListParked(proj)
	if len(list) != 0 {
		t.Fatalf("a resolved action leaves the queue: %+v", list)
	}
}

// §4.4: list verbs default to one project and opt into all of them.
func TestListDefaultsToProject(t *testing.T) {
	s := open(t)
	create(t, s, "here")
	if _, err := s.CreateTask(tasks.NewTaskInput{Project: "/tmp/project-b", Title: "there"}, operator, tick(t)); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	mine, _ := s.ListTasks(TaskFilter{Project: proj})
	if len(mine) != 1 {
		t.Fatalf("scoped list = %d, want 1", len(mine))
	}
	all, _ := s.ListTasks(TaskFilter{AllProjects: true})
	if len(all) != 2 {
		t.Fatalf("--all-projects list = %d, want 2", len(all))
	}
}

func finish(t *testing.T, s *Store, task *tasks.Task) {
	t.Helper()
	a := peer("wF:p1", "claude")
	steps := []func(*tasks.Task) (tasks.Event, error){
		func(x *tasks.Task) (tasks.Event, error) { return tasks.Claim(x, a, tick(t), 60_000) },
		func(x *tasks.Task) (tasks.Event, error) { return tasks.Submit(x, a, "done", nil, tick(t)) },
		func(x *tasks.Task) (tasks.Event, error) { return tasks.Approve(x, operator, tick(t)) },
	}
	for i, step := range steps {
		if _, err := s.TaskTransition(task.Project, task.ID, 0, step); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}
}

// A % or _ that a human typed is a character, not a wildcard.
func TestQueryTreatsWildcardsAsLiterals(t *testing.T) {
	s := open(t)
	create(t, s, "cut latency by 50% on the hot path")
	create(t, s, "50 tasks imported")
	got, err := s.ListTasks(TaskFilter{Project: proj, Query: "50%"})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Title, "50%") {
		t.Fatalf("query \"50%%\" matched %d tasks, want only the one with a literal 50%%", len(got))
	}
	// A bare "%" is the character, so it matches the one title that has one —
	// not every task, which is what an unescaped wildcard would do.
	if all, _ := s.ListTasks(TaskFilter{Project: proj, Query: "%"}); len(all) != 1 {
		t.Fatalf("query \"%%\" matched %d tasks, want the 1 with a literal %%", len(all))
	}
}

// §8.2: the trail streams in insertion order. Several events on one entity
// inside a single millisecond is the ordinary case, not an edge case: a claim
// and its submit can land in the same tick.
func TestEventsKeepInsertionOrderWithinOneMillisecond(t *testing.T) {
	const frozen = int64(1_700_000_000_000)
	for trial := 0; trial < 40; trial++ {
		s := open(t)
		task, err := s.CreateTask(tasks.NewTaskInput{Project: proj, Title: "frozen"}, operator, frozen)
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
		a := peer("wF:p1", "claude")
		steps := []func(*tasks.Task) (tasks.Event, error){
			func(x *tasks.Task) (tasks.Event, error) { return tasks.Claim(x, a, frozen, 60_000) },
			func(x *tasks.Task) (tasks.Event, error) { return tasks.Submit(x, a, "r", nil, frozen) },
			func(x *tasks.Task) (tasks.Event, error) { return tasks.Approve(x, operator, frozen) },
		}
		for i, step := range steps {
			if _, err := s.TaskTransition(proj, task.ID, 0, step); err != nil {
				t.Fatalf("step %d: %v", i, err)
			}
		}
		evs, err := s.Events(EventFilter{Project: proj})
		if err != nil {
			t.Fatalf("Events: %v", err)
		}
		got := make([]string, 0, len(evs))
		for _, e := range evs {
			got = append(got, e.Kind)
		}
		if want := "created,claimed,submitted,approved"; strings.Join(got, ",") != want {
			t.Fatalf("trial %d: trail = %v, want %s", trial, got, want)
		}
		s.Close()
	}
}

// §8.2: --since resumes from an id, which only works if the ids of same-ms
// events order the same way the events happened.
func TestEventsSinceDoesNotSkipASameMillisecondEvent(t *testing.T) {
	const frozen = int64(1_700_000_000_000)
	s := open(t)
	task, err := s.CreateTask(tasks.NewTaskInput{Project: proj, Title: "frozen"}, operator, frozen)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	a := peer("wF:p1", "claude")
	if _, err := s.TaskTransition(proj, task.ID, 0, func(x *tasks.Task) (tasks.Event, error) {
		return tasks.Claim(x, a, frozen, 60_000)
	}); err != nil {
		t.Fatalf("claim: %v", err)
	}
	all, _ := s.Events(EventFilter{Project: proj})
	if len(all) != 2 {
		t.Fatalf("events = %d", len(all))
	}
	rest, _ := s.Events(EventFilter{Project: proj, SinceID: all[0].ID})
	if len(rest) != 1 || rest[0].Kind != "claimed" {
		t.Fatalf("--since dropped or duplicated a same-millisecond event: %+v", rest)
	}
}

// §6.2 / §6.1: `note list --query` is the same substring match `task list`
// already has — over the note's free text, which is its body AND the verdict
// reason. The reason is where "we dropped this, because…" is written, and the
// duplicate check the query exists for is looking for exactly that.
func TestNoteQueryMatchesBodyOrReasonAndComposesWithStatus(t *testing.T) {
	s := open(t)
	agent := peer("wF:p1", "claude")
	body := func(text string) *tasks.Note {
		t.Helper()
		n, err := s.CreateNote(tasks.NewNoteInput{Project: proj, Body: text}, agent, tick(t))
		if err != nil {
			t.Fatalf("CreateNote: %v", err)
		}
		return n
	}
	body("the pane popup swallows esc")
	body("cut latency by 50% on the hot path")
	decided := body("something about the socket")
	if _, err := s.NoteTransition(proj, decided.ID, 0, func(x *tasks.Note) (tasks.Event, error) {
		return tasks.NoteDrop(x, operator, "duplicate of the popup incident", tick(t))
	}); err != nil {
		t.Fatalf("drop: %v", err)
	}

	// Body match, and case-insensitively for ASCII, the way task list matches.
	for _, q := range []string{"popup", "POPUP", "Popup"} {
		got, err := s.ListNotes(NoteFilter{Project: proj, Query: q})
		if err != nil {
			t.Fatalf("ListNotes(%q): %v", q, err)
		}
		if len(got) != 2 {
			t.Fatalf("query %q matched %d notes, want the body and the drop reason", q, len(got))
		}
	}
	// Reason match alone: this note's body says nothing about a popup.
	got, _ := s.ListNotes(NoteFilter{Project: proj, Query: "duplicate of"})
	if len(got) != 1 || got[0].ID != decided.ID {
		t.Fatalf("a query over the drop reason matched %d notes, want the dropped one", len(got))
	}
	// Composes with --status as AND: both filters hold, not either.
	got, _ = s.ListNotes(NoteFilter{Project: proj, Status: string(tasks.NoteInbox), Query: "popup"})
	if len(got) != 1 || got[0].Status != tasks.NoteInbox {
		t.Fatalf("status+query matched %d notes, want only the inbox one", len(got))
	}
	got, _ = s.ListNotes(NoteFilter{Project: proj, Status: string(tasks.NoteDropped), Query: "esc"})
	if len(got) != 0 {
		t.Fatalf("status+query matched %d notes; a note that fails either half must not be listed", len(got))
	}
	// A % or _ a human typed is a character, not a wildcard.
	if got, _ = s.ListNotes(NoteFilter{Project: proj, Query: "%"}); len(got) != 1 {
		t.Fatalf("query \"%%\" matched %d notes, want the 1 with a literal %%", len(got))
	}
	if got, _ = s.ListNotes(NoteFilter{Project: proj, Query: "_"}); len(got) != 0 {
		t.Fatalf("query \"_\" matched %d notes, want none: no body has a literal underscore", len(got))
	}
}

// claimBy puts a lease on a task the way the daemon does, so the sweep tests
// can set up an interleaving without going through a door.
func claimBy(t *testing.T, s *Store, id string, by tasks.Actor, at, leaseMS int64) {
	t.Helper()
	if _, err := s.TaskTransition(proj, id, 0, func(x *tasks.Task) (tasks.Event, error) {
		return tasks.Claim(x, by, at, leaseMS)
	}); err != nil {
		t.Fatalf("claim: %v", err)
	}
}

// kinds is the event trail of one entity, in order.
func kinds(t *testing.T, s *Store, id string) []string {
	t.Helper()
	evs, err := s.Events(EventFilter{Project: proj, EntityID: id})
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	out := make([]string, 0, len(evs))
	for _, e := range evs {
		out = append(out, e.Kind)
	}
	return out
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// §11.5: the sweep scans stale ids in one query and releases each in its own
// transaction, so a lease renewed in between must survive. Release with
// KindSwept bypasses the holder check by design (tasks/task.go:245), which
// means the only thing that can defend the renewal is a re-check inside the
// closure.
func TestSweepLeavesALeaseRenewedAfterTheScanAlone(t *testing.T) {
	s := open(t)
	task := create(t, s, "renewed under the sweep")
	holder := peer("wF:p1", "claude")
	at := tick(t)
	claimBy(t, s, task.ID, holder, at, 1)

	now := at + 1000
	renewed := int64(0)
	s.DuringSweep = func(id string) {
		s.DuringSweep = nil
		got, err := s.TaskTransition(proj, id, 0, func(x *tasks.Task) (tasks.Event, error) {
			return tasks.Touch(x, holder, now, 900_000)
		})
		if err != nil {
			t.Fatalf("touch: %v", err)
		}
		renewed = got.LeaseUntil
	}

	swept, err := s.SweepLeases(now)
	if err != nil {
		t.Fatalf("SweepLeases: %v", err)
	}
	if len(swept) != 0 {
		t.Fatalf("swept = %v, want nothing: the lease was renewed before the release", swept)
	}
	got, err := s.GetTask(proj, task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Status != tasks.StatusDoing {
		t.Fatalf("status = %q, want doing", got.Status)
	}
	if got.ClaimedBy != holder.Principal {
		t.Fatalf("claimed_by = %q, want %q", got.ClaimedBy, holder.Principal)
	}
	if got.LeaseUntil != renewed {
		t.Fatalf("lease_until = %d, want the renewed %d", got.LeaseUntil, renewed)
	}
	if k := kinds(t, s, task.ID); contains(k, "swept") {
		t.Fatalf("the trail says it was swept: %v", k)
	}
}

// §11.5: one renewed row must not stop the sweep doing its job on the rest.
func TestSweepStillReleasesTheStaleOneInAMixedBatch(t *testing.T) {
	s := open(t)
	renewed := create(t, s, "renewed")
	abandoned := create(t, s, "abandoned")
	holder := peer("wF:p1", "claude")
	at := tick(t)
	claimBy(t, s, renewed.ID, holder, at, 1)
	claimBy(t, s, abandoned.ID, holder, at, 1)

	now := at + 1000
	s.DuringSweep = func(id string) {
		if id != renewed.ID {
			return
		}
		if _, err := s.TaskTransition(proj, id, 0, func(x *tasks.Task) (tasks.Event, error) {
			return tasks.Touch(x, holder, now, 900_000)
		}); err != nil {
			t.Fatalf("touch: %v", err)
		}
	}

	swept, err := s.SweepLeases(now)
	if err != nil {
		t.Fatalf("SweepLeases: %v", err)
	}
	if len(swept) != 1 || swept[0] != abandoned.ID {
		t.Fatalf("swept = %v, want only the abandoned %s", swept, abandoned.ID)
	}
	gone, _ := s.GetTask(proj, abandoned.ID)
	if gone.Status != tasks.StatusTodo || gone.ClaimedBy != "" {
		t.Fatalf("the abandoned task was not released: %+v", gone)
	}
	kept, _ := s.GetTask(proj, renewed.ID)
	if kept.Status != tasks.StatusDoing || kept.ClaimedBy != holder.Principal {
		t.Fatalf("the renewed task was released: %+v", kept)
	}
}

// §11.5: `pane.exited` releases what THAT pane holds. A task another pane
// claimed between the scan and the release is not that pane's to give back.
func TestReleaseByPaneLeavesATaskAnotherPaneReclaimed(t *testing.T) {
	s := open(t)
	task := create(t, s, "handed over mid-sweep")
	gone := peer("wF:p1", "claude")
	next := peer("wF:p2", "codex")
	at := tick(t)
	claimBy(t, s, task.ID, gone, at, 900_000)

	now := at + 5
	s.DuringSweep = func(id string) {
		s.DuringSweep = nil
		if _, err := s.TaskTransition(proj, id, 0, func(x *tasks.Task) (tasks.Event, error) {
			return tasks.Release(x, operator, "pane went away", now, tasks.KindReleased)
		}); err != nil {
			t.Fatalf("release: %v", err)
		}
		claimBy(t, s, id, next, now, 900_000)
	}

	released, err := s.ReleaseByPane("wF:p1", now)
	if err != nil {
		t.Fatalf("ReleaseByPane: %v", err)
	}
	if len(released) != 0 {
		t.Fatalf("released = %v, want nothing: the task belongs to another pane now", released)
	}
	got, _ := s.GetTask(proj, task.ID)
	if got.Status != tasks.StatusDoing {
		t.Fatalf("status = %q, want doing", got.Status)
	}
	if got.ClaimedBy != next.Principal {
		t.Fatalf("claimed_by = %q, want the new claimer %q", got.ClaimedBy, next.Principal)
	}
	if k := kinds(t, s, task.ID); contains(k, "swept") {
		t.Fatalf("the trail says it was swept: %v", k)
	}
}

// §5.7 with §5.8: a task others depend on does not hard-delete. The dep edge
// carries ON DELETE CASCADE and foreign keys are enforced, so deleting the
// dependency took the edge with it and its dependent quietly became ready —
// no event on the dependent, nothing in its trail saying why the thing it was
// waiting for stopped mattering. Meanwhile CANCELLING that same dependency
// blocks the dependent for good. The two removal paths pulled opposite ways
// and the destructive one was the silent one.
func TestATaskWithDependentsIsNotDeleted(t *testing.T) {
	s := open(t)
	blocker := create(t, s, "must happen first")
	dependent := create(t, s, "waits for it")
	if _, err := s.TaskTransition(proj, dependent.ID, 0, func(x *tasks.Task) (tasks.Event, error) {
		x.Deps = []string{blocker.ID}
		return tasks.Update(x, operator, tasks.UpdatePatch{Deps: &x.Deps}, tick(t))
	}); err != nil {
		t.Fatalf("add the edge: %v", err)
	}

	err := s.DeleteTask(proj, blocker.ID)
	if got := codeOf(t, err); got != codes.Conflict {
		t.Fatalf("code = %q, want CONFLICT", got)
	}
	if !strings.Contains(err.Error(), fmt.Sprint(dependent.Seq)) {
		t.Fatalf("the refusal does not name the dependent: %v", err)
	}

	// The dependent is untouched: still blocked, still carrying the edge, and
	// nothing new in its trail.
	got, err := s.GetTask(proj, dependent.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if !got.Blocked {
		t.Fatal("the dependent stopped being blocked")
	}
	if len(got.Deps) != 1 || got.Deps[0] != blocker.ID {
		t.Fatalf("the edge went: %v", got.Deps)
	}
	if k := kinds(t, s, dependent.ID); len(k) != 2 {
		t.Fatalf("the dependent's trail changed: %v", k)
	}

	// And the refusal is a step, not a dead end: drop the edge, then delete.
	none := []string{}
	if _, err := s.TaskTransition(proj, dependent.ID, 0, func(x *tasks.Task) (tasks.Event, error) {
		return tasks.Update(x, operator, tasks.UpdatePatch{Deps: &none}, tick(t))
	}); err != nil {
		t.Fatalf("drop the edge: %v", err)
	}
	if err := s.DeleteTask(proj, blocker.ID); err != nil {
		t.Fatalf("delete after dropping the edge: %v", err)
	}
}

// §5.8: only `done` satisfies a dependency, so a CANCELLED one blocks for
// good. That is the right rule — a prerequisite that was abandoned is not one
// that was met — but it was invisible, which turned a decision into a wall.
func TestADependentOfACancelledTaskSaysSo(t *testing.T) {
	s := open(t)
	blocker := create(t, s, "will be cancelled")
	pending := create(t, s, "not done yet")
	dependent := create(t, s, "waits for both")
	deps := []string{blocker.ID, pending.ID}
	if _, err := s.TaskTransition(proj, dependent.ID, 0, func(x *tasks.Task) (tasks.Event, error) {
		return tasks.Update(x, operator, tasks.UpdatePatch{Deps: &deps}, tick(t))
	}); err != nil {
		t.Fatalf("add the edges: %v", err)
	}

	// Before the cancel: blocked, and nothing abandoned.
	got, _ := s.GetTask(proj, dependent.ID)
	if !got.Blocked || len(got.Abandoned) != 0 {
		t.Fatalf("an ordinary unfinished dependency must not look abandoned: %+v", got)
	}

	if _, err := s.TaskTransition(proj, blocker.ID, 0, func(x *tasks.Task) (tasks.Event, error) {
		return tasks.Cancel(x, operator, "not doing this", tick(t))
	}); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	got, _ = s.GetTask(proj, dependent.ID)
	if !got.Blocked {
		t.Fatal("a cancelled dependency must still block")
	}
	if len(got.Abandoned) != 1 || got.Abandoned[0] != blocker.Seq {
		t.Fatalf("abandoned = %v, want only #%d", got.Abandoned, blocker.Seq)
	}
	// The list version answers the same way, so a board and a get agree.
	list, err := s.ListTasks(TaskFilter{Project: proj})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	for _, x := range list {
		if x.ID != dependent.ID {
			continue
		}
		if len(x.Abandoned) != 1 || x.Abandoned[0] != blocker.Seq {
			t.Fatalf("the list says abandoned = %v", x.Abandoned)
		}
	}
}

// §6.3: "it is not there" and "I could not look" are different answers, and
// telling the operator the first when the second is true is the worst kind of
// wrong — during `parked resolve` it says the action they are looking at has
// gone. readTask and readNote already tell them apart; GetParked mapped EVERY
// failure to NOT_FOUND.
func TestGetParkedTellsMissingFromBroken(t *testing.T) {
	s := open(t)
	id, err := s.Park(Parked{Project: proj, Subject: "agent:wF:p1", Verb: "tasks.approve",
		Target: "#1", Reason: "the gate said no"}, tick(t))
	if err != nil {
		t.Fatalf("Park: %v", err)
	}
	if _, err := s.GetParked(proj, id); err != nil {
		t.Fatalf("precondition: the row is there: %v", err)
	}

	// A genuinely absent id: unchanged.
	_, err = s.GetParked(proj, "01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if got := codeOf(t, err); got != codes.NotFound {
		t.Fatalf("an absent id: code = %q, want NOT_FOUND", got)
	}
	if !strings.Contains(err.Error(), "01ARZ3NDEKTSV4RRFFQ69G5FAV") {
		t.Fatalf("the message does not name what was missing: %v", err)
	}

	// Now break the database under it. The row still exists; the read does not.
	s.Close()
	_, err = s.GetParked(proj, id)
	if err == nil {
		t.Fatal("a read on a closed database must fail")
	}
	if got := codeOf(t, err); got == codes.NotFound {
		t.Fatalf("a broken read is reported as a missing row: %v", err)
	}
	if !strings.Contains(err.Error(), "closed") && !strings.Contains(err.Error(), "done") {
		t.Fatalf("the error does not carry what the database said: %v", err)
	}
}

// §6.3, kept from coming back: a read helper that maps a failure to NOT_FOUND
// has to say WHICH failure. Source-level, because the point is that the next
// helper written in this package cannot quietly repeat it.
func TestEveryNotFoundInThisPackageDistinguishesNoRows(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			body := string(src[fn.Body.Pos()-1 : fn.Body.End()])
			if !strings.Contains(body, "codes.NotFound") {
				continue
			}
			checked++
			// A function that reads a row and answers NOT_FOUND must be
			// answering it for the no-rows case specifically.
			if !strings.Contains(body, "sql.ErrNoRows") {
				t.Errorf("%s: %s answers NOT_FOUND without telling no-rows from any other failure",
					name, fn.Name.Name)
			}
		}
	}
	if checked < 3 {
		t.Fatalf("only %d functions answer NOT_FOUND; the sweep is not finding them", checked)
	}
}
