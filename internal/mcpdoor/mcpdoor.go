// Package mcpdoor serves MCP over stdio (§7.1). It is a thin door over the
// same daemon calls as the CLI and holds no state of its own (§7.2): every
// tool builds the same protocol.Request the CLI builds and returns the same
// JSON the CLI prints with --json (§7.4).
package mcpdoor

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/husniadil/herdr-tasks/internal/client"
	"github.com/husniadil/herdr-tasks/internal/codes"
	"github.com/husniadil/herdr-tasks/internal/daemon"
	"github.com/husniadil/herdr-tasks/internal/project"
	"github.com/husniadil/herdr-tasks/internal/protocol"
	"github.com/husniadil/herdr-tasks/internal/verbs"
)

// ServerName is how this server registers itself. It carries the plugin's
// identity — the manifest id — because that is what an operator wiring it into
// a client sees and names, and because it is the label a client namespaces
// this server's tools under. §7.1 names the tools themselves after the verb
// alone for that reason: the identity is already said once, here.
const ServerName = "herdr-tasks"

// Title is the display name.
const Title = "Herdr Tasks"

// Instructions is §7.2's required paragraph: what the plugin is, that
// pane/agent/workspace mean what Herdr says they mean, and where to start.
const Instructions = "herdr-tasks is the task backlog and notes board for agents running on Herdr. " +
	"A task moves todo → doing → review → done behind a claim with a renewable lease; a note is a " +
	"pre-decision idea that only the operator promotes into a task, keeps, or drops. Everything is " +
	"scoped to a project, the git root of the directory you are working in. `pane`, `agent`, " +
	"`harness`, `workspace` and `agent_status` mean what Herdr says they mean, and your principal is " +
	"derived from the pane you run in — you never declare who you are. The usual entry points are " +
	"`list` to find ready work, `claim` to take it, `touch` at the start of every turn to " +
	"renew the lease, and `submit` with a report and evidence when it is done. `goal` prints a " +
	"paste-ready /goal condition for a task. The CLI (`htask`) carries every verb, including the ones " +
	"missing here; `htask --help` lists them."

// Caller is what the door needs to reach the daemon. The default calls the
// real socket; a test swaps in something that answers in-process.
type Caller func(protocol.Request) (json.RawMessage, error)

// Options is what the door was STARTED with, which under the process-bound
// identity rule (§3.2) is the only place a door's identity may come from.
// There is one option and it is not a general-purpose bag: everything else a
// door needs, it derives per call.
type Options struct {
	// Operator is §7.5's declaration: this door speaks for the operator.
	// Read once from `htask mcp --operator` and never from a tool call, which
	// is what keeps it from being --as with a different spelling.
	Operator bool
}

// New builds the MCP server with the pinned tool list.
func New(call Caller, opt Options) *mcp.Server {
	if call == nil {
		call = client.Call
	}
	s := mcp.NewServer(&mcp.Implementation{
		Name:        ServerName,
		Title:       Title,
		Version:     daemon.Version,
		Description: "Tasks, claims and notes for a Herdr fleet",
	}, &mcp.ServerOptions{Instructions: Instructions})
	for _, v := range verbs.MCPTools() {
		s.AddTool(tool(v), handlerFor(v, call, opt))
	}
	return s
}

// Serve runs the server on stdio until the client disconnects (§7.1).
func Serve(ctx context.Context, call Caller, opt Options) error {
	// §7.5, and it is defence in depth rather than the guarantee: Daemon.actor
	// resolves the pane BEFORE it reads the declaration, so a declared door
	// inside a pane is that pane's agent on every call whether this returns
	// or not (TestAnInPaneDeclaredDoorIsStillThePanesAgent holds that). What
	// this buys is failing loudly once, instead of running a door whose two
	// answers about who it is disagree for as long as it is up.
	if opt.Operator && os.Getenv("HERDR_PANE_ID") != "" {
		return codes.Errorf(codes.Forbidden,
			"--operator declares this door speaks for the operator, but it is starting inside "+
				"Herdr pane %s, which makes it that pane's agent (§3.2); the declaration is for "+
				"a door in no pane (§7.5)", os.Getenv("HERDR_PANE_ID"))
	}
	return New(call, opt).Run(ctx, &mcp.StdioTransport{})
}

