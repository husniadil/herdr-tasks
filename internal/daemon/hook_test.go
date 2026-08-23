package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/husniadil/herdr-tasks/internal/config"
	"github.com/husniadil/herdr-tasks/internal/protocol"
	"github.com/husniadil/herdr-tasks/internal/store"
)

// §8.3: the hook fires for the event the write WROTE. It used to be found by
// re-reading the entity's whole trail and taking the end of it, which is the
// same event only while nothing else is writing — two callers mutating one
// task both saw the later event, so it was announced twice and the earlier one
// never was. A consumer that acts on the trail then acted twice on one change
// and missed another.
func TestEveryEventIsAnnouncedExactlyOnce(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	hook := filepath.Join(dir, "hook")
	// One line per firing, appended: the shell's >> on a short write is what
	// makes concurrent appends land whole.
	if err := os.WriteFile(hook,
		[]byte("#!/bin/sh\nprintf '%s %s\\n' \"$TASKS_EVENT\" \"$TASKS_AT\" >> "+log+"\n"), 0o755); err != nil {
		t.Fatalf("write hook: %v", err)
	}
	d := newDaemon(t, &config.Config{LeaseSeconds: 900, SweepSeconds: 60, OnEvent: []string{hook}})
	task := createTask(t, d, "raced")
	mustCall(t, d, protocol.Request{Verb: "task.claim",
		PaneID: "wF:p1", Args: map[string]any{"id": task.Task.ID}})

	const writers = 12
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.Answer(protocol.Request{Verb: "task.touch", Project: proj,
				PaneID: "wF:p1", Args: map[string]any{"id": task.Task.ID}})
		}()
	}
	wg.Wait()

	// What the store says happened, as the identity the hook carries: the
	// event name and the millisecond it was written at.
	evs, err := d.Store.Events(store.EventFilter{Project: proj, Entity: "task", EntityID: task.Task.ID})
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	want := []string{}
	for _, e := range evs {
		want = append(want, fmt.Sprintf("%s %d", e.Name, e.At))
	}
	sort.Strings(want)

	got := []string{}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		body, err := os.ReadFile(log)
		if err == nil {
			got = got[:0]
			for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
				if line != "" {
					got = append(got, line)
				}
			}
			if len(got) >= len(want) {
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	sort.Strings(got)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("the hook announced\n  %v\nfor a trail of\n  %v", got, want)
	}
}
