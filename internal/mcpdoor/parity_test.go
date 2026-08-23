package mcpdoor

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/husniadil/herdr-tasks/internal/codes"
	"github.com/husniadil/herdr-tasks/internal/config"
	"github.com/husniadil/herdr-tasks/internal/daemon"
	"github.com/husniadil/herdr-tasks/internal/herdrclient"
	"github.com/husniadil/herdr-tasks/internal/project"
	"github.com/husniadil/herdr-tasks/internal/protocol"
	"github.com/husniadil/herdr-tasks/internal/store"
	"github.com/husniadil/herdr-tasks/internal/testenv"
	"github.com/husniadil/herdr-tasks/internal/verbs"
)

// pinnedTools is the tool list §7.1 pins. Adding a tool is a deliberate change
// to a semver-bound surface: it changes this list in the same commit, and
// removing or renaming one is a breaking change.
var pinnedTools = []string{
	"create",
	"list",
	"get",
	"claim",
	"touch",
	"release",
	"submit",
	"amend",
	"approve",
	"reject",
	"goal",
	"cancel",
	"update",
	"archive",
	"delete",
	"note_add",
	"note_list",
	"note_get",
	"note_update",
	"note_discuss",
	"note_verdict",
	"note_promote",
	"note_fold",
	"note_unfold",
	"note_keep",
	"note_drop",
	"note_delete",
	"parked_list",
	"parked_resolve",
	"events",
	"doctor",
	"sweep",
	"dump",
}

// bareName is the §7.1 name for a registry verb: the verb alone, with dots as
// underscores. `task` is the board's default entity and drops out, so
// `task.claim` is `claim` and `note.add` stays `note_add` — the qualifier is
// there when it separates two verbs and absent when it repeats the subject.
func bareName(verb string) string {
	return strings.ReplaceAll(strings.TrimPrefix(verb, "task."), ".", "_")
}

// §7.1: the tool list is pinned by a test and is semver-bound once released.
func TestMCPToolListIsPinned(t *testing.T) {
	got := []string{}
	for _, v := range verbs.MCPTools() {
		got = append(got, v.MCP)
	}
	if strings.Join(got, ",") != strings.Join(pinnedTools, ",") {
		t.Fatalf("the MCP tool list moved.\n got: %v\nwant: %v\nIf this change is intended, update pinnedTools in the same commit (§7.1).", got, pinnedTools)
	}
}

// §7.3 (0.10.0): every verb the CLI serves is on the door, read from the
// door's own side. The rule this replaces asked for 8–16 tools, which is how
// `note_promote` came to be a verb a harness with no shell could not reach —
// an accident of a count rather than a decision about authority. Its
// successor let a verb stay off the door if it wrote down why, and every one
// of the eight that did said a form of "this authority is the operator's".
// §3.7 made that advice an agent confirms, so no reason was left standing and
// the field recording them is gone.
func TestEveryCLIVerbReachesTheMCPDoor(t *testing.T) {
	published := map[string]bool{}
	for _, v := range verbs.MCPTools() {
		published[v.Name] = true
	}
	for _, v := range verbs.All {
		if !published[v.Name] {
			t.Errorf("%s is on the CLI and not on the MCP door; §7.3 admits no CLI-only verb", v.Name)
		}
	}
	if len(pinnedTools) != len(verbs.All) {
		t.Errorf("%d pinned tools for %d verbs; the door is not total", len(pinnedTools), len(verbs.All))
	}
}

// §6.1: a parity test MUST enumerate both surfaces and fail when they drift.
// Every MCP tool names a CLI subcommand, resolves to the same daemon verb, and
// declares the same arguments.
func TestCLIAndMCPSurfacesDoNotDrift(t *testing.T) {
	cliByVerb := map[string]verbs.Verb{}
	for _, v := range verbs.All {
		if len(v.CLI) == 0 {
			t.Errorf("verb %q has no CLI subcommand; §6.1 says every verb is one", v.Name)
			continue
		}
		cliByVerb[v.Name] = v
	}
	seen := map[string]bool{}
	for _, tl := range verbs.MCPTools() {
		cli, ok := cliByVerb[tl.Name]
		if !ok {
			t.Errorf("MCP tool %q has no CLI subcommand", tl.MCP)
			continue
		}
		if seen[tl.MCP] {
			t.Errorf("MCP tool %q is declared twice", tl.MCP)
		}
		seen[tl.MCP] = true
		// §7.1: the tool name is the verb alone, dots turned into
		// underscores. The client's server label carries the plugin's
		// identity, so a prefix here would say it twice.
		if want := bareName(tl.Name); tl.MCP != want {
			t.Errorf("tool %q is not the bare verb %q (§7.1)", tl.MCP, want)
		}
		// Same arguments: the tool schema is built from the same Args the CLI
		// builds its flags from, so this compares the rendered surfaces.
		schema, _ := json.Marshal(tool(tl).InputSchema)
		var got struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		}
		json.Unmarshal(schema, &got)
		for _, a := range cli.Args {
			if _, ok := got.Properties[a.Name]; !ok {
				t.Errorf("tool %q is missing the %q argument the CLI takes", tl.MCP, a.Name)
			}
			if a.Required && !contains(got.Required, a.Name) {
				t.Errorf("tool %q does not require %q, but the CLI does", tl.MCP, a.Name)
			}
		}
		// Nothing extra beyond the CLI's args and the globals this verb's
		// tool is supposed to carry — and nothing MISSING from those either,
		// which is the half that could not see base_updated_at.
		globals := map[string]bool{}
		for _, name := range GlobalsFor(cli) {
			globals[name] = true
			if _, ok := got.Properties[name]; !ok {
				t.Errorf("tool %q does not take %q, which the CLI offers this verb", tl.MCP, name)
			}
		}
		for name := range got.Properties {
			if globals[name] {
				continue
			}
			if !hasArg(cli.Args, name) {
				t.Errorf("tool %q takes %q, which the CLI does not", tl.MCP, name)
			}
		}
	}
}

