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

Light, typographic — ported from news.irrssue.com. All tokens live in
`baremail-app/src/index.css` `:root`. **Keep this section in sync with that file.**

| Token | Value | Use |
|-------|-------|-----|
| `--bg` | `#ffffff` | Page background |
| `--bg-2` | `#f5f5f4` | Raised surface |
| `--bg-3` | `#ececea` | Active pill / kbd |
| `--ink` | `#1a1a1a` | Primary text |
| `--ink-2` | `#44444a` | Secondary text |
| `--ink-3` | `#76767a` | Faint / metadata |
| `--ink-4` | `#a6a6a6` | Disabled / separators |
| `--rule` | `#e6e6e3` | Dividers, borders |
| `--rule-2` | `#dcdcd9` | Stronger dividers |
| `--op` / `--hot` | `#826b49` | Warm accent |

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
