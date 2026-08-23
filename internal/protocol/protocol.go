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
	// Operator is the door saying that the PROCESS it runs in was started by
	// a deliberate human act, which is what §3.7 requires before a paneless
	// call is `human` rather than `none`. The CLI sets it always: one process
	// per call, so its argv is that act. A server door sets it only when it
	// was started with `--operator` (§7.5). It is a property of the door
	// process, never of the call — no tool schema exposes it, and the door
	// fills it in rather than copying it out of a request.
	Operator bool `json:"operator,omitempty"`
	// BaseUpdatedAt is the §5.6 optimistic guard.
	BaseUpdatedAt int64 `json:"base_updated_at,omitempty"`
	// Build is which binary the DOOR making this call is running (§13.3),
	// and only a long-lived door sends it. The MCP door serves its
	// Instructions once, at construction, so a session can be acting on prose
	// that was corrected days ago while every other signal reads correct; the
	// daemon stats this path to say so in doctor. A CLI process has no such
	// window — it reads its own help and exits inside one invocation — so it
	// leaves this empty, and empty means "not a long-lived door", never "a
	// door too old to say". Additive: an old daemon ignores it.
	Build verbs.Build `json:"build,omitempty"`
	// Follow turns `events` into a subscription (§8.2).
	Follow bool           `json:"follow,omitempty"`
	Args   map[string]any `json:"args,omitempty"`
}

// Response is what comes back: exactly one of Result or Error (§6.2).
type Response struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  *ErrorBody      `json:"error,omitempty"`
	// Done ends a stream on purpose. Without it a daemon that finished and a
	// daemon that was killed both look like a closed socket, and a follower
	// cannot tell "there is no more" from "I stopped being told" — so it
	// reported success for both. Only `events --follow` sends it.
	Done bool `json:"done,omitempty"`
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