// §6.1 / §7.4: the two doors return the same JSON for the same call. This runs
// both surfaces against one daemon and compares the documents.
func TestCLIAndMCPReturnTheSameDocument(t *testing.T) {
	testenv.SkipUnlessFull(t)
	d, call := inProcessDaemon(t)
	_ = d

	// The CLI path: the daemon's own answer, which is what --json prints.
	cliRaw, err := call(protocol.Request{Verb: "task.create", Project: canonProject(t, "/tmp/p"),
		Args: map[string]any{"title": "same both ways"}})
	if err != nil {
		t.Fatalf("cli call: %v", err)
	}

	// The MCP path: the tool handler over the same caller.
	srv := New(call, Options{})
	clientSide := mcp.NewClient(&mcp.Implementation{Name: "parity-test", Version: "0"}, nil)
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	sess, err := clientSide.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer sess.Close()

	tools, err := sess.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	names := []string{}
	for _, tl := range tools.Tools {
		names = append(names, tl.Name)
	}
	sort.Strings(names)
	want := append([]string(nil), pinnedTools...)
	sort.Strings(want)
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("the served tool list is not the pinned list:\n got %v\nwant %v", names, want)
	}

	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      "get",
		Arguments: map[string]any{"id": "1", "project": canonProject(t, "/tmp/p")},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool error: %s", text(res))
	}
	cliGet, err := call(protocol.Request{Verb: "task.get", Project: canonProject(t, "/tmp/p"), Args: map[string]any{"id": "1"}})
	if err != nil {
		t.Fatalf("cli get: %v", err)
	}
	if !sameJSON(t, []byte(text(res)), cliGet) {
		t.Fatalf("the doors disagree (§6.1):\nmcp: %s\ncli: %s", text(res), cliGet)
	}
	if !strings.Contains(string(cliRaw), "same both ways") {
		t.Fatalf("create did not take: %s", cliRaw)
	}
}

// §7.4: an error is a tool error carrying the §6.3 code, not a protocol error.
func TestMCPErrorsCarryTheContractCode(t *testing.T) {
	testenv.SkipUnlessFull(t)
	_, call := inProcessDaemon(t)
	srv := New(call, Options{})
	clientSide := mcp.NewClient(&mcp.Implementation{Name: "parity-test", Version: "0"}, nil)
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	sess, err := clientSide.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer sess.Close()

	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      "get",
		Arguments: map[string]any{"id": "404", "project": canonProject(t, "/tmp/p")},
	})
	if err != nil {
		t.Fatalf("a missing task must be a tool error, not a protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatal("want a tool error")
	}
	if !strings.Contains(text(res), `"NOT_FOUND"`) {
		t.Fatalf("tool error does not carry the §6.3 code: %s", text(res))
	}
}

// §7.2: the instructions say what the plugin is, that pane/agent/workspace are
// Herdr's, and which verbs are the usual entry points.
func TestInstructionsCoverTheRequiredGround(t *testing.T) {
	for _, want := range []string{"Herdr", "pane", "agent", "workspace", "claim", "submit", "project"} {
		if !strings.Contains(Instructions, want) {
			t.Errorf("instructions do not mention %q (§7.2)", want)
		}
	}
}

// §12.3 with §3.2: the MCP door derives its principal from HERDR_PANE_ID in
// its own process environment (mcpdoor.go:162-164) and these tests run
// in-process, so without a seam the suite's principal is the pane of whoever
// ran it — the operator's live Herdr identity reaching a test. The harness
// clears the trio; this holds that it does, for the principal and for the
// Herdr context stored alongside it.
//
// The hostile identity is planted here rather than inherited, so this test
// says the same thing inside a Herdr pane and outside one.
func TestTheMCPDoorTakesNoIdentityFromTheProcessThatRanTheTests(t *testing.T) {
	testenv.SkipUnlessFull(t)
	t.Setenv("HERDR_PANE_ID", "wOPERATOR:p9")
	t.Setenv("HERDR_TAB_ID", "wOPERATOR:t9")
	t.Setenv("HERDR_WORKSPACE_ID", "wOPERATOR")
	_, call := inProcessDaemon(t)
	project := canonProject(t, "/tmp/p")

	// `none`, not `human`: the harness cleared the ambient pane, and a door in
	// no pane that nobody declared has no principal (§3.7). Before that rule
	// this read `human`, which is the same evidence — nothing — read as the
	// highest authority in the system.
	anonymous := createThroughMCP(t, call, project, "created by nobody in particular")
	if got := anonymous["created_by"]; got != "none" {
		t.Fatalf("created_by = %v, want none: HERDR_PANE_ID reached the door from the process environment", got)
	}
	for field, envVar := range map[string]string{
		"pane_id": "HERDR_PANE_ID", "tab_id": "HERDR_TAB_ID", "workspace_id": "HERDR_WORKSPACE_ID",
	} {
		if got, ok := anonymous[field]; ok && got != "" {
			t.Fatalf("%s = %v: %s reached the door from the process environment", field, got, envVar)
		}
	}

	// And a test that WANTS a pane still gets exactly the one it names, so
	// long as it names it after the harness has cleared the ambient one.
	t.Setenv("HERDR_PANE_ID", "wF:p7")
	pinned := createThroughMCP(t, call, project, "created by a named pane")
	if got := pinned["created_by"]; got != "agent:wF:p7" {
		t.Fatalf("created_by = %v, want agent:wF:p7", got)
	}
}

// createThroughMCP creates a task through the tool handler rather than through
// the caller directly, because reading the environment is what the handler
// does and what is under test here.
func createThroughMCP(t *testing.T, call Caller, project, title string) map[string]any {
	t.Helper()
	srv := New(call, Options{})
	clientSide := mcp.NewClient(&mcp.Implementation{Name: "identity-test", Version: "0"}, nil)
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	sess, err := clientSide.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer sess.Close()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name: "create", Arguments: map[string]any{"title": title, "project": project},
	})
	if err != nil || res.IsError {
		t.Fatalf("create through the MCP door: %v %s", err, text(res))
	}
	var doc struct {
		Task map[string]any `json:"task"`
	}
	if err := json.Unmarshal([]byte(text(res)), &doc); err != nil {
		t.Fatalf("the create answer is not one JSON document: %q", text(res))
	}
	return doc.Task
}

