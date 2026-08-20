// Package config resolves the plugin's directories and reads its TOML config
// (§5.1, §10). Config never holds a secret: a value that needs one names a
// file path or an environment variable and is dereferenced at use (§10.2).
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Name is the plugin's short name (§13.2). It names the state dir, the socket,
// the database, the config file, and the TASKS_ environment prefix.
const Name = "tasks"

// EnvPrefix is the §10.1 override prefix.
const EnvPrefix = "TASKS_"

// PluginID is the id Herdr knows this plugin by (§13.1), which is also the
// directory Herdr keeps plugin state under. We do not store anything there —
// it is named here only so doctor can spot a store left behind at that path.
const PluginID = "herdr-tasks"

// Defaults. A lease long enough that an agent thinking hard does not lose its
// claim, short enough that a dead pane frees work within the hour.
const (
	DefaultLeaseSeconds = 900
	DefaultSweepSeconds = 60
)

// One plugin, one store. Herdr injects HERDR_PLUGIN_STATE_DIR and
// HERDR_PLUGIN_CONFIG_DIR into what IT spawns — startup, actions, popup panes —
// and injects neither into a managed pane, where the agents and MCP servers
// run. Honouring them, which is what §5.1 and §10.1 say to do, therefore gives
// one plugin two stores that never see each other's rows. We deliberately do
// not read them; docs/contract-notes.md records the divergence.
//
// TASKS_STATE_DIR and TASKS_CONFIG_DIR are the plugin-owned overrides (§10.1's
// TASKS_ prefix). They are how tests isolate, and how an operator asks for a
// second store on purpose rather than by accident.

// StateDir is TASKS_STATE_DIR, else ${XDG_STATE_HOME:-~/.local/state}/tasks.
func StateDir() string {
	return dirFrom(EnvPrefix+"STATE_DIR", "XDG_STATE_HOME", filepath.Join(".local", "state"))
}

// ConfigDir is TASKS_CONFIG_DIR, else ${XDG_CONFIG_HOME:-~/.config}/tasks.
func ConfigDir() string {
	return dirFrom(EnvPrefix+"CONFIG_DIR", "XDG_CONFIG_HOME", ".config")
}

// dirFrom resolves one directory: the plugin's own override, else the XDG base
// with the plugin's name under it, else the same layout under the home dir.
func dirFrom(own, xdg, home string) string {
	if d := os.Getenv(own); d != "" {
		return d
	}
	if d := os.Getenv(xdg); d != "" {
		return filepath.Join(d, Name)
	}
	return filepath.Join(homeDir(), home, Name)
}

// SocketPath is <state_dir>/tasks.sock (§2.2).
func SocketPath() string { return filepath.Join(StateDir(), Name+".sock") }

// LockPath is <state_dir>/tasks.lock, the file whose flock elects the one
// daemon per store (§2.3). It is held for the daemon's lifetime and released
// by the kernel when the process ends, so a crash leaves nothing to clean up.
func LockPath() string { return filepath.Join(StateDir(), Name+".lock") }

// DBPath is <state_dir>/tasks.db (§5.1).
func DBPath() string { return filepath.Join(StateDir(), Name+".db") }

// ConfigPath is <config_dir>/tasks.toml (§10.1).
func ConfigPath() string { return filepath.Join(ConfigDir(), Name+".toml") }

// OrphanStoreDirs lists the directories a second tasks.db could be sitting in
// because an older build resolved the store from Herdr's injected dirs. It is
// detection, never deletion: a database is not something this code removes on
// the operator's behalf. The store actually in use is never listed, so doctor
// can never point at live data.
func OrphanStoreDirs() []string {
	var out []string
	inUse := StateDir()
	add := func(dir string) {
		if dir == "" || dir == inUse {
			return
		}
		for _, seen := range out {
			if seen == dir {
				return
			}
		}
		out = append(out, dir)
	}
	// Where Herdr puts us when Herdr spawns us.
	add(os.Getenv("HERDR_PLUGIN_STATE_DIR"))
	// The same place, worked out from the layout, for a caller Herdr did not
	// spawn — a plain shell has no injected variable to read.
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		base = filepath.Join(homeDir(), ".local", "state")
	}
	add(filepath.Join(base, "herdr", "plugins", PluginID))
	return out
}

