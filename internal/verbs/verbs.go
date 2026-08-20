// Package verbs is the single registry both doors are built from. §6.1 says
// every verb is a CLI subcommand with --json and a matching MCP tool with the
// same name, arguments, and result shape, and that a parity test must
// enumerate both surfaces. Generating both from this list is how that is kept
// true rather than checked after the fact.
package verbs

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Kind of an argument, in the small vocabulary both doors can render.
const (
	String  = "string"
	Int     = "int"
	Bool    = "bool"
	Strings = "string[]"
)

// Arg is one parameter of a verb. Positional args are CLI positionals and
// ordinary named fields over MCP and the socket.
type Arg struct {
	Name       string
	Type       string
	Desc       string
	Required   bool
	Positional bool
}

// Verb is one operation, in every surface it appears in.
type Verb struct {
	// Name is the daemon verb on the socket.
	Name string
	// CLI is the subcommand path, e.g. {"task", "claim"}.
	CLI []string
	// MCP is the tool name, `tasks_<verb>` (§7.1). Empty means CLI-only: §7.3
	// says keep the tool count small and let the skill teach the rest.
	MCP string
	// Short is the one-line help both doors show.
	Short string
	// Long is the CLI's longer help, when a verb needs one.
	Long string
	// Args is the parameter list, in CLI positional order.
	Args []Arg
	// Gated is the §9.4 verb name passed to the policy gate. Empty means this
	// verb is not offered to the gate — which is NOT the same as "changes
	// nothing", and used to be documented as if it were: eleven verbs that
	// write carry no gate name, including three that destroy. A Mutates verb
	// with no Gated must say why in Ungated.
	Gated string
	// Ungated is why a verb that writes passes no name to the policy gate.
	// Required exactly when Mutates is true and Gated is empty, so the eleven
	// cannot quietly become twelve.
	Ungated string
	// Who is the principal rule: who may call this verb. Every verb that
	// writes states one, because the alternative is a rule of "whoever asked"
	// arrived at by omission — which is how deleting a rival's note came to be
	// easier than fixing a typo in it.
	Who string
	// Mutates marks a verb that writes, for the --base-updated-at guard (§5.6).
	Mutates bool
}

// idArg is the reference every entity verb takes: a 26-character id or the
// project's own number (§5.4).
func idArg(desc string) Arg {
	return Arg{Name: "id", Type: String, Desc: desc, Required: true, Positional: true}
}

