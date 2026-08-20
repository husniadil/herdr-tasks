package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// §4.1: the project is the git common dir's parent, so every worktree of a
// repository shares one project.
func TestWorktreesShareOneProject(t *testing.T) {
	if testing.Short() {
		t.Skip("needs git")
	}
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	mustRun(t, root, "git", "init", "-q", "repo")
	mustRun(t, repo, "git", "config", "user.email", "t@example.test")
	mustRun(t, repo, "git", "config", "user.name", "t")
	mustRun(t, repo, "git", "commit", "-q", "--allow-empty", "-m", "root")
	sub := filepath.Join(repo, "internal", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	wt := filepath.Join(root, "wt")
	mustRun(t, repo, "git", "worktree", "add", "-q", "-b", "side", wt)

	fromRoot, err := Resolve(Options{Cwd: repo})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	fromSub, err := Resolve(Options{Cwd: sub})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	fromWorktree, err := Resolve(Options{Cwd: wt})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if fromRoot != fromSub || fromRoot != fromWorktree {
		t.Fatalf("worktrees disagree: %q, %q, %q", fromRoot, fromSub, fromWorktree)
	}
}

// §4.1: a directory that is not a repository is its own project by canonical
// path, with symlinks resolved.
func TestNonRepoIsItsOwnProject(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	viaReal, err := Resolve(Options{Cwd: real})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	viaLink, err := Resolve(Options{Cwd: link})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if viaReal != viaLink {
		t.Fatalf("symlink not resolved: %q != %q", viaReal, viaLink)
	}
}

// §4.2: explicit --project wins over everything.
func TestExplicitProjectWins(t *testing.T) {
	dir := t.TempDir()
	got, err := Resolve(Options{Explicit: dir, Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want, _ := filepath.EvalSymlinks(dir)
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// §4.2: then HERDR_PLUGIN_CONTEXT_JSON's focused pane cwd, when the caller is
// a Herdr plugin command. The document is Herdr's PluginInvocationContext: one
// flat object of snake_case keys, not a nested one. A popup pane gets no
// HERDR_PANE_ID and runs with the plugin root as its working directory, so
// this env var is the ONLY thing that tells the board which project it is
// looking at.
func TestHerdrPluginContextCwd(t *testing.T) {
	pane := t.TempDir()
	t.Setenv("HERDR_PLUGIN_CONTEXT_JSON",
		`{"workspace_id":"wM","workspace_cwd":"/nope","tab_id":"wM:t1","focused_pane_id":"wM:p1","focused_pane_cwd":`+quote(pane)+`}`)
	got, err := Resolve(Options{Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want, _ := filepath.EvalSymlinks(pane)
	if got != want {
		t.Fatalf("got %q, want the focused pane's cwd %q", got, want)
	}
}

func TestHerdrPluginContextFallsBackToWorkspaceCwd(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", `{"workspace_id":"wM","workspace_cwd":`+quote(ws)+`}`)
	got, err := Resolve(Options{Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want, _ := filepath.EvalSymlinks(ws)
	if got != want {
		t.Fatalf("got %q, want the workspace cwd %q", got, want)
	}
}

// §4.2: --project still wins over the context Herdr supplied.
func TestExplicitProjectBeatsTheHerdrContext(t *testing.T) {
	explicit := t.TempDir()
	t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", `{"focused_pane_cwd":`+quote(t.TempDir())+`}`)
	got, err := Resolve(Options{Explicit: explicit, Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want, _ := filepath.EvalSymlinks(explicit)
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// A context with no cwd in it at all — Herdr omits absent fields — leaves the
// working directory as the last resort rather than resolving to nothing.
func TestHerdrContextWithoutACwdFallsBackToTheWorkingDirectory(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", `{"workspace_id":"wM","invocation_source":"pane"}`)
	got, err := Resolve(Options{Cwd: cwd})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want, _ := filepath.EvalSymlinks(cwd)
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// §4.1: the display name is a convenience, never the key.
func TestDisplayNameIsBasename(t *testing.T) {
	if got := DisplayName("/a/b/herdr-tasks"); got != "herdr-tasks" {
		t.Fatalf("DisplayName = %q", got)
	}
}

func quote(s string) string { return `"` + s + `"` }

func mustRun(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}
