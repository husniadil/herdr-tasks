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
