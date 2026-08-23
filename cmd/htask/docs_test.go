package main_test

import (
	"os"
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

// §6.1 in the direction the docs kept getting wrong: a flag a verb OFFERS is a
// flag the docs have to teach, once the docs have taken on that verb at all.
// Verbs no document shows are out of scope — README says `htask --help` lists
// every verb, and forcing an example for each would make the docs longer
// without making them truer.
func TestDocsTeachEveryFlagOfTheVerbsTheyShow(t *testing.T) {
	shown := map[string]map[string]bool{}
	for _, doc := range docFiles(t) {
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
