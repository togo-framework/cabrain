#!/usr/bin/env bash
# The scheduler for graph-sync-run.sh.
#
# This workspace has no systemd and no cron (PID 1 is the coder agent), so the
# "timer" is a supervised loop: run a pass, sleep GRAPH_SYNC_INTERVAL, repeat.
# It is idempotent and flock-guarded, so a double start is harmless.
#
#   nohup setsid scripts/graph-sync-loop.sh >/dev/null 2>&1 &
#
# Check it with:  cat ~/.cabrain-graph-sync/state.json
#                 tail ~/.cabrain-graph-sync/run.log
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STATE_DIR="${GRAPH_SYNC_DIR:-$HOME/.cabrain-graph-sync}"
set -a; . "$STATE_DIR/env"; set +a
INTERVAL="${GRAPH_SYNC_INTERVAL:-900}"

echo "$$" > "$STATE_DIR/loop.pid"
trap 'rm -f "$STATE_DIR/loop.pid"' EXIT

while true; do
  "$HERE/graph-sync-run.sh" || true
  sleep "$INTERVAL"
done
