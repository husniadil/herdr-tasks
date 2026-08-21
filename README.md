# herdr-tasks

A task backlog and notes board for agents running on Herdr.

Tasks move **todo → doing → review → done** behind a claim with a renewable
lease, carry evidence, and are reviewed by someone who is not the session that
wrote them. Notes are pre-decision ideas: agents propose, the operator decides.

One statically linked Go binary, `htask`, is the daemon, the CLI, the MCP
server and the board TUI — the operator-facing view a board owes (§2.1). No
browser, no web server, no PTYs, no second multiplexer.

## Install

```sh
herdr plugin install husniadil/herdr-tasks
```

The manifest's `[[build]]` step compiles `bin/htask` on install, and `[[startup]]`
starts the daemon. Herdr has no shutdown hook, so the **Stop the tasks daemon**
workspace action is the way to turn it off.

Then link the agent skill, which the install does not place for you:

```sh
root=$(herdr plugin list --plugin herdr-tasks --json | jq -r '.result.plugins[0].plugin_root')
ln -s "$root/skills/tasks" ~/.claude/skills/tasks
```

Herdr keeps an installed plugin under `~/.config/herdr/plugins/github/`, in a
directory named for the plugin id and a hash of where it came from — ask for
`plugin_root` rather than writing that path out, because the hash is not
something you can predict. `herdr plugin config-dir herdr-tasks` is a
different directory: the plugin's config, not its checkout.

To develop against a checkout:

```sh
make build && make test-full
herdr plugin link .
ln -s "$PWD/skills/tasks" ~/.claude/skills/tasks
```

The symlink is what puts the skill in front of an agent — nothing in the Herdr
manifest installs it, because the skill is read by the harness and not by
Herdr. Link it rather than copy it: the checkout stays the single source, and
a copy is a second version of the truth from the next commit onwards.

## Using it

```sh
htask task list --ready                       # unblocked, unclaimed work
htask task claim 12                           # one winner
htask task touch 12                           # renew the lease, every turn
htask task submit 12 --report "…" --evidence "make test-full: ok" \
                     --evidence-for "1: make test-full: ok, 214 tests"
htask task approve 12                         # not if your session wrote it
htask note add "the sweep releases a lease without logging why"
htask task goal 12                            # a paste-ready /goal condition
htask doctor
```

`--evidence` is proof for the task as a whole. `--evidence-for` is proof for
one acceptance criterion, written as `"<criterion>: what it printed"` where the
number is the criterion's place in the list `htask task get` prints. Cite one
and `task get` shows the criteria as a checklist — `[x]` where a citation
landed, `[ ]` where none did — with the citing lines under the criterion they
prove. Citing is opt-in: a submit that cites nothing behaves exactly as it
always has, and a submit that cites one criterion has to cite every required
one, so a checklist is never half a claim.

`htask --help` lists every verb. Add `--json` to any of them for exactly one
machine-readable document on stdout; without it the output is prose and is not
meant to be parsed. The skill in `skills/tasks/` teaches the CLI to agents.

## The boards, on a key

Two popups: `board` for tasks in review, `notes` for ideas awaiting a verdict.
Both are on Herdr's plugin menu, and both are plugin actions, so a key can
reach them.

Herdr does not load a plugin's keybindings — a plugin does not get to claim
keys in your config. Copy the two blocks from `keybindings.toml` into
`~/.config/herdr/config.toml` and change the keys to whatever is free:

```sh
cat keybindings.toml >> ~/.config/herdr/config.toml
```

```toml
[[keys.command]]
key = "prefix+b"
type = "plugin_action"
command = "herdr-tasks.board"
description = "Open the tasks board"
```

`type = "plugin_action"` is what makes `command` an action rather than a shell
command; the value is `<plugin id>.<action id>`, both from `herdr-plugin.toml`.
Pressing the key when the board is already open is a no-op rather than an
error, so it can be leaned on.

### Once it is open

A popup pane carries no `HERDR_PANE_ID`, so the board is the **human**
principal (§3.2) and offers human verbs only: there is no claim, touch or
submit key, because that work belongs to the agent doing it. Everything below
is also clickable — the footer draws each verb where the mouse can reach it.

| Key | Where | What it does |
| --- | --- | --- |
| `enter` | board | open the detail panel on the selection |
| `a` | board, task in review | approve it |
| `x` | board, task in review | reject it, asking what must change |
| `v` | notes | record a verdict — a proposal, not a decision |
| `p` | notes | promote the note into a task |
| `K` | notes | keep it: approved, not now |
| `d` | notes | drop it, with a reason |
| `e` | notes | open the body in `$EDITOR` (`VISUAL` first) and save the edit |
| `a` | notes | file a new note |
| `y` | parked actions | run the action the policy gate deferred |
| `n` | parked actions | reject it |
| `/` | either board | filter it — the search runs in the daemon, like `--query` |
| `tab` | either board | switch between the tasks board and the notes board |
| `esc` | either board | close the detail, then clear the filter, then leave |

