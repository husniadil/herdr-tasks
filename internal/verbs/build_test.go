package verbs

import (
	"strings"
	"testing"
)

// §13.3: the surface fingerprint answers "would this daemon drop part of my
// request". It cannot answer "is this daemon running my code" — a change to a
// bound or a rule keeps every argument and every gate name, so the hash is
// identical. That is what the build identity is for, and these are the cases
// it has to get right.
func TestBuildIdentityTellsTwoBinariesApart(t *testing.T) {
	// A test binary has no vcs stamping at all (measured: only -buildmode and
	// -compiler are set, Main.Version is "(devel)"), so the identity has to
	// stand on the executable alone or it would be empty exactly where the
	// tests run.
	mine := ThisBuild()
	if mine.Stamp == "" {
		t.Fatal("this build has no stamp; the identity is empty in a test binary")
	}
	if mine.Short() == "" {
		t.Fatal("this build has no short form to print in a warning")
	}
	if !mine.Same(mine) {
		t.Fatal("a build is not the same as itself")
	}

	const rev = "cca71ef610aa"
	for name, tc := range map[string]struct {
		a, b Build
		same bool
	}{
		"same file, same stamp": {
			Build{Exe: "/p/bin/ht", Stamp: "10-1", Revision: rev},
			Build{Exe: "/p/bin/ht", Stamp: "10-1", Revision: rev}, true},
		// The development loop: rebuild over the same path, daemon still old.
		// The revision cannot see this — the commit did not move — so the file
		// is what has to answer.
		"same file, rebuilt": {
			Build{Exe: "/p/bin/ht", Stamp: "10-1", Revision: rev + "+dirty"},
			Build{Exe: "/p/bin/ht", Stamp: "11-2", Revision: rev + "+dirty"}, false},
		// `make install` is `go install`, so the door can be a different file
		// from the daemon's ./bin/ht with the same code in it. Stat identity
		// would cry wolf here; the revision is what survives the move.
		"different files, one commit": {
			Build{Exe: "/home/u/go/bin/ht", Stamp: "10-1", Revision: rev},
			Build{Exe: "/p/bin/ht", Stamp: "99-9", Revision: rev}, true},
		"different files, different commits": {
			Build{Exe: "/home/u/go/bin/ht", Stamp: "10-1", Revision: rev},
			Build{Exe: "/p/bin/ht", Stamp: "10-1", Revision: "8862a01fdeef"}, false},
		// Two working-tree builds at different paths share a revision and a
		// dirty bit and are still two different binaries. Unknowable is not
		// the same as equal, and this plugin says so rather than assuming.
		"different files, both dirty": {
			Build{Exe: "/home/u/go/bin/ht", Stamp: "10-1", Revision: rev + "+dirty"},
			Build{Exe: "/p/bin/ht", Stamp: "10-1", Revision: rev + "+dirty"}, false},
		// A daemon old enough to predate the field says nothing, which is the
		// same retroactive rule the fingerprint already uses.
		"nothing to compare": {
			Build{Exe: "/p/bin/ht", Stamp: "10-1", Revision: rev}, Build{}, false},
		"neither knows anything": {Build{}, Build{}, false},
	} {
		if got := tc.a.Same(tc.b); got != tc.same {
			t.Errorf("%s: Same = %v, want %v", name, got, tc.same)
		}
		if got := tc.b.Same(tc.a); got != tc.same {
			t.Errorf("%s, reversed: Same = %v, want %v", name, got, tc.same)
		}
	}
}

// A warning that prints a 40-character hash and an absolute path twice is a
// warning nobody reads.
func TestBuildIdentityPrintsShort(t *testing.T) {
	b := Build{Exe: "/p/bin/ht", Stamp: "10-1", Revision: "cca71ef610aa6e882922a5d6b40ac7b70c180e4b"}
	if got := b.Short(); len(got) > 30 || got == "" {
		t.Fatalf("Short() = %q", got)
	}
	if !strings.HasPrefix(b.Short(), "cca71ef610aa") {
		t.Fatalf("Short() does not lead with the commit a human can act on: %q", b.Short())
	}
	// The case the warning exists for: two working-tree builds of one commit.
	// If they print the same, the message names them both and says nothing.
	rebuilt := Build{Exe: "/p/bin/ht", Stamp: "11-2", Revision: b.Revision + "+dirty"}
	stale := Build{Exe: "/p/bin/ht", Stamp: "10-1", Revision: b.Revision + "+dirty"}
	if rebuilt.Short() == stale.Short() {
		t.Fatalf("two different builds of one commit both print %q", stale.Short())
	}
	dirty := Build{Exe: "/p/bin/ht", Stamp: "10-1", Revision: "cca71ef610aa6e882922a5d6b40ac7b70c180e4b+dirty"}
	if dirty.Short() == b.Short() {
		t.Fatal("a working-tree build prints the same as the commit it came from")
	}
	// With no vcs stamping the file is all there is, and it still has to name
	// itself.
	if (Build{Exe: "/p/bin/ht", Stamp: "10-1"}).Short() == "" {
		t.Fatal("a build with no revision prints nothing")
	}
	if (Build{}).Short() != "" {
		t.Fatal("a build that knows nothing about itself claims a name")
	}
}