// tool renders one registry entry as an MCP tool. The schema is built from the
// same Args the CLI builds its flags from, which is what §6.1 parity means
// here: same name, same arguments, same result shape.
func tool(v verbs.Verb) *mcp.Tool {
	props := map[string]any{}
	required := []string{}
	for _, a := range v.Args {
		props[a.Name] = map[string]any{"type": jsonType(a.Type), "description": a.Desc}
		if a.Type == verbs.Strings {
			props[a.Name] = map[string]any{
				"type": "array", "items": map[string]any{"type": "string"}, "description": a.Desc,
			}
		}
		if a.Required {
			required = append(required, a.Name)
		}
	}
	// The globals, mirrored from the CLI's flags so that §6.1 parity is about
	// the whole surface and not only the per-verb half. --as is deliberately
	// absent: §3.2 says agent and human principals are derived, never
	// declared, and an MCP caller has no pane to derive one from.
	props[argProject] = map[string]any{"type": "string",
		"description": "The project to act in; defaults to the directory this server runs in (§4.2)"}
	props[argAllProjects] = map[string]any{"type": "boolean",
		"description": "Act across every project rather than this one"}
	if v.Mutates {
		props[argBaseUpdatedAt] = map[string]any{"type": "integer",
			"description": "Fail with CONFLICT if the row moved since this updated_at (§5.6)"}
	}
	schema := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		schema["required"] = required
	}
	return &mcp.Tool{
		Name:        v.MCP,
		Description: v.Short,
		InputSchema: schema,
	}
}

func jsonType(t string) string {
	switch t {
	case verbs.Int:
		return "integer"
	case verbs.Bool:
		return "boolean"
	default:
		return "string"
	}
}

// handlerFor turns a tool call into the same daemon call the CLI makes.
func handlerFor(v verbs.Verb, call Caller, opt Options) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := map[string]any{}
		if len(req.Params.Arguments) > 0 {
			if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
				return errorResult(codes.New(codes.Usage, "unreadable arguments: "+err.Error())), nil
			}
		}
		if err := checkArgs(v, args); err != nil {
			return errorResult(err), nil
		}
		explicit, _ := args[argProject].(string)
		allProjects, _ := args[argAllProjects].(bool)
		baseUpdatedAt, _ := args[argBaseUpdatedAt].(float64)
		delete(args, argProject)
		delete(args, argAllProjects)
		delete(args, argBaseUpdatedAt)

		cwd, _ := os.Getwd()
		proj, err := project.Resolve(project.Options{Explicit: explicit, Cwd: cwd, Warn: os.Stderr})
		if err != nil {
			return errorResult(codes.Errorf(codes.Usage, "cannot resolve the project: %v", err)), nil
		}
		raw, err := call(protocol.Request{
			Verb:          v.Name,
			Project:       proj,
			AllProjects:   allProjects,
			BaseUpdatedAt: int64(baseUpdatedAt),
			PaneID:        os.Getenv("HERDR_PANE_ID"),
			TabID:         os.Getenv("HERDR_TAB_ID"),
			WorkspaceID:   os.Getenv("HERDR_WORKSPACE_ID"),
			// From the door's own startup, never from args: §7.5 says the
			// declaration may not arrive per call, and this is the line that
			// makes that true rather than intended.
			Operator: opt.Operator,
			Args:     args,
		})
		if err != nil {
			return errorResult(withDeclarationHint(err, opt)), nil
		}
		var structured any
		if uerr := json.Unmarshal(raw, &structured); uerr != nil {
			structured = nil
		}
		return &mcp.CallToolResult{
			Content:           []mcp.Content{&mcp.TextContent{Text: string(raw)}},
			StructuredContent: structured,
		}, nil
	}
}

