# Contract notes

Gaps found while implementing the shared plugin contract. The rule is in
`CLAUDE.md`: where implementation shows a contract rule is wrong or
unimplementable as written, record it here and follow the contract until
`docs/contract.md` is amended. The contract is maintained in agamemnon
(`docs/contract.md`) and vendored here as a byte copy, so an amendment is a
change there, citing the § it changes and bumping the revision its Status line
states, re-vendored to every copy in the same change. Nothing here is a licence to diverge quietly.

## §5.1 / §10.1 — the store is resolved without Herdr's injected dirs

**Closed in contract revision 0.5.0.** This was a recorded divergence against
0.4.0 and earlier, where §5.1 said `state_dir` is `HERDR_PLUGIN_STATE_DIR`
when set, else `${XDG_STATE_HOME:-~/.local/state}/<name>`, and §10.1 said the
same for `HERDR_PLUGIN_CONFIG_DIR`. Followed literally, that gave one plugin
two stores. 0.5.0 amends both sections to forbid reading those variables, so
what this plugin does IS the contract now and nothing below is a deviation.
The measurement is kept because it is the evidence the amendment rests on.

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

What this costs, recorded honestly: Herdr does not place this plugin's state,
so a second or sandboxed Herdr does not get a separate store for free
(`TASKS_STATE_DIR` buys one deliberately), and if Herdr ever cleans up
`~/.local/state/herdr/plugins/<id>` on uninstall, this plugin's data will
outlive that. Both were weighed against a failure that looks like data loss to
the operator, and the operator chose this — and 0.5.0 makes that choice the
rule for every plugin, on the evidence that the second store-carrying plugin
written against the old text reached the same answer on its own.

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

The payload is specified and discoverable, and the manifest declares
`[[events]]` blocks that rely on it. Read out of Herdr's own source
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
Ours is both by construction rather than by filtering: `htask sweep --pane <id>`
releases the leases that one pane holds, which is nothing for a pane that
holds none, and nothing again the second time.

Leases are still also freed by the bounded timer §11.5 allows and by the
reconciliation sweep at daemon start. The hook makes the common case immediate
instead of up to a lease-length late.

## §6.1 — parity between a small MCP surface and the full CLI (0.2.0-draft to 0.6.0; superseded)

**Historical.** This entry records how the two clauses were read while §7.3
still asked for a tool budget. 0.7.0 removed the budget and 0.10.0 removed the
last reason a verb had for staying off the door, so the reading below is not
this plugin's any more. What replaced it is at the end of this entry. Kept
because a conformance record that deletes its own earlier readings cannot be
checked against the binaries that shipped under them.

§6.1 said every verb is a CLI subcommand **and** a matching MCP tool. §7.3 said
keep the MCP tool count to roughly 8–16 and push rarely used verbs to the CLI.
Read literally, together, they could not both hold for a plugin with 30 verbs.

This plugin read §7.3 as the narrowing rule and §6.1 as the no-drift rule:
every verb was a CLI subcommand; a chosen subset was also an MCP tool; and
where a verb appeared in both, the name, the arguments and the result shape
were identical because both doors are generated from one registry
(`internal/verbs`).

Since 0.10.0 there is no subset and no narrowing rule: every one of the 34
verbs is a CLI subcommand and an MCP tool, so §6.1 and §7.3 say the same thing
and neither has to give. The one registry and the parity test survive
unchanged and are what the entry was really about — `TestCLIAndMCPSurfacesDoNotDrift`
still fails on any difference between the two surfaces, including a tool
taking an argument the CLI does not, and `TestEveryVerbIsOnBothDoors` with
`TestEveryCLIVerbReachesTheMCPDoor` now fail on an absence from either.

## §5.5 — `<entity>_events` table naming

§5.5 names the sibling table `<entity>_events`, which for entities named `task`
and `note` reads as `task_events` / `note_events`. The tables here are
`tasks_events` and `notes_events`, plural, matching the entity tables they sit
beside (`tasks`, `notes`). No behaviour depends on the spelling; recorded so a
future conformance suite that greps for the name is not surprised. The third,
`parked_events`, sits beside `parked` and needs no pluralising to land on the
same shape.

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

### §3.4 and §6.6 — absent is not unresolved

§3.4's third bullet says the session is the native reference "if Herdr has
one … otherwise null", and §3.4's "store unknown rather than guess" sentence is
about the *harness*. §6.6 then says an `agent_session` "the plugin could not
resolve" is stored as unknown. Read together those are two different facts, not
one rule stated twice: Herdr answering with no `agent_session` is an ANSWER and
records null, while a snapshot that never landed records unknown. `sessionOf`
in `internal/tasks/task.go` had collapsed them and stamped `"unknown"` for
both, which wrote absence down as a value — the mistake §3.7 removed for
`human` — in the one field §6.6 recuses on.

The signal separating them is the harness: `Daemon.actor` seeds `"unknown"` and
overwrites it only from a reply Herdr gave, so an unresolved harness means an
unresolved session and §6.6's unknown-matches-unknown blip case still recuses.
Pinned by `TestClaimRecordsAnAbsentAgentSessionAsAbsentNotUnknown` in
`internal/tasks/task_test.go`, inside `make test-full`. It was
`TestAgentGetSnapshotIsTakenFromRealHerdrAtClaim` in layer 3 that caught it,
and layer 3 is not in the gate, which is why the pin lives in layer 1.

No contract text needed amending: both paragraphs are right as written.

## §11.5 — proving lease release when a pane dies, end to end

Both halves are now proved against a real headless Herdr, and they are
different halves.

`TestLeaseIsReleasedAfterTheClaimingPaneDies` closes the pane and runs
`htask sweep --pane <id>` itself — the MANUAL pass, which is what an operator
runs and what the manifest's reaction runs on their behalf. It is worth
keeping on its own because `scripts/on-pane-gone.sh` exits early when Herdr
gives it no pane id, and then this is the only way the work comes back.

`TestClosingAPaneReleasesItsLeasesWithoutBeingAsked` links this plugin into
the throwaway Herdr so the manifest's own `[[events]]` reactions are
registered, closes the pane, and waits for the claim to come back with nobody
asking. Measured on herdr 0.8.0: it comes back in well under a second. The
control matters as much as the result — with the plugin NOT linked, the same
test waits the full twenty seconds and the task is still `doing`, still held
by the closed pane. So Herdr really is delivering the event and the manifest's
reaction really is what releases the lease.

Earlier versions of this note said the manifest declared no `[[events]]`
reaction. It has declared two — `pane.closed` and `pane.exited` — since the
manifest gained them; the note and a comment in the layer-3 suite both went
stale, and nothing caught it because nothing ran the automatic path.

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
`htask tui` reads `VISUAL` then `EDITOR` and refuses, naming both, rather than
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

## §4.1 — "the git common dir's parent" is not the repository in three shapes

§4.1 defines `project` as the canonical absolute path of the git common dir's
parent, and gives the reason: "so every worktree of a repository shares one
project". Measured against real repositories built in a temp dir, the rule
serves that reason in two shapes and defeats it in three others.

Where `git rev-parse --path-format=absolute --git-common-dir` points, and what
its parent would be:

| shape | common dir | parent |
|---|---|---|
| ordinary clone | `<repo>/.git` | `<repo>` — correct |
| linked worktree | `<main>/.git` | `<main>` — correct, and the point of the rule |
| submodule | `<super>/.git/modules/<name>` | `<super>/.git/modules` |
| `--separate-git-dir` | wherever the git dir was put | the git dir's parent |
| bare repository | `<repo>.git` | the directory above it |

