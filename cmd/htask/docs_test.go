package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/husniadil/herdr-tasks/internal/daemon"
	"github.com/husniadil/herdr-tasks/internal/verbs"
)

// The docs are read by people and by agents, and both of them then type what
// they read. A verb or a flag that only ever existed in a paragraph is worse
// than an undocumented one: it fails at the terminal, after the reader trusted
// it. So the docs are checked against the registry both doors are generated
// from (§6.1) rather than against anyone's memory of it.

// globalFlags are not in any verb's Args because every verb has them.
var globalFlags = map[string]bool{
	"json": true, "project": true, "all-projects": true, "as": true,
	"base-updated-at": true, "follow": true, "help": true,
}

// notInTheRegistry are the CLI's own commands: they run in the door rather
// than travelling to the daemon as a verb, so verbs.All does not know them.
var notInTheRegistry = map[string]bool{
	"daemon": true, "mcp": true, "tui": true, "version": true,
}

var (
	fence     = regexp.MustCompile("(?s)```[a-z]*\n(.*?)```")
	quoted    = regexp.MustCompile(`"[^"]*"|'[^']*'`)
	skillPath = regexp.MustCompile(`skills/[A-Za-z0-9_./-]+`)
)

func docFiles(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, name := range []string{"README.md", filepath.Join("skills", "tasks", "SKILL.md"), contractFile} {
		body, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		out[name] = string(body)
	}
	return out
}

// commandLines pulls every `htask …` invocation out of a document's fenced
// code blocks, joining the backslash continuations a long example is wrapped
// with. The literal below is the binary's name, so it moved when the binary
// did — and if it had not, the coverage floor in the caller would have caught
// a run that suddenly found nothing.
func commandLines(doc string) []string {
	var out []string
	for _, block := range fence.FindAllStringSubmatch(doc, -1) {
		joined := strings.ReplaceAll(block[1], "\\\n", " ")
		for _, line := range strings.Split(joined, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "htask ") || line == "htask" {
				out = append(out, line)
			}
		}
	}
	return out
}

// §6.1: every verb and flag the docs teach is one the CLI actually has.
func TestDocsCiteTheRealSurface(t *testing.T) {
	docs := docFiles(t)
	seen := 0
	for name, doc := range docs {
		for _, line := range commandLines(doc) {
			// A quoted value can hold anything, including something that looks
			// like a flag; only the shape outside the quotes is a command.
			fields := strings.Fields(quoted.ReplaceAllString(line, `""`))
			for _, cmd := range splitCommands(fields) {
				seen++
				checkCommand(t, name, line, cmd)
			}
		}
	}
	// Logged, not just floored: after a rename this number is the difference
	// between "the docs were checked" and "the extractor stopped matching".
	t.Logf("%d command lines checked across %d documents", seen, len(docs))
	if seen < 20 {
		t.Fatalf("only %d command lines found in the docs; the extractor is reading nothing", seen)
	}
}

// splitCommands cuts a line into its commands. The skill's "everything else"
// block lays two per line in columns, which is a fine way to write it and a
// bad way to read it one command at a time.
func splitCommands(fields []string) [][]string {
	var out [][]string
	for _, f := range fields {
		if f == "htask" {
			out = append(out, []string{})
			continue
		}
		if len(out) == 0 {
			continue
		}
		out[len(out)-1] = append(out[len(out)-1], f)
	}
	return out
}

// wordsAndFlags splits one command into the verb path and the long flags it
// passes. Both directions of the docs check read a command the same way, so
// they read it in one place.
func wordsAndFlags(cmd []string) (words, flags []string) {
	for _, f := range cmd {
		switch {
		case strings.HasPrefix(f, "--"):
			flags = append(flags, strings.TrimPrefix(strings.SplitN(f, "=", 2)[0], "--"))
		default:
			words = append(words, f)
		}
	}
	return words, flags
}

func checkCommand(t *testing.T, name, line string, cmd []string) {
	t.Helper()
	for _, f := range cmd {
		if strings.HasPrefix(f, "-") && !strings.HasPrefix(f, "--") {
			t.Errorf("%s: %q uses a short flag; this CLI declares none", name, line)
		}
	}
	words, flags := wordsAndFlags(cmd)
	if len(words) > 0 && notInTheRegistry[words[0]] {
		return
	}
	v, ok := verbFor(words)
	if !ok {
		if len(words) == 0 && len(flags) > 0 {
			return // `htask --help`
		}
		t.Errorf("%s: %q names no verb this CLI has", name, line)
		return
	}
	for _, f := range flags {
		if globalFlags[f] || v.Accepts(f) {
			continue
		}
		t.Errorf("%s: %q passes --%s, which %s does not take", name, line, f, v.Name)
	}
}

// verbFor matches the longest registry CLI path at the head of the words.
func verbFor(words []string) (verbs.Verb, bool) {
	best, found := verbs.Verb{}, false
	for _, v := range verbs.All {
		if len(v.CLI) > len(words) {
			continue
		}
		match := true
		for i, part := range v.CLI {
			if words[i] != part {
				match = false
				break
			}
		}
		if match && len(v.CLI) > len(best.CLI) {
			best, found = v, true
		}
	}
	return best, found
}

