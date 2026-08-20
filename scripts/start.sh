#!/bin/sh
# Start the tasks daemon and exit, which is what Herdr expects of a startup
# command. `htask daemon` refuses when one is already listening, and that refusal
# is success here: the daemon we wanted running is running.
set -eu
cd "$(dirname "$0")/.."
if [ ! -x bin/htask ]; then
  echo "tasks: bin/htask is missing; the [[build]] step did not run" >&2
  exit 1
fi
# Absolute path deliberately: stop.sh matches on it, and `pkill -f` sees the
# argv this line writes, not the path the shell resolved it from.
nohup "$(pwd)/bin/htask" daemon >/dev/null 2>&1 &
exit 0
