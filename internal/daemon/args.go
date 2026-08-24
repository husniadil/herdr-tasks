package daemon

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/husniadil/herdr-tasks/internal/codes"
	"github.com/husniadil/herdr-tasks/internal/tasks"
)

// The args map arrives as decoded JSON, so a number may be a float64 and a
// list may be []any. These readers are the one place that knows it.

func argString(args map[string]any, name string) string {
	v, ok := args[name]
	if !ok || v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	case float64:
		return strconv.FormatInt(int64(x), 10)
	case bool:
		return strconv.FormatBool(x)
	default:
		return ""
	}
}

func argInt(args map[string]any, name string) int64 {
	v, ok := args[name]
	if !ok || v == nil {
		return 0
	}
	switch x := v.(type) {
	case float64:
		return int64(x)
	case int64:
		return x
	case int:
		return int64(x)
	case json.Number:
		n, _ := x.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(x, 10, 64)
		return n
	default:
		return 0
	}
}

func argBool(args map[string]any, name string) bool {
	v, ok := args[name]
	if !ok || v == nil {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		b, _ := strconv.ParseBool(x)
		return b
	default:
		return false
	}
}

func argStrings(args map[string]any, name string) []string {
	v, ok := args[name]
	if !ok || v == nil {
		return nil
	}
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		if x == "" {
			return nil
		}
		return []string{x}
	default:
		return nil
	}
}

// has reports whether the caller sent a key at all, which is what separates
// "clear this field" from "leave it alone" in an update patch.
func has(args map[string]any, name string) bool {
	_, ok := args[name]
	return ok
}

func parseMS(s string) (int64, bool) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// decodeArgs reads a parked action's stored payload back into an args map
// (§9.3): the re-run must carry exactly what the original call carried.
func decodeArgs(payload string, out *map[string]any) error {
	if payload == "" || payload == "null" {
		*out = map[string]any{}
		return nil
	}
	return json.Unmarshal([]byte(payload), out)
}

// oneOf refuses a filter value the vocabulary does not hold. An empty value is
// "no filter" and passes; anything else has to be a word the store can match,
// because a value that cannot match is answered with an empty list that reads
// as a fact about the board (§6.2). The refusal names what the caller could
// have said, which is the whole of what it is for.
func oneOf(what, got string, allowed []string) error {
	if got == "" {
		return nil
	}
	for _, a := range allowed {
		if got == a {
			return nil
		}
	}
	return codes.Errorf(codes.Usage, "%q is not a %s; it is one of %s",
		got, what, strings.Join(allowed, ", "))
}

// taskStatuses, noteStatuses and entities are the three vocabularies a filter
// is checked against, read from the state machine rather than restated here.
func taskStatuses() []string {
	out := make([]string, 0, len(tasks.Statuses))
	for _, s := range tasks.Statuses {
		out = append(out, string(s))
	}
	return out
}

func noteStatuses() []string {
	out := make([]string, 0, len(tasks.NoteStatuses))
	for _, s := range tasks.NoteStatuses {
		out = append(out, string(s))
	}
	return out
}

// entities is the three event tables §8.1 names. It is not derived from a
// vocabulary elsewhere because there is no elsewhere: store.Events itself
// spells the set out.
func entities() []string { return []string{"task", "note", "parked"} }
