# Vue SPA Migration Plan

Date: 2026-06-15

Status: design agreed; implementation not started.

## Scope

Replace the embedded single-page Alpine.js UI with two Vue 3 single-page apps —
the user **dashboard** (served at `/`) and the super-admin **console** (served at
`/admin`) — without changing the backend API/auth contract. The migration must:

1. **Preserve the existing embed.** The release binary keeps serving the UI from
   `go:embed`; no Node runtime in production, single binary, same routes.
2. **Add local UI development.** Run a Vite dev server with HMR and reach the real
   backend API through a dev proxy, so the UI can be developed against live data
   without rebuilding the Go binary.
3. **Draw clean boundaries** between the three runtime modes (production embed /
   local dev / build) so each is obviously correct and hard to misuse.

This is almost entirely a frontend-tooling-and-embed change. The backend stays
as-is apart from a trivial change to *where* the embedded assets come from and a
"UI not built" guard.

## Why This Is Low-Risk On The Backend

The current UI already talks to a clean seam:

- One Go listener (`server/internal/serve/serve.go`) serves MCP (`/mcp/`), the
  HTTP connector (`/http/`), the UI pages, and the `/api/*` JSON endpoints from
  the same origin.
- Auth is an HttpOnly sealed session cookie: `SameSite=Lax`, and
  `Secure: isHTTPS(r)` (`server/internal/ui/session.go`). Over plain
  `http://localhost` the cookie is **not** marked Secure, so a dev proxy works
  without TLS.
- The frontend only does same-origin `fetch('/api/...')`; cookies ride along.
- Views are already hash-routed (`#view=connectors`, `#tasks=<dir>`), which maps
  directly onto Vue Router hash mode, so existing URLs keep working.

So the API contract is the boundary; the Vue apps consume it exactly as Alpine
does today.

## Current State (what is being replaced)

`server/internal/ui/ui.go` embeds `assets/*` and serves two hand-written pages:

| Route    | HTML          | JS          | Alpine factory     |
|----------|---------------|-------------|--------------------|
| `/`      | `index.html`  | `script.js` | `memdApp()`        |
| `/admin` | `admin.html`  | `admin.js`  | `memdAdminApp()`   |

Shared `style.css`, `alpine.min.js`, favicon, logos, and `icons/*.svg`. The
`/assets/` route is a `http.FileServer` over the embedded FS, wrapped in a
`no-cache` handler (embedded files have no modtime, so without it there is no
cache validator).

## Decisions

Agreed on 2026-06-15:

1. **One Vite project, two entry points** — a single `web/` project that builds
   two HTML entries (`dashboard.html`, `admin.html`). Shared api client, session
   handling, theme, and CSS are plain imports; Vite code-splitting keeps the
   admin bundle out of the dashboard. One `node_modules`, one dev server, one
   config. Still ships two distinct builds.
2. **Build-only embed, gitignore `dist/`** — the Vite output is not committed.
   `build/build.sh` runs the frontend build before `go build`. Clean git history;
   `build/build.sh` becomes the required build path (see the embed contract
   below for how `go build` still compiles).
3. **TypeScript** — typed components, stores, and api client. Catches drift
   against the Go JSON contract at build time.
4. **Vue 3 + Vue Router (hash mode), no Pinia** — Router preserves the existing
   `#view=` URLs; shared state lives in composables (`useSession`, `useTheme`,
   …). Pinia can be added later if cross-view state grows.

## Target Architecture: Three Runtime Boundaries

### 1. Production (embedded) — unchanged contract

`go build` embeds the built Vue output and serves it. No Node at runtime, single
binary, same routes. The only change versus today is that the embedded files are
Vite output under `dist/` instead of hand-written `assets/`.

`index()` serves `dist/dashboard.html`; `adminIndex()` serves `dist/admin.html`;
`/assets/` serves the hashed bundles from the embedded `dist/assets`. Because
Vite content-hashes filenames, caching is correct by construction: long-cache the
hashed assets and only no-cache the two HTML entry documents (the current blanket
`no-cache` hack can be dropped for assets).

### 2. Local UI development (the new capability)

Two terminals:

```bash
# Terminal 1: the real backend + DB on :7878
./dist/<os>/memd-... serve --init-db

# Terminal 2: Vite dev server with HMR on :5173, proxying API/auth to :7878
npm --prefix web run dev
```

Vite proxy config:

```ts
// web/vite.config.ts
server: {
  proxy: {
    '/api':  'http://127.0.0.1:7878',
    '/auth': 'http://127.0.0.1:7878',
    '/http': 'http://127.0.0.1:7878',
  },
}
```

The browser sees only the Vite origin (`:5173`); Vite forwards `/api` and `/auth`
to the Go backend. The session cookie flows because the backend (seeing plain
http from the proxy) does not set `Secure`, and `SameSite=Lax` is fine
first-party to the Vite origin.

**One caveat:** the OIDC `/auth/callback` redirect is absolute to `baseURL`
(`:7878`), so SSO login does **not** transparently round-trip through `:5173`.
Local-password login is the dev path; SSO is tested against the real server.
Everything else proxies cleanly.

