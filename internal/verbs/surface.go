// The caller surface digest. It lives in its own file because the release
// guard COPIES this file, verbatim, on top of the verbs package as it stood at
// the last release tag, and runs it there: the pin has to be the same function
// over old data, not a different function over a remembered number. Nothing
// else belongs in here.
package verbs

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// CallerSurface is everything a caller of either door can be BROKEN by,
// hashed. It is deliberately a second digest rather than a widening of
// Fingerprint: Fingerprint is shipped in `doctor --json` and semver-bound
// (§13.3), so a consumer pins it and it cannot grow new inputs. This one is
// internal, and its only reader is the release guard that asks whether the
// surface moved since the last release without a changelog entry.
//
// What it hashes that Fingerprint does not is the CLI subcommand path and the
// MCP tool name — the two strings a caller literally types or wires in. A
// release that renamed every task verb's CLI path moved neither the verb
// names, the gate names, nor any argument, so the shipped fingerprint sat
// still through a breaking change.
func CallerSurface() string { return CallerSurfaceOf(All) }

// CallerSurfaceOf is CallerSurface over an arbitrary table, so a test can
// prove that a given move changes the answer.
func CallerSurfaceOf(list []Verb) string {
	var b strings.Builder
	for _, v := range list {
		b.WriteString(v.Name)
		b.WriteString("\x00")
		b.WriteString(strings.Join(v.CLI, " "))
		b.WriteString("\x00")
		b.WriteString(v.MCP)
		b.WriteString("\x00")
		b.WriteString(v.Gated)
		// Both of these decide whether a call is ANSWERED or refused with
		// USAGE (§4.4, §5.6), which a caller feels exactly as hard as a
		// renamed flag.
		b.WriteString("\x00")
		b.WriteString(boolField(v.AllProjects))
		b.WriteString(boolField(v.Mutates))
		for _, a := range v.Args {
			b.WriteString("\x00")
			b.WriteString(a.Name)
			b.WriteString(":")
			b.WriteString(a.Type)
			b.WriteString(":")
			b.WriteString(boolField(a.Required))
			b.WriteString(boolField(a.Positional))
		}
		b.WriteString("\n")
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:8])
}

func boolField(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
