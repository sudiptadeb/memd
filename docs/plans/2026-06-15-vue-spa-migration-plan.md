# Vue SPA Migration Plan

Date: 2026-06-15

Status: design agreed; implementation not started.

## Scope

Replace the embedded single-page Alpine.js UI with Vue 3 apps — the user
**dashboard** (served at `/`) and the super-admin **console** (served at
`/admin`) — without changing the backend API/auth contract. The migration must:

1. **Preserve the existing embed.** The release binary keeps serving the UI from
   `go:embed`; no Node runtime in production, single binary, same routes.
2. **Add local UI development.** Run a Vite dev server with HMR and reach the real
   backend API through a dev proxy, so the UI can be developed against live data
   without rebuilding the Go binary.
3. **Draw clean boundaries** between the three runtime modes (production embed /
   local dev / build) and between the apps, with `build/build.sh` as the one
   correct build path.

This is almost entirely a frontend-tooling-and-embed change. The backend stays
as-is apart from a small change to *where* the embedded assets come from.

## Design Principles

These are the load-bearing decisions; everything below follows from them.

1. **One HTML page = one app = independently buildable and embeddable.** Each
   distinct page is its own app with its own build, its own self-contained output,
   and its own base path. **Multi-entry builds are rejected**: they force the apps
   to be hosted together and embedded as a single unit, which removes the option
   to serve, embed, or omit one app independently later. Self-contained per-app
   builds keep that option open.
2. **Shared behavior comes from a common app template + a tiny event bus, not
   from a coupled build.** A shared bootstrap (`createMemdApp`) wires every app
   the same way, and a small event bus carries ephemeral cross-cutting signals
   (toasts, full-page loader). These are deliberate *seams*: a single place to
   reach through and migrate or upgrade every app at once down the line. Shared
   code is duplicated into each app's bundle — the accepted, cheap cost of
   independence.
3. **`build/build.sh` is the one build path.** `dist/` is gitignored; a bare
   `go build` without a prior frontend build is allowed to fail. No sentinels, no
   runtime fallbacks.
4. **The API/auth seam is unchanged.** Apps consume the same same-origin
   `/api/*` + session-cookie contract the Alpine UI uses today.

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

1. **Separate per-app builds, one project.** A single `web/` project holds every
   app under `src/apps/<app>/index.html` (`dashboard`, `admin`), but each app is
   **built independently** (`VITE_APP=<app> vite build`) into its own
   self-contained `dist/<app>/` with its own base path. No shared chunk graph;
   each app can be embedded or served on its own. The dev server hosts all apps
   from one process for convenience.
2. **Build-only embed; `build.sh` is the one path.** `dist/` is gitignored.
   `build/build.sh` builds each app, then `go build` embeds the result. A bare
   `go build` without a frontend build fails to compile — acceptable and
   expected.
3. **TypeScript** — typed components and api client; catches drift against the Go
   JSON contract at build time.
4. **Vue 3 + Vue Router (hash mode), no Pinia** — Router preserves the existing
   `#view=` URLs; shared state lives in composables (`useSession`, `useTheme`)
   plus the event bus. Pinia can be added later if cross-view state grows.

## Target Architecture: Three Runtime Boundaries

### 1. Production (embedded) — unchanged contract

`go build` embeds the built apps and serves them. No Node at runtime, single
binary, same routes. Each app is self-contained under `dist/<app>/` with its own
base path so its asset URLs never collide with another app's:

- Dashboard: `base: '/'` → `dist/dashboard/index.html`, assets at `dist/dashboard/assets/`.
  Go serves `/` → that HTML and `/assets/...` → that asset tree.
- Admin: `base: '/admin/'` → `dist/admin/index.html`, assets at `dist/admin/assets/`.
  Go serves `/admin` → that HTML and `/admin/assets/...` → that asset tree.

Because each app is self-contained, it stays independently embeddable — a future
build could embed only the dashboard, or serve the admin console from a separate
binary/process, with no code change to the other. Vite content-hashes filenames,
so caching is correct by construction: long-cache the hashed assets, no-cache
only the HTML documents.

### 2. Local UI development (the new capability)

Two terminals:

```bash
# Terminal 1: the real backend + DB on :7878
./dist/<os>/memd-... serve --init-db

# Terminal 2: Vite dev server with HMR on :5173, hosting the apps, proxying to :7878
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

### 3. Build

`build/build.sh` orchestrates: build each app independently, then `go build`
embeds them.

```sh
build_web() {
  command -v npm >/dev/null || { echo "npm is required to build the web UI" >&2; exit 1; }
  ( cd web && npm ci
    for app in dashboard admin; do
      VITE_APP="$app" npm run build   # → server/internal/ui/dist/<app>, self-contained
    done )
}
```

Each app owns its own `dist/<app>/`, so `emptyOutDir: true` per app is safe with
no cross-app ordering hacks. `build_web` runs before `build_one` in the `host`
and `all` targets; `clean` clears `dist/`.

## The Embed (kept simple)

- `dist/` is fully gitignored: `/server/internal/ui/dist/`.
- Embed with `//go:embed dist`. On a fresh checkout (no frontend build) the
  directory is absent and `go build` fails with "no matching files" — the
  intended signal to run `build/build.sh`. No sentinel, no `all:`, no runtime
  fallback page.

(If independent embedding of a single app is ever wanted, switch to per-app
directives like `//go:embed dist/dashboard` in separate files/build tags; the
self-contained layout already supports it.)

## Repo Layout

