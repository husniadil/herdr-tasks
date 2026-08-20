// Package mcpdoor serves MCP over stdio (§7.1). It is a thin door over the
// same daemon calls as the CLI and holds no state of its own (§7.2): every
// tool builds the same protocol.Request the CLI builds and returns the same
// JSON the CLI prints with --json (§7.4).
package mcpdoor

import (
	"context"
	"encoding/json"
	"errors"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/husniadil/herdr-tasks/internal/client"
	"github.com/husniadil/herdr-tasks/internal/codes"
	"github.com/husniadil/herdr-tasks/internal/daemon"
	"github.com/husniadil/herdr-tasks/internal/project"
	"github.com/husniadil/herdr-tasks/internal/protocol"
	"github.com/husniadil/herdr-tasks/internal/verbs"
)

// ServerName is the MCP server name; tools are ServerName_<verb> (§7.1).
const ServerName = "tasks"

// Title is the display name.
const Title = "Tasks"

// Instructions is §7.2's required paragraph: what the plugin is, that
// pane/agent/workspace mean what Herdr says they mean, and where to start.
const Instructions = "herdr-tasks is the task backlog and notes board for agents running on Herdr. " +
	"A task moves todo → doing → review → done behind a claim with a renewable lease; a note is a " +
	"pre-decision idea that only the operator promotes into a task, keeps, or drops. Everything is " +
	"scoped to a project, the git root of the directory you are working in. `pane`, `agent`, " +
	"`harness`, `workspace` and `agent_status` mean what Herdr says they mean, and your principal is " +
	"derived from the pane you run in — you never declare who you are. The usual entry points are " +
	"tasks_list to find ready work, tasks_claim to take it, tasks_touch at the start of every turn to " +
	"renew the lease, and tasks_submit with a report and evidence when it is done. tasks_goal prints a " +
	"paste-ready /goal condition for a task. The CLI (`ht`) carries every verb, including the ones " +
	"missing here; `ht --help` lists them."

// Caller is what the door needs to reach the daemon. The default calls the
// real socket; a test swaps in something that answers in-process.
type Caller func(protocol.Request) (json.RawMessage, error)

// New builds the MCP server with the pinned tool list.
func New(call Caller) *mcp.Server {
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
		s.AddTool(tool(v), handlerFor(v, call))
	}
	return s
}

// Serve runs the server on stdio until the client disconnects (§7.1).
func Serve(ctx context.Context, call Caller) error {
	return New(call).Run(ctx, &mcp.StdioTransport{})
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
	// Both doors resolve the project the same way, and MCP callers may name it
	// explicitly for the same reason the CLI has --project (§4.2).
	props["project"] = map[string]any{"type": "string",
		"description": "The project to act in; defaults to the directory this server runs in (§4.2)"}
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
func handlerFor(v verbs.Verb, call Caller) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := map[string]any{}
		if len(req.Params.Arguments) > 0 {
			if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
				return errorResult(codes.New(codes.Usage, "unreadable arguments: "+err.Error())), nil
			}
		}
		explicit, _ := args["project"].(string)
		delete(args, "project")

		cwd, _ := os.Getwd()
		proj, err := project.Resolve(project.Options{Explicit: explicit, Cwd: cwd})
		if err != nil {
			return errorResult(codes.Errorf(codes.Usage, "cannot resolve the project: %v", err)), nil
		}
		raw, err := call(protocol.Request{
			Verb:        v.Name,
			Project:     proj,
			PaneID:      os.Getenv("HERDR_PANE_ID"),
			TabID:       os.Getenv("HERDR_TAB_ID"),
			WorkspaceID: os.Getenv("HERDR_WORKSPACE_ID"),
			Args:        args,
		})
		if err != nil {
			return errorResult(err), nil
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
