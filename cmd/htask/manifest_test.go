package main_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/husniadil/herdr-tasks/internal/daemon"
	"github.com/husniadil/herdr-tasks/internal/verbs"
)

// commandLine matches one manifest argv: `command = ["./bin/htask", "sweep"]`.
var commandLine = regexp.MustCompile(`(?m)^command\s*=\s*\[\s*"([^"]*)"`)

// Herdr resolves a plugin command's argv0 against the plugin root when it is
// relative AND contains a separator, and PATH-searches it otherwise. Writing
// the leading `./` says which of the two is meant, at the one place a reader
// looks. A path that means "in this plugin" and does not say so reads as a
// PATH lookup, and the failure — a pane that will not spawn, a startup hook
// that silently does not run — surfaces in Herdr, not here.
func TestManifestCommandsInThePluginRootSayTheyAre(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "herdr-plugin.toml"))
	if err != nil {
		t.Fatalf("read the manifest: %v", err)
	}
	matches := commandLine.FindAllStringSubmatch(string(body), -1)
	if len(matches) == 0 {
		t.Fatal("the manifest declares no commands; this test would prove nothing")
	}
	for _, m := range matches {
		argv0 := m[1]
		if !strings.Contains(argv0, "/") {
			// No separator: a PATH lookup, deliberately (`go`, `sh`).
			continue
		}
		if !strings.HasPrefix(argv0, "./") {
			t.Errorf("manifest command %q is a path in the plugin root: write it as %q", argv0, "./"+argv0)
		}
	}
}

// Every command the manifest points at must actually be there — a manifest
// entry naming a script that was renamed is a pane that fails to open.
func TestManifestCommandsExist(t *testing.T) {
	root := filepath.Join("..", "..")
	body, err := os.ReadFile(filepath.Join(root, "herdr-plugin.toml"))
	if err != nil {
		t.Fatalf("read the manifest: %v", err)
	}
	built := buildArtifact(t, string(body))
	for _, m := range commandLine.FindAllStringSubmatch(string(body), -1) {
		argv0 := m[1]
		if !strings.Contains(argv0, "/") {
			continue
		}
		// The artifact the manifest's own [[build]] step declares producing is
		// absent from a fresh clone until that step runs, which is not a
		// broken manifest. Read from [[build]] rather than matched by name:
		// the name it was matched by outlived the binary it named, and the
		// only place that could not happen is the manifest itself.
		if argv0 == built {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, argv0)); err != nil {
			t.Errorf("manifest command %q is not in the plugin root: %v", argv0, err)
		}
	}
}

// buildCommand matches the `[[build]]` section's whole command array, so the
// artifact can be read out of it.
var buildCommand = regexp.MustCompile(`(?ms)^\[\[build\]\].*?^command\s*=\s*\[([^\]]*)\]`)

