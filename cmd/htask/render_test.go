package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/husniadil/herdr-tasks/internal/codes"
	"github.com/husniadil/herdr-tasks/internal/mcpdoor"
	"github.com/husniadil/herdr-tasks/internal/tasks"
	"github.com/husniadil/herdr-tasks/internal/verbs"
)

// §9.3 / §6.1: both audiences see that a resolved action's verb failed. The
// --json half is pinned in the daemon's own tests; this is the prose half,
// which is the surface an operator reads when they wonder why the approve they
// resolved did not happen.
func TestParkedListSaysWhenAVerbFailed(t *testing.T) {
	raw := json.RawMessage(`{"parked":[
		{"id":"P1","project":"/repo","subject":"agent:wF:p1","verb":"tasks.approve","target":"#3","state":"parked"},
		{"id":"P2","project":"/repo","subject":"agent:wF:p1","verb":"tasks.approve","target":"#4","state":"failed",
		 "error":"CONFLICT: task is todo, not review"}],"count":2}`)
	v, ok := verbs.ByName("parked.list")
	if !ok {
		t.Fatal("no parked.list verb")
	}
	out := captureStdout(t, func() {
		if err := renderHuman(v, raw, 0); err != nil {
			t.Fatalf("renderHuman: %v", err)
		}
	})
	if !strings.Contains(out, "P1") || !strings.Contains(out, "P2") {
		t.Fatalf("both rows must be listed:\n%s", out)
	}
	if !strings.Contains(out, "failed") || !strings.Contains(out, "task is todo, not review") {
		t.Fatalf("the failed row does not say what happened:\n%s", out)
	}
	// The waiting row is not decorated with a state it does not have.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "P1") && strings.Contains(line, "failed") {
			t.Fatalf("the waiting row was reported as failed: %q", line)
		}
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	saved := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var buf strings.Builder
		io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	os.Stdout = saved
	w.Close()
	out := <-done
	r.Close()
	return out
}

// §6.2 for the project-resolution failure at root.go:171. That path is not
// reachable from the command line — I could not construct an input that makes
// project.Resolve fail: filepath.Abs only fails for a relative path when the
// working directory has gone, and a process cannot be started in one that
// has. So the door's answer for it is pinned where it is built instead.
func TestAResolutionFailureIsOneUsageEnvelope(t *testing.T) {
	err := codes.Errorf(codes.Usage, "cannot resolve the project: %v", errors.New("getwd: no such file or directory"))
	out := captureStdout(t, func() { printErrorEnvelope(err, codes.Usage) })

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 {
		t.Fatalf("stdout carries %d documents, want one:\n%s", len(lines), out)
	}
	var doc struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if uerr := json.Unmarshal([]byte(lines[0]), &doc); uerr != nil {
		t.Fatalf("not one JSON document: %q (%v)", lines[0], uerr)
	}
	if doc.Error.Code != codes.Usage {
		t.Fatalf("code = %q, want USAGE", doc.Error.Code)
	}
	if !strings.Contains(doc.Error.Message, "cannot resolve the project") {
		t.Fatalf("the envelope does not say what failed: %q", doc.Error.Message)
	}
}

// §6.2: the door reads --json out of argv before cobra parses, because
// cobra's own parse failures have to answer with a document too.
func TestWantsJSONReadsTheRawArgv(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		want bool
	}{
		{[]string{"task", "list"}, false},
		{[]string{"task", "list", "--json"}, true},
		{[]string{"--json", "task", "list"}, true},
		{[]string{"task", "list", "--json=true"}, true},
		{[]string{"task", "list", "--json=false"}, false},
		{[]string{"task", "list", "--json=0"}, false},
		// A value cobra will refuse still counts as asking for a document:
		// the refusal is the answer, and it should arrive in the shape the
		// caller asked for.
		{[]string{"task", "list", "--json=maybe"}, true},
		{[]string{"task", "list", "--nonesuch", "--json"}, true},
		// After the terminator it is an argument, not this flag.
		{[]string{"note", "add", "--", "--json"}, false},
		{[]string{"note", "add", "--json", "--", "--json=false"}, true},
	} {
		if got := wantsJSON(tc.argv); got != tc.want {
			t.Errorf("wantsJSON(%v) = %v, want %v", tc.argv, got, tc.want)
		}
	}
}

