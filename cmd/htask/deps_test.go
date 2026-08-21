package main_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// devSection is README's Development section: everything from its heading to
// the next one. The dependency budget is stated there, so that is where a
// reader looks and where the check has to hold.
var devSection = regexp.MustCompile(`(?s)\n## Development\n(.*?)\n## `)

// directRequire matches one line of go.mod's direct require block. An indirect
// line carries the `// indirect` marker and is not a dependency this
// repository chose.
var directRequire = regexp.MustCompile(`(?m)^\t([^\s]+) v[^\s]+$`)

// The budget is a non-negotiable in CLAUDE.md, and README is where it is
// declared to a reader. It said three while go.mod required five, and stayed
// wrong through two dependency additions because nothing read the two
// together. This reads go.mod towards README, which is the direction that
// catches an addition nobody wrote down.
func TestDependenciesAreDeclaredInTheReadme(t *testing.T) {
	mod, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	m := devSection.FindSubmatch(readme)
	if m == nil {
		t.Fatal("README has no Development section; the dependency budget is declared there")
	}
	dev := string(m[1])

	direct := directModules(string(mod))
	if len(direct) == 0 {
		t.Fatal("no direct requires read from go.mod; the pattern is reading nothing")
	}
	missing := 0
	for _, path := range direct {
		if !strings.Contains(dev, path) {
			missing++
			t.Errorf("go.mod requires %s and README's Development section does not name it", path)
		}
	}
	t.Logf("%d direct dependencies, %d unnamed in README: %s", len(direct), missing, strings.Join(direct, ", "))
}

// directModules is go.mod's direct requires: the first require block, whose
// lines carry no `// indirect` marker.
func directModules(mod string) []string {
	out := []string{}
	inBlock := false
	for _, line := range strings.Split(mod, "\n") {
		switch {
		case strings.HasPrefix(line, "require ("):
			inBlock = true
			continue
		case inBlock && line == ")":
			inBlock = false
			continue
		}
		if !inBlock || strings.Contains(line, "// indirect") {
			continue
		}
		if m := directRequire.FindStringSubmatch(line + "\n"); m != nil {
			out = append(out, m[1])
		}
	}
	return out
}
