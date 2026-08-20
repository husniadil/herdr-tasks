package mcpdoor

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/husniadil/herdr-tasks/internal/config"
	"github.com/husniadil/herdr-tasks/internal/daemon"
	"github.com/husniadil/herdr-tasks/internal/herdrclient"
	"github.com/husniadil/herdr-tasks/internal/protocol"
	"github.com/husniadil/herdr-tasks/internal/store"
	"github.com/husniadil/herdr-tasks/internal/testenv"
	"github.com/husniadil/herdr-tasks/internal/verbs"
)

// pinnedTools is the tool list §7.1 pins. Adding a tool is a deliberate change
// to a semver-bound surface: it changes this list in the same commit, and
// removing or renaming one is a breaking change.
var pinnedTools = []string{
	"tasks_create",
	"tasks_list",
	"tasks_get",
	"tasks_claim",
	"tasks_touch",
	"tasks_release",
	"tasks_submit",
	"tasks_approve",
	"tasks_reject",
	"tasks_goal",
	"tasks_note_add",
	"tasks_note_list",
	"tasks_note_verdict",
	"tasks_events",
	"tasks_doctor",
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
		if !strings.HasPrefix(tl.MCP, ServerName+"_") {
			t.Errorf("tool %q is not named %s_<verb> (§7.1)", tl.MCP, ServerName)
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
		// Nothing extra beyond the CLI's args and the shared --project.
		for name := range got.Properties {
			if name == "project" {
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
	cliRaw, err := call(protocol.Request{Verb: "task.create", Project: "/tmp/p",
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
		Name:      "tasks_get",
		Arguments: map[string]any{"id": "1", "project": "/tmp/p"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool error: %s", text(res))
	}
	cliGet, err := call(protocol.Request{Verb: "task.get", Project: "/tmp/p", Args: map[string]any{"id": "1"}})
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
		Name:      "tasks_get",
		Arguments: map[string]any{"id": "404", "project": "/tmp/p"},
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
	for _, want := range []string{"Herdr", "pane", "agent", "workspace", "tasks_claim", "tasks_submit", "project"} {
		if !strings.Contains(Instructions, want) {
			t.Errorf("instructions do not mention %q (§7.2)", want)
		}
	}
}

// inProcessDaemon gives the MCP door a real daemon without a socket, so the
// parity test compares the doors and not the transport.
func inProcessDaemon(t *testing.T) (*daemon.Daemon, Caller) {
	t.Helper()
	dir := testenv.ShortDir(t)
	t.Setenv("HERDR_PLUGIN_STATE_DIR", dir)
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", t.TempDir())
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