// §6.1: every flag the CLI adds beyond a verb's own arguments is accounted for
// on the MCP door — mapped to a property, or excluded with a reason. The
// flags live here, in the CLI, so this is the half of the drift check that
// only this package can make: internal/mcpdoor cannot enumerate cobra's flags.
//
// base_updated_at was unreachable through MCP because nothing compared the two
// surfaces at this level. An absence is not a decision until it is written
// down.
func TestEveryCLIGlobalIsAccountedForOnTheMCPDoor(t *testing.T) {
	root := newRootCmd()
	seen := map[string]bool{}
	note := func(f *pflag.Flag) {
		if f.Name == "help" {
			return
		}
		seen[f.Name] = true
	}
	root.PersistentFlags().VisitAll(note)

	byName := map[string]verbs.Verb{}
	for _, v := range verbs.All {
		byName[v.Name] = v
	}
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			walk(sub)
		}
		v, ok := byName[verbNameOf(c)]
		if !ok {
			return
		}
		c.Flags().VisitAll(func(f *pflag.Flag) {
			if f.Name == "help" {
				return
			}
			for _, a := range v.Args {
				if a.Name == f.Name {
					return
				}
			}
			seen[f.Name] = true
		})
	}
	walk(root)

	if len(seen) < 4 {
		t.Fatalf("only %d global flags found; the walk is not reaching the command tree", len(seen))
	}
	for name := range seen {
		g, ok := mcpdoor.Globals[name]
		if !ok {
			t.Errorf("--%s is a CLI global the MCP door says nothing about; map it or record why not", name)
			continue
		}
		if g.Property == "" && g.Excluded == "" {
			t.Errorf("--%s is excluded from the MCP door with no reason recorded", name)
		}
		if g.Property != "" && g.Excluded != "" {
			t.Errorf("--%s is both mapped to %q and excluded", name, g.Property)
		}
	}
	// And the table does not name flags that do not exist.
	for name := range mcpdoor.Globals {
		if !seen[name] {
			t.Errorf("the MCP door's globals table names --%s, which the CLI does not offer", name)
		}
	}
}

// verbNameOf rebuilds a registry verb name from a cobra command's path.
func verbNameOf(c *cobra.Command) string {
	parts := []string{}
	for cur := c; cur != nil && cur.Name() != "htask"; cur = cur.Parent() {
		parts = append([]string{cur.Name()}, parts...)
	}
	return strings.Join(parts, ".")
}

// §6.1: the prose half. `htask task get` on a task blocked by something that was
// cancelled says which one — the operator reading prose needs the same fact
// the JSON carries.
func TestProseNamesACancelledBlocker(t *testing.T) {
	abandoned := &tasks.Task{Seq: 7, Title: "waits on dropped work",
		Status: tasks.StatusTodo, Blocked: true, Abandoned: []int64{3, 4}}
	out := captureStdout(t, func() { printTask(abandoned, 0) })
	for _, want := range []string{"blocked", "cancelled", "#3", "#4"} {
		if !strings.Contains(out, want) {
			t.Errorf("the prose does not say %q:\n%s", want, out)
		}
	}

	plain := &tasks.Task{Seq: 8, Title: "waits on work still to come",
		Status: tasks.StatusTodo, Blocked: true}
	out = captureStdout(t, func() { printTask(plain, 0) })
	if !strings.Contains(out, "blocked") {
		t.Errorf("an ordinary blocked task does not say so:\n%s", out)
	}
	if strings.Contains(out, "cancelled") {
		t.Errorf("an ordinary blocked task was reported as abandoned:\n%s", out)
	}
}

// §5.9 with §16.2: truncation cuts CHARACTERS, not bytes. Both firstLine
// helpers sliced a byte offset, so a cut landing inside a multi-byte character
// produced invalid UTF-8 — and the daemon's copy becomes a promoted note's
// task TITLE, so the broken bytes are written to the database and read back
// forever.
func TestFirstLineCutsOnCharacterBoundaries(t *testing.T) {
	// The byte cut at 80 lands inside a three-byte character; the rune cut at
	// 80 has to happen too, so the string is longer than 80 characters.
	body := strings.Repeat("状", 120)
	got := firstLine(body)
	if !utf8.ValidString(got) {
		t.Fatalf("the CLI's firstLine cut a character in half: %q", got)
	}
	if got == body {
		t.Fatalf("nothing was truncated, so the test proves nothing: %d runes", len([]rune(got)))
	}
}

