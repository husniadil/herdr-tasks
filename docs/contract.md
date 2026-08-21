# The shared plugin contract

Status: binding. Version: 0.5.0. Date: 2026-08-21.

Changes in 0.5.0: §5.1 and §10.1 stop resolving `state_dir` and `config_dir`
from `HERDR_PLUGIN_STATE_DIR` / `HERDR_PLUGIN_CONFIG_DIR`, and forbid it.
Herdr injects those only into what Herdr itself spawns, so the old text gave
one plugin two stores; both store-carrying plugins written against it had
already diverged the same way, independently. §11.4 states what delivery by
`agent prompt` guarantees — best-effort, no receipt — and requires a plugin
that delivers by prompt to keep an authoritative store the recipient can read
having never seen the prompt.

Changes in 0.4.0: §7.1 names a tool by its verb alone. An MCP client
namespaces a server's tools under the label it wired the server in as, so a
plugin prefix on the tool itself spends the agent's attention saying the same
word twice.

Also in 0.4.0: §2.1 owes a TUI only where the plugin's concern includes
an operator-facing view. A plugin without one names its human surface instead;
a status verb is enough. The sentence was written when the only plugin was one
that owns a board.

Changes in 0.3.0-draft, from live testing of herdr-tasks' pane and delivery
paths: §11.4 slash-command delivery rules (measured); §11.6 plugin-pane
mechanics — the `./` command rule, the attached-client requirement, and the
plugin-pane principal; §8.4 rewritten once the `[[events]]` payload contract
was measured — event reactions are now sanctioned, self-filtered, idempotent,
and paired with the sweep.

Changes in 0.2.0-draft, all from the first implementation (herdr-tasks):
§5.5 event-table naming follows the entity table; §5.9 write-time text bounds;
§6.1 reconciled with §7.3 (CLI is total, MCP is a pinned subset); §8.4 event
names use the schema's spelling and manifest `[[events]]` is not yet usable;
§11.2 names the schema document's shape; §11.5 spelling; §13.1 plugin ids no
longer carry the umbrella name.

This contract governs a set of independent Herdr plugins that together
replace an earlier generation of tools. Each plugin is its own repository,
its own binary, its own SQLite file. What makes them one system is this contract, not
shared code. A plugin that follows every MUST below can be composed with any
other such plugin through CLI, MCP, events, and hooks alone.

Sections are numbered so tests and READMEs can cite them (`§4.2`). A MUST is a
conformance requirement; a SHOULD is the default that a plugin may deviate from
with a written reason in its README. Later versions are additive only once a
section is marked stable.

Not covered by this contract, on purpose: backward compatibility with the
tools these plugins replace; Telegram or any chat gateway; a web dashboard;
multi-machine sync; hosts without Herdr. See §15.

---

## §1 Scope and layering

§1.1 Herdr is the only terminal substrate. A plugin MUST NOT drive tmux, zmx,
or any other multiplexer, and MUST NOT spawn PTYs of its own. Everything that
touches a terminal goes through the Herdr socket API or the `herdr` CLI.

§1.2 Herdr is the agent registry. A plugin MUST NOT maintain a registry of
running agents. Pane identity, agent name, detected harness, agent status, and
native session reference come from Herdr (§3).

§1.3 Herdr is the human UI. A plugin's human surface is a Herdr plugin pane
(`[[panes]]`) and its CLI. A plugin MUST NOT require a browser or a web server
for its core function.

§1.4 Plugins compose by calling each other's CLI or MCP, by subscribing to each
other's events (§8), and by the hooks in §9. A plugin MUST NOT import another
plugin's code or read another plugin's SQLite file.

§1.5 The layering, from the bottom: Herdr (panes, agents, workspaces, events)
→ plugins (each owns one concern: tasks, dispatch, mail, schedule) →
the umbrella project (this contract, the glossary, and later the `kit`
module and the installer that composes the plugins).

## §2 Language, binary, process model

§2.1 Every plugin is written in Go and ships one statically linked binary named
after the plugin's short name (§13.2). The same binary provides the daemon, the
CLI and the MCP server: `<name> daemon`, `<name> mcp`, and one subcommand per
verb. A plugin whose concern includes an operator-facing view MUST also provide
that view as `<name> tui [<view>]` in the same binary. A plugin with no such
view MUST NOT ship an empty one; it names the human surface it does have
instead, and a read-only status verb (`<name> status`) is enough.

