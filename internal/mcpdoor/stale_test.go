package mcpdoor

import (
	"encoding/json"
	"testing"

	"github.com/husniadil/herdr-tasks/internal/protocol"
	"github.com/husniadil/herdr-tasks/internal/verbs"
)

// TestTheDoorTellsTheDaemonWhichBuildServedTheInstructions holds the door half
// of the stale-instructions report. §7.1 hands Instructions to the server once,
// at construction, so a session started before a correction goes on acting on
// the old prose forever; the repository is right, the pin is green, the binary
// on disk is current, and nothing the agent can see says otherwise. The skew
// warning in internal/client cannot cover it twice over: it compares the door
// with the DAEMON rather than with the file at the door's own path, and it
// writes to stderr, which on a stdio door is the MCP client's log and not the
// agent's channel.
//
// So the only process that knows how old the served prose is, is the door, and
// the only way the daemon can say so is if the door tells it. This test drives
// a real tool call through a live session and reads what reached the daemon.
func TestTheDoorTellsTheDaemonWhichBuildServedTheInstructions(t *testing.T) {
	var got protocol.Request
	sess := mcpSession(t, func(req protocol.Request) (json.RawMessage, error) {
		got = req
		return json.RawMessage(`{}`), nil
	})

	callTool(t, sess, "doctor", nil)

	if got.Verb == "" {
		t.Fatal("no request reached the daemon; the test would pass on a door that called nothing")
	}
	if got.Build != verbs.ThisBuild() {
		t.Fatalf("the door sent build %+v, want %+v; without it the daemon cannot say that the "+
			"instructions this session is acting on are older than the binary at the door's path",
			got.Build, verbs.ThisBuild())
	}
	if got.Build.Exe == "" {
		t.Fatal("the door sent a build with no path; the daemon stats that path to answer the question")
	}
}

// TestEveryToolCarriesTheDoorBuildNotOnlyDoctor guards the obvious narrowing.
// Sending it from the doctor handler alone would pass the test above and leave
// the field a special case, which is one edit away from being dropped when
// doctor's handler is refactored.
func TestEveryToolCarriesTheDoorBuildNotOnlyDoctor(t *testing.T) {
	for _, v := range verbs.MCPTools() {
		var got protocol.Request
		sess := mcpSession(t, func(req protocol.Request) (json.RawMessage, error) {
			got = req
			return json.RawMessage(`{}`), nil
		})
		args := map[string]any{}
		for _, a := range v.Args {
			if !a.Required {
				continue
			}
			switch a.Type {
			case verbs.Int:
				args[a.Name] = 1
			case verbs.Bool:
				args[a.Name] = true
			case verbs.Strings:
				args[a.Name] = []any{"x"}
			default:
				args[a.Name] = "x"
			}
		}
		res := callTool(t, sess, v.MCP, args)
		if res.IsError {
			// The door refused before calling; nothing to check, and the
			// refusal is another test's business.
			continue
		}
		if got.Build != verbs.ThisBuild() {
			t.Errorf("tool %s sent build %+v, want %+v", v.MCP, got.Build, verbs.ThisBuild())
		}
	}
}
