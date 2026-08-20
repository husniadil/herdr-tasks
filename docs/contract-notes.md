# Contract notes

Gaps found while implementing the shared plugin contract, v0. The rule is in
`CLAUDE.md`: where implementation shows a contract rule is wrong or
unimplementable as written, record it here and follow the contract until it is
amended upstream. Nothing here is a licence to diverge quietly.

## §5.1 / §10.1 — the store is resolved without Herdr's injected dirs

§5.1 says `state_dir` is `HERDR_PLUGIN_STATE_DIR` when set, else
`${XDG_STATE_HOME:-~/.local/state}/<name>`, and §10.1 says the same for
`HERDR_PLUGIN_CONFIG_DIR`. Followed literally, that gives one plugin two
stores.

Herdr injects those variables into the processes IT spawns — the manifest's
`[[startup]]`, `[[actions]]` and `[[panes]]` — and injects neither into a
managed pane, where the agents and the MCP servers run. Measured on a live
system: the daemon Herdr started carried
`HERDR_PLUGIN_STATE_DIR=~/.local/state/herdr/plugins/herdr-tasks`, while the
daemon and MCP servers running in panes carried only `HERDR_PANE_ID` and fell
through to `~/.local/state/tasks`. Both paths held a `tasks.db` and a live
socket. The popup board therefore opened an empty database and rendered
nothing, and the `[[actions]]` entries — including "Stop the tasks daemon" —
acted on the daemon that held no data.

So this plugin does not read `HERDR_PLUGIN_STATE_DIR` or
`HERDR_PLUGIN_CONFIG_DIR` at all. `TASKS_STATE_DIR` and `TASKS_CONFIG_DIR` are
the overrides (§10.1's `TASKS_` prefix), then the XDG bases, then `~`. Ignoring
them rather than lowering their precedence is deliberate: any order that still
consults them splits the store again for whichever surface Herdr does inject.

What this costs, recorded honestly: Herdr can no longer place this plugin's
state, so a second or sandboxed Herdr no longer gets a separate store for free
(`TASKS_STATE_DIR` still buys one deliberately), and if Herdr ever cleans up
`~/.local/state/herdr/plugins/<id>` on uninstall, this plugin's data will
outlive that. Both were weighed against a failure that looks like data loss to
the operator, and the operator chose this.

`doctor` names a `tasks.db` left at the old path as a second store not in use,
and says whether it holds rows. It never deletes one.

## §11.2 — the shape of `herdr api schema --json`

The contract says to read the schema once and decide which requests and events
exist, without naming the document's shape. What Herdr actually prints is a
JSON Schema:

- request methods are the `const` of `schemas.request.oneOf[].properties.method`
  (90 of them at protocol 19: `agent.get`, `agent.prompt`, `pane.run`, …),
- event kinds are the enum at `schemas.event.$defs.EventKind`.

`internal/herdrclient` reads both, and also accepts a flat
`{"requests": [...], "events": [...]}` document so a future Herdr that
simplifies this keeps working. No protocol number is pinned; the number is read
and shown in `doctor` output only.

## §8.4 / §11.5 — event names are spelled both ways, and which one depends

The contract writes Herdr's event names with dots (`pane.exited`,
`pane.agent_status_changed`). Herdr's own `EventKind` enum spells them with
underscores (`pane_exited`, `pane_agent_status_changed`). Both are right, in
different places, and the rule is now known rather than guessed:

- a manifest `[[events]]` hook's `on` is validated against
  `plugin_hook_event_names()`, which maps the hookable kinds through
  `EventKind::dot_name()` — so the manifest takes **dots**, and an underscore
  spelling is rejected when Herdr loads the plugin, not here;
- the `EventKind` enum in `herdr api schema --json` uses **underscores**.

`Schema.Has` goes on matching either spelling, which is right for a document
whose own two halves disagree. The manifest does not get that latitude: it is
validated against one list, and `herdr-plugin.toml` spells its hooks with dots.

## §8.4 — what a manifest `[[events]]` command receives

This entry used to say the payload was unspecified and undiscoverable, and
that the manifest therefore declared no `[[events]]` block. That was wrong,
and a note asserting a blocker that does not exist is how a capability stays
unused. Read out of Herdr's own source
(`src/app/api/plugins/runtime.rs`, `context.rs`, `manifest.rs`), a hook
command receives its payload as **environment variables**:

- `HERDR_PLUGIN_EVENT` — the event name,
- `HERDR_PLUGIN_EVENT_JSON` — the whole event envelope,
- `HERDR_PLUGIN_CONTEXT_JSON` — the invocation context,
- `HERDR_PANE_ID` — for pane events, plus `HERDR_WORKSPACE_ID` and
  `HERDR_TAB_ID` when the context has them.

There is **no argv substitution**. A `command` argv containing
`"$HERDR_PANE_ID"` receives that literal string, so a reaction that needs an
id has to be a script that reads the environment. Ours is
`scripts/on-pane-gone.sh`.

Which pane `HERDR_PANE_ID` names, since "focused pane" and "the pane the event
is about" are not obviously the same thing and a sweep aimed at the wrong one
would take work off a live agent: for `pane.closed` the context is built
directly from the closed pane's id; for `pane.exited` it resolves through the
event's own pane, and yields **nothing** if the pane has already left Herdr's
state. So the variable is either the event's pane or absent — never a
different live pane. The script guards the absent case and releases nothing.

