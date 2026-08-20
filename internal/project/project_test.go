package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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

// §4.2: HERDR_PLUGIN_CONTEXT_JSON is the ONLY thing that tells a popup which
// project it is looking at — a plugin pane's working directory is the
// server's, not the focused pane's. A document that cannot be read therefore
// silently scoped the whole board to somewhere else, which is the split-brain
// empty-board incident this plugin has already lived through once.
func TestAnUnreadableHerdrContextWarnsAndSaysWhatItUsed(t *testing.T) {
	contextWarned = sync.Once{}
	cwd := t.TempDir()
	t.Setenv(EnvContext, `{"focused_pane_cwd": not-json-at-all`)
	var warn strings.Builder

	got, err := Resolve(Options{Cwd: cwd, Warn: &warn})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// The answer is still the documented fallback, and it is asserted rather
	// than assumed: a warning about a project it did not use would be worse
	// than no warning.
	if want := canonicalOf(t, cwd); got != want {
		t.Fatalf("resolved %q, want the working directory %q", got, want)
	}
	line := warn.String()
	if !strings.Contains(line, EnvContext) {
		t.Errorf("the warning does not name the variable, so it cannot be grepped: %q", line)
	}
	if !strings.Contains(line, got) {
		t.Errorf("the warning does not say which project was used: %q", line)
	}
	if !strings.Contains(line, "could not be read") {
		t.Errorf("the warning does not say what went wrong: %q", line)
	}
	if strings.Count(line, "\n") != 1 {
		t.Errorf("the warning is not one line: %q", line)
	}
}

// The value may be a long document and it is not the point; printing it buries
// the warning and can put a path the operator did not ask to see on a terminal.
func TestTheWarningDoesNotEchoTheValue(t *testing.T) {
	contextWarned = sync.Once{}
	t.Setenv(EnvContext, `{"focused_pane_cwd": "/private/somewhere-distinctive-9f3c", `)
	var warn strings.Builder
	if _, err := Resolve(Options{Cwd: t.TempDir(), Warn: &warn}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if strings.Contains(warn.String(), "somewhere-distinctive-9f3c") {
		t.Fatalf("the warning echoed the value: %q", warn.String())
	}
}

// §4.2: the two benign cases stay silent, which is what keeps the warning
// worth reading. An absent variable means this is not a plugin pane; a
// well-formed document with neither cwd key is Herdr omitting what it has no
// answer for, which its own shape allows.
func TestABenignHerdrContextSaysNothing(t *testing.T) {
	for name, value := range map[string]string{
		"no variable at all":     "",
		"neither cwd key":        `{"workspace_id":"wM","tab_id":"wM:t1"}`,
		"the keys present-empty": `{"workspace_cwd":"","focused_pane_cwd":""}`,
	} {
		contextWarned = sync.Once{}
		if value == "" {
			t.Setenv(EnvContext, "")
		} else {
			t.Setenv(EnvContext, value)
		}
		var warn strings.Builder
		if _, err := Resolve(Options{Cwd: t.TempDir(), Warn: &warn}); err != nil {
			t.Fatalf("%s: Resolve: %v", name, err)
		}
		if warn.String() != "" {
			t.Errorf("%s: said %q, want silence", name, warn.String())
		}
	}
}

// Once per invocation, the way the door/daemon skew warning is: a board that
// repeats it every time it polls is a board nobody reads the warnings on.
func TestTheContextWarningIsSaidOnce(t *testing.T) {
	contextWarned = sync.Once{}
	t.Setenv(EnvContext, `{`)
	var warn strings.Builder
	for i := 0; i < 3; i++ {
		if _, err := Resolve(Options{Cwd: t.TempDir(), Warn: &warn}); err != nil {
			t.Fatalf("Resolve: %v", err)
		}
	}
	if got := strings.Count(warn.String(), "\n"); got != 1 {
		t.Fatalf("the warning was said %d times: %q", got, warn.String())
	}
}

// §4.1: a directory that does not exist YET still has one canonical form.
// EvalSymlinks cannot resolve a path that is not there, and the result was
// simply taken unresolved — so `--project /var/tmp/x` was a different project
// from the same directory once created, and everything filed against the first
// key was stranded the moment it was.
func TestAPathThatDoesNotExistYetGetsTheKeyItWillHave(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// Through the symlinked parent, at a child that is not there yet.
	missing := filepath.Join(link, "not-yet")
	before, err := Resolve(Options{Explicit: missing})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if err := os.Mkdir(filepath.Join(real, "not-yet"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	after, err := Resolve(Options{Explicit: missing})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if before != after {
		t.Fatalf("the same directory is two projects:\n  before it existed: %q\n  after:             %q", before, after)
	}
	if before != filepath.Join(canonicalOf(t, real), "not-yet") {
		t.Fatalf("resolved %q, want the symlink-resolved parent with the child on it", before)
	}
}

// The macOS case the note reproduced: /var is a symlink to /private/var, so a
// not-yet-existing path under it used to key differently from the same path
// once created.
func TestASymlinkedSystemDirIsResolvedBeforeItExists(t *testing.T) {
	if _, err := os.Lstat("/var"); err != nil {
		t.Skip("no /var here")
	}
	resolvedVar, err := filepath.EvalSymlinks("/var")
	if err != nil {
		t.Skipf("cannot resolve /var: %v", err)
	}
	if resolvedVar == "/var" {
		t.Skip("/var is not a symlink on this host")
	}
	got, err := Resolve(Options{Explicit: "/var/tmp/ht-not-created-9f3c"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if want := filepath.Join(resolvedVar, "tmp", "ht-not-created-9f3c"); got != want {
		t.Fatalf("resolved %q, want %q", got, want)
	}
}
