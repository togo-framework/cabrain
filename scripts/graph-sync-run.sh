#!/usr/bin/env bash
# One pass of the FlowOS GRAPH refresh: nodes -> edges -> repo links -> spine.
#
# WHY THIS IS NOT ON THE PROXMOX BOX WITH THE OTHER TIMERS
# --------------------------------------------------------
# flowos-sync.py, code-index.py and the analytics/rollup job all run from
# /opt/* on 159.195.203.241 and write to the brain over HTTPS. The graph
# builders cannot: they INSERT into entities / entity_edges, and there is no
# HTTP surface for graph writes, so they need a direct brain DSN.
#
# The brain Postgres is not reachable from 159.195.203.241 and no credential
# will make it so. 10.10.10.30 (which an earlier attempt probed and got
# "password authentication failed for user cabrain" from) is LXC 102 togo-db —
# that is FlowOS PROD, a different database entirely. The brain lives in the
# `pg` container on the `stack_stacknet` docker network inside Docker Desktop
# on the operator's machine, whose egress is 196.137.11.160 and whose 5432 /
# 55432 / 8080 are all closed from the Proxmox host (measured).
#
# The only host that reaches BOTH databases is this workspace: the brain via
# the Docker Desktop gateway (host.docker.internal:55432) and FlowOS prod via
# an SSH tunnel to the Proxmox box. So the graph refresh runs here, and this
# script opens its own tunnel rather than depending on one a human left behind.
#
# Config lives in ~/.cabrain-graph-sync/env (mode 600). No secret is in this file.
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STATE_DIR="${GRAPH_SYNC_DIR:-$HOME/.cabrain-graph-sync}"
ENV_FILE="$STATE_DIR/env"
LOG="$STATE_DIR/run.log"
LOCK="$STATE_DIR/.lock"

[ -r "$ENV_FILE" ] || { echo "missing $ENV_FILE" >&2; exit 2; }
set -a; . "$ENV_FILE"; set +a

PY="${GRAPH_SYNC_PYTHON:-python3}"
TUNNEL_PORT="${FLOWOS_TUNNEL_PORT:-15433}"

log() { printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" | tee -a "$LOG"; }

# ── FlowOS prod is only reachable through the Proxmox box ────────────────────
ensure_tunnel() {
  if (exec 3<>"/dev/tcp/127.0.0.1/$TUNNEL_PORT") 2>/dev/null; then return 0; fi
  log "tunnel: opening 127.0.0.1:$TUNNEL_PORT -> $FLOWOS_TUNNEL_TARGET via $FLOWOS_TUNNEL_HOST"
  ssh -f -N -o BatchMode=yes -o ExitOnForwardFailure=yes \
      -o ServerAliveInterval=30 -o ServerAliveCountMax=3 \
      -L "$TUNNEL_PORT:$FLOWOS_TUNNEL_TARGET" "$FLOWOS_TUNNEL_HOST" \
      >>"$LOG" 2>&1 || return 1
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    (exec 3<>"/dev/tcp/127.0.0.1/$TUNNEL_PORT") 2>/dev/null && return 0
    sleep 1
  done
  return 1
}

# flock keeps a slow pass from overlapping the next tick. entity_edges' unique
# key includes valid_from, so two concurrent builders would each fail to see the
# other's open edge and write a DUPLICATE live edge.
exec 9>"$LOCK"
if ! flock -n 9; then
  log "another pass is still running — skipping this tick"
  exit 0
fi

ensure_tunnel || { log "FAIL tunnel to FlowOS prod could not be established"; exit 1; }

rc=0
run_step() {
  local name="$1"; shift
  local t0=$SECONDS out
  out="$("$PY" "$HERE/$name" "$@" 2>&1)"; local st=$?
  printf '%s\n' "$out" >>"$LOG"
  if [ $st -ne 0 ]; then
    log "FAIL $name (exit $st, $((SECONDS-t0))s) :: $(printf '%s' "$out" | tail -1)"
    rc=1
  else
    log "ok   $name ($((SECONDS-t0))s) :: $(printf '%s' "$out" | tail -1)"
  fi
}

log "=== graph refresh start ==="
run_step flowos-graph.py            # entity nodes from FlowOS prod
run_step flowos-graph-edges.py      # the typed relation set
run_step flowos-graph-repo-links.py # repo -> venture / portfolio
run_step flowos-graph-spine.py      # activity, feeds, meetings, ontology reconcile
run_step flowos-rollup-relink.py    # re-attach rollups the API-only timer orphaned
log "=== graph refresh done (rc=$rc) ==="

# A machine-readable heartbeat, so "did the timer fire?" is a file read and not
# a log grep.
printf '{"lastRun":"%s","rc":%d}\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$rc" \
  > "$STATE_DIR/state.json"
exit $rc
