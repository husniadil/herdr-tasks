# herdr-tasks

A task backlog and notes board for agents running on Herdr, shipped as a Herdr
plugin: one Go binary (`htask`) that is the daemon, the CLI, and a stdio MCP
server. Tasks move todo → doing → review → done with claims, leases, evidence,
and review; notes are pre-decision ideas a human promotes or drops.

## Commands

- `make test` — the fast loop, seconds: the state machine, the store, payload
  shapes, the error vocabulary. Run it on every edit.
- `make test-full` — **the gate**, and what CI runs: the above plus every case
  that starts the daemon, walks the socket, or drives the fake `herdr`, with
  `-race` and a cross-compile vet of the other supported platform. Run it
  before every commit.
- `make e2e` — layer 3: the built binary against a REAL throwaway headless
  `herdr` on private socket paths. Out of the gate on purpose, because CI has
  no Herdr; run it before a release tag, and when anything touching
  `internal/herdrclient` or the pane lifecycle changes.
- `make build` / `make install` — `./cmd/htask`.

A green `make test` is not a green gate. Nothing is committed on it alone.

## The contract comes first

This plugin conforms to a **shared plugin contract** and declares that
in its README and `htask doctor` output. The contract fixes the error codes and
exit statuses, the `--json` envelope, identity and principals, project scoping,
storage rules, MCP naming, events, the policy gate, and the testing layers.

Tests cite the contract section they enforce (`// §5.6`) in a name or comment.
If implementation shows a contract rule is wrong or unimplementable as written,
do not silently diverge: record the gap in `docs/contract-notes.md` and follow
the contract until `docs/contract.md` is amended, with the § it changes cited
in the amendment the same way a test cites the § it enforces.

## Non-negotiables

1. **Dependency budget: five libraries** — cobra, modernc.org/sqlite, the
   official MCP go-sdk (`github.com/modelcontextprotocol/go-sdk`, pinned
   v1.7.0), bubbletea (`github.com/charmbracelet/bubbletea`) for the TUI, and
   `github.com/charmbracelet/x/ansi` for measuring text in terminal cells.
   Adding or swapping one is a deliberate decision, recorded here in the same
   commit that makes it. bubbletea earns its place because §11.6 asks for a
   mouse-first pane and the alternative is our own terminal input parser; the
   model and update logic stay pure so the tests never start a terminal.

   `x/ansi` is the one other charm import, and it is here for a reason that is
   not convenience: bubbletea's renderer truncates every line it writes with
   `ansi.Truncate` at the terminal width, so any width function of ours that
   disagreed with `ansi.StringWidth` would put the overflow back — the layout
   would believe a line fits and the renderer would cut it, losing text
   silently. Agreement with the renderer is the requirement, so we use the
   renderer's own function rather than a second opinion. It was already
   compiled into the binary through bubbletea, so this costs a `go.mod` line
   and no new supply-chain surface. `mattn/go-runewidth`, the obvious
   alternative and also already present, is NOT interchangeable: it sums
   runes where the renderer measures graphemes, and reports a regional-
   indicator flag as one cell against the renderer's two. Nothing else from
   the charm family is imported directly.

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
doors:      CLI (cmd/htask) · MCP (internal/mcpdoor) · TUI (internal/tui)
                          │
daemon:     internal/daemon — socket server, sweeps, the policy gate
                          │
domain:     internal/tasks — the pure state machine, no I/O
            internal/store — SQLite, migrations, append-only event tables
            internal/herdrclient — the one place that talks to `herdr`
```

Defaults are decided once, in the daemon. A door that invents its own default
has introduced a second contract.
