package main_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/husniadil/herdr-tasks/internal/verbs"
)

// The 0.9.0 sweep listed these MUSTs as verified by reading the source and
// answerable to nothing: remove the behaviour and the suite stays green. Each
// one below is a static property of what this repository commits, so the pin
// is a scan of the tracked files rather than a run of the binary. That is not
// a weaker test — the MUST is itself a statement about the source — but it is
// a narrower one, and each comment says which sentence of its § it holds.

// goSource returns every tracked non-test .go file with its body, which is
// the surface all four §1 MUSTs are about: what this plugin's own code does,
// not what a test harness may do to stand in for Herdr.
func goSource(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, name := range trackedFiles(t) {
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		out[name] = string(body)
	}
	if len(out) < 20 {
		t.Fatalf("scanned %d non-test .go files; the file list is reading nothing", len(out))
	}
	return out
}

// otherMultiplexer matches a reference to a terminal multiplexer that is not
// Herdr, and to the PTY primitives a plugin would need to spawn one of its
// own. `zmx` is bounded so that a longer identifier containing it is not a
// hit; the rest are distinctive enough to match bare.
var otherMultiplexer = regexp.MustCompile(`(?i)tmux|\bzmx\b|creack/pty|/dev/ptmx|openpty|posix_openpt|Setctty`)

// §1.1: Herdr is the only terminal substrate — no other multiplexer, and no
// PTY of this plugin's own. Everything that touches a terminal goes through
// `herdr`. Nothing held this: a `tmux` shell-out added anywhere in the daemon
// left the suite green.
func TestHerdrIsTheOnlyTerminalSubstrate(t *testing.T) {
	for name, body := range goSource(t) {
		if m := otherMultiplexer.FindString(body); m != "" {
			t.Errorf("%s names %q. §1.1 admits no multiplexer but Herdr and no PTY of this "+
				"plugin's own; a terminal is reached through the `herdr` CLI or its socket", name, m)
		}
	}
}

// createTable matches a table this plugin declares.
var createTable = regexp.MustCompile(`(?i)CREATE\s+TABLE\s+([a-z_][a-z0-9_]*)`)

// §1.2: Herdr is the agent registry, and a plugin MUST NOT maintain one of
// its own. Pane identity, agent name, harness, status and native session all
// come from Herdr per request; what this plugin stores is which principal
// holds a claim, which is a fact about a TASK. The failure this guards is a
// table of running agents growing beside the tasks table, at which point two
// answers to "who is alive" exist and the stale one wins arguments.
func TestNoRegistryOfRunningAgents(t *testing.T) {
	tables := createTable.FindAllStringSubmatch(schemaSQL(t), -1)
	if len(tables) < 5 {
		t.Fatalf("found %d tables in the schema; the pattern is reading nothing", len(tables))
	}
	for _, m := range tables {
		switch strings.ToLower(m[1]) {
		case "agents", "agent", "panes", "pane", "harnesses", "workspaces", "tabs":
			t.Errorf("the schema declares a %q table. §1.2 makes Herdr the agent registry: "+
				"pane identity, agent name, harness, status and native session are read from "+
				"Herdr through internal/herdrclient, never kept here", m[1])
		}
	}
}

// §1.3: Herdr is the human UI. The human surface is a Herdr plugin pane and
// the CLI, and the core function MUST NOT require a browser or a web server.
// `net/http` is the import that would arrive first if one ever did, and the
// dependency budget in CLAUDE.md is a separate rule that would not catch it —
// `net/http` is in the standard library and costs no `go.mod` line.
func TestNoBrowserOrWebServerInTheCoreFunction(t *testing.T) {
	for name, body := range goSource(t) {
		if strings.Contains(body, `"net/http"`) || strings.Contains(body, `"net/http/`) {
			t.Errorf("%s imports net/http. §1.3 makes the human surface a Herdr pane and the "+
				"CLI, and forbids requiring a browser or a web server for the core function", name)
		}
	}
}

