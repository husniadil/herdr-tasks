# Changelog

What a consumer of `htask` has to change between released versions. §13.3 of
the shared plugin contract makes the CLI, the MCP tool list, the JSON shapes
and the error codes stable within a minor and changeable between minors with an
entry here, so every entry says what moved and what a caller does about it.

## Unreleased

## 0.9.0 — 2026-08-28

`list` is a listing. A row of `task list` — `htask list`, the `list` MCP tool,
`htask list --json` — no longer carries `description`, `report`, `evidence`,
`evidence_for`, `feedback` or `release_note`. Those are free text and they were
almost all of a finished board's bytes: 92 done tasks answered 1.6 MB, and a
consumer reading `list` across several projects paid that for each of them.

Everything else on the row is where it was, under the same keys: `id`, `seq`,
`project`, `title`, `status`, `priority`, `validation`, `discovered_from`,
`deps`, `blocked`, `blocked_by_cancelled`, `created_by`, `created_at`,
`updated_at`, the `claimed_by*` snapshot with `claimed_at`, `lease_until` and
`ever_claimed`, `released_at`, the `submitted_by*` snapshot with `submitted_at`,
`amended_at`, `amend_count`, `reviewed_by`, `completed_at`, `cancelled_at`,
`archived_at`, and `pane_id`, `tab_id` and `workspace_id`. The envelope keeps
`count`, `project` and `elsewhere`, and `--ready`, `--mine`, `--status`,
`--query`, `--archived` and `--limit` filter exactly as they did — `--query`
still matches the description, which is read on the board rather than answered.

`get` is unchanged and answers the task in full, so a caller that reads a body
off a listing reads it with one `get` for the task it opens. That is the whole
migration: nothing was renamed, no verb moved, and the six keys exist on `get`
as they always did.

## 0.8.3 — 2026-08-27

The `failed` event a `parked resolve` writes when the re-run errors now carries
`detail.on_behalf_of_operator: true` on the same terms its `resolved` and
`rejected` siblings already did (§3.7): the mark is there when the principal
that resolved it is not the operator, and absent when the operator resolved it
themselves. The key sits beside the existing `error` detail rather than
replacing it, and the actor is the deciding principal as it was. A consumer
reading the trail sees one added field on an event it already receives.

## 0.8.2 — 2026-08-27

The declared contract revision is now 0.10.1, up from 0.10.0. It is the value
`doctor --json` reports as `contract`, and 0.10.1 asks nothing new of this
plugin: it writes down what the plugins already do, and the two divergences it
closes here — `parked list` staying project-scoped, and §16's verb spellings —
were questions about the document rather than about behaviour. No verb, no flag
and no error code moved with it, so a caller that does not read the revision
has nothing to do about this one.

A `parked resolve` by a principal that is not the operator now carries
`detail.on_behalf_of_operator: true` on the `resolved` or `rejected` event it
writes, which is the mark the five note verbs already wrote (§3.7). The actor
is the deciding principal as it was, `resolved_by` on the row is unchanged, and
a resolve the operator performed carries no such key. A consumer reading the
trail sees one added field on events it already receives, and nothing it read
before has moved.

## 0.8.1 — 2026-08-27

`htask mcp --project <path>` is now the board the MCP door serves. The flag was
accepted and then ignored: every tool call that named no `project` resolved
against the door's own working directory, which for a server started by a
client is the CLIENT's directory and not the operator's, so a door wired to one
repository answered about another. A call that passes `project` still wins
(§4.2), and `all_projects` is unchanged. A client that worked around this by
passing `project` on every call has nothing to change; one that set the flag
and expected it to hold now gets what it asked for.

Two refusals and a guard, all correctness, and nothing shipped changes shape.
A call the §9 gate parks now carries its `base_updated_at` guard on the parked
row, so the re-run a resolve performs is checked against the version the caller
wrote the call against; the guard rode on the request alone, the re-run carried
0, and a deferred update overwrote whatever had moved while the call sat
parked. A resolve whose parked payload names an argument the verb no longer
declares is refused with `USAGE` instead of being re-run with that argument
dropped, which reported a call that did not happen as one that did; it is the
same refusal a live call gets. And `htask note fold` into a task that is `done`
or `cancelled` is refused with `CONFLICT`: a fold ends the note on the task that
carries it, an unfold will not bring it back, so the idea would be gone for
good — fold into a task that will still be worked, or keep the note. A caller
folding into open tasks and resolving calls parked by this daemon has nothing
to change.

## 0.8.0 — 2026-08-25

Breaking, CLI only. The task verbs are spelled flat: `htask claim 12`, not
`htask task claim 12`. Every verb of the task group moved to the top level —
`create`, `get`, `list`, `claim`, `touch`, `release`, `submit`, `amend`,
`approve`, `reject`, `cancel`, `update`, `archive`, `delete`, `goal` — so the
CLI's shape matches the MCP door's bare tool names and the binary's own name
stops being repeated. The `note` and `parked` groups stay groups, spelled with
a space.

A caller that spells `htask task <verb>` is refused with `USAGE`, the same way
any unknown command is. Drop the word `task` from the call site. The MCP tool
names do not move, so a client that reaches the board through MCP has nothing
to change.

## 0.7.0 — 2026-08-24

