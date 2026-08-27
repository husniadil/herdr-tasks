# herdr-tasks

A task backlog and notes board for agents running on Herdr, shipped as a Herdr
plugin: one Go binary (`htask`) that is the daemon, the CLI, a stdio MCP
server, and the popup TUI board the manifest opens. Tasks move todo → doing →
review → done with claims, leases, evidence, and review; notes are pre-decision
ideas a human promotes or drops.

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
- `make release-check` — the release gate, run on the machine that cuts the
  tag: `test-full`, a build, then the released-surface pin under
  `TASKS_PIN_REQUIRED=1` and layer 3 under `TASKS_E2E_REQUIRED=1`, where a
  missing Herdr is a failure instead of a skip.
- `make build` / `make install` — `./cmd/htask`. `make clean` removes `bin`,
  `dist` and `coverage.out`.

A green `make test` is not a green gate. Nothing is committed on it alone.

## The contract comes first

This plugin conforms to a **shared plugin contract** and declares the revision
it conforms to in its README and in `htask version` and `htask doctor` output
(§13.4). The contract fixes the error codes and exit statuses, the `--json`
envelope, identity and principals, project scoping, storage rules, MCP naming,
events, the policy gate, and the testing layers.

The text is vendored at `docs/contract.md`, byte-identical with the copy each
sibling plugin carries, so every § this repository cites resolves inside it —
`TestContractCitationsResolve` fails on one that does not. An amendment is made
where the contract is maintained and re-vendored here; editing this copy alone
puts it out of step with the plugins written against the same words.

`daemon.ContractVersion` is the revision this binary claims, and
`TestTheDeclaredRevisionIsTheVendoredOne` lets it LAG the vendored document —
only that way, and only while `docs/contract-notes.md` names both revisions in
one entry. Declaring higher is conformance to a text nobody here can read.

Tests cite the contract section they enforce (`// §5.6`) in a name or comment.
If implementation shows a contract rule is wrong or unimplementable as written,
do not silently diverge: record the gap in `docs/contract-notes.md` and follow
the contract until it is amended, with the § it changes cited in the amendment
the same way a test cites the § it enforces.

## Non-negotiables

1. **Dependency budget: five libraries** (six `go.mod` lines) — cobra,
   modernc.org/sqlite, the
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

   `github.com/spf13/pflag` is the sixth `go.mod` line and not a sixth
   library: it is cobra's own flag package, already compiled in through
   cobra, named directly by one test. `Command.Flags()` hands back a
   `*pflag.FlagSet` whose only complete enumeration is
   `VisitAll(func(*pflag.Flag))`, and Go infers no parameter type for a
   function literal, so calling it means writing the type. The one
   alternative that avoids it, `FlagUsages()`, skips hidden flags, which
   would leave the CLI-to-MCP flag drift check blind to a whole class of
   flag. Declaring it beats a test that quietly stops checking.

2. **The `--json` shape and the error-code vocabulary are semver-bound.** A
   shipped field or code is never repurposed or removed; only new ones are
   added.

3. **Herdr is the only substrate.** No PTYs, no other multiplexer, no polling
   Herdr for what an event already says. Herdr facts (pane, agent, harness,
   status) are read through `herdr`, never inferred. Feature-detect via
   `herdr api schema --json`; never pin a protocol number.

4. **The daemon is the only writer.** CLI and MCP talk to it over the Unix
   socket; neither opens the SQLite file. MCP is a thin door over the same
   daemon calls as the CLI, and `TestEveryVerbIsOnBothDoors` keeps the two
   surfaces identical: every verb of the registry is on both (§7.3). `--as` is
   the one CLI-only flag, and it is excluded on the surface, not by intent.

5. **Tests must NEVER touch the operator's live Herdr, config, or state.**
   State and config dirs in tests are temp dirs; `herdr` in tests is a fake on
   PATH. The end-to-end layer uses a throwaway headless Herdr server.

6. **Both audiences are first-class.** A human typing a verb and reading prose,
   and a program parsing `--json`, are both supported callers. A change that
   serves one at the other's expense needs a reason.

## The sibling repo standard

This repo is one of four Herdr plugins — herdr-dispatch (`hdis`), herdr-tasks
(`htask`), herdr-mail (`hmail`), herdr-sched (`hsched`), the short names §13.2
fixes — maintained as one discipline. `docs/repo-standard.md` **in the
herdr-dispatch checkout** is where that shape is written down (it was audited
across the first three, before the fourth): what the short name governs on
disk, the internal package names, the one verb registry both doors are built
from, the Makefile targets, and the README shape.
Read it before adding a verb, a package, or a Makefile
target, and file a delta on the owning repo's board rather than diverging
quietly.

## Working agreements

- **Development is test-first.** Failing test, then the code that makes it
  pass, then refactor. The state machine is pure and tested with no daemon, no
  socket, no Herdr — that layer exists from the first commit.
- **Commit at checkpoints.** Small, working increments rather than one large
  drop. Lowercase conventional commit subjects, no co-author lines.
- **Fail loud.** A gate that cannot answer denies; a sweep that releases a
  lease writes an event saying so; silent fallback is a bug by definition.
- **The docs are under test.** `README.md` and `skills/tasks/SKILL.md` are
  checked against the verb registry, not against anyone's memory of it: a verb,
  a flag, a path or a test name they teach must exist. A surface change lands
  with the documents in the same commit or the gate fails.
- **A caller-visible move needs a changelog entry.** When `verbs.CallerSurface()`
  — the CLI paths and MCP tool names a caller is broken by — no longer matches
  the `daemon.ReleasedSurface` digest pinned at `daemon.Version`, `## Unreleased`
  in `CHANGELOG.md` must say what a caller does about it (§13.3). The pin is
  re-taken beside the version bump at release.

## Layering

```
registry:   internal/verbs — the one list both doors are generated from
                          │
doors:      CLI (cmd/htask) · MCP (internal/mcpdoor) · TUI (internal/tui)
                          │
socket:     internal/client dials it · internal/protocol is the line format
                          │
daemon:     internal/daemon — socket server, sweeps, and internal/gate, the
            policy check every world-changing verb passes through (§9)
                          │
domain:     internal/tasks — the pure state machine, no I/O
            internal/store — SQLite, migrations, append-only event tables
            internal/herdrclient — the one place that talks to `herdr`
```

Defaults are decided once, in the daemon. A door that invents its own default
has introduced a second contract.
