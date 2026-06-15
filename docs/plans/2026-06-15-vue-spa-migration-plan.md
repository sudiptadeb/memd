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
   local dev / build), with `build/build.sh` as the one correct build path.

This is almost entirely a frontend-tooling-and-embed change. The backend stays
as-is apart from a one-line change to *where* the embedded assets come from.

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

1. **One Vite project, folder-per-app, single build.** A single `web/` project
   with each app as a folder under `src/apps/<app>/index.html` (`dashboard`,
   `admin`). A single `vite build` (multi-entry) emits both apps; route-level
   code-splitting keeps the dashboard from loading admin view code. The dev
   server hosts both apps from one process. (Fully isolated per-app bundles via a
   `VITE_APP` build loop are a documented option, not the default — see below.)
2. **Build-only embed; `build.sh` is the one path.** `dist/` is gitignored and
   never committed. `build/build.sh` runs the frontend build, then `go build`
   embeds the result. A bare `go build` *without* a prior frontend build simply
   **fails to compile** — that is acceptable and expected; the supported path is
   always `build/build.sh`. No sentinel files, no runtime fallbacks.
3. **TypeScript** — typed components and api client. Catches drift against the Go
   JSON contract at build time.
4. **Vue 3 + Vue Router (hash mode), no Pinia** — Router preserves the existing
   `#view=` URLs; shared state lives in composables (`useSession`, `useTheme`,
   …) plus a tiny event bus for ephemeral signals (toasts, full-page loader).
   Pinia can be added later if cross-view state grows.

## Target Architecture: Three Runtime Boundaries

### 1. Production (embedded) — unchanged contract

`go build` embeds the built Vue output and serves it. No Node at runtime, single
binary, same routes. The only change versus today is that the embedded files are
Vite output under `dist/` instead of hand-written `assets/`.

With Vite `root: src/apps`, each app emits its HTML at its root-relative path, so
the build produces `dist/dashboard/index.html` and `dist/admin/index.html` —
which map 1:1 onto Go's existing two routes. `index()` serves
`dist/dashboard/index.html`; `adminIndex()` serves `dist/admin/index.html`;
`/assets/` serves the hashed bundles from `dist/assets/` (via
`fs.Sub(embedFS, "dist")`). Because Vite content-hashes filenames, caching is
correct by construction: long-cache the hashed assets and only no-cache the two
HTML entry documents (the current blanket `no-cache` hack can be dropped for
assets).

### 2. Local UI development (the new capability)

Two terminals:

```bash
# Terminal 1: the real backend + DB on :7878
./dist/<os>/memd-... serve --init-db

# Terminal 2: Vite dev server with HMR on :5173, hosting both apps, proxying to :7878
npm --prefix web run dev
```

Vite proxy config (plain http, no certs, no manual CORS — same-origin via the
proxy means the browser only ever sees the Vite origin):

```ts
// web/vite.config.ts
server: {
  proxy: {
    '/api':  { target: 'http://127.0.0.1:7878', changeOrigin: true },
    '/auth': { target: 'http://127.0.0.1:7878', changeOrigin: true },
    '/http': { target: 'http://127.0.0.1:7878', changeOrigin: true },
  },
}
```

The session cookie flows because the backend (seeing plain http from the proxy)
does not set `Secure`, and `SameSite=Lax` is fine first-party to the Vite origin.

**One caveat:** the OIDC `/auth/callback` redirect is absolute to `baseURL`
(`:7878`), so SSO login does **not** transparently round-trip through `:5173`.
Local-password login is the dev path; SSO is tested against the real server.
Everything else proxies cleanly.

### 3. Build

`build/build.sh` orchestrates: build the frontend, then `go build` embeds it.

```sh
build_web() {
  command -v npm >/dev/null || { echo "npm is required to build the web UI" >&2; exit 1; }
  ( cd web && npm ci && npm run build )   # one vite build → server/internal/ui/dist
}
```

`build_web` runs before `build_one` in the `host` and `all` targets. `clean`
also clears `dist/`.

## The Embed (kept simple)

- `dist/` is fully gitignored:

  ```gitignore
  /server/internal/ui/dist/
  ```

- Embed with `//go:embed dist`. On a fresh checkout (no frontend build yet) the
  directory is absent and `go build` fails with "no matching files" — that is the
  intended signal to run `build/build.sh`. No sentinel, no `all:` prefix, no
  runtime "not built" page.

`build/build.sh` is the supported build path (already true per the README); it
always builds the frontend before `go build`, so the embed directory exists.

## Repo Layout

Folder-per-app layout, adapted from the reference project:

