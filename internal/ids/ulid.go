// Package ids generates the ULIDs the contract fixes as entity identity
// (§5.4). Written here rather than pulled in: the dependency budget is three
// libraries, and a ULID is 40 lines.
package ids

import (
	"crypto/rand"
	"encoding/binary"
)

// alphabet is Crockford base32: no I, L, O or U, so an id read aloud or typed
// from a terminal does not turn into a different id.
const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// New builds a ULID for the given Unix-millisecond timestamp: 48 bits of time
// then 80 bits of randomness, rendered as 26 Crockford characters.
func New(nowMS int64) string {
	var raw [16]byte
	binary.BigEndian.PutUint64(raw[:8], uint64(nowMS)<<16)
	if _, err := rand.Read(raw[6:]); err != nil {
		// crypto/rand does not fail on any platform this ships to; if it ever
		// does, an id we cannot make unique is not something to paper over.
		panic("ids: crypto/rand: " + err.Error())
	}
	return encode(raw)
}

// encode renders 128 bits as 26 base32 characters, five bits at a time from
// the top. The first character carries only two significant bits, which is why
// a ULID's leading digit never exceeds 7.
func encode(raw [16]byte) string {
	out := make([]byte, 26)
	for i := 0; i < 26; i++ {
		bit := i * 5
		var acc uint16
		acc = uint16(raw[bit/8]) << 8
		if bit/8+1 < 16 {
			acc |= uint16(raw[bit/8+1])
		}
		out[i] = alphabet[(acc>>(11-uint(bit%8)))&0x1f]
	}
	return string(out)
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
