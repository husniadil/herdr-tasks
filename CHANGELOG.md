# Changelog

What a consumer of `htask` has to change between released versions. §13.3 of
the shared plugin contract makes the CLI, the MCP tool list, the JSON shapes
and the error codes stable within a minor and changeable between minors with an
entry here, so every entry says what moved and what a caller does about it.

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
