package ids

import (
	"strings"
	"testing"
)

// §5.4: entity ids are ULIDs stored as 26-char text.
func TestULIDIs26CrockfordChars(t *testing.T) {
	id := New(1_700_000_000_000)
	if len(id) != 26 {
		t.Fatalf("len = %d, want 26: %q", len(id), id)
	}
	for _, r := range id {
		if !strings.ContainsRune(alphabet, r) {
			t.Fatalf("%q is not Crockford base32: %q", r, id)
		}
	}
}

// §5.4: the ULID's leading 48 bits are the timestamp, so ids sort by time.
func TestULIDSortsByTime(t *testing.T) {
	early := New(1_700_000_000_000)
	late := New(1_700_000_001_000)
	if !(early < late) {
		t.Fatalf("%q should sort before %q", early, late)
	}
}

func TestULIDIsUniqueWithinAMillisecond(t *testing.T) {
	seen := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		id := New(1_700_000_000_000)
		if seen[id] {
			t.Fatalf("duplicate id %q at %d", id, i)
		}
		seen[id] = true
	}
}

func TestValidRejectsWrongShape(t *testing.T) {
	if !Valid(New(1_700_000_000_000)) {
		t.Fatal("a generated id must validate")
	}
	for _, bad := range []string{"", "short", strings.Repeat("A", 27), strings.Repeat("U", 26)} {
		if Valid(bad) {
			t.Fatalf("%q must not validate", bad)
		}
	}
}
