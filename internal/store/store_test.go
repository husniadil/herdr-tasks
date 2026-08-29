package store

import (
	"database/sql"
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
	if err := s.DeleteTask(proj, task.ID, operator, tick(t)); err != nil {
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
	if got := codeOf(t, s.DeleteTask(proj, other.ID, operator, tick(t))); got != codes.Conflict {
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
	if err := s.ResolveParked(proj, id, "resolved", tasks.Actor{Principal: "agent:wF:p1"}, tick(t)); err != nil {
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
		func(x *tasks.Task) (tasks.Event, error) { return tasks.Submit(x, a, "done", nil, nil, tick(t)) },
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
			func(x *tasks.Task) (tasks.Event, error) { return tasks.Submit(x, a, "r", nil, nil, frozen) },
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

	released, err := s.ReleaseByPane("wF:p1", "pane exited", now)
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

	err := s.DeleteTask(proj, blocker.ID, operator, tick(t))
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
	if err := s.DeleteTask(proj, blocker.ID, operator, tick(t)); err != nil {
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

// leftAlignedID renders an id with the 128 bits LEFT-aligned in the 26
// characters. It is the input migration 3 takes, and nothing in the shipped
// code produces it — which is the point.
func leftAlignedID(ms int64, tail byte) string {
	const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	var raw [16]byte
	for i := 0; i < 6; i++ {
		raw[i] = byte(ms >> uint(40-8*i))
	}
	raw[15] = tail
	out := make([]byte, 26)
	for i := 0; i < 26; i++ {
		bit := i * 5
		acc := uint16(raw[bit/8]) << 8
		if bit/8+1 < 16 {
			acc |= uint16(raw[bit/8+1])
		}
		out[i] = alphabet[(acc>>(11-uint(bit%8)))&0x1f]
	}
	return string(out)
}

// v2Store builds a database at schema version 2 — the version whose ids are
// left-aligned — and fills it with a graph that touches every column an id
// lives in: two tasks with a dependency between them, a note promoted to one
// of them, a parked action, and events in both trails. It returns the path.
func v2Store(t *testing.T) (path string, byLabel map[string]string) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "tasks.db")
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	for i := 0; i < 2; i++ {
		if _, err := db.Exec(migrations[i].SQL); err != nil {
			t.Fatalf("migration %d: %v", i+1, err)
		}
	}
	if _, err := db.Exec("INSERT INTO meta (schema_version, created_at) VALUES (2, 1)"); err != nil {
		t.Fatalf("stamp: %v", err)
	}

	at := int64(1_787_225_085_000)
	byLabel = map[string]string{
		"blocker":   leftAlignedID(at, 1),
		"dependent": leftAlignedID(at+1, 2),
		"note":      leftAlignedID(at+2, 3),
		"parked":    leftAlignedID(at+3, 4),
		"ev1":       leftAlignedID(at+4, 5),
		"ev2":       leftAlignedID(at+5, 6),
		"nev1":      leftAlignedID(at+6, 7),
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for label, seq := range map[string]int{"blocker": 1, "dependent": 2} {
		exec(`INSERT INTO tasks (id, seq, project, title, status, priority, created_by, created_at, updated_at)
		      VALUES (?, ?, ?, ?, 'todo', 0, 'human', ?, ?)`,
			byLabel[label], seq, proj, label, at, at)
	}
	// The per-project counters, so a task created after the migration does not
	// collide with the seqs this fixture wrote by hand.
	exec("INSERT INTO seqs (project, entity, next) VALUES (?, 'task', 3)", proj)
	exec("INSERT INTO seqs (project, entity, next) VALUES (?, 'note', 2)", proj)
	exec("INSERT INTO task_deps (task_id, depends_on_id) VALUES (?, ?)",
		byLabel["dependent"], byLabel["blocker"])
	exec(`INSERT INTO notes (id, seq, project, body, status, author, task_id, created_at, updated_at)
	      VALUES (?, 1, ?, 'promoted already', 'task', 'human', ?, ?, ?)`,
		byLabel["note"], proj, byLabel["blocker"], at, at)
	exec(`INSERT INTO parked (id, project, subject, verb, target, payload, state, created_at)
	      VALUES (?, ?, 'agent:wF:p1', 'tasks.approve', '#1', '{}', 'parked', ?)`,
		byLabel["parked"], proj, at)
	exec(`INSERT INTO tasks_events (id, entity_id, project, at, actor, kind, detail)
	      VALUES (?, ?, ?, ?, 'human', 'created', '{}')`, byLabel["ev1"], byLabel["blocker"], proj, at)
	exec(`INSERT INTO tasks_events (id, entity_id, project, at, actor, kind, detail)
	      VALUES (?, ?, ?, ?, 'human', 'created', '{}')`, byLabel["ev2"], byLabel["dependent"], proj, at+1)
	exec(`INSERT INTO notes_events (id, entity_id, project, at, actor, kind, detail)
	      VALUES (?, ?, ?, ?, 'human', 'added', '{}')`, byLabel["nev1"], byLabel["note"], proj, at)
	return path, byLabel
}

// shape reads every id-bearing row as a structure with the ids replaced by
// stable labels, so a before and an after can be compared for everything
// EXCEPT the ids — which is exactly what the migration is allowed to change.
func shape(t *testing.T, db *sql.DB, label map[string]string) string {
	t.Helper()
	name := map[string]string{}
	for k, v := range label {
		name[v] = k
	}
	// Post-migration ids are not in the label map, so they are named by the
	// row they belong to instead: what matters is that the RELATIONSHIPS are
	// the same, not what the strings are.
	rename := func(id string) string {
		if n, ok := name[id]; ok {
			return n
		}
		return "?" + id[:0] + "" // an id nobody knows: rendered as a blank
	}
	var b strings.Builder
	for _, q := range []struct{ label, sql string }{
		{"task", "SELECT seq, title, status FROM tasks ORDER BY seq"},
		{"dep", "SELECT t.seq, d.seq FROM task_deps x JOIN tasks t ON t.id = x.task_id JOIN tasks d ON d.id = x.depends_on_id ORDER BY t.seq"},
		{"note", "SELECT n.seq, n.body, t.seq FROM notes n JOIN tasks t ON t.id = n.task_id ORDER BY n.seq"},
		{"parked", "SELECT verb, target, state FROM parked ORDER BY created_at"},
		{"tev", "SELECT t.seq, e.kind, e.at FROM tasks_events e JOIN tasks t ON t.id = e.entity_id ORDER BY e.at"},
		{"nev", "SELECT n.seq, e.kind, e.at FROM notes_events e JOIN notes n ON n.id = e.entity_id ORDER BY e.at"},
	} {
		rows, err := db.Query(q.sql)
		if err != nil {
			t.Fatalf("%s: %v", q.label, err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			cells := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range cells {
				ptrs[i] = &cells[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				rows.Close()
				t.Fatalf("%s scan: %v", q.label, err)
			}
			fmt.Fprintf(&b, "%s %v\n", q.label, cells)
		}
		rows.Close()
	}
	_ = rename
	return b.String()
}

// §5.4: every stored id moves, in one transaction, and the graph still holds
// together afterwards.
func TestMigrationReencodesEveryStoredID(t *testing.T) {
	path, label := v2Store(t)

	before, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	beforeShape := shape(t, before, label)
	before.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open (which migrates): %v", err)
	}
	defer s.Close()

	var version int64
	if err := s.db.QueryRow("SELECT schema_version FROM meta").Scan(&version); err != nil {
		t.Fatalf("version: %v", err)
	}
	if version != SchemaVersion {
		t.Fatalf("schema_version = %d, want %d", version, SchemaVersion)
	}

	// Every id changed, and no left-aligned one is left anywhere.
	for _, c := range idColumns {
		rows, err := s.db.Query(fmt.Sprintf("SELECT %s FROM %s WHERE %s IS NOT NULL AND %s != ''",
			c.column, c.table, c.column, c.column))
		if err != nil {
			t.Fatalf("%s.%s: %v", c.table, c.column, err)
		}
		for rows.Next() {
			var got string
			if err := rows.Scan(&got); err != nil {
				rows.Close()
				t.Fatalf("scan: %v", err)
			}
			for name, old := range label {
				if got == old {
					rows.Close()
					t.Fatalf("%s.%s still holds the left-aligned id for %s: %s", c.table, c.column, name, got)
				}
			}
		}
		rows.Close()
	}

	// Nothing dangles. The migration asks this inside its own transaction and
	// refuses to commit if it fails; this asks it again from outside, over a
	// wider set — notes.task_id is a reference the migration's own check does
	// not cover because it is nullable.
	for _, ref := range []struct{ table, column, parent string }{
		{"task_deps", "task_id", "tasks"},
		{"task_deps", "depends_on_id", "tasks"},
		{"tasks_events", "entity_id", "tasks"},
		{"notes_events", "entity_id", "notes"},
		{"notes", "task_id", "tasks"},
	} {
		var n int
		q := fmt.Sprintf("SELECT COUNT(*) FROM %s r WHERE r.%s IS NOT NULL AND r.%s != '' AND NOT EXISTS (SELECT 1 FROM %s p WHERE p.id = r.%s)",
			ref.table, ref.column, ref.column, ref.parent, ref.column)
		if err := s.db.QueryRow(q).Scan(&n); err != nil {
			t.Fatalf("%s.%s: %v", ref.table, ref.column, err)
		}
		if n > 0 {
			t.Fatalf("%d rows of %s.%s point at no %s", n, ref.table, ref.column, ref.parent)
		}
	}

	// §6.2 in spirit: the same rows with the same relationships, differing
	// only in the id strings.
	if got := shape(t, s.db, label); got != beforeShape {
		t.Fatalf("the migration changed more than the ids:\nbefore:\n%safter:\n%s", beforeShape, got)
	}

	// §6.2 through the verb an operator would use: dump still renders the
	// whole store, with the note still pointing at the task it was promoted
	// into and the dependency still joining two tasks that exist.
	d, err := s.Dump()
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}
	if len(d.Tasks) != 2 || len(d.Notes) != 1 || len(d.Parked) != 1 {
		t.Fatalf("dump lost rows: %d tasks, %d notes, %d parked", len(d.Tasks), len(d.Notes), len(d.Parked))
	}
	byID := map[string]*tasks.Task{}
	for _, x := range d.Tasks {
		byID[x.ID] = x
	}
	if origin := byID[d.Notes[0].TaskID]; origin == nil {
		t.Fatalf("the promoted note points at %q, which is no task in the dump", d.Notes[0].TaskID)
	}
	deps := 0
	for _, x := range d.Tasks {
		for _, dep := range x.Deps {
			if byID[dep] == nil {
				t.Fatalf("task %d depends on %q, which is no task in the dump", x.Seq, dep)
			}
			deps++
		}
	}
	if deps != 1 {
		t.Fatalf("the dependency edge did not survive: %d edges", deps)
	}

	// And the ids are now readable as ULIDs: the trail's first event still
	// carries the millisecond it was written at.
	var first string
	if err := s.db.QueryRow("SELECT id FROM tasks_events ORDER BY at ASC LIMIT 1").Scan(&first); err != nil {
		t.Fatalf("read an event id: %v", err)
	}
	if ms := ulidMS(t, first); ms != 1_787_225_085_004 {
		t.Fatalf("the migrated event id reads as %d, want the millisecond it was minted at", ms)
	}
}

// ulidMS reads the timestamp out of a spec ULID, from the spec.
func ulidMS(t *testing.T, id string) int64 {
	t.Helper()
	const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	hi, lo := uint64(0), uint64(0)
	for i := 0; i < len(id); i++ {
		v := strings.IndexByte(alphabet, id[i])
		if v < 0 {
			t.Fatalf("%q is not a Crockford string", id)
		}
		hi = hi<<5 | lo>>59
		lo = lo<<5 | uint64(v)
	}
	return int64(hi >> 16)
}

// §5.2: the migration runs once. Opening again is a no-op, not a second
// re-encode — which would be the one thing that could still break the trail's
// order, since it would move some ids and not others.
func TestTheReencodeRunsOnce(t *testing.T) {
	path, _ := v2Store(t)
	first, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ids := map[string][]string{}
	read := func(s *Store) map[string][]string {
		out := map[string][]string{}
		for _, c := range idColumns {
			rows, err := s.db.Query(fmt.Sprintf("SELECT %s FROM %s WHERE %s IS NOT NULL AND %s != '' ORDER BY 1",
				c.column, c.table, c.column, c.column))
			if err != nil {
				t.Fatalf("%s.%s: %v", c.table, c.column, err)
			}
			key := c.table + "." + c.column
			for rows.Next() {
				var v string
				if err := rows.Scan(&v); err != nil {
					rows.Close()
					t.Fatalf("scan: %v", err)
				}
				out[key] = append(out[key], v)
			}
			rows.Close()
		}
		return out
	}
	ids = read(first)
	first.Close()

	second, err := Open(path)
	if err != nil {
		t.Fatalf("re-open: %v", err)
	}
	defer second.Close()
	again := read(second)
	if fmt.Sprint(again) != fmt.Sprint(ids) {
		t.Fatalf("opening again moved the ids:\nfirst:  %v\nsecond: %v", ids, again)
	}
}

// §8.2: the trail's order survives the boundary. Events written before the
// migration and after it come back in the order they happened, and --since
// with a pre-migration id returns exactly what followed it.
func TestTheTrailKeepsItsOrderAcrossTheMigration(t *testing.T) {
	path, _ := v2Store(t)
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	// Two events from BEFORE the migration are already there. Add one after,
	// minted by the new encoder at a later millisecond.
	later := create(t, s, "written after the migration")
	_ = later

	evs, err := s.Events(EventFilter{Project: proj, Entity: "task"})
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(evs) < 3 {
		t.Fatalf("expected the two migrated events and the new one, got %d", len(evs))
	}
	for i := 1; i < len(evs); i++ {
		if evs[i-1].At > evs[i].At {
			t.Fatalf("the trail came back out of order at %d: %d then %d", i, evs[i-1].At, evs[i].At)
		}
		if evs[i-1].ID >= evs[i].ID {
			t.Fatalf("event ids do not sort in the order they happened: %q then %q", evs[i-1].ID, evs[i].ID)
		}
	}

	// --since a PRE-migration id returns exactly what followed it.
	since := evs[0].ID
	after, err := s.Events(EventFilter{Project: proj, Entity: "task", SinceID: since})
	if err != nil {
		t.Fatalf("Events since: %v", err)
	}
	if len(after) != len(evs)-1 {
		t.Fatalf("--since the first event returned %d, want %d", len(after), len(evs)-1)
	}
	for i, e := range after {
		if e.ID != evs[i+1].ID {
			t.Fatalf("--since returned %q at %d, want %q", e.ID, i, evs[i+1].ID)
		}
	}
}

// §5.2 and the additive half of §14 evidence: citations arrived in a NEW
// column, so a row a daemon wrote before it existed — a bare JSON array of
// strings in `evidence`, nothing in `evidence_for` — must still read back
// exactly as it did, with no citations and no decode error. This is the test
// that would catch someone "upgrading" the evidence column in place.
func TestEvidenceForFlatRows(t *testing.T) {
	s := open(t)
	a := peer("wF:p1", "claude")

	legacy := create(t, s, "written by the old daemon")
	if _, err := s.db.Exec(
		`UPDATE tasks SET status = 'review', report = 'done', evidence = ?, evidence_for = NULL WHERE id = ?`,
		`["a","b"]`, legacy.ID); err != nil {
		t.Fatalf("planting the legacy row: %v", err)
	}
	got, err := s.GetTask(proj, legacy.ID)
	if err != nil {
		t.Fatalf("GetTask on a legacy row: %v", err)
	}
	if len(got.Evidence) != 2 || got.Evidence[0] != "a" || got.Evidence[1] != "b" {
		t.Fatalf("flat evidence did not survive: %+v", got.Evidence)
	}
	if len(got.EvidenceFor) != 0 {
		t.Fatalf("a legacy row invented citations: %+v", got.EvidenceFor)
	}

	// And the same store round-trips a row that DOES carry citations, so the
	// empty case above is back-compat rather than a column nobody writes.
	fresh, err := s.CreateTask(tasks.NewTaskInput{Project: proj, Title: "written by this daemon",
		Validation: []tasks.Criterion{{Text: "the gate is green", Required: true}}}, operator, tick(t))
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	for _, step := range []func(*tasks.Task) (tasks.Event, error){
		func(x *tasks.Task) (tasks.Event, error) { return tasks.Claim(x, a, tick(t), 60_000) },
		func(x *tasks.Task) (tasks.Event, error) {
			return tasks.Submit(x, a, "done", []string{"make build: ok"},
				[]string{"1: make test-full -> exit 0"}, tick(t))
		},
	} {
		if _, err := s.TaskTransition(proj, fresh.ID, 0, step); err != nil {
			t.Fatalf("transition: %v", err)
		}
	}
	back, err := s.GetTask(proj, fresh.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if len(back.EvidenceFor) != 1 || back.EvidenceFor[0].Criterion != 1 ||
		back.EvidenceFor[0].Text != "make test-full -> exit 0" {
		t.Fatalf("citations did not round-trip: %+v", back.EvidenceFor)
	}
	if len(back.Evidence) != 1 || back.Evidence[0] != "make build: ok" {
		t.Fatalf("task-level evidence did not round-trip: %+v", back.Evidence)
	}
}

// §11.5: the sweep takes a lease back from work that was ABANDONED. Work that
// was submitted was handed off, and a swept event written about it says the
// daemon took work from someone who stopped — a false statement in a table
// nothing ever rewrites. The submitted row must be out of the sweep's reach.
func TestSweepCannotReachASubmittedTask(t *testing.T) {
	s := open(t)
	a := peer("wF:p1", "claude")
	task := create(t, s, "handed off, not abandoned")
	for i, step := range []func(*tasks.Task) (tasks.Event, error){
		func(x *tasks.Task) (tasks.Event, error) { return tasks.Claim(x, a, tick(t), 60_000) },
		func(x *tasks.Task) (tasks.Event, error) { return tasks.Submit(x, a, "done", nil, nil, tick(t)) },
	} {
		if _, err := s.TaskTransition(proj, task.ID, 0, step); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}
	// Far past any lease this task could have been holding.
	released, err := s.SweepLeases(clock + 10_000_000)
	if err != nil {
		t.Fatalf("SweepLeases: %v", err)
	}
	if len(released) != 0 {
		t.Fatalf("the sweep took back %d submitted task(s): %v", len(released), released)
	}
	// Read the absence from the trail, not only from the return value: a sweep
	// that released nothing and a sweep that wrote nothing are two claims.
	evs, err := s.Events(EventFilter{Project: proj})
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	for _, e := range evs {
		if e.Kind == "swept" {
			t.Fatalf("a swept event was written about work that was submitted: %+v", e)
		}
	}
	got, err := s.GetTask(proj, task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Status != tasks.StatusReview || got.ClaimedBy != a.Principal {
		t.Fatalf("the submitted task moved: status=%q claimed_by=%q", got.Status, got.ClaimedBy)
	}
	if got.LeaseUntil != 0 {
		t.Fatalf("lease_until = %d on a submitted task, want 0", got.LeaseUntil)
	}
}

// §11.5: the same guarantee as TestSweepCannotReachASubmittedTask, on the
// other sweep path. `pane.exited` says the pane stopped, not that the work
// was abandoned: the submit already handed it off and ended the lease, so
// there is no lease left for the dead pane to give back. Seen live: event
// 01M0HCFFY63VE0VJVTRQM6P3MR wrote kind=swept detail "pane exited" on a task
// that was already in review and cleared its holder, which the timer sweep
// has been unable to do since the lease ends at submit.
func TestReleaseByPaneCannotReachASubmittedTask(t *testing.T) {
	s := open(t)
	a := peer("wF:p1", "claude")
	task := create(t, s, "submitted, then the pane died")
	for i, step := range []func(*tasks.Task) (tasks.Event, error){
		func(x *tasks.Task) (tasks.Event, error) { return tasks.Claim(x, a, tick(t), 60_000) },
		func(x *tasks.Task) (tasks.Event, error) { return tasks.Submit(x, a, "done", nil, nil, tick(t)) },
	} {
		if _, err := s.TaskTransition(proj, task.ID, 0, step); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}
	released, err := s.ReleaseByPane("wF:p1", "pane exited", tick(t))
	if err != nil {
		t.Fatalf("ReleaseByPane: %v", err)
	}
	if len(released) != 0 {
		t.Fatalf("the pane sweep took back %d submitted task(s): %v", len(released), released)
	}
	// The absence has to be read from the trail too: releasing nothing and
	// writing nothing are two claims, and only the second one is append-only.
	if k := kinds(t, s, task.ID); contains(k, "swept") {
		t.Fatalf("the trail says the submitted task was swept: %v", k)
	}
	got, err := s.GetTask(proj, task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Status != tasks.StatusReview || got.ClaimedBy != a.Principal {
		t.Fatalf("the submitted task moved: status=%q claimed_by=%q", got.Status, got.ClaimedBy)
	}
}

// §5.2 with §6.6 (0.6.0): migration 5 adds submitted_by_session beside the
// harness. A store written before it holds submitted rows with no session at
// all, and those rows must read back exactly as they did, with an empty
// session rather than a guessed one.
func TestMigrationAddsSubmittedSessionAndKeepsOldRowsReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.db")
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for i := 0; i < 4; i++ { // migrations 1..4: schema version 4, which has no session column
		if migrations[i].SQL == "" {
			continue
		}
		if _, err := db.Exec(migrations[i].SQL); err != nil {
			t.Fatalf("migration %d: %v", i+1, err)
		}
	}
	if _, err := db.Exec("INSERT INTO meta (schema_version, created_at) VALUES (4, 1)"); err != nil {
		t.Fatalf("stamp: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO tasks
		(id, seq, project, title, status, priority, created_by, created_at, updated_at,
		 claimed_by, claimed_by_harness, claimed_by_session, ever_claimed,
		 report, submitted_by, submitted_by_harness, submitted_at)
		VALUES ('01ARZ3NDEKTSV4RRFFQ69G5FAV', 1, '/p', 'old work', 'review', 0, 'human', 1, 2,
		        'agent:wF:p1', 'claude', 'sess-old', 1, 'done', 'agent:wF:p1', 'claude', 3)`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open (which migrates): %v", err)
	}
	defer s.Close()

	got, err := s.GetTask("/p", "1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Title != "old work" || got.Report != "done" || got.SubmittedByHarness != "claude" {
		t.Fatalf("the pre-migration row did not survive: %+v", got)
	}
	if got.SubmittedBySession != "" {
		t.Fatalf("submitted_by_session = %q, want empty: the old row never had one", got.SubmittedBySession)
	}
	// And the column is writable now, on the same file.
	if _, err := s.db.Exec("UPDATE tasks SET submitted_by_session = 'sess-new' WHERE seq = 1"); err != nil {
		t.Fatalf("write the new column: %v", err)
	}
	if got, err = s.GetTask("/p", "1"); err != nil || got.SubmittedBySession != "sess-new" {
		t.Fatalf("read back = %+v, %v", got, err)
	}
}

// §5.2: migration 6 adds notes.task_project beside task_id. A note promoted
// before it was promoted within its own project, so it must read back with an
// empty task_project — which is exactly what "the note's own board" means —
// rather than a guessed one.
func TestMigrationAddsTaskProjectAndKeepsOldNotesReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.db")
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for i := 0; i < 5; i++ { // migrations 1..5: schema version 5, before task_project
		if migrations[i].SQL == "" {
			continue
		}
		if _, err := db.Exec(migrations[i].SQL); err != nil {
			t.Fatalf("migration %d: %v", i+1, err)
		}
	}
	if _, err := db.Exec("INSERT INTO meta (schema_version, created_at) VALUES (5, 1)"); err != nil {
		t.Fatalf("stamp: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO notes
		(id, seq, project, body, status, author, task_id, created_at, updated_at)
		VALUES ('01ARZ3NDEKTSV4RRFFQ69G5FB1', 1, '/p', 'an old idea', 'task', 'human',
		        '01ARZ3NDEKTSV4RRFFQ69G5FAV', 1, 2)`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open (which migrates): %v", err)
	}
	defer s.Close()

	got, err := s.GetNote("/p", "1")
	if err != nil {
		t.Fatalf("GetNote: %v", err)
	}
	if got.Body != "an old idea" || got.TaskID != "01ARZ3NDEKTSV4RRFFQ69G5FAV" {
		t.Fatalf("the pre-migration row did not survive: %+v", got)
	}
	if got.TaskProject != "" {
		t.Fatalf("task_project = %q, want empty: the old row was promoted at home", got.TaskProject)
	}
}

// §4.4: a ULID is the cross-board address, so an --all-projects get finds a
// task filed anywhere; a number is only unique inside a project, so it is
// refused rather than resolved against a board the caller did not name.
func TestGetAnyProjectResolvesULIDAndRefusesNumber(t *testing.T) {
	s := open(t)
	create(t, s, "here")
	there, err := s.CreateTask(tasks.NewTaskInput{Project: "/tmp/project-b", Title: "there"}, operator, tick(t))
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := s.GetTask(proj, there.ID); codeOf(t, err) != codes.NotFound {
		t.Fatalf("scoped get of a task on another board = %v, want NOT_FOUND", err)
	}
	got, err := s.GetTaskAnyProject(there.ID)
	if err != nil {
		t.Fatalf("GetTaskAnyProject: %v", err)
	}
	if got.ID != there.ID || got.Project != "/tmp/project-b" {
		t.Fatalf("GetTaskAnyProject = %s in %s, want %s in /tmp/project-b", got.ID, got.Project, there.ID)
	}
	_, err = s.GetTaskAnyProject("1")
	if codeOf(t, err) != codes.Usage {
		t.Fatalf("GetTaskAnyProject by number = %v, want USAGE", err)
	}
	if !strings.Contains(err.Error(), "only unique inside a project") {
		t.Fatalf("refusal does not say why: %v", err)
	}
}

func note(t *testing.T, s *Store, body string) *tasks.Note {
	t.Helper()
	n, err := s.CreateNote(tasks.NewNoteInput{Project: proj, Body: body}, peer("wF:p1", "claude"), tick(t))
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	return n
}

// Several notes turn out to be one change: one is promoted and the rest are
// folded into the task it creates, in the same transaction. All of them end on
// that task, and none of them is left reading as undecided.
func TestSeveralNotesPromoteIntoOneTask(t *testing.T) {
	s := open(t)
	first, second, third := note(t, s, "the sweep is quiet"), note(t, s, "and it drops the lease"), note(t, s, "same sweep, no event")
	_, task, err := s.PromoteNote(proj, first.ID, 0, tasks.NewTaskInput{
		Project: proj, Title: "make the sweep say what it did", Description: first.Body,
	}, []string{second.ID, third.ID}, operator, tick(t))
	if err != nil {
		t.Fatalf("PromoteNote: %v", err)
	}
	for _, want := range []*tasks.Note{first, second, third} {
		got, err := s.GetNote(proj, want.ID)
		if err != nil {
			t.Fatalf("GetNote: %v", err)
		}
		if got.Status != tasks.NoteTask {
			t.Fatalf("note #%d is %q, want task", got.Seq, got.Status)
		}
		if got.TaskID != task.ID {
			t.Fatalf("note #%d points at %q, want %q", got.Seq, got.TaskID, task.ID)
		}
	}
	// Exactly one task, and it was made from the note that was promoted.
	list, err := s.ListTasks(TaskFilter{Project: proj})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("%d tasks, want 1", len(list))
	}
	if list[0].Description != first.Body {
		t.Fatalf("the task was made from %q, not the promoted note", list[0].Description)
	}
	// The origin does not unfold; the folded ones do.
	if got, _ := s.GetNote(proj, first.ID); got.Folded {
		t.Fatal("the promoted note is marked folded")
	}
	if got, _ := s.GetNote(proj, second.ID); !got.Folded {
		t.Fatal("a note folded into the task is not marked folded")
	}
}

// A promote whose --also names a note that cannot be folded writes nothing at
// all: no task, no half-folded set.
func TestAPromoteThatCannotFoldEveryNoteWritesNothing(t *testing.T) {
	s := open(t)
	first, taken := note(t, s, "the sweep is quiet"), note(t, s, "already spoken for")
	if _, _, err := s.PromoteNote(proj, taken.ID, 0, tasks.NewTaskInput{Project: proj, Title: "first"}, nil, operator, tick(t)); err != nil {
		t.Fatalf("first promote: %v", err)
	}
	_, _, err := s.PromoteNote(proj, first.ID, 0, tasks.NewTaskInput{Project: proj, Title: "second"}, []string{taken.ID}, operator, tick(t))
	if codeOf(t, err) != codes.Conflict {
		t.Fatalf("code = %q, want CONFLICT", codeOf(t, err))
	}
	list, _ := s.ListTasks(TaskFilter{Project: proj})
	if len(list) != 1 {
		t.Fatalf("%d tasks, want the 1 from the first promote", len(list))
	}
	if got, _ := s.GetNote(proj, first.ID); got.Status != tasks.NoteInbox {
		t.Fatalf("note #%d is %q; a refused promote leaves it where it was", got.Seq, got.Status)
	}
}

// The case that actually happened: the second note was filed after the task
// existed, so it is attached to it without creating another one.
func TestFoldAttachesANoteToATaskThatAlreadyExists(t *testing.T) {
	s := open(t)
	first, late := note(t, s, "the sweep is quiet"), note(t, s, "filed after the task")
	_, task, err := s.PromoteNote(proj, first.ID, 0, tasks.NewTaskInput{Project: proj, Title: "make the sweep talk"}, nil, operator, tick(t))
	if err != nil {
		t.Fatalf("PromoteNote: %v", err)
	}
	got, onto, err := s.FoldNote(proj, late.ID, 0, proj, task.ID, operator, tick(t))
	if err != nil {
		t.Fatalf("FoldNote: %v", err)
	}
	if onto.ID != task.ID {
		t.Fatalf("folded onto %q, want %q", onto.ID, task.ID)
	}
	if got.Status != tasks.NoteTask || got.TaskID != task.ID || !got.Folded {
		t.Fatalf("bad fold: %+v", got)
	}
	if list, _ := s.ListTasks(TaskFilter{Project: proj}); len(list) != 1 {
		t.Fatalf("%d tasks; a fold creates none", len(list))
	}
	// A note whose own task exists is refused, and the refusal names the task
	// holding it rather than repointing it.
	_, _, err = s.FoldNote(proj, first.ID, 0, proj, task.ID, operator, tick(t))
	if codeOf(t, err) != codes.Conflict {
		t.Fatalf("code = %q, want CONFLICT", codeOf(t, err))
	}
	if !strings.Contains(err.Error(), "#1") {
		t.Fatalf("refusal %q does not name the task holding the note", err)
	}
	// The task has to be there: folding into nothing is a typo, not a link.
	if _, _, err := s.FoldNote(proj, late.ID, 0, proj, "404", operator, tick(t)); codeOf(t, err) != codes.NotFound {
		t.Fatalf("code = %q, want NOT_FOUND", codeOf(t, err))
	}
}

// A task that is done or cancelled will not be worked, and a fold cannot be
// undone, so folding into one would lose the note for good. Refused.
func TestFoldRefusesATaskThatWillNotBeWorked(t *testing.T) {
	s := open(t)
	first, late := note(t, s, "the sweep is quiet"), note(t, s, "filed after the task")
	_, task, err := s.PromoteNote(proj, first.ID, 0, tasks.NewTaskInput{Project: proj, Title: "make the sweep talk"}, nil, operator, tick(t))
	if err != nil {
		t.Fatalf("PromoteNote: %v", err)
	}
	if _, err := s.TaskTransition(proj, task.ID, 0, func(x *tasks.Task) (tasks.Event, error) {
		return tasks.Cancel(x, operator, "not doing this", tick(t))
	}); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	_, _, err = s.FoldNote(proj, late.ID, 0, proj, task.ID, operator, tick(t))
	if codeOf(t, err) != codes.Conflict {
		t.Fatalf("code = %q, want CONFLICT: %v", codeOf(t, err), err)
	}
	got, err := s.GetNote(proj, late.ID)
	if err != nil {
		t.Fatalf("GetNote: %v", err)
	}
	if got.Status == tasks.NoteTask || got.TaskID != "" {
		t.Fatalf("the refused fold still moved the note: %+v", got)
	}
}

// The way back, at the store: unfolding returns the row to the inbox and the
// trail keeps both events.
func TestUnfoldReturnsAFoldedNoteToTheBoard(t *testing.T) {
	s := open(t)
	first, late := note(t, s, "the sweep is quiet"), note(t, s, "filed after the task")
	_, task, err := s.PromoteNote(proj, first.ID, 0, tasks.NewTaskInput{Project: proj, Title: "make the sweep talk"}, nil, operator, tick(t))
	if err != nil {
		t.Fatalf("PromoteNote: %v", err)
	}
	if _, _, err := s.FoldNote(proj, late.ID, 0, proj, task.ID, operator, tick(t)); err != nil {
		t.Fatalf("FoldNote: %v", err)
	}
	got, err := s.NoteTransition(proj, late.ID, 0, func(x *tasks.Note) (tasks.Event, error) {
		return tasks.NoteUnfold(x, operator, tick(t))
	})
	if err != nil {
		t.Fatalf("unfold: %v", err)
	}
	if got.Status != tasks.NoteInbox || got.TaskID != "" || got.Folded {
		t.Fatalf("bad unfold: %+v", got)
	}
	evs, _ := s.Events(EventFilter{Project: proj, EntityID: late.ID})
	kinds := []string{}
	for _, e := range evs {
		kinds = append(kinds, e.Kind)
	}
	if strings.Join(kinds, ",") != "added,folded,unfolded" {
		t.Fatalf("events = %v", kinds)
	}
}

// §5.2: a note written before the fold existed reached its task by promotion,
// and reads back that way. `folded` is a NEW column, so nothing about that row
// changes meaning — which is the half of the migration a fresh store cannot
// prove.
func TestMigrationAddsFoldedAndKeepsOldNotesReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.db")
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for i := 0; i < 6; i++ { // migrations 1..6: schema version 6, before folded
		if migrations[i].SQL == "" {
			continue
		}
		if _, err := db.Exec(migrations[i].SQL); err != nil {
			t.Fatalf("migration %d: %v", i+1, err)
		}
	}
	if _, err := db.Exec("INSERT INTO meta (schema_version, created_at) VALUES (6, 1)"); err != nil {
		t.Fatalf("stamp: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO notes
		(id, seq, project, body, status, author, task_id, created_at, updated_at)
		VALUES ('01ARZ3NDEKTSV4RRFFQ69G5FB2', 1, '/p', 'promoted before folding existed', 'task', 'human',
		        '01ARZ3NDEKTSV4RRFFQ69G5FAV', 1, 2)`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open (which migrates): %v", err)
	}
	defer s.Close()

	got, err := s.GetNote("/p", "1")
	if err != nil {
		t.Fatalf("GetNote: %v", err)
	}
	if got.Body != "promoted before folding existed" || got.TaskID != "01ARZ3NDEKTSV4RRFFQ69G5FAV" {
		t.Fatalf("the pre-migration row did not survive: %+v", got)
	}
	if got.Folded {
		t.Fatal("an old row reads as folded; a note that predates the column was promoted")
	}
	// And it is the origin of its task, so it does not unfold.
	_, err = s.NoteTransition("/p", "1", 0, func(x *tasks.Note) (tasks.Event, error) {
		return tasks.NoteUnfold(x, operator, tick(t))
	})
	if codeOf(t, err) != codes.Conflict {
		t.Fatalf("code = %q, want CONFLICT", codeOf(t, err))
	}
}

// Fail loud: a JSON column that does not read back as JSON is a corrupt row.
// Swallowing the decode handed the caller a task with no acceptance criteria
// and no evidence, which reads exactly like a task that never had any.
func TestScanTaskRefusesUnreadableJSONColumns(t *testing.T) {
	s := open(t)
	task := create(t, s, "has criteria")
	for _, col := range []string{"validation", "evidence", "evidence_for"} {
		if _, err := s.db.Exec("UPDATE tasks SET "+col+" = ? WHERE id = ?", "{not json", task.ID); err != nil {
			t.Fatalf("corrupt %s: %v", col, err)
		}
		_, err := s.GetTask(proj, task.ID)
		if err == nil {
			t.Fatalf("%s: a corrupt column read back as a task", col)
		}
		if !strings.Contains(err.Error(), col) {
			t.Fatalf("%s: the failure does not name the column: %v", col, err)
		}
		if _, err := s.db.Exec("UPDATE tasks SET "+col+" = NULL WHERE id = ?", task.ID); err != nil {
			t.Fatalf("restore %s: %v", col, err)
		}
	}
}

// A scan loop that never asks rows.Err() reports "this pane held nothing"
// when the truth is "the read broke half way", which is the silent-success
// failure the sweep exists to avoid. SweepLeases checks; so must this.
func TestReleaseByPaneChecksTheRowsError(t *testing.T) {
	s := open(t)
	raw, err := os.ReadFile("tasks.go")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	start := strings.Index(string(raw), "func (s *Store) ReleaseByPane")
	if start < 0 {
		t.Fatal("ReleaseByPane is not in tasks.go any more")
	}
	end := strings.Index(string(raw)[start:], "\n}\n")
	if !strings.Contains(string(raw)[start:start+end], "rows.Err()") {
		t.Fatal("ReleaseByPane scans its rows without ever checking rows.Err()")
	}
	// And it still does what it did: a pane's held lease comes back.
	task := create(t, s, "held")
	if _, err := s.TaskTransition(proj, task.ID, 0, func(tk *tasks.Task) (tasks.Event, error) {
		return tasks.Claim(tk, tasks.Actor{Principal: "agent:wF:p9"}, tick(t), 900_000)
	}); err != nil {
		t.Fatalf("claim: %v", err)
	}
	released, err := s.ReleaseByPane("wF:p9", "pane exited", tick(t))
	if err != nil || len(released) != 1 {
		t.Fatalf("ReleaseByPane = %v, %v; want the one lease", released, err)
	}
}