Hooks fire for every subject, not only the plugin's own panes, so a reaction
must be safe to run for a pane it knows nothing about and safe to run twice.
Ours is both by construction rather than by filtering: `ht sweep --pane <id>`
releases the leases that one pane holds, which is nothing for a pane that
holds none, and nothing again the second time.

Leases are still also freed by the bounded timer §11.5 allows and by the
reconciliation sweep at daemon start. The hook makes the common case immediate
instead of up to a lease-length late.

## §6.1 — parity between a small MCP surface and the full CLI

§6.1 says every verb is a CLI subcommand **and** a matching MCP tool. §7.3 says
keep the MCP tool count to roughly 8–16 and push rarely used verbs to the CLI.
Read literally, together, they cannot both hold for a plugin with 30 verbs.

This plugin reads §7.3 as the narrowing rule and §6.1 as the no-drift rule:
every verb is a CLI subcommand; a chosen subset is also an MCP tool; and where
a verb appears in both, the name, the arguments and the result shape are
identical because both doors are generated from one registry
(`internal/verbs`). The parity test enumerates both surfaces and fails on any
difference, including a tool taking an argument the CLI does not.

## §5.5 — `<entity>_events` table naming

§5.5 names the sibling table `<entity>_events`, which for entities named `task`
and `note` reads as `task_events` / `note_events`. The tables here are
`tasks_events` and `notes_events`, plural, matching the entity tables they sit
beside (`tasks`, `notes`). No behaviour depends on the spelling; recorded so a
future conformance suite that greps for the name is not surprised.

## §3.4 — `herdr agent get` takes no `--json`, and wraps its answer

Herdr 0.8.0's `agent get <target>` always prints JSON and rejects a `--json`
flag with a usage error. The plugin passed the flag, and because §3.4 says to
store `harness = "unknown"` rather than guess, the usage error came back as
"unknown" for every claim instead of as a loud failure — a fallback hiding a
failure, which the working agreements call a bug by definition. Layer 3 is
what caught it; the flag is gone and both the wrapped answer
(`{"result":{"agent":{…}}}`) and a bare object are read.

Two further shape notes, from the same pass:

- `agent_session` is an object in Herdr (`{"kind":"id","value":"…"}`) where
  §3.4 describes a reference. It is flattened to its `value`.
- An agent declared over the CLI (`pane report-agent`) gets no `agent_session`
  at all in this Herdr, so §3.4's third fact is legitimately null there. The
  end-to-end test asserts the snapshot stays empty rather than inventing one.

## §11.5 — proving lease release when a pane dies, end to end

The layer-3 test closes the claiming pane through the real Herdr and then runs
`ht sweep --pane <id>`, the same pass a `pane.exited` reaction would run, and
asserts the task is back in `todo` with a `swept` event on its trail. That is
the nearest provable equivalent while the manifest carries no `[[events]]`
reaction (see the §11.5 note above): what is not yet proved end to end is
Herdr *delivering* the event, not what the plugin does when it has it.

## §11.6 — what a popup is, and what it is not

Three facts measured while probing whether a popup can suspend into an editor.
All three were found in a throwaway named session with an attached client,
because a popup needs one (§11.6) and the operator's session is out of bounds
(§12.3).

A popup is **not addressable as a pane**. It does not appear in
`herdr pane list`, and `herdr plugin pane close` takes a pane id — so a plugin
cannot find and close its own popup over the API. Opening one again while it
is up answers `popup already open`, which is how `scripts/open-pane.sh`
recognises the idempotent case.

`herdr plugin pane open` has a second refusal besides that one:
`ui_busy — popup panes can only open from the normal workspace view`, returned
whenever the client is in any mode but `Terminal`
(`src/app/api/plugins/mod.rs`, the placement check). A palette, a picker or a
restored startup screen is enough. So the board's keybinding legitimately does
nothing at those moments; the script passes the message through rather than
treating it as success, which is the right side to err on.

A plugin pane's environment is the **server's**, not the operator's shell and
not the pane the popup was opened from. Measured with `$EDITOR`: the value the
popup saw was the one exported by the shell that started the herdr session. A
herdr launched from a login shell therefore carries that shell's editor, and
one started by a launcher or a service may carry none at all — which is why
`ht tui` reads `VISUAL` then `EDITOR` and refuses, naming both, rather than
falling back to `vi`. An operator who cannot leave the editor a plugin chose
for them is in the trap §11.6's close key exists to have closed.

## §12.3 — what "never the operator's Herdr" costs in practice

The throwaway server in `internal/e2e` needs more than a private
`HERDR_SOCKET_PATH`. A `herdr server` started with only that still restores the
default session's persisted state — the operator's live workspaces and panes
appear on the private socket. Isolation needs a throwaway `HERDR_SESSION` as
well, and this suite also overrides `HOME` and the XDG dirs, because Herdr puts
a named session's state under `<config>/herdr/sessions/<name>/` regardless of
`HERDR_CONFIG_PATH`. Recorded because "private socket" reads like enough and
is not.
