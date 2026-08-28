package daemon

import (
	"encoding/json"
	"testing"

	"github.com/husniadil/herdr-tasks/internal/protocol"
)

// bodyKeys are the free-text bodies a row carries: the description a task was
// written with, the report and evidence a submission put on it, the feedback a
// rejection wrote, and the note a release left. Together they are almost all
// of a finished board's bytes.
var bodyKeys = []string{"description", "report", "evidence", "evidence_for", "feedback", "release_note"}

// worked files one task and walks it through everything that puts a body on
// the row, so a list of it has the most to leave out.
func worked(t *testing.T, d *Daemon) {
	t.Helper()
	mustCall(t, d, protocol.Request{Verb: "task.create", Args: map[string]any{
		"title": "wire the door", "description": "the long one", "validation": []any{"the gate is green"}}})
	mustCall(t, d, protocol.Request{Verb: "task.claim", PaneID: "wF:p1", Args: map[string]any{"id": "1"}})
	mustCall(t, d, protocol.Request{Verb: "task.submit", PaneID: "wF:p1", Args: map[string]any{
		"id": "1", "report": "wired it", "evidence": []any{"make test: ok"},
		"evidence-for": []any{"1: make test: ok"}}})
	mustCall(t, d, protocol.Request{Verb: "task.reject", Args: map[string]any{"id": "1", "feedback": "not yet"}})
	mustCall(t, d, protocol.Request{Verb: "task.release", Args: map[string]any{"id": "1", "note": "half done"}})
}

// §13.3: `list` is a listing, not a read of every task in full. A board of 92
// finished tasks answered 1.6 MB because every row carried its description,
// its report and all of its evidence, and a hub reading `list` over several
// boxes and projects paid that for each. The bodies live on `get`, which is
// one call for the one task a caller actually opens.
func TestListRowsCarryNoBodies(t *testing.T) {
	d := newDaemon(t, nil)
	worked(t, d)

	var res struct {
		Tasks []map[string]json.RawMessage `json:"tasks"`
		Count int                          `json:"count"`
	}
	raw := mustCall(t, d, protocol.Request{Verb: "task.list"})
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if res.Count != 1 || len(res.Tasks) != 1 {
		t.Fatalf("list answered %d rows, want the one that was filed: %s", len(res.Tasks), raw)
	}
	for _, key := range bodyKeys {
		if _, ok := res.Tasks[0][key]; ok {
			t.Errorf("the list row still carries %q, which is what makes a listing cost a read: %s", key, raw)
		}
	}
}

// The other half of the same rule: what `list` drops, `get` still answers in
// full. A caller that reads a body reads it there, so dropping it from the
// listing costs one call rather than the fact itself.
func TestGetStillCarriesEveryBody(t *testing.T) {
	d := newDaemon(t, nil)
	worked(t, d)

	var res struct {
		Task map[string]json.RawMessage `json:"task"`
	}
	raw := mustCall(t, d, protocol.Request{Verb: "task.get", Args: map[string]any{"id": "1"}})
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("unmarshal get: %v", err)
	}
	for _, key := range bodyKeys {
		if _, ok := res.Task[key]; !ok {
			t.Errorf("`get` no longer answers %q, and nothing else does: %s", key, raw)
		}
	}
}

// The listing keeps everything a caller selects, sorts, routes or renders on:
// the ids, the scope, the status and priority, the claim, the submission and
// review stamps, the Herdr context a sibling plugin routes a report to, and
// the dependency facts `--ready` and the board's blocked marker are computed
// from. Dropping one of these would make `list` answer a question it is the
// only verb asked.
func TestListRowsKeepEverySummaryFact(t *testing.T) {
	d := newDaemon(t, nil)
	// #1 is held, so the claim snapshot is on it. #2 depends on it, so the
	// dependency facts are on that one. #3 goes all the way to done, so the
	// submission and review stamps are on the third.
	mustCall(t, d, protocol.Request{Verb: "task.create", PaneID: "wF:p1", TabID: "wF:t1", WorkspaceID: "wF",
		Args: map[string]any{"title": "the held one", "priority": int64(3),
			"validation": []any{"the gate is green"}}})
	mustCall(t, d, protocol.Request{Verb: "task.create", Args: map[string]any{
		"title": "the blocked one", "depends-on": []any{"1"}, "discovered-from": "1"}})
	mustCall(t, d, protocol.Request{Verb: "task.create", Args: map[string]any{"title": "the done one"}})
	mustCall(t, d, protocol.Request{Verb: "task.claim", PaneID: "wF:p1", Args: map[string]any{"id": "1"}})
	mustCall(t, d, protocol.Request{Verb: "task.claim", PaneID: "wF:p2", Args: map[string]any{"id": "3"}})
	mustCall(t, d, protocol.Request{Verb: "task.submit", PaneID: "wF:p2", Args: map[string]any{
		"id": "3", "report": "wired it"}})
	mustCall(t, d, protocol.Request{Verb: "task.approve", Args: map[string]any{"id": "3"}})

	var res struct {
		Tasks []map[string]json.RawMessage `json:"tasks"`
	}
	raw := mustCall(t, d, protocol.Request{Verb: "task.list"})
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	rows := map[string]map[string]json.RawMessage{}
	for _, r := range res.Tasks {
		rows[string(r["seq"])] = r
	}
	if len(rows) != 3 {
		t.Fatalf("the listing has %d rows, want 3: %s", len(rows), raw)
	}
	for seq, keys := range map[string][]string{
		"1": {"id", "seq", "project", "title", "status", "priority", "blocked",
			"created_by", "created_at", "updated_at", "claimed_by", "claimed_by_name",
			"claimed_by_harness", "claimed_by_session", "claimed_at", "lease_until",
			"ever_claimed", "pane_id", "tab_id", "workspace_id"},
		"2": {"deps", "discovered_from", "blocked"},
		"3": {"submitted_by", "submitted_by_harness", "submitted_by_session", "submitted_at",
			"reviewed_by", "completed_at"},
	} {
		for _, key := range keys {
			if _, ok := rows[seq][key]; !ok {
				t.Errorf("the list row #%s lost %q: %s", seq, key, raw)
			}
		}
	}
}
