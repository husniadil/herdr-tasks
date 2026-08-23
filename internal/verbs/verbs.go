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
	// MCP is the tool name: the verb alone, with dots as underscores
	// (§7.1). Every verb has one, and §7.3 admits no CLI-only verb: the only
	// ground for keeping one off the agent surface was "this authority is the
	// operator's", and §3.7 makes that authority advice an agent confirms
	// rather than a refusal a door makes. A verb withheld from a principal is
	// withheld by the §9 gate, which both doors pass through.
	MCP string
	// Short is the one-line help both doors show.
	Short string
	// Long is the CLI's longer help, when a verb needs one.
	Long string
	// Args is the parameter list, in CLI positional order.
	Args []Arg
	// Gated is the §9.4 verb name passed to the policy gate. Empty means this
	// verb is not offered to the gate, which is NOT the same as "changes
	// nothing": thirteen verbs that write carry no gate name, three of them
	// destructive. A Mutates verb with no Gated must say why in Ungated.
	Gated string
	// Ungated is why a verb that writes passes no name to the policy gate.
	// Required exactly when Mutates is true and Gated is empty, so the thirteen
	// cannot quietly become fourteen.
	Ungated string
	// Who is the principal rule: who may call this verb. Every verb that
	// writes states one, because the alternative is a rule of "whoever asked"
	// arrived at by omission — which is how destroying a rival's note becomes
	// easier than fixing a typo in it.
	Who string
	// Mutates marks a verb that writes, for the --base-updated-at guard (§5.6).
	Mutates bool
	// AllProjects marks a verb that HONOURS --all-projects (§4.4). Both doors
	// carry the flag on every verb, so the daemon refuses it with USAGE
	// wherever this is false: a scope a verb ignores is a scope the caller
	// thinks it asked for and never got.
	AllProjects bool
}

// idArg is the reference every entity verb takes: a 26-character id or the
// project's own number (§5.4).
func idArg(desc string) Arg {
	return Arg{Name: "id", Type: String, Desc: desc, Required: true, Positional: true}
}

