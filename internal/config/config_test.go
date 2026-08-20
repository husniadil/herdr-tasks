package config

import (
	"os"
	"path/filepath"
	"testing"
)

// §5.1 as amended (docs/contract-notes.md): TASKS_STATE_DIR and
// TASKS_CONFIG_DIR win; the XDG defaults are the fallback. Tests set both, so
// nothing reaches the operator's dirs (§12.3).
func TestDirsPreferThePluginOwnedEnv(t *testing.T) {
	state, conf := t.TempDir(), t.TempDir()
	t.Setenv("TASKS_STATE_DIR", state)
	t.Setenv("TASKS_CONFIG_DIR", conf)
	if got := StateDir(); got != state {
		t.Fatalf("StateDir = %q, want %q", got, state)
	}
	if got := ConfigDir(); got != conf {
		t.Fatalf("ConfigDir = %q, want %q", got, conf)
	}
}

// §5.1 as amended: the store is the same one whoever spawned the process, so a
// herdr-injected dir must not move it. This is the whole point of the change:
// a popup and an agent pane must open the same database.
func TestHerdrInjectedDirsAreIgnored(t *testing.T) {
	base := t.TempDir()
	t.Setenv("HERDR_PLUGIN_STATE_DIR", filepath.Join(base, "herdr-state"))
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", filepath.Join(base, "herdr-config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(base, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(base, "config"))
	if got, want := StateDir(), filepath.Join(base, "state", Name); got != want {
		t.Fatalf("StateDir = %q, want %q", got, want)
	}
	if got, want := ConfigDir(), filepath.Join(base, "config", Name); got != want {
		t.Fatalf("ConfigDir = %q, want %q", got, want)
	}
}

func TestDirsFallBackToXDG(t *testing.T) {
	t.Setenv("TASKS_STATE_DIR", "")
	t.Setenv("TASKS_CONFIG_DIR", "")
	base := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(base, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(base, "config"))
	if got, want := StateDir(), filepath.Join(base, "state", Name); got != want {
		t.Fatalf("StateDir = %q, want %q", got, want)
	}
	if got, want := ConfigDir(), filepath.Join(base, "config", Name); got != want {
		t.Fatalf("ConfigDir = %q, want %q", got, want)
	}
}

// §3.5: the state dir is 0700 and the socket 0600; the boundary is the local
// user account and nothing else.
func TestEnsureStateDirIsPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested")
	t.Setenv("TASKS_STATE_DIR", dir)
	if err := EnsureStateDir(); err != nil {
		t.Fatalf("EnsureStateDir: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("mode = %o, want 700", perm)
	}
}

func TestSocketAndDBPaths(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TASKS_STATE_DIR", dir)
	if got, want := SocketPath(), filepath.Join(dir, "tasks.sock"); got != want {
		t.Fatalf("SocketPath = %q, want %q", got, want)
	}
	if got, want := DBPath(), filepath.Join(dir, "tasks.db"); got != want {
		t.Fatalf("DBPath = %q, want %q", got, want)
	}
}

// §10.1: config is TOML, and TASKS_ environment variables override it.
func TestLoadReadsTOMLAndEnvOverrides(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TASKS_CONFIG_DIR", dir)
	body := `
# the policy gate (§9) and the event hook (§8.3)
lease_seconds = 300
gate_command = ["/usr/local/bin/policy", "check"]
on_event = ["/usr/local/bin/notify"]
sweep_seconds = 30
`
	if err := os.WriteFile(filepath.Join(dir, "tasks.toml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.LeaseSeconds != 300 || c.SweepSeconds != 30 {
		t.Fatalf("config = %+v", c)
	}
	if len(c.GateCommand) != 2 || c.GateCommand[0] != "/usr/local/bin/policy" {
		t.Fatalf("gate_command = %v", c.GateCommand)
	}
	if len(c.OnEvent) != 1 {
		t.Fatalf("on_event = %v", c.OnEvent)
	}
	t.Setenv("TASKS_LEASE_SECONDS", "900")
	c, err = Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.LeaseSeconds != 900 {
		t.Fatalf("TASKS_LEASE_SECONDS did not override: %d", c.LeaseSeconds)
	}
}

// A missing config file is the unconfigured default, not an error: §9.2 says
// unconfigured allows, and §10.3 says doctor never fails.
func TestLoadWithoutFileIsDefaults(t *testing.T) {
	t.Setenv("TASKS_CONFIG_DIR", t.TempDir())
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.LeaseSeconds != DefaultLeaseSeconds || len(c.GateCommand) != 0 {
		t.Fatalf("config = %+v", c)
	}
}

func TestMalformedConfigFailsLoud(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TASKS_CONFIG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "tasks.toml"), []byte("lease_seconds = \n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("a config we cannot parse must fail loud, not fall back silently")
	}
}

// §3.5: a state dir that already exists more widely is tightened, not
// accepted.
func TestEnsureStateDirTightensAnExistingDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "loose")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Setenv("TASKS_STATE_DIR", dir)
	if err := EnsureStateDir(); err != nil {
		t.Fatalf("EnsureStateDir: %v", err)
	}
	info, _ := os.Stat(dir)
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("mode = %o, want 700", perm)
	}
}

// §5.1 as amended: the herdr-injected dir is ignored for resolution, but an
// orphan store left there is still worth naming, so OrphanStoreDirs reports
// the places a second tasks.db could be hiding.
func TestOrphanStoreDirsNamesTheHerdrPathAndNotTheOneInUse(t *testing.T) {
	base := t.TempDir()
	t.Setenv("TASKS_STATE_DIR", filepath.Join(base, "in-use"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(base, "xdg"))
	injected := filepath.Join(base, "herdr-plugin-state")
	t.Setenv("HERDR_PLUGIN_STATE_DIR", injected)

	got := OrphanStoreDirs()
	if !contains(got, injected) {
		t.Fatalf("the injected dir must be a candidate: %v", got)
	}
	// The conventional herdr layout, for a caller herdr did not spawn.
	conventional := filepath.Join(base, "xdg", "herdr", "plugins", PluginID)
	if !contains(got, conventional) {
		t.Fatalf("the conventional herdr path must be a candidate: %v", got)
	}
	if contains(got, StateDir()) {
		t.Fatalf("the store in use is not an orphan: %v", got)
	}
}

// The store in use must never be reported as its own orphan, whatever route
// resolved it — otherwise doctor tells the operator to delete live data.
func TestOrphanStoreDirsExcludesTheStoreInUse(t *testing.T) {
	base := t.TempDir()
	injected := filepath.Join(base, "same")
	t.Setenv("TASKS_STATE_DIR", injected)
	t.Setenv("HERDR_PLUGIN_STATE_DIR", injected)
	if got := OrphanStoreDirs(); contains(got, injected) {
		t.Fatalf("the dir in use was reported as an orphan: %v", got)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
