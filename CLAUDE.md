# baremail

A bare, minimal email reader. Gmail OAuth → read-only inbox view. Nothing more.

## Structure
- `/baremail-app`: Vite + React 19 frontend (Tailwind v4 + hand-written design system)
- `/server`: Express backend — Google OAuth, Gmail API proxy, serves built frontend
- `/PRIVACY`: privacy policy assets

## Stack
- Frontend: React 19, Vite 7, Tailwind v4 (`@tailwindcss/vite`)
- Backend: Express, `googleapis`, in-memory token store, header-based token auth (`x-session-token`)
- Single-origin deploy: Express serves the built `baremail-app/dist`

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
cd server && npm start          # :3001 (API + serves built frontend)
cd baremail-app && npm run dev  # :5173 (Vite, proxies /api + /auth to :3001)
cd baremail-app && npm run build
```

## Deploy

Self-hosted on the homelab — **not Vercel**. Routing:

```
mail.irrssue.com → cloudflare tunnel → homelab localhost:3003
  → pm2 process "baremail" → server/index.js, STATIC_DIR=~/baremail/dist
```

The homelab (`ssh irrssue@homelab`, dir `~/baremail`) is **not a git checkout**
and has no auto-pull. After any frontend or server change, deploy with:

```
./deploy.sh   # build → rsync dist + scp server/index.js → pm2 restart → verify
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
