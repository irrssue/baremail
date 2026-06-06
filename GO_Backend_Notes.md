# Go Backend — Reference Notes

The `/server` backend was ported from **Node/Express → Go** (standard-library
`net/http`). The HTTP surface is byte-for-byte identical to the old Node server,
so the React frontend needed **zero changes**. This file is the map of what's
where and why.

> Direct port of the old `server/index.js`. If you ever need the original Node
> logic, see git history before commit `54ead3d` (the rewrite commit).

---

## TL;DR

- Language: **Go 1.26+**, std-lib `net/http` (no web framework)
- OAuth: `golang.org/x/oauth2`
- Gmail: `google.golang.org/api/gmail/v1`
- Token store: disk-backed (`sessions.json`), atomic + owner-only (0600), debounced writes
- Auth: `x-session-token` header (same as Node)
- Single origin: the Go server also serves the built `baremail-app/dist`
- Tests: `go test ./...` → **29 tests**, all green
- Deploy: `deploy.sh` cross-compiles linux/amd64 and re-points pm2 at the binary

---

## File layout (`/server`, ~1360 LOC total)

| File | LOC | Responsibility |
|------|-----|----------------|
| `main.go` | 390 | HTTP routes, OAuth config, CORS, auth middleware, static + SPA fallback, email handlers |
| `gmail.go` | 168 | Email structs, From-header parse, MIME body walk, relative-time + timestamp formatting, base64 decode |
| `send.go` | 135 | `POST /api/send`: recipient parse/validate, RFC 2822 MIME build (RFC 2047 subject), Gmail send, scope-403 mapping |
| `sessions.go` | 131 | Disk-backed token store: load / get / set / delete / debounced atomic flush, file-perm hardening |
| `googletoken.go` | 104 | Token JSON in `googleapis` wire shape (`expiry_date` millis) so the old Node `sessions.json` still loads |
| `dotenv.go` | 60 | Minimal `.env` loader (stand-in for Node's dotenv) |
| `gmail_test.go` | 160 | Tests: From-parse, MIME walk, relative-time, JSON shapes |
| `server_test.go` | 240 | Tests: store persistence, token merge, auth handlers, CORS, SPA fallback, traversal guard |
| `googletoken_test.go` | 105 | Tests: Node-shape token load + round-trip (the migration interop guarantee) |
| `go.mod` / `go.sum` | — | Module `baremail`, deps pinned |

---

## HTTP API contract (unchanged from Node)

Every route, method, and JSON shape matches the old Express server exactly.

| Method | Path | Auth | Response |
|--------|------|------|----------|
| GET | `/auth/google` | — | 302 redirect to Google consent (`access_type=offline`, scopes `gmail.readonly` + `gmail.send`) |
| GET | `/auth/google/callback?code=` | — | exchanges code, stores session, 302 → `CLIENT_URL?token=<sessionToken>` |
| GET | `/auth/status` | header | `{"authenticated": bool}` |
| GET | `/auth/logout` | header | `{"ok": true}` (deletes the session) |
| GET | `/api/emails?pageToken=` | **required** | `{"emails":[…], "nextPageToken": string\|null}` |
| GET | `/api/emails/:id` | **required** | full email (see below) |
| POST | `/api/send` | **required** | `{to,subject,body}` → `{"id": <sent msg id>}`; `403` if session lacks send scope |
| GET | `/*` (non-api/auth) | — | static file, else `index.html` (SPA fallback) |

**Auth**: `x-session-token: <hex>` header. Missing/unknown → `401 {"error":"Not authenticated"}`.

**`/api/emails` row** (`emailSummary`):
```json
{ "id","name","sender","subject","snippet","date","ts","unread" }
```
- `name`/`sender` parsed from the `From` header via regex `^"?([^"<]*)"?\s*<(.+)>$`
- `date` = relative label: today → `3:04 PM`, older → `Jan 2`
- `ts` = unix **millis** (for client-side sorting); `0` if Date header unparseable
- `unread` = `UNREAD` in the message's labelIds

**`/api/emails/:id`** (`emailFull`):
```json
{ "id","name","sender","subject","to","body","bodyHtml","snippet" }
```
- `body` = first `text/plain` part, `bodyHtml` = first `text/html` part (MIME tree walked depth-first)
- GitHub-style notifications carry the rich card in `bodyHtml`; the frontend prefers it, falls back to `body`

---

## Environment variables

Same names the Node server used. Read from `server/.env` (working dir) by `dotenv.go`,
or from the real environment (real env always wins).

| Var | Default | Purpose |
|-----|---------|---------|
| `PORT` | `3001` | Listen port (homelab uses `3003`) |
| `CLIENT_URL` | `http://localhost:5173` | Frontend origin; CORS + post-auth redirect target |
| `CLIENT_ID` | — | Google OAuth client id |
| `CLIENT_SECRET` | — | Google OAuth client secret |
| `REDIRECT_URI` | `http://localhost:3001/auth/google/callback` | OAuth callback |
| `STATIC_DIR` | `../baremail-app/dist` (rel. to binary) | Built frontend to serve |
| `SESSIONS_FILE` | `sessions.json` (next to binary) | Token store path |

---

## Key design decisions / gotchas

### 1. Token persistence in `googleapis` wire shape — **the migration-critical bit**
`googletoken.go` exists for one reason: the live `sessions.json` on the homelab
was written by the **old Node `googleapis`** library, which serializes tokens as:
```json
{ "access_token","refresh_token","scope","token_type","expiry_date": <unix millis> }
```
But Go's native `oauth2.Token` JSON uses `expiry` as an **RFC3339 string**, not
`expiry_date` millis. A naive Go load would:
1. read `access_token` ✓ but ignore `expiry_date` → `Expiry` = zero value
2. treat every token as **expired** → try to refresh
3. fail on sessions with no `refresh_token` → **log everyone out**

`googleToken` is an `oauth2.Token` wrapper with custom `MarshalJSON` /
`UnmarshalJSON` that bridges `expiry_date` (millis) ↔ `Expiry`, and round-trips
any extra fields (`scope`, etc.) losslessly. This is what kept logged-in users
logged in across the Node→Go swap. **Don't remove it** unless every live session
is known to use the native shape.

> Note: many Google sessions never carry a `refresh_token` (Google only returns
> it on first consent). Those sessions die when the ~1h access token expires —
> same under Node and Go. Not a Go regression.

### 2. Token rotation persisted back to disk
`persistingSource` (in `main.go`) wraps the standard refreshing token source.
When Google mints a fresh access token, it merges (`{...old, ...fresh}` —
keeping the old refresh_token if the refresh response omits one) and writes back
to the store. Mirrors the Node `oauth2Client.on("tokens", …)` handler.

### 3. Concurrent inbox fetch
`/api/emails` lists 20 message ids, then fetches each message's metadata
**concurrently** (goroutines + `sync.WaitGroup`), writing into a fixed-size slice
by index so **Gmail's list order is preserved**. This is the `Promise.all`
equivalent. First error aborts with a 500, same as Node.

### 4. Atomic, owner-only session writes
`sessions.go` `flush()`: marshal → write to `sessions.json.<pid>.tmp` (mode
0600) → `os.Rename` over the real file. Guarantees the tokens file is never
momentarily world-readable and a crash mid-write can't truncate it. Writes are
debounced 200ms so rapid mutations coalesce. The file + its parent dir are
chmod'd 0600/0700 on every boot.

### 5. Static serve + SPA fallback + traversal guard
`handleStatic`: `filepath.Clean` the path, confirm it stays within `STATIC_DIR`
(`within()` helper), serve the file if it exists, else fall back to
`index.html`. `/api` and `/auth` have their own mux entries so they never reach
here.

### 6. Send scope is additive — old sessions can't send until re-consent
`/auth/google` now requests `gmail.readonly` **+** `gmail.send`. Sessions that
consented under readonly-only keep reading fine but Google rejects their send
calls with a 403 (insufficient scope). `handleSend` detects that
(`googleapi.Error` code 403/401) and returns its own 403 with a "log out and
sign in again" message, so the frontend can prompt re-auth instead of showing a
generic failure. New logins get both scopes in one consent screen.

The wire body is plain-text only: `buildMIME` writes To / Subject (RFC 2047
encoded-word so non-ASCII subjects survive) / `text/plain; charset=UTF-8`, CRLF
line endings, and lets Gmail fill From / Date / Message-ID. Recipients go
through `net/mail.ParseAddressList` (comma-separated, each validated).

### 7. Relative-time formatting parity
Go's `time` reference-time layout reproduces the JS `toLocaleTimeString` /
`toLocaleDateString` en-US output: `3:04 PM` (leading-zero hour stripped) for
today, `Jan 2` for older. `parseDate` tolerates the RFC formats Gmail Date
headers use and strips trailing `(UTC)`-style comments.

---

## Dev

```bash
cd server && go run .          # :3001 (API + serves built frontend)
cd server && go test ./...     # 29 unit + interop tests
cd server && go vet ./...
```
Needs Go 1.26+. Reads `server/.env` from the working dir.

Build a local binary:
```bash
cd server && go build -o baremail .
```

---

## Deploy (homelab, linux/amd64)

```
mail.irrssue.com → cloudflare tunnel → homelab localhost:3003
  → pm2 "baremail" → Go binary (~/baremail/server/baremail)
    cwd ~/baremail/server, STATIC_DIR=~/baremail/dist
```

`./deploy.sh` does:
1. `npm run build` the frontend
2. cross-compile: `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o baremail-linux-amd64 .`
   → static ELF, no libc deps, runs on the homelab with **no Go installed**
3. rsync `dist/` + scp binary (staged `.new` then atomic `mv`)
4. pm2: `delete` then `start ./baremail --cwd ~/baremail/server` with `STATIC_DIR`
   → cwd is what makes the binary find `.env` + `sessions.json`
5. verify live asset hash **and** that `/auth/status` responds

A 502 during pm2 restart is normal (tunnel reconnect); the script retries.

**pm2 runs a compiled binary now, interpreter `none`** — not `node index.js`.

### Homelab files (gitignored, live on the box)
- `~/baremail/server/.env` — OAuth secrets
- `~/baremail/server/sessions.json` — live tokens (8 sessions at migration time)
- `~/baremail/server/baremail` — the deployed binary
- Old Node leftovers (`index.js`, `package*.json`, `node_modules`) sit idle —
  pm2 ignores them. Safe to delete:
  ```
  ssh irrssue@homelab 'rm -rf ~/baremail/server/{index.js,package*.json,node_modules}'
  ```

---

## Dependencies (`go.mod`, module `baremail`)

Direct:
- `google.golang.org/api/gmail/v1` — Gmail REST client
- `google.golang.org/api/option` — client options (token source)
- `golang.org/x/oauth2` (+ `/google`) — OAuth2 flow + token refresh

Everything else in `go.mod` is `// indirect` (transitive: cloud auth, otel,
grpc, protobuf, x/net, x/crypto…). No external web framework, no external dotenv.

---

## Test inventory (`go test ./...` → 29)

- **gmail_test.go** — `splitFrom`, `headerValue`, `hasLabel`, `relTime` (same-day /
  other-day / empty / garbage), `tsOf`, `walkBody` (first-of-each / nested),
  summary + full JSON key shapes
- **send_test.go** — `parseRecipients` (single / list / display-name / blank /
  garbage), `buildMIME` (header shape + no bare LF), non-ASCII subject is RFC 2047
  encoded, `normalizeCRLF`
- **server_test.go** — store persist+reload (0600 check), delete, token merge
  (keeps refresh / nil old), `/auth/status`, `/auth/logout`, requireAuth rejects
  missing token, CORS preflight, SPA fallback, traversal guard, random token
- **googletoken_test.go** — loads Node-shape token (`expiry_date` millis),
  round-trip preserves `scope` + re-emits millis, store reads a full Node-shape
  `sessions.json` end-to-end

Run a single test: `go test ./... -run TestWalkBodyNested -v`
