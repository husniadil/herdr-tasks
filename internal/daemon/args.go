package daemon

import (
	"encoding/json"
	"strconv"
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
