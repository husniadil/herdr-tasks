package main_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/husniadil/herdr-tasks/internal/daemon"
)

// contractFile is the vendored contract, which is what a citation resolves
// against. It is in the repository so that a reader who has only this
// repository can follow every § the code cites.
const contractFile = "docs/contract.md"

// citation matches "§6.1" and bare "§6", the two forms the repository writes.
var citation = regexp.MustCompile(`§[0-9]+(?:\.[0-9]+)?`)

// contractAnchor matches what the contract DEFINES: a section heading
// ("## §6 Verbs, CLI, and error envelope") and a numbered clause at the start
// of its own line ("§6.1 Every verb ...").
var contractAnchor = regexp.MustCompile(`(?m)^(?:#+ )?(§[0-9]+(?:\.[0-9]+)?)`)

// §13.4 and the reason docs_test exists: a citation nobody can resolve is a
// citation nobody checks. docs_test learned this one direction at a time —
// it read from the docs towards the registry and let an undocumented flag
// ship — so this reads every tracked file towards the contract, whatever its
// extension. Citations are in .go, .md, .sh, .toml and the Makefile alike.
func TestContractCitationsResolve(t *testing.T) {
	defined := contractAnchors(t)
	if len(defined) < 50 {
		t.Fatalf("%s defines %d anchors; the contract is not being read", contractFile, len(defined))
	}
	files := trackedFiles(t)
	if len(files) < 20 {
		t.Fatalf("scanned %d tracked files; the file list is reading nothing", len(files))
	}

	cited := map[string][]string{}
	for _, name := range files {
		if name == contractFile {
			continue
		}
		body, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, c := range citation.FindAllString(string(body), -1) {
			if !contains(cited[c], name) {
				cited[c] = append(cited[c], name)
			}
		}
	}
	if len(cited) == 0 {
		t.Fatal("no citations found in any tracked file; the pattern is reading nothing")
	}

	unresolved := []string{}
	for c, where := range cited {
		if !defined[c] {
			unresolved = append(unresolved, fmt.Sprintf("%s (cited in %s)", c, strings.Join(where, ", ")))
		}
	}
	sort.Strings(unresolved)
	for _, u := range unresolved {
		t.Errorf("%s resolves to nothing in %s", u, contractFile)
	}
	// Logged, not just floored: after a contract revision this number is the
	// difference between a citation set that shrank and one that stopped
	// being read.
	t.Logf("%d distinct citations across %d tracked files, %d unresolved, against %d anchors in %s",
		len(cited), len(files), len(unresolved), len(defined), contractFile)
}

func contractAnchors(t *testing.T) map[string]bool {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", contractFile))
	if err != nil {
		t.Fatalf("read %s: %v", contractFile, err)
	}
	out := map[string]bool{}
	for _, m := range contractAnchor.FindAllStringSubmatch(string(body), -1) {
		out[m[1]] = true
	}
	return out
}

// trackedFiles is every tracked file. Tracked, because an untracked scratch
// file is not something this repository ships.
func trackedFiles(t *testing.T) []string {
	t.Helper()
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = filepath.Join("..", "..")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	names := []string{}
	for _, b := range bytes.Split(out, []byte{0}) {
		if name := string(b); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func contains(all []string, s string) bool {
	for _, v := range all {
		if v == s {
			return true
		}
	}
	return false
}

// anyRevision matches a semver-with-tag token, so a page naming a revision
// other than the declared one is caught.
var anyRevision = regexp.MustCompile(`[0-9]+\.[0-9]+\.[0-9]+-draft`)

// contractVersion matches the revision the vendored contract states for itself.
var contractVersion = regexp.MustCompile(`(?m)^Status:.*\bVersion: ([0-9][^.\s]*(?:\.[^.\s]*)*?)\. `)

// §13.4: the declared revision is a conformance claim, and it drifted two
// revisions behind the contract without anything noticing, because the
// constant and the document were never read together. This reads them
// together. Bumping the constant without checking the deltas is still
// possible — a test cannot verify conformance — but declaring a revision the
// vendored text does not carry is not.
func TestTheDeclaredRevisionIsTheVendoredOne(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", contractFile))
	if err != nil {
		t.Fatalf("read %s: %v", contractFile, err)
	}
	m := contractVersion.FindSubmatch(body)
	if m == nil {
		t.Fatalf("%s states no Version on its Status line", contractFile)
	}
	stated := string(m[1])
	if stated != daemon.ContractVersion {
		t.Errorf("%s is revision %s and this binary declares %s; doctor, version and README "+
			"all read that constant, so they are claiming conformance to a text that is not here",
			contractFile, stated, daemon.ContractVersion)
	}
	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	// The declaration itself, not merely the string somewhere on the page: a
	// sentence ABOUT another revision contains the same token, and the first
	// version of this check passed on exactly that.
	declared := "conforms to the **shared plugin contract**, revision " + daemon.ContractVersion
	if !strings.Contains(string(readme), declared) {
		t.Errorf("README does not declare %q, which §13.4 requires it to state "+
			"alongside doctor output", declared)
	}
	// And no second revision is named anywhere, because a page that declares
	// one revision and mentions another is the drift this task closed.
	for _, other := range anyRevision.FindAllString(string(readme), -1) {
		if other != daemon.ContractVersion {
			t.Errorf("README names revision %s as well as the declared %s", other, daemon.ContractVersion)
		}
	}
}