The submodule row is the sharp one: the key is a path inside git's own
internals, **every** submodule of one superproject collapses onto it, and it is
not reachable from either the submodule or the superproject. That is the exact
opposite of what the rule exists to do. `--separate-git-dir` collapses the same
way whenever git dirs are kept in one place, which is the usual reason to use
the flag.

This plugin therefore takes the parent **only when the common dir's basename is
`.git`**, and otherwise asks `git rev-parse --show-toplevel` for the working
tree. A bare repository answers neither and falls through to §4.1's own
not-a-repository fallback, the directory's canonical path. The two correct rows
above are unchanged, so worktrees still share one project.

Cost, recorded because it is a data-visibility change and not only a bug fix:
rows filed from a submodule or a `--separate-git-dir` clone may be keyed by the
git-internals path, and are not visible from the working-tree key this
resolves to. That key collides across every submodule of one superproject and
is reachable from neither the submodule nor the superproject, so it is not one
anything could have relied on.

Suggested amendment to §4.1: define `project` as the working tree of the
repository containing the directory — `--show-toplevel` — with the common dir
consulted only to make linked worktrees resolve to the main working tree.

## §5.4 — ids are ULIDs, and left-aligned ids are still in stores

§5.4 says entity ids are ULIDs, which is the name of a FORMAT, not a shape.
Two renderings of 26 Crockford base32 characters are in play, and only one of
them is a ULID.

A ULID is 128 bits rendered **right-aligned** in 26 five-bit characters. That
is 130 bits, so the leading two are padding and the first character carries
three significant bits — which is why a ULID's leading digit never exceeds 7.
The left-aligned rendering packs five bits at a time from the TOP, making
every id the value shifted left by two.

Measured on a left-aligned id, `06G1XTMPG597T1S9F5HZG0BC94`:

| read as | milliseconds | date |
|---|---|---|
| a spec ULID | 7148900342277 | year 2196 |
| shifted right two | 1787225085569 | 2026-08-20 11:24:45 UTC |

The second is the millisecond that id carries. A tool that decodes a
left-aligned id as a ULID — which §5.4 invites it to — reads a timestamp two
centuries out.

Ordering is unaffected by the difference: a constant shift preserves it, and
`ORDER BY id` over the trail (§5.5, §8.2) is right under either rendering. Only
an outside decoder can tell them apart, which is why the encoder alone is not
where this shows up.

**The encoder is not enough on its own.** For the same 128 bits a left-aligned
id is four times a right-aligned one, so a correctly minted id sorts BEFORE
every left-aligned one. A store holding both renderings returns its trail in
the wrong order at the boundary, and `--since <id>` skips or repeats.
Migration 3 therefore re-encodes every stored id — tasks, notes, parked, both events tables and both
of `task_deps`' columns — in ONE transaction, with foreign keys deferred to
the commit so a parent and its children move together, and a referential check
inside the transaction that refuses to commit a graph that no longer holds.

Recorded because the difference is invisible from inside: a test written
against this package's own encoder agrees with whatever that encoder does. The
format can only be checked against a decoder written from the spec, and that
decoder lives in the id tests, deliberately not derived from `encode`.

## §13.1 — what "not a common Unix command" excludes here

§13.1 lets the binary be "the short name or an agreed abbreviation that is
unique on a developer machine and **not a common Unix command**". That second
clause is a real constraint on a short abbreviation, and it is worth recording
which names it excludes on the machine this plugin is developed on.

`ht` is TeX4ht's, measured rather than assumed:

```
/opt/homebrew/bin/ht -> ../Cellar/texlive/20260301/bin/ht
# ht (2024-01-23-13:46), generated from tex4ht-mkht.tex
```

and Homebrew's own formula for that name is `hte`, a hex viewer, which brew
records as `Conflicts with: texlive (because both install `ht` binaries)`. The
name is contested between two unrelated projects before any plugin asks for
it, so an agent taught it by a skill and running it from PATH reaches one of
them. `htask` is free on PATH and in Homebrew, and is this plugin's binary.

**Three names live here and the contract keeps them apart.** They are easy to
conflate:

| what | value | fixed by |
|---|---|---|
| binary | `htask` | §13.1, an abbreviation chosen per plugin |
| plugin id | `herdr-tasks` | §13.1, the repository name |
| short name | `tasks` | §13.2 |

§10.1 fixes the env prefix as the "uppercase short name", which settles that
the `<name>` in §2.2's socket path and §5.1's database path is the SHORT name
as well. So `<state_dir>/tasks.sock`, `<state_dir>/tasks.db` and `TASKS_*` are
keyed by the short name and are independent of the binary's, which a test
asserts rather than leaving it to be discovered by an operator whose board went
missing.

**Upstream amendment to propose:** §13.2 says binary abbreviations are "listed
in the glossary (§14)". The glossary entry for this plugin should read `htask`.

## §7.1 — the registration name is the namespace, so the tools are bare

§7.1 names a tool by its verb alone and says nothing about the name an MCP
server registers itself under. Both are settled here, and they are two
different things: `ServerName` is `herdr-tasks`, the plugin id a client wires
in and labels the tools with, while the fifteen pinned tool names are
`claim`, `submit`, `list`, `note_add` and the rest. `task` is the board's
default entity and drops out of a task verb's name; `note_` stays, because it
separates two verbs rather than repeating the subject.

Recorded because "the tool list is semver-bound once released" made this a
version event rather than a rename: the binary is 0.2.0 and a client holding
the older names calls tools this server does not serve.

## §13.3 — what the declared revision is checked against

`docs/contract.md` is the contract itself, in this repository so a reader who
has only this repository can resolve the `§` citations its code and docs make.
It is a transcription: normative content is unchanged, and the only edits are
the ones this repository's own rules force — the umbrella project's name and
the tools it replaces are not named (§13.1), and the revision tag reads "this
revision" where the source wrote a version token.

The vendored document states Version 0.10.0, and `ContractVersion` in
`internal/daemon/daemon.go` says the same. `htask version`, `htask doctor` and
the README all read that one constant, and
`TestTheDeclaredRevisionIsTheVendoredOne` fails when the constant, the README
sentence and the document's own Status line stop agreeing.

0.4.0 carried the `-draft` tag through its three amendments and lost it when a
second plugin shipped against it. A draft is a revision only one implementation
has read; a second conforming plugin is what turns the text into something two
callers agree on, and the Status line says binding from that point. The
amendments themselves did not move, and the rows below that name what each one
points at were written while the tag was still there.

Declaring a revision is a conformance claim, and no test can make it. What
earns it is reading each change the contract's changelog lists against the code
that answers it. Every delta but one was derived from this plugin, so it has an implementation
to point at; the §2.1 amendment came from a peer plugin, and what it points at
here is the half of the rule this plugin answers:

