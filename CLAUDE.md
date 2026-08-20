# herdr-tasks

A task backlog and notes board for agents running on Herdr, shipped as a Herdr
plugin: one Go binary (`ht`) that is the daemon, the CLI, and a stdio MCP
server. Tasks move todo → doing → review → done with claims, leases, evidence,
and review; notes are pre-decision ideas a human promotes or drops.

## Commands

- `make test` — the fast loop, seconds: the state machine, the store, payload
  shapes, the error vocabulary. Run it on every edit.
- `make test-full` — **the gate**, and what CI runs: the above plus every case
  that starts the daemon, walks the socket, or drives the fake `herdr`, with
  `-race` and a cross-compile vet of the other supported platform. Run it
  before every commit.
- `make build` / `make install` — `./cmd/ht`.

A green `make test` is not a green gate. Nothing is committed on it alone.

## The contract comes first

This plugin conforms to a **shared plugin contract, v0** and declares that
in its README and `ht doctor` output. The contract fixes the error codes and
exit statuses, the `--json` envelope, identity and principals, project scoping,
storage rules, MCP naming, events, the policy gate, and the testing layers.

Tests cite the contract section they enforce (`// §5.6`) in a name or comment.
If implementation shows a contract rule is wrong or unimplementable as written,
do not silently diverge: record the gap in `docs/contract-notes.md` and follow
the contract until it is amended upstream.

## Non-negotiables

1. **Dependency budget: four libraries** — cobra, modernc.org/sqlite, the
   official MCP go-sdk (`github.com/modelcontextprotocol/go-sdk`, pinned
   v1.7.0), and bubbletea (`github.com/charmbracelet/bubbletea`) for the TUI.
   Adding or swapping one is a deliberate decision, recorded here in the same
   commit that makes it. bubbletea earns its place because §11.6 asks for a
   mouse-first pane and the alternative is our own terminal input parser;
   nothing else from the charm family is imported directly, and the model and
   update logic stay pure so the tests never start a terminal.

2. **The `--json` shape and the error-code vocabulary are semver-bound.** A
   shipped field or code is never repurposed or removed; only new ones are
   added.

3. **Herdr is the only substrate.** No PTYs, no other multiplexer, no polling
   Herdr for what an event already says. Herdr facts (pane, agent, harness,
   status) are read through `herdr`, never inferred. Feature-detect via
   `herdr api schema --json`; never pin a protocol number.

4. **The daemon is the only writer.** CLI and MCP talk to it over the Unix
   socket; neither opens the SQLite file. MCP is a thin door over the same
   daemon calls as the CLI, and a parity test keeps the two surfaces identical.

5. **Tests must NEVER touch the operator's live Herdr, config, or state.**
   State and config dirs in tests are temp dirs; `herdr` in tests is a fake on
   PATH. The end-to-end layer uses a throwaway headless Herdr server.

6. **Both audiences are first-class.** A human typing a verb and reading prose,
   and a program parsing `--json`, are both supported callers. A change that
   serves one at the other's expense needs a reason.

## Working agreements

- **Development is test-first.** Failing test, then the code that makes it
  pass, then refactor. The state machine is pure and tested with no daemon, no
  socket, no Herdr — that layer exists from the first commit.
- **Commit at checkpoints.** Small, working increments rather than one large
  drop. Lowercase conventional commit subjects, no co-author lines.
- **Fail loud.** A gate that cannot answer denies; a sweep that releases a
  lease writes an event saying so; silent fallback is a bug by definition.

## Layering

```
doors:      CLI (cmd/ht) · MCP (internal/mcpdoor) · TUI (internal/tui)
                          │
daemon:     internal/daemon — socket server, sweeps, the policy gate
                          │
domain:     internal/tasks — the pure state machine, no I/O
            internal/store — SQLite, migrations, append-only event tables
            internal/herdrclient — the one place that talks to `herdr`
```

Defaults are decided once, in the daemon. A door that invents its own default
has introduced a second contract.