// Coded is any failure that already knows its §6.3 code. The socket client
// returns one; so does anything else that speaks for the daemon.
type Coded interface {
	Code() string
	Message() string
}

// errorResult is §7.4: a failure is a tool error carrying the §6.3 code, never
// a JSON-RPC protocol error.
func errorResult(err error) *mcp.CallToolResult {
	code, message := codes.Unexpected, err.Error()
	var coded Coded
	var ce *codes.Error
	switch {
	case errors.As(err, &coded):
		code, message = coded.Code(), coded.Message()
	case errors.As(err, &ce):
		code, message = ce.Code, ce.Message
	}
	body, _ := json.Marshal(map[string]any{"error": map[string]string{"code": code, "message": message}})
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: string(body)}},
	}
}

// argOperator is not an argument. It is named here so that checkArgs can
// refuse it BY name, because a reserved word nothing spells out is one a
// later edit spells differently.
const argOperator = "operator"

// The names of the shared arguments every tool takes, mirroring the CLI's
// global flags. They are constants because tool() declares them and checkArgs
// enforces them, and a typo in one place would make the door publish a promise
// it does not keep.
const (
	argProject       = "project"
	argAllProjects   = "all_projects"
	argBaseUpdatedAt = "base_updated_at"
)

// checkArgs holds the door to the schema it published. The go-sdk validates
// arguments only for tools registered through its generic AddTool, which wants
// a Go type per tool; this registry is a table, so the check lives here and
// walks the SAME verbs.Arg list tool() renders the schema from. One source,
// two readers — and a test drives every published property through both, so
// they cannot come apart.
//
// Without it the door accepted what its own schema forbade and coerced it
// silently: limit "nope" became 0, which the filters read as unlimited, and
// priority 2.9 became 2. The CLI refuses both at parse.
func checkArgs(v verbs.Verb, args map[string]any) error {
	for _, name := range sortedNames(args) {
		// §7.5: the operator declaration is read from how this door was
		// started and never from a call. The daemon would refuse an argument
		// no verb declares anyway, but leaving it to the daemon means the
		// refusal is generic and stops being a refusal at all the day a verb
		// declares an argument by that name. The door owns this one.
		if name == argOperator {
			return codes.Errorf(codes.Usage,
				"%q is not an argument: a door speaks for the operator because of how it was "+
					"started, never because a call says so (§7.5). Start the server with "+
					"--operator instead", argOperator)
		}
		var want string
		switch name {
		case argProject:
			want = verbs.String
		case argAllProjects:
			want = verbs.Bool
		case argBaseUpdatedAt:
			want = verbs.Int
		default:
			a, ok := argByName(v, name)
			if !ok {
				// An argument this verb does not declare: the daemon refuses
				// it, and says something more useful than a type could.
				continue
			}
			want = a.Type
		}
		if err := checkType(name, want, args[name]); err != nil {
			return err
		}
	}
	return nil
}

func argByName(v verbs.Verb, name string) (verbs.Arg, bool) {
	for _, a := range v.Args {
		if a.Name == name {
			return a, true
		}
	}
	return verbs.Arg{}, false
}

// checkType compares one received value against its declared type. Numbers
// arrive as float64 because that is what encoding/json makes of them, so an
// integer is a float64 with nothing after the point.
func checkType(name, want string, got any) error {
	switch want {
	case verbs.Int:
		f, ok := got.(float64)
		if !ok {
			return codes.Errorf(codes.Usage, "%s must be an integer, not %s", name, jsonKind(got))
		}
		if f != math.Trunc(f) {
			return codes.Errorf(codes.Usage, "%s must be an integer, not the fraction %v", name, f)
		}
	case verbs.Bool:
		if _, ok := got.(bool); !ok {
			return codes.Errorf(codes.Usage, "%s must be true or false, not %s", name, jsonKind(got))
		}
	case verbs.Strings:
		list, ok := got.([]any)
		if !ok {
			return codes.Errorf(codes.Usage, "%s must be a list of strings, not %s", name, jsonKind(got))
		}
		for i, item := range list {
			if _, ok := item.(string); !ok {
				return codes.Errorf(codes.Usage, "%s[%d] must be a string, not %s", name, i, jsonKind(item))
			}
		}
	default:
		if _, ok := got.(string); !ok {
			return codes.Errorf(codes.Usage, "%s must be a string, not %s", name, jsonKind(got))
		}
	}
	return nil
}

