#!/usr/bin/env bash
set -e
export DOCKER_HOST=unix:///mnt/wsl/docker-desktop/shared-sockets/host-services/docker.proxy.sock
export DOCKER_CONFIG=/tmp/dc-clean
export PATH=/usr/bin:/usr/local/bin:$PATH

SRC=/home/fadymondy/services/cabrain-src
LOG=/home/fadymondy/services/cabrain-deploy/last.log
: > "$LOG"; exec >>"$LOG" 2>&1
echo "=== [$(date -Is)] deploy triggered ==="

cd "$SRC"
git fetch --all --prune 2>&1 | tail -3
git reset --hard origin/main 2>&1 | tail -3
echo "  HEAD: $(git rev-parse --short HEAD) — $(git log -1 --pretty=%s)"

sudo -E DOCKER_CONFIG=/tmp/dc-clean DOCKER_BUILDKIT=1 docker build -t cabrain:latest . 2>&1 | tail -15

. /mnt/c/services/cabrain/.env
CABRAIN_PW=$(echo "$CABRAIN_DATABASE_URL" | sed -E 's|.*://cabrain:([^@]+)@.*|\1|')

sudo -E docker rm -f cabrain 2>/dev/null || true
sudo -E docker run -d --name cabrain \
  --network stack_stacknet --restart unless-stopped \
  --env-file /mnt/c/services/cabrain/.env \
  -e DATABASE_URL="postgresql://cabrain:${CABRAIN_PW}@pg:5432/cabrain?search_path=cabrain_auth,public" \
  -e DB_DRIVER=pgx -e CACHE_DRIVER=redis -e REDIS_URL=redis://redis:6379 \
  -e BRAIN_BM25_TOKENIZER=cabrain_ml \
  cabrain:latest
sleep 5
sudo -E docker ps --format '{{.Names}} {{.Status}}' | grep -E "^cabrain "
echo "=== [$(date -Is)] deploy done ==="