// siblingPlugin matches an import path into another plugin of this contract,
// and the filename of another plugin's store. §13.2 fixes the short names, so
// the set is known rather than guessed.
var siblingPlugin = regexp.MustCompile(`herdr-(mail|dispatch|schedule)|(mail|dispatch|schedule)\.db`)

// §1.4: plugins compose by calling each other's CLI or MCP, never by
// importing each other's code or reading each other's SQLite file. The
// composition this plugin does have — a sibling declaring itself a door
// (§3.5) — goes through the socket, so nothing here should name a sibling in
// an import or a path.
func TestNoSiblingPluginCodeOrStore(t *testing.T) {
	for name, body := range goSource(t) {
		for _, line := range strings.Split(body, "\n") {
			trimmed := strings.TrimSpace(line)
			// A comment may discuss a sibling; §1.4 is about what the code
			// reaches for, and a citation that named no sibling would be
			// unreadable.
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			if m := siblingPlugin.FindString(trimmed); m != "" {
				t.Errorf("%s names %q outside a comment. §1.4 admits no import of another "+
					"plugin's code and no read of another plugin's store; composition is by "+
					"CLI, MCP, events, or the §9 hooks:\n  %s", name, m, trimmed)
			}
		}
	}
}

// schemaSQL is every CREATE and ALTER this plugin's store declares, which is
// the whole surface §4.3 and §14 are about. Reading the file rather than an
// open database keeps this in the fast layer and means a migration that is
// written but not yet applied is checked too.
func schemaSQL(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "internal", "store", "schema.go"))
	if err != nil {
		t.Fatalf("read the schema: %v", err)
	}
	if !strings.Contains(string(body), "CREATE TABLE tasks") {
		t.Fatal("the schema file declares no tasks table; this test is reading the wrong file")
	}
	return string(body)
}

// keyDeclaration matches the four places a column can become a key: a
// table-level PRIMARY KEY clause, a COLUMN-level one (where the column name
// sits BEFORE the words, which the first cut of this pattern read past —
// `pane_id TEXT PRIMARY KEY` is the most direct shape §4.3 forbids and it
// survived), a UNIQUE index, and a plain index.
var keyDeclaration = regexp.MustCompile(`(?im)^\s*[a-z_][a-z0-9_]*[^,;\n]*PRIMARY KEY[^;\n]*|PRIMARY KEY[^;\n]*|CREATE\s+(?:UNIQUE\s+)?INDEX[^;]*`)

// §4.3: `workspace_id`, `tab_id` and `pane_id` are context, not scope. A row
// MAY carry them for display and navigation and MUST NOT be partitioned by
// them. `internal/tasks/task.go` says so in a comment and nothing checked it:
// making `pane_id` half of a primary key failed nothing, and a store keyed on
// a pane would lose a project's work the day a pane id was reused.
func TestHerdrContextIsNeverAPartitionKey(t *testing.T) {
	schema := schemaSQL(t)
	keys := keyDeclaration.FindAllString(schema, -1)
	if len(keys) < 5 {
		t.Fatalf("found %d key declarations in the schema; the pattern is reading nothing", len(keys))
	}
	for _, key := range keys {
		for _, id := range []string{"workspace_id", "tab_id", "pane_id"} {
			if strings.Contains(key, id) {
				t.Errorf("%q is a key, and §4.3 makes Herdr's %s context rather than scope: "+
					"a plugin records it for display and navigation and never partitions on it",
					strings.TrimSpace(key), id)
			}
		}
	}
}

// forbiddenNoun matches §14's forbidden vocabulary as a whole word.
var forbiddenNoun = regexp.MustCompile(`(?i)\b(sidebar|card|row|widget|seat|instance|session)\b`)

// herdrsOwnSession is §14's one exception, spelled the way this schema
// spells it. §14 writes the exception as `agent_session`; the two columns
// here suffix it — the value in both is exactly Herdr's native session
// reference for the agent that acted, which is the thing the exception
// names. The spelling gap is recorded in docs/contract-notes.md rather than
// papered over with a looser pattern, because a pattern that let any
// `*_session` through would let a session of this plugin's own invention
// through with it.
var herdrsOwnSession = map[string]bool{
	"claimed_by_session":   true,
	"submitted_by_session": true,
}