§2.2 The daemon is the only writer of the plugin's SQLite file. CLI and MCP
talk to the daemon over a Unix socket at `<state_dir>/<name>.sock`. A CLI
invocation that finds no live socket MUST start the daemon and wait for it,
bounded (3 s with backoff is the reference), rather than fail.

§2.3 There is one daemon per user, not per Herdr session or per workspace. Data
is scoped by project (§4); a Herdr session is just a caller.

§2.4 The Herdr manifest MUST declare `[[startup]]` that starts the daemon and
exits, and `[[actions]]` `stop` and `restart`. Herdr has no shutdown hook; the
`stop` action is the only way to turn a plugin off. The daemon MUST survive the
pane that opened it being closed.

§2.5 Every subcommand is non-interactive by default and reads nothing from a
TTY unless it is `tui`. Long-running subcommands (`events --follow`, `wait`)
exit cleanly on SIGINT and SIGTERM.

## §3 Identity and principals

§3.1 A principal is a string `<kind>:<id>` naming who acts. Kinds in this
revision:

| kind | id | meaning |
|---|---|---|
| `agent` | Herdr pane id (e.g. `wF:p1`) | an agent running in a Herdr-managed pane |
| `human` | (none; the literal `human`) | the operator |
| `cron` | job id | a schedule job acting on its own |
| `trigger` | trigger id | a webhook / watcher firing |
| `plugin` | plugin short name | a plugin acting on its own behalf (sweeps, hooks) |

§3.2 A caller's principal is derived, never declared. A CLI or MCP call made
from a process whose environment carries `HERDR_PANE_ID` is `agent:<that id>`.
A call without `HERDR_PANE_ID` is `human`. A call that wants to act as `cron`,
`trigger`, or `plugin` MUST pass an explicit `--as <principal>` flag, and a
plugin MAY refuse `--as` for principals it does not own.

§3.3 Child processes inherit `HERDR_PANE_ID`. A subagent, a shell, or a second
`claude` spawned from inside a pane is the same principal as the pane. This is
the Herdr model (one pane, one seat) and is a feature: recusal (§6.6) is
decided per harness, not per process.

§3.4 An `agent` principal carries three facts that a plugin MUST snapshot at
the moment they matter (claim, submit, review, send) by calling
`herdr agent get <pane>` through `HERDR_BIN_PATH`:

- `name` — the Herdr agent name (human-readable, may change; snapshot it),
- `harness` — Herdr's detected agent kind (`claude`, `codex`, `pi`, ...),
- `session` — the native session reference if Herdr has one
  (`agent_session`), otherwise null.

A plugin MUST NOT invent a harness from argv or a process name when Herdr has
an answer, and MUST store `harness = "unknown"` rather than guess when it has
none.

§3.5 There are no session keys, tokens, or HMAC identities in this revision.
The boundary is the local user account: whoever can open the socket is trusted
as the user. A plugin MUST document this in its README and MUST create its
state dir and socket with mode 0700/0600.

§3.6 Agents outside Herdr-managed panes (a `claude` in a plain terminal) are
`human` to every plugin. This is a deliberate limit of this revision, not a
bug to work around.

## §4 Scope: project and workspace

§4.1 The unit of data scoping is `project`: the canonical absolute path of the
git common dir's parent (`git rev-parse --path-format=absolute --git-common-dir`,
then its parent), so every worktree of a repository shares one project. A
directory that is not a git repository is its own project by canonical path.
Symlinks are resolved. The project key is stored as the path string; a plugin
MAY also store a display name (`basename`) but MUST NOT use it as a key.

§4.2 Project is resolved per call, in this order: explicit `--project <path>`,
then `HERDR_PLUGIN_CONTEXT_JSON`'s focused pane cwd or workspace cwd when the
caller is a Herdr plugin command, then the caller's working directory.

§4.3 `workspace_id`, `tab_id`, and `pane_id` from Herdr are context, not
scope. A plugin MAY record them on a row for display and navigation and MUST
NOT use them as the partition key of a table.

§4.4 Every table that holds user-visible entities has a `project TEXT NOT
NULL` column. List verbs default to the resolved project and accept
`--all-projects`. Cross-project references (a dependency, a `discovered_from`)
are an error unless a verb explicitly documents otherwise.

## §5 Storage

