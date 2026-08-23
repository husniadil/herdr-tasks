---
name: tasks
description: The task backlog and notes board for this Herdr fleet. Use when picking up work, holding a claim, submitting for review, reviewing someone else's submission, or filing an idea that is not yet a commitment. Trigger words - task, backlog, claim, ready work, review, submit, note, board.
---

# Tasks

`htask` is the backlog. A **task** is a commitment with a lifecycle and a claim; a
**note** is an idea that has not been decided yet. Everything is scoped to the
**project**, the git root of wherever you are — no flag needed. An empty list
says which project it searched and how many rows match elsewhere; read that
line before concluding there is no work.

A task's 26-character id is the cross-board address: `htask task get <id>
--all-projects` finds it whichever board it was filed on, and says which. The
`#<n>` number is per project — `#24` exists on every board that has filed 24
tasks — so a number with `--all-projects` is refused rather than guessed at.

You never say who you are. Your principal is derived from the Herdr pane you
run in, and the harness is read from Herdr, not from you.

## Working a task

```sh
htask task list --ready              # unblocked, unclaimed, waiting for someone
htask task get 12                    # the whole thing: criteria, deps, feedback
htask task claim 12                  # one winner; CONFLICT means someone else won
htask task touch 12                  # renew the lease — see below
htask task submit 12 --report "what you did and how you verified it" \
                  --evidence "make test-full: ok, 214 tests" \
                  --evidence-for "1: make test-full: ok, 214 tests" \
                  --evidence-for "2: go test ./internal/store -run Sweep -v: PASS"
```

**Renew the lease at the start of every turn.** `htask task touch <id>` is one
call and it is the difference between holding your work and having it swept
back into the queue while you are still doing it. A lapsed lease is released
automatically and the task becomes claimable by anyone. Make it the first thing
you do each turn, before reading files, before running anything.

If you cannot finish — blocked, out of scope, out of turns — hand it back
rather than sitting on it:

```sh
htask task release 12 --note "schema migration written, the sweep path is untouched"
```

The note is what the next claimer reads first. Write it for them.

## What belongs in a submission

`--report` is prose: what changed and how you know it works. `--evidence` is
repeatable and is meant to be checkable: the command you ran and what it
printed. A criterion that says "the tests pass" is answered by evidence that
shows them passing, not by a claim that they do.

`--evidence-for` is the same kind of proof, bound to the criterion it proves.
Write it as `"<criterion>: what it printed"`, where the number is the
criterion's place in the list `htask task get 12` prints. It is repeatable, and
several citations may point at the same criterion. `--evidence` stays what it
was: proof for the task as a whole rather than for one line of it.

The reviewer then reads a checklist rather than two lists side by side. A
criterion is `[x]` because a citation landed on it and `[ ]` because none did,
with the citing lines underneath; nobody ticks a box by hand, and a criterion
marked `(optional)` needs no citation. Citing is opt-in — a submit that cites
nothing is the submit this plugin always took — but the moment you cite one
criterion you are claiming coverage, so every required criterion needs a
citation or the submit is refused with `USAGE` naming the ones you left. A
citation whose number is not in the list is refused the same way, and nothing
is written: the task is still yours, still `doing`.

### Name the test that pins each claim

Every property your report claims needs a named test, and the report says
which test pins which claim. Write the mapping as `<the claim> — pinned by
<TestName>`, in `--evidence-for` when the claim answers a criterion and in
`--report` otherwise. Stating it is yours, not the reviewer's to reconstruct.

The claim that gets missed is always the reassuring one: "it refuses X", "it
never Y", "a broken Z reads as a failure". A refusal is the code nobody runs
in normal operation, so nothing else fails when it goes, and a sentence saying
it refuses reads as proof that it does. It is a claim. If you cannot name a
test that FAILS when the behaviour is deleted, you have not verified it —
either write that test or drop the sentence from the report.

## Through MCP instead of the CLI

