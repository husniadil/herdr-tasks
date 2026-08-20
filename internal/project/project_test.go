package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// gitWorld builds the repository shapes §4.1 has to answer for, with real git
// rather than a mock — the failure this covers is git's own output, so a mock
// would only restate the bug. The operator's git config is never read: global
// is a file this test wrote and system is turned off (§12.3).
func gitWorld(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("needs git")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("no git on PATH: %v", err)
	}
	root := t.TempDir()
	cfg := filepath.Join(root, "gitconfig")
	if err := os.WriteFile(cfg, []byte(
		"[user]\n\tname = t\n\temail = t@example.test\n[protocol \"file\"]\n\tallow = always\n"), 0o600); err != nil {
		t.Fatalf("write git config: %v", err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	for _, name := range []string{"lib", "tools"} {
		dir := filepath.Join(root, name)
		mustRun(t, root, "git", "init", "-q", name)
		mustRun(t, dir, "git", "commit", "-q", "--allow-empty", "-m", name)
	}
	super := filepath.Join(root, "super")
	mustRun(t, root, "git", "init", "-q", "super")
	mustRun(t, super, "git", "commit", "-q", "--allow-empty", "-m", "super")
	mustRun(t, super, "git", "submodule", "add", "-q", filepath.Join(root, "lib"), "sub-a")
	mustRun(t, super, "git", "submodule", "add", "-q", filepath.Join(root, "tools"), "sub-b")
	mustRun(t, super, "git", "commit", "-q", "-m", "submodules")
	mustRun(t, super, "git", "worktree", "add", "-q", "-b", "side", filepath.Join(root, "wt"))
	if err := os.MkdirAll(filepath.Join(root, "gitdirs"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mustRun(t, root, "git", "init", "-q", "--separate-git-dir="+filepath.Join(root, "gitdirs", "sgd"), "sgd")
	mustRun(t, root, "git", "init", "-q", "--bare", "bare.git")
	return root
}

func resolved(t *testing.T, dir string) string {
	t.Helper()
	got, err := Resolve(Options{Cwd: dir})
	if err != nil {
		t.Fatalf("Resolve(%s): %v", dir, err)
	}
	return got
}

func canonicalOf(t *testing.T, dir string) string {
	t.Helper()
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

// §4.1: a submodule is a repository, and its project is its own working tree.
// git reports a submodule's common dir as <super>/.git/modules/<name>, so
// taking that dir's parent made every submodule of one superproject share the
// key <super>/.git/modules — a path inside git's internals, and one neither
// the superproject nor the submodule can reach from where it stands.
func TestASubmoduleIsItsOwnProject(t *testing.T) {
	root := gitWorld(t)
	a := filepath.Join(root, "super", "sub-a")
	b := filepath.Join(root, "super", "sub-b")

	if got, want := resolved(t, a), canonicalOf(t, a); got != want {
		t.Errorf("submodule resolves to %q, want its own working tree %q", got, want)
	}
	if got, want := resolved(t, b), canonicalOf(t, b); got != want {
		t.Errorf("submodule resolves to %q, want its own working tree %q", got, want)
	}
	if resolved(t, a) == resolved(t, b) {
		t.Errorf("two different submodules share the project %q", resolved(t, a))
	}
	if got := resolved(t, a); strings.Contains(got, string(filepath.Separator)+".git") {
		t.Errorf("the project is a path inside git's internals: %q", got)
	}
	// The superproject is still its own project, not the submodule's.
	if super, sub := resolved(t, filepath.Join(root, "super")), resolved(t, a); super == sub {
		t.Errorf("the superproject and its submodule share the project %q", super)
	}
}

// §4.1: the reason --git-common-dir is used at all — every worktree of one
// repository is one project. This must not change.
func TestALinkedWorktreeStillSharesTheSuperprojectsProject(t *testing.T) {
	root := gitWorld(t)
	main := resolved(t, filepath.Join(root, "super"))
	if got := resolved(t, filepath.Join(root, "wt")); got != main {
		t.Errorf("the linked worktree resolves to %q, want the superproject %q", got, main)
	}
}

// §4.1: --separate-git-dir puts the git dir anywhere, so its parent has
// nothing to do with the working tree — and if git dirs are kept together,
// which is the usual reason to use the flag, every such clone collapses into
// one project.
func TestASeparateGitDirResolvesToTheWorkingTree(t *testing.T) {
	root := gitWorld(t)
	sgd := filepath.Join(root, "sgd")
	if got, want := resolved(t, sgd), canonicalOf(t, sgd); got != want {
		t.Errorf("the clone resolves to %q, want its working tree %q", got, want)
	}
}

// §4.1: a bare repository has no working tree at all. It must not fail and
// must not become a git-internals path; it is simply not a repository here,
// which is the documented fallback.
func TestABareRepoFallsBackToTheDirectoryItself(t *testing.T) {
	root := gitWorld(t)
	bare := filepath.Join(root, "bare.git")
	got := resolved(t, bare)
	if got != canonicalOf(t, bare) {
		t.Errorf("the bare repo resolves to %q, want the directory itself %q", got, canonicalOf(t, bare))
	}
}

// §4.1: whatever the shape, the answer is absolute and symlink-resolved.
func TestEveryShapeIsCanonical(t *testing.T) {
	root := gitWorld(t)
	for _, dir := range []string{
		filepath.Join(root, "super"),
		filepath.Join(root, "super", "sub-a"),
		filepath.Join(root, "wt"),
		filepath.Join(root, "sgd"),
		filepath.Join(root, "bare.git"),
	} {
		got := resolved(t, dir)
		if !filepath.IsAbs(got) {
			t.Errorf("%s resolves to a relative path %q", dir, got)
		}
		if real, err := filepath.EvalSymlinks(got); err == nil && real != got {
			t.Errorf("%s resolves to %q, which is not symlink-resolved (%q)", dir, got, real)
		}
	}
}