§5.1 One SQLite file per plugin at `<state_dir>/<name>.db`, WAL mode,
`busy_timeout` 3000 ms, foreign keys on. `state_dir` is
`${XDG_STATE_HOME:-~/.local/state}/<name>`, overridden only by the plugin's own
`<NAME>_STATE_DIR` (§10.1). A plugin MUST NOT resolve `state_dir` from
`HERDR_PLUGIN_STATE_DIR`. Herdr injects that variable into the processes it
spawns itself — the manifest's `[[startup]]`, `[[actions]]` and `[[panes]]` —
and into no others; a managed pane, where the agents and the MCP servers run,
never carries it. A plugin that honours it therefore holds two stores that
never see each other's rows, one per spawn path, which reaches the operator as
data loss. One plugin, one store, whoever started the process.

§5.2 A `meta` table with at least `schema_version INTEGER` and
`created_at INTEGER`. Migrations are numbered, append-only, and run at daemon
start inside one transaction each. A daemon that finds a newer schema than it
knows MUST refuse to start with a clear message, not downgrade.

§5.3 All timestamps are `INTEGER` Unix milliseconds UTC. No seconds, no ISO
strings in storage (render ISO only at the presentation edge). This is a hard
rule because a seconds/milliseconds mix-up shipped once already.

§5.4 Entity ids are ULIDs stored as 26-char text. A plugin MAY additionally
expose a per-project human-friendly `seq` integer (hird style) for humans and
agents to type; the ULID remains the identity.

§5.5 Every entity table has a sibling append-only events table named after
the entity table itself (`tasks` → `tasks_events`)
(`id, entity_id, at, actor, kind, detail`) written in the same transaction as
the mutation. It is the audit trail and the source of `events --follow` (§8).

§5.6 Concurrent writers are serialized by the daemon. Mutations that can race
with a human editing in the TUI accept `--base-updated-at <ms>` and fail with
`CONFLICT` when the row moved. Claims and other one-winner transitions are a
single conditional `UPDATE ... WHERE <state guard>`; zero rows changed is
`CONFLICT`, never a silent no-op.

§5.7 Nothing is hard-deleted except rows that never left their initial state
(a note still in `inbox`, a task never claimed). Everything else is
`cancelled` or `archived` with a timestamp.

§5.8 `<name> dump --json` prints the whole store as JSON. A plugin whose data
cannot be read without the plugin is not acceptable.

§5.9 Verbs that accept free text SHOULD enforce a documented length bound with
`USAGE` at write time. A consumer that renders stored text into a bounded
artifact (a `/goal` condition, a notification) MUST clamp at render time
regardless, and say what it dropped.

## §6 Verbs, CLI, and error envelope

§6.1 Every verb is a CLI subcommand with `--json`. A pinned subset of verbs is
also exposed as MCP tools (§7.3); where a verb is on both surfaces its name,
arguments, and result shape are identical — generate both doors from one verb
registry rather than maintaining two. A parity test MUST enumerate both
surfaces and fail when they drift.

§6.2 With `--json`, stdout carries exactly one JSON document: the result on
success, or `{"error":{"code":"<CODE>","message":"<text>"}}` on failure.
Progress and diagnostics go to stderr. Without `--json`, output is for humans
and MUST NOT be parsed.

§6.3 Error codes and exit statuses are shared by every plugin:

| code | exit | meaning |
|---|---|---|
| `USAGE` | 2 | caller-validatable input error |
| `NOT_FOUND` | 3 | the named entity does not exist in scope |
| `UNAVAILABLE` | 4 | daemon, Herdr, or a required binary unreachable |
| `TIMEOUT` | 5 | a bounded wait elapsed |
| `CONFLICT` | 6 | state guard failed: claimed by someone else, row moved, duplicate |
| `UNSUPPORTED` | 7 | the host or Herdr lacks a capability this verb needs |
| `FORBIDDEN` | 8 | caller principal may not perform this on this target |
| `DENIED` | 9 | the policy gate (§9) said no |
| `UNEXPECTED` | 1 | anything else |

Codes are never repurposed; new codes are appended. A plugin MAY define
sub-reasons inside `message`, never new top-level codes outside this list
without a contract bump.

§6.4 Verbs that wait (`wait`, `--wait`) take `--timeout <duration>` and return
`TIMEOUT` with whatever partial state is known. Verbs never block forever by
default.

§6.5 A non-zero exit of a command the plugin ran *for the caller* (a hook, a
gate command) is a result, reported in the JSON, not the plugin's own failure.

§6.6 Recusal, where a plugin has review semantics: a principal MUST NOT review
work produced by the same `harness` as its own (§3.4). `human` is exempt. The
check is by harness, not by pane or session, because two panes of the same
model are one model reading its own homework.

