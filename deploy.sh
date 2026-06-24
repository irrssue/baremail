#!/usr/bin/env bash
# Deploy baremail to the homelab self-host as a Docker container.
#
# mail.irrssue.com is served by a Docker container on the homelab (NOT Vercel):
#   cloudflare tunnel -> homelab localhost:3003 -> baremail container
#   (image built on the homelab from this source via Dockerfile + compose.yaml)
#
# The homelab only needs Docker installed — the Go and Node toolchains live
# inside the multi-stage build, so nothing is compiled locally. server/.env and
# the token store (data/sessions.json) live on the homelab and are gitignored.
#
# Usage: ./deploy.sh

set -euo pipefail

HOST="irrssue@homelab"
REMOTE="baremail"   # ~/baremail on the homelab

cd "$(dirname "$0")"

echo "==> Syncing source to $HOST:~/$REMOTE"
# --delete keeps the remote tree clean; the excludes both skip the transfer and
# protect those remote paths from deletion (.env, the token store, the old pm2
# binary/node files, build artifacts).
rsync -az --delete \
  --exclude '.git' \
  --exclude '**/node_modules' \
  --exclude 'baremail-app/dist' \
  --exclude 'server/baremail' \
  --exclude 'server/baremail-linux-amd64' \
  --exclude 'server/.env' \
  --exclude 'server/sessions.json' \
  --exclude 'server/index.js' \
  --exclude 'server/package.json' \
  --exclude 'server/package-lock.json' \
  --exclude 'data' \
  ./ "$HOST:$REMOTE/"

echo "==> Building image and (re)starting container on the homelab"
ssh "$HOST" "
  set -e
  cd ~/$REMOTE
  # Seed the token store on the volume from the old pm2 location on first deploy.
  mkdir -p data
  if [ ! -f data/sessions.json ] && [ -f server/sessions.json ]; then
    cp server/sessions.json data/sessions.json
  fi
  # Retire the old pm2 process first so it releases :3003 before the container
  # tries to bind it (no-op once it's already gone).
  pm2 delete baremail >/dev/null 2>&1 || true
  pm2 save >/dev/null 2>&1 || true
  docker compose up -d --build
  docker compose ps
"

echo "==> Verifying live site"
# The cloudflare tunnel briefly drops (502) while the container restarts; retry.
API_OK=""
for i in 1 2 3 4 5; do
  API_OK=$(curl -fsS https://mail.irrssue.com/auth/status 2>/dev/null | grep -o 'authenticated' || true)
  [ -n "$API_OK" ] && break
  echo "   tunnel not ready yet (attempt $i), retrying..."
  sleep 3
done

LIVE_JS=$(curl -fsS https://mail.irrssue.com/ 2>/dev/null | grep -oE 'index-[A-Za-z0-9]+\.js' | head -1 || true)

if [ -n "$API_OK" ] && [ -n "$LIVE_JS" ]; then
  echo "==> OK — container is live, API up and serving $LIVE_JS"
else
  echo "!! API down or site not serving:"
  echo "   api '${API_OK:-down}' / live asset '${LIVE_JS:-<no response>}'"
  exit 1
fi