| § | what the revision says | what answers it |
|---|---|---|
| §2.1 (0.4.0) | a TUI is owed only by a plugin whose concern includes an operator-facing view; one without a view names its human surface instead, and a status verb is enough | this plugin owns the operator's board, so it owes the view and ships it: `cmd/htask/tui.go` is `htask tui [<view>]`, `TestTheBoardOffersHumanVerbsOnly` holds it to human verbs, and the README names it in the first paragraph. The sentence was written when a board was the only plugin there was; a peer plugin whose operator surface is a read-only `status` verb could not declare §13.4 conformance against a MUST for a view it has no concern to draw |
| §7.1 (0.4.0) | a tool is named by its verb alone, no plugin prefix, because the client's registration label already namespaces it | `internal/verbs/verbs.go` carries the bare `MCP` names; `pinnedTools` in `internal/mcpdoor/parity_test.go` pins them and `TestTheServerNameCarriesTheIdentityAndTheToolsDoNot` refuses any plugin lead; `daemon.Version` is `0.2.0` for the semver-bound list that moved |
| §5.5 | an events table named after its entity table, written in the same transaction | `internal/store/schema.go:82` `tasks_events`, `:113` `notes_events`, with §5.5's columns; `internal/store/tasks.go:31-51` opens the transaction, appends the event, commits |
| §5.9 | write-time text bounds with `USAGE`, and a render-time clamp that says what it dropped | `internal/tasks/bounds.go`; `TestEveryTaskFreeTextFieldIsBounded`, `TestEveryNoteFreeTextFieldIsBounded`; the clamp is `internal/daemon/goal.go`, with `TestGoalSaysWhenItDroppedCriteria` and `TestGoalClipsRatherThanOverflows` |
| §6.1 / §7.3 (0.10.0) | CLI total, MCP total, one registry, a parity test that fails in both directions | `internal/verbs/verbs.go` is the one registry; `TestCLIAndMCPSurfacesDoNotDrift`, `TestMCPToolListIsPinned`, `TestEveryVerbIsOnBothDoors`, `TestEveryCLIVerbReachesTheMCPDoor`; 34 verbs, 34 tools. The `roughly 8–16` budget this row asserted until 0.10.0, and `TestMCPToolCountStaysSmall` which held it, are both gone — see the §6.1 entry above |
| §8.4 spelling | Herdr event names spelled as its schema prints them | `internal/herdrclient/client.go:173` matches either spelling, which is right for a document whose halves disagree; the manifest takes dots because Herdr validates it against dots — the entry above records which is which |
| §8.4 reaction | `[[events]]` is usable; a reaction self-filters, is idempotent, and complements the sweep | `herdr-plugin.toml` declares `pane.closed` and `pane.exited`; `scripts/on-pane-gone.sh` sweeps by pane, which is both by construction; `TestClosingAPaneReleasesItsLeasesWithoutBeingAsked` drives it against a real Herdr |
| §11.2 | the schema document's shape, and the flat form too | `internal/herdrclient/client.go` reads `schemas.request.oneOf[].properties.method` and `schemas.event.$defs.EventKind`, and the flat `{requests, events}` form; the protocol number is read for `doctor` and never pinned; `TestSchemaListsCapabilities` |
| §6.6 (0.6.0) | recusal is by principal and by agent session; the harness no longer recuses, and an unresolved session matches an unresolved session | `CheckRecusal` in `internal/tasks/task.go` compares the principal (which is the pane, §3.2) and then `sessionOf`; migration 5 in `internal/store/schema.go` adds `submitted_by_session` beside the harness. `TestApproveAllowsTheSameHarnessInADifferentSession` is the incident the amendment came from, with `TestApproveRecusesTheSamePane`, `TestApproveRecusesTheSameSessionInADifferentPane`, `TestApproveRecusesWhenBothSessionsAreUnknown`, `TestDeclaredPrincipalReviewsAcrossAnUnknownSession` and `TestRecusalIsBySessionNotByHarness` on the daemon door |
| §11.4 (0.5.0) | delivery by `agent prompt` is best-effort with no receipt, and a plugin that delivers by prompt keeps an authoritative store the recipient can read having never seen the prompt | vacuously satisfied here: nothing in this plugin delivers text to an agent, and the board IS an authoritative store a claimant reads by `htask get` without any prompt ever arriving |
| §11.4 | delivery through `herdr agent prompt`, no type-verify-retype loop | `internal/herdrclient/client.go` `Prompt`. The slash-command paragraph binds a plugin that starts an agent under one; this plugin does not — `task goal` prints a paste-ready condition for a human (§16.2), which is the branch that paragraph ends on |
| §11.5 | liveness from `pane_exited` / `pane_closed`, swept on those events and on a bounded timer, recorded in `_events` | `KindSwept` in `internal/tasks/task.go`; the timer in `internal/daemon/daemon.go`; `TestLeaseIsReleasedAfterTheClaimingPaneDies` |
| §11.6 | the `./` command rule, popups needing an attached client, a plugin pane being `human` | `TestManifestCommandsInThePluginRootSayTheyAre`; both panes are `placement = "popup"` and nothing automated opens one, so no e2e depends on a pane appearing; `internal/project/project.go` reads `HERDR_PLUGIN_CONTEXT_JSON` and never a pane id, and `TestTheBoardOffersHumanVerbsOnly` holds the pane to human verbs |
| §13.1 | the plugin id is the repository name and names no umbrella | `herdr-plugin.toml` `id = "herdr-tasks"`; `cmd/htask/manifest_test.go` |
| §14 (0.4.0) | the glossary lists the binary abbreviations §13.2 says it lists: `htask` for `tasks`, `hdis` for `dispatch` | this plugin ships the first of the two: `cmd/htask` builds `htask`, `herdr-plugin.toml` names it in `[[build]]`, and `CHANGELOG.md` is written for callers of that binary. The list was empty while two abbreviations were in use, so §13.2 pointed at a place that answered nothing; adding them keeps a promise the contract already made rather than adding an obligation, which is why the revision does not move |

One observation the table does not settle: `Prompt` has no caller. It is the
§11.4-conformant primitive and it is correct, but nothing in this plugin
delivers text to an agent today, so §11.4 is satisfied by a path that is not
exercised outside its own package.

And one thing the table itself broke, found under task 89 and fixed there.
`gapRecorded` in `cmd/htask/contract_test.go` asks whether a lagging
declaration is written down, by looking for a SINGLE paragraph naming both
revisions. A markdown table carries no blank line, so the table above is one
paragraph naming most of this document's revisions at once, and it answered
yes for pairs nobody had ever recorded: with the declaration mutated to 0.5.0
the guard stayed green. That is the same "both strings appear somewhere"
failure the paragraph anchor was introduced to close, arriving again one level
down. `gapRecorded` now drops table rows before splitting, so a recorded gap
has to be prose or a heading; the mutation to 0.5.0 fails, a mutation to 0.9.0
passes and logs the entry that records it, and leading the document still
fails.

## 0.7.0 opens a parity gap in every plugin, this one included (0.7.0 to 0.10.0; superseded)

**Historical, and CLOSED.** This entry's condition was two things: bring the
door to parity AND move the declaration. Both are now true. Parity landed
when 0.10.0 removed the last ground a verb had for staying off the door, and
the declaration moved under task 89: `daemon.ContractVersion` and the README
say **0.10.0**, the revision `docs/contract.md` states, so
`TestTheDeclaredRevisionIsTheVendoredOne` no longer takes its lag branch at
all. What earned the move is later in this file: the 0.9.0 sweep under task
86 and its completion under task 88 audited every MUST in this document
against 0.9.0's rule, and the `--as` pin this entry says was owed exists.
Kept, not deleted, because the binaries that shipped declaring 0.6.0 are
readable only against what was true then.

The 0.7.0 amendment to §7.3 requires a plugin's MCP door to serve every verb
its CLI serves. Nothing served that when it was written. This plugin served 13
of its 30 verbs, and every operator verb was off the door, so a harness with
no shell could approve a task and could not promote a note. That half is now
closed: 0.10.0 removed the only ground those absences rested on, and every
verb is on both doors, pinned by `TestEveryVerbIsOnBothDoors` and
`TestEveryCLIVerbReachesTheMCPDoor`.