## §7 MCP

§7.1 `<name> mcp` serves MCP over stdio. A tool is named by its verb alone,
with dots as underscores and no plugin prefix (`claim`, `submit`,
`note_add`): an MCP client already namespaces a server's tools under the name
it registers, so the plugin's identity belongs in that registration and not in
every tool. The tool list is pinned by a test and is semver-bound once
released.

§7.2 MCP is a thin door over the same daemon calls as the CLI; it holds no
state of its own. The server's `instructions` string MUST say, in one
paragraph, what the plugin is, that pane/agent/workspace refer to Herdr, and
which verbs are the usual entry points.

§7.3 A plugin SHOULD keep its MCP tool count small (roughly 8–16) and push
rarely used verbs to the CLI, which the skill teaches. The CLI is the primary
agent surface; MCP exists for discoverability and namespace grounding.

§7.4 Tool results are the same JSON as `--json` CLI output. Errors are tool
errors carrying the §6.3 code, never JSON-RPC protocol errors.

## §8 Events and loose coupling

§8.1 Every plugin emits events for every state change, named
`<name>.<entity>.<verb>` in past tense (`tasks.task.claimed`,
`tasks.note.promoted`). The payload is `{id, at, actor, project, entity, kind,
detail}` where `detail` is verb-specific JSON.

§8.2 `<name> events [--follow] [--since <id|ms>] [--json]` streams events from
the `_events` tables (§5.5). `--follow` is the subscription primitive other
plugins and humans use; there is no push bus in this revision.

§8.3 Each plugin supports one configurable hook command per event class in its
config (§10): `on_event = ["cmd", "args"...]`, run detached with all three
stdio closed, environment `<NAME>_EVENT`, `<NAME>_ENTITY`, `<NAME>_ID`,
`<NAME>_PROJECT`, `<NAME>_ACTOR`, plus event-specific keys. A hook that fails
MUST NOT fail the write that caused it. This is the hird model and it is enough
for this revision.