// inProcessDaemon gives the MCP door a real daemon without a socket, so the
// parity test compares the doors and not the transport.
func inProcessDaemon(t *testing.T) (*daemon.Daemon, Caller) {
	t.Helper()
	dir := testenv.ShortDir(t)
	t.Setenv("TASKS_STATE_DIR", dir)
	t.Setenv("TASKS_CONFIG_DIR", t.TempDir())
	// §12.3: the MCP door reads the Herdr trio from its own environment
	// (mcpdoor.go:162-164) and these tests run in-process, so the suite would
	// otherwise derive its principal — and the pane, tab and workspace it
	// stores on a task — from whoever ran it. Cleared here, at the one seam
	// every test goes through, because a rule enforced per call site is a rule
	// that regresses. A test that wants a specific pane names it AFTER this
	// returns; see TestBothDoorsRefuseWithTheSameWords.
	t.Setenv("HERDR_PANE_ID", "")
	t.Setenv("HERDR_TAB_ID", "")
	t.Setenv("HERDR_WORKSPACE_ID", "")
	s, err := store.Open(filepath.Join(dir, "tasks.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	cfg := &config.Config{LeaseSeconds: 900, SweepSeconds: 60}
	d := daemon.New(s, cfg, herdrclient.New(testenv.FakeHerdr(t)))
	return d, func(req protocol.Request) (json.RawMessage, error) {
		resp := d.Answer(req)
		if resp.Error != nil {
			return nil, &callFailure{resp.Error}
		}
		return resp.Result, nil
	}
}

type callFailure struct{ body *protocol.ErrorBody }

func (c *callFailure) Error() string   { return c.body.Code + ": " + c.body.Message }
func (c *callFailure) Code() string    { return c.body.Code }
func (c *callFailure) Message() string { return c.body.Message }

func text(res *mcp.CallToolResult) string {
	if len(res.Content) == 0 {
		return ""
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		return ""
	}
	return tc.Text
}

func sameJSON(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &y); err != nil {
		return false
	}
	ax, _ := json.Marshal(x)
	by, _ := json.Marshal(y)
	return string(ax) == string(by)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func hasArg(args []verbs.Arg, name string) bool {
	for _, a := range args {
		if a.Name == name {
			return true
		}
	}
	return false
}

// §6.1: the two doors take the same arguments, and for a search that matters
// more than usual — an agent's documented duplicate check runs over MCP, so a
// query the CLI has and the tool does not is the asymmetry this asserts is
// gone. The parity test above proves the two surfaces MATCH; this one proves
// what they match ON, which parity alone cannot say.
func TestNoteQueryReachesBothDoors(t *testing.T) {
	v, ok := verbs.ByName("note.list")
	if !ok {
		t.Fatal("no note.list verb")
	}
	if !v.Accepts("query") {
		t.Fatal("note.list takes no query argument, so `htask note list --query` does not exist")
	}
	schema, err := json.Marshal(tool(v).InputSchema)
	if err != nil {
		t.Fatalf("marshal the tool schema: %v", err)
	}
	var got struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(schema, &got); err != nil {
		t.Fatalf("read the tool schema: %v", err)
	}
	if _, ok := got.Properties["query"]; !ok {
		t.Fatalf("note_list's input schema has no query property: %s", schema)
	}
}

// §6.1 with §3.1: a refusal is part of the answer, so the two doors have to
// refuse identically — same code, same words. The three verbs whose principal
// rule this change tightened (note.delete, task.cancel, sweep --pane) are
// CLI-only, so the property is pinned here on a verb that IS on both doors:
// §6.6 recusal, where a principal may not review its own submission.
func TestBothDoorsRefuseWithTheSameWords(t *testing.T) {
	testenv.SkipUnlessFull(t)
	d, call := inProcessDaemon(t)
	// Both doors derive their principal from HERDR_PANE_ID; this test needs a
	// specific one rather than the cleared default, so it names it after the
	// harness has cleared what the process brought in.
	t.Setenv("HERDR_PANE_ID", "wF:p1")
	_ = d
	project := canonProject(t, "/tmp/p")
	if _, err := call(protocol.Request{Verb: "task.create", Project: project,
		Args: map[string]any{"title": "reviewed by its own submitter"}}); err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, req := range []protocol.Request{
		{Verb: "task.claim", Project: project, PaneID: "wF:p1", Args: map[string]any{"id": "1"}},
		{Verb: "task.submit", Project: project, PaneID: "wF:p1",
			Args: map[string]any{"id": "1", "report": "done"}},
	} {
		if _, err := call(req); err != nil {
			t.Fatalf("%s: %v", req.Verb, err)
		}
	}

	cliErr := d.Answer(protocol.Request{Verb: "task.approve", Project: project, PaneID: "wF:p1",
		Args: map[string]any{"id": "1"}}).Error
	if cliErr == nil || cliErr.Code != codes.Forbidden {
		t.Fatalf("the CLI door did not refuse: %+v", cliErr)
	}

	srv := New(call, Options{})
	clientSide := mcp.NewClient(&mcp.Implementation{Name: "parity-test", Version: "0"}, nil)
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	sess, err := clientSide.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer sess.Close()

	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      "approve",
		Arguments: map[string]any{"id": "1", "project": project},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatalf("the MCP door did not refuse: %s", text(res))
	}
	got := text(res)
	if !strings.Contains(got, codes.Forbidden) {
		t.Fatalf("the MCP refusal does not carry the §6.3 code: %s", got)
	}
	if !strings.Contains(got, cliErr.Message) {
		t.Fatalf("the doors refuse in different words (§6.1):\nmcp: %s\ncli: %s", got, cliErr.Message)
	}
}

// mcpSession opens a real in-memory MCP client against an undeclared door,
// which is what every test but the §7.5 ones wants.
func mcpSession(t *testing.T, call Caller) *mcp.ClientSession {
	return mcpSessionWith(t, call, Options{})
}

// mcpSessionWith is the same against a door started with the given options.
func mcpSessionWith(t *testing.T, call Caller, opt Options) *mcp.ClientSession {
	t.Helper()
	srv := New(call, opt)
	clientSide := mcp.NewClient(&mcp.Implementation{Name: "parity-test", Version: "0"}, nil)
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	sess, err := clientSide.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { sess.Close() })
	return sess
}

func callTool(t *testing.T, sess *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	return res
}

// §7.1 with §6.1: the schema the door publishes is a promise, and a door that
// coerces instead of refusing breaks it silently. limit "nope" became 0 —
// which the filters read as unlimited — and priority 2.9 became 2. The CLI
// refuses both at parse with exit 2, so the two doors disagreed about what a
// declared integer means.
func TestTheMCPDoorRefusesArgumentsItsSchemaForbids(t *testing.T) {
	testenv.SkipUnlessFull(t)
	_, call := inProcessDaemon(t)
	sess := mcpSession(t, call)

	for name, tc := range map[string]struct {
		tool string
		args map[string]any
		arg  string
	}{
		"a word where an integer is declared":     {"list", map[string]any{"limit": "nope", "project": canonProject(t, "/tmp/p")}, "limit"},
		"a fraction where an integer is declared": {"create", map[string]any{"title": "t", "priority": 2.9, "project": canonProject(t, "/tmp/p")}, "priority"},
		"a number where a string is declared":     {"get", map[string]any{"id": 12, "project": canonProject(t, "/tmp/p")}, "id"},
		"a string where a list is declared":       {"submit", map[string]any{"id": "1", "report": "r", "evidence": "one", "project": canonProject(t, "/tmp/p")}, "evidence"},
	} {
		res := callTool(t, sess, tc.tool, tc.args)
		if !res.IsError {
			t.Errorf("%s: the door accepted it: %s", name, text(res))
			continue
		}
		got := text(res)
		if !strings.Contains(got, codes.Usage) {
			t.Errorf("%s: refused with %s, want USAGE", name, got)
		}
		if !strings.Contains(got, tc.arg) {
			t.Errorf("%s: the refusal does not name the argument: %s", name, got)
		}
	}
}

