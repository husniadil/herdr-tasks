# Contract notes

Gaps found while implementing the shared plugin contract. The rule is in
`CLAUDE.md`: where implementation shows a contract rule is wrong or
unimplementable as written, record it here and follow the contract until
`docs/contract.md` is amended. The contract is in this repository, so an
amendment is a change to that file, citing the § it changes and bumping the
revision its Status line states. Nothing here is a licence to diverge quietly.

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

What this costs, recorded honestly: Herdr does not place this plugin's state,
so a second or sandboxed Herdr does not get a separate store for free
(`TASKS_STATE_DIR` buys one deliberately), and if Herdr ever cleans up
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

The vendored document states Version 0.4.0-draft, and `ContractVersion` in
`internal/daemon/daemon.go` says the same. `htask version`, `htask doctor` and
the README all read that one constant, and
`TestTheDeclaredRevisionIsTheVendoredOne` fails when the constant, the README
sentence and the document's own Status line stop agreeing.

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
| §6.1 / §7.3 | CLI total, MCP a pinned subset of roughly 8–16, one registry, a parity test | `internal/verbs/verbs.go` is the one registry; `TestCLIAndMCPSurfacesDoNotDrift`, `TestMCPToolListIsPinned`, `TestMCPToolCountStaysSmall`; 15 tools today |
| §8.4 spelling | Herdr event names spelled as its schema prints them | `internal/herdrclient/client.go:173` matches either spelling, which is right for a document whose halves disagree; the manifest takes dots because Herdr validates it against dots — the entry above records which is which |
| §8.4 reaction | `[[events]]` is usable; a reaction self-filters, is idempotent, and complements the sweep | `herdr-plugin.toml` declares `pane.closed` and `pane.exited`; `scripts/on-pane-gone.sh` sweeps by pane, which is both by construction; `TestClosingAPaneReleasesItsLeasesWithoutBeingAsked` drives it against a real Herdr |
| §11.2 | the schema document's shape, and the flat form too | `internal/herdrclient/client.go` reads `schemas.request.oneOf[].properties.method` and `schemas.event.$defs.EventKind`, and the flat `{requests, events}` form; the protocol number is read for `doctor` and never pinned; `TestSchemaListsCapabilities` |
| §11.4 | delivery through `herdr agent prompt`, no type-verify-retype loop | `internal/herdrclient/client.go` `Prompt`. The slash-command paragraph binds a plugin that starts an agent under one; this plugin does not — `task goal` prints a paste-ready condition for a human (§16.2), which is the branch that paragraph ends on |
| §11.5 | liveness from `pane_exited` / `pane_closed`, swept on those events and on a bounded timer, recorded in `_events` | `KindSwept` in `internal/tasks/task.go`; the timer in `internal/daemon/daemon.go`; `TestLeaseIsReleasedAfterTheClaimingPaneDies` |
| §11.6 | the `./` command rule, popups needing an attached client, a plugin pane being `human` | `TestManifestCommandsInThePluginRootSayTheyAre`; both panes are `placement = "popup"` and nothing automated opens one, so no e2e depends on a pane appearing; `internal/project/project.go` reads `HERDR_PLUGIN_CONTEXT_JSON` and never a pane id, and `TestTheBoardOffersHumanVerbsOnly` holds the pane to human verbs |
| §13.1 | the plugin id is the repository name and names no umbrella | `herdr-plugin.toml` `id = "herdr-tasks"`; `cmd/htask/manifest_test.go` |
| §14 (0.4.0) | the glossary lists the binary abbreviations §13.2 says it lists: `htask` for `tasks`, `hdis` for `dispatch` | this plugin ships the first of the two: `cmd/htask` builds `htask`, `herdr-plugin.toml` names it in `[[build]]`, and `CHANGELOG.md` is written for callers of that binary. The list was empty while two abbreviations were in use, so §13.2 pointed at a place that answered nothing; adding them keeps a promise the contract already made rather than adding an obligation, which is why the revision does not move |

One observation the table does not settle: `Prompt` has no caller. It is the
§11.4-conformant primitive and it is correct, but nothing in this plugin
delivers text to an agent today, so §11.4 is satisfied by a path that is not
exercised outside its own package.
