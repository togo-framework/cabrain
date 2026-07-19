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
docker rm -f cabrain 2>/dev/null || true
docker run -d --name cabrain \
  --network stack_stacknet --restart unless-stopped \
  --env-file /deploy/.env \
  -e DATABASE_URL="postgresql://cabrain:${CABRAIN_PW}@pg:5432/cabrain?search_path=cabrain_auth,public" \
  -e DB_DRIVER=pgx -e CACHE_DRIVER=redis -e REDIS_URL=redis://redis:6379 \
  -e BRAIN_BM25_TOKENIZER=cabrain_ml \
  cabrain:latest

# Prune dangling images + build cache older than 24h (safe: only untagged, unrooted layers)
echo "  --- prune ---"
docker image prune -f 2>&1 | tail -1
docker builder prune -f --filter until=24h 2>&1 | tail -1
docker container prune -f --filter until=1h 2>&1 | tail -1

sleep 5
docker ps --format '{{.Names}} {{.Status}}' | grep -E "^cabrain "
echo "=== [$(date -Is)] deploy done ==="