// A path the docs tell someone to symlink or read has to be a path that is
// there: an install line that names the wrong directory fails in their shell,
// where nothing in this repo can catch it.
func TestDocsCiteTheRealSurfacePaths(t *testing.T) {
	for name, doc := range docFiles(t) {
		for _, p := range skillPath.FindAllString(doc, -1) {
			p = strings.TrimSuffix(p, ".")
			if _, err := os.Stat(filepath.Join("..", "..", p)); err != nil {
				t.Errorf("%s names %s, which is not in the repository: %v", name, p, err)
			}
		}
	}
}

// untaught names a flag the docs deliberately leave out, and why. It is a
// written-down list rather than a silence: --evidence-for shipped with the
// docs unchanged and the gate stayed green, because TestDocsCiteTheRealSurface
// only reads from the docs towards the registry. The test below reads the
// other way, so a new flag on a verb the docs already show has to be taught,
// or excused here, in the commit that adds it.
//
// The key is "<verb> --<flag>". Being on this list is a claim that a reader
// who never learns the flag still gets the verb right — true of a limit or a
// filter, false of anything that changes what a call MEANS.
var untaught = map[string]string{
	"task.list --status":        "--ready and --mine are the two the docs teach; the rest are filters a reader finds in --help",
	"task.list --query":         "a search filter; not knowing it costs nothing",
	"task.list --archived":      "a filter; the default is the one the docs describe",
	"task.list --limit":         "a filter; the default is the one the docs describe",
	"note.list --limit":         "a filter; the default is the one the docs describe",
	"events --entity":           "the events verb is shown as a shape, and §8.2 documents its arguments",
	"task.create --description": "the docs teach create through --validation, which is the part a reader gets wrong",
	"task.create --priority":    "an ordering hint; a task created without it is still a correct task",
	// The skill's "Everything else" block points at a verb in one line rather
	// than teaching it; --priority is the one example it carries.
	"task.update --title":       "named in the skill's Everything-else roundup, which points rather than teaches",
	"task.update --description": "named in the skill's Everything-else roundup, which points rather than teaches",
	"task.update --validation":  "named in the skill's Everything-else roundup, which points rather than teaches",
	"task.update --depends-on":  "named in the skill's Everything-else roundup, which points rather than teaches",
}

// toolCalls reads the `<tool> { "arg": ... }` blocks a document shows, and
// answers with the REGISTRY verb name each one names and the arguments it
// passes. A tool is named by its bare verb (§7.1), so the lookup is over
// verbs.MCPTools rather than over the dotted socket name.
func toolCalls(doc string) map[string][]string {
	byTool := map[string]string{}
	for _, v := range verbs.MCPTools() {
		byTool[v.MCP] = v.Name
	}
	out := map[string][]string{}
	call := regexp.MustCompile(`(?m)^\s*([a-z_]+)\s+\{`)
	key := regexp.MustCompile(`"([a-z][a-z-]*)"\s*:`)
	for _, m := range call.FindAllStringSubmatchIndex(doc, -1) {
		tool := doc[m[2]:m[3]]
		name, ok := byTool[tool]
		if !ok {
			continue
		}
		// The object runs to its matching brace; a doc block is small, so a
		// depth count over the rest of the file is enough and stops at zero.
		depth, end := 0, m[1]-1
		for i := m[1] - 1; i < len(doc); i++ {
			switch doc[i] {
			case '{':
				depth++
			case '}':
				depth--
			}
			if depth == 0 {
				end = i
				break
			}
		}
		for _, k := range key.FindAllStringSubmatch(doc[m[1]-1:end+1], -1) {
			out[name] = append(out[name], k[1])
		}
	}
	return out
}

// §6.1 in the direction the docs kept getting wrong: a flag a verb OFFERS is a
// flag the docs have to teach, once the docs have taken on that verb at all.
// Verbs no document shows are out of scope — README says `htask --help` lists
// every verb, and forcing an example for each would make the docs longer
// without making them truer.
func TestDocsTeachEveryFlagOfTheVerbsTheyShow(t *testing.T) {
	shown := map[string]map[string]bool{}
	for _, doc := range docFiles(t) {
		// A tool call teaches an argument as surely as a --flag does, and the
		// docs lead with tool calls now: §7.3 makes both doors first-class, so
		// a guard that only reads shell lines would hold the documents in the
		// shape of the surface it happens to know.
		for name, args := range toolCalls(doc) {
			if shown[name] == nil {
				shown[name] = map[string]bool{}
			}
			for _, a := range args {
				shown[name][a] = true
			}
		}
		for _, line := range commandLines(doc) {
			fields := strings.Fields(quoted.ReplaceAllString(line, `""`))
			for _, cmd := range splitCommands(fields) {
				words, flags := wordsAndFlags(cmd)
				if len(words) > 0 && notInTheRegistry[words[0]] {
					continue
				}
				v, ok := verbFor(words)
				if !ok {
					continue // already reported by TestDocsCiteTheRealSurface
				}
				if shown[v.Name] == nil {
					shown[v.Name] = map[string]bool{}
				}
				for _, f := range flags {
					shown[v.Name][f] = true
				}
			}
		}
	}
	if len(shown) < 10 {
		t.Fatalf("only %d verbs found in the docs; the extractor is reading nothing", len(shown))
	}
	for name, seen := range shown {
		v, ok := verbs.ByName(name)
		if !ok {
			t.Fatalf("verbFor returned %q, which verbs.ByName does not know", name)
		}
		for _, a := range v.Args {
			// A positional is never typed as --flag, so the docs teach it by
			// showing the verb at all.
			if a.Positional || seen[a.Name] {
				continue
			}
			if _, excused := untaught[name+" --"+a.Name]; excused {
				continue
			}
			t.Errorf("the docs show %s but never teach --%s. Teach it in README.md or skills/tasks/SKILL.md, or say in untaught why a reader does not need it.", name, a.Name)
		}
	}
	// An excuse that is no longer true is worse than no excuse: it says a
	// decision was made about a flag that has since been taught or removed.
	for key, why := range untaught {
		name, flag, ok := strings.Cut(key, " --")
		if !ok || why == "" {
			t.Errorf("untaught[%q] is not \"<verb> --<flag>\" with a reason", key)
			continue
		}
		v, known := verbs.ByName(name)
		if !known || !v.Accepts(flag) {
			t.Errorf("untaught names %s --%s, which is not a flag this CLI has", name, flag)
			continue
		}
		if shown[name][flag] {
			t.Errorf("untaught still excuses %s --%s, but the docs now teach it; drop the entry", name, flag)
		}
	}
}

