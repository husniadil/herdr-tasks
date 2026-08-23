#!/bin/sh
# Herdr has no shutdown hook, so this is the way to actually turn the plugin
# off. It ASKS the daemon to end rather than signalling it: `htask stop` is
# answered first, and the daemon then stops accepting, finishes the calls
# already in flight, and gives up the socket and the lock the way SIGTERM does
# (§2.5). A signal cuts a call in flight; this one does not.
#
# There is one daemon per user (§2.3), and it is not always the one start.sh
# launched — a PATH-installed `htask` answers as the same daemon. So this
# prefers this checkout's binary and falls back to whatever is on PATH; either
# one reaches the same socket.
set -eu
cd "$(dirname "$0")/.."

if [ -x bin/htask ]; then
  htask=./bin/htask
elif command -v htask >/dev/null 2>&1; then
  htask=htask
else
  echo "tasks: no htask binary here or on PATH, so there is nothing to ask" >&2
  exit 1
fi

# Nothing is unset here. `stop` is refused from a pane — one daemon serves
# every pane of this user, so ending it takes the board away from panes that
# never asked — and a workspace action is not a pane: Herdr injects
# HERDR_PANE_ID into the panes it manages, not into the `[[actions]]` it runs
# (docs/contract-notes.md §5.1). Run from a pane's own shell the refusal is
# right and fires.

# Finding no daemon is not a failure here: `htask stop` says so and exits 0,
# because the state this script asks for already holds.
exec "$htask" stop
