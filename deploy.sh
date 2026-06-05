#!/usr/bin/env bash
# Deploy baremail to the homelab self-host.
#
# mail.irrssue.com is served by a pm2 process on the homelab (NOT Vercel):
#   cloudflare tunnel -> homelab localhost:3003 -> pm2 "baremail"
#   -> server/index.js, STATIC_DIR=~/baremail/dist
#
# The homelab is not a git checkout, so it has no auto-pull. This script
# builds the frontend locally and pushes the build + server to the homelab.
#
# Usage: ./deploy.sh

set -euo pipefail

HOST="irrssue@homelab"
REMOTE="~/baremail"
PM2_APP="baremail"

cd "$(dirname "$0")"

echo "==> Building frontend"
( cd baremail-app && npm run build )

echo "==> Syncing dist/ to $HOST:$REMOTE/dist"
rsync -az --delete baremail-app/dist/ "$HOST:$REMOTE/dist/"

echo "==> Syncing server/index.js"
scp -q server/index.js "$HOST:$REMOTE/server/index.js"

echo "==> Restarting pm2 process '$PM2_APP'"
ssh "$HOST" "pm2 restart $PM2_APP --update-env >/dev/null && pm2 describe $PM2_APP | grep -E 'status' | head -1"

echo "==> Verifying live site"
LOCAL_JS=$(grep -oE 'index-[A-Za-z0-9]+\.js' baremail-app/dist/index.html | head -1)

# The cloudflare tunnel briefly drops (502) while pm2 restarts, so retry.
LIVE_JS=""
for i in 1 2 3 4 5; do
  LIVE_JS=$(curl -fsS https://mail.irrssue.com/ 2>/dev/null | grep -oE 'index-[A-Za-z0-9]+\.js' | head -1) || true
  [ -n "$LIVE_JS" ] && break
  echo "   tunnel not ready yet (attempt $i), retrying..."
  sleep 3
done

if [ "$LOCAL_JS" = "$LIVE_JS" ]; then
  echo "==> OK — live asset matches build ($LIVE_JS)"
else
  echo "!! Mismatch: built '$LOCAL_JS' but live serves '${LIVE_JS:-<no response>}'"
  echo "   (cloudflare cache may lag; recheck in a moment)"
  exit 1
fi