§8.4 Herdr event names are spelled exactly as its schema prints them
(`pane_agent_status_changed`, `pane_exited`, ...); a plugin matches that
spelling and invents no synonyms. The manifest's `[[events]]` reaction is
usable, and its payload contract is (measured in Herdr's source and in the
wild): the hook command runs with `HERDR_PLUGIN_EVENT` (the event name),
`HERDR_PLUGIN_EVENT_JSON` (the full event envelope), `HERDR_PLUGIN_CONTEXT_JSON`
(the subject's workspace/tab/pane context), and `HERDR_PANE_ID` when the event
has a pane subject. A hook fires for EVERY matching event, not only the
plugin's own subjects, so it MUST self-filter and be idempotent. Event
reactions complement — never replace — the reconciliation sweep at daemon
start and the bounded timer, because hooks can be missed while the daemon or
Herdr is down. A plugin MUST NOT poll Herdr for state an event or the timer
already covers.

## §9 Policy gate

§9.1 Every verb that changes the world (claim, submit, approve, dispatch,
send, schedule, run) passes through one function
`gate(subject principal, verb string, target string) → allow | deny | defer`
before doing anything.

§9.2 The gate is configured, not built in. Unconfigured: allow. Configured to a
command: the command receives `{subject, verb, target}` on stdin and prints
`{"decision":"allow"|"deny"|"defer"[, "reason":...]}`. Any failure to get a
well-formed answer — unreachable, non-zero, malformed, oversized, unknown
decision — is `deny`. The gate fails closed.

§9.3 `defer` means "park it": the plugin records a parked action
(`subject, verb, target, payload, state`) and returns `DENIED` with
`parked_id`; only `human` may later resolve or reject it. Resolving re-runs the
verb under the original subject, never the resolver's.

§9.4 Verb names are `<name>.<verb>` (`tasks.approve`, `dispatch.peer`). A
plugin lists its gated verbs in its README so a future policy plugin can name
them.

## §10 Configuration

§10.1 Config is TOML at `<config_dir>/<name>.toml`, where `config_dir` is
`${XDG_CONFIG_HOME:-~/.config}/<name>`. Environment overrides use the prefix
`<NAME>_` (uppercase short name), and `<NAME>_CONFIG_DIR` is the only override
of `config_dir`. A plugin MUST NOT resolve `config_dir` from
`HERDR_PLUGIN_CONFIG_DIR`, for the reason §5.1 gives: Herdr injects it only
into what Herdr spawns, so honouring it splits one plugin's configuration
along the same seam that splits its store. A plugin
reads config at daemon start and on SIGHUP; the CLI reads only what it needs to
find the socket.

§10.2 Config never holds secrets. A plugin that needs one reads a file path or
an environment variable name from config and dereferences it at use.

§10.3 `<name> doctor` prints: version, state dir, config dir, socket liveness,
Herdr reachability and the Herdr schema/protocol it saw, hook and gate
configuration, and anything degraded. It never fails.

## §11 Herdr integration rules

§11.1 Call Herdr through `HERDR_BIN_PATH` (fallback `herdr` on PATH) or the
socket at `HERDR_SOCKET_PATH`. Never hard-code a socket path.

§11.2 Feature-detect, never pin. At daemon start read `herdr api schema --json`
once and decide which requests and events exist. The document is a JSON Schema:
request methods are the `const` values under
`schemas.request.oneOf[].properties.method`, event kinds are the enum at
`schemas.event.$defs.EventKind`; also accept a flat
`{"requests": [...], "events": [...]}` document so a simpler future shape keeps
working. A missing capability is
`UNSUPPORTED` at the verb that needs it, with the capability named; it is not a
refusal to start. Pinning an exact protocol number is a contract violation.

§11.3 Use Herdr's names. `pane`, `tab`, `workspace`, `agent`, `agent_status`
(`idle|working|blocked|done|unknown`) mean what Herdr says they mean. Do not
introduce `seat`, `instance`, or `session` as synonyms for pane.

§11.4 Delivering text to an agent uses `herdr agent prompt <pane> <text>
[--wait --until <status>]`; delivering to a plain shell uses `herdr pane run`.
Plugins MUST NOT implement their own type-verify-retype loops.

Slash commands are not prompts. `agent prompt` submits one bracketed paste,
and an agent TUI collapses a multiline or long paste into a placeholder its
command parser never sees — the text is then executed as a plain prompt
(measured with Claude Code: a short one-line `/goal` registers; the same
condition multiline, or ~3k chars on one line, does not). A plugin that must
start an agent under a slash command MUST flatten it to one line and pass it
as the initial-prompt argv of `agent start` (argv bypasses the composer; a
3.2k one-liner registers; Herdr rejects argv containing newlines). For an
agent already running there is no reliable programmatic path for a long slash
command — hand it to the human.

Delivery by `agent prompt` is best-effort and carries no receipt. A successful
call says Herdr accepted the text, never that the agent read it or acted on
it; the collapse rule above is one way accepted text reaches nothing, and a
pane that exits between the call and the agent's next turn is another. A
plugin that delivers by prompt MUST keep an authoritative store the recipient
can read having never seen the prompt, and the prompt MUST be a hint pointing
at that store rather than the only copy of what it carries. A plugin MUST NOT
treat a successful `agent prompt` as delivery, and MUST NOT wait on a receipt
Herdr does not offer.

§11.5 Liveness of an `agent` principal is `herdr agent get` plus the
`pane_exited` / `pane_closed` events. A plugin with leases (claims) sweeps them
on those events and on a bounded timer, and records the sweep in the entity's
`_events`.

§11.6 Opening a pane for a human: `[[panes]]` with `placement = "popup"` for
transient views (a board, an approval list) and `overlay`/`split` for
long-lived ones, following Herdr's existing UI language. The TUI is
mouse-first.

Mechanics, all measured: a manifest command that names a file in the plugin
root MUST start with `./` — a bare relative argv0 is PATH-searched by Herdr's
spawner and the pane silently fails to open. Popup and overlay panes require
an attached client and target its active pane; they cannot be opened on a
headless server, so nothing automated may depend on one appearing. A plugin
pane's process receives `HERDR_PLUGIN_CONTEXT_JSON` (workspace, tab, and
focused-pane ids and cwds — the §4.2 scope source) and `HERDR_PLUGIN_ROOT`,
but no `HERDR_PANE_ID`: a plugin pane is therefore the `human` principal, and
a pane that offers verbs MUST offer human verbs only.

## §12 Testing and conformance

§12.1 Three layers, in order of obligation: (1) the state machine and all
transitions, unit-tested with no daemon, no socket, no Herdr — mandatory from
the first commit; (2) CLI and MCP against a fake Herdr (recorded socket
responses) — mandatory before a plugin is used by another plugin; (3) end to
end in a headless `herdr server` with a named throwaway session — before a
release tag.

