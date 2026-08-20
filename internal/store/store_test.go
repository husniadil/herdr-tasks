package store

import (
	"encoding/json"
	"path/filepath"
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