// §5.6 through the MCP door. Without base_updated_at an MCP agent's writes had
// no lost-update protection at all, on every mutating verb — the guard existed
// and was unreachable.
func TestBaseUpdatedAtIsReachableThroughMCP(t *testing.T) {
	testenv.SkipUnlessFull(t)
	d, call := inProcessDaemon(t)
	// A counted clock, not the wall clock: create and update landing in the
	// same millisecond leaves updated_at where it was, and then the value the
	// caller read is not stale after all — a test that passes or fails on how
	// fast the machine is proves nothing either way.
	tick := new(atomic.Int64)
	tick.Store(1_700_000_000_000)
	d.Now = func() int64 { return tick.Add(1) }
	sess := mcpSession(t, call)
	project := canonProject(t, "/tmp/p")

	raw, err := call(protocol.Request{Verb: "task.create", Project: project,
		Args: map[string]any{"title": "raced"}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var made struct {
		Task struct {
			ID        string `json:"id"`
			UpdatedAt int64  `json:"updated_at"`
		} `json:"task"`
	}
	if err := json.Unmarshal(raw, &made); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	stale := made.Task.UpdatedAt

	// Move the row, so the value the caller read is no longer current.
	if _, err := call(protocol.Request{Verb: "task.update", Project: project,
		Args: map[string]any{"id": made.Task.ID, "title": "renamed"}}); err != nil {
		t.Fatalf("update: %v", err)
	}

	res := callTool(t, sess, "claim", map[string]any{
		"id": made.Task.ID, "project": project, "base_updated_at": stale})
	if !res.IsError {
		t.Fatalf("the §5.6 guard did not fire through MCP: %s", text(res))
	}
	if !strings.Contains(text(res), codes.Conflict) {
		t.Fatalf("refused with %s, want CONFLICT", text(res))
	}
	// The same call through the daemon says the same thing.
	cli := d.Answer(protocol.Request{Verb: "task.claim", Project: project, BaseUpdatedAt: stale,
		Args: map[string]any{"id": made.Task.ID}})
	if cli.Error == nil || !strings.Contains(text(res), cli.Error.Message) {
		t.Fatalf("the doors disagree:\nmcp: %s\ncli: %+v", text(res), cli.Error)
	}
	// And a CURRENT value still works.
	fresh := d.Answer(protocol.Request{Verb: "task.get", Project: project,
		Args: map[string]any{"id": made.Task.ID}})
	var got struct {
		Task struct {
			UpdatedAt int64 `json:"updated_at"`
		} `json:"task"`
	}
	if err := json.Unmarshal(fresh.Result, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	res = callTool(t, sess, "claim", map[string]any{
		"id": made.Task.ID, "project": project, "base_updated_at": got.Task.UpdatedAt})
	if res.IsError {
		t.Fatalf("a current base_updated_at was refused: %s", text(res))
	}
}

// §4.4 through the MCP door: the list tools can be asked for every project,
// the way the CLI's flag asks.
func TestAllProjectsIsReachableThroughMCP(t *testing.T) {
	testenv.SkipUnlessFull(t)
	_, call := inProcessDaemon(t)
	sess := mcpSession(t, call)
	for _, p := range []string{canonProject(t, "/tmp/one"), canonProject(t, "/tmp/two")} {
		if _, err := call(protocol.Request{Verb: "task.create", Project: p,
			Args: map[string]any{"title": "in " + p}}); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	scoped := callTool(t, sess, "list", map[string]any{"project": canonProject(t, "/tmp/one")})
	if scoped.IsError {
		t.Fatalf("scoped list: %s", text(scoped))
	}
	var one struct{ Count int }
	if err := json.Unmarshal([]byte(text(scoped)), &one); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if one.Count != 1 {
		t.Fatalf("scoped list found %d, want 1: %s", one.Count, text(scoped))
	}

	all := callTool(t, sess, "list", map[string]any{"project": canonProject(t, "/tmp/one"), "all_projects": true})
	if all.IsError {
		t.Fatalf("all-projects list: %s", text(all))
	}
	var every struct{ Count int }
	if err := json.Unmarshal([]byte(text(all)), &every); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if every.Count != 2 {
		t.Fatalf("all-projects list found %d, want 2: %s", every.Count, text(all))
	}
}

// §6.1: a type the door DECLARES is a type the door ENFORCES. This walks
// every published property of every tool and sends a value of the wrong kind,
// requiring USAGE each time — so a property added later cannot be published
// without being checked. Criterion-shaped rather than mechanism-shaped: it
// says nothing about how the door validates, only that it does.
func TestEveryDeclaredTypeIsEnforced(t *testing.T) {
	testenv.SkipUnlessFull(t)
	_, call := inProcessDaemon(t)
	sess := mcpSession(t, call)

	// A value that is wrong for each declared type, and right for none.
	wrong := map[string]any{
		"string":  float64(7),
		"integer": "seven",
		"boolean": "seven",
		"array":   "seven",
	}
	checked := 0
	for _, v := range verbs.MCPTools() {
		schema, _ := json.Marshal(tool(v).InputSchema)
		var got struct {
			Properties map[string]struct {
				Type string `json:"type"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(schema, &got); err != nil {
			t.Fatalf("%s: unmarshal schema: %v", v.MCP, err)
		}
		for name, prop := range got.Properties {
			bad, ok := wrong[prop.Type]
			if !ok {
				t.Fatalf("%s.%s declares the unknown type %q", v.MCP, name, prop.Type)
			}
			args := map[string]any{"project": canonProject(t, "/tmp/p"), name: bad}
			// Fill the required arguments so the refusal is about the type
			// and not about something missing.
			for _, a := range v.Args {
				if a.Required && a.Name != name {
					args[a.Name] = "x"
				}
			}
			res := callTool(t, sess, v.MCP, args)
			if !res.IsError {
				t.Errorf("%s.%s declares %q and accepted %v: %s", v.MCP, name, prop.Type, bad, text(res))
				continue
			}
			if !strings.Contains(text(res), codes.Usage) {
				t.Errorf("%s.%s declares %q and refused %v with %s, want USAGE",
					v.MCP, name, prop.Type, bad, text(res))
			}
			checked++
		}
	}
	if checked < 40 {
		t.Fatalf("only %d properties were checked; the walk is not covering the surface", checked)
	}
}

// §6.1 / §7.4: a mutating call made through each door with the same
// base_updated_at returns the same document — the guard is not merely present
// on both, it means the same thing on both.
func TestBothDoorsGuardAMutationTheSameWay(t *testing.T) {
	testenv.SkipUnlessFull(t)
	d, call := inProcessDaemon(t)
	tick := new(atomic.Int64)
	tick.Store(1_700_000_000_000)
	d.Now = func() int64 { return tick.Add(1) }
	sess := mcpSession(t, call)
	project := canonProject(t, "/tmp/p")

	make := func(title string) (string, int64) {
		t.Helper()
		raw, err := call(protocol.Request{Verb: "task.create", Project: project,
			Args: map[string]any{"title": title}})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		var res struct {
			Task struct {
				ID        string `json:"id"`
				UpdatedAt int64  `json:"updated_at"`
			} `json:"task"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return res.Task.ID, res.Task.UpdatedAt
	}

	// Two identical tasks, so each door acts on its own and the documents can
	// be compared field for field apart from the id and the timestamps.
	viaCLI, cliBase := make("guarded")
	viaMCP, mcpBase := make("guarded")

	cliResp := d.Answer(protocol.Request{Verb: "task.update", Project: project, BaseUpdatedAt: cliBase,
		Args: map[string]any{"id": viaCLI, "title": "renamed"}})
	if cliResp.Error != nil {
		t.Fatalf("the CLI door refused a current base_updated_at: %+v", cliResp.Error)
	}
	res := callTool(t, sess, "create", map[string]any{"title": "ignored", "project": project})
	if res.IsError {
		t.Fatalf("setup: %s", text(res))
	}
	mcpRes := callTool(t, sess, "claim", map[string]any{
		"id": viaMCP, "project": project, "base_updated_at": mcpBase})
	if mcpRes.IsError {
		t.Fatalf("the MCP door refused a current base_updated_at: %s", text(mcpRes))
	}

	// And a stale one is refused identically on both.
	cliStale := d.Answer(protocol.Request{Verb: "task.update", Project: project, BaseUpdatedAt: cliBase,
		Args: map[string]any{"id": viaCLI, "title": "again"}})
	mcpStale := callTool(t, sess, "claim", map[string]any{
		"id": viaMCP, "project": project, "base_updated_at": mcpBase})
	if cliStale.Error == nil || !mcpStale.IsError {
		t.Fatalf("a stale guard was not refused on both doors: cli=%+v mcp=%s", cliStale.Error, text(mcpStale))
	}
	if !strings.Contains(text(mcpStale), codes.Conflict) || cliStale.Error.Code != codes.Conflict {
		t.Fatalf("the doors disagree on the code: cli=%s mcp=%s", cliStale.Error.Code, text(mcpStale))
	}
	// The messages differ only in the numbers, which are each door's own row.
	for _, want := range []string{"task moved", "updated_at is"} {
		if !strings.Contains(cliStale.Error.Message, want) || !strings.Contains(text(mcpStale), want) {
			t.Fatalf("the doors refuse in different words: cli=%q mcp=%s", cliStale.Error.Message, text(mcpStale))
		}
	}
}

// canonProject is a project key BOTH doors agree on. The MCP door resolves
// what it is handed (§4.1) and the daemon takes the string as it comes, so a
// fixture that spells the same directory two ways is testing its own
// inconsistency — which is what canonProject(t, "/tmp/p") became once a path that does not
// exist yet started resolving its symlinks like one that does.
func canonProject(t *testing.T, dir string) string {
	t.Helper()
	p, err := project.Resolve(project.Options{Explicit: dir})
	if err != nil {
		t.Fatalf("resolve %s: %v", dir, err)
	}
	return p
}

// §7.1 with §13.1: the server registers under the plugin's identity and its
// tools are bare verbs. The registration name is what namespaces them in a
// client, which is why the tool names carry no plugin prefix of their own.
func TestTheServerNameCarriesTheIdentityAndTheToolsDoNot(t *testing.T) {
	if ServerName != "herdr-tasks" {
		t.Errorf("ServerName = %q, want the plugin id", ServerName)
	}
	// The pinned fifteen are bare verbs, and no plugin name of any spelling
	// leads them: the label the client shows already says which plugin this is.
	for _, v := range verbs.MCPTools() {
		if want := bareName(v.Name); v.MCP != want {
			t.Errorf("tool %q is not the bare verb %q (§7.1)", v.MCP, want)
		}
		for _, lead := range []string{"tasks_", ServerName + "_", config.Name + "_"} {
			if strings.HasPrefix(v.MCP, lead) {
				t.Errorf("tool %q is prefixed with %q, which the server label already says", v.MCP, lead)
			}
		}
	}
	if n := len(verbs.MCPTools()); n != len(pinnedTools) {
		t.Fatalf("%d tools, want the pinned %d", n, len(pinnedTools))
	}
}

// And the SERVED server really reports it, not just the constant.
func TestTheServedServerRegistersUnderThePluginIdentity(t *testing.T) {
	testenv.SkipUnlessFull(t)
	_, call := inProcessDaemon(t)
	sess := mcpSession(t, call)
	got := sess.InitializeResult().ServerInfo
	if got.Name != ServerName {
		t.Errorf("the server registered as %q, want %q", got.Name, ServerName)
	}
	if got.Title != Title {
		t.Errorf("the server's title is %q, want %q", got.Title, Title)
	}
	tools, err := sess.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	served := map[string]bool{}
	for _, tl := range tools.Tools {
		served[tl.Name] = true
	}
	for _, want := range pinnedTools {
		if !served[want] {
			t.Errorf("the server does not serve the pinned tool %q", want)
		}
	}
}

// §4.4 / §6.1: get carries all_projects with the same two behaviours the CLI
// flag has — a ULID resolves against every board and the document names the
// one it was found in, and a number is refused because it is only unique
// inside a project.
func TestGetAcrossProjectsThroughMCP(t *testing.T) {
	testenv.SkipUnlessFull(t)
	_, call := inProcessDaemon(t)
	sess := mcpSession(t, call)
	here, there := canonProject(t, "/tmp/here"), canonProject(t, "/tmp/there")
	made, err := call(protocol.Request{Verb: "task.create", Project: there,
		Args: map[string]any{"title": "filed over there"}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var created struct {
		Task struct{ ID string } `json:"task"`
	}
	if err := json.Unmarshal(made, &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if scoped := callTool(t, sess, "get", map[string]any{"id": created.Task.ID, "project": here}); !scoped.IsError {
		t.Fatalf("without the flag a task on another board resolved: %s", text(scoped))
	} else if !strings.Contains(text(scoped), here) {
		t.Fatalf("the refusal does not name the caller's project: %s", text(scoped))
	}

	res := callTool(t, sess, "get", map[string]any{"id": created.Task.ID, "project": here, "all_projects": true})
	if res.IsError {
		t.Fatalf("all_projects get: %s", text(res))
	}
	var got struct {
		Task struct{ ID, Project string } `json:"task"`
	}
	if err := json.Unmarshal([]byte(text(res)), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Task.ID != created.Task.ID || got.Task.Project != there {
		t.Fatalf("all_projects get = %s in %q, want %s in %q",
			got.Task.ID, got.Task.Project, created.Task.ID, there)
	}

	num := callTool(t, sess, "get", map[string]any{"id": "1", "project": here, "all_projects": true})
	if !num.IsError {
		t.Fatalf("a number resolved across projects: %s", text(num))
	}
	if !strings.Contains(text(num), "only unique inside a project") {
		t.Fatalf("the refusal does not say why: %s", text(num))
	}
}

// §4.4 / §6.1: a transition carries all_projects through this door too — the
// case a sibling plugin hits when it releases a task filed on another board —
// and a verb that does not honour the flag refuses it rather than acting on
// the caller's own board.
func TestTransitionAcrossProjectsThroughMCP(t *testing.T) {
	testenv.SkipUnlessFull(t)
	_, call := inProcessDaemon(t)
	sess := mcpSession(t, call)
	here, there := canonProject(t, "/tmp/here"), canonProject(t, "/tmp/there")
	made, err := call(protocol.Request{Verb: "task.create", Project: there,
		Args: map[string]any{"title": "filed over there"}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var created struct {
		Task struct{ ID string } `json:"task"`
	}
	if err := json.Unmarshal(made, &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	res := callTool(t, sess, "claim", map[string]any{"id": created.Task.ID, "project": here, "all_projects": true})
	if res.IsError {
		t.Fatalf("all_projects claim: %s", text(res))
	}
	var got struct {
		Task struct{ ID, Project, Status string } `json:"task"`
	}
	if err := json.Unmarshal([]byte(text(res)), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Task.Project != there || got.Task.Status != "doing" {
		t.Fatalf("all_projects claim = %s in %q, want doing in %q", got.Task.Status, got.Task.Project, there)
	}
	no := callTool(t, sess, "goal", map[string]any{"id": created.Task.ID, "project": here, "all_projects": true})
	if !no.IsError {
		t.Fatalf("a verb that ignores all_projects accepted it: %s", text(no))
	}
	if !strings.Contains(text(no), "does not act across projects") {
		t.Fatalf("the refusal does not say why: %s", text(no))
	}
}

// §7.5, and the sentence that keeps the declaration from being `--as` with a
// different spelling: it is read from how the door was STARTED and can never
// arrive per call. Three things have to hold at once, so all three are here —
// no schema offers it, a call that tries to carry it is refused, and a call
// that tries to carry it does not reach the daemon carrying it either.
func TestTheOperatorDeclarationNeverArrivesPerCall(t *testing.T) {
	testenv.SkipUnlessFull(t)
	for _, v := range verbs.MCPTools() {
		props, _ := tool(v).InputSchema.(map[string]any)["properties"].(map[string]any)
		for _, name := range []string{"operator", "as", "principal", "human"} {
			if _, ok := props[name]; ok {
				t.Errorf("tool %q offers %q as an argument; §7.5 forbids the declaration "+
					"reaching a door through a call", v.MCP, name)
			}
		}
	}

	// What actually reached the daemon, recorded rather than inferred.
	var seen []protocol.Request
	var mu sync.Mutex
	_, real := inProcessDaemon(t)
	spy := func(req protocol.Request) (json.RawMessage, error) {
		mu.Lock()
		seen = append(seen, req)
		mu.Unlock()
		return real(req)
	}
	project := canonProject(t, "/tmp/p")

	sess := mcpSession(t, spy)
	res := callTool(t, sess, "create", map[string]any{
		"title": "declared by the caller", "project": project, "operator": true})
	if !res.IsError {
		t.Fatalf("the door accepted a call carrying the declaration: %s", text(res))
	}
	if got := text(res); !strings.Contains(got, codes.Usage) || !strings.Contains(got, "operator") {
		t.Fatalf("refused with %s, want USAGE naming operator", got)
	}
	mu.Lock()
	reached := len(seen)
	mu.Unlock()
	if reached != 0 {
		t.Fatalf("%d requests reached the daemon; the refusal happens at the door", reached)
	}

	// And an undeclared door sends the declaration false even when the same
	// call, minus the rejected argument, succeeds.
	if res := callTool(t, sess, "create", map[string]any{"title": "ordinary", "project": project}); res.IsError {
		t.Fatalf("create: %s", text(res))
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 {
		t.Fatalf("%d requests reached the daemon, want 1", len(seen))
	}
	if seen[0].Operator {
		t.Fatal("an undeclared door told the daemon it speaks for the operator")
	}
}

// The other half: a door started WITH the declaration says so on every call,
// and the operator verb it was refused before now goes through. This is the
// door-side of §3.7 — the same tool call, the same daemon, two doors.
func TestTheDeclaredDoorIsTheOperatorAndTheUndeclaredOneIsNot(t *testing.T) {
	testenv.SkipUnlessFull(t)
	t.Setenv("HERDR_PANE_ID", "")
	_, call := inProcessDaemon(t)
	project := canonProject(t, "/tmp/p")

	for _, tc := range []struct {
		name string
		opt  Options
		want string
	}{
		{"a door nobody declared", Options{}, "none"},
		{"a door declared with --operator", Options{Operator: true}, "human"},
	} {
		sess := mcpSessionWith(t, call, tc.opt)
		res := callTool(t, sess, "note_add", map[string]any{"body": "filed through " + tc.name, "project": project})
		if res.IsError {
			t.Fatalf("%s: note_add: %s", tc.name, text(res))
		}
		var added struct {
			Note struct {
				Author string `json:"author"`
			} `json:"note"`
		}
		if err := json.Unmarshal([]byte(text(res)), &added); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if added.Note.Author != tc.want {
			t.Fatalf("%s: author = %q, want %q", tc.name, added.Note.Author, tc.want)
		}
	}
}

// And the refusal an undeclared door meets says what would change it. A bare
// FORBIDDEN is true and useless here: the caller is a harness with no shell,
// so "only the operator promotes a note" leaves it nothing to do. Driven with
// a stub daemon rather than a real one, because the operator verbs this fires
// on are exactly the ones not yet on the door — the asymmetry §7.3's parity
// MUST closes, in its own task.
func TestAnUndeclaredDoorIsToldWhyItHasNoPrincipal(t *testing.T) {
	testenv.SkipUnlessFull(t)
	t.Setenv("HERDR_PANE_ID", "")
	refuse := func(protocol.Request) (json.RawMessage, error) {
		return nil, codes.New(codes.Forbidden, "only the operator promotes a note")
	}
	conflict := func(protocol.Request) (json.RawMessage, error) {
		return nil, codes.New(codes.Conflict, "note is dropped")
	}
	project := canonProject(t, "/tmp/p")
	args := map[string]any{"body": "an idea", "project": project}

	got := text(callTool(t, mcpSessionWith(t, refuse, Options{}), "note_add", args))
	for _, want := range []string{codes.Forbidden, "only the operator promotes a note", "--operator", "§3.7"} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal does not say %q: %s", want, got)
		}
	}
	// A declared door is the operator, so nothing is added to its refusals.
	if got := text(callTool(t, mcpSessionWith(t, refuse, Options{Operator: true}), "note_add", args)); strings.Contains(got, "--operator") {
		t.Errorf("a declared door was told to declare itself: %s", got)
	}
	// And only FORBIDDEN is explained. Every other code has nothing to do
	// with which principal the caller is.
	if got := text(callTool(t, mcpSessionWith(t, conflict, Options{}), "note_add", args)); strings.Contains(got, "--operator") {
		t.Errorf("a CONFLICT was answered with an identity hint: %s", got)
	}
}

// §7.5's fourth property, on the mechanism: a door given --operator inside a
// Herdr pane refuses to START. This is defence in depth rather than the thing
// that stops the escalation — the test below holds that — and it earns its
// place by failing loudly once instead of running an ambiguous door all day.
// It went in with no test at all, and `if false && ...` left the whole suite
// green.
func TestServeRefusesADeclaredDoorInsideAPane(t *testing.T) {
	testenv.SkipUnlessFull(t)
	// Cancelled before it starts, so the cases that ARE allowed to serve
	// return from the transport promptly instead of reading stdin. What is
	// under test is which of the two answers comes back.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	silent := func(protocol.Request) (json.RawMessage, error) { return nil, nil }

	for name, tc := range map[string]struct {
		pane    string
		opt     Options
		refused bool
	}{
		"declared inside a pane":   {"wF:p1", Options{Operator: true}, true},
		"declared in no pane":      {"", Options{Operator: true}, false},
		"undeclared inside a pane": {"wF:p1", Options{}, false},
		"undeclared and paneless":  {"", Options{}, false},
	} {
		t.Setenv("HERDR_PANE_ID", tc.pane)
		done := make(chan error, 1)
		opt := tc.opt
		go func() { done <- Serve(ctx, silent, opt) }()
		var err error
		select {
		case err = <-done:
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: Serve did not return", name)
		}
		// The same two shapes errorResult unwraps: an error that already knows
		// its §6.3 code, and *codes.Error itself.
		var coded Coded
		var ce *codes.Error
		refused := (errors.As(err, &coded) && coded.Code() == codes.Forbidden) ||
			(errors.As(err, &ce) && ce.Code == codes.Forbidden)
		if refused != tc.refused {
			t.Errorf("%s: refused = %v, want %v (err = %v)", name, refused, tc.refused, err)
			continue
		}
		if tc.refused && !strings.Contains(err.Error(), tc.pane) {
			t.Errorf("%s: the refusal does not name the pane: %v", name, err)
		}
	}
}

// And the property the guard above is only the loud half of: a declared door
// that somehow IS inside a pane is still that pane's agent, never `human`.
// This is what actually prevents the escalation — `Daemon.actor` reads the
// pane before it reads the declaration — so it is pinned here rather than
// left resting on a startup check that a caller can avoid by starting the
// door another way.
func TestAnInPaneDeclaredDoorIsStillThePanesAgent(t *testing.T) {
	testenv.SkipUnlessFull(t)
	var seen []protocol.Request
	var mu sync.Mutex
	_, real := inProcessDaemon(t)
	// AFTER inProcessDaemon, which clears the trio at the one seam every test
	// goes through (§12.3). Named before it, this test read `human` and would
	// have passed for the wrong reason on a door that never sent the pane.
	t.Setenv("HERDR_PANE_ID", "wF:p1")
	spy := func(req protocol.Request) (json.RawMessage, error) {
		mu.Lock()
		seen = append(seen, req)
		mu.Unlock()
		return real(req)
	}
	project := canonProject(t, "/tmp/p")

	sess := mcpSessionWith(t, spy, Options{Operator: true})
	res := callTool(t, sess, "note_add", map[string]any{"body": "filed from a declared pane", "project": project})
	if res.IsError {
		t.Fatalf("note_add: %s", text(res))
	}
	var added struct {
		Note struct {
			Author string `json:"author"`
		} `json:"note"`
	}
	if err := json.Unmarshal([]byte(text(res)), &added); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if added.Note.Author != "agent:wF:p1" {
		t.Fatalf("author = %q, want agent:wF:p1: the declaration overruled the pane", added.Note.Author)
	}
	// And the door really did send the declaration, so what refused it is the
	// daemon's ordering and not the door quietly dropping the flag. Without
	// this the test would still pass on a door that never sent it, which is a
	// different plugin from the one §7.5 describes.
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 {
		t.Fatalf("%d requests reached the daemon, want 1", len(seen))
	}
	if !seen[0].Operator || seen[0].PaneID != "wF:p1" {
		t.Fatalf("the daemon saw operator=%v pane=%q; the door dropped one of the two",
			seen[0].Operator, seen[0].PaneID)
	}
}

// The finding this fixes: a note whose content shipped inside another note's
// task sat in `proposed` forever and read as open work. Once folded it is
// decided, so `note list` with no status filter no longer counts it among the
// undecided — and the two doors say the same thing about it, because a fold
// only reads as an ending on the surface the operator is looking at.
func TestAFoldedNoteIsNoLongerUndecidedOnEitherDoor(t *testing.T) {
	testenv.SkipUnlessFull(t)
	t.Setenv("HERDR_PANE_ID", "")
	_, call := inProcessDaemon(t)
	project := canonProject(t, "/tmp/p")
	sess := mcpSessionWith(t, call, Options{Operator: true})

	for _, body := range []string{"the sweep is quiet", "and the second half of the same change"} {
		if res := callTool(t, sess, "note_add", map[string]any{"body": body, "project": project}); res.IsError {
			t.Fatalf("note_add: %s", text(res))
		}
	}
	if res := callTool(t, sess, "note_promote", map[string]any{"id": "1", "project": project}); res.IsError {
		t.Fatalf("note_promote: %s", text(res))
	}
	// Note #2 was filed after the task existed: it is folded into it, and no
	// second task is created.
	res := callTool(t, sess, "note_fold", map[string]any{"id": "2", "into": "1", "project": project})
	if res.IsError {
		t.Fatalf("note_fold: %s", text(res))
	}
	var folded struct {
		Note struct {
			Status string `json:"status"`
			TaskID string `json:"task_id"`
			Folded bool   `json:"folded"`
		} `json:"note"`
		Task struct {
			Seq int64 `json:"seq"`
		} `json:"task"`
	}
	if err := json.Unmarshal([]byte(text(res)), &folded); err != nil {
		t.Fatalf("read the fold: %v", err)
	}
	if folded.Note.Status != "task" || !folded.Note.Folded || folded.Task.Seq != 1 {
		t.Fatalf("bad fold: %+v", folded)
	}

	// A note whose own task exists is refused, naming the task holding it.
	if res := callTool(t, sess, "note_fold", map[string]any{"id": "1", "into": "1", "project": project}); !res.IsError {
		t.Fatal("folding a note that already has a task was allowed")
	} else if !strings.Contains(text(res), "#1") {
		t.Fatalf("refusal %q does not name the task holding the note", text(res))
	}

	// Neither door counts a folded note as undecided, and the two agree on
	// every note in the answer.
	undecided := map[string]bool{"inbox": true, "discussing": true, "needs_input": true, "proposed": true}
	type listed struct {
		Notes []struct {
			Seq    int64  `json:"seq"`
			Status string `json:"status"`
			Folded bool   `json:"folded"`
		} `json:"notes"`
		Count int `json:"count"`
	}
	cliRaw, err := call(protocol.Request{Verb: "note.list", Project: project, Operator: true})
	if err != nil {
		t.Fatalf("cli note.list: %v", err)
	}
	var viaCLI, viaMCP listed
	if err := json.Unmarshal(cliRaw, &viaCLI); err != nil {
		t.Fatalf("read the CLI list: %v", err)
	}
	mcpRes := callTool(t, sess, "note_list", map[string]any{"project": project})
	if mcpRes.IsError {
		t.Fatalf("note_list: %s", text(mcpRes))
	}
	if err := json.Unmarshal([]byte(text(mcpRes)), &viaMCP); err != nil {
		t.Fatalf("read the MCP list: %v", err)
	}
	cliJSON, _ := json.Marshal(viaCLI)
	mcpJSON, _ := json.Marshal(viaMCP)
	if string(cliJSON) != string(mcpJSON) {
		t.Fatalf("the doors disagree about the board:\n cli %s\n mcp %s", cliJSON, mcpJSON)
	}
	if viaCLI.Count != 2 {
		t.Fatalf("%d notes listed, want both of them still on the board", viaCLI.Count)
	}
	for _, n := range viaCLI.Notes {
		if undecided[n.Status] {
			t.Fatalf("note #%d is %q after being folded into a task", n.Seq, n.Status)
		}
	}

	// And the way back: unfolding returns note #2 to the undecided list,
	// through the door that folded it.
	if res := callTool(t, sess, "note_unfold", map[string]any{"id": "2", "project": project}); res.IsError {
		t.Fatalf("note_unfold: %s", text(res))
	}
	back := callTool(t, sess, "note_list", map[string]any{"status": "inbox", "project": project})
	var inbox listed
	if err := json.Unmarshal([]byte(text(back)), &inbox); err != nil {
		t.Fatalf("read the inbox: %v", err)
	}
	if inbox.Count != 1 || inbox.Notes[0].Seq != 2 {
		t.Fatalf("unfold did not return the note to the inbox: %+v", inbox)
	}
}

// Several notes are one change: they promote into ONE task, through the door.
func TestSeveralNotesPromoteIntoOneTaskThroughTheDoor(t *testing.T) {
	testenv.SkipUnlessFull(t)
	t.Setenv("HERDR_PANE_ID", "")
	_, call := inProcessDaemon(t)
	project := canonProject(t, "/tmp/p")
	sess := mcpSessionWith(t, call, Options{Operator: true})

	for _, body := range []string{"the sweep is quiet", "it drops the lease too", "same sweep, still no event"} {
		if res := callTool(t, sess, "note_add", map[string]any{"body": body, "project": project}); res.IsError {
			t.Fatalf("note_add: %s", text(res))
		}
	}
	res := callTool(t, sess, "note_promote", map[string]any{
		"id": "1", "also": []any{"2", "3"}, "project": project})
	if res.IsError {
		t.Fatalf("note_promote: %s", text(res))
	}
	var listed struct {
		Notes []struct {
			Seq    int64  `json:"seq"`
			Status string `json:"status"`
			TaskID string `json:"task_id"`
		} `json:"notes"`
	}
	all := callTool(t, sess, "note_list", map[string]any{"project": project})
	if err := json.Unmarshal([]byte(text(all)), &listed); err != nil {
		t.Fatalf("read the board: %v", err)
	}
	if len(listed.Notes) != 3 {
		t.Fatalf("%d notes on the board, want 3", len(listed.Notes))
	}
	for _, n := range listed.Notes {
		if n.Status != "task" {
			t.Fatalf("note #%d is %q, want task", n.Seq, n.Status)
		}
		if n.TaskID != listed.Notes[0].TaskID {
			t.Fatalf("note #%d points at %q, not the one task they were promoted into", n.Seq, n.TaskID)
		}
	}
	var tasks struct {
		Count int `json:"count"`
	}
	tl := callTool(t, sess, "list", map[string]any{"project": project})
	if err := json.Unmarshal([]byte(text(tl)), &tasks); err != nil {
		t.Fatalf("read the backlog: %v", err)
	}
	if tasks.Count != 1 {
		t.Fatalf("%d tasks, want the one they were folded into", tasks.Count)
	}
}

// §3.7 (0.10.0) with §7.3: the principal rule has nowhere left to be enforced
// for an operator verb, so it has to be READ — and read the same on both
// doors. TestHelpCarriesTheWhoRuleForEveryVerb holds that the registry
// composes it; this holds that the MCP door actually shows it, which is the
// half that goes silently missing when a door goes back to Short alone.
func TestEveryToolDescriptionCarriesTheWhoRule(t *testing.T) {
	for _, v := range verbs.MCPTools() {
		desc := tool(v).Description
		if !strings.Contains(desc, v.Short) {
			t.Errorf("%s: the tool description drops the one-line summary", v.MCP)
		}
		if v.Who != "" && !strings.Contains(desc, v.Who) {
			t.Errorf("%s: the tool description does not say who may call it; an agent reads this "+
				"where a human reads --help, and two texts would be two rules", v.MCP)
		}
	}
}

// §7.2 requires the instructions to say three things, and the audit behind
// task 86 found only the last two held. TestInstructionsCoverTheRequiredGround
// looks for seven words scattered anywhere in the paragraph, so deleting the
// opening sentence — the one that says WHAT THE PLUGIN IS — left it green,
// because "Herdr", "pane" and the rest all appear again further down. A
// keyword that survives in a different sentence is not the clause.
//
// This holds the first clause on its own: the plugin's own name, and the noun
// it is. It reads the opening sentence rather than the whole string, which is
// the difference that made the old check pass against the wrong text.
func TestTheInstructionsOpenBySayingWhatThePluginIs(t *testing.T) {
	opening, _, ok := strings.Cut(Instructions, ".")
	if !ok {
		t.Fatal("the instructions have no first sentence to read (§7.2)")
	}
	for _, want := range []string{ServerName, "backlog"} {
		if !strings.Contains(opening, want) {
			t.Errorf("the instructions open with %q, which does not say %q; §7.2 asks first for "+
				"what the plugin IS", opening, want)
		}
	}
}