// consumerSection is README's section for a program that is not Herdr and not
// an agent in a pane — everything from its heading to the next one.
var consumerSection = regexp.MustCompile(`(?s)\n## Driving htask from another program\n(.*?)\n## `)

// doctorFields is every json tag DoctorReport actually ships, read from the
// struct so a renamed field fails here rather than in the consumer that had
// been reading the old name out of this README.
func doctorFields(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	rt := reflect.TypeOf(daemon.DoctorReport{})
	for i := 0; i < rt.NumField(); i++ {
		tag, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		if tag != "" && tag != "-" {
			out[tag] = true
		}
	}
	if len(out) == 0 {
		t.Fatal("DoctorReport has no json tags; the reflection is reading nothing")
	}
	return out
}

// The section exists because a second binary now reads this plugin, and the
// three things it needs — where the store is, what scope it gets, which
// principal it may declare — are facts spread across §3.2, §4.2 and §5.1 that
// nothing gathered in one place. A section is only worth having if it cannot
// quietly go stale, so this reads its claims back against the code: the doctor
// fields against the struct that ships them, the pane-gone claim against the
// manifest and the script it names.
func TestTheExternalConsumerSectionStaysTrue(t *testing.T) {
	readme := docFiles(t)["README.md"]
	m := consumerSection.FindStringSubmatch(readme)
	if m == nil {
		t.Fatal("README has no `## Driving htask from another program` section, which is where an external consumer is taught")
	}
	section := m[1]

	// Discovery: the fields it tells a consumer to read are fields doctor has.
	fields := doctorFields(t)
	for _, want := range []string{"socket_path", "state_dir"} {
		if !fields[want] {
			t.Errorf("the section teaches doctor's %q and DoctorReport no longer ships it", want)
		}
		if !strings.Contains(section, want) {
			t.Errorf("the section does not name doctor's %q, which is how a consumer finds the store", want)
		}
	}

	// Scoping, principal, resume, completion, and the on-demand sweep. Each is
	// a claim a consumer acts on, so each has to be present in words.
	for _, want := range []string{
		"--project", "--all-projects", // §4.2 scope
		"--as plugin:", // §3.2, and TestAsRefusesDerivedPrincipals holds the refusal
		"--json",       // the semver-bound envelope, rather than the socket
		"--since",      // the replay, and the way out of it
		`"done":true`,  // completion at the socket
		"sweep --pane", // the on-demand form of the pane-gone release
		"--one-line",   // §16.2 through an argv that refuses a newline
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the section never mentions %q", want)
		}
	}

	// The pane-gone claim, checked against what actually implements it. A
	// consumer told not to reimplement this is owed a plugin that still does.
	manifest, err := os.ReadFile(filepath.Join("..", "..", "herdr-plugin.toml"))
	if err != nil {
		t.Fatalf("read herdr-plugin.toml: %v", err)
	}
	for _, event := range []string{"pane.closed", "pane.exited"} {
		if !strings.Contains(string(manifest), `on = "`+event+`"`) {
			t.Errorf("the section says the manifest reacts to %s and it no longer declares it", event)
		}
	}
	script := "scripts/on-pane-gone.sh"
	if !strings.Contains(string(manifest), script) {
		t.Errorf("the manifest no longer runs %s, which the section names as the reaction", script)
	}
	if !strings.Contains(section, script) {
		t.Errorf("the section does not name %s, so a reader cannot check the claim", script)
	}
	if _, err := os.Stat(filepath.Join("..", "..", script)); err != nil {
		t.Errorf("%s is named by the section and the manifest and is not in the repository: %v", script, err)
	}
}

// The contract binds a MUST to a test that fails without it. The skill is
// where the worker who writes the report meets that obligation, in the words
// a worker acts on: name the test that pins each claim, and name it in the
// report rather than leaving the reviewer to reconstruct the mapping. Pinned
// here for the same reason the rule exists — a paragraph of advice nothing
// checks is the shape the finding was about.
func TestTheSkillTeachesWhichTestPinsWhichClaim(t *testing.T) {
	doc := docFiles(t)[filepath.Join("skills", "tasks", "SKILL.md")]
	flat := strings.Join(strings.Fields(doc), " ")
	for _, phrase := range []string{
		"Every property your report claims needs a named test",
		"which test pins which claim",
		"a test that FAILS when the behaviour is deleted",
		"either write that test or drop the sentence from the report",
	} {
		if !strings.Contains(flat, phrase) {
			t.Errorf("skills/tasks/SKILL.md does not teach the claim-to-test mapping: %q is missing", phrase)
		}
	}
}

