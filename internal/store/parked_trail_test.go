package store

import (
	"testing"

	"github.com/husniadil/herdr-tasks/internal/tasks"
)

// §5.5 with §8.1: the parked queue is an entity this store mutates, so it has
// an append-only sibling table like the other two. Before this it had none, and
// the gate's own decisions — what it deferred, and what the operator then did
// about it — were the only writes in the plugin that left no trail at all: the
// `parked` row was overwritten in place, so "who deferred this and when" was
// gone the moment it was resolved, and no `events --follow` consumer ever
// learned a gate decision had happened.
func TestParkingAndResolvingLeaveATrail(t *testing.T) {
	s := open(t)
	id, err := s.Park(Parked{Project: proj, Subject: "agent:wF:p1", Verb: "tasks.claim",
		Target: "1", Payload: "{}"}, tick(t))
	if err != nil {
		t.Fatalf("Park: %v", err)
	}
	if err := s.ResolveParked(proj, id, "resolved", tasks.Principal("human"), tick(t)); err != nil {
		t.Fatalf("ResolveParked: %v", err)
	}
	evs, err := s.Events(EventFilter{Project: proj, Entity: "parked", EntityID: id})
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(evs) != 2 {
		t.Fatalf("%d events for a parked action that was parked and resolved, want 2", len(evs))
	}
	if evs[0].Kind != "parked" || evs[1].Kind != "resolved" {
		t.Fatalf("kinds %q then %q, want parked then resolved", evs[0].Kind, evs[1].Kind)
	}
	// §8.1 names an event `tasks.<entity>.<kind>`, and a consumer filters on
	// the name rather than on the pair.
	if evs[0].Name != "tasks.parked.parked" || evs[1].Name != "tasks.parked.resolved" {
		t.Errorf("names %q then %q, want tasks.parked.parked then tasks.parked.resolved", evs[0].Name, evs[1].Name)
	}
	// The actor is the one the decision belongs to: the subject the gate
	// stopped when it was parked, the principal that decided it afterwards.
	if evs[0].Actor != tasks.Principal("agent:wF:p1") {
		t.Errorf("the park was recorded as %q, want the subject the gate stopped", evs[0].Actor)
	}
	if evs[1].Actor != tasks.Principal("human") {
		t.Errorf("the resolve was recorded as %q, want the principal that decided it", evs[1].Actor)
	}
}

// A rejection and a failed re-run are decisions too, and each is the only
// record that the deferred verb did NOT happen.
func TestRejectingAndFailingLeaveATrail(t *testing.T) {
	s := open(t)
	rejected, err := s.Park(Parked{Project: proj, Subject: "agent:wF:p1", Verb: "tasks.approve", Target: "1"}, tick(t))
	if err != nil {
		t.Fatalf("Park: %v", err)
	}
	if err := s.ResolveParked(proj, rejected, "rejected", tasks.Principal("human"), tick(t)); err != nil {
		t.Fatalf("ResolveParked: %v", err)
	}
	failed, err := s.Park(Parked{Project: proj, Subject: "agent:wF:p2", Verb: "tasks.approve", Target: "2"}, tick(t))
	if err != nil {
		t.Fatalf("Park: %v", err)
	}
	if err := s.ResolveParked(proj, failed, "resolved", tasks.Principal("human"), tick(t)); err != nil {
		t.Fatalf("ResolveParked: %v", err)
	}
	if err := s.FailParked(proj, failed, "the task is not in review", tick(t)); err != nil {
		t.Fatalf("FailParked: %v", err)
	}
	for _, c := range []struct {
		id    string
		kinds []string
	}{
		{rejected, []string{"parked", "rejected"}},
		{failed, []string{"parked", "resolved", "failed"}},
	} {
		evs, err := s.Events(EventFilter{Project: proj, Entity: "parked", EntityID: c.id})
		if err != nil {
			t.Fatalf("Events: %v", err)
		}
		if len(evs) != len(c.kinds) {
			t.Fatalf("%d events for %s, want %d", len(evs), c.id, len(c.kinds))
		}
		for i, want := range c.kinds {
			if evs[i].Kind != want {
				t.Errorf("%s event %d is %q, want %q", c.id, i, evs[i].Kind, want)
			}
		}
	}
}

// §8.2: `events` reads the merged trail, so a gate decision reaches the same
// stream every consumer already resumes from rather than a table only a
// bespoke reader knows about.
func TestTheMergedTrailSurfacesGateDecisions(t *testing.T) {
	s := open(t)
	task := create(t, s, "something to defer")
	id, err := s.Park(Parked{Project: proj, Subject: "agent:wF:p1", Verb: "tasks.claim", Target: task.ID}, tick(t))
	if err != nil {
		t.Fatalf("Park: %v", err)
	}
	all, err := s.Events(EventFilter{Project: proj})
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	var sawTask, sawParked bool
	for _, e := range all {
		switch e.Entity {
		case "task":
			sawTask = true
		case "parked":
			sawParked = e.EntityID == id
		}
	}
	if !sawTask {
		t.Error("the merged trail dropped the task events")
	}
	if !sawParked {
		t.Error("the merged trail does not carry the parked action, so no `events` consumer sees a gate decision")
	}
}
