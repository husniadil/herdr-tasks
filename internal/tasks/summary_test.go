package tasks

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// bodies are the free-text fields a listing leaves out. Everything else on a
// Task is a summary fact and belongs on Summary under the same JSON name.
var bodies = map[string]string{
	"description":  "the brief the task was written with",
	"report":       "what the submission said",
	"evidence":     "what the submission ran",
	"evidence_for": "which criterion each of those proves",
	"feedback":     "what a rejection asked for",
	"release_note": "what a release left for whoever claims it next",
}

func jsonNames(t *testing.T, v any) map[string]string {
	t.Helper()
	out := map[string]string{}
	rt := reflect.TypeOf(v)
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			t.Fatalf("%s.%s carries no json name", rt.Name(), f.Name)
		}
		out[name] = f.Type.String()
	}
	return out
}

// A field added to Task lands on `get` and, unless it is a body, on `list` as
// well — a summary fact that only one of the two verbs answers is a fact a
// caller has to guess where to read. So the two shapes are compared here
// rather than being kept in step by whoever remembers.
func TestSummaryCarriesEveryTaskFactThatIsNotABody(t *testing.T) {
	task, summary := jsonNames(t, Task{}), jsonNames(t, Summary{})
	for name, typ := range task {
		if _, isBody := bodies[name]; isBody {
			if _, ok := summary[name]; ok {
				t.Errorf("Summary carries %q, which is a body: %s", name, bodies[name])
			}
			continue
		}
		got, ok := summary[name]
		if !ok {
			t.Errorf("Task has %q and Summary does not; a listing answers every fact that "+
				"is not a body, or a caller has to guess which verb carries it", name)
			continue
		}
		if got != typ {
			t.Errorf("%q is %s on Task and %s on Summary; the same key must mean the same "+
				"thing on both verbs", name, typ, got)
		}
	}
	for name := range summary {
		if _, ok := task[name]; !ok {
			t.Errorf("Summary has %q and Task does not, so a listing answers a fact `get` "+
				"cannot", name)
		}
	}
	for name := range bodies {
		if _, ok := task[name]; !ok {
			t.Errorf("this test still calls %q a body and Task no longer has it", name)
		}
	}
}

// Summarize copies, and a copy is only worth what it carries: a field left out
// of it reads as an empty one on every row of every listing.
func TestSummarizeCopiesEveryField(t *testing.T) {
	full := Task{
		ID: "T1", Seq: 3, Project: "/repo", Title: "the door", Status: StatusReview,
		Priority: 5, Validation: []Criterion{{Text: "the gate is green", Required: true}},
		DiscoveredFrom: "T0", Deps: []string{"T0"}, Blocked: true, Abandoned: []int64{2},
		CreatedBy: "human", CreatedAt: 1, UpdatedAt: 2,
		ClaimedBy: "agent:wF:p1", ClaimedByName: "builder", ClaimedByHarness: "claude",
		ClaimedBySession: "s1", ClaimedAt: 3, LeaseUntil: 4, EverClaimed: true,
		ReleasedAt: 5, SubmittedBy: "agent:wF:p1", SubmittedByHarness: "claude",
		SubmittedBySession: "s1", SubmittedAt: 6, AmendedAt: 7, AmendCount: 1,
		ReviewedBy: "human", CompletedAt: 8, CancelledAt: 9, ArchivedAt: 10,
		PaneID: "wF:p1", TabID: "wF:t1", WorkspaceID: "wF",
		Description: "the long one", Report: "wired it", Evidence: []string{"make test: ok"},
		EvidenceFor: []Citation{{Criterion: 1, Text: "make test: ok"}},
		Feedback:    "not yet", ReleaseNote: "half done",
	}
	var row, whole map[string]any
	raw, err := json.Marshal(Summarize(&full))
	if err != nil {
		t.Fatalf("marshal summary: %v", err)
	}
	if err := json.Unmarshal(raw, &row); err != nil {
		t.Fatalf("unmarshal summary: %v", err)
	}
	whole = map[string]any{}
	rawFull, err := json.Marshal(full)
	if err != nil {
		t.Fatalf("marshal task: %v", err)
	}
	if err := json.Unmarshal(rawFull, &whole); err != nil {
		t.Fatalf("unmarshal task: %v", err)
	}
	for name := range jsonNames(t, Summary{}) {
		got, ok := row[name]
		if !ok {
			t.Errorf("Summarize left %q off a row that has every field set", name)
			continue
		}
		if want := whole[name]; !reflect.DeepEqual(got, want) {
			t.Errorf("%q is %v on the row and %v on the task", name, got, want)
		}
	}
	for name := range bodies {
		if _, ok := row[name]; ok {
			t.Errorf("the row carries %q", name)
		}
	}
}
