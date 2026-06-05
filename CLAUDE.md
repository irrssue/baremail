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

Dark-only, typographic — ported from news.irrssue.com. All tokens live in
`baremail-app/src/index.css` `:root`. **Keep this section in sync with that file.**

| Token | Value | Use |
|-------|-------|-----|
| `--bg` | `#0b0d0f` | Page background |
| `--bg-2` | `#141414` | Raised surface |
| `--bg-3` | `#1a1a1a` | Active pill / kbd |
| `--ink` | `#e2e2e1` | Primary text |
| `--ink-2` | `#b8b8b7` | Secondary text |
| `--ink-3` | `#8a8a89` | Faint / metadata |
| `--ink-4` | `#585857` | Disabled / separators |
| `--rule` | `#1e1e1c` | Dividers, borders |
| `--rule-2` | `#262624` | Stronger dividers |
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