// sqlIdentifier matches a declared table, column, or index name: the name at
// the head of its own line in a CREATE TABLE body, and the names CREATE
// TABLE / INDEX and ALTER TABLE ADD COLUMN introduce. ADD COLUMN is matched
// wherever it appears and not only at a line head: a migration is a one-line
// Go string, so `ALTER TABLE tasks ADD COLUMN instance TEXT;` sits mid-line,
// and the first cut of this pattern read past every column the store has
// added since its first release.
var sqlIdentifier = regexp.MustCompile(`(?im)^\s*(?:CREATE\s+TABLE\s+|CREATE\s+(?:UNIQUE\s+)?INDEX\s+)?([a-z_][a-z0-9_]*)\s|ADD\s+COLUMN\s+([a-z_][a-z0-9_]*)`)

// §14: the forbidden nouns appear in no schema and no verb name. This is a
// glossary MUST with real teeth — `row`, `card` and `instance` are exactly
// the words a schema drifts into — and nothing grepped for them.
func TestTheForbiddenNounsAreInNoSchemaOrVerbName(t *testing.T) {
	// The schema, identifier by identifier rather than as prose: the file's
	// comments explain the design and are allowed the English words.
	for _, m := range sqlIdentifier.FindAllStringSubmatch(schemaSQL(t), -1) {
		name := m[1]
		if name == "" {
			name = m[2]
		}
		if herdrsOwnSession[name] {
			continue
		}
		if noun := forbiddenNoun.FindString(name); noun != "" {
			t.Errorf("the schema declares %q, and §14 forbids %q in APIs and schemas "+
				"(only Herdr's own agent session is excepted)", name, noun)
		}
	}
	// And the verb names, which are the other half of what a caller sees:
	// the socket verb, every CLI word, the MCP tool name, and each argument.
	for _, v := range verbs.All {
		names := append([]string{v.Name, v.MCP}, v.CLI...)
		for _, a := range v.Args {
			names = append(names, a.Name)
		}
		for _, name := range names {
			if noun := forbiddenNoun.FindString(name); noun != "" {
				t.Errorf("the verb %s exposes the name %q, and §14 forbids %q in APIs",
					v.Name, name, noun)
			}
		}
	}
}

// §3.5's README half. §3.5 has two MUSTs and only one was held: the modes are
// pinned in three places, and the sentence that tells an operator what the
// trust boundary IS — that whoever can open the socket is trusted as the user
// — was answerable to nothing. It is the half a human depends on, because a
// mode nobody explains is a number.
func TestTheREADMEDocumentsTheTrustBoundary(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	readme := strings.ToLower(string(body))
	for _, want := range []string{
		"local user account",
		"whoever can open the socket is trusted",
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("README does not say %q. §3.5 MUST document the trust boundary there: "+
				"there are no session keys, tokens or HMAC identities in this revision, and "+
				"the boundary is the local user account", want)
		}
	}
}

// §8.4's no-poll half. A plugin MUST NOT poll Herdr for state an event or the
// timer already covers. The reachable pin is the layer that owns the rule:
// `internal/herdrclient` is the ONE place that talks to `herdr`, so a poll of
// Herdr has to be built there, and every call it makes today is one-shot
// under a timeout. This does not prove the daemon never calls it too often —
// no test does — but it does fail the moment a loop or a timer appears in the
// only package that can reach Herdr at all.
func TestHerdrIsNotPolledForWhatAnEventCovers(t *testing.T) {
	found := false
	for name, body := range goSource(t) {
		if !strings.HasPrefix(name, "internal/herdrclient/") {
			continue
		}
		found = true
		for _, poll := range []string{"time.NewTicker", "time.Tick(", "time.NewTimer", "for {"} {
			if strings.Contains(body, poll) {
				t.Errorf("%s contains %q. §8.4 forbids polling Herdr for what an event or the "+
					"§11.5 timer already covers, and this is the only package that reaches "+
					"Herdr, so a poll of it would be built here", name, poll)
			}
		}
	}
	if !found {
		t.Fatal("no internal/herdrclient source was scanned; this test is reading nothing")
	}
}

