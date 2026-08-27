package store

import (
	"encoding/json"
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
	if err := s.ResolveParked(proj, id, "resolved", tasks.Actor{Principal: tasks.PrincipalHuman}, tick(t)); err != nil {
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
	if err := s.ResolveParked(proj, rejected, "rejected", tasks.Actor{Principal: tasks.PrincipalHuman}, tick(t)); err != nil {
		t.Fatalf("ResolveParked: %v", err)
	}
	failed, err := s.Park(Parked{Project: proj, Subject: "agent:wF:p2", Verb: "tasks.approve", Target: "2"}, tick(t))
	if err != nil {
		t.Fatalf("Park: %v", err)
	}
	if err := s.ResolveParked(proj, failed, "resolved", tasks.Actor{Principal: tasks.PrincipalHuman}, tick(t)); err != nil {
		t.Fatalf("ResolveParked: %v", err)
	}
	if err := s.FailParked(proj, failed, "the task is not in review", tasks.Actor{Principal: tasks.PrincipalHuman}, tick(t)); err != nil {
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

// §3.7: resolving a deferral is the operator's authority, and since 0.10.0 an
// agent reaches it after confirming with the user rather than being refused.
// Nothing checks that the confirmation happened, so the trail is the whole
// accountability and it is only honest if it says that the authority exercised
// was somebody else's: the resolution event carries the same
// `on_behalf_of_operator` mark the five note verbs write, and carries nothing
// extra when the operator resolved it themselves. `resolved_by` answers who
// resolved it, which is a different question from on whose authority.
func TestResolvingMarksAnOperatorVerbAnAgentPerformed(t *testing.T) {
	for _, c := range []struct {
		name   string
		by     tasks.Actor
		marked bool
	}{
		{"an agent", tasks.Actor{Principal: "agent:wF:p1"}, true},
		{"the operator", tasks.Actor{Principal: tasks.PrincipalHuman}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := open(t)
			id, err := s.Park(Parked{Project: proj, Subject: "agent:wF:p2", Verb: "tasks.approve",
				Target: "1", Payload: "{}"}, tick(t))
			if err != nil {
				t.Fatalf("Park: %v", err)
			}
			if err := s.ResolveParked(proj, id, "resolved", c.by, tick(t)); err != nil {
				t.Fatalf("ResolveParked: %v", err)
			}
			evs, err := s.Events(EventFilter{Project: proj, Entity: "parked", EntityID: id})
			if err != nil {
				t.Fatalf("Events: %v", err)
			}
			if len(evs) != 2 || evs[1].Kind != "resolved" {
				t.Fatalf("%d events, last %q; want a parked then a resolved", len(evs), evs[len(evs)-1].Kind)
			}
			if evs[1].Actor != c.by.Principal {
				t.Fatalf("the resolve was recorded as %q, want %q: the trail names who acted",
					evs[1].Actor, c.by.Principal)
			}
			detail := map[string]any{}
			if len(evs[1].Detail) > 0 {
				if err := json.Unmarshal(evs[1].Detail, &detail); err != nil {
					t.Fatalf("detail %s: %v", evs[1].Detail, err)
				}
			}
			if got := detail[tasks.OnBehalfOfOperator] == true; got != c.marked {
				t.Errorf("detail = %v, %s = %v, want %v: %s resolved it",
					detail, tasks.OnBehalfOfOperator, got, c.marked, c.name)
			}
		})
	}
}

// §3.7 again, on the other end of a resolve: when the re-run errors, the
// `failed` event is the last thing the trail says about that deferral, and it
// is written on the same authority as the `resolved` event before it. So it
// carries the same mark beside the error it already reported, and a failure
// after the operator's own resolve carries nothing extra.
func TestFailingMarksAnOperatorVerbAnAgentPerformed(t *testing.T) {
	for _, c := range []struct {
		name   string
		by     tasks.Actor
		marked bool
	}{
		{"an agent", tasks.Actor{Principal: "agent:wF:p1"}, true},
		{"the operator", tasks.Actor{Principal: tasks.PrincipalHuman}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := open(t)
			id, err := s.Park(Parked{Project: proj, Subject: "agent:wF:p2", Verb: "tasks.approve",
				Target: "1", Payload: "{}"}, tick(t))
			if err != nil {
				t.Fatalf("Park: %v", err)
			}
			if err := s.ResolveParked(proj, id, "resolved", c.by, tick(t)); err != nil {
				t.Fatalf("ResolveParked: %v", err)
			}
			if err := s.FailParked(proj, id, "the task is not in review", c.by, tick(t)); err != nil {
				t.Fatalf("FailParked: %v", err)
			}
			evs, err := s.Events(EventFilter{Project: proj, Entity: "parked", EntityID: id})
			if err != nil {
				t.Fatalf("Events: %v", err)
			}
			if len(evs) != 3 || evs[2].Kind != KindFailed {
				t.Fatalf("%d events, last %q; want a parked, a resolved then a failed",
					len(evs), evs[len(evs)-1].Kind)
			}
			if evs[2].Actor != c.by.Principal {
				t.Fatalf("the failure was recorded as %q, want %q: the trail names who acted",
					evs[2].Actor, c.by.Principal)
			}
			detail := map[string]any{}
			if len(evs[2].Detail) > 0 {
				if err := json.Unmarshal(evs[2].Detail, &detail); err != nil {
					t.Fatalf("detail %s: %v", evs[2].Detail, err)
				}
			}
			// The mark is added to the error detail, never in place of it: the
			// operator resolving again needs to read why the verb did not run.
			if detail["error"] != "the task is not in review" {
				t.Errorf("detail = %v, want the error the re-run reported", detail)
			}
			if got := detail[tasks.OnBehalfOfOperator] == true; got != c.marked {
				t.Errorf("detail = %v, %s = %v, want %v: %s resolved it",
					detail, tasks.OnBehalfOfOperator, got, c.marked, c.name)
			}
		})
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
