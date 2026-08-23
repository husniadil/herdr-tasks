package main_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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

// anyRevision matches a named revision, tagged or not, so a page naming a
// revision other than the declared one is caught. It reads "revision <token>"
// rather than a bare semver because the same page states the binary version,
// which is a different number and moves on its own.
var anyRevision = regexp.MustCompile(`revision ([0-9]+\.[0-9]+\.[0-9]+(?:-[a-z]+)?)`)

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
	// The declaration may LAG the vendored document, and only that way: an
	// amendment lands in the text before any plugin has been brought to it,
	// and a plugin that declared the new revision on the day it was written
	// would be claiming conformance it has not done the work for. Every
	// other shape is the drift this test was written for, so both halves of
	// that sentence are enforced rather than described. Declaring a revision
	// HIGHER than the vendored one is conformance to a text that is not in
	// this repository at all, which is worse than the lag it looks like.
	// And the gap must be a RECORDED one: the two revisions named together
	// in a single entry of docs/contract-notes.md, not merely both present
	// somewhere in a file that already names six of them.
	if stated != daemon.ContractVersion {
		if !revisionLower(daemon.ContractVersion, stated) {
			t.Fatalf("this binary declares revision %s against a contract vendored at %s; a "+
				"declaration may only lag the document, never lead it, and %s is not in this "+
				"repository for anyone to read", daemon.ContractVersion, stated, daemon.ContractVersion)
		}
		if !gapRecorded(t, daemon.ContractVersion, stated) {
			t.Fatalf("this binary declares revision %s against a contract vendored at %s and "+
				"docs/contract-notes.md has no single entry naming both; a lag nobody wrote "+
				"down is the silent drift this test exists to catch",
				daemon.ContractVersion, stated)
		}
		t.Logf("declared revision %s lags the vendored %s; docs/contract-notes.md records the gap",
			daemon.ContractVersion, stated)
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
	for _, m := range anyRevision.FindAllStringSubmatch(string(readme), -1) {
		if other := m[1]; other != daemon.ContractVersion {
			t.Errorf("README names revision %s as well as the declared %s", other, daemon.ContractVersion)
		}
	}
}

// revisionNumbers turns "0.7.0" or "0.4.0-draft" into its numeric fields. A
// pre-release suffix orders BELOW the release it leads to, which is the order
// this repository's own history used when 0.4.0-draft became 0.4.0.
func revisionNumbers(v string) ([]int, bool, bool) {
	pre := false
	if i := strings.IndexByte(v, '-'); i >= 0 {
		v, pre = v[:i], true
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return nil, false, false
	}
	out := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, false, false
		}
		out[i] = n
	}
	return out, pre, true
}

// revisionLower reports whether a is strictly below b.
func revisionLower(a, b string) bool {
	an, apre, aok := revisionNumbers(a)
	bn, bpre, bok := revisionNumbers(b)
	if !aok || !bok {
		return false
	}
	for i := range an {
		if an[i] != bn[i] {
			return an[i] < bn[i]
		}
	}
	// Same numbers: a pre-release is below the release, nothing else is.
	return apre && !bpre
}

// gapRecorded reports whether docs/contract-notes.md names both revisions in
// ONE entry. An entry is a paragraph — the unit a reader takes in as a single
// statement — because "both strings appear in the file" is satisfied by a
// changelog that has accumulated every revision the project ever had.
func gapRecorded(t *testing.T, declared, vendored string) bool {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "docs", "contract-notes.md"))
	if err != nil {
		t.Fatalf("read docs/contract-notes.md: %v", err)
	}
	for _, para := range strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n\n") {
		if strings.Contains(para, declared) && strings.Contains(para, vendored) {
			return true
		}
	}
	return false
}

// contractSection returns one section of the vendored contract with its
// whitespace collapsed: from the anchor that opens it to the next anchor at
// the same or a higher level. Collapsed, because the document is hard-wrapped
// at around 76 columns and a sentence this file wants to read straddles two
// or three lines wherever an edit last left it.
func contractSection(t *testing.T, anchor string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", contractFile))
	if err != nil {
		t.Fatalf("read %s: %v", contractFile, err)
	}
	lines := strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n")
	start := -1
	for i, line := range lines {
		if m := contractAnchor.FindStringSubmatch(line); m != nil && m[1] == anchor {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("%s defines no %s", contractFile, anchor)
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if contractAnchor.MatchString(lines[i]) {
			end = i
			break
		}
	}
	return strings.Join(strings.Fields(strings.Join(lines[start:end], " ")), " ")
}

// §3.2 gained a NAMED rule, and the name is the whole point of it: a
// consequence with a name is one the next person can be pointed AT before
// they design a transport or a tool list around it, rather than one they
// rediscover afterwards. This pins the name and the three clauses that carry
// it, because prose that names a rule and then does not state it reads as a
// rule and holds nothing.
func TestSection32NamesTheProcessBoundIdentityRule(t *testing.T) {
	body := contractSection(t, "§3.2")
	const rule = "The process-bound identity rule"
	if !strings.Contains(body, rule) {
		t.Fatalf("§3.2 does not name %q; a consequence nobody can cite by name is one "+
			"the next transport design meets after it is written, not before", rule)
	}
	for _, phrase := range []string{
		"fixed when the door process starts",
		"cannot be learned from a call",
		"one process per call",
		"outlives every call it serves",
	} {
		if !strings.Contains(body, phrase) {
			t.Errorf("§3.2 names the rule but does not state it: %q is missing", phrase)
		}
	}
}

// §7.3's parity MUST and its `--as` exclusion are one argument or they are
// two, and 0.7.0 shipped them as two: the MUST put every verb on the door
// while the exclusion said a shell-less caller must not gain authority it has
// no other way to reach — which is what the operator verbs the MUST added
// were. This fails if a later edit keeps either half without the rule they
// now share, or without the other half.
func TestParityAndTheAsExclusionRestOnTheSameArgument(t *testing.T) {
	body := contractSection(t, "§7.3")
	parity := strings.Contains(body, "MUST serve every verb its CLI serves")
	as := strings.Contains(body, "`--as` (§3.2) stays CLI-only")
	rule := strings.Contains(body, "process-bound identity rule")
	if parity != as {
		t.Fatalf("§7.3 states the parity MUST (%v) and the `--as` exclusion (%v) "+
			"separately; they answer the same question and neither stands without the other",
			parity, as)
	}
	if (parity || as) && !rule {
		t.Fatalf("§7.3 states the parity MUST and the `--as` exclusion without resting " +
			"either on the process-bound identity rule (§3.2); that is the shape 0.7.0 " +
			"shipped, where the exclusion's reason also condemned the MUST")
	}
	// And the argument itself, not merely its name: every verb is safe on the
	// door BECAUSE the door's principal is settled before a call arrives, and
	// `--as` is excluded BECAUSE it is the one identity claim a call carries.
	for _, phrase := range []string{
		"fixed before any call arrives",
		"identity claim carried BY a call",
	} {
		if !strings.Contains(body, phrase) {
			t.Errorf("§7.3 cites the rule but does not apply it: %q is missing", phrase)
		}
	}
}