// All is the registry. Order is the order the CLI lists them in.
var All = []Verb{
	{
		Name: "task.create", CLI: []string{"task", "create"}, MCP: "tasks_create",
		Short:   "File a task in the backlog",
		Gated:   "tasks.create",
		Who:     "Anyone: filing work is not destructive, and §3.5 trusts the local user.",
		Mutates: true,
		Args: []Arg{
			{Name: "title", Type: String, Desc: "What the task is", Required: true, Positional: true},
			{Name: "description", Type: String, Desc: "The context a claimer needs"},
			{Name: "priority", Type: Int, Desc: "Higher sorts first"},
			{Name: "validation", Type: Strings, Desc: "An acceptance criterion, as a command and what its output must show (repeatable)"},
			{Name: "depends-on", Type: Strings, Desc: "A task that must be done first (repeatable)"},
			{Name: "discovered-from", Type: String, Desc: "The task this was found while working on"},
		},
	},
	{
		Name: "task.list", CLI: []string{"task", "list"}, MCP: "tasks_list",
		Short: "List tasks in this project",
		Args: []Arg{
			{Name: "status", Type: String, Desc: "todo, doing, review, done or cancelled"},
			{Name: "ready", Type: Bool, Desc: "Only unblocked, unclaimed todo tasks"},
			{Name: "mine", Type: Bool, Desc: "Only tasks this principal holds"},
			{Name: "query", Type: String, Desc: "Match title or description"},
			{Name: "archived", Type: Bool, Desc: "Include archived tasks"},
			{Name: "limit", Type: Int, Desc: "Stop after this many"},
		},
	},
	{
		Name: "task.get", CLI: []string{"task", "get"}, MCP: "tasks_get",
		Short: "Read one task in full",
		Args:  []Arg{idArg("The task id or number")},
	},
	{
		Name: "task.claim", CLI: []string{"task", "claim"}, MCP: "tasks_claim",
		Short:   "Take a task and its lease",
		Gated:   "tasks.claim",
		Who:     "Anyone, for an unclaimed and unblocked task; the holder, to renew.",
		Mutates: true,
		Args:    []Arg{idArg("The task id or number")},
	},
	{
		Name: "task.touch", CLI: []string{"task", "touch"}, MCP: "tasks_touch",
		Short:   "Renew the lease on a task you hold",
		Long:    "Run this at the start of each turn: a lease that lapses is swept and the task returns to the queue.",
		Who:     "The holder only.",
		Ungated: "renewing your own lease is not a decision a policy could usefully defer",
		Mutates: true,
		Args:    []Arg{idArg("The task id or number")},
	},
	{
		Name: "task.release", CLI: []string{"task", "release"}, MCP: "tasks_release",
		Short:   "Hand a task back with a note saying what is left",
		Who:     "The holder, or the operator.",
		Ungated: "handing work back is the safe direction; a gate that could park it would strand the task",
		Mutates: true,
		Args: []Arg{
			idArg("The task id or number"),
			{Name: "note", Type: String, Desc: "What is left, for whoever claims it next"},
		},
	},
	{
		Name: "task.submit", CLI: []string{"task", "submit"}, MCP: "tasks_submit",
		Short:   "Send a task to review with a report and its evidence",
		Gated:   "tasks.submit",
		Who:     "The holder, or the operator.",
		Mutates: true,
		Args: []Arg{
			idArg("The task id or number"),
			{Name: "report", Type: String, Desc: "What you did and how it was verified", Required: true},
			{Name: "evidence", Type: Strings, Desc: "A command you ran and what it printed (repeatable)"},
		},
	},
	{
		Name: "task.approve", CLI: []string{"task", "approve"}, MCP: "tasks_approve",
		Short:   "Accept submitted work",
		Long:    "A harness may not approve work its own harness produced (§6.6). The operator is exempt.",
		Gated:   "tasks.approve",
		Who:     "Anyone but the submitting harness (§6.6); the operator is exempt.",
		Mutates: true,
		Args:    []Arg{idArg("The task id or number")},
	},
	{
		Name: "task.reject", CLI: []string{"task", "reject"}, MCP: "tasks_reject",
		Short:   "Send submitted work back with feedback",
		Gated:   "tasks.reject",
		Who:     "Anyone but the submitting harness (§6.6); the operator is exempt.",
		Mutates: true,
		Args: []Arg{
			idArg("The task id or number"),
			{Name: "feedback", Type: String, Desc: "What must change", Required: true},
		},
	},
	{
		Name: "task.goal", CLI: []string{"task", "goal"}, MCP: "tasks_goal",
		Short: "Print a paste-ready /goal condition for a task",
		Args:  []Arg{idArg("The task id or number")},
	},
	{
		Name: "task.cancel", CLI: []string{"task", "cancel"},
		Short:   "End a task that will not be done",
		Gated:   "tasks.cancel",
		Who:     "The holder or the operator while claimed; anyone while unclaimed — the same rule release and submit apply.",
		Mutates: true,
		Args: []Arg{
			idArg("The task id or number"),
			{Name: "reason", Type: String, Desc: "Why it is being cancelled"},
		},
	},
	{
		Name: "task.update", CLI: []string{"task", "update"},
		Short:   "Edit a live task",
		Gated:   "tasks.update",
		Who:     "Anyone, while the task is not terminal.",
		Mutates: true,
		Args: []Arg{
			idArg("The task id or number"),
			{Name: "title", Type: String, Desc: "A new title"},
			{Name: "description", Type: String, Desc: "A new description"},
			{Name: "priority", Type: Int, Desc: "A new priority"},
			{Name: "validation", Type: Strings, Desc: "Replace the acceptance criteria (repeatable)"},
			{Name: "depends-on", Type: Strings, Desc: "Replace the dependencies (repeatable)"},
		},
	},
	{
		Name: "task.archive", CLI: []string{"task", "archive"},
		Short:   "Hide a finished task from the default list",
		Who:     "Anyone, and only a terminal task.",
		Ungated: "hiding a finished task changes no work and is reversible",
		Mutates: true,
		Args:    []Arg{idArg("The task id or number")},
	},
	{
		Name: "task.delete", CLI: []string{"task", "delete"},
		Short:   "Remove a task that was never claimed",
		Long:    "Only a never-claimed task is deleted (§5.7). Everything else is cancelled or archived.",
		Who:     "Anyone, and only a task that was never claimed (§5.7).",
		Ungated: "a task nobody ever claimed is nobody's work to protect",
		Mutates: true,
		Args:    []Arg{idArg("The task id or number")},
	},
	{
		Name: "note.add", CLI: []string{"note", "add"}, MCP: "tasks_note_add",
		Short:   "File a pre-decision idea on the board",
		Gated:   "tasks.note_add",
		Who:     "Anyone.",
		Mutates: true,
		Args: []Arg{
			{Name: "body", Type: String, Desc: "The idea, in a sentence or two", Required: true, Positional: true},
		},
	},
	{
		Name: "note.list", CLI: []string{"note", "list"}, MCP: "tasks_note_list",
		Short: "List notes in this project",
		Args: []Arg{
			{Name: "status", Type: String, Desc: "inbox, discussing, needs_input, proposed, keep, task or dropped"},
			{Name: "query", Type: String, Desc: "Match the body or the verdict reason"},
			{Name: "limit", Type: Int, Desc: "Stop after this many"},
		},
	},
	{
		Name: "note.get", CLI: []string{"note", "get"},
		Short: "Read one note in full",
		Args:  []Arg{idArg("The note id or number")},
	},
	{
		Name: "note.update", CLI: []string{"note", "update"},
		Short: "Fix the wording of a note",
		Long: "The author fixes their own note and the operator fixes anyone's, until the\n" +
			"operator has decided it. A decided note is what was decided on, so its\n" +
			"wording no longer moves.",
		Who:     "The author, or the operator, until the note is decided.",
		Mutates: true,
		Gated:   "tasks.note_update",
		Args: []Arg{
			idArg("The note id or number"),
			{Name: "body", Type: String, Desc: "What the note should say instead", Required: true},
		},
	},
	{
		Name: "note.discuss", CLI: []string{"note", "discuss"},
		Short:   "Open or re-open triage on a note",
		Who:     "Anyone.",
		Ungated: "opening a triage is how an agent volunteers; a gate here would stop the board being worked",
		Mutates: true,
		Args: []Arg{
			idArg("The note id or number"),
			{Name: "question", Type: String, Desc: "Park the discussion on the operator with this question"},
		},
	},
	{
		Name: "note.verdict", CLI: []string{"note", "verdict"}, MCP: "tasks_note_verdict",
		Short:   "End a discussion with a proposal: task, keep or drop",
		Long:    "A verdict is a proposal, never the decision. Only the operator promotes, keeps or drops a note.",
		Who:     "Anyone, amendable until the operator acts.",
		Ungated: "a verdict is a proposal, not a decision — the decision is note.promote, which is gated",
		Mutates: true,
		Args: []Arg{
			idArg("The note id or number"),
			{Name: "verdict", Type: String, Desc: "task, keep or drop", Required: true, Positional: true},
			{Name: "reason", Type: String, Desc: "Why"},
		},
	},
	{
		Name: "note.promote", CLI: []string{"note", "promote"},
		Short:   "Turn a note into a task (operator only)",
		Gated:   "tasks.note_promote",
		Who:     "The operator only.",
		Mutates: true,
		Args: []Arg{
			idArg("The note id or number"),
			{Name: "title", Type: String, Desc: "The task title; the note body is the default"},
			{Name: "validation", Type: Strings, Desc: "An acceptance criterion, as a command and what its output must show (repeatable)"},
		},
	},
	{
		Name: "note.keep", CLI: []string{"note", "keep"},
		Short:   "File a note as approved but not now (operator only)",
		Who:     "The operator only.",
		Ungated: "already the narrowest principal there is; a gate cannot narrow it further",
		Mutates: true,
		Args: []Arg{
			idArg("The note id or number"),
			{Name: "reason", Type: String, Desc: "Why"},
		},
	},
	{
		Name: "note.drop", CLI: []string{"note", "drop"},
		Short:   "Reject a note (operator only)",
		Who:     "The operator only.",
		Ungated: "already the narrowest principal there is; a gate cannot narrow it further",
		Mutates: true,
		Args: []Arg{
			idArg("The note id or number"),
			{Name: "reason", Type: String, Desc: "Why"},
		},
	},
	{
		Name: "note.delete", CLI: []string{"note", "delete"},
		Short:   "Remove a note that is still in the inbox",
		Who:     "The author, or the operator, and only an inbox note (§5.7).",
		Ungated: "an inbox note is pre-decision, and its author or the operator removing it decides nothing",
		Mutates: true,
		Args:    []Arg{idArg("The note id or number")},
	},
	{
		Name: "parked.list", CLI: []string{"parked", "list"},
		Short: "List actions the policy gate deferred",
	},
	{
		Name: "parked.resolve", CLI: []string{"parked", "resolve"},
		Short:   "Run or reject a deferred action (operator only)",
		Long:    "Resolving re-runs the verb under the original subject, never the resolver's (§9.3).",
		Who:     "The operator only (§9.3).",
		Ungated: "this verb IS the gate's own resolution; gating it would defer the answer to the deferral",
		Mutates: true,
		Args: []Arg{
			idArg("The parked action id"),
			{Name: "reject", Type: Bool, Desc: "Reject it instead of running it"},
		},
	},
	{
		Name: "events", CLI: []string{"events"}, MCP: "tasks_events",
		Short: "Stream the append-only event trail",
		Args: []Arg{
			{Name: "since", Type: String, Desc: "An event id or a Unix-millisecond timestamp to resume from"},
			{Name: "entity", Type: String, Desc: "task or note"},
			{Name: "limit", Type: Int, Desc: "Stop after this many"},
		},
	},
	{
		Name: "doctor", CLI: []string{"doctor"}, MCP: "tasks_doctor",
		Short: "Report version, dirs, socket, Herdr, hooks, gate and anything degraded",
	},
	{
		Name: "sweep", CLI: []string{"sweep"},
		Short:   "Release leases that have lapsed, or every lease a pane holds",
		Long:    "The daemon does this on a timer (§11.5). Run it by hand, or from a Herdr\nevent reaction, when a pane died and its work should return to the queue now.",
		Who:     "With --pane: that pane, or the operator. Without: anyone, and it releases only leases that are already expired.",
		Ungated: "the daemon's own §11.5 timer calls it, and a gate that parked the timer would stop leases coming back at all",
		Mutates: true,
		Args: []Arg{
			{Name: "pane", Type: String, Desc: "Release every lease this Herdr pane holds, expired or not"},
		},
	},
	{
		Name: "dump", CLI: []string{"dump"},
		Short: "Print the whole store as JSON",
	},
}

