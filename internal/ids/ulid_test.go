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

// §5.4 / §8.2: the store orders its event trail by id, so ids minted inside
// one millisecond must still increase. Randomness alone does not give that.
func TestULIDIsMonotonicWithinAMillisecond(t *testing.T) {
	const frozen = int64(1_700_000_000_000)
	prev := New(frozen)
	for i := 1; i < 5000; i++ {
		got := New(frozen)
		if !(prev < got) {
			t.Fatalf("id %d went backwards within one millisecond: %q then %q", i, prev, got)
		}
		prev = got
	}
}

// A clock that steps back must not mint an id that sorts before one already
// issued: the trail's order is insertion order, not the host's opinion of time.
func TestULIDSurvivesAClockStepBackwards(t *testing.T) {
	late := New(1_700_000_000_000)
	early := New(1_600_000_000_000)
	if !(late < early) {
		t.Fatalf("a backwards clock reordered the trail: %q then %q", late, early)
	}
}

// Monotonic ids are still unique across a concurrent burst.
func TestULIDIsUniqueUnderConcurrency(t *testing.T) {
	const frozen = int64(1_700_000_000_000)
	const workers, each = 8, 500
	out := make(chan string, workers*each)
	done := make(chan struct{})
	for w := 0; w < workers; w++ {
		go func() {
			for i := 0; i < each; i++ {
				out <- New(frozen)
			}
			done <- struct{}{}
		}()
	}
	for w := 0; w < workers; w++ {
		<-done
	}
	close(out)
	seen := make(map[string]bool, workers*each)
	for id := range out {
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
	}
}
