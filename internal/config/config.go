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

// Defaults. A lease long enough that an agent thinking hard does not lose its
// claim, short enough that a dead pane frees work within the hour.
const (
	DefaultLeaseSeconds = 900
	DefaultSweepSeconds = 60
)

// StateDir is HERDR_PLUGIN_STATE_DIR, else ${XDG_STATE_HOME:-~/.local/state}/tasks (§5.1).
func StateDir() string {
	if d := os.Getenv("HERDR_PLUGIN_STATE_DIR"); d != "" {
		return d
	}
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, Name)
	}
	return filepath.Join(homeDir(), ".local", "state", Name)
}

// ConfigDir is HERDR_PLUGIN_CONFIG_DIR, else ${XDG_CONFIG_HOME:-~/.config}/tasks (§10.1).
func ConfigDir() string {
	if d := os.Getenv("HERDR_PLUGIN_CONFIG_DIR"); d != "" {
		return d
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, Name)
	}
	return filepath.Join(homeDir(), ".config", Name)
}

// SocketPath is <state_dir>/tasks.sock (§2.2).
func SocketPath() string { return filepath.Join(StateDir(), Name+".sock") }

// DBPath is <state_dir>/tasks.db (§5.1).
func DBPath() string { return filepath.Join(StateDir(), Name+".db") }

// ConfigPath is <config_dir>/tasks.toml (§10.1).
func ConfigPath() string { return filepath.Join(ConfigDir(), Name+".toml") }

// EnsureStateDir creates the state dir with mode 0700 (§3.5).
func EnsureStateDir() error {
	return os.MkdirAll(StateDir(), 0o700)
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