// buildArtifact is the file the manifest's [[build]] step says it writes: the
// element after `-o` in its command. Failing loudly rather than returning ""
// matters — an empty answer would exempt nothing and the caller would report
// the artifact as a missing file, which reads as a broken manifest.
func buildArtifact(t *testing.T, manifest string) string {
	t.Helper()
	m := buildCommand.FindStringSubmatch(manifest)
	if m == nil {
		t.Fatal("the manifest declares no [[build]] command, so nothing says what a fresh clone still has to build")
	}
	parts := []string{}
	for _, raw := range strings.Split(m[1], ",") {
		if v := strings.Trim(strings.TrimSpace(raw), `"`); v != "" {
			parts = append(parts, v)
		}
	}
	for i, p := range parts {
		if p == "-o" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	t.Fatalf("the [[build]] command %v names no -o output, so what it produces cannot be read from it", parts)
	return ""
}

// eventBlock matches one `[[events]]` section's `on` and `command` argv0.
var eventBlock = regexp.MustCompile(`(?m)^\[\[events\]\]\s*\non\s*=\s*"([^"]*)"\s*\ncommand\s*=\s*\[\s*"([^"]*)"`)

// §8.4: a pane that dies gives its work back at once instead of waiting out
// the lease. Herdr validates a hook's `on` against its dot-spelled names, so
// the underscore spelling the event schema uses would be rejected at load —
// the manifest would not fail here, it would fail in Herdr.
func TestManifestReactsToAPaneGoingAway(t *testing.T) {
	root := filepath.Join("..", "..")
	body, err := os.ReadFile(filepath.Join(root, "herdr-plugin.toml"))
	if err != nil {
		t.Fatalf("read the manifest: %v", err)
	}
	hooks := map[string]string{}
	for _, m := range eventBlock.FindAllStringSubmatch(string(body), -1) {
		hooks[m[1]] = m[2]
	}
	for _, want := range []string{"pane.closed", "pane.exited"} {
		argv0, ok := hooks[want]
		if !ok {
			t.Fatalf("no [[events]] hook on %q; the manifest has %v", want, hooks)
		}
		// Herdr substitutes nothing into an argv: the ids arrive only in the
		// environment, so a command that names the variable gets it literally.
		if strings.Contains(argv0, "$") {
			t.Errorf("hook on %q runs %q, but Herdr does not substitute argv — read the environment in a script", want, argv0)
		}
		info, err := os.Stat(filepath.Join(root, argv0))
		if err != nil {
			t.Fatalf("hook on %q runs %q, which is not there: %v", want, argv0, err)
		}
		if info.Mode()&0o100 == 0 {
			t.Errorf("hook on %q runs %q, which is not executable (%s)", want, argv0, info.Mode())
		}
	}
}

// actionBlock matches one `[[actions]]` section's id and command argv0.
var actionBlock = regexp.MustCompile(`(?ms)^\[\[actions\]\]\nid = "([^"]*)".*?^command = \[\s*"([^"]*)"`)

// §11.6: the popups are the human surface, and until now the only way to
// reach one was the plugin menu. Herdr binds keys to plugin ACTIONS, so an
// action per popup is what makes a keybinding possible at all.
func TestManifestOffersAnActionPerPopup(t *testing.T) {
	root := filepath.Join("..", "..")
	body, err := os.ReadFile(filepath.Join(root, "herdr-plugin.toml"))
	if err != nil {
		t.Fatalf("read the manifest: %v", err)
	}
	actions := map[string]string{}
	for _, m := range actionBlock.FindAllStringSubmatch(string(body), -1) {
		actions[m[1]] = m[2]
	}
	panes := regexp.MustCompile(`(?m)^\[\[panes\]\]\nid = "([^"]*)"`).FindAllStringSubmatch(string(body), -1)
	if len(panes) == 0 {
		t.Fatal("the manifest declares no panes; this test would prove nothing")
	}
	for _, p := range panes {
		id := p[1]
		argv0, ok := actions[id]
		if !ok {
			t.Fatalf("pane %q has no [[actions]] entry of the same id, so no key can reach it; actions are %v", id, actions)
		}
		if !strings.HasPrefix(argv0, "./") {
			t.Errorf("action %q runs %q; a path in the plugin root is written %q", id, argv0, "./"+argv0)
		}
		info, err := os.Stat(filepath.Join(root, argv0))
		if err != nil {
			t.Fatalf("action %q runs %q, which is not there: %v", id, argv0, err)
		}
		if info.Mode()&0o100 == 0 {
			t.Errorf("action %q runs %q, which is not executable (%s)", id, argv0, info.Mode())
		}
	}
}

// Herdr does not load a plugin's keybindings: the operator copies them into
// their own config. A block that names an action that does not exist, or
// spells the qualified id wrong, fails in their config and not in ours — so
// it is checked here, against the manifest it refers to.
func TestManifestKeybindingsFileIsCopyReady(t *testing.T) {
	root := filepath.Join("..", "..")
	body, err := os.ReadFile(filepath.Join(root, "keybindings.toml"))
	if err != nil {
		t.Fatalf("read keybindings.toml: %v", err)
	}
	text := string(body)
	blocks := regexp.MustCompile(`(?ms)^\[\[keys\.command\]\](.*?)(?:\n\n|\z)`).FindAllStringSubmatch(text, -1)
	if len(blocks) != 2 {
		t.Fatalf("want two [[keys.command]] blocks, got %d", len(blocks))
	}
	manifest, err := os.ReadFile(filepath.Join(root, "herdr-plugin.toml"))
	if err != nil {
		t.Fatalf("read the manifest: %v", err)
	}
	pluginID := regexp.MustCompile(`(?m)^id = "([^"]*)"`).FindStringSubmatch(string(manifest))[1]

	want := map[string]bool{pluginID + ".board": false, pluginID + ".notes": false}
	for _, b := range blocks {
		block := b[1]
		if !strings.Contains(block, `type = "plugin_action"`) {
			t.Errorf("a block is not a plugin_action, so Herdr would run it as a shell command: %s", block)
		}
		m := regexp.MustCompile(`command = "([^"]*)"`).FindStringSubmatch(block)
		if m == nil {
			t.Errorf("a block names no command: %s", block)
			continue
		}
		if _, ok := want[m[1]]; !ok {
			t.Errorf("command %q is not one of the two qualified action ids", m[1])
			continue
		}
		want[m[1]] = true
		if !regexp.MustCompile(`key = "[^"]+"`).MatchString(block) {
			t.Errorf("the block for %q binds no key: %s", m[1], block)
		}
	}
	for id, seen := range want {
		if !seen {
			t.Errorf("no [[keys.command]] block invokes %q", id)
		}
	}
}

// §13.1: the manifest id is the REPOSITORY name and does not follow the
// binary. Three names live in this plugin and only one of them moved — the
// binary is htask, the plugin id is herdr-tasks, the short name behind the
// socket, the store and TASKS_* is tasks.
func TestTheManifestIdentityDidNotFollowTheBinary(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "herdr-plugin.toml"))
	if err != nil {
		t.Fatalf("read the manifest: %v", err)
	}
	manifest := string(body)
	if !strings.Contains(manifest, `id = "herdr-tasks"`) {
		t.Errorf("the manifest id is not herdr-tasks:\n%s", firstLines(manifest, 12))
	}
	if strings.Contains(manifest, `id = "htask"`) {
		t.Error("the manifest id followed the binary")
	}
	// And every command it runs names the binary that exists.
	for _, argv := range commandLine.FindAllStringSubmatch(manifest, -1) {
		if strings.Contains(argv[0], `"./bin/ht"`) {
			t.Errorf("a manifest command runs %s, which is not this plugin's binary", argv[0])
		}
	}
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

