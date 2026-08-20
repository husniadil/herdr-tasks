#!/bin/sh
# Herdr has no shutdown hook, so this is the only way to turn the plugin off.
# The daemon exits cleanly on SIGTERM (§2.5) and the socket goes with it.
set -eu
cd "$(dirname "$0")/.."
pkill -f "$(pwd)/bin/htask daemon" || echo "tasks: no daemon was running"
exit 0