// §3.7 (0.10.0) makes an operator verb advice an agent confirms, and the
// plugin deliberately builds no mechanism to check that the confirmation
// happened. That leaves the skill as the ONLY place the duty is taught, which
// makes it load-bearing in a way a paragraph of prose usually is not: delete
// it and an agent reads a door that promotes without asking as permission to
// promote without asking. Pinned for exactly the reason the duty has no
// mechanism.
func TestTheSkillTeachesTheConfirmationDuty(t *testing.T) {
	doc := docFiles(t)[filepath.Join("skills", "tasks", "SKILL.md")]
	flat := strings.Join(strings.Fields(doc), " ")
	for _, phrase := range []string{
		// The verbs are reachable.
		"You may run those verbs",
		// And what is owed before running one.
		"confirm with the user",
		"AskUserQuestion",
		"asked for autonomy at the outset is not asked again",
		// Why nothing enforces it, said where an agent will read it.
		"Nothing checks that you asked",
		// And the boundary: what no confirmation lifts.
		"no confirmation lifts them",
	} {
		if !strings.Contains(flat, phrase) {
			t.Errorf("skills/tasks/SKILL.md does not teach the confirmation duty §3.7 relies on: %q is missing", phrase)
		}
	}
}

// A doc that cites a test by name is making a promise about what is enforced,
// and the citation is the only thing a reader can check it by. This task was
// rejected for one: docs/contract-notes.md named a test as the current
// guarantee for §7.3 in the same commit that deleted it, thirty lines below a
// paragraph asserting the opposite fact. Nothing was red, because no test
// reads the docs' citations.
//
// A citation is allowed to name a test that no longer exists — this file is a
// conformance record, and deleting its earlier readings would make it
// uncheckable against the binaries that shipped under them. What it may not
// do is name one SILENTLY: the SENTENCE carrying the citation has to say the
// test is gone, so a reader meets the citation and its status in one breath.
//
// Sentence, not paragraph, and that is the whole aim of this test. The
// paragraph that got this task rejected opened with "TestMCPToolCountStaysSmall
// is gone" and then, four sentences later, named its successor as what holds
// today — in a commit that deleted the successor too. A marker anywhere in the
// paragraph would have passed it.
func TestEveryTestCitedInTheDocsExistsOrIsMarkedGone(t *testing.T) {
	live := liveTestNames(t)
	// Words that make a paragraph a record of something that was, rather than
	// a claim about what holds now.
	// Deliberately NOT "replaced": "X is replaced by Y" marks X as gone, but
	// "what replaced it is Y" asserts Y is live, and the sentence that got
	// this task rejected was the second kind. A marker word has to mean gone
	// in every sentence it can appear in, or it excuses the citation it was
	// meant to catch.
	past := []string{"gone", "deleted", "no longer exists", "does not exist",
		"no longer", "returns nothing", "superseded", "was removed", "is removed"}

	// NOT docFiles: that set is README, the skill and the contract, and the
	// file this test exists for — the conformance record — was never in it,
	// which is the reason nothing was red. Every tracked markdown file is
	// scanned, because a citation is a promise wherever it is written.
	for name, doc := range everyMarkdownFile(t) {
		for _, sentence := range sentences(doc) {
			lower := strings.ToLower(sentence)
			marked := false
			for _, w := range past {
				if strings.Contains(lower, w) {
					marked = true
					break
				}
			}
			for _, cited := range citedTests.FindAllStringSubmatch(sentence, -1) {
				if live[cited[1]] || marked {
					continue
				}
				t.Errorf("%s cites %s as something that holds, and no such test exists. "+
					"Either the citation is stale, or this sentence has to say the test is gone:\n  %s",
					name, cited[1], sentence)
			}
		}
	}
}

// citedTests matches a backticked Go test name in prose.
var citedTests = regexp.MustCompile("`(Test[A-Za-z0-9_]+)`")

