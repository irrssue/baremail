#!/usr/bin/env bash
# Deploy baremail to the homelab self-host.
#
# mail.irrssue.com is served by a pm2 process on the homelab (NOT Vercel):
#   cloudflare tunnel -> homelab localhost:3003 -> pm2 "baremail"
#   -> the Go backend binary, cwd ~/baremail/server, STATIC_DIR=~/baremail/dist
#
# The backend is written in Go (see ./server). This script cross-compiles the
# binary for the homelab (linux/amd64), builds the frontend locally, pushes both
# to the homelab, and (re)starts the pm2 process pointing at the binary.
#
# The homelab is not a git checkout, so it has no auto-pull.
#
# Usage: ./deploy.sh

set -euo pipefail

HOST="irrssue@homelab"
REMOTE="~/baremail"
PM2_APP="baremail"
GO=${GO:-go}

cd "$(dirname "$0")"

echo "==> Building frontend"
( cd baremail-app && npm run build )

echo "==> Cross-compiling Go backend for linux/amd64"
# Static-ish build: CGO off so the binary runs on the homelab without libc deps.
( cd server && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 "$GO" build -trimpath -o baremail-linux-amd64 . )

echo "==> Syncing dist/ to $HOST:$REMOTE/dist"
rsync -az --delete baremail-app/dist/ "$HOST:$REMOTE/dist/"

echo "==> Syncing backend binary"
# Copy to a staging name, then atomically move over the live binary so a running
# process is never reading a half-written file.
scp -q server/baremail-linux-amd64 "$HOST:$REMOTE/server/baremail.new"
ssh "$HOST" "mv -f $REMOTE/server/baremail.new $REMOTE/server/baremail && chmod +x $REMOTE/server/baremail"

echo "==> (Re)starting pm2 process '$PM2_APP' on the Go binary"
# pm2 currently runs the old `node index.js`. Re-point it at the compiled binary
# with the correct cwd (so it reads server/.env + server/sessions.json) and the
# homelab STATIC_DIR. `pm2 delete` is a no-op the first time after the switch.
ssh "$HOST" "
  set -e
  cd $REMOTE/server
  pm2 delete $PM2_APP >/dev/null 2>&1 || true
  STATIC_DIR=$REMOTE/dist pm2 start ./baremail --name $PM2_APP --cwd $REMOTE/server --update-env >/dev/null
  pm2 save >/dev/null
  pm2 describe $PM2_APP | grep -E 'status' | head -1
"

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

# Also confirm the API responds (the new binary actually serving, not just static).
API_OK=$(curl -fsS https://mail.irrssue.com/auth/status 2>/dev/null | grep -o 'authenticated' || true)

if [ "$LOCAL_JS" = "$LIVE_JS" ] && [ -n "$API_OK" ]; then
  echo "==> OK — live asset matches build ($LIVE_JS) and API is up"
else
  echo "!! Mismatch or API down:"
  echo "   built '$LOCAL_JS' / live '${LIVE_JS:-<no response>}' / api '${API_OK:-down}'"
  echo "   (cloudflare cache may lag; recheck in a moment)"
  exit 1
fi
