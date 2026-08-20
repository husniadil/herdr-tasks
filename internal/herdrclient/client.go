// Package herdrclient is the one place that talks to `herdr` (§11.1). Every
// Herdr fact — pane, agent, harness, status — is read here and nowhere else,
// and nothing in the plugin infers one when Herdr has an answer.
package herdrclient

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/husniadil/herdr-tasks/internal/codes"
)

// Client runs the herdr CLI.
type Client struct {
	bin string

	mu     sync.Mutex
	schema *Schema
}

// New builds a client for an explicit binary path.
func New(bin string) *Client { return &Client{bin: bin} }

// FromEnv resolves the binary the way §11.1 says: HERDR_BIN_PATH, else `herdr`
// on PATH. It never hard-codes a socket path.
func FromEnv() *Client {
	if bin := os.Getenv("HERDR_BIN_PATH"); bin != "" {
		return New(bin)
	}
	return New("herdr")
}

// Bin is the resolved binary path, for doctor output.
func (c *Client) Bin() string { return c.bin }

// Agent is the §3.4 snapshot: the three facts an agent principal carries.
type Agent struct {
	PaneID  string `json:"pane_id"`
	Name    string `json:"name"`
	Harness string `json:"agent"`
	Status  string `json:"agent_status"`
	Session string `json:"agent_session"`
}

// AgentGet snapshots a pane's agent. A pane Herdr cannot answer for is not an
// error: it yields harness "unknown", which §3.4 requires over a guess.
func (c *Client) AgentGet(pane string) (Agent, error) {
	out, err := c.run(2*time.Second, "agent", "get", pane, "--json")
	if err != nil {
		var ce *codes.Error
		if ok := asCoded(err, &ce); ok && ce.Code == codes.Unavailable {
			return Agent{}, err
		}
		return Agent{PaneID: pane, Harness: "unknown"}, nil
	}
	var a Agent
	if err := json.Unmarshal(out, &a); err != nil || a.Harness == "" {
		return Agent{PaneID: pane, Harness: "unknown"}, nil
	}
	a.PaneID = pane
	return a, nil
}

// Schema is what `herdr api schema --json` says this Herdr can do (§11.2).
type Schema struct {
	Requests []string `json:"requests"`
	Events   []string `json:"events"`
}

// Has reports whether Herdr listed a request or an event.
func (s *Schema) Has(capability string) bool {
	if s == nil {
		return false
	}
	for _, r := range s.Requests {
		if r == capability {
			return true
		}
	}
	for _, e := range s.Events {
		if e == capability {
			return true
		}
	}
	return false
}

// Schema reads the schema once and caches it: §11.2 says read it at daemon
// start and decide, not poll.
func (c *Client) Schema() (*Schema, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.schema != nil {
		return c.schema, nil
	}
	out, err := c.run(2*time.Second, "api", "schema", "--json")
	if err != nil {
		return nil, err
	}
	var s Schema
	if err := json.Unmarshal(out, &s); err != nil {
		return nil, codes.Errorf(codes.Unavailable, "herdr api schema: unreadable answer: %v", err)
	}
	c.schema = &s
	return c.schema, nil
}

// Require is the §11.2 gate at the verb: a missing capability is UNSUPPORTED
// with the capability named, not a startup failure.
func (c *Client) Require(capability string) error {
	s, err := c.Schema()
	if err != nil {
		return err
	}
	if !s.Has(capability) {
		return codes.Errorf(codes.Unsupported, "this Herdr does not offer %s", capability)
	}
	return nil
}

// Prompt delivers text to an agent (§11.4). Typing into a pane is Herdr's job:
// the plugin never implements a type-verify-retype loop of its own.
func (c *Client) Prompt(pane, text string) error {
	if err := c.Require("agent.prompt"); err != nil {
		return err
	}
	_, err := c.run(10*time.Second, "agent", "prompt", pane, text)
	return err
}

func (c *Client) run(timeout time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, codes.Errorf(codes.Timeout, "herdr %s: timed out", args[0])
	}
	if err != nil {
		if isNotRunnable(err) {
			return nil, codes.Errorf(codes.Unavailable, "cannot run %s: %v", c.bin, err)
		}
		return nil, codes.Errorf(codes.Unexpected, "herdr %v: %v: %s", args, err, bytes.TrimSpace(stderr.Bytes()))
	}
	return stdout.Bytes(), nil
}

func isNotRunnable(err error) bool {
	var ee *exec.ExitError
	return !asExit(err, &ee)
}