// liveTestNames is every test function this repository defines.
func liveTestNames(t *testing.T) map[string]bool {
	t.Helper()
	out, err := exec.Command("git", "-C", filepath.Join("..", ".."), "ls-files", "*_test.go").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	decl := regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]+)\(`)
	names := map[string]bool{}
	for _, f := range strings.Fields(string(out)) {
		body, err := os.ReadFile(filepath.Join("..", "..", f))
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for _, m := range decl.FindAllStringSubmatch(string(body), -1) {
			names[m[1]] = true
		}
	}
	if len(names) == 0 {
		t.Fatal("no test functions found; the scan is not reading the tree")
	}
	return names
}

// sentences splits flattened prose on sentence ends and on the cell walls of a
// markdown table, which is one line but many claims.
func sentences(doc string) []string {
	flat := strings.Join(strings.Fields(doc), " ")
	for _, sep := range []string{". ", "; ", " | ", ": "} {
		flat = strings.ReplaceAll(flat, sep, "\x00")
	}
	out := []string{}
	for _, s := range strings.Split(flat, "\x00") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// everyMarkdownFile reads every tracked .md file in the repository.
func everyMarkdownFile(t *testing.T) map[string]string {
	t.Helper()
	out, err := exec.Command("git", "-C", filepath.Join("..", ".."), "ls-files", "*.md").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	docs := map[string]string{}
	for _, f := range strings.Fields(string(out)) {
		body, err := os.ReadFile(filepath.Join("..", "..", f))
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		docs[f] = string(body)
	}
	if len(docs) == 0 {
		t.Fatal("no markdown files found; the scan is not reading the tree")
	}
	if _, ok := docs[filepath.Join("docs", "contract-notes.md")]; !ok {
		t.Fatal("docs/contract-notes.md is not in the scan; it is the file this test was written for")
	}
	return docs
}

// The other half of the same rejection. Alongside the dead citation, the same
// entry asserted "the door serves 18 of 30 verbs now" and "every remaining
// operator verb is still CLI-only", thirty lines below a paragraph saying the
// opposite. `TestEveryVerbIsOnBothDoors` makes both unconditionally false, so
// a doc that says them is not out of date, it is wrong.
//
// Only PRESENT-tense assertion shapes are listed. This file is a conformance
// record and has to be able to say what was true at 0.7.0, so the discipline
// this enforces is "write history in the past tense", which is a discipline a
// reader benefits from anyway. `--as` is a flag rather than a verb and sibling
// plugins are not this one, so a sentence naming either is left alone.
func TestNoDocClaimsThisPluginsDoorIsPartial(t *testing.T) {
	// Every entry is present tense BY CONSTRUCTION, so a sentence trips it
	// only by asserting something about today. "served 13 of its 30 verbs"
	// and "was off the door" are records and pass, which is the point: the
	// file has to be able to say what was true at 0.7.0. An earlier draft of
	// this list carried "of 30 verbs" and "pinned subset" and flagged three
	// correctly past-tense sentences — a count is not a tense, and a guard
	// that fires on honest history is one someone will delete.
	falseToday := []string{
		"is still off the door", "are still off the door",
		"is off the door", "are off the door",
		"still cli-only", "is cli-only", "are cli-only",
		"verbs now", "tools now",
	}
	// A second draft added "pinned subset", "chosen subset" and "narrowing
	// rule" to catch the superseded §6.1 reading, and they flagged that
	// entry's own PAST-tense rewrite — "this plugin read §7.3 as the
	// narrowing rule ... a chosen subset was also an MCP tool". A noun phrase
	// carries no tense, so no substring of one can tell a record from a
	// claim. That entry is pinned by its marker instead, in
	// TestTheSupersededParityReadingSaysSoInItsHeading.
	for name, doc := range everyMarkdownFile(t) {
		for _, sentence := range sentences(doc) {
			lower := strings.ToLower(sentence)
			if strings.Contains(lower, "`--as`") || strings.Contains(lower, "herdr-mail") ||
				strings.Contains(lower, "herdr-dispatch") {
				continue
			}
			for _, claim := range falseToday {
				if strings.Contains(lower, claim) {
					t.Errorf("%s says %q in the present tense, and §7.3 admits no CLI-only verb; "+
						"every one of this plugin's verbs is on both doors. Write it in the past "+
						"tense if it is a record of what was:\n  %s", name, claim, sentence)
				}
			}
		}
	}
}

// §13.3 makes the CLI, the MCP tool list, the JSON shapes and the error codes
// changeable between minors WITH AN ENTRY in the changelog, which is the
// changelog's own opening paragraph. The commit that grew the tool list from
// 24 to 32 bumped the version and wrote no entry, and nothing was red: the
// promise had no mechanism. This is the mechanism.
func TestTheChangelogHasAnEntryForThisVersion(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "CHANGELOG.md"))
	if err != nil {
		t.Fatalf("read CHANGELOG.md: %v", err)
	}
	if !strings.Contains(string(body), "\n## "+daemon.Version+" ") {
		t.Errorf("CHANGELOG.md has no `## %s` entry, and this binary is %s. §13.3 makes a "+
			"surface change between minors legal only with an entry here, and the changelog "+
			"says so in its own first paragraph", daemon.Version, daemon.Version)
	}
}

// The README says the binary version too, in the §7.1/§13.3 paragraph whose
// whole job is to tell a client which release its wired-in tool names are
// semver-bound against. That sentence drifted a release behind at 0.6.0 with
// nothing red, because daemon.Version was bound to a CHANGELOG heading and to
// nothing in the README.
//
// The clause is anchored on "The binary is **<version>**" rather than on the
// bare number, because the same sentence names the version it moved FROM and
// the file names older releases all over. A guard that only asked whether the
// number appears somewhere would pass a sentence still naming the old one.
func TestTheREADMESaysTheBinaryVersionThisIs(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	want := "The binary is **" + daemon.Version + "**"
	if !strings.Contains(string(body), want) {
		t.Errorf("README.md does not say %q, and this binary is %s. That sentence tells a "+
			"client which release its tool names are bound to, so a stale one sends it to "+
			"the wrong contract for the names it has wired in", want, daemon.Version)
	}
}