§12.2 A test MUST cite the contract section it enforces in its name or a
comment (`// §5.6`). A future `kit` conformance suite will assert every MUST
above is cited by at least one test in each plugin.

§12.3 Tests never touch the operator's live Herdr session, state dir, or
config dir. State and config dirs in tests are temp dirs; Herdr in tests is a
throwaway server.

## §13 Naming and versioning

§13.1 Repositories are `herdr-<short>`; the plugin id in the Herdr manifest is
the repository name (`id = "herdr-tasks"`); the binary is the short name or an
agreed abbreviation that is unique on a developer machine and not a common Unix
command. Nothing a plugin commits — files, manifest, README, git history —
names the umbrella project; a plugin cites "the shared plugin contract" and
its version.

§13.2 Short names in this revision: `tasks`, `dispatch`, `mail`, `schedule`.
The binary abbreviations are decided per plugin and listed in the glossary
(§14).

§13.3 Semantic versioning. Before 1.0 the CLI, MCP tool list, JSON shapes, and
error codes are stable within a minor version and may change between minors
with a CHANGELOG entry. From 1.0 they are additive only. `<name> version`
prints the version; consumers that need a floor check it and fail with
`UNAVAILABLE` naming the floor.

§13.4 This contract is versioned separately. A plugin declares the contract
version it satisfies in its README and in `doctor` output.

## §14 Glossary

Shared vocabulary. A term used in CLI help, MCP descriptions, schema, or docs
MUST be one of these or a plugin-local noun that does not collide with them.

- **project** — the git-root scope (§4.1).
- **workspace / tab / pane** — Herdr's terms, Herdr's ids.
- **agent** — the program Herdr detected in a pane; `harness` is its kind.
- **principal** — who acts (§3.1).
- **task** — a unit of work with a lifecycle and a claim.
- **note** — a pre-decision idea on a board; becomes a task, is kept, or is
  dropped.
- **claim / lease** — one principal's exclusive hold on a task, time-bounded.
- **evidence** — what a submitter attaches to prove a task is done.
- **review / verdict** — approve or reject on submitted work; reject carries
  feedback.
- **recusal** — the rule that a harness does not review its own work.
- **goal** — a paste-ready `/goal` condition derived from a task (§16).
- **formula** — a template that instantiates several tasks with dependencies.
- **dispatch** — starting an agent in a pane with an argv and a prompt.
- **message** — an ask, notify, or reply between principals.
- **job / trigger** — a schedule or an external signal that runs an action.
- **gate / parked** — the policy check and its deferred result (§9).
- **event** — an append-only record of a state change (§8).

Binary abbreviations (§13.2), the names a developer types:

- **htask** — the binary of the `tasks` plugin.
- **hdis** — the binary of the `dispatch` plugin.
- **hmail** — the binary of the `mail` plugin. `mail` itself is a common Unix
  command, which §13.1 forbids as a binary name.

Forbidden in APIs and schemas: `sidebar`, `card`, `row`, `widget`, `seat`,
`instance`, `session` (except Herdr's own `agent_session`).

## §15 Non-goals of this revision

No compatibility with the data, APIs, or floors of the tools these plugins
replace. No Telegram or other chat gateway. No web dashboard. No PTY or web
terminal. No blackboard/TTL scratchpad. No autopilot. No files/apps/browser.
No multi-machine sync. No session keys. No host without Herdr. Each of these
is a separate decision later, taken only when its absence is felt in daily
use.

## §16 Tasks as `/goal` runs

This section binds only plugins that own tasks, but it is here because it
shapes the task data model.

§16.1 A task's acceptance criteria (`validation[]`) MUST be expressible as
proofs an evaluator can check from a transcript: a command and what its output
must show. The TUI and CLI nudge toward that shape.

§16.2 `task goal <id>` prints a paste-ready `/goal` condition under 4,000
characters: directive from the title, context from the description and the
latest task notes and last reject feedback, "Done when" from the criteria plus
the obligation to run `task submit <id> ...` and show its output, and a stop
clause that runs `task release <id> --note "<what is left>"` and files
out-of-scope findings as notes or `--discovered-from` tasks.

§16.3 The claim lease is renewable (`task touch <id>`) and the skill instructs
the agent to renew at the start of each turn. Pane death releases the lease
(§11.5) with the last note preserved.

§16.4 One task per session is the default; a "work the queue" goal is the
documented alternative for small tasks and formula runs.
