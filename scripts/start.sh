#!/bin/sh
# Start the tasks daemon and exit, which is what Herdr expects of a startup
# command. `ht daemon` refuses when one is already listening, and that refusal
# is success here: the daemon we wanted running is running.
set -eu
cd "$(dirname "$0")/.."
if [ ! -x bin/ht ]; then
  echo "tasks: bin/ht is missing; the [[build]] step did not run" >&2
  exit 1
fi
nohup ./bin/ht daemon >/dev/null 2>&1 &
exit 0
