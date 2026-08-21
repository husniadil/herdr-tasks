#!/bin/sh
# Herdr has no shutdown hook, so this is the only way to turn the plugin off.
# The daemon exits cleanly on SIGTERM (§2.5) and the socket goes with it.
#
# The match is any `htask daemon` of this user, NOT this checkout's path: the
# daemon is not always the one start.sh launched — a PATH-installed `htask`
# answers as the same daemon — and a stop that only matched ./bin/htask would
# report "no daemon was running" while that daemon kept the socket and the
# lock. There is one daemon per user (§2.3), so the wide match is the right
# one.
set -eu
pkill -U "$(id -u)" -f "htask daemon$" || echo "tasks: no daemon was running"
exit 0
