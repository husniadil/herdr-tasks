package daemon

import (
	"strings"
	"testing"

	"github.com/husniadil/herdr-tasks/internal/codes"
	"github.com/husniadil/herdr-tasks/internal/protocol"
)

// §6.2 with §6.3: a filter value outside the vocabulary can never match, so
// an empty list is not an answer to it — it is the answer to a different,
// well-formed question the caller did not ask. `--status revieww` read as "no
// tasks are in review", which is the one reading a board must never invent.
func TestAnUnknownFilterValueIsRefusedRatherThanAnsweredEmpty(t *testing.T) {
	d := newDaemon(t, nil)
	createTask(t, d, "something to not find")
	for _, c := range []struct {
		verb, arg, value, names string
	}{
		{"task.list", "status", "revieww", "review"},
		{"note.list", "status", "inboxx", "inbox"},
		{"events", "entity", "tasks", "task"},
	} {
		body := mustFail(t, d, protocol.Request{Verb: c.verb,
			Args: map[string]any{c.arg: c.value}}, codes.Usage)
		if !strings.Contains(body.Message, c.value) || !strings.Contains(body.Message, c.names) {
			t.Errorf("%s --%s %s: %q names neither the value nor what it could have been",
				c.verb, c.arg, c.value, body.Message)
		}
	}
}

// §6.1: the same check, on the streaming path. A follower that named an
// entity nobody has would otherwise sit on a stream that can never say
// anything, which looks exactly like a quiet project.
func TestAFollowedStreamRefusesAnUnknownEntity(t *testing.T) {
	d := newDaemon(t, nil)
	path := socketOf(t, d)
	resp, _, conn := ask(t, path, protocol.Request{Verb: "events", Project: proj, Follow: true,
		Args: map[string]any{"entity": "tasks"}})
	conn.Close()
	if resp.Error == nil {
		t.Fatalf("the stream was opened on an entity nobody has: %s", resp.Result)
	}
	if resp.Error.Code != codes.Usage {
		t.Fatalf("code = %s, want USAGE: %s", resp.Error.Code, resp.Error.Message)
	}
	plain := d.Answer(protocol.Request{Verb: "events", Project: proj,
		Args: map[string]any{"entity": "tasks"}})
	if plain.Error == nil || plain.Error.Message != resp.Error.Message {
		t.Fatalf("the two paths refuse differently:\nfollow: %+v\nplain:  %+v", resp.Error, plain.Error)
	}
}

// A known value still lists, so the check is a vocabulary and not a wall.
func TestKnownFilterValuesStillList(t *testing.T) {
	d := newDaemon(t, nil)
	createTask(t, d, "a todo task")
	mustCall(t, d, protocol.Request{Verb: "task.list", Args: map[string]any{"status": "todo"}})
	mustCall(t, d, protocol.Request{Verb: "note.list", Args: map[string]any{"status": "inbox"}})
	mustCall(t, d, protocol.Request{Verb: "events", Args: map[string]any{"entity": "task"}})
	mustCall(t, d, protocol.Request{Verb: "events"})
}