// The same promise, one shipped value over. `doctor --json` carries the
// contract revision this binary declares, and §13.3's entry rule covers what a
// consumer can pin on — the revision is one of those, since a caller reads it
// to decide which contract's rules the daemon it is talking to answers to.
// Task 89 moved it from 0.6.0 to 0.10.0 with a green full gate and no word in
// the changelog, which is the same shape TestTheChangelogHasAnEntryForThisVersion
// closed for daemon.Version, so this is the same mechanism aimed at the second
// value.
//
// A bare version string is not the anchor: "0.6.0" appears in this file as a
// RELEASE heading, and the entry that records a move names the version it
// moved FROM as well as the one it moved to. Either would let a guard that
// only asks "is this string in the file, near that phrase" pass a move BACK to
// a version some other sentence already mentions. So what is pinned is one
// clause that can only be about the value this binary declares TODAY, with the
// revision inside it — which is what makes the guard fire in both directions:
// neither an upward nor a downward move can borrow a string it did not write.
func TestTheChangelogHasALineForTheDeclaredContractRevision(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "CHANGELOG.md"))
	if err != nil {
		t.Fatalf("read CHANGELOG.md: %v", err)
	}
	clause := "the declared contract revision is now " + daemon.ContractVersion
	// The changelog wraps its prose, so the clause can carry a newline where a
	// space is; compare on collapsed whitespace.
	flat := strings.ToLower(strings.Join(strings.Fields(string(body)), " "))
	// The version has to END where the clause ends. A mutation proved the
	// prefix match alone unsound: with the changelog announcing 0.10.0-draft
	// and the binary declaring 0.10.0, the clause was found and the guard went
	// green while the file named a DIFFERENT revision — and a revision of this
	// contract really can carry a -draft suffix, so that is not a contrived
	// input.
	found := false
	for i := 0; ; {
		at := strings.Index(flat[i:], clause)
		if at < 0 {
			break
		}
		i += at + len(clause)
		if i == len(flat) || !strings.ContainsRune("0123456789.-", rune(flat[i])) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("CHANGELOG.md has no entry saying %q, and this binary declares contract "+
			"revision %s in `doctor --json`. §13.3 makes a change a consumer can pin on legal "+
			"between minors only with an entry here, and the revision moved without one",
			clause, daemon.ContractVersion)
	}
}

// The reviewer's condition on the superseded §6.1 entry, kept as a condition
// rather than a promise. That entry records how §6.1 and §7.3 were read while
// §7.3 still asked for a tool budget, and it is KEPT: a conformance record
// that deletes its earlier readings cannot be checked against the binaries
// that shipped under them. What it may not do is read as current, and no
// phrase test can tell a record from a claim — "a chosen subset was also an
// MCP tool" and "a chosen subset is also an MCP tool" differ by one word that
// no noun phrase carries. So the marker is what is pinned: the heading says
// which revisions the entry covers and that it is superseded, which is the
// first thing a reader meets.
func TestTheSupersededParityReadingSaysSoInItsHeading(t *testing.T) {
	doc := everyMarkdownFile(t)[filepath.Join("docs", "contract-notes.md")]
	var heading string
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "## §6.1") {
			heading = line
			break
		}
	}
	if heading == "" {
		t.Fatal("docs/contract-notes.md has no §6.1 entry; it is the record of the reading §7.3's budget forced")
	}
	if !strings.Contains(strings.ToLower(heading), "superseded") {
		t.Errorf("the §6.1 entry's heading does not say it is superseded, so it reads as this "+
			"plugin's current reading of §6.1 and §7.3 — and it is not, since 0.10.0 put every "+
			"verb on both doors:\n  %s", heading)
	}
}

// The same condition on the 0.9.0 entry, and it is here because a mutation
// proved the marker unheld: with "superseded" stripped from that heading the
// whole package stayed green, and the entry read again as a current claim that
// nothing had audited this plugin against 0.9.0's rule — which task 86's sweep
// had already answered, seventy lines below in the same file. That defect shape
// — an entry saying a piece of WORK has not happened, after it has — is not
// reachable by a phrase list: "nothing has audited this" is the honest and
// necessary way to record a gap on the day it is recorded, so a list of such
// phrases would fire on every correct gap entry. What IS pinnable is the same
// thing §6.1 pins: one literal heading, and whether it carries its marker.
func TestTheSupersededAuditGapEntrySaysSoInItsHeading(t *testing.T) {
	doc := everyMarkdownFile(t)[filepath.Join("docs", "contract-notes.md")]
	var heading string
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "## 0.9.0 ") {
			heading = line
			break
		}
	}
	if heading == "" {
		t.Fatal("docs/contract-notes.md has no 0.9.0 entry; it is the record of the gap between " +
			"0.9.0's rule and the sweep that answered it")
	}
	if !strings.Contains(strings.ToLower(heading), "superseded") {
		t.Errorf("the 0.9.0 entry's heading does not say it is superseded, so it reads as a "+
			"current claim that nothing has audited this plugin against 0.9.0's rule — and the "+
			"sweep that did is in this same file:\n  %s", heading)
	}
}

