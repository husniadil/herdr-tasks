package daemon

import (
	"encoding/json"
	"fmt"
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

// §5.6 with §9.3: a call made with --base-updated-at is re-run with it. A
// task that moved while the call sat parked is a conflict, not a silent
// overwrite of what moved it.
func TestAParkedCallKeepsTheGuardItWasMadeWith(t *testing.T) {
	// Only p1's update is deferred: the create and p2's rival update land.
	path := filepath.Join(t.TempDir(), "gate")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+
		"if grep -q '\"subject\":\"agent:wF:p1\",\"verb\":\"tasks.update\"'; then echo '{\"decision\":\"defer\",\"reason\":\"ask first\"}'; "+
		"else echo '{\"decision\":\"allow\"}'; fi\n"), 0o755); err != nil {
		t.Fatalf("write gate: %v", err)
	}
	d := newDaemon(t, &config.Config{LeaseSeconds: 900, SweepSeconds: 60, GateCommand: []string{path}})
	created := mustCall(t, d, protocol.Request{Verb: "task.create", PaneID: "wF:p1", Operator: true,
		Args: map[string]any{"title": "guarded"}})
	var cr TaskResult
	if err := json.Unmarshal(created, &cr); err != nil {
		t.Fatal(err)
	}
	t0 := cr.Task
	body := mustFail(t, d, protocol.Request{Verb: "task.update", PaneID: "wF:p1", BaseUpdatedAt: t0.UpdatedAt,
		Args: map[string]any{"id": fmt.Sprint(t0.Seq), "title": "from the parked call"}}, codes.Denied)

	// Someone else moves the task while the call is parked.
	mustCall(t, d, protocol.Request{Verb: "task.update", PaneID: "wF:p2", Operator: true,
		Args: map[string]any{"id": fmt.Sprint(t0.Seq), "title": "moved meanwhile"}})

	mustFail(t, d, protocol.Request{Verb: "parked.resolve", PaneID: "wF:p1",
		Args: map[string]any{"id": body.ParkedID}}, codes.Conflict)
	got, err := d.Store.GetTask(proj, fmt.Sprint(t0.Seq))
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Title != "moved meanwhile" {
		t.Errorf("the resolve overwrote the change made while it was parked: title %q", got.Title)
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
