package store

import (
	"testing"

	"github.com/husniadil/herdr-tasks/internal/tasks"
)

// §5.5: an events table is APPEND-ONLY. Hard delete removed the entity's
// events with the entity, so the one operation the trail most needed to
// survive was the one that erased it: a row that was created and then removed
// left no record that either had happened. §5.7 lets the ROW go; nothing lets
// the trail go with it, and the events carry their own entity_id rather than a
// foreign key, so nothing joins them to a live row.
func TestDeletingATaskLeavesItsTrailStanding(t *testing.T) {
	s := open(t)
	task := create(t, s, "typo")
	before, err := s.Events(EventFilter{Project: proj, Entity: "task", EntityID: task.ID})
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(before) == 0 {
		t.Fatal("the create wrote no event, so this proves nothing")
	}
	if err := s.DeleteTask(proj, task.ID); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}
	after, err := s.Events(EventFilter{Project: proj, Entity: "task", EntityID: task.ID})
	if err != nil {
		t.Fatalf("Events after the delete: %v", err)
	}
	if len(after) != len(before) {
		t.Errorf("%d events for a deleted task, want the %d it had", len(after), len(before))
	}
	// And the whole-project listing still reads, so a gap in what an entity
	// points at does not break the stream every consumer resumes from (§8.2).
	if _, err := s.Events(EventFilter{Project: proj}); err != nil {
		t.Fatalf("the project trail no longer reads after a delete: %v", err)
	}
}

// The same for a note (§5.5, §5.7).
func TestDeletingANoteLeavesItsTrailStanding(t *testing.T) {
	s := open(t)
	n, err := s.CreateNote(tasks.NewNoteInput{Project: proj, Body: "an idea"}, operator, tick(t))
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	before, err := s.Events(EventFilter{Project: proj, Entity: "note", EntityID: n.ID})
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(before) == 0 {
		t.Fatal("the create wrote no event, so this proves nothing")
	}
	if err := s.DeleteNote(proj, n.ID, operator); err != nil {
		t.Fatalf("DeleteNote: %v", err)
	}
	after, err := s.Events(EventFilter{Project: proj, Entity: "note", EntityID: n.ID})
	if err != nil {
		t.Fatalf("Events after the delete: %v", err)
	}
	if len(after) != len(before) {
		t.Errorf("%d events for a deleted note, want the %d it had", len(after), len(before))
	}
}