The same verbs are MCP tools on the `herdr-tasks` server, named by the verb
alone: `list`, `get`, `claim`, `touch`, `release`, `submit`, `approve`,
`reject`, `goal`, `note_add`, `note_list`, `note_verdict`, `create`, `events`,
`doctor`. Your client shows them under the server's own label, which is what
tells you whose `claim` you are calling. Every other verb is on the CLI, which
carries all of them.

## Reviewing

```sh
htask task approve 12
htask task reject 12 --feedback "no test cites the sweep path"
```

You may not review your own work: the pane that claimed or submitted a task,
or any pane carrying the same agent session, is refused with FORBIDDEN
(§6.6). A different session reviewing the same model's work is fine — two
sessions are two reviewers. Ask a peer in another pane, or leave it for the
operator.

## Notes: propose, do not decide

A note is for something you noticed that is not this task and is not yet worth
committing to.

```sh
htask note add "the sweep releases a lease without logging why"
htask note list --status inbox
htask note list --query sweep               # search bodies and verdict reasons
htask note discuss 3                        # you are triaging it
htask note discuss 3 --question "is this ours or herdr's?"   # park it on the operator
htask note verdict 3 task --reason "small, and it costs us every incident"
```

`verdict` is a **proposal**. Promoting a note into a task, keeping it, or
dropping it is the operator's call — those verbs refuse an agent principal.
When the operator does promote, the task is created on the note's own board
unless they say otherwise, and the note stays where it was filed and points at
the task it became:

```sh
htask note promote 3 --title "Log the reason a lease was swept" \
                     --validation "make test-full: ok"
htask note promote 3 --to-project ../sibling-repo
```

`--to-project` is for a note whose work belongs to a different repository: the
task lands on that project's board, the note stays on this one, and `note get`
names both, so the trail from idea to task survives crossing projects.
Amend your own verdict freely until the operator acts — and the wording of a
note you wrote, the same way, until they decide it:

```sh
htask note update 3 --body "the sweep releases a lease without saying why"
```

Found real work while doing something else? Either file a note, or file the
task and say where it came from:

```sh
htask task create "Log the reason a lease was swept" --discovered-from 12
```

Do not do it under the task you hold.

## Dependencies and readiness

```sh
htask task create "Ship the migration" --depends-on 4 --depends-on 5
```

Only `done` satisfies a dependency. A task with an unfinished dependency is
blocked, is not in `--ready`, and cannot be claimed. Cycles are rejected when
you create or update the edge, not later.

## Acceptance criteria

Write criteria an evaluator can check from a transcript — a command and what
its output must show:

```sh
htask task create "Make the sweep speak" \
  --validation "\`make test-full\` passes and its output is shown" \
  --validation "\`htask events --json\` shows a tasks.task.swept entry after a sweep"
```

Add ` (optional)` to the end of a criterion that is nice to have.

## Handing a task to another agent

```sh
htask task goal 12
```

prints a paste-ready `/goal` condition: the directive, the context, the "Done
when" including the obligation to run `htask task submit` and show its output, and
a stop clause that releases the claim rather than pushing past the scope.

`htask task goal 12 --one-line` renders the same condition with no newline in
it, for the argv of `herdr agent start`, which refuses one. It fits the same
ceiling, so it gives up context and criteria sooner and says what it dropped.

## Everything else

```sh
htask task list --mine            htask task update 12 --priority 5
htask task cancel 12 --reason …   htask task archive 12
htask events --follow             htask doctor
htask dump --json                 htask --help
```

Add `--json` to any verb for one machine-readable document; without it the
output is prose and is not meant to be parsed. Errors carry a code —
`CONFLICT` means someone else won or the row moved, `FORBIDDEN` means the rule
says not you, `DENIED` means the policy gate said no.

A line on stderr saying this door and the daemon are different builds is a
**warning, not a failure**: your answer is still on stdout and still correct
for the daemon that gave it. Report the line rather than retrying — restarting
the daemon is the operator's call, not yours.