// §13.3 makes the CLI and the MCP tool list changeable between minors WITH AN
// ENTRY here, and until now nothing held that end of the promise. The version
// guards above fire on a version that MOVED; a surface that moves without one
// — every change between two releases, which is where a caller break is
// actually written — had nothing watching it. Task 93's mutation pass proved
// it by emptying the Unreleased entry: the whole prose and docs suite stayed
// green while the file said nothing about a CLI break that renamed every task
// verb.
//
// The mechanism is the one already in the repo, aimed one step further.
// `verbs.Fingerprint` is the door surface the daemon reports in `doctor
// --json`, and it is SHIPPED, so it cannot be widened to close this: a
// consumer pins it. `verbs.CallerSurface` is the same idea over everything a
// caller can be broken by, including the two the shipped fingerprint does not
// hash — the CLI subcommand path and the MCP tool name. `daemon.ReleasedSurface`
// records what that digest was at `daemon.Version`, and cutting a release
// re-pins it beside the version bump.
//
// The rule is only "say something", not "say the right thing". A guard cannot
// read prose for truth. What it can do is refuse the case that actually
// happened: a surface that moved since the last release with an Unreleased
// entry that is empty.
func TestASurfaceThatMovedSinceTheReleasePinHasAnUnreleasedEntry(t *testing.T) {
	if verbs.CallerSurface() == daemon.ReleasedSurface {
		t.Skip("the caller surface has not moved since " + daemon.Version)
	}
	body, err := os.ReadFile(filepath.Join("..", "..", "CHANGELOG.md"))
	if err != nil {
		t.Fatalf("read CHANGELOG.md: %v", err)
	}
	if strings.TrimSpace(unreleasedEntry(string(body))) == "" {
		t.Errorf("the caller surface is %s and %s shipped %s, so the CLI or the MCP tool "+
			"list moved since the last release — and the `## Unreleased` entry in "+
			"CHANGELOG.md is empty. §13.3 makes that move legal only with an entry saying "+
			"what a caller does about it",
			verbs.CallerSurface(), daemon.Version, daemon.ReleasedSurface)
	}
}

// The pin is only worth what it catches, so this is the proof that it catches
// the move that got past everything else: task 93's, where the verb names, the
// gate names and every argument stayed exactly as they were and only the CLI
// path changed. The shipped `verbs.Fingerprint` hashes none of that path, so
// it does NOT move here — asserting both halves is the point.
func TestTheCallerSurfaceMovesWhenOnlyTheCLIPathDoes(t *testing.T) {
	flat := append([]verbs.Verb{}, verbs.All...)
	grouped := append([]verbs.Verb{}, verbs.All...)
	grouped[0].CLI = append([]string{"task"}, grouped[0].CLI...)

	if verbs.CallerSurfaceOf(flat) == verbs.CallerSurfaceOf(grouped) {
		t.Error("moving a verb's CLI path left the caller surface digest unchanged, so the " +
			"guard above would sleep through exactly the break it exists for")
	}
	if verbs.FingerprintOf(flat) != verbs.FingerprintOf(grouped) {
		t.Error("the shipped door fingerprint moved on a CLI-path-only change; it is " +
			"semver-bound and this test's premise is that it does not")
	}
}

// The MCP tool name is the other half a client wires in, and §7.1 pins the
// list. It is not in the shipped fingerprint either.
func TestTheCallerSurfaceMovesWhenOnlyTheMCPToolNameDoes(t *testing.T) {
	before := append([]verbs.Verb{}, verbs.All...)
	after := append([]verbs.Verb{}, verbs.All...)
	after[0].MCP = after[0].MCP + "_renamed"

	if verbs.CallerSurfaceOf(before) == verbs.CallerSurfaceOf(after) {
		t.Error("renaming an MCP tool left the caller surface digest unchanged")
	}
}

// unreleasedEntry is the body of the `## Unreleased` heading: everything up to
// the next `## `, which is the most recent release. An absent heading and an
// empty one are the same answer, because they leave a caller with the same
// nothing.
func unreleasedEntry(changelog string) string {
	_, rest, found := strings.Cut(changelog, "\n## Unreleased\n")
	if !found {
		return ""
	}
	if next := strings.Index(rest, "\n## "); next >= 0 {
		rest = rest[:next]
	}
	return rest
}

func TestUnreleasedEntryReadsTheEntryAndNotItsNeighbour(t *testing.T) {
	const file = "# Changelog\n\n## Unreleased\n\nthe entry\n\n## 0.7.0 — 2026-08-24\n\nthe release\n"
	if got := strings.TrimSpace(unreleasedEntry(file)); got != "the entry" {
		t.Errorf("unreleasedEntry = %q, want %q", got, "the entry")
	}
	const emptied = "# Changelog\n\n## Unreleased\n\n## 0.7.0 — 2026-08-24\n\nthe release\n"
	if got := strings.TrimSpace(unreleasedEntry(emptied)); got != "" {
		t.Errorf("an emptied entry read as %q, want empty", got)
	}
	const missing = "# Changelog\n\n## 0.7.0 — 2026-08-24\n\nthe release\n"
	if got := strings.TrimSpace(unreleasedEntry(missing)); got != "" {
		t.Errorf("a missing heading read as %q, want empty", got)
	}
}