The boards scroll. The wheel moves the region under the pointer and only that
one, so a long report scrolls under the pointer while the list behind it stays
where you left it, and the cursor keys drag the window along with the
selection. The detail panel is a bounded bottom panel rather than a cover: it
takes at most half of what is left, and its own text scrolls inside that.

Nothing scrolls out of sight silently. Each heading carries its count and,
when part of that column is off the screen, which of its rows are drawn —
`todo (40) 6-14`. A column the shared offset has scrolled clean past reads
`done (3) none`, which is what tells a column you have gone by from one that
was empty all along. The detail panel says the same on its separator row.

## Driving htask from another program

Everything above assumes a human at a terminal or an agent doing the work in a
Herdr pane. A separate program — a dispatcher, a monitor — is a supported
caller too, and it is not defined by being outside a pane: one that drives
panes is usually running in one itself. These are the facts it needs, which are
otherwise spread across §3.2, §4.2 and §5.1.

**Shell out to the CLI rather than opening the socket.** `htask <verb> --json`
is the surface with a compatibility promise: the `--json` shape and the
error-code vocabulary are semver-bound, so a shipped field is never removed or
repurposed and only new ones are added. The socket's wire format carries no
such promise and is free to change between builds. The daemon starts on first
use, so a consumer does not manage its lifetime either.

**Discovery.** `htask doctor --json` orients a consumer in one call:
`socket_path` and `state_dir` say where this installation keeps itself,
`project` and `principal` say what the call just made resolved to, and
`contract` and `build` say which revision and binary answered.

```sh
htask doctor --json
htask task list --ready --all-projects --json
htask task get 12 --json
htask task goal 12 --one-line
```

**Handing a task to an agent.** `htask task goal <id>` prints the condition a
human pastes. A program that starts the agent itself wants `--one-line`: Herdr
refuses a newline in agent argv outright, so the document form cannot be
delivered as the initial prompt of `herdr agent start` at all, while the same
condition rendered as one line goes through whole. The 4,000-character ceiling
holds on whichever form is printed. A break costs four characters as a
separator instead of one, so the one-line form gives up context and criteria
sooner, and says how many criteria it dropped.

**Scope.** Every verb resolves a project per call (§4.2). `--project <path>`
sets it explicitly, which is what a consumer running from its own working
directory wants, and `--all-projects` opts out of scoping for one watching
several repositories at once. `task list --ready` is the unblocked, unclaimed
work.

**Principal.** A principal is derived from the environment, so a consumer
does not get to assume which one it will be: a program with no `HERDR_PANE_ID`
is `human` (§3.2), and one started inside a Herdr pane inherits that pane id
and is `agent:<pane id>` instead — which is the usual case, because a program
that drives panes tends to be running in one. The two are not interchangeable:
`human` is exempt from recusal and is the only principal that may promote a
note. Rather than depend on either, a consumer says what it is with
`--as plugin:<name>` on every call, reads as well as writes. `--as agent:…`
and `--as human` are refused, because those two are derived from the
environment and never declared; `TestAsRefusesDerivedPrincipals` holds that.
So is `--as plugin:tasks`: that is the board's own hand in the trail, not a
caller's to claim (§3.2), and so is any principal id carrying whitespace or a
control character.

**Watching the trail.** `htask events --json` answers the batch shape,
`{"events":[…],"count":N}`. Adding `--follow` streams instead: one bare event
object per line, no envelope around it. Both carry the same §8.1 payload and
both are semver-bound.

Without `--since`, both start at the BEGINNING. A follower is handed the whole
trail, oldest event first, and waits for something new only once that backlog
is drained — so a consumer that restarts without passing `--since <event id>`
sees every event it has already handled, again. Resume from the last id
processed.

The two doors end a stream differently. At the socket the daemon sends a final
`{"done":true}` document, which is the only thing separating a stream that
ended on purpose from a daemon that died: otherwise both are just a closed
connection. The CLI consumes that sentinel and reports the same fact by exiting
0 with nothing further on stdout, while a daemon that died leaves the CLI
exiting `UNAVAILABLE` with one error envelope as its last document.

```sh
htask events --follow --since 01K7Q0S8R4V6WZJH8M2NQ3XYZ0 --json
htask events --follow --limit 3 --json
```

