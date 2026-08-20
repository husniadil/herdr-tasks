// Package ids generates the ULIDs the contract fixes as entity identity
// (§5.4). Written here rather than pulled in: the dependency budget is three
// libraries, and a ULID is a hundred lines.
//
// The ids are monotonic. §8.2 makes the event trail a stream that `--since
// <id>` resumes from, and §5.5 has the store order that trail by id — so two
// events written in the same millisecond must still compare in the order they
// were written. Timestamp plus randomness does not give that: within one
// millisecond the ordering is a coin toss, and a claim can precede the create
// it followed. So a mint inside the same millisecond increments the previous
// id's random component instead of drawing a fresh one.
package ids

import (
	"crypto/rand"
	"encoding/binary"
	"sync"
)

// alphabet is Crockford base32: no I, L, O or U, so an id read aloud or typed
// from a terminal does not turn into a different id.
const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// mono is the monotonic factory's state. One daemon is the only writer (§2.2),
// so one process-wide factory covers every id this plugin issues.
var mono struct {
	sync.Mutex
	last  [16]byte
	valid bool
}

// New builds a ULID for the given Unix-millisecond timestamp: 48 bits of time
// then 80 bits of randomness, rendered as 26 Crockford characters.
//
// Inside a millisecond already used — or behind one, if the host clock steps
// backwards — the previous id's random component is incremented rather than
// redrawn, so the result is always greater than the last id issued.
func New(nowMS int64) string {
	mono.Lock()
	defer mono.Unlock()
	if !mono.valid || nowMS > lastMS() {
		mono.last = fresh(nowMS)
		mono.valid = true
		return encode(mono.last)
	}
	// Same millisecond, or an earlier one. Keep the last id's timestamp — a
	// clock that went backwards must not be able to mint an id that sorts
	// before one already handed out — and step the random component.
	if !increment(mono.last[6:]) {
		// All 80 bits were set, which will not happen in this decade: borrow a
		// millisecond and start over rather than wrap into a smaller id.
		mono.last = fresh(lastMS() + 1)
	}
	return encode(mono.last)
}

// fresh draws a new id: the timestamp in the leading 48 bits, randomness in
// the trailing 80.
func fresh(ms int64) [16]byte {
	var raw [16]byte
	binary.BigEndian.PutUint64(raw[:8], uint64(ms)<<16)
	if _, err := rand.Read(raw[6:]); err != nil {
		// crypto/rand does not fail on any platform this ships to; if it ever
		// does, an id we cannot make unique is not something to paper over.
		panic("ids: crypto/rand: " + err.Error())
	}
	return raw
}

// lastMS reads the timestamp back out of the last id issued.
func lastMS() int64 {
	return int64(binary.BigEndian.Uint64(mono.last[:8]) >> 16)
}

// increment adds one to a big-endian byte string, reporting false on overflow.
func increment(b []byte) bool {
	for i := len(b) - 1; i >= 0; i-- {
		b[i]++
		if b[i] != 0 {
			return true
		}
	}
	return false
}

// encode renders the 128 bits as 26 Crockford base32 characters, RIGHT-aligned:
// 26 characters hold 130 bits, so the leading two are always zero and the first
// character carries three significant bits — which is why a ULID's leading
// digit never exceeds 7.
//
// This used to align them the other way, five bits at a time from the top, and
// its comment said "two significant bits" while claiming a ceiling of 7, which
// needs three. Internally that was harmless — a constant shift preserves
// ordering — but it is not the format §5.4 names, and a decoder that did not
// know about this implementation read the timestamp as a date two centuries
// out. The divergence and the migration off it are in docs/contract-notes.md.
func encode(raw [16]byte) string {
	out := make([]byte, 26)
	for i := range out {
		// Character 25 holds the lowest five bits, 24 the next five, and so on.
		out[i] = alphabet[fiveBitsAt(raw, (25-i)*5)]
	}
	return string(out)
}

// fiveBitsAt reads five bits starting at `lo` counted from the LEAST
// significant end of the 128-bit value. Bits past the top read as zero, which
// is what makes the leading character three bits wide rather than five.
func fiveBitsAt(raw [16]byte, lo int) byte {
	// Big-endian: byte 15 holds the lowest eight bits.
	at, shift := 15-lo/8, lo%8
	v := uint16(raw[at]) >> shift
	if shift > 3 && at > 0 {
		v |= uint16(raw[at-1]) << (8 - shift)
	}
	return byte(v & 0x1f)
}

// Valid reports whether s has the shape of a stored id: 26 Crockford
// characters. It is the guard a door uses before hitting the store with
// something a caller typed.
func Valid(s string) bool {
	if len(s) != 26 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !inAlphabet(s[i]) {
			return false
		}
	}
	return true
}

func inAlphabet(c byte) bool {
	for i := 0; i < len(alphabet); i++ {
		if alphabet[i] == c {
			return true
		}
	}
	return false
}

// Reencode converts an id minted by the old LEFT-aligned rendering into the
// spec's right-aligned one. The two differ by a constant two-bit shift, so the
// conversion is exact and total: read the 130 bits the old string spelled,
// shift the padding off the wrong end, and render them again.
//
// It reports false for anything that is not a 26-character Crockford string,
// and for an old string whose low two bits are set — which the old encoder
// could never produce, since it left them as padding. That is the one check
// available for "this has already been migrated", and it is why the migration
// leans on the schema version for idempotence rather than on the ids.
func Reencode(old string) (string, bool) {
	if !Valid(old) {
		return "", false
	}
	hi, lo := uint64(0), uint64(0)
	for i := 0; i < len(old); i++ {
		v := uint64(index(old[i]))
		hi = hi<<5 | lo>>59
		lo = lo<<5 | v
	}
	if lo&0x3 != 0 {
		// The old encoder put the 128 bits at the TOP of the 130, so the
		// bottom two were always zero. Anything else was not minted by it.
		return "", false
	}
	// Drop the two padding bits off the bottom to get the real 128.
	lo = lo>>2 | hi<<62
	hi >>= 2
	var raw [16]byte
	for i := 7; i >= 0; i-- {
		raw[i] = byte(hi)
		hi >>= 8
	}
	for i := 15; i >= 8; i-- {
		raw[i] = byte(lo)
		lo >>= 8
	}
	return encode(raw), true
}

func index(c byte) int {
	for i := 0; i < len(alphabet); i++ {
		if alphabet[i] == c {
			return i
		}
	}
	return -1
}