```
web/
  package.json
  vite.config.ts             # root: src/apps; per-VITE_APP base + outDir; dev hosts all apps
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
      bootstrap.ts           # createMemdApp(RootComponent, routes): installs router, theme, globals, mounts #app
      bus.ts                 # tiny mitt event bus (toasts, full-page loader) — the upgrade seam
      api.ts                 # typed fetch client against /api/* (replaces the api() helper)
      session.ts             # useSession composable
      theme.ts               # useTheme composable (dark mode, layout width)
      utils.ts               # isEmpty / isEqual / formatting helpers
      components/            # MButton, MField, MIcon, MToast, MLoader … (wrap existing CSS)
      css/                   # style.css ported: design tokens + base, then scoped per component
  public/                    # favicon, logos, icons/*.svg → emitted as static assets

server/internal/ui/
  ui.go                      # //go:embed dist  (was assets/*)
  dist/                      # Vite output — gitignored, created by build.sh
    dashboard/{index.html, assets/*}   # self-contained, base '/'
    admin/{index.html, assets/*}       # self-contained, base '/admin/'
```

The current `assets/` contents (favicon, logos, icons, css) migrate into the Vue
project: static files into `public/`, `style.css` imported and split into tokens
+ per-component scoped styles over time.

## Patterns Borrowed From The Reference Project

Reviewed a Vue 3 + TS multi-app reference (Ulaa/Puvi: `vite.config.ts`,
`build.py`, `router.ts`, app `index.html`, `BaseApp.vue`, `BaseSetup.js`,
`DevIndex.vue`, `i18n.ts`, a page component + scoped CSS). The reference is a
corp product; these are the distilled patterns, minified of its specifics.

**Adopt**

- `src/apps/<app>/index.html` folder-per-app layout, with **`VITE_APP`-selected
  per-app builds** so each app is self-contained and independently embeddable
  (the core principle above).
- Tiny per-app `index.html` stub deferring to a **shared bootstrap template**
  (the reference's `BaseSetup.js` / `InitializeApp(routes)` → one `createApp`
  wiring) — the seam for upgrading all apps at once.
- A small **event bus** for ephemeral global signals (toasts, full-page loader,
  as in `BaseApp.vue`), pairing with composables for persistent state — fits the
  "no Pinia" choice and is a second upgrade seam.
- `@` → `src` alias, `<script setup lang="ts">`, co-located scoped CSS per
  component, and a centralized typed `shared/api.ts` + `shared/utils.ts`.

**Adapt**

- Proxy: keep `changeOrigin: true`, but plain `http://127.0.0.1:7878` with **no
  TLS and no manual `Access-Control-*` headers** (those exist in the reference
  only because its dev origin differs from its backend; memd is same-origin via
  the proxy).
- Build orchestration in `build.sh` (bash), not a separate Python `build.py`.
- Optional dev-only **`DevIndex`** landing page listing the apps (reference uses a
  `__APP_DIRECTORIES__` define). Marginal for two apps; cheap if wanted.

**Reject (reference-specific, or against the principles)**

- **Single multi-entry build** — couples hosting and embedding; the whole point
  is separate per-app builds.
- `createWebHistory` (history mode) — keep **hash mode** for back-compat with
  memd's `#view=` URLs and to avoid a server SPA-fallback catch-all.
- HTTPS dev server with real corp certs and a hardcoded corp host.
- `vue-i18n` (memd is English-only), `pinia` and `axios` (composables + the fetch
  wrapper are enough), and SEO/OpenGraph/Twitter meta (memd is an auth-gated
  local tool).

## Migration Phases

**Phase 0 — Pipeline + boundaries.** Stand up `web/` (Vite + Vue 3 + TS + Router),
the shared bootstrap + bus, the dev proxy, per-app `VITE_APP` builds into
`dist/<app>/`, the `go:embed dist` switch, the `.gitignore` entry, and the
`build.sh` `build_web` step. Prove the full chain (dev proxy → per-app build →
embed → serve) with placeholder pages before porting real views.

**Phase 1 — Admin console pilot (`/admin`).** Port the super-admin console first:
small (~295 HTML / ~437 JS lines), self-contained, security-sensitive enough to
deserve a clean rebuild. It exercises login, session, theme, the api client, and
the embed end-to-end at low risk. Ship it from Vue while the dashboard is still
Alpine (independent apps, so they can diverge in framework mid-migration).

**Phase 2 — Dashboard (`/`).** Port view-by-view behind Vue Router, one existing
hash view at a time (Teams, Directories, Tasks, Connectors, Activity, Info),
reusing `style.css` initially. Keep the `#view=` / `#tasks=<dir>` URL scheme.

**Phase 3 — Cleanup.** Remove `alpine.min.js`, the old `index.html` / `admin.html`
/ `script.js` / `admin.js`, refactor shared CSS into components, and tighten
caching headers now that assets are content-hashed.

## Go Server Changes (minimal)

- `server/internal/ui/ui.go`: change the embed directive to `//go:embed dist`;
  point `index()` / `adminIndex()` at `dist/dashboard/index.html` /
  `dist/admin/index.html`; serve `/assets/` from the dashboard tree and
  `/admin/assets/` from the admin tree (each via `fs.Sub`); relax cache headers
  for the hashed assets.
- No changes to `/api/*`, auth, session, OIDC, MCP, or HTTP-connector handlers.

## Open Items / Risks

- **CI.** Needs a Node step before the Go build (`build.sh` already sequences
  this); cache `node_modules`. Optionally lint/type-check the frontend.
- **SSO in dev.** Not proxyable due to the absolute callback URL; documented as a
  known limitation (use password login locally).
- **Asset path stability.** Existing deep links to `/assets/...` change to hashed
  names; keep stable public paths for anything externally referenced via Vite
  `public/`.

## See Also

The reusable, project-agnostic version of this skeleton (one app per HTML page,
shared bootstrap template, event bus, dev proxy, `go:embed` build path) is
distilled into memd memory so future Vue apps can reuse it without re-deriving it
from this plan.
