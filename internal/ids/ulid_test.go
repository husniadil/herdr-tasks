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

// specDecode is a decoder written from the ULID spec, NOT from our encoder: a
// ULID is the 128-bit value rendered right-aligned in 26 Crockford base32
// characters, which is 130 bits, so the leading two bits are always zero and
// the first character never exceeds 7. Its top 48 bits are the Unix
// millisecond. Writing it here is the point — a decoder derived from encode()
// would agree with any encoding at all, including the wrong one.
func specDecode(t *testing.T, id string) (ms int64, ok bool) {
	t.Helper()
	if len(id) != 26 {
		return 0, false
	}
	// The timestamp is the top 48 of 128 bits. Read the whole thing as a big
	// integer over the 26 characters and shift the 80 random bits off.
	hi, lo := uint64(0), uint64(0) // hi holds bits 127..64, lo holds 63..0
	for _, c := range []byte(id) {
		v := strings.IndexByte(alphabet, c)
		if v < 0 {
			return 0, false
		}
		hi = hi<<5 | lo>>59
		lo = lo<<5 | uint64(v)
	}
	return int64(hi >> 16), true
}

// §5.4: the ids are ULIDs, which is the name of a format. A decoder that does
// not know about this implementation must be able to read the time out of one.
func TestIdsAreSpecULIDs(t *testing.T) {
	// The factory is monotonic and its state is process-wide, so an id minted
	// for a timestamp EARLIER than the last one keeps the last one's — the
	// clock-step guarantee, not a decoding failure. Ask it where it is rather
	// than assuming, so this holds however many times the package is run in
	// one process.
	base, ok := specDecode(t, New(1_900_000_000_000))
	if !ok {
		t.Fatal("a freshly minted id is not decodable")
	}
	for _, ms := range []int64{base + 1, base + 2, base + 1000} {
		id := New(ms)
		got, ok := specDecode(t, id)
		if !ok {
			t.Fatalf("%q is not decodable at all", id)
		}
		if got != ms {
			t.Errorf("New(%d) = %q, which a spec decoder reads as %d", ms, id, got)
		}
	}

	// And the shape the spec promises: the leading character carries three
	// bits, so it never exceeds 7.
	id := New(base + 2000)
	if id[0] > '7' {
		t.Errorf("the leading character of %q is %q, which is past the 3 bits a ULID has there", id, id[0])
	}
}

// leftAlignedEncode renders the 128 bits LEFT-aligned in the 26 characters,
// five bits at a time from the top. It lives in the test because ids in this
// rendering are what Reencode and migration 3 take as input, and nothing in
// the shipped code produces it.
func leftAlignedEncode(raw [16]byte) string {
	out := make([]byte, 26)
	for i := 0; i < 26; i++ {
		bit := i * 5
		acc := uint16(raw[bit/8]) << 8
		if bit/8+1 < 16 {
			acc |= uint16(raw[bit/8+1])
		}
		out[i] = alphabet[(acc>>(11-uint(bit%8)))&0x1f]
	}
	return string(out)
}

// §5.4: conversion is exact — same 128 bits, spelled the way the format says.
// A left-aligned id decodes to the millisecond it carries.
func TestReencodeKeepsTheBitsAndFixesTheSpelling(t *testing.T) {
	// Left-aligned, at a known millisecond.
	ms := int64(1_787_225_085_569)
	var raw [16]byte
	raw[0], raw[1] = byte(ms>>40), byte(ms>>32)
	raw[2], raw[3] = byte(ms>>24), byte(ms>>16)
	raw[4], raw[5] = byte(ms>>8), byte(ms)
	raw[6], raw[15] = 0xAB, 0x5C
	old := leftAlignedEncode(raw)

	got, ok := Reencode(old)
	if !ok {
		t.Fatalf("Reencode refused %q", old)
	}
	if len(got) != 26 {
		t.Fatalf("Reencode returned %d characters", len(got))
	}
	if decoded, ok := specDecode(t, got); !ok || decoded != ms {
		t.Fatalf("the converted id reads as %d, want %d", decoded, ms)
	}
	// The old string read by a spec decoder is the nonsense this is fixing.
	if decoded, _ := specDecode(t, old); decoded == ms {
		t.Fatalf("the left-aligned spelling already decoded correctly, so the fixture is wrong")
	}
	// Converting is a pure re-spelling: the same bits, twice.
	again, ok := Reencode(old)
	if !ok || again != got {
		t.Fatalf("Reencode is not deterministic: %q then %q", got, again)
	}
	// And ORDER is preserved, which is what makes a whole-store migration
	// safe: two left-aligned ids convert to two that compare the same way.
	raw[15] = 0x5D
	older, newer := old, leftAlignedEncode(raw)
	a, _ := Reencode(older)
	b, _ := Reencode(newer)
	if (older < newer) != (a < b) {
		t.Fatalf("re-encoding reordered %q<%q into %q<%q", older, newer, a, b)
	}
}

// What Reencode refuses, and — more importantly — what it CANNOT refuse.
//
// The left-aligned rendering leaves the bottom two bits as padding, so a
// string with either of them set was certainly not produced by it. That is the
// only signal available, and it is a weak one: three quarters of
// correctly-encoded ids have a bit set down there and the other quarter do
// not, so Reencode cannot tell an already-converted id from a left-aligned
// one. Running the migration twice would therefore corrupt most of a store,
// which is why idempotence rests on the schema version
// (TestTheReencodeRunsOnce in the store) and not on the ids themselves.
// Pinned here so nobody mistakes this check for that guard.
func TestReencodeRefusesOnlyWhatItCan(t *testing.T) {
	if _, ok := Reencode("not an id"); ok {
		t.Fatal("Reencode accepted a non-id")
	}
	if _, ok := Reencode(strings.Repeat("Z", 26)); ok {
		t.Fatal("Reencode accepted a string whose padding bits are set")
	}
	// And the honest half: some new ids look old to it.
	looksOld := 0
	for i := 0; i < 40; i++ {
		if _, ok := Reencode(New(1_900_000_100_000 + int64(i))); ok {
			looksOld++
		}
	}
	if looksOld == 0 {
		t.Fatal("no new id was accepted, so this check reads stronger than it is")
	}
}
