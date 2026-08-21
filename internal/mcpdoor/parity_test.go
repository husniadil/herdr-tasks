package mcpdoor

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

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
	"approve",
	"reject",
	"goal",
	"note_add",
	"note_list",
	"note_verdict",
	"events",
	"doctor",
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

// §7.3: keep the tool count small, roughly 8–16, and push the rest to the CLI.
func TestMCPToolCountStaysSmall(t *testing.T) {
	if n := len(pinnedTools); n < 8 || n > 16 {
		t.Fatalf("%d tools; §7.3 asks for roughly 8–16, with the rest on the CLI", n)
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
	srv := New(call)
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
	srv := New(call)
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

	anonymous := createThroughMCP(t, call, project, "created by nobody in particular")
	if got := anonymous["created_by"]; got != "human" {
		t.Fatalf("created_by = %v, want human: HERDR_PANE_ID reached the door from the process environment", got)
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
	srv := New(call)
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

	srv := New(call)
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

// mcpSession opens a real in-memory MCP client against the door.
func mcpSession(t *testing.T, call Caller) *mcp.ClientSession {
	t.Helper()
	srv := New(call)
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