// §11.4's two halves — delivery by `agent prompt` is best-effort with no
// receipt, and a plugin that delivers by prompt keeps an authoritative store
// the recipient can read having never seen the prompt. docs/contract-notes.md
// records both as satisfied VACUOUSLY: nothing in this plugin delivers text
// to an agent. That record was itself unheld, and it is the interesting thing
// to hold, because the day a caller appears the two halves stop being vacuous
// and become obligations nobody would be reminded of. `Prompt` stays — it is
// the §11.4-conformant primitive — and this fails when something calls it.
func TestNothingDeliversTextToAnAgent(t *testing.T) {
	for name, body := range goSource(t) {
		if strings.HasPrefix(name, "internal/herdrclient/") {
			continue
		}
		if strings.Contains(body, ".Prompt(") {
			t.Errorf("%s calls Prompt. Delivery by `herdr agent prompt` is best-effort with no "+
				"receipt (§11.4), so the caller owes an authoritative store the recipient can "+
				"read having never seen the prompt. docs/contract-notes.md records §11.4 as "+
				"satisfied vacuously because nothing delivered text; update it with the store "+
				"the new caller relies on before deleting this check", name)
		}
	}
}

// enforcesNoContractSection is the written-down list of test files that hold
// a rule of this REPOSITORY rather than a section of the contract, in the
// shape docs_test.go and annotations_test.go already use: an exception is a
// listed entry with a reason, never a silent skip. §12.2 binds a test to cite
// the section IT ENFORCES, so a test that enforces none owes no citation —
// but which of the two a file is cannot be read off its source, and an
// unlisted uncited file is the case this test exists to catch.
var enforcesNoContractSection = map[string]string{
	"cmd/htask/deps_test.go": "the dependency budget is a non-negotiable in CLAUDE.md and a " +
		"declaration in README; the contract sets no budget",
	"cmd/htask/annotations_test.go": "a comment says what the code is and not what it " +
		"replaced, which is this repository's writing rule",
	"internal/e2e/wait_test.go": "layer 3's own harness helpers and their tests; §12.1 " +
		"names the layer, and what these hold is whether the harness reports honestly",
}

// §12.2: a test MUST cite the contract section it enforces, in its name or a
// comment. `TestContractCitationsResolve` reads the other direction — that a
// citation which EXISTS resolves — so a test file that cited nothing at all
// was answerable to nothing. This holds the obligation at file granularity:
// per function it would fire on helpers and table rows, which are not tests
// enforcing a section, and a rule that has to be silenced everywhere teaches
// people to silence it.
func TestEveryTestFileCitesTheContract(t *testing.T) {
	files := 0
	for name := range enforcesNoContractSection {
		if _, err := os.Stat(filepath.Join("..", "..", name)); err != nil {
			t.Errorf("%s is written down as enforcing no contract section and does not exist; "+
				"an exception nobody can check is worse than none", name)
		}
	}
	for _, name := range trackedFiles(t) {
		if !strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		files++
		if enforcesNoContractSection[name] != "" {
			if citation.Match(body) {
				t.Errorf("%s is written down as enforcing no contract section and now cites "+
					"one; drop its entry from enforcesNoContractSection", name)
			}
			continue
		}
		if !citation.Match(body) {
			t.Errorf("%s cites no contract section. §12.2 requires a test to cite the § it "+
				"enforces in its name or a comment, and a future `kit` conformance suite "+
				"asserts every MUST is cited by at least one test in each plugin", name)
		}
	}
	if files < 10 {
		t.Fatalf("scanned %d test files; the file list is reading nothing", files)
	}
}