The declaration lagged on purpose for four revisions, and no longer does.
While it lagged, `daemon.ContractVersion` and the README stayed at **0.6.0**
against a `docs/contract.md` stating **0.10.0**, because declaring a revision
is a conformance claim and no test can make it: declaring 0.7.0 on the day the
text was written would have claimed a door that did not exist yet, and 0.8.0,
0.9.0 and 0.10.0 each carried a claim this repository had not audited itself
against end to end. That audit has since run and the declaration moved to
**0.10.0**; the paragraph below describes the guard that held the lag, which
is now dormant rather than gone.
`TestTheDeclaredRevisionIsTheVendoredOne` was taught this one shape and no
other, and it enforces both halves rather than describing them: the declared
revision must be strictly LOWER than the vendored one, and the gap must be
named in a SINGLE entry of this file. Leading the document fails, because a
binary declaring a revision this repository does not contain is claiming
conformance to a text nobody can read. A lag with no entry fails, because this
file already names six revisions and "both strings appear somewhere" is
satisfied by almost anything — which is how the first version of the relaxed
guard let a two-revision silent lag through. Auditing the declaration forward
from 0.6.0 through 0.10.0 was its own task, task 89, and running it is what
closed this entry. Both halves of the guard still fail on a mutation with the
revisions equal, which is the point: a guard that only worked while there was
a lag was never a guard.

`TestMCPToolCountStaysSmall` is gone, earlier than this entry said it would
be. It asserted the 8–16 range of a §7.3 sentence 0.7.0 removed, and it stood
until a verb had to be on both doors: the note-fold task put `note_promote`,
`note_fold` and `note_unfold` on the door, which was 18 tools, and the stale
test would have failed the change it was written to allow. Its successor,
`TestNoToolBudgetDecidesWhichDoorAVerbReaches`, held those three verbs on both
doors while saying in its own comment that this was not yet full parity, and
it is gone too: 0.10.0 removed the last reason a verb had for staying off the
door, so there is no absence left for it to read a reason out of.

What holds the clause NOW, and what the paragraph above should be read
against, is `TestEveryVerbIsOnBothDoors` in `internal/verbs` and
`TestEveryCLIVerbReachesTheMCPDoor` in `internal/mcpdoor`: nothing may be
absent from either door, in either direction. The door serves all 34 verbs,
so the parity half of the 0.7.0 gap is CLOSED. The declaration half is closed
too: `daemon.ContractVersion` and the README state 0.10.0, the revision the
vendored document states, and the four revisions between were audited by the
0.9.0 sweep and its completion before the constant moved.

The `--as` pin now EXISTS. It is `TestAsStaysOffTheMCPDoor` in
`internal/mcpdoor/as_test.go`, the name §7.3's first draft cited before
anything answered to it. It reads the SERVED tool list off a live in-memory
session and asserts that `as` is neither a tool name nor a property of any
tool's input schema — the surface §7.3 promises, rather than the intent behind
it. What held the clause before is still there and still does its own job:
`internal/mcpdoor/mcpdoor.go:345`, where `as` carries an `Excluded` reason
citing §3.2, and `cmd/htask/render_test.go:186-193`, which asserts only that
every global has exactly one of `Property` or `Excluded`. That assertion never
held the promise and does not hold it now — it passes just as happily if a
later edit maps `as` to a property. The two fail independently, which is the
point of adding a second one: publishing `as` as a schema property fails the
new test and leaves the render assertion green, and dropping the `as` entry
from `mcpdoor.Globals` fails the render assertion and leaves the new test
green. A contract is the one document that may not cite a guarantee that is
not there, and it no longer does.

## 0.8.0 answers §3.2 and leaves §7.3's parity gap where it was

0.7.0 amended §7.3 while the identity question under it was still open, and
the two texts did not sit together: the MUST put every verb on every door,
including the operator verbs, while the `--as` exclusion beside it gave as its
reason that a shell-less harness must not gain authority it has no other way
to reach. A paneless harness derived to `human` under the old §3.2, so the
MUST handed it exactly that. 0.8.0 closes the contradiction from the identity
side rather than by taking verbs off a door.

What moved, and what answers it here:

| § | what 0.8.0 says | what answers it |
|---|---|---|
| §3.2 | the process-bound identity rule, by name: a door's principal is fixed when the process starts and cannot be learned from a call; a CLI invocation is one process per call, a server door outlives every call it serves | `TestSection32NamesTheProcessBoundIdentityRule` in `cmd/htask/contract_test.go` pins the name and the four clauses that carry it, through a new `contractSection` helper that reads one § with its hard wrapping collapsed |
| §3.7 | `human` is never the fallback for knowing nothing; a paneless door with no declaration is the literal `none`, and verbs reserved for the operator refuse it with `FORBIDDEN` | `tasks.PrincipalNone`; `Daemon.actor` in `internal/daemon/daemon.go` returns it when `PaneID` is empty and the request carries no declaration; `TestADoorWithNoPaneAndNoDeclarationHasNoPrincipal` drives `note.promote` both ways and reads the principal back out of `doctor`. `protocol.Request.Operator` is how a door says which it is, and `cmd/htask/root.go` sets it unconditionally because a CLI process is one process per call |
| §7.5 | the operator declaration: `--operator` on the server command, read once, never per call, never inside a pane | `cmd/htask/mcp.go` declares it on the `mcp` command alone and not as a persistent flag; `mcpdoor.Options` carries it from `Serve` into every handler; `checkArgs` refuses the word `operator` as an argument BY NAME, at the door, rather than letting the daemon's generic unknown-argument check stand in for it. `TestTheOperatorDeclarationNeverArrivesPerCall` holds all three — no schema offers it, a call carrying it is refused with `USAGE` before any request is built, and an undeclared door sends `false` — and `TestTheDeclaredDoorIsTheOperatorAndTheUndeclaredOneIsNot` runs the same tool call through both doors. The fourth property is two requirements with a test each: `Daemon.actor` resolves the pane before it reads the declaration, held by `TestAnInPaneDeclaredDoorIsStillThePanesAgent`, which also asserts the door really sent both facts so the test cannot pass on a door that quietly dropped the flag; and `Serve` refuses to START a declared door carrying `HERDR_PANE_ID`, held by `TestServeRefusesADeclaredDoorInsideAPane` across all four combinations of pane and declaration |
| §7.3 | the parity MUST and the `--as` exclusion rest on the process-bound identity rule instead of on two arguments that contradicted each other | `TestParityAndTheAsExclusionRestOnTheSameArgument` fails if either half stands without the other, or if either stands without the rule; the `as` entry in `mcpdoor.Globals` now records the rule rather than the old "no pane to derive one from" reason |

The parity gap above was untouched by this. At 0.8.0 this plugin served 13 of
its 30 verbs and every operator verb was off the door, so the sharpest symptom
note 60 measured — a paneless harness that could approve a task and could not
promote a note — was still here. What 0.8.0 changed is that it became safe to
close: a door that reaches those verbs is either a pane's agent, a declared
operator, or `none`, and its principal is settled before any call arrives.
0.10.0 closed it; the door serves all 34 verbs, and `none` is no longer
refused an operator verb, it is recorded as having performed one.

One consequence recorded rather than left to be rediscovered. `mcpdoor` grew
`withDeclarationHint`, which appends the missing declaration to a `FORBIDDEN`
an undeclared door meets. No verb on the door fires it today, for the same
reason the gap is open: the operator verbs are not there yet. It is tested
against a stub caller, and the first real caller arrives with parity.

Two consequences recorded rather than left to be rediscovered.

`mcpdoor` grew `withDeclarationHint`, which appends the missing declaration to
a `FORBIDDEN` an undeclared door meets. No verb on the door fires it today,
for the same reason the gap is open: the operator verbs are not there yet. It
is tested against a stub caller, and the first real caller arrives with parity.