// EnsureStateDir creates the state dir with mode 0700 (§3.5). It tightens a
// dir that already exists more widely rather than accepting it: the boundary
// is the local user account, and a state dir another account can read is not
// that boundary.
func EnsureStateDir() error {
	dir := StateDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.Chmod(dir, 0o700)
}

func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

// Config is the whole of what the plugin reads at daemon start and on SIGHUP.
type Config struct {
	// LeaseSeconds is how long a claim holds before the sweep may take it back.
	LeaseSeconds int64 `json:"lease_seconds"`
	// SweepSeconds is the bounded timer of §11.5.
	SweepSeconds int64 `json:"sweep_seconds"`
	// GateCommand is the policy gate of §9.2. Empty means unconfigured, which
	// means allow.
	GateCommand []string `json:"gate_command"`
	// OnEvent is the §8.3 hook, run detached with all three stdio closed.
	OnEvent []string `json:"on_event"`
	// Path is where this came from, for doctor.
	Path string `json:"path"`
	// Present says whether the file existed at all.
	Present bool `json:"present"`
}

// Load reads the config file, applies defaults, then applies TASKS_ overrides.
// A missing file is the unconfigured default; a malformed one is an error,
// because silently falling back would turn a typo in the gate command into an
// open gate (§9.2 fails closed).
func Load() (*Config, error) {
	c := &Config{
		LeaseSeconds: DefaultLeaseSeconds,
		SweepSeconds: DefaultSweepSeconds,
		Path:         ConfigPath(),
	}
	raw, err := os.ReadFile(c.Path)
	switch {
	case os.IsNotExist(err):
	case err != nil:
		return nil, fmt.Errorf("read %s: %w", c.Path, err)
	default:
		c.Present = true
		kv, err := parseTOML(string(raw))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", c.Path, err)
		}
		for k, v := range kv {
			if err := c.apply(k, v); err != nil {
				return nil, fmt.Errorf("%s: %w", c.Path, err)
			}
		}
	}
	for _, k := range []string{"lease_seconds", "sweep_seconds", "gate_command", "on_event"} {
		if v, ok := os.LookupEnv(EnvPrefix + strings.ToUpper(k)); ok && v != "" {
			if err := c.apply(k, value{scalar: v, isList: strings.HasSuffix(k, "command") || k == "on_event",
				list: strings.Fields(v)}); err != nil {
				return nil, fmt.Errorf("%s%s: %w", EnvPrefix, strings.ToUpper(k), err)
			}
		}
	}
	return c, nil
}

func (c *Config) apply(key string, v value) error {
	switch key {
	case "lease_seconds":
		n, err := strconv.ParseInt(strings.TrimSpace(v.scalar), 10, 64)
		if err != nil || n <= 0 {
			return fmt.Errorf("lease_seconds must be a positive number, got %q", v.scalar)
		}
		c.LeaseSeconds = n
	case "sweep_seconds":
		n, err := strconv.ParseInt(strings.TrimSpace(v.scalar), 10, 64)
		if err != nil || n <= 0 {
			return fmt.Errorf("sweep_seconds must be a positive number, got %q", v.scalar)
		}
		c.SweepSeconds = n
	case "gate_command":
		c.GateCommand = v.list
	case "on_event":
		c.OnEvent = v.list
	default:
		return fmt.Errorf("unknown key %q", key)
	}
	return nil
}

// LeaseMS is the lease in the milliseconds the state machine works in (§5.3).
func (c *Config) LeaseMS() int64 { return c.LeaseSeconds * 1000 }
