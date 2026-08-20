// Package project resolves the unit of data scoping (§4): the git common
// dir's parent, so every worktree of a repository shares one project, and a
// plain directory is its own project by canonical path.
package project

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Options is the §4.2 resolution order, as data.
type Options struct {
	// Explicit is --project, which wins.
	Explicit string
	// Cwd is the caller's working directory, the last resort.
	Cwd string
}

// Resolve answers §4.2: explicit --project, then HERDR_PLUGIN_CONTEXT_JSON's
// focused pane cwd or workspace cwd, then the caller's working directory.
func Resolve(o Options) (string, error) {
	dir := o.Explicit
	if dir == "" {
		dir = fromHerdrContext()
	}
	if dir == "" {
		dir = o.Cwd
	}
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		dir = wd
	}
	return canonical(dir)
}

// herdrContext is the shape of HERDR_PLUGIN_CONTEXT_JSON this plugin reads.
// Herdr owns the full shape; the plugin reads the two cwds §4.2 names and
// ignores the rest.
type herdrContext struct {
	FocusedPane struct {
		Cwd string `json:"cwd"`
	} `json:"focused_pane"`
	Workspace struct {
		Cwd string `json:"cwd"`
	} `json:"workspace"`
}

func fromHerdrContext() string {
	raw := os.Getenv("HERDR_PLUGIN_CONTEXT_JSON")
	if raw == "" {
		return ""
	}
	var c herdrContext
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return ""
	}
	if c.FocusedPane.Cwd != "" {
		return c.FocusedPane.Cwd
	}
	return c.Workspace.Cwd
}

// canonical turns a directory into the project key: the parent of the git
// common dir when there is one, else the directory itself. Symlinks are
// resolved either way (§4.1).
func canonical(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	if root, ok := gitCommonRoot(abs); ok {
		return root, nil
	}
	return abs, nil
}

// gitCommonRoot runs the one git invocation §4.1 specifies. A directory that
// is not a repository, or a host with no git, simply is not a repository here
// — that is the documented fallback, not a failure.
func gitCommonRoot(dir string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--path-format=absolute", "--git-common-dir")
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", false
	}
	common := strings.TrimSpace(out.String())
	if common == "" {
		return "", false
	}
	root := filepath.Dir(common)
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	return root, true
}

// DisplayName is the basename a human reads. It is never a key (§4.1).
func DisplayName(project string) string { return filepath.Base(project) }