// ByName finds a verb by its daemon name.
// Fingerprint identifies the door surface this build speaks: every verb, every
// argument it declares, and its gate name. It is NOT the release version.
// Version is bumped by hand and stayed 0.1.0 across the change that added an
// argument to note.promote, so a version comparison could not tell a door and
// a daemon apart while one of them was silently dropping the new argument.
// This changes exactly when the surface changes.
func Fingerprint() string { return FingerprintOf(All) }

// FingerprintOf is Fingerprint over an arbitrary table, so a test can prove
// that a changed surface changes the answer.
func FingerprintOf(list []Verb) string {
	var b strings.Builder
	for _, v := range list {
		b.WriteString(v.Name)
		b.WriteString("\x00")
		b.WriteString(v.Gated)
		for _, a := range v.Args {
			b.WriteString("\x00")
			b.WriteString(a.Name)
			b.WriteString(":")
			b.WriteString(a.Type)
		}
		b.WriteString("\n")
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:8])
}

// Accepts reports whether this verb declares an argument by that name. The
// daemon refuses the ones it does not: an argument nobody reads is a request
// the caller thinks it made and the daemon never saw.
func (v Verb) Accepts(name string) bool {
	for _, a := range v.Args {
		if a.Name == name {
			return true
		}
	}
	return false
}

func ByName(name string) (Verb, bool) {
	for _, v := range All {
		if v.Name == name {
			return v, true
		}
	}
	return Verb{}, false
}

// MCPTools is the pinned tool list (§7.1), in registry order.
func MCPTools() []Verb {
	out := make([]Verb, 0, len(All))
	for _, v := range All {
		if v.MCP != "" {
			out = append(out, v)
		}
	}
	return out
}

// GatedVerbs is the list a README must carry so a future policy plugin can
// name them (§9.4).
func GatedVerbs() []string {
	out := []string{}
	for _, v := range All {
		if v.Gated != "" {
			out = append(out, v.Gated)
		}
	}
	return out
}
