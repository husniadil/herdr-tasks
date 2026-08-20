// Package protocol is the wire between the doors and the daemon: one JSON
// document per line over the Unix socket at <state_dir>/tasks.sock (§2.2).
package protocol

import (
	"encoding/json"

	"github.com/husniadil/herdr-tasks/internal/verbs"
)

// Request is one verb call. The principal is derived by the door from its own
// environment (§3.2) and carried here as pane id or an explicit --as: the
// boundary is the local user account, and whoever can open the socket is
// trusted as the user (§3.5).
type Request struct {
	Verb        string `json:"verb"`
	Project     string `json:"project"`
	AllProjects bool   `json:"all_projects,omitempty"`
	PaneID      string `json:"pane_id,omitempty"`
	TabID       string `json:"tab_id,omitempty"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	// As is the §3.2 escape hatch for cron, trigger and plugin principals.
	As string `json:"as,omitempty"`
	// BaseUpdatedAt is the §5.6 optimistic guard.
	BaseUpdatedAt int64 `json:"base_updated_at,omitempty"`
	// Follow turns `events` into a subscription (§8.2).
	Follow bool           `json:"follow,omitempty"`
	Args   map[string]any `json:"args,omitempty"`
}

// Response is what comes back: exactly one of Result or Error (§6.2).
type Response struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  *ErrorBody      `json:"error,omitempty"`
	// Fingerprint is the door surface the answering daemon speaks. A door
	// compares it with its own and says so when they differ — and an empty
	// one is the same signal, because a daemon old enough to predate this
	// field is exactly the daemon that would drop an argument it never
	// learned. Additive, so an old door simply ignores it.
	Fingerprint string `json:"fingerprint,omitempty"`
	// Build is which binary answered (§13.3). The fingerprint says what the
	// daemon SPEAKS; this says what it IS, and a change to a rule or a bound
	// moves only the second. Additive, like the field above it.
	Build verbs.Build `json:"build,omitempty"`
}

// ErrorBody is the §6.2 envelope. ParkedID is the §9.3 addition a DENIED
// answer carries when the gate deferred rather than refused.
type ErrorBody struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	ParkedID string `json:"parked_id,omitempty"`
}
