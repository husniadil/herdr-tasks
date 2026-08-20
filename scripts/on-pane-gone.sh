#!/bin/sh
# Herdr says a pane closed or its command exited. Give that pane's work back
# now, rather than letting the claim sit until the §11.5 timer notices.
#
# The hook fires for every pane in the session, not only panes working on
# tasks. That needs no filter of its own: `sweep --pane` releases the leases
# that ONE pane holds, so a pane holding none releases nothing, and a second
# firing releases nothing again. Self-filtering and idempotent by
# construction.
set -eu
cd "$(dirname "$0")/.."

if [ ! -x bin/ht ]; then
  echo "tasks: bin/ht is missing; the [[build]] step did not run" >&2
  exit 1
fi

# Herdr passes the ids in the environment and substitutes nothing into argv.
# HERDR_PANE_ID is the pane the event is about — Herdr fills it from the
# event's own pane, never from some other pane that happens to be focused —
# but it is absent when the pane had already left Herdr's state by the time
# the hook ran. Then there is no pane to name and nothing to release.
if [ -z "${HERDR_PANE_ID:-}" ]; then
  echo "tasks: a pane event arrived with no pane id, so there is nothing to release" >&2
  exit 0
fi

exec ./bin/ht sweep --pane "$HERDR_PANE_ID"
