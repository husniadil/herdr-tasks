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

// §3.7's two halves are one rule or they are none. 0.8.0 said `human` is
// never the fallback for knowing nothing; 0.10.0 stops an operator verb
// refusing a non-operator principal, which leaves the trail as the only place
// the operator's authority is visible at all — so the first half is what makes
// the second meaningful, and dropping either leaves a plugin that either
// refuses what it was told not to refuse, or performs it and files it under
// the operator. The behaviour is pinned in internal/tasks and internal/daemon;
// this pins that the document still asks for it, because a MUST nobody can
// find is a MUST nobody applies.
func TestTheOperatorVerbRuleKeepsBothOfItsHalves(t *testing.T) {
	// NOT contractSection: its anchor regex matches the first line starting
	// with `§3.7`, and that is the 0.8.0 preamble entry, which summarises the
	// clause rather than stating it. This reads the numbered §3, where the
	// clause itself lives.
	body := contractHeading(t, "## §3 ", "## §4 ")
	for _, phrase := range []string{
		// The half 0.8.0 wrote, unchanged.
		"`human` is never the fallback for knowing nothing",
		// The refusal 0.10.0 removed, and the duty that replaces it.
		"MUST NOT refuse an operator verb on the ground that the caller is not the operator",
		"an agent confirms with the user",
		// Why there is no mechanism behind the duty.
		"does not verify that the confirmation happened",
		// The trail that carries the accountability instead, both halves.
		"MUST record the calling principal as the event's actor — never `human`",
		"MUST mark the event as an operator verb performed by someone other than",
		// And the 0.9.0 preamble's rule applied to this clause's own MUSTs.
		"MUST pin both halves with a test",
		// The boundary: what does NOT become advisory with it.
		"does not become advisory with it",
	} {
		if !strings.Contains(body, phrase) {
			t.Errorf("§3.7 no longer states the operator-verb rule: %q is missing", phrase)
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
		// 0.10.0: the one reason a plugin ever gave for a CLI-only verb was
		// "this authority is the operator's", and §3.7 no longer supports it.
		// Without this sentence the MUST reads as one a drafter may carve an
		// operator-verb exception out of, which is the shape it already had.
		"There is no operator-verb exception to this MUST",
		"MUST pin the totality with a test",
	} {
		if !strings.Contains(body, phrase) {
			t.Errorf("§7.3 cites the rule but does not apply it: %q is missing", phrase)
		}
	}
}

// §9 with §3.7 (0.10.0): withholding a verb from a principal is the gate's
// job. Two things had to move with the amendment and both are MUSTs, so both
// are pinned here: a parked action must name who resolved it, because §9.3
// re-runs the verb under the ORIGINAL subject and would otherwise leave the
// decider out of the only record there is; and §3.7 must not be read as a
// second place to withhold a verb now that it withholds nothing.
func TestTheGateIsTheOnlyPlaceAVerbIsWithheld(t *testing.T) {
	body := contractHeading(t, "## §9 ", "## §10 ")
	for _, phrase := range []string{
		"the parked record MUST also carry WHO resolved it",
		"an agent confirms with the user and resolves",
		"A door MUST NOT withhold one by not carrying it",
		"neither is an operator-verb refusal a way to withhold one",
	} {
		if !strings.Contains(body, phrase) {
			t.Errorf("§9 no longer states where withholding lives: %q is missing", phrase)
		}
	}
}

// contractHeading returns the vendored contract between two `## ` headings,
// flattened the way contractSection flattens a section. It exists because the
// section anchors also appear in the preamble's change entries, so a clause
// whose number is named up front cannot be addressed by its anchor alone.
func contractHeading(t *testing.T, from, to string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", contractFile))
	if err != nil {
		t.Fatalf("read %s: %v", contractFile, err)
	}
	text := strings.ReplaceAll(string(body), "\r\n", "\n")
	i := strings.Index(text, "\n"+from)
	if i < 0 {
		t.Fatalf("%s has no %q heading", contractFile, from)
	}
	j := strings.Index(text[i+1:], "\n"+to)
	if j < 0 {
		t.Fatalf("%s has no %q heading after %q", contractFile, to, from)
	}
	return strings.Join(strings.Fields(text[i:i+1+j]), " ")
}

// contractParagraph returns the blank-line-delimited paragraph containing
// needle, flattened the way contractSection flattens a section, together with
// the byte offset the paragraph starts at. The offset is what lets a test ask
// WHERE in the document a sentence lives, which for a rule aimed at a drafter
// is half of what the rule is.
func contractParagraph(t *testing.T, needle string) (string, int) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", contractFile))
	if err != nil {
		t.Fatalf("read %s: %v", contractFile, err)
	}
	body := strings.ReplaceAll(string(raw), "\r\n", "\n")
	at := 0
	for _, para := range strings.SplitAfter(body, "\n\n") {
		if strings.Contains(strings.Join(strings.Fields(para), " "), needle) {
			return strings.Join(strings.Fields(para), " "), at
		}
		at += len(para)
	}
	t.Fatalf("%s has no paragraph containing %q", contractFile, needle)
	return "", 0
}

// The finding this pins is that three tasks in a row shipped a documented
// refusal with nothing that fails when the refusal goes: the MECHANISM got a
// test because a criterion asked for it, and the GUARD got prose. A guard is
// exactly the code nobody exercises in normal operation, so no other test
// catches it either. The contract now says once, generally, what §7.5 had
// said about one clause. This pins the sentence AND its position: a rule a
// drafter meets after the sections it governs is an appendix, and the whole
// point of this one is that it is read before a MUST is written.
func TestAMustIsNotSatisfiedUntilATestFailsWithoutIt(t *testing.T) {
	const def = "A MUST is a conformance requirement"
	para, at := contractParagraph(t, def)

	raw, err := os.ReadFile(filepath.Join("..", "..", contractFile))
	if err != nil {
		t.Fatalf("read %s: %v", contractFile, err)
	}
	first := bytes.Index(raw, []byte("\n## §1 "))
	if first < 0 {
		t.Fatalf("%s has no §1 heading; the document is not being read", contractFile)
	}
	if at > first {
		t.Fatalf("the paragraph defining MUST sits at byte %d, after §1 at byte %d; "+
			"a rule about how a MUST is satisfied belongs where a drafter meets it, "+
			"not behind the sections it governs", at, first)
	}

	for _, phrase := range []string{
		"is not satisfied by code that behaves correctly today",
		"a test that FAILS when the behaviour is removed",
		"binds a refusal and a guard exactly as it binds a mechanism",
		"cannot name the test that fails without a MUST",
	} {
		if !strings.Contains(para, phrase) {
			t.Errorf("the paragraph defining MUST does not state the evidence rule: %q is missing", phrase)
		}
	}
}