**Leases are not a consumer's to manage.** When a pane dies its claims come
back with no help from anyone. The manifest reacts to `pane.closed` and
`pane.exited` by running `scripts/on-pane-gone.sh`, which sweeps the leases
that one pane holds; the §11.5 timer releases anything that lapses; and a
reconciliation sweep runs at daemon start. An external program must not
reimplement any of it — the daemon is the only writer (§2.2), and a second one
racing it is exactly the bug the single-writer rule exists to prevent. Use
`htask sweep --pane <pane id>` when something should be released now rather
than when its lease runs out.

## The shared plugin contract

This plugin conforms to the **shared plugin contract**, revision 0.6.0,
which §13.4 requires it to declare here and in `doctor` output — `htask version`
and `htask doctor` both print it. The contract is vendored at
[`docs/contract.md`](docs/contract.md), so every § this repository cites
resolves inside it; `TestContractCitationsResolve` fails on one that does not,
and `TestTheDeclaredRevisionIsTheVendoredOne` fails when this sentence and that
document name different revisions. Each change the contract's own changelog
lists is checked against the code that answers it, and the checks are written
down in [`docs/contract-notes.md`](docs/contract-notes.md). Where implementation
finds the contract underspecified, the gap is recorded there too rather than
silently worked around.

Deviations worth naming up front:

- **§6.1 / §7.3** — every verb is a CLI subcommand; a pinned subset of 15 is
  also an MCP tool. Both doors are generated from one registry, and a parity
  test fails on any drift between them. See the contract note.
- **§7.1 / §13.3** — an MCP tool is named by its verb alone: `claim`, `submit`,
  `list`, `note_add`. The server registers as `herdr-tasks`, and a client
  namespaces the tools under that label, so the plugin's identity is said once
  where a client reads it. That tool list is semver-bound, so this binary is
  **0.2.0** — `htask version` and `htask doctor` print it, and a client with
  the older tool names wired in calls names this server no longer answers to.
- **§8.4** — the manifest reacts to `pane.closed` and `pane.exited`, both
  running `scripts/on-pane-gone.sh`, which sweeps the leases of the pane the
  event names. It self-filters by construction rather than by a check:
  `htask sweep --pane <id>` releases what that one pane holds, which is nothing
  for a pane holding none and nothing again on a second firing. The reaction
  complements the other two paths and replaces neither, because a hook is
  missed while the daemon or Herdr is down — leases are still freed by the
  §11.5 timer, by the reconciliation sweep at daemon start, and by
  `htask sweep`. Herdr's manifest spells these hook names with dots while its
  event schema spells them with underscores; see the contract note.

## Trust boundary

**There are no session keys, tokens, or HMAC identities. The boundary is your
local user account: whoever can open the socket is trusted as you** (§3.5).

The daemon listens on a Unix socket at `<state_dir>/tasks.sock`, created 0600
inside a state dir created 0700. Any process running as you can call any verb
as any principal it can present — including your agents, their subagents, and
anything else in a Herdr-managed pane, since `HERDR_PANE_ID` is inherited by
child processes (§3.3). This is deliberate and is not a bug to work
around: the plugin is a local tool for a single operator's machine, and the
protection is the account, not the plugin.

Two things follow. Your principal is **derived, never declared** — a call from
a pane is `agent:<pane id>`, a call from anywhere else is `human`, and `--as`
is accepted only for `cron`, `trigger` and `plugin` principals. And the policy
gate below is a governance tool for coordinating agents, not a security
boundary against a hostile one.

## The policy gate

Every world-changing verb passes through one gate before anything happens
(§9). Unconfigured, it allows. Configured to a command, that command reads
`{"subject","verb","target"}` on stdin and prints
`{"decision":"allow"|"deny"|"defer"}`. **Any failure to get a well-formed
answer — unreachable, non-zero, malformed, oversized, slow — is a deny.** A
`defer` parks the action and returns `DENIED` with a `parked_id`; only the
operator resolves it, and the re-run happens under the original subject.

The gated verbs, for a future policy plugin to name:

```
tasks.create       tasks.claim         tasks.submit   tasks.approve
tasks.reject       tasks.cancel        tasks.update   tasks.note_add
tasks.note_update  tasks.note_promote
```

## Configuration

TOML at `${TASKS_CONFIG_DIR:-${XDG_CONFIG_HOME:-~/.config}/tasks}/tasks.toml`,
re-read on SIGHUP. Every key is overridable with a `TASKS_` environment
variable.

