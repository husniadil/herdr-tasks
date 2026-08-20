# herdr-tasks

A task backlog and notes board for agents running on Herdr.

Tasks move **todo → doing → review → done** behind a claim with a renewable
lease, carry evidence, and are reviewed by someone who is not the harness that
wrote them. Notes are pre-decision ideas: agents propose, the operator decides.

One statically linked Go binary, `ht`, is the daemon, the CLI and the MCP
server. No browser, no web server, no PTYs, no second multiplexer.

## Install

```sh
herdr plugin install husniadil/herdr-tasks
```

The manifest's `[[build]]` step compiles `bin/ht` on install, and `[[startup]]`
starts the daemon. Herdr has no shutdown hook, so the **Stop the tasks daemon**
workspace action is the way to turn it off.

To develop against a checkout:

```sh
make build && make test-full
herdr plugin link .
```

## Using it

```sh
ht task list --ready                       # unblocked, unclaimed work
ht task claim 12                           # one winner
ht task touch 12                           # renew the lease, every turn
ht task submit 12 --report "…" --evidence "make test-full: ok"
ht task approve 12                         # not if your harness wrote it
ht note add "the sweep releases a lease without logging why"
ht task goal 12                            # a paste-ready /goal condition
ht doctor
```

`ht --help` lists every verb. Add `--json` to any of them for exactly one
machine-readable document on stdout; without it the output is prose and is not
meant to be parsed. The skill in `skills/tasks/` teaches the CLI to agents.

## The shared plugin contract

This plugin conforms to the **shared plugin contract, v0** (0.1.0-draft). The
version is printed by `ht version` and `ht doctor`. Where implementation found
the contract underspecified, the gap is recorded in
[`docs/contract-notes.md`](docs/contract-notes.md) rather than silently worked
around.

Deviations worth naming up front:

- **§6.1 / §7.3** — every verb is a CLI subcommand; a pinned subset of 15 is
  also an MCP tool. Both doors are generated from one registry, and a parity
  test fails on any drift between them. See the contract note.
- **§8.4** — no manifest `[[events]]` reaction. Leases are freed by the §11.5
  timer, by the reconciliation sweep at daemon start, and by `ht sweep`.

## Trust boundary

**There are no session keys, tokens, or HMAC identities. The boundary is your
local user account: whoever can open the socket is trusted as you** (§3.5).

The daemon listens on a Unix socket at `<state_dir>/tasks.sock`, created 0600
inside a state dir created 0700. Any process running as you can call any verb
as any principal it can present — including your agents, their subagents, and
anything else in a Herdr-managed pane, since `HERDR_PANE_ID` is inherited by
child processes (§3.3). This is deliberate for v0 and is not a bug to work
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
tasks.create   tasks.claim    tasks.submit   tasks.approve
tasks.reject   tasks.cancel   tasks.update   tasks.note_add
tasks.note_promote
```

## Configuration

TOML at `${HERDR_PLUGIN_CONFIG_DIR:-${XDG_CONFIG_HOME:-~/.config}/tasks}/tasks.toml`,
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

SQLite at `${HERDR_PLUGIN_STATE_DIR:-${XDG_STATE_HOME:-~/.local/state}/tasks}/tasks.db`,
WAL, written only by the daemon. `ht dump --json` prints the whole store —
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

Three dependencies, deliberately: [cobra](https://github.com/spf13/cobra),
[modernc.org/sqlite](https://modernc.org/sqlite) (pure Go, no cgo), and the
official [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk).

## Licence

MIT. See [LICENSE](LICENSE).
