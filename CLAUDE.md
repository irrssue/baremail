# baremail

A bare, minimal email reader. Gmail OAuth → read-only inbox view. Nothing more.

## Structure
- `/baremail-app`: Vite + React 19 frontend (Tailwind v4 + hand-written design system)
- `/server`: **Go** backend — Google OAuth, Gmail API proxy, serves built frontend
- `/PRIVACY`: privacy policy assets

## Stack
- Frontend: React 19, Vite 7, Tailwind v4 (`@tailwindcss/vite`)
- Backend: **Go** (`net/http` std lib), `google.golang.org/api/gmail/v1` +
  `golang.org/x/oauth2`. Disk-backed token store (`sessions.json`, atomic 0600
  write), header-based token auth (`x-session-token`)
- Single-origin deploy: the Go server serves the built `baremail-app/dist`

### Go backend layout (`/server`)
- `main.go` — HTTP routes, OAuth config, CORS, static + SPA fallback
- `sessions.go` — disk-backed token store (debounced atomic write, owner-only)
- `googletoken.go` — token JSON in `googleapis` wire shape (`expiry_date` millis)
  so sessions written by the old Node server still load
- `gmail.go` — header parse, MIME body walk, relative-time formatting
- `dotenv.go` — minimal `.env` loader (stands in for Node's dotenv)
- `*_test.go` — unit + interop tests (`go test ./...`)

API contract (unchanged from the old Node server, byte-for-byte JSON):
`GET /auth/google` · `/auth/google/callback?code=` → redirect `CLIENT_URL?token=` ·
`/auth/status` → `{authenticated}` · `/auth/logout` → `{ok}` ·
`/api/emails?pageToken=` → `{emails:[{id,name,sender,subject,snippet,date,ts,unread}],nextPageToken}` ·
`/api/emails/:id` → `{id,name,sender,subject,to,body,bodyHtml,snippet}`.
Env: `PORT CLIENT_URL CLIENT_ID CLIENT_SECRET REDIRECT_URI STATIC_DIR SESSIONS_FILE`.

## Design system

Dark, typographic. All tokens live in
`baremail-app/src/index.css` `:root`. **Keep this section in sync with that file.**

| Token | Value | Use |
|-------|-------|-----|
| `--bg` | `#1a1a1a` | Page background |
| `--bg-2` | `#232323` | Raised surface |
| `--bg-3` | `#2c2c2c` | Active pill / kbd |
| `--ink` | `#f3f3f1` | Primary text |
| `--ink-2` | `#c9c9c5` | Secondary text |
| `--ink-3` | `#8d8d88` | Faint / metadata |
| `--ink-4` | `#5e5e5a` | Disabled / separators |
| `--rule` | `#2e2e2e` | Dividers, borders |
| `--rule-2` | `#3a3a3a` | Stronger dividers |
| `--op` / `--hot` | `#b89a6a` | Warm accent |

Fonts: **Fraunces** (titles/brand), **JetBrains Mono** (meta, time, labels),
**Inter** (body). Loaded via Google Fonts in `baremail-app/index.html`.

Layout: sticky serif brand topbar, 820px centered column, mono footer.
List row = `who | subject·snippet | relative-time`. Reader = serif subject,
mono From/To head, prose body. Mobile breakpoint at 600px.

## Dev
```
cd server && go run .           # :3001 (API + serves built frontend)
cd server && go test ./...      # unit + interop tests
cd baremail-app && npm run dev  # :5173 (Vite, proxies /api + /auth to :3001)
cd baremail-app && npm run build
```
The Go server reads `server/.env` from its working dir (same vars the Node
server used). Requires Go 1.26+.

## Deploy

Self-hosted on the homelab — **not Vercel**. Routing:

```
mail.irrssue.com → cloudflare tunnel → homelab localhost:3003
  → pm2 process "baremail" → Go binary (~/baremail/server/baremail),
    cwd ~/baremail/server, STATIC_DIR=~/baremail/dist
```

The homelab is **linux/amd64**, so `deploy.sh` cross-compiles the Go backend
(`GOOS=linux GOARCH=amd64`). The homelab (`ssh irrssue@homelab`, dir `~/baremail`)
is **not a git checkout** and has no auto-pull — it does not need Go installed,
only the compiled binary. `server/.env` and `server/sessions.json` live on the
homelab (gitignored); the binary runs with cwd `~/baremail/server` so it finds
both. After any frontend or server change, deploy with:

```
./deploy.sh   # build frontend → cross-compile Go → rsync dist + binary
              # → pm2 (re)start on the binary → verify asset + API
```

A 502 during the pm2 restart is normal (tunnel reconnect); the script retries.

**Ship live after every change.** Once a frontend or server change is committed
and pushed, run `./deploy.sh` to push it to the homelab. A commit is not done
until it is live at mail.irrssue.com. Never finish a task without deploying.

## Contact
Any public-facing contact email (footer, privacy, docs) must be `liam@irrssue.com`.

## Git workflow
**Commit and push after every change.** No matter how small:
1. `git add` the changed files
2. `git commit` with a readable, descriptive message — describe the change in
   plain English (what behaviour/layout/fix shipped), not the file touched
3. `git push origin main`

Never finish a task without committing and pushing. Write commit messages like an
experienced engineer reading `git log` six months later would understand without
opening the diff. Never use generic messages like "update files".

- Never commit `.DS_Store` (in `.gitignore`)
- Never commit `.env` or OAuth secrets
