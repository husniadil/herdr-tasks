package store

import (
	"strings"
	"testing"
)

// §5.4: a task is addressed by its 26-character id or by the project's own
// number. Every rendering this plugin has prints that number with a `#` in
// front of it — `#12  todo  …` — so `#12` is the form a reader copies, and
// refusing it made the board's own output unusable as input.
func TestARefTakesTheHashTheBoardPrints(t *testing.T) {
	for _, ref := range []string{"12", "#12", " #12 "} {
		clause, arg, err := refClause(ref)
		if err != nil {
			t.Fatalf("refClause(%q): %v", ref, err)
		}
		if clause != "seq = ?" || arg != int64(12) {
			t.Errorf("refClause(%q) = %q, %v; want seq = ?, 12", ref, clause, arg)
		}
	}
}

// A `#` on its own, or on something that is not a number, is still nothing
// this can look up.
func TestAHashIsNotAReferenceByItself(t *testing.T) {
	for _, ref := range []string{"#", "#abc", "#0", "#-1", "# 12"} {
		if _, _, err := refClause(ref); err == nil {
			t.Errorf("refClause(%q) was accepted", ref)
		} else if !strings.Contains(err.Error(), ref) && !strings.Contains(err.Error(), strings.TrimSpace(ref)) {
			t.Errorf("refClause(%q) refused without naming what it read: %v", ref, err)
		}
	}
}
