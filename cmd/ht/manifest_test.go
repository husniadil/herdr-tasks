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
