# Changelog

What a consumer of `htask` has to change between released versions. §13.3 of
the shared plugin contract makes the CLI, the MCP tool list, the JSON shapes
and the error codes stable within a minor and changeable between minors with an
entry here, so every entry says what moved and what a caller does about it.

## 0.5.0 — 2026-08-23

Additive for every caller; nothing shipped changes meaning. An agent gains
verbs it was refused before, so a caller that relied on those refusals to keep
an agent out has to move that rule into the policy gate (§9).

Ten new MCP tools, which is every verb the door did not carry: `archive`,
`delete`, `note_keep`, `note_drop`, `note_delete`, `parked_resolve`, `sweep`
and `dump`. §7.3 admits no CLI-only verb, so the CLI and the MCP door now
serve the same 32 verbs. No tool was renamed or removed; the names already on
the list have not moved since 0.2.0.

`note promote`, `note fold`, `note unfold`, `note keep`, `note drop` and
`parked resolve` no longer refuse a principal that is not the operator. The
authority is still the operator's; it is advice an agent confirms with the
user before acting, and the plugin does not check that the confirmation
happened. A caller that wants one of these verbs genuinely withheld from a
principal configures the policy gate to deny it — that was always where
withholding belonged, and it is now the only place it lives.

Two new `--json` fields, both added and neither repurposed. An event whose
verb is the operator's carries `detail.on_behalf_of_operator: true` when the
actor is not the operator; the actor itself is always the calling principal
and is never recorded as `human`. A parked action carries `resolved_by`, the
principal that ran or rejected it, which §9.3 needs because resolving re-runs
the verb under the ORIGINAL subject.

CLI help and MCP tool descriptions now carry a `Who:` line saying who may call
a verb and what an agent owes before calling it. Same text on both doors, from
the one registry.

The store moves to schema version 8 for the `resolved_by` column. A store this
daemon has opened is refused by an older one, which is the §5.2 rule and not
new.

## 0.4.0 — 2026-08-23

Additive for every caller; nothing shipped changes meaning.

Three new MCP tools — `note_promote`, `note_fold`, `note_unfold` — and two new
CLI verbs, `htask note fold` and `htask note unfold`. `note promote` gains
`--also`, which takes further notes on the same board and folds them into the
task it creates instead of leaving them undecided. `note fold --into <task>`
does the same for a note filed after the task already existed. A note whose own
task exists is refused, naming the task holding it, rather than being
repointed; `note unfold` returns a folded note to the inbox without deleting
the row, and the note a task was PROMOTED from does not unfold.

The tool list grew past the 8–16 range the old §7.3 asked for, deliberately:
0.7.0 removed that budget, and these are the verbs it was keeping off the door.

The note JSON gains one field, `folded`, present only when true. A note that
reached its task by promotion does not carry it, which is how every note
written before this release reads. Nothing was repurposed or removed.

The store moves to schema version 7 for the column behind it. A store this
daemon has opened is refused by an older one, which is the §5.2 rule and not
new.

## 0.3.0 — 2026-08-23

Breaking for one caller: an MCP door registered in a harness that stands in no
Herdr pane. Such a door is `none`, a caller with no principal, and every verb
reserved for the operator refuses it with `FORBIDDEN`. In 0.2.0 it was
`human`, the operator, because §3.2 read a missing `HERDR_PANE_ID` that way.

Declare such a door once, where it is registered, by starting it as
`htask mcp --operator`. The flag is read when the server starts and never from
a tool call; a call that passes `operator` is refused with `USAGE`, and a
declared door that starts inside a Herdr pane refuses to start at all.

Nothing changes for the CLI, or for a door running inside a pane. A CLI
invocation is one process per call, so `htask note promote 12` from a plain
terminal is the operator exactly as before.

The JSON shape did not change; the values did. `created_by`, `author`, `actor`
and `principal` can now hold `none`, which no earlier release could produce. A
consumer matching on the principal set should read an unknown value as "not
identified" rather than falling through to a default.

The vendored contract moves to 0.8.0 for the §3.2, §3.7, §7.3 and §7.5
amendments behind this. The declared revision stays at 0.6.0 while §7.3's
parity MUST is open; see `docs/contract-notes.md`.

## 0.2.0 — 2026-08-21

Breaking, MCP only. A tool is now named by its verb alone: `create`, `list`,
`get`, `claim`, `touch`, `release`, `submit`, `approve`, `reject`, `goal`,
`note_add`, `note_list`, `note_verdict`, `events`, `doctor`. The `tasks_`
prefix every tool carried in 0.1.0 is gone, because an MCP client already
namespaces a server's tools under the label it registered the server as.

A client holding the 0.1.0 names calls tools this server does not serve. Drop
the prefix at the call site: `tasks_claim` becomes `claim`, `tasks_note_add`
becomes `note_add`. Nothing else about a tool changed — same arguments, same
results, same error codes. Clients that reach the server through its
registration label (`mcp__herdr-tasks__claim`) get the new name from the server
and need no edit.

The CLI did not move. `htask task claim 12` is the same command it was.

The vendored contract moves to 0.4.0-draft for the §7.1 amendment behind this.

## 0.1.0 — 2026-08-21

First release. One binary, `htask`, that is the daemon, the CLI, the stdio MCP
server, and the operator's TUI. Tasks move todo → doing → review → done behind
a claim with a renewable lease, evidence and review; notes are pre-decision
ideas the operator promotes or drops. Declares the shared plugin contract at
0.3.0-draft.