// All is the registry. Order is the order the CLI lists them in.
var All = []Verb{
	{
		Name: "task.create", CLI: []string{"task", "create"}, MCP: "create",
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
		Name: "task.list", CLI: []string{"task", "list"}, MCP: "list",
		Short:       "List tasks in this project",
		AllProjects: true,
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
		Name: "task.get", CLI: []string{"task", "get"}, MCP: "get",
		Short:       "Read one task in full",
		AllProjects: true,
		Args:        []Arg{idArg("The task id or number")},
	},
	{
		Name: "task.claim", CLI: []string{"task", "claim"}, MCP: "claim",
		Short:       "Take a task and its lease",
		AllProjects: true,
		Gated:       "tasks.claim",
		Who:         "Anyone, for an unclaimed and unblocked task; the holder, to renew.",
		Mutates:     true,
		Args:        []Arg{idArg("The task id or number")},
	},
	{
		Name: "task.touch", CLI: []string{"task", "touch"}, MCP: "touch",
		Short:       "Renew the lease on a task you hold",
		AllProjects: true,
		Long:        "Run this at the start of each turn: a lease that lapses is swept and the task returns to the queue.",
		Who:         "The holder only.",
		Ungated:     "renewing your own lease is not a decision a policy could usefully defer",
		Mutates:     true,
		Args:        []Arg{idArg("The task id or number")},
	},
	{
		Name: "task.release", CLI: []string{"task", "release"}, MCP: "release",
		Short:       "Hand a task back with a note saying what is left",
		AllProjects: true,
		Who:         "The holder, or the operator.",
		Ungated:     "handing work back is the safe direction; a gate that could park it would strand the task",
		Mutates:     true,
		Args: []Arg{
			idArg("The task id or number"),
			{Name: "note", Type: String, Desc: "What is left, for whoever claims it next"},
		},
	},
	{
		Name: "task.submit", CLI: []string{"task", "submit"}, MCP: "submit",
		Short:       "Send a task to review with a report and its evidence",
		AllProjects: true,
		Gated:       "tasks.submit",
		Who:         "The holder, or the operator.",
		Mutates:     true,
		Args: []Arg{
			idArg("The task id or number"),
			{Name: "report", Type: String, Desc: "What you did and how it was verified", Required: true},
			{Name: "evidence", Type: Strings, Desc: "A command you ran and what it printed (repeatable)"},
			{Name: "evidence-for", Type: Strings, Desc: "Evidence for one acceptance criterion, as \"<criterion>: what it printed\" (repeatable)"},
		},
	},
	{
		Name: "task.amend", CLI: []string{"task", "amend"}, MCP: "amend",
		Short:       "Correct the report and evidence of work already in review",
		AllProjects: true,
		Long: "Submit is made once and judged once, so it refuses a second call. This is the other half: " +
			"the holder replaces the report and the evidence of a task waiting for a reviewer, and the " +
			"submission itself — when it was submitted, by whom, from which session — does not move. " +
			"The row records that it was amended and how many times, and the trail carries an `amended` " +
			"event, so a reviewer sees the correction rather than learning about it in a message. " +
			"A list you do not name is left alone; naming one with nothing in it clears it.",
		Gated:   "tasks.amend",
		Who:     "The holder, or the operator.",
		Mutates: true,
		Args: []Arg{
			idArg("The task id or number"),
			{Name: "report", Type: String, Desc: "The corrected report, which replaces the one on the row", Required: true},
			{Name: "evidence", Type: Strings, Desc: "A command you ran and what it printed, replacing the whole list; leave it out to keep what is there (repeatable)"},
			{Name: "evidence-for", Type: Strings, Desc: "Evidence for one acceptance criterion, as \"<criterion>: what it printed\" (repeatable)"},
		},
	},
	{
		Name: "task.approve", CLI: []string{"task", "approve"}, MCP: "approve",
		Short:       "Accept submitted work",
		AllProjects: true,
		Long:        "A principal may not approve work it, its own pane, or its own agent session produced (§6.6). The harness does not recuse: claude reviews claude from another session. The operator is exempt.",
		Gated:       "tasks.approve",
		Who:         "Anyone but the submitting principal, pane or agent session (§6.6); the operator is exempt.",
		Mutates:     true,
		Args:        []Arg{idArg("The task id or number")},
	},
	{
		Name: "task.reject", CLI: []string{"task", "reject"}, MCP: "reject",
		Short:       "Send submitted work back with feedback",
		AllProjects: true,
		Gated:       "tasks.reject",
		Who:         "Anyone but the submitting principal, pane or agent session (§6.6); the operator is exempt.",
		Mutates:     true,
		Args: []Arg{
			idArg("The task id or number"),
			{Name: "feedback", Type: String, Desc: "What must change", Required: true},
		},
	},
	{
		Name: "task.goal", CLI: []string{"task", "goal"}, MCP: "goal",
		Short: "Print a paste-ready /goal condition for a task",
		Long: "The document form is for a human to paste. --one-line renders the same condition with no " +
			"newline in it, for the argv of `herdr agent start`, which refuses a newline outright. A line " +
			"break costs four characters there instead of one, so the one-line form has less room under the " +
			"same 4,000-character ceiling: it gives up context and criteria in the same order the document " +
			"does, and says how many criteria it dropped, rather than going over.",
		Args: []Arg{
			idArg("The task id or number"),
			{Name: "one-line", Type: Bool, Desc: "Render it as one line, for an argv that refuses newlines"},
		},
	},
	{
		Name: "task.cancel", CLI: []string{"task", "cancel"}, MCP: "cancel",
		Short:       "End a task that will not be done",
		AllProjects: true,
		Gated:       "tasks.cancel",
		Who:         "The holder or the operator while claimed; anyone while unclaimed — the same rule release and submit apply.",
		Mutates:     true,
		Args: []Arg{
			idArg("The task id or number"),
			{Name: "reason", Type: String, Desc: "Why it is being cancelled"},
		},
	},
	{
		Name: "task.update", CLI: []string{"task", "update"}, MCP: "update",
		Short:       "Edit a live task",
		AllProjects: true,
		Gated:       "tasks.update",
		Who:         "Anyone, while the task is not terminal.",
		Mutates:     true,
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
		Name: "task.archive", CLI: []string{"task", "archive"}, MCP: "archive",
		Short:       "Hide a finished task from the default list",
		AllProjects: true,
		Who:         "Anyone, and only a terminal task. Arranging the default view is the operator's call, so an agent asks before tidying someone else's board (§3.7).",
		Ungated:     "hiding a finished task changes no work and is reversible",
		Mutates:     true,
		Args:        []Arg{idArg("The task id or number")},
	},
	{
		Name: "task.delete", CLI: []string{"task", "delete"}, MCP: "delete",
		Short:   "Remove a task that was never claimed",
		Long:    "Only a never-claimed task is deleted (§5.7). Everything else is cancelled or archived.",
		Who:     "Anyone, and only a task that was never claimed (§5.7). Deleting removes the row rather than ending it, so an agent confirms with the operator first and reaches for `cancel` otherwise (§3.7).",
		Ungated: "a task nobody ever claimed is nobody's work to protect",
		Mutates: true,
		Args:    []Arg{idArg("The task id or number")},
	},
	{
		Name: "note.add", CLI: []string{"note", "add"}, MCP: "note_add",
		Short:   "File a pre-decision idea on the board",
		Gated:   "tasks.note_add",
		Who:     "Anyone.",
		Mutates: true,
		Args: []Arg{
			{Name: "body", Type: String, Desc: "The idea, in a sentence or two", Required: true, Positional: true},
		},
	},
	{
		Name: "note.list", CLI: []string{"note", "list"}, MCP: "note_list",
		Short:       "List notes in this project",
		AllProjects: true,
		Args: []Arg{
			{Name: "status", Type: String, Desc: "inbox, discussing, needs_input, proposed, keep, task or dropped"},
			{Name: "query", Type: String, Desc: "Match the body or the verdict reason"},
			{Name: "limit", Type: Int, Desc: "Stop after this many"},
		},
	},
	{
		Name: "note.get", CLI: []string{"note", "get"}, MCP: "note_get",
		Short: "Read one note in full",
		Args:  []Arg{idArg("The note id or number")},
	},
	{
		Name: "note.update", CLI: []string{"note", "update"}, MCP: "note_update",
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
		Name: "note.discuss", CLI: []string{"note", "discuss"}, MCP: "note_discuss",
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
		Name: "note.verdict", CLI: []string{"note", "verdict"}, MCP: "note_verdict",
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
		Name: "note.promote", CLI: []string{"note", "promote"}, MCP: "note_promote",
		Short: "Turn one or more notes into a task (ask the operator first)",
		Long: "The task is created on the note's own board unless --to-project names another one; the note stays where it was filed either way and points at the task it became.\n" +
			"Notes named by --also are folded into the same task rather than each becoming one: several notes are often one change, and the folded ones end on the task instead of reading as undecided forever.",
		Gated:   "tasks.note_promote",
		Who:     "The operator, or an agent that has confirmed this promotion with them first (§3.7). The event records the agent, never the operator.",
		Mutates: true,
		Args: []Arg{
			idArg("The note id or number"),
			{Name: "title", Type: String, Desc: "The task title; the note body is the default"},
			{Name: "to-project", Type: String, Desc: "Create the task on this project's board instead of the note's own (§4.2)"},
			{Name: "also", Type: Strings, Desc: "Another note on this board to fold into the same task (repeatable)"},
			{Name: "validation", Type: Strings, Desc: "An acceptance criterion, as a command and what its output must show (repeatable)"},
		},
	},
	{
		Name: "note.fold", CLI: []string{"note", "fold"}, MCP: "note_fold",
		Short: "Point a note at a task that already exists (ask the operator first)",
		Long: "For the note filed AFTER the task that covers it: it ends on that task without a second one being created.\n" +
			"A note whose own task exists is refused, naming the task holding it, rather than being repointed. `note unfold` is the way back.",
		Gated:   "tasks.note_fold",
		Who:     "The operator, or an agent that has confirmed it with them first, for the reason note.promote is: this is the same decision about a second note (§3.7).",
		Mutates: true,
		Args: []Arg{
			idArg("The note id or number"),
			{Name: "into", Type: String, Desc: "The task id or number to fold it into", Required: true},
			{Name: "to-project", Type: String, Desc: "The board that task lives on, when it is not the note's own (§4.2)"},
		},
	},
	{
		Name: "note.unfold", CLI: []string{"note", "unfold"}, MCP: "note_unfold",
		Short:   "Undo a fold and return the note to the inbox (ask the operator first)",
		Long:    "The way back from a fold that was a mistake, without deleting the row: the note is undecided again and promotable on its own. The note a task was PROMOTED from does not unfold — the task was made from its body.",
		Who:     "The operator, or an agent that has confirmed it with them first; undoing a fold is the same authority as making one (§3.7).",
		Ungated: "a fold is gated on the way in, and a gate that could park the way back would leave a wrong fold standing",
		Mutates: true,
		Args:    []Arg{idArg("The note id or number")},
	},
	{
		Name: "note.keep", CLI: []string{"note", "keep"}, MCP: "note_keep",
		Short:   "File a note as approved but not now (ask the operator first)",
		Who:     "The operator, or an agent that has confirmed this verdict with them first; `note_verdict` proposes it without asking anyone (§3.7).",
		Ungated: "filing a note for later puts nothing on the board; note.promote is gated because it does create a task, which a freeze can usefully hold",
		Mutates: true,
		Args: []Arg{
			idArg("The note id or number"),
			{Name: "reason", Type: String, Desc: "Why"},
		},
	},
	{
		Name: "note.drop", CLI: []string{"note", "drop"}, MCP: "note_drop",
		Short:   "Reject a note (ask the operator first)",
		Who:     "The operator, or an agent that has confirmed this verdict with them first; rejecting a peer's idea outright is not a call an agent makes alone (§3.7).",
		Ungated: "rejecting a note ends it where it stands and creates no work; note.promote is gated because it does create a task, which a freeze can usefully hold",
		Mutates: true,
		Args: []Arg{
			idArg("The note id or number"),
			{Name: "reason", Type: String, Desc: "Why"},
		},
	},
	{
		Name: "note.delete", CLI: []string{"note", "delete"}, MCP: "note_delete",
		Short:   "Remove a note that is still in the inbox",
		Who:     "The author, or the operator, and only an inbox note (§5.7). This one still REFUSES another agent: erasing a peer's idea is wrong however the operator answers.",
		Ungated: "an inbox note is pre-decision, and its author or the operator removing it decides nothing",
		Mutates: true,
		Args:    []Arg{idArg("The note id or number")},
	},
	{
		Name: "parked.list", CLI: []string{"parked", "list"}, MCP: "parked_list",
		Short: "List actions the policy gate deferred",
	},
	{
		Name: "parked.resolve", CLI: []string{"parked", "resolve"}, MCP: "parked_resolve",
		Short:   "Run or reject a deferred action (ask the operator first)",
		Long:    "Resolving re-runs the verb under the original subject, never the resolver's (§9.3), and the row records who resolved it.",
		Who:     "The operator, or an agent that has confirmed it with them first; overruling the gate that stopped you is exactly the call to bring to the operator (§3.7, §9.3).",
		Ungated: "this verb IS the gate's own resolution; gating it would defer the answer to the deferral",
		Mutates: true,
		Args: []Arg{
			idArg("The parked action id"),
			{Name: "reject", Type: Bool, Desc: "Reject it instead of running it"},
		},
	},
	{
		Name: "events", CLI: []string{"events"}, MCP: "events",
		Short:       "Stream the append-only event trail",
		AllProjects: true,
		Long: "Without --since this reads from the BEGINNING: the answer, and a\n" +
			"--follow stream alike, start with the whole trail, oldest event first.\n" +
			"--follow only waits for something new once that backlog is drained, so a\n" +
			"consumer that resumes has to pass --since <event id or unix ms> or it\n" +
			"sees every event again on every restart.",
		Args: []Arg{
			{Name: "since", Type: String, Desc: "An event id or a Unix-millisecond timestamp to resume from"},
			{Name: "entity", Type: String, Desc: "task or note"},
			{Name: "limit", Type: Int, Desc: "Stop after this many"},
		},
	},
	{
		Name: "doctor", CLI: []string{"doctor"}, MCP: "doctor",
		Short: "Report version, dirs, socket, Herdr, hooks, gate and anything degraded",
	},
	{
		Name: "sweep", CLI: []string{"sweep"}, MCP: "sweep",
		Short:   "Release leases that have lapsed, or every lease a pane holds",
		Long:    "The daemon does this on a timer (§11.5). Run it by hand, or from a Herdr\nevent reaction, when a pane died and its work should return to the queue now.",
		Who:     "With --pane: that pane, or the operator — another pane's leases are still REFUSED, because they are that holder's, not the operator's to grant on their behalf. Without: anyone, and it releases only leases that are already expired.",
		Ungated: "the daemon's own §11.5 timer calls it, and a gate that parked the timer would stop leases coming back at all",
		Mutates: true,
		Args: []Arg{
			{Name: "pane", Type: String, Desc: "Release every lease this Herdr pane holds, expired or not"},
		},
	},
	{
		Name: "dump", CLI: []string{"dump"}, MCP: "dump",
		Short:       "Print the whole store as JSON, every project included (§3.5)",
		AllProjects: true,
	},
	{
		Name: "stop", CLI: []string{"stop"}, MCP: "stop",
		Short: "Stop the daemon",
		Long: "The daemon answers this call and then ends itself the way SIGTERM ends it (§2.5):\n" +
			"it stops accepting, lets the calls already in flight finish, and gives up the socket\n" +
			"and the lock. The next call starts a fresh one, so this is how the plugin is turned\n" +
			"off rather than how work is stopped.",
		Who: "The operator, from a process with no pane. A call from a pane is REFUSED, and not on " +
			"the ground that the caller is not the operator (§3.7): one daemon serves every pane of " +
			"this user (§2.3), so ending it takes the board away from panes that never asked, which " +
			"is not the operator's to grant away on their behalf — the same ground `sweep --pane` " +
			"refuses another pane's leases on.",
		Ungated: "the gate is how the operator holds a verb back from an agent, and this verb reaches no agent to hold back",
		Mutates: true,
	},
}

// Help is the description both doors show: the one-line Short, the longer
// prose where a verb has it, and the principal rule. §3.7 made an operator
// verb advice an agent confirms rather than a refusal a door makes, which
// leaves the rule with nowhere to be enforced and everywhere to be READ — so
// it is rendered rather than kept as registry metadata, in one text a human
// reads in `--help` and an agent reads in a tool description. Two wordings
// would be two rules.
func (v Verb) Help() string {
	out := v.Short
	if v.Long != "" {
		out += "\n\n" + v.Long
	}
	if v.Who != "" {
		out += "\n\nWho: " + v.Who
	}
	return out
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
