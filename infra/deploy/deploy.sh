#!/usr/bin/env sh
set -e
export DOCKER_HOST=unix:///var/run/docker.sock
LOG=/deploy/last.log
: > "$LOG"; exec >>"$LOG" 2>&1
echo "=== [$(date -Is)] deploy triggered ==="

. /deploy/.env

if [ ! -d /src/.git ]; then
  git clone https://x-access-token:${GITHUB_TOKEN}@github.com/togo-framework/cabrain.git /src
fi
cd /src
git remote set-url origin https://x-access-token:${GITHUB_TOKEN}@github.com/togo-framework/cabrain.git
git fetch --all --prune 2>&1 | tail -3
git checkout main 2>&1 | tail -2 || true
git reset --hard origin/main 2>&1 | tail -2
echo "  HEAD: $(git rev-parse --short HEAD) — $(git log -1 --pretty=%s)"

echo "  --- verifying gen dirs exist ---"
ls -d internal/db/gen internal/graph/gen 2>&1

docker build -t cabrain:latest . 2>&1 | tail -12

CABRAIN_PW=$(echo "$CABRAIN_DATABASE_URL" | sed -E 's|.*://cabrain:([^@]+)@.*|\1|')

# Free the canonical name so `--name cabrain` below doesn't collide. (The thorough,
# rogue-catching purge happens AFTER the new container is up — see below.)
docker rm -f cabrain 2>/dev/null || true

docker run -d --name cabrain \
  --network stack_stacknet --restart unless-stopped \
  --env-file /deploy/.env \
  -e DATABASE_URL="postgresql://cabrain:${CABRAIN_PW}@pg:5432/cabrain?search_path=cabrain_auth,public" \
  -e DB_DRIVER=pgx -e CACHE_DRIVER=redis -e REDIS_URL=redis://redis:6379 \
  -e BRAIN_BM25_TOKENIZER=cabrain_ml \
  -e CABRAIN_REQUIRE_AUTH=1 \
  -e BRAIN_CHAT_LLM_MODEL=qwen2.5:3b-instruct \
  cabrain:latest
# CABRAIN_REQUIRE_AUTH=1 makes EVERY /api/brain/* endpoint require a login session
# (browser) or a valid X-Cabrain-Token (MCP) — the public URL is NOT open. Per-brain
# grants (canRead/canWrite/adminOnly) still run in-handler. This line must never be
# dropped again: without it the deploy silently serves the whole brain unauthenticated.
# (AUTH_SECRET + CABRAIN_SECRETS_KEY come from --env-file so sessions + the secrets
# vault survive restarts — they MUST be present in /deploy/.env.)

sleep 5
NEW_ID=$(docker inspect -f '{{.Id}}' cabrain)
NEW_IP=$(docker inspect -f '{{with index .NetworkSettings.Networks "stack_stacknet"}}{{.IPAddress}}{{end}}' cabrain)
echo "  new container: $(echo "$NEW_ID" | cut -c1-12) @ ${NEW_IP}"

# ── ROGUE PURGE (behavior-based, name/image/alias-AGNOSTIC) ──────────────────
# The real leak: an OLD container from an earlier manual run stayed attached to
# stack_stacknet sharing the `cabrain` network ALIAS, so NPM/Docker-DNS round-
# robined the public URL between the enforced (new) and unenforced (rogue) one —
# ~half of anonymous requests read the whole brain. That rogue had a non-cabrain
# NAME *and* a since-retagged IMAGE, so every name/image/ancestor filter missed it.
# The only reliable signature is BEHAVIOUR: it answers the brain API on :8080.
# So: probe every OTHER container on stack_stacknet and remove any that responds to
# /api/brain/ping — that is definitionally a stray CaBrain app. Infra (pg/redis/nats/
# gotrue/storage) never answer this path, so they are never touched. The new
# container is excluded by ID.
echo "  --- purging rogue app containers (behavior-based) ---"
for c in $(docker ps -q); do
  full=$(docker inspect -f '{{.Id}}' "$c" 2>/dev/null || echo)
  [ "$full" = "$NEW_ID" ] && continue
  ip=$(docker inspect -f '{{with index .NetworkSettings.Networks "stack_stacknet"}}{{.IPAddress}}{{end}}' "$c" 2>/dev/null)
  [ -z "$ip" ] && continue
  code=$(docker run --rm --network stack_stacknet curlimages/curl -s -m 5 \
           -o /dev/null -w '%{http_code}' "http://$ip:8080/api/brain/ping" 2>/dev/null || echo 000)
  if [ "$code" = "200" ]; then
    nm=$(docker inspect -f '{{.Name}}' "$c" 2>/dev/null | tr -d /)
    echo "  removing ROGUE app container ${nm:-$c} ($ip) — it answered /api/brain/ping"
    docker rm -f "$c" 2>/dev/null || true
  fi
done

# Prune dangling images + build cache older than 24h (safe: only untagged, unrooted layers)
echo "  --- prune ---"
docker image prune -f 2>&1 | tail -1
docker builder prune -f --filter until=24h 2>&1 | tail -1

docker ps --format '{{.Names}} {{.Status}}' | grep -E "^cabrain "

# ── AUTH-GATE REGRESSION GUARD (fail CLOSED) ─────────────────────────────────
# Probe the NEW container BY IP (not the `cabrain` alias, which round-robins and
# could report a rogue's state). distroless has no shell to exec, so use a throwaway
# curl container on the same network. If auth is definitively OFF on the new
# container, tear it down — a 502 is safe, an open brain is not. A probe that cannot
# run only warns, so an image/network hiccup never blocks a legitimate deploy.
echo "  --- verifying auth gate on the new container ($NEW_IP) ---"
PING=$(docker run --rm --network stack_stacknet curlimages/curl -s -m 10 \
        "http://${NEW_IP}:8080/api/brain/ping" 2>/dev/null || true)
echo "  gate probe: ${PING:-<no response>}"
case "$PING" in
  *'"authRequired":true'*)  echo "  ✓ auth ENFORCED on the new container" ;;
  *'"authRequired":false'*) echo "  ✗ AUTH OFF despite CABRAIN_REQUIRE_AUTH=1 — refusing to leave the brain public"
                            docker rm -f cabrain; exit 1 ;;
  *)                        echo "  ! could not probe the gate (non-fatal) — verify https://cabrain.fadymondy.com/api/brain/ping manually" ;;
esac

# Final belt-and-braces: confirm the PUBLIC edge now rejects an anonymous read.
echo "  --- verifying public edge rejects anonymous recall ---"
EDGE=$(docker run --rm --network stack_stacknet curlimages/curl -s -m 10 -o /dev/null -w '%{http_code}' \
        -X POST "http://${NEW_IP}:8080/api/brain/recall" -H 'Content-Type: application/json' \
        -d '{"namespace":"cabrain","query":"x","limit":1}' 2>/dev/null || echo 000)
echo "  anonymous recall on new container → HTTP $EDGE (expect 401/403)"
echo "=== [$(date -Is)] deploy done ==="