// jsonKind names what arrived, in the caller's vocabulary rather than Go's.
func jsonKind(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return "true or false"
	case float64:
		if x != math.Trunc(x) {
			return "a fraction"
		}
		return "a number"
	case string:
		return "a string"
	case []any:
		return "a list"
	default:
		return "an object"
	}
}

// sortedNames keeps the refusal stable: a call with two wrong arguments names
// the same one every time.
func sortedNames(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Global describes what the MCP door does with one of the CLI's global flags.
// It is exported because the flags themselves live in the CLI, in package
// main, and the drift tests read this table from both sides: one asserts every
// CLI global is accounted for here, the other asserts the tools carry exactly
// what this table says they do. An absence here is a global that is
// unreachable through MCP with a parity test that cannot see it.
type Global struct {
	// Property is the MCP argument this flag becomes, or "" when excluded.
	Property string
	// Only limits the flag to some verbs, mirroring the CLI. Nil means all.
	Only func(verbs.Verb) bool
	// Excluded is why the door does not offer it, and is required when
	// Property is empty.
	Excluded string
}

// Globals maps the CLI's flag names to their place on the MCP door.
var Globals = map[string]Global{
	"project":      {Property: argProject},
	"all-projects": {Property: argAllProjects},
	"base-updated-at": {
		Property: argBaseUpdatedAt,
		Only:     func(v verbs.Verb) bool { return v.Mutates },
	},
	"as": {Excluded: "§7.3: an identity claim carried BY a call, which the process-bound " +
		"identity rule (§3.2) forbids a long-lived door to read; the door's own identity " +
		"comes from how it was started, and --operator (§7.5) is the startup counterpart"},
	"json": {Excluded: "§7.1: a tool call already answers with a structured document, " +
		"so there is no prose mode to switch off"},
	"follow": {Excluded: "§7.1: a tool call answers once; a stream is not a tool call"},
}

// GlobalsFor is the properties the globals add to one verb's tool.
func GlobalsFor(v verbs.Verb) []string {
	out := []string{}
	for _, g := range Globals {
		if g.Property == "" {
			continue
		}
		if g.Only == nil || g.Only(v) {
			out = append(out, g.Property)
		}
	}
	sort.Strings(out)
	return out
}

// withDeclarationHint says WHY an undeclared door was refused. A door with no
// principal (§3.7) meets the operator verbs as a bare FORBIDDEN, and the
// message the daemon writes — "only the operator promotes a note" — is true
// and tells the caller nothing about the one thing that would change it. The
// door is the surface that knows both facts, so it is the surface that says
// them.
func withDeclarationHint(err error, opt Options) error {
	if opt.Operator || os.Getenv("HERDR_PANE_ID") != "" {
		return err
	}
	var coded Coded
	var ce *codes.Error
	switch {
	case errors.As(err, &coded):
		if coded.Code() != codes.Forbidden {
			return err
		}
	case errors.As(err, &ce):
		if ce.Code != codes.Forbidden {
			return err
		}
	default:
		return err
	}
	return codes.Errorf(codes.Forbidden, "%s. This door stands in no Herdr pane and was "+
		"not started with --operator, so it has no principal (§3.7); the operator declares "+
		"a door once, in the client's server configuration (§7.5)", strings.TrimRight(err.Error(), "."))
}
