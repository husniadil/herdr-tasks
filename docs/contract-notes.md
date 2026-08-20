# Contract notes

Gaps found while implementing the shared plugin contract, v0. The rule is in
`CLAUDE.md`: where implementation shows a contract rule is wrong or
unimplementable as written, record it here and follow the contract until it is
amended upstream. Nothing here is a licence to diverge quietly.

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

## §8.4 / §11.5 — event names are spelled with underscores

The contract writes Herdr's event names with dots (`pane.exited`,
`pane.agent_status_changed`). Herdr's own `EventKind` enum spells them with
underscores (`pane_exited`, `pane_agent_status_changed`). `Schema.Has` matches
either spelling rather than picking a side, since §11.3 says use Herdr's names
and §8.4 uses the other ones.

## §8.4 — no manifest `[[events]]` reaction yet

§8.4 lets a plugin react to Herdr events through the manifest. What a plugin's
`[[events]]` command receives — argv substitution, environment variables, or
stdin — is not specified by the contract and is not documented anywhere this
implementation could check, and `pane.exited` carries only a pane id, so a
reaction that cannot read that id is not a reaction.

Rather than guess a substitution syntax, the manifest declares no `[[events]]`
block. Leases are freed by the bounded timer §11.5 explicitly allows, by the
reconciliation sweep at daemon start (§8.4's stated exception), and by the
`sweep` action and `ht sweep --pane <id>` verb for "that pane died, give its
work back now". When the manifest's event contract is written down, the
reaction is a two-line addition.

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

## §12.3 — what "never the operator's Herdr" costs in practice

The throwaway server in `internal/e2e` needs more than a private
`HERDR_SOCKET_PATH`. A `herdr server` started with only that still restores the
default session's persisted state — the operator's live workspaces and panes
appear on the private socket. Isolation needs a throwaway `HERDR_SESSION` as
well, and this suite also overrides `HOME` and the XDG dirs, because Herdr puts
a named session's state under `<config>/herdr/sessions/<name>/` regardless of
`HERDR_CONFIG_PATH`. Recorded because "private socket" reads like enough and
is not.
