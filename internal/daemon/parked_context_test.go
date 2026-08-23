package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/husniadil/herdr-tasks/internal/codes"
	"github.com/husniadil/herdr-tasks/internal/config"
	"github.com/husniadil/herdr-tasks/internal/protocol"
	"github.com/husniadil/herdr-tasks/internal/store"
	"github.com/husniadil/herdr-tasks/internal/tasks"
)

// deferringDaemon is a daemon whose policy gate defers everything (§9.2).
func deferringDaemon(t *testing.T) *Daemon {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gate")
	if err := os.WriteFile(path,
		[]byte("#!/bin/sh\necho '{\"decision\":\"defer\",\"reason\":\"ask first\"}'\n"), 0o755); err != nil {
		t.Fatalf("write gate: %v", err)
	}
	return newDaemon(t, &config.Config{LeaseSeconds: 900, SweepSeconds: 60, GateCommand: []string{path}})
}

// §9.3: resolving re-runs the verb as the ORIGINAL call, not as a call
// reassembled from the parts that happened to be kept. The tab and the
// workspace the pane sat in were dropped, so a task created through the gate
// came back with a pane of origin and no tab or workspace beside it — the
// same row, filed differently, depending on whether a policy was configured.
func TestAParkedCallKeepsTheTabAndWorkspaceItWasMadeFrom(t *testing.T) {
	d := deferringDaemon(t)
	body := mustFail(t, d, protocol.Request{Verb: "task.create",
		PaneID: "wF:p1", TabID: "wF:t3", WorkspaceID: "wF",
		Args: map[string]any{"title": "parked work"}}, codes.Denied)

	mustCall(t, d, protocol.Request{Verb: "parked.resolve", PaneID: "wF:p1",
		Args: map[string]any{"id": body.ParkedID}})
	list, err := d.Store.ListTasks(store.TaskFilter{Project: proj})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("%d tasks, want the one the resolve ran", len(list))
	}
	got := list[0]
	if got.PaneID != "wF:p1" || got.TabID != "wF:t3" || got.WorkspaceID != "wF" {
		t.Errorf("the re-run lost where the call came from: pane %q tab %q workspace %q",
			got.PaneID, got.TabID, got.WorkspaceID)
	}
}

// §4.4 with §9.3: a call made with --all-projects is re-run with it. Without
// the scope the re-run looks on the resolver's board for a task that is on
// another one, and answers NOT_FOUND for a verb the gate only deferred.
func TestAParkedCallKeepsTheScopeItWasMadeWith(t *testing.T) {
	d := deferringDaemon(t)
	other := "/tmp/project-b"
	// Created outside the gate's reach: task.create is gated, so it is made
	// through the store the way the fixture does elsewhere.
	created, err := d.Store.CreateTask(tasks.NewTaskInput{Project: other, Title: "elsewhere"},
		tasks.Actor{Principal: tasks.PrincipalHuman}, d.Now())
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	body := mustFail(t, d, protocol.Request{Verb: "task.claim", Project: proj, AllProjects: true,
		PaneID: "wF:p1", Args: map[string]any{"id": created.ID}}, codes.Denied)

	mustCall(t, d, protocol.Request{Verb: "parked.resolve", PaneID: "wF:p1",
		Args: map[string]any{"id": body.ParkedID}})

	after, err := d.Store.GetTask(other, created.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if after.Status != tasks.StatusDoing {
		t.Fatalf("the re-run left the task %s; the scope it was made with was lost", after.Status)
	}
}