// The review row is the only claimed row that printed no time: a doing row
// carries its lease, and submitted_at sat in the payload with no reader. Both
// human surfaces answer "for how long" from submitted_at, at display time.
func TestTheReviewLineSaysHowLongItHasWaited(t *testing.T) {
	const now = int64(1_700_000_000_000)
	const hour = int64(3_600_000)
	waiting := &tasks.Task{
		ID: "T1", Seq: 3, Status: tasks.StatusReview, Title: "wire the door",
		ClaimedBy: "agent:wF:p1", ClaimedByHarness: "claude",
		// UpdatedAt moved after the submission — an edit while it waited.
		// The age is the wait, so it is the submission it counts from.
		SubmittedAt: now - 2*hour, UpdatedAt: now - 5*60_000,
	}
	working := &tasks.Task{
		ID: "T2", Seq: 4, Status: tasks.StatusDoing, Title: "hold the lease",
		ClaimedBy: "agent:wF:p2", ClaimedByHarness: "claude",
		LeaseUntil: now + 10*60_000, UpdatedAt: now,
	}

	get, ok := verbs.ByName("task.get")
	if !ok {
		t.Fatal("no task.get verb")
	}
	raw, err := json.Marshal(map[string]any{"task": waiting})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := captureStdout(t, func() {
		if err := renderHuman(get, raw, now); err != nil {
			t.Fatalf("renderHuman: %v", err)
		}
	})
	if strings.Contains(out, "5m ago") {
		t.Fatalf("the age came from UpdatedAt, not SubmittedAt — UpdatedAt moves on "+
			"reject and resubmit, restarting the clock on the task waiting longest:\n%s", out)
	}
	if !strings.Contains(out, "submitted 2h ago") {
		t.Fatalf("task get does not say how long it has waited:\n%s", out)
	}

	// The doing row is untouched: it already prints a time, and it is a
	// different one.
	raw, err = json.Marshal(map[string]any{"task": working})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out = captureStdout(t, func() {
		if err := renderHuman(get, raw, now); err != nil {
			t.Fatalf("renderHuman: %v", err)
		}
	})
	if !strings.Contains(out, "lease until") {
		t.Fatalf("the doing line lost its lease:\n%s", out)
	}
	if strings.Contains(out, "ago") {
		t.Fatalf("a doing task is not waiting for review:\n%s", out)
	}

	list, ok := verbs.ByName("task.list")
	if !ok {
		t.Fatal("no task.list verb")
	}
	raw, err = json.Marshal(map[string]any{
		"tasks": []*tasks.Task{waiting, working}, "count": 2, "project": "/repo"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out = captureStdout(t, func() {
		if err := renderHuman(list, raw, now); err != nil {
			t.Fatalf("renderHuman: %v", err)
		}
	})
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.Contains(line, "wire the door"):
			if strings.Contains(line, "5m ago") {
				t.Fatalf("the row's age came from UpdatedAt, not SubmittedAt: %q", line)
			}
			if !strings.Contains(line, "submitted 2h ago") {
				t.Fatalf("the review row does not say how long it has waited: %q", line)
			}
		case strings.Contains(line, "hold the lease"):
			if strings.Contains(line, "ago") {
				t.Fatalf("a doing row is not waiting for review: %q", line)
			}
		}
	}
}

// The units a wait is read in: seconds while it is fresh, days once nobody
// has looked. A wait is never negative, and a clock that says it is says the
// smallest true thing instead.
func TestAWaitIsReadInTheUnitThatFitsIt(t *testing.T) {
	const now = int64(1_700_000_000_000)
	for _, c := range []struct {
		submitted int64
		want      string
	}{
		{now, "0s ago"},
		{now - 45_000, "45s ago"},
		{now - 90_000, "1m ago"},
		{now - 2*3_600_000, "2h ago"},
		{now - 50*3_600_000, "2d ago"},
		{now + 60_000, "0s ago"},
	} {
		if got := waited(c.submitted, now); got != c.want {
			t.Fatalf("waited(%d) = %q, want %q", c.submitted-now, got, c.want)
		}
	}
}

// §6.1, the prose half of criterion 2: a reviewer reading `htask task get` is
// told the report above is not the one that was submitted. The --json caller
// has amend_count; the human reading prose has this line, and without it the
// two surfaces would answer the same question differently.
func TestProseSaysAReportWasAmended(t *testing.T) {
	amended := &tasks.Task{Seq: 9, Title: "corrected after submitting",
		Status: tasks.StatusReview, Report: "done at 9f738bf", AmendCount: 1, AmendedAt: 1}
	out := captureStdout(t, func() { printTask(amended, 0) })
	for _, want := range []string{"Amended", "1 amendment", "submitted"} {
		if !strings.Contains(out, want) {
			t.Errorf("the prose does not say %q:\n%s", want, out)
		}
	}

	twice := &tasks.Task{Seq: 10, Title: "corrected twice",
		Status: tasks.StatusReview, Report: "done", AmendCount: 2, AmendedAt: 1}
	if out := captureStdout(t, func() { printTask(twice, 0) }); !strings.Contains(out, "2 amendments") {
		t.Errorf("the prose does not count two amendments:\n%s", out)
	}

	plain := &tasks.Task{Seq: 11, Title: "submitted once",
		Status: tasks.StatusReview, Report: "done"}
	if out := captureStdout(t, func() { printTask(plain, 0) }); strings.Contains(out, "Amended") {
		t.Errorf("a report nobody amended was reported as amended:\n%s", out)
	}
}
