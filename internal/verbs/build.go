package verbs

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
)

// Build identifies the binary a process is running (§13.3). Fingerprint says
// what the door SPEAKS; this says what it IS, and the two answer different
// questions: a change to a length bound or a state rule keeps every argument
// and every gate name, so the fingerprint is identical while the behaviour is
// not. That silence is what let a stale daemon go on accepting a
// 100,000-character title after the bounds shipped.
//
// It is not one value but three, because no single one is enough:
//
//   - Exe and Stamp are the running file and what it looked like when this
//     process started. Two builds over the same path differ here even when
//     they are the same commit with uncommitted changes — the development
//     loop, which a revision cannot see.
//   - Revision is the commit, with a `+dirty` marker for a working-tree
//     build. It survives the door and the daemon being different FILES, which
//     is an ordinary layout: `make install` is `go install`, while the
//     manifest's daemon runs ./bin/htask.
type Build struct {
	// Exe is the absolute path of the running binary.
	Exe string `json:"exe,omitempty"`
	// Stamp is "<size>-<modtime ms>" of that file, read when the process
	// started rather than when it is asked: a daemon must report the file it
	// IS, not whatever now sits at its path.
	Stamp string `json:"stamp,omitempty"`
	// Revision is the vcs revision, plus "+dirty" for a working-tree build.
	// Empty where Go does not stamp it, which includes every test binary.
	Revision string `json:"revision,omitempty"`
}

// thisBuild is computed at process start, not on demand, so a daemon whose
// binary is rebuilt underneath it goes on reporting the file it is actually
// running.
var thisBuild = readBuild()

// ThisBuild is the identity of the running process.
func ThisBuild() Build { return thisBuild }

func readBuild() Build {
	var b Build
	if exe, err := os.Executable(); err == nil {
		b.Exe = exe
		if fi, err := os.Stat(exe); err == nil {
			b.Stamp = fmt.Sprintf("%d-%d", fi.Size(), fi.ModTime().UnixMilli())
		}
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return b
	}
	var rev string
	var dirty bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev != "" {
		b.Revision = rev
		if dirty {
			b.Revision += "+dirty"
		}
	}
	return b
}

// Same reports whether two builds are the same binary. It compares on the
// path: the same file answers with its stat, which is exact; two different
// files answer with the commit, which is all that is portable between them.
//
// Anything it cannot decide is NOT the same. A build with nothing to say
// about itself, a daemon too old to carry the field, and two working-tree
// builds at different paths all fall here — unknowable and identical are
// different answers, and only one of them is safe to give.
func (b Build) Same(o Build) bool {
	if b.Exe != "" && b.Exe == o.Exe {
		return b.Stamp != "" && b.Stamp == o.Stamp
	}
	if b.Revision == "" || b.Revision != o.Revision {
		return false
	}
	return !strings.HasSuffix(b.Revision, "+dirty")
}

// Short is the identity as a warning prints it. The commit is the half a
// human can act on, and the digest of the file is the half that is always
// different when the builds are: two working-tree builds of the same commit
// share a revision, and a warning that named them both "abc123+dirty" would
// read like a bug in the warning rather than a difference in the binaries.
func (b Build) Short() string {
	digest := ""
	if b.Stamp != "" {
		sum := sha256.Sum256([]byte(b.Exe + "\x00" + b.Stamp))
		digest = "@" + hex.EncodeToString(sum[:3])
	}
	if b.Revision == "" {
		return strings.TrimPrefix(digest, "@")
	}
	rev, dirty, _ := strings.Cut(b.Revision, "+")
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if dirty != "" {
		rev += "+" + dirty
	}
	return rev + digest
}
