package main_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

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
	for _, name := range []string{"README.md", filepath.Join("skills", "tasks", "SKILL.md")} {
		body, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		out[name] = string(body)
	}
	return out
}

// commandLines pulls every `ht …` invocation out of a document's fenced code
// blocks, joining the backslash continuations a long example is wrapped with.
func commandLines(doc string) []string {
	var out []string
	for _, block := range fence.FindAllStringSubmatch(doc, -1) {
		joined := strings.ReplaceAll(block[1], "\\\n", " ")
		for _, line := range strings.Split(joined, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "ht ") || line == "ht" {
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
		if f == "ht" {
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

func checkCommand(t *testing.T, name, line string, cmd []string) {
	t.Helper()
	words, flags := []string{}, []string{}
	{
		for _, f := range cmd {
			switch {
			case strings.HasPrefix(f, "--"):
				flags = append(flags, strings.TrimPrefix(strings.SplitN(f, "=", 2)[0], "--"))
			case strings.HasPrefix(f, "-"):
				t.Errorf("%s: %q uses a short flag; this CLI declares none", name, line)
			default:
				words = append(words, f)
			}
		}
	}
	if len(words) > 0 && notInTheRegistry[words[0]] {
		return
	}
	v, ok := verbFor(words)
	if !ok {
		if len(words) == 0 && len(flags) > 0 {
			return // `ht --help`
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