And §7.5's fourth property was drafted as one claim — "never an escalation" —
resting on `Serve`'s startup refusal, which nothing tested: `if false &&
opt.Operator && ...` compiled and left the whole suite green. Review found it,
and found the claim too strong besides. The refusal is not what prevents the
escalation. `Daemon.actor` tests `req.PaneID == ""` before it looks at the
declaration, so a declared door inside a pane sends `Operator` true and the
daemon still resolves it to that pane's agent; deleting the startup check
changes who a call is attributed to not at all. So §7.5 now states the two
requirements separately, says which one is the guarantee and which is defence
in depth, and requires a test for each. The ordering in `actor` is the guarantee, and it is
pinned as one rather than left as an implementation detail that happens to be
in the right order.

§7.1's `serves MCP over stdio` was read and deliberately left alone. A door
being first-class is a statement about which verbs it serves, not about how
bytes reach it; the transport question a first-class surface raises is recorded
separately and is not answered by anything in 0.7.0.

## 0.9.0 binds a MUST to a test, and this repository did not yet meet it (0.9.0 to 0.10.0; superseded)

**Historical.** This entry records what was true between 0.9.0's amendment and
the sweep it asked for. The sweep has since run and its answer is later in this
file; the pointer is at the end of this entry. Kept because a conformance
record that deletes its own earlier readings cannot be checked against the
binaries that shipped under them.

The preamble added in 0.9.0 says a MUST is not satisfied by code that behaves
correctly today, only by a test that FAILS when the behaviour is removed. That
is a conformance requirement over every other MUST in the document, and at
0.9.0 nothing had audited this plugin against it. The two tests written with
the amendment, `TestAMustIsNotSatisfiedUntilATestFailsWithoutIt` and
`TestTheSkillTeachesWhichTestPinsWhichClaim`, pinned the amendment's own text
and nothing else; they did not establish that every MUST here had a pin, and
they were not evidence that it did.

So the gap was recorded rather than closed: the sweep that names each MUST and
the test that fails without it was made its own task, and until it ran, this
plugin followed 0.9.0's rule for new work without claiming conformance for old.
The declaration stayed where the 0.7.0 entry above left it. Deliberately out of
scope of the amendment and named here so nobody reads their absence as a
decision: a repo-level scanner that walks for unguarded refusals, and the
verification lane's mutation condition, which lives in a sibling plugin and
belongs on that board.

What replaced it: the sweep ran under task 86 and its result is the entry
below, "The 0.9.0 sweep: every MUST, and the test that fails without it". That
sweep did not finish the job on its own — it proved 38 MUSTs by mutation,
closed five that had no pin, and LISTED eleven it had not pinned. **Task 88 is
what closed the sweep this entry was waiting on**, answering all eleven with
thirteen further tests and two arguments; its own summary is in that entry and
says nothing is left open. So the pointer is to both: task 86 for the sweep,
task 88 for its closure. Together, that entry rather than this one is this
plugin's current answer to 0.9.0's rule, and it is what let task 89 move the
declared revision. The two
items named as out of scope in the paragraph above were not part of the sweep
and are still open.

## 0.10.0 makes an operator verb advice, and this plugin follows it

The direction came from the operator in session and is binding: an agent must
be able to help without the operator typing. `note promote`, `note fold`,
`note unfold`, `note keep`, `note drop` and `parked resolve` refused every
principal but `human`; they now perform for whoever calls, and the duty an
agent owes is to confirm with the user first. That duty has no mechanism
behind it on purpose. A verb that demanded proof of a confirmation would be
the same refusal in a different shape, so §3.7 teaches the duty and requires
an honest trail instead, and the skill teaches it where an agent reads it.

What the code does with `IsHuman` is the whole design: its job inverted from
refusing to marking. Every one of the five operator-authority verbs (promote, fold,
unfold, keep and drop) now runs through `operatorVerb` in
`internal/tasks/task.go`, which writes
`on_behalf_of_operator` into the event when the caller is not the operator and
leaves the actor as the calling principal. The alternative was to trust each
call site to remember, which is how `human` became a fallback in the first
place (§3.7's first half, 0.8.0). `TestPromoteByAnAgentSucceedsAndIsMarked`,
`TestNoteKeepAndDropByAnAgentSucceedAndAreMarked`,
`TestNoteFoldByAnAgentSucceedsAndIsMarked`,
`TestNoteUnfoldByAnAgentSucceedsAndIsMarked` and
`TestAnAgentPromotesAndFoldsThroughTheDaemon` fail if the actor becomes
`human` or the mark is dropped.

Five refusals stayed, and the reason they stayed is the boundary of the
amendment: `NoteUpdate` and `CanHardDeleteNote` guard AUTHORSHIP, and `Release`,
`Submit` and `Cancel` guard the CLAIM HOLDER's lease. Neither authority is the
operator's to grant away, so no answer they could give makes a peer's row or a
peer's work the caller's. `TestAnotherAgentStillCannotEditOrDeleteANote` and
`TestAClaimIsTheHoldersAndStillRefusesAStranger` hold them. Recusal (§6.6) was
not touched.

`parked resolve` needed one thing the other five did not. §9.3 re-runs the verb
under the ORIGINAL subject, so an agent resolving its own deferral would leave
a trail showing only its own verb running and nothing about who let it. Hence
migration 8, `parked.resolved_by`, and the §9.3 sentence requiring it.

The `NotMCP` field is gone rather than kept empty. Every one of the eight
reasons it held said a form of "this authority is the operator's", so none of
them survived the amendment, and a field with no valid reason left to hold is
dead weight the next drafter would fill in badly. The two tests task 77 added
to police those reasons — `TestEveryCLIOnlyVerbSaysWhy` in `internal/verbs`
and `TestNoToolBudgetDecidesWhichDoorAVerbReaches` in `internal/mcpdoor` —
are gone with it, replaced one for one by `TestEveryVerbIsOnBothDoors` and
`TestEveryCLIVerbReachesTheMCPDoor`. Those ask for more, not less: nothing may
be absent from either door, in either direction, so there is no absence left
for a reason to justify.

Sibling plugins are deliberately untouched, and where each one stands is its
own board's decision rather than this one's. herdr-dispatch's `stop` is
CLI-only for a reason of its own. herdr-mail has taken this direction: every
verb it serves is on its door, `dump` included, with the confidentiality
boundary that verb crosses enforced in its daemon where both doors reach it.

## 0.10.1 catches the contract up, and the declaration moves to it

0.10.1 asks nothing new of this plugin. It writes down what four plugins
already do, and three of its clauses land on divergences this file had open
against 0.10.0. §4.4 now names the entity list verbs that take
`--all-projects` and says `parked.list` is not one of them, which closes the
entry below by text rather than by any change here. §16 spells its verbs in
the §2.1 form (`htask goal <id>`), which closes the spelling entry below the
same way. §5.4, §8.4, §13.2 and §14 name facts this repository already
answered. So `daemon.ContractVersion` and the README move from **0.10.0** to
**0.10.1** in the same change that reads the delta, which is what earns a
declaration: this delta is one this plugin was already on the far side of.
What did NOT come free is §3.7's mark on `parked resolve`. That was open
against 0.10.0, it is still open in 0.10.1's text, and it is implemented here
rather than declared away — its entry below says how.

## The 0.9.0 sweep: every MUST, and the test that fails without it

The entry above headed "0.9.0 binds a MUST to a test, and this repository did
not yet meet it" recorded that nothing had audited this plugin against 0.9.0's
rule. This is that audit, run against contract text 0.10.0 with the
declaration left at 0.6.0 (moving it is a separate decision, taken with this
answer and the §7.3 `--as` pin both in hand).

Method, because it is the whole value of the list. A MUST was NOT counted as
pinned by reading its test. For each one, the behaviour was removed from the
source, the suite was run, and the pin counted only if a test actually went
red; the failing test is the one named below. Mutations that turned out to be
no-ops — an error message reworded while the refusal stayed, a `MkdirAll` mode
undone by a following `Chmod` — were re-cut until they removed the behaviour,
because a survived no-op is not a finding. The mutation set lives in the task
86 transcript rather than in the repository: it is a record of one audit, not
a suite anyone should run again.

**38 MUSTs were proven this way. Five of them had no pin until this task and
now have one. Eleven more were listed as unpinned or unproven, and task 88
closed that list: thirteen further tests, two arguments, nothing left
open.** Where a MUST is unpinned, the second question 0.9.0
actually cares about — is it a missing test, or a MUST this plugin does not
satisfy — is answered for each. Every one came back "missing test": no MUST in
this contract was found unimplemented.

### Pinned and proven

| MUST | The test that failed when the behaviour was removed |
|---|---|
| §2.1 the view is served as `<name> tui` | `TestTheBinaryServesTheViewUnderTheContractsName` (added here) |
| §2.2 a CLI call with no live socket starts the daemon, bounded | `TestCLIAutostartsTheDaemon` |
| §2.4 the manifest declares `[[startup]]` and `stop`/`restart` | `TestManifestDeclaresStartupAndTheLifecycleActions` (added here) |
| §3.2 `--as` is refused for a principal that is derived | `TestAsRefusesDerivedPrincipals` |
| §3.2 `--as cron/trigger/plugin` is refused from a pane | `TestPaneMayNotDeclareAPluginPrincipal` |
| §3.4 harness is `unknown` when Herdr has no answer, never a guess | `TestAgentGetUnknownPaneIsUnknownHarness` |
| §3.5 the state dir is 0700 | `TestEnsureStateDirIsPrivate` |
| §3.7 a paneless undeclared door is `none`, never `human` | `TestADoorWithNoPaneAndNoDeclarationHasNoPrincipal` |
| §3.7 an operator verb records the CALLING principal as actor | `TestPromoteByAnAgentSucceedsAndIsMarked` |
| §3.7 an operator verb by a non-operator is MARKED | `TestPromoteByAnAgentSucceedsAndIsMarked` |
| §4.1 the display name is never the key | `TestDisplayNameIsBasename` |
| §5.1 `state_dir` is not resolved from `HERDR_PLUGIN_STATE_DIR` | `TestHerdrInjectedDirsAreIgnored` |
| §5.2 a daemon that finds a newer schema refuses to start | `TestSchemaVersionAndRefusalToDowngrade` |
| §5.4 entity ids are 26-char ULIDs | `TestULIDIs26CrockfordChars` |
| §5.5 the event is written in the mutation's transaction | `TestTransitionWritesEventInSameTx` |
| §5.6 a stale `--base-updated-at` is `CONFLICT` | `TestBaseUpdatedAtConflict` |
| §5.7 only a row that never left its initial state hard-deletes | `TestHardDeleteOnlyNeverClaimed` |
| §5.8 `dump --json` prints the whole store | `TestDumpIsCompleteJSON` |
| §5.9 a bounded artifact clamps at render time and says what it dropped | `TestGoalSaysWhenItDroppedCriteria` |
| §6.1 the parity test fails when the two surfaces drift | `TestCLIAndMCPSurfacesDoNotDrift` |
| §6.2 a `--json` failure is exactly one error envelope | `TestJSONEnvelopeAndExitStatuses` |
| §6.3 a code's exit status is the contract's | `TestTheErrorCodeTableIsTheContractsTable` (added here) |
| §6.6 a principal does not review its own work | `TestApproveRecusesTheSamePane` |
| §7.1 a tool is named by its verb alone, with no plugin prefix | `TestMCPToolListIsPinned` |
| §7.2 the instructions say what the plugin IS | `TestTheInstructionsOpenBySayingWhatThePluginIs` (added here) |
| §7.3 every verb the CLI serves is SERVED by the door | `TestEveryCLIVerbIsServedByTheDoor` (added here) |
| §7.3 `--as` is off the door, as tool and as argument | `TestAsStaysOffTheMCPDoor` |
| §7.5 `--operator` is never accepted as a tool argument | `TestTheOperatorDeclarationNeverArrivesPerCall` |
| §7.5 the pane wins over the declaration | `TestPrincipalIsDerivedAndHarnessSnapshotted` |
| §7.5 a declared door carrying `HERDR_PANE_ID` refuses to START | `TestServeRefusesADeclaredDoorInsideAPane` |
| §8.3 a hook that fails does not fail the write | `TestEventHookRunsAndCannotFailTheWrite` |
| §9.3 the parked record carries WHO resolved it | `TestGateDeferParksAndAnAgentResolvesOnTheRecord` |
| §10.1 `config_dir` is not resolved from `HERDR_PLUGIN_CONFIG_DIR` | `TestHerdrInjectedDirsAreIgnored` |
| §11.2 a missing capability is `UNSUPPORTED`, named | `TestRequireNamesTheMissingCapability` |
| §11.4 a slash command is flattened to one line | `TestTheOneLineGoalHasNoNewlineAnywhere` |
| §11.6 a manifest command in the plugin root starts with `./` | `TestManifestCommandsInThePluginRootSayTheyAre` |
| §11.6 a pane offers human verbs only | `TestFooterVerbsAreClickableWhereTheyAreDrawn` |
| §16.1 a criterion is a proof, and the required ones are covered | `TestCitingOneCriterionMeansCitingEveryRequiredOne` |

### The five that had no pin, and what the mutation showed

Each of these was implemented correctly and answerable to nothing. The gap was
a missing test in all five; none was a MUST this plugin fails to satisfy.

**§7.3's totality.** The contract says in as many words that a plugin MUST pin
it with a test. Dropping a verb from the loop in `mcpdoor.New` that adds the
tools left every §7.3 test green, because `TestEveryCLIVerbReachesTheMCPDoor`
reads `verbs.MCPTools()` towards `verbs.All` — two registry-side lists, neither
of them the door. A registry entry is the INTENT to serve a verb; what a
harness can reach is the served tool list. `TestEveryCLIVerbIsServedByTheDoor`
now reads that list off a live session. This also closes §9.5's door half,
which is the same requirement said from the gate's side.

**§6.3's exit statuses.** Every exit assertion in the suite is written
`status != codes.Exit(codes.Forbidden)` — the observed status compared against
the same table it is meant to be checking. Changing FORBIDDEN from 8 to 1 in
`internal/codes` left the whole suite green, comments reading "want 8" and all.
A shipped, semver-bound vocabulary was answerable only to itself.
`TestTheErrorCodeTableIsTheContractsTable` reads §6.3's own table towards the
package in both directions.

**§7.2's first clause.** §7.2 requires three things of the instructions and
`TestInstructionsCoverTheRequiredGround` looks for seven words scattered
anywhere in the paragraph. Deleting the opening sentence — the one that says
what the plugin is — left it green, because every keyword appears again
further down. This is the same shape as the two instances note 72 records: an
assertion satisfied by text other than the text it is about.

**§2.1's spelling.** Renaming the subcommand from `tui` to `board` failed
nothing. `internal/tui` tests the MODEL, reached in-process, which says nothing
about the name a human or a Herdr popup action types.

**§2.4's `[[startup]]`.** Renaming the table failed nothing; the manifest tests
read the argv0 of whatever command blocks they find, so a manifest with no
startup block at all satisfied them. `stop` was equally loose, and Herdr has no
shutdown hook, which is why the contract names it.

### The eleven that were unpinned, and what each one answered

Task 88 took the list below as it stood and closed it: thirteen new tests,
each proven the way the 38 above were proven — the behaviour removed from the
source, the suite run, the pin counted only when the named test went red. Two
entries are arguments rather than tests, and both say why. Nothing on the list
was left unanswered.

Two of the mutations here are worth naming as a method note. Where a MUST is a
statement about the SOURCE — "MUST NOT drive tmux", "MUST NOT import
net/http" — removing the behaviour means adding the forbidden thing, and a
mutation that only breaks the build proves nothing. Every mutation below was
gated on `go build ./...` succeeding first, so each kill is a test failing on
what it asserts and not a compiler failing on an unused import.

| Was unpinned | Now held by | The mutation that killed it |
|---|---|---|
| §1.1 no other multiplexer, no PTY of its own | `TestHerdrIsTheOnlyTerminalSubstrate` | a package-level `tmux` reference in `internal/daemon/daemon.go` |
| §1.2 no registry of running agents | `TestNoRegistryOfRunningAgents` | an `agents` table in `internal/store/schema.go` |
| §1.3 no browser or web server for the core function | `TestNoBrowserOrWebServerInTheCoreFunction` | `net/http` imported and used in `internal/daemon/doctor.go` |
| §1.4 no sibling plugin's code or store | `TestNoSiblingPluginCodeOrStore` | a `herdr-mail` string outside a comment |
| §4.3 Herdr's ids are context, never the partition key | `TestHerdrContextIsNeverAPartitionKey` | `pane_id` added to `tasks_project_status` |
| §14 the forbidden nouns are in no schema or verb name | `TestTheForbiddenNounsAreInNoSchemaOrVerbName` | a `card` table |
| §12.2 a test cites the § it enforces | `TestEveryTestFileCitesTheContract` | every `§` stripped from `internal/tui/model_test.go` |
| §3.5's README half | `TestTheREADMEDocumentsTheTrustBoundary` | the trust-boundary sentence reworded in README.md |
| §8.4's self-filter half | `TestPaneGoneLeavesAnotherPanesWorkAlone` | `sweep --pane "$HERDR_PANE_ID"` → `sweep` in the shipped hook |
| §8.4's no-poll half | `TestHerdrIsNotPolledForWhatAnEventCovers` | a package-level ticker in `internal/herdrclient` |
| §11.4's authoritative-store and no-receipt halves | `TestNothingDeliversTextToAnAgent` | a method on `Daemon` calling `Herdr.Prompt` |
| §11.5's bounded-timer half | `TestTheBoundedTimerSweepsWithoutBeingAsked` | `d.Sweep()` dropped from `sweepLoop`'s tick |
| §13.4's doctor half | `TestDoctorDeclaresTheContractRevision` | `Contract: ""` in `internal/daemon/doctor.go` |

### Two of these pins were re-cut after a mutation survived

The table above is the second cut. A review pass mutated the pins themselves
rather than re-reading the report of them, and two of the new tests turned out
to be aimed short. Both are recorded here because a pin that misses the shape
its § most directly forbids is worse than no pin: it reads as coverage.

- **§4.3.** The first `keyDeclaration` pattern read forward from the words
  `PRIMARY KEY`, so it caught a table-level clause and every index and missed
  the COLUMN-level form, where the name sits BEFORE the words. `pane_id TEXT
  PRIMARY KEY` — the most direct shape §4.3 forbids — survived. The pattern
  now matches the column name that leads such a line.
- **§14.** `sqlIdentifier` required `ADD COLUMN` at the head of a line.
  Migrations in this store are one-line Go strings, so `ALTER TABLE tasks ADD
  COLUMN instance TEXT;` sits mid-line and every column the store has added
  since its first release was unscanned. `ADD COLUMN` is now matched wherever
  it appears.

One survival in that pass was itself false: a mutation whose anchor text did
not exist changed nothing and was reported as a survivor. Every mutation in
both rounds is now gated on the file actually differing as well as on
`go build ./...` succeeding, which is the same no-op trap task 86 recorded.

### The citation group, which was the interesting half

Task 88 was asked to take this group first and report what it found, because a
test citing a § it does not enforce is its own defect and might have been the
whole finding. It was not: in every case the cited test was doing honest work
on a NEIGHBOURING sentence of the same section, and the section had a second
sentence nobody held. The honest fix was a new test each time, and no citation
was found pointing at a section its test had nothing to do with.

- **§3.5** has two MUSTs. `TestEnsureStateDirIsPrivate` and the `cli_test`
  mode checks hold the 0700/0600 half properly. The README half — the sentence
  that tells an operator the boundary IS the local user account — was held by
  nothing, and it is the half a human depends on, because a mode nobody
  explains is a number.
- **§8.4's self-filter.** `TestPaneGoneWithNoPaneIDTouchesNobody` holds the
  no-id case. The sentence is about a hook firing for EVERY matching event, so
  the case it is actually about is pane A dying while pane B works, and that
  was untested: a reaction that swept the whole board passed both existing
  tests.
- **§8.4's no-poll.** Nothing held it and nothing can hold it completely — no
  test proves a daemon never calls Herdr too often. What is holdable is the
  layer that owns the rule: `internal/herdrclient` is the one place that talks
  to `herdr`, so a poll would be built there, and the pin fails the moment a
  loop or a timer appears in it. The limit is stated in the test's own comment
  rather than left for a reader to discover.
- **§11.4's two 0.5.0 halves** were recorded here as satisfied VACUOUSLY —
  nothing in this plugin delivers text to an agent — and that record was
  itself unheld. Holding the record is the useful thing: the day a caller
  appears, the two halves stop being vacuous and become obligations nobody
  would be reminded of. `Prompt` stays, because it is the §11.4-conformant
  primitive; the test fails when something calls it, and says what the caller
  then owes.
- **§11.5.** `TestSweepReleasesExpiredLease` calls `Sweep` itself, so what it
  proves is that a sweep releases and records. §11.5 says a plugin with leases
  sweeps them on a BOUNDED TIMER, and the wiring from the configured cadence
  to the sweep was answerable to nothing: a daemon that built the ticker and
  never ran it passed every §11.5 test in the file while an abandoned claim
  sat until someone typed `sweep`.
- **§13.1** is two claims. The id half is pinned by
  `TestTheManifestIdentityDidNotFollowTheBinary`. The other half — nothing a
  plugin commits names the umbrella project — is **argued, not tested**, and
  this is the one place where a test would be self-defeating: the only way to
  grep for a name is to write it down, and writing it down in this repository
  is the thing §13.1 forbids. The vendored contract already removes it (see
  the §13.3 entry above), so there is no name here to search for. Review holds
  this one, as it holds the sibling rule that a test cite the § it enforces.
- **§13.2** was on the list and should not have been. `TestRenamingTheBinaryMovesNoStoredPath` opens with `Name != "tasks"` as a fatal guard, and
  changing the short name fails it. The citation is correct and the pin is
  real; it reads as a guard clause rather than the point of the test, which is
  how it came to be read as unpinned. **No new test — corrected reading.**
- **§13.3** likewise. `TestTheChangelogHasAnEntryForThisVersion` holds the
  changelog clause and `cli_test`'s version test holds `version` printing the
  version. The remaining clause — a consumer that needs a floor checks it and
  fails `UNAVAILABLE` — binds a CONSUMER of this plugin, not this plugin.
  **No new test — the clause is not this repository's to satisfy.**
- **§13.4** is the README **and** `doctor`. `TestTheDeclaredRevisionIsTheVendoredOne` holds the README half. `TestDoctorAndDump` checks that a
  `contract` KEY exists, which is §10.3's requirement about doctor's shape and
  a neighbouring fact here — doctor could report an empty string and nothing
  went red. Doctor is the surface a program reads at runtime, where the README
  is not.

### One spelling the §14 pin had to decide

§14 forbids `session` in APIs and schemas "except Herdr's own
`agent_session`". This schema spells that exception `claimed_by_session` and
`submitted_by_session`: the value in both is exactly Herdr's native session
reference for the agent that acted, which is what the exception names, under a
suffix rather than the contract's compound. The pin allows the two by name
rather than by a looser `*_session` pattern, because a pattern that let any
suffix through would let a session of this plugin's own invention through with
it. This is a spelling gap, not a conformance gap, and it is recorded here
rather than papered over.

### §12.2 at file granularity, and the three files it exempts

`TestEveryTestFileCitesTheContract` holds §12.2 per FILE, not per test
function. Per function it would fire on helpers and table rows, which are not
tests enforcing a section, and a rule that has to be silenced everywhere
teaches people to silence it. Three test files hold a rule of this REPOSITORY
rather than a section of the contract and are listed with a reason, in the
shape `docs_test.go` and `annotations_test.go` already use:
`cmd/htask/deps_test.go` (the dependency budget is a CLAUDE.md
non-negotiable), `cmd/htask/annotations_test.go` (this repository's writing
rule), and `internal/e2e/wait_test.go` (layer 3's own harness helpers). The
list is checked in both directions: an entry naming a file that does not exist
fails, and so does a listed file that starts citing a section.

### One MUST deliberately not mutation-proved

**§12.3's temp-dir half.** A faithful mutation points the suite's state dir at
the operator's real one, which is the thing non-negotiable 5 forbids. The
identity half of §12.3 is pinned by
`TestTheMCPDoorTakesNoIdentityFromTheProcessThatRanTheTests`; the dirs half is
recorded as verified by reading `internal/testenv` and `internal/e2e/harness_test.go`,
not by mutation, and that is on purpose.

### One thing this audit found that is not a pin

`mcpdoor.Instructions` told an agent that "the CLI (`htask`) carries every
verb, including the ones missing here". Since 0.10.0 there are no verbs missing
there, and `TestEveryCLIVerbIsServedByTheDoor` holds that. The sentence was
stale rather than wrong-in-mechanism, and correcting served prose is a change
to the door's shipped text, so it was left for the door task that moves the
declared revision. Task 89 made that change, and it did not go in as prose
alone: `TestTheInstructionsDoNotSendAgentsToTheCLIForMissingVerbs` reads the
instructions off a live session and fails on any wording claiming a verb is
absent from this door, so the stale claim cannot return unnoticed. This item
is therefore no longer "not a pin" — it is one.

## §3.7 with §2.3 — why `stop` refuses a pane, and why that is not the refusal 0.10.0 removed

`stop` ends the daemon. It is the 34th verb, it is on both doors, and it is
the first verb in this plugin that refuses a principal at the door rather than
at the §9 gate. §3.7 forbids exactly one shape of refusal — "a plugin MUST NOT
refuse an operator verb on the ground that the caller is not the operator" —
and this is not that ground, so the entry is here rather than as a divergence.

The ground is the sentence §3.7 ends on: "An authority that is not the
operator's to grant away does not become advisory with it." There is one
daemon per user (§2.3) and it serves every pane, every project and every door
on the machine. A pane that ends it takes the board away from panes that never
asked and are not its to answer for — the same reason `sweep --pane` refuses
another pane's leases to a caller who is not that pane, which no answer the
operator gives makes right. What §3.7 makes advisory is the operator's own
authority over the BOARD; the daemon's lifetime is not a board decision, and
what is being protected is the other panes, not the operator's prerogative.

Consequences recorded rather than left to be rediscovered:

- The verb carries no gate name. The gate is how the operator holds a verb
  back from an agent, and `stop` reaches no agent to hold back — the ungated
  count in `internal/verbs` moved from twelve to thirteen with this.
- `stop` writes no event, so §3.7's `on_behalf_of_operator` mark has nothing
  to attach to and nothing is owed there.
- No door starts a daemon in order to stop it: `internal/client` skips §2.2's
  autostart for this verb alone. The CLI turns "nothing was listening" into
  exit 0, because the state `stop` asks for already holds and
  `scripts/stop.sh` runs where the daemon may or may not be up; the MCP door,
  which has no exit status to carry that difference, is told `UNAVAILABLE`.
- The refusal is bypassable by anything that can send a signal, and nothing
  here pretends otherwise. What it buys is that an agent reaching for the
  board's own surface is told no rather than finding out afterwards.

## §9.1 — eleven writing verbs pass no name to the gate, and each says why

§9.1 sends every verb that changes the world through the gate. Eleven verbs
here write and offer the gate no name at all: `task.touch`, `task.release`,
`task.archive`, `note.discuss`, `note.verdict`, `note.unfold`, `note.keep`,
`note.drop`, `parked.resolve`, `sweep` and `stop`. That is a divergence, and
it is recorded here rather than left to be read off the registry by whoever
notices.

The class is not "these change nothing". It is verbs where a gate that could
deny or defer would break the thing the gate exists to protect: renewing your
own lease, handing work back, the gate's own resolution verb, and the sweep
the daemon's §11.5 timer calls. Each one carries its own reason in the
`Ungated` field of `internal/verbs`, and `TestEachUngatedReasonIsAboutItsOwnVerb`
refuses two verbs the same reason — a reason two verbs share is a reason about
a class, and a class reason is disproved the moment a sibling in that class is
gated. `TestEveryWritingVerbStatesItsRuleAndItsGate` pins the count, so the
eleven becoming twelve is an edit a reviewer reads rather than a line that
slips in.

What changed, and why this entry exists now: `task.delete` and `note.delete`
were in this class and are not any more. They destroy the entity outright, and
while they were ungated no policy the operator could write was consulted at
all — a freeze that denied every other write still let an agent hard-delete a
never-claimed task or an inbox note on any project's board. They are now
`tasks.delete` and `tasks.note_delete`, and no verb outside the gate destroys
anything.

## §3.7 — `parked resolve` carries the operator mark

Closed, and closed by implementing it rather than by an amendment. The five
note verbs 0.10.0 turned from refusals into marks write
`on_behalf_of_operator` through `operatorVerb` (`internal/tasks/note.go`), and
the resolution event now writes the same detail through the same function:
`ResolveParked` in `internal/store/parked.go` takes the whole Actor and passes
it to `tasks.MarkOperatorVerb`, which is `operatorVerb` under an exported name
for the one operator verb whose event is written outside `internal/tasks`. So
a resolve an agent performed after confirming with the operator now reads the
way its promote or its drop does, and a resolve the operator performed carries
nothing extra. `parked.resolved_by` still answers who resolved it, which is a
different question from on whose authority.
`TestResolvingMarksAnOperatorVerbAnAgentPerformed` in `internal/store` fails
in both directions §3.7 asks a plugin to pin: dropping the mark fails its
agent case, and marking a resolve the operator performed fails its human one.

## §4.4 — `parked list` stays project-scoped (recorded against 0.10.0; amended in 0.10.1)

Closed by the contract. §4.4 now scopes `--all-projects` to the entity list
verbs and says `parked.list` is not one of them, for the reason this entry
recorded: a parked action is resolved where it was parked, by an operator
acting in that project. `parked.list` declares no such flag
(`internal/verbs/verbs.go`) and the daemon refuses it with USAGE
(`internal/daemon/daemon.go`), which is conformance now rather than a
divergence.

## §16 — the `goal`, `submit`, `release` and `touch` spellings (recorded against 0.10.0; amended in 0.10.1)

Closed by the contract. §16 now writes its verbs in the §2.1 form, so the
document says `htask goal <id>`, `htask submit <id>`, `htask release <id>` and
`htask touch <id>` — which is what `internal/verbs/verbs.go` registers and what
`BuildGoal` in `internal/daemon/goal.go` renders into the condition it prints.
The divergence was spelling only and there is none of it left.