```
web/
  package.json
  vite.config.ts             # root: src/apps; dev hosts both apps; single multi-entry build
  tsconfig.json
  src/
    apps/
      dashboard/
        index.html           # stub: <div id="app"> + module script → shared bootstrap
        main.ts
        router.ts            # hash-mode routes for the dashboard views
        pages/               # Teams, Directories, Tasks, Connectors, Activity, Info
      admin/
        index.html
        main.ts
        router.ts            # Users, Single sign-on, Doctrines
        pages/
    shared/
      bootstrap.ts           # createMemdApp(BaseApp, routes): installs router, theme, globals, mounts #app
      api.ts                 # typed fetch client against /api/* (replaces the api() helper)
      session.ts             # useSession composable
      theme.ts               # useTheme composable (dark mode, layout width)
      bus.ts                 # tiny mitt event bus (toasts, full-page loader)
      utils.ts               # isEmpty / isEqual / formatting helpers
      components/            # MButton, MField, MIcon, MToast, MLoader … (wrap existing CSS)
      css/                   # style.css ported: design tokens + base, then scoped per component
  public/                    # favicon, logos, icons/*.svg → emitted as static assets

server/internal/ui/
  ui.go                      # //go:embed dist  (was assets/*)
  dist/                      # Vite output — gitignored, created by build.sh
    dashboard/index.html
    admin/index.html
    assets/*                 # content-hashed JS/CSS + static (img, fonts, icons)
```

Vite `base: '/'` for both apps; the shared `dist/assets/` holds content-hashed
bundles for both. The current `assets/` contents (favicon, logos, icons, css)
migrate into the Vue project: static files into `public/`, `style.css` imported
and split into tokens + per-component scoped styles over time.

## Patterns Borrowed From The Reference Project

Reviewed a Vue 3 + TS multi-app reference (Ulaa/Puvi: `vite.config.ts`,
`build.py`, `router.ts`, app `index.html`, `BaseApp.vue`, `BaseSetup.js`,
`DevIndex.vue`, `i18n.ts`, and a page component + scoped CSS). Adopt / adapt /
reject:

**Adopt**

- `src/apps/<app>/index.html` folder-per-app layout (with Vite `root: src/apps`),
  which naturally yields `dist/<app>/index.html`.
- Tiny per-app `index.html` stub deferring to a **shared bootstrap factory**
  (the reference's `BaseSetup.js` / `InitializeApp(routes)` → one `createApp`
  wiring), so the two apps don't duplicate app setup.
- A small **event bus** for ephemeral global signals (toasts, full-page loader,
  as in `BaseApp.vue`), pairing with composables for persistent state — fits the
  "no Pinia" choice.
- `@` → `src` alias, `<script setup lang="ts">`, co-located scoped CSS per
  component, and a centralized typed `shared/api.ts` + `shared/utils.ts`.

**Adapt**

- Proxy: keep `changeOrigin: true`, but plain `http://127.0.0.1:7878` with **no
  TLS and no manual `Access-Control-*` headers** (those exist in the reference
  only because its dev origin differs from its backend; memd is same-origin via
  the proxy).
- Build orchestration in `build.sh` (bash), not a separate Python `build.py`.

**Optional (not the default)**

- `VITE_APP`-selected **per-app build loop** for fully isolated bundles (no shared
  chunk graph between dashboard and admin). Adds a second build pass and
  `--emptyOutDir` ordering; only worth it if we later want the admin bundle to
  share nothing with the dashboard. Route-level code-splitting in the single
  build already prevents the dashboard from loading admin view code.
- A dev-only **`DevIndex`** landing page listing the apps (reference uses a
  `__APP_DIRECTORIES__` define). Marginal for two apps; cheap if wanted.

**Reject (reference-specific, not memd's context)**

- HTTPS dev server with real corp certs and a hardcoded
  `ulaa.csez.zohocorpin.com:8443` host.
- `createWebHistory` (history mode) — keep **hash mode** for back-compat with
  memd's `#view=` URLs and to avoid a server SPA-fallback catch-all.
- `vue-i18n` (memd is English-only), `pinia` and `axios` (composables + the fetch
  wrapper are enough), and SEO/OpenGraph/Twitter meta (memd is an auth-gated
  local tool).

## Migration Phases

**Phase 0 — Pipeline + boundaries.** Stand up `web/` (Vite + Vue 3 + TS + Router),
the shared bootstrap, the dev proxy, the single multi-entry build into `dist/`,
the `go:embed dist` switch, the `.gitignore` entry, and the `build.sh`
`build_web` step. Prove the full chain (dev proxy → build → embed → serve) with
placeholder pages before porting real views.

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

- `server/internal/ui/ui.go`: change the embed directive to `//go:embed dist`;
  point `index()` / `adminIndex()` at `dist/dashboard/index.html` /
  `dist/admin/index.html`; serve `/assets/` from `fs.Sub(embedFS, "dist")`; relax
  cache headers for the hashed assets.
- No changes to `/api/*`, auth, session, OIDC, MCP, or HTTP-connector handlers.

## Open Items / Risks

- **CI.** Needs a Node step before the Go build (`build.sh` already sequences
  this); cache `node_modules`. Optionally lint/type-check the frontend.
- **SSO in dev.** Not proxyable due to the absolute callback URL; documented as a
  known limitation (use password login locally).
- **Asset path stability.** Existing deep links to `/assets/...` (icons in
  external docs, if any) change to hashed names; keep stable public paths for
  anything externally referenced via Vite `public/`.