// The two tests above pin two of the digest's inputs by name and leave the
// rest to the reader's trust, which a mutation pass showed is not enough:
// dropping AllProjects, Mutates, Required and Positional out of the hash left
// the whole suite green. Each of those decides whether a call is ANSWERED or
// refused with USAGE (§4.4, §5.6, §6.1), so a verb that quietly stops honouring
// --all-projects is a caller break the pin would have slept through.
//
// So every field the digest claims to read gets a mutator here. A field added
// to the hash without a line in this table is a field nothing proves is in it.
func TestEveryFieldTheCallerSurfaceHashesMovesIt(t *testing.T) {
	for _, tc := range []struct {
		field string
		move  func(*verbs.Verb)
	}{
		{"Name", func(v *verbs.Verb) { v.Name += ".renamed" }},
		{"CLI", func(v *verbs.Verb) { v.CLI = append([]string{"task"}, v.CLI...) }},
		{"MCP", func(v *verbs.Verb) { v.MCP += "_renamed" }},
		{"Gated", func(v *verbs.Verb) { v.Gated += ".renamed" }},
		{"AllProjects", func(v *verbs.Verb) { v.AllProjects = !v.AllProjects }},
		{"Mutates", func(v *verbs.Verb) { v.Mutates = !v.Mutates }},
		{"Args[].Name", func(v *verbs.Verb) { v.Args[0].Name += "_renamed" }},
		{"Args[].Type", func(v *verbs.Verb) { v.Args[0].Type = verbs.Strings }},
		{"Args[].Required", func(v *verbs.Verb) { v.Args[0].Required = !v.Args[0].Required }},
		{"Args[].Positional", func(v *verbs.Verb) { v.Args[0].Positional = !v.Args[0].Positional }},
	} {
		t.Run(tc.field, func(t *testing.T) {
			before := append([]verbs.Verb{}, verbs.All...)
			after := append([]verbs.Verb{}, verbs.All...)
			// The Verb is copied by the append above, but Args is a slice
			// header pointing at the registry's own backing array; mutating an
			// element through it would corrupt verbs.All for every later test.
			subject := after[0]
			subject.Args = append([]verbs.Arg{}, subject.Args...)
			tc.move(&subject)
			after[0] = subject

			if verbs.CallerSurfaceOf(before) == verbs.CallerSurfaceOf(after) {
				t.Errorf("moving %s left the caller surface digest unchanged, so it is not "+
					"in the hash and the release guard cannot see it move", tc.field)
			}
		})
	}
}

// The pin the guard above compares against is a number in a source file, and a
// mutation pass showed what that is worth on its own: re-pointing
// daemon.ReleasedSurface at HEAD's own digest turned the guard from PASS to
// SKIP and nothing anywhere went red. That is not a contrived input — it is
// the cheapest way to make a red gate green, one edit, no changelog entry, and
// it silences the exact rule §13.3 asks for.
//
// So the pin is checked against the thing it claims to be: verbs.CallerSurface
// over the verbs package as it stood at tag v<daemon.Version>. The digest
// function is copied onto that old package rather than reimplemented, so the
// comparison is the same function over old data.
//
// A checkout with no tags cannot answer, and this SKIPS there rather than
// failing a shallow CI clone. TASKS_PIN_REQUIRED=1 turns that skip into a
// failure, the way TASKS_E2E_REQUIRED does for layer 3, and `make
// release-check` sets it: the machine that cuts a tag is the machine that has
// them, and it is the only place the pin's accuracy is load-bearing.
func TestTheReleasedSurfacePinIsWhatThatTagActuallyShipped(t *testing.T) {
	root := filepath.Join("..", "..")
	tag := "v" + daemon.Version
	required := os.Getenv("TASKS_PIN_REQUIRED") == "1"

	if err := exec.Command("git", "-C", root, "rev-parse", "-q", "--verify",
		"refs/tags/"+tag).Run(); err != nil {
		if required {
			t.Fatalf("TASKS_PIN_REQUIRED=1 and this checkout has no tag %s, so "+
				"daemon.ReleasedSurface cannot be checked against what that release "+
				"shipped: %v", tag, err)
		}
		t.Skipf("no tag %s in this checkout; run with TASKS_PIN_REQUIRED=1 where there is one", tag)
	}

	dir := t.TempDir()
	pkg := filepath.Join(dir, "verbs")
	if err := os.MkdirAll(filepath.Join(dir, "cmd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}

	// Every non-test source file of the verbs package at that tag.
	out, err := exec.Command("git", "-C", root, "ls-tree", "--name-only",
		tag, "internal/verbs/").Output()
	if err != nil {
		t.Fatalf("list internal/verbs at %s: %v", tag, err)
	}
	for _, path := range strings.Fields(string(out)) {
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			continue
		}
		body, err := exec.Command("git", "-C", root, "show", tag+":"+path).Output()
		if err != nil {
			t.Fatalf("read %s at %s: %v", path, tag, err)
		}
		if err := os.WriteFile(filepath.Join(pkg, filepath.Base(path)), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Today's digest, verbatim, on top of that old package. If the tag already
	// had a surface.go this replaces it, which is what we want: the pin is
	// today's question asked of old data.
	today, err := os.ReadFile(filepath.Join(root, "internal", "verbs", "surface.go"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "surface.go"), today, 0o644); err != nil {
		t.Fatal(err)
	}

	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module pin\n\ngo 1.22\n")
	write(filepath.Join("cmd", "main.go"),
		"package main\n\nimport (\n\t\"fmt\"\n\n\t\"pin/verbs\"\n)\n\nfunc main() { fmt.Print(verbs.CallerSurface()) }\n")

	run := exec.Command("go", "run", "./cmd")
	run.Dir = dir
	// No network: the reconstructed module has no requirements, and a digest
	// that suddenly needs one is a finding, not something to go fetch.
	run.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOPROXY=off")
	shipped, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("could not compute the surface of %s: %v\n%s\n\nIf this is a compile "+
			"error, the digest now reads a Verb field that did not exist at %s. That "+
			"changes what the pin MEANS, so re-pin it by hand and say so in the "+
			"changelog rather than deleting this test", tag, err, shipped, tag)
	}
	if got := strings.TrimSpace(string(shipped)); got != daemon.ReleasedSurface {
		t.Errorf("daemon.ReleasedSurface is %q, but the verbs table at %s digests to %q. "+
			"The pin is the record of what the last release put in front of a caller; a "+
			"wrong one makes the changelog guard ask the wrong question, and pointing it "+
			"at HEAD makes it ask none at all", daemon.ReleasedSurface, tag, got)
	}
}
