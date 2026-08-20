package main_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// commandLine matches one manifest argv: `command = ["./bin/ht", "sweep"]`.
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
	for _, m := range commandLine.FindAllStringSubmatch(string(body), -1) {
		argv0 := m[1]
		if !strings.Contains(argv0, "/") {
			continue
		}
		// bin/ht is built by the manifest's own [[build]] step, so its absence
		// in a fresh checkout is not a broken manifest.
		if strings.HasSuffix(argv0, "/ht") {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, argv0)); err != nil {
			t.Errorf("manifest command %q is not in the plugin root: %v", argv0, err)
		}
	}
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
