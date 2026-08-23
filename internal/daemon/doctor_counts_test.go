package daemon

import (
	"strings"
	"testing"

	"github.com/husniadil/herdr-tasks/internal/protocol"
	"github.com/husniadil/herdr-tasks/internal/tasks"
)

// §10.3 with §6.5: `leases_outstanding` counts LEASES, and submit ends the
// lease while keeping `claimed_by` — the board's answer to who submitted this,
// which §6.6 still needs. Counting claims instead reported a lease on every
// row waiting for a reviewer, which is exactly the set of rows the sweep can
// never touch.
func TestLeasesOutstandingCountsLeasesAndNotClaims(t *testing.T) {
	d := newDaemon(t, nil)
	one := createTask(t, d, "in review")
	mustCall(t, d, protocol.Request{Verb: "task.claim", Args: map[string]any{"id": one.Task.ID}})
	mustCall(t, d, protocol.Request{Verb: "task.submit",
		Args: map[string]any{"id": one.Task.ID, "report": "done"}})

	two := createTask(t, d, "in hand")
	mustCall(t, d, protocol.Request{Verb: "task.claim", Args: map[string]any{"id": two.Task.ID}})

	report := d.Doctor(protocol.Request{Project: proj}, tasks.Actor{Principal: tasks.PrincipalHuman})
	if report.TasksInProject != 2 {
		t.Fatalf("tasks_in_project = %d, want 2", report.TasksInProject)
	}
	if report.LeasesOutstands != 1 {
		t.Errorf("leases_outstanding = %d, want 1: the submitted row holds a claim and no lease",
			report.LeasesOutstands)
	}
}

// §10.3: doctor never fails, and a read it could not make is a line in
// Degraded rather than a zero that reads as an empty board. A count of 0 for
// want of an answer and a count of 0 because there is nothing are the same
// number, and only one of them is a fact about the board.
func TestDoctorSaysWhenItCouldNotReadWhatItCounts(t *testing.T) {
	d := newDaemon(t, nil)
	createTask(t, d, "a task nobody will be able to count")
	for _, table := range []string{"tasks", "parked"} {
		if _, err := d.Store.DB().Exec("DROP TABLE " + table); err != nil {
			t.Fatalf("drop %s: %v", table, err)
		}
	}
	report := d.Doctor(protocol.Request{Project: proj}, tasks.Actor{Principal: tasks.PrincipalHuman})
	if report.TasksInProject != 0 || report.LeasesOutstands != 0 || report.ParkedWaiting != 0 {
		t.Fatalf("counts came back from a store that cannot answer: %+v", report)
	}
	degraded := strings.Join(report.Degraded, "\n")
	if !strings.Contains(degraded, "tasks_in_project") {
		t.Errorf("nothing in Degraded names the task read that failed:\n%s", degraded)
	}
	if !strings.Contains(degraded, "parked_waiting") {
		t.Errorf("nothing in Degraded names the parked read that failed:\n%s", degraded)
	}
}