// §2.4: "The Herdr manifest MUST declare `[[startup]]` that starts the daemon
// and exits, and `[[actions]]` `stop` and `restart`." The audit behind task 86
// renamed the `[[startup]]` table and every manifest test stayed green: the
// existing ones read the argv0 of whatever command blocks they find, so a
// manifest with no startup block at all still satisfied them. Herdr has no
// shutdown hook, which is why `stop` is named in the contract rather than left
// to taste — a plugin without it cannot be turned off.
func TestManifestDeclaresStartupAndTheLifecycleActions(t *testing.T) {
	root := filepath.Join("..", "..")
	body, err := os.ReadFile(filepath.Join(root, "herdr-plugin.toml"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	manifest := string(body)
	if !strings.Contains(manifest, "[[startup]]") {
		t.Error("the manifest declares no [[startup]]; nothing starts the daemon (§2.4)")
	}
	ids := map[string]bool{}
	for _, m := range actionBlock.FindAllStringSubmatch(manifest, -1) {
		ids[m[1]] = true
	}
	if len(ids) == 0 {
		t.Fatal("no [[actions]] blocks parsed; the manifest is not being read")
	}
	for _, want := range []string{"stop", "restart"} {
		if !ids[want] {
			t.Errorf("the manifest has no [[actions]] %q; Herdr has no shutdown hook, so it is "+
				"the only way to turn this plugin off (§2.4)", want)
		}
	}
}

// §13.3: the manifest's version is what Herdr shows for this plugin and
// `htask version` is what the binary says it is. Two numbers for one build is
// one of them being wrong, and the manifest is the copy nothing in the code
// path reads — so it drifted to 0.3.0 while the binary said 0.6.0.
func TestManifestVersionIsTheBinaryVersion(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "herdr-plugin.toml"))
	if err != nil {
		t.Fatalf("read the manifest: %v", err)
	}
	m := regexp.MustCompile(`(?m)^version\s*=\s*"([^"]*)"`).FindStringSubmatch(string(body))
	if m == nil {
		t.Fatal("the manifest declares no version")
	}
	if m[1] != daemon.Version {
		t.Fatalf("the manifest says %q and the binary says %q", m[1], daemon.Version)
	}
}

// §9.4: the README carries the gated verb list so a future policy plugin can
// name them. A list written by hand beside a registry is a list that goes
// stale — tasks.amend was gated and the block never learned about it.
func TestReadmeListsEveryGatedVerb(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatalf("read the README: %v", err)
	}
	block := regexp.MustCompile("(?s)The gated verbs, for a future policy plugin to name:\n\n```\n(.*?)```").
		FindStringSubmatch(string(body))
	if block == nil {
		t.Fatal("the README no longer carries the gated verb block")
	}
	listed := map[string]bool{}
	for _, f := range strings.Fields(block[1]) {
		listed[f] = true
	}
	for _, v := range verbs.GatedVerbs() {
		if !listed[v] {
			t.Errorf("the README's gated verb list is missing %s", v)
		}
		delete(listed, v)
	}
	for v := range listed {
		t.Errorf("the README lists %s, which the registry does not gate", v)
	}
}