Nothing shipped is repurposed or removed, and a caller that only reads fields
has nothing to change. What moves is what a call is ALLOWED to be: three
things a door accepted are refusals here, which makes this a surface change
and not a patch. The MCP tool list goes from 33 to 34 with `stop`, so a client
that pins the list gains an entry. `--all-projects` is declared per verb
(§4.4) and refused with `USAGE` on every verb that does not read it — a caller
that passed it to one of those was answered as though the fleet had been
searched when only its own board was — and the verbs that DO reach past the
board include the transitions, which resolve a ULID across boards the way
`task get` already did. `--as` without an id is refused the same way: a
principal is `<kind>:<id>` (§3.1), so bare `--as cron` no longer writes a whole
class of caller into the trail as the actor. So is a filter value outside the
vocabulary — `--status`, a note's `--kind`, and `events --entity` — where the
answer was an empty list, which reads as a fact about the board rather than as
a question nobody could answer. A task or note reference may be written `#12`
as well as `12`, which is the form every rendering here prints. And a reject
hands back a claim WITH a lease, so a worker that dies after a rejection is
swept like any other (§6.5) rather than holding the row until a human releases
it by hand.

One new verb on both doors, `htask stop` / the `stop` MCP tool: the daemon
answers the call and then ends itself the way SIGTERM ends it — stop accepting,
finish what is in flight, give up the socket and the lock (§2.5). The tool list
goes from 33 to 34, so a client that pins it gains an entry; nothing shipped
changes meaning. `scripts/stop.sh` now runs it instead of `pkill`, which is why
a call in flight is finished rather than cut. Two things a caller should know:
`stop` is REFUSED with `FORBIDDEN` from a pane, because one daemon serves every
pane of this user (§2.3) and ending it takes the board away from panes that
never asked; and no door starts a daemon in order to stop it — `htask stop`
with nothing listening prints that and exits 0, while the MCP tool answers
`UNAVAILABLE`.

A hard-deleted task or note leaves its events behind. §5.5 calls the events
tables append-only, and the delete removed the entity's rows with it, so the
one operation the trail most needed to survive was the one that erased it. A
consumer of `events` now sees entity ids that name no live row, which is what
an append-only trail across a §5.7 delete looks like; `events --entity task
--json` reads them like any other.

`task archive` on an already-archived task answers `CONFLICT`. The second call
moved `archived_at` to now and appended a second `archived` event, so the row
answered "when was this hidden" with the last time anyone asked and the trail
said it happened twice. A caller that archives defensively reads the code and
treats CONFLICT as "already done".

Three new `--json` fields on a parked action, `tab_id`, `workspace_id` and
`all_projects`, all `omitempty`. They are what the door derived rather than
what the caller passed, and §9.3 re-runs a resolved verb as the ORIGINAL call
— without them a task created through the gate was filed with a pane of origin
and no tab or workspace beside it, and a call made with `--all-projects` was
re-run against the resolver's board alone. Schema version 10 adds the columns;
a row parked before it reads back absent, which is what a call with no tab, no
workspace and no `--all-projects` looks like.

The declared contract revision is now 0.10.0, up from 0.6.0. It is the value
`doctor --json` reports as `contract`, and a caller that reads it to decide
which contract's rules this daemon answers to sees a different answer than it
did under 0.6.0. No verb and no error code moved with it, so a caller that does
not read the revision has nothing to do about this one.

`claimed_by_harness` is absent from a task nobody holds. Releasing, sweeping,
cancelling and approving clear the whole §3.4 claim snapshot instead of some of
its five fields, so a row whose `claimed_by` is empty no longer carries the previous
holder's harness beside it. The field is `omitempty`, so what a caller sees is
the key disappearing rather than emptying. Nothing read it wrongly before —
the human renderer checks `claimed_by` first and recusal turns on the session —
so a caller that reads it on a row somebody holds is unaffected, and one that
reads it on a released row should read it as absent, which is what it always
meant.

## 0.6.0 — 2026-08-23

Additive for every caller; nothing shipped changes meaning. A caller that
parses a task row gains two fields and may keep ignoring them.

One new verb on both doors, `task amend` / the `amend` MCP tool: the holder of
a task in review replaces its report, `evidence` and `evidence-for` without
touching the submission. `submitted_at`, `submitted_by`, the submitter's
harness and session, and the status all stay as `submit` left them, so §6.6
still recuses the session that produced the work. It exists because the
self-review lane asks a worker to keep fixing what a mutation proves unpinned:
the lane deliberately produces commits after the submit, and until now the only
record of the newer head was a message in a different store.

Two new `--json` fields on a task, `amended_at` and `amend_count`, absent on a
row that was never amended. Schema migration 9 adds the two columns; a row
written by an older binary reads back with neither, which means never amended.

A reviewer who read a row and then approves against what they read is refused
by the §5.6 `--base-updated-at` guard, because an amendment moves
`updated_at`. That guard is not new and is still opt-in — what is new is that
an amendment trips it.

## 0.5.0 — 2026-08-23

Additive for every caller; nothing shipped changes meaning. An agent gains
verbs it was refused before, so a caller that relied on those refusals to keep
an agent out has to move that rule into the policy gate (§9).

Eight new MCP tools, which is every verb the door did not carry: `archive`,
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