### 3. Build

`build/build.sh` orchestrates: run the Vite build (emits into
`server/internal/ui/dist/`), then `go build` embeds it.

```sh
build_web() {
  command -v npm >/dev/null || { echo "npm is required to build the web UI" >&2; exit 1; }
  (cd web && npm ci && npm run build)   # outDir → ../server/internal/ui/dist
}
```

`build_web` runs before `build_one` in the `host` and `all` targets. `clean`
also clears `dist/` back to just the sentinel.

## The Embed Contract (the sharp edge of "gitignore dist/")

`//go:embed dist` is a **compile error** when the directory has no matching
files, and a gitignored `dist/` is empty on a fresh checkout. Resolution:

- Commit a single tracked sentinel `server/internal/ui/dist/.gitkeep`; gitignore
  everything else under `dist/`:

  ```gitignore
  /server/internal/ui/dist/*
  !/server/internal/ui/dist/.gitkeep
  ```

- Embed with `//go:embed all:dist`. The `all:` prefix includes the dotfile
  sentinel, so the package always compiles even before the frontend build runs.

- Runtime guard: if `dist/dashboard.html` is absent (bare `go build`, no frontend
  build), the handler returns a clear page —
  *"UI not built. Run `npm --prefix web run build` (or `build/build.sh host`)."*
  — instead of a confusing 404.

Consequence, stated plainly: `build/build.sh` is the required build path (already
true per the README). A bare `go build` compiles and runs but serves the
"not built" page until the Vite build runs. This is the accepted cost of keeping
the build output out of git.

## Repo Layout

```
web/                         # all frontend lives here
  package.json
  vite.config.ts             # multi-page: dashboard.html + admin.html entries
  tsconfig.json
  dashboard.html             # entry → src/dashboard/main.ts
  admin.html                 # entry → src/admin/main.ts
  src/
    shared/                  # api client, session/theme composables, css tokens, icons
    dashboard/               # the "/" app
    admin/                   # the "/admin" app

server/internal/ui/
  ui.go                      # //go:embed all:dist  (was assets/*)
  dist/                      # Vite output (gitignored except .gitkeep)
    .gitkeep
    dashboard.html
    admin.html
    assets/*
```

The current `assets/` (favicon, logos, icons, css) migrates into the Vue
project: static files into Vite's `public/`, `style.css` imported as-is at first
and refactored into components/scoped styles later.

## Migration Phases

**Phase 0 — Pipeline + boundaries.** Stand up `web/` (Vite + Vue 3 + TS + Router),
the dev proxy, the multi-page build into `dist/`, the `go:embed all:dist` switch,
the runtime "not built" guard, the `.gitignore` sentinel, and the `build.sh`
`build_web` step. Prove the full chain (dev proxy → build → embed → serve) with a
placeholder page before porting real views.

**Phase 1 — Admin console pilot (`/admin`).** Port the super-admin console first:
it is small (~295 HTML / ~437 JS lines), self-contained, and security-sensitive
enough to deserve a clean rebuild. It exercises login, session, theme, the api
client, and the embed end-to-end at low risk. Ship it serving from Vue while the
dashboard is still Alpine (the two pages are independent documents, so they can
diverge in framework during the migration).

**Phase 2 — Dashboard (`/`).** Port view-by-view behind Vue Router, one existing
hash view at a time (Teams, Directories, Tasks, Connectors, Activity, Info),
reusing `style.css` initially. Keep the `#view=` / `#tasks=<dir>` URL scheme for
back-compat.

**Phase 3 — Cleanup.** Remove `alpine.min.js`, the old `index.html` / `admin.html`
/ `script.js` / `admin.js`, refactor shared CSS into components, and tighten
caching headers now that assets are content-hashed.

## Go Server Changes (minimal)

- `server/internal/ui/ui.go`: change the embed directive to `//go:embed all:dist`;
  point `index()` / `adminIndex()` at `dist/dashboard.html` / `dist/admin.html`;
  serve `/assets/` from `dist/assets`; add the "not built" guard; relax cache
  headers for hashed assets.
- No changes to `/api/*`, auth, session, OIDC, MCP, or HTTP-connector handlers.

## Open Items / Risks

- **Bare `go build` UX.** Mitigated by the "not built" guard + README note that
  `build/build.sh` is the build path. Revisit if `go install` from the module
  path needs to yield a working UI (would require committing `dist/`).
- **CI.** Needs a Node step before the Go build; cache `node_modules`. Optionally
  lint/type-check the frontend.
- **SSO in dev.** Not proxyable due to the absolute callback URL; documented as a
  known limitation (use password login locally).
- **Asset path stability.** Existing deep links to `/assets/...` (icons in
  external docs, if any) change to hashed names; keep stable public paths for
  anything externally referenced via Vite `public/`.
- **Bundle separation.** Admin and dashboard share a project; verify code-split
  output so the dashboard never pulls admin chunks (and vice versa).