```toml
lease_seconds = 900                              # how long a claim holds
sweep_seconds = 60                               # how often lapsed leases are swept
gate_command  = ["/usr/local/bin/policy", "check"]
on_event      = ["/usr/local/bin/notify"]        # run detached on every state change
```

The event hook receives `TASKS_EVENT`, `TASKS_ENTITY`, `TASKS_ID`,
`TASKS_PROJECT`, `TASKS_ACTOR`, `TASKS_KIND` and `TASKS_AT`. A hook that fails
never fails the write that caused it.

Config holds no secrets. A value that needs one names a file path or an
environment variable and is dereferenced at use.

## Limits on free text

Every field that takes free text is bounded when it is written, and a call
over a bound is `USAGE` (exit 2) naming the field and the limit — never a
silent truncation, because text a caller believes it stored and cannot read
back is worse than a refusal.

| Bound | Characters | Fields |
|---|---|---|
| Title | 200 | a task's `title` |
| Prose | 20,000 | `description`, `report`, `feedback`, a release `note`, a cancel or verdict `reason`, a note `body`, a clarification `question` |
| One entry | 4,000 | one acceptance criterion, one piece of evidence |
| Entries | 200 | how many criteria or pieces of evidence one call may carry |

Lengths count characters, not bytes, so a note written in a language that does
not fit ASCII gets the same allowance as one that does.

The numbers are deliberately far above real use — when they were set, the
longest values this plugin had ever stored were an 86-character title, a
747-character description, a 2,225-character report and a 7,902-character
verdict reason. Hitting one should mean something went wrong, not that
something got long.

A bound is a check the verbs run, not a promise about what is already stored:
rows written before these existed are still over them. Anything that renders
stored text into a bounded artifact — `task goal` into a `/goal` condition —
clamps at render time regardless, and says what it dropped.

## Your data

SQLite at `${TASKS_STATE_DIR:-${XDG_STATE_HOME:-~/.local/state}/tasks}/tasks.db`,
WAL, written only by the daemon. `htask dump --json` prints the whole store —
tasks, notes, the append-only event trail, dependencies, the parked queue — so
nothing here needs this plugin to be readable.

Nothing is hard-deleted except a row that never left its initial state: a task
that was never claimed, a note still in the inbox. Everything else is cancelled
or archived, with a timestamp and an event.

## Development

```sh
make test        # the fast loop: the state machine, the store, the vocabulary
make test-full   # the gate: the above plus the daemon, the socket, the fake
                 # herdr, with -race and a cross-compile vet of the other OS
```

Tests never touch your live Herdr, config or state: state and config dirs are
temp dirs and `herdr` is a fake on PATH (§12.3). Every test cites the contract
section it enforces.

Six `go.mod` lines, five libraries, each with a reason:

- [`github.com/spf13/cobra`](https://github.com/spf13/cobra) — the CLI, whose
  subcommands are generated from the one verb registry both doors read.
- [`modernc.org/sqlite`](https://modernc.org/sqlite) — the store, in pure Go,
  so the binary builds and ships without cgo.
- [`github.com/modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk)
  — the MCP door, on the official SDK rather than a hand-rolled protocol.
- [`github.com/charmbracelet/bubbletea`](https://github.com/charmbracelet/bubbletea)
  — the board pane. §11.6 asks for a mouse-first surface and the alternative
  is our own terminal input parser; the model and update logic stay pure, so
  the tests never start a terminal.
- [`github.com/charmbracelet/x/ansi`](https://github.com/charmbracelet/x/ansi)
  — measuring text in display cells. Bubbletea's renderer truncates every line
  it writes with `ansi.Truncate` at the terminal width, so a width function
  that disagreed with `ansi.StringWidth` would let the layout believe a line
  fits while the renderer cut it. Agreement with the renderer is the
  requirement, so the layout uses the renderer's own function.

- [`github.com/spf13/pflag`](https://github.com/spf13/pflag) — cobra's own
  flag library, named directly by one test. `Command.Flags()` returns a
  `*pflag.FlagSet`, and the only complete enumeration it offers is
  `VisitAll(func(*pflag.Flag))`; Go infers no parameter type for a function
  literal, so the type has to be written to call it. `FlagUsages()` is the one
  name source that avoids the type and it skips hidden flags, which would
  leave `TestEveryCLIGlobalIsAccountedForOnTheMCPDoor` silently blind to
  exactly the flags it exists to catch. This is a `go.mod` line and no new
  supply-chain surface: pflag is already compiled in through cobra.

`TestDependenciesAreDeclaredInTheReadme` reads go.mod's direct requires and
fails on one this list does not name.

## Licence

MIT. See [LICENSE](LICENSE).
