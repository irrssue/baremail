# baremail

A bare, minimal email reader. Gmail OAuth → read-only inbox view. Nothing more.

## Structure
- `/baremail-app`: Vite + React 19 frontend (Tailwind v4 + hand-written design system)
- `/server`: **Go** backend — Google OAuth, Gmail API proxy, serves built frontend
- `/PRIVACY`: privacy policy assets

## Stack
- Frontend: React 19, Vite 7, Tailwind v4 (`@tailwindcss/vite`)
- Backend: **Go** (`net/http` std lib), `google.golang.org/api/gmail/v1` +
  `golang.org/x/oauth2`, `github.com/yuin/goldmark` (Markdown→HTML on send).
  Disk-backed token store (`sessions.json`, atomic 0600 write), header-based
  token auth (`x-session-token`)
- Single-origin deploy: the Go server serves the built `baremail-app/dist`

### Go backend layout (`/server`)
- `main.go` — HTTP routes, OAuth config, CORS, static + SPA fallback
- `sessions.go` — disk-backed token store (debounced atomic write, owner-only)
- `googletoken.go` — token JSON in `googleapis` wire shape (`expiry_date` millis)
  so sessions written by the old Node server still load
- `gmail.go` — header parse, MIME body walk, relative-time formatting,
  thread→inbox-row summary (`summarizeThread`) and thread→reader (`buildThread`)
- `send.go` — `/api/send`: recipient validation, Markdown render, multipart/alternative build, reply threading
- `profile.go` — `/api/profile`: signed-in Google account (name/email/photo) via the OIDC userinfo endpoint
- `dotenv.go` — minimal `.env` loader (stands in for Node's dotenv)
- `*_test.go` — unit + interop tests (`go test ./...`)

API contract:
`GET /auth/google` · `/auth/google/callback?code=` → redirect `CLIENT_URL?token=` ·
`/auth/status` → `{authenticated}` · `/auth/logout` → `{ok}` ·
`/api/emails?pageToken=` → `{emails:[{id,name,sender,subject,snippet,date,ts,unread,count}],nextPageToken}`
(one row per **conversation** — `id` is a Gmail thread id, the latest message
gives who/when/snippet, `unread` if any message is, `count` = messages in the
thread; `q` passes through as a Gmail thread search) ·
`/api/emails/:id` (`:id` is a **thread id**) →
`{id,threadId,subject,name,sender,to,messageId,references,snippet,messages:[{id,messageId,name,sender,to,date,ts,body,bodyHtml,snippet,unread}]}`
(`messages` is the whole conversation oldest→newest; the top-level
`sender`/`messageId`/`threadId` are the latest message's, so a reply threads
into the same conversation) ·
`POST /api/send` `{to,cc,bcc,subject,body,inReplyTo,threadId}` → `{id,threadId}`
(body is **Markdown** → rendered server-side via goldmark into a
`multipart/alternative` message; `inReplyTo`+`threadId` thread a reply; 403 if
the session was consented before the send scope existed) ·
`GET /api/profile` → `{name,email,picture}` (signed-in Google account for the
topbar profile chip, fetched from the OIDC userinfo endpoint; 403 if the session
predates the userinfo scopes → frontend falls back to an initials chip).
OAuth scopes: `gmail.readonly` + `gmail.send` + `userinfo.profile` +
`userinfo.email`.
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

Layout: sticky serif brand topbar (right cluster = profile avatar · search ·
compose), 820px centered column, mono footer. The avatar opens a dropdown
(account identity · Settings · Sign out); Settings reuses the reader shell.
List row = `who | subject·snippet | relative-time` (one per conversation; a mono
count chip after the sender when the thread has >1 message). Reader = serif
subject over a stacked **conversation**: each message a mono From/To head + prose
body, latest + any unread expanded, the rest collapsed to a clickable summary
line (single-message threads render as a plain one-message reader). Mobile
breakpoint at 600px.

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

Self-hosted on the homelab as a **Docker container** — **not Vercel**. Routing:

```
mail.irrssue.com → cloudflare tunnel → homelab localhost:3003
  → baremail Docker container (published 127.0.0.1:3003->3003)
    → Go binary, STATIC_DIR=/app/dist, SESSIONS_FILE=/data/sessions.json
```

The image is a **multi-stage build** (`Dockerfile`): Node builds the frontend,
golang compiles the backend, both land in a tiny Alpine runtime. It is built
**on the homelab** (`docker compose up -d --build`), so the homelab needs only
Docker — no Go or Node toolchain, and the dev Mac needs no Go to deploy.

The homelab (`ssh irrssue@homelab`, dir `~/baremail`) is **not a git checkout**
and has no auto-pull. `server/.env` (OAuth secrets + `PORT=3003`) lives there
gitignored and is wired in via `compose.yaml`'s `env_file`; `compose.yaml`
overrides `STATIC_DIR`/`SESSIONS_FILE`/`PORT` for the in-container paths. The
token store persists on the `~/baremail/data` bind-mount volume
(`data/sessions.json`), seeded once from the old `server/sessions.json`. After
any frontend or server change, deploy with:

```
./deploy.sh   # rsync source → docker compose up -d --build on the homelab
              # → retire old pm2 process → verify API + served asset
```

A 502 during the container restart is normal (tunnel reconnect); the script
retries. The container runs `restart: unless-stopped` with a `/auth/status`
healthcheck.

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
