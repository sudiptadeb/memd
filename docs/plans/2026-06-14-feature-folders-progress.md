# Feature Folders — Progress & Next Steps

**Companion to:** [`2026-06-14-feature-folders-design.md`](2026-06-14-feature-folders-design.md) (the design + decisions).
**This file:** what is built, what is verified, and what comes after Phase 2.
**As of:** 2026-06-14. Phases 1 + 2 are on `main`.

---

## Status at a glance

| Area | State |
|------|-------|
| Strategic validation (market research) | ✅ done — recorded in design doc §1 |
| Design decisions | ✅ locked — design doc §1–§8 |
| Phase 1: framework + tasks (doctrine-only) | ✅ built, tested, merged to `main` |
| UI: directory feature toggles | ✅ built (not browser-smoke-tested) |
| UI: super-admin live doctrine editor | ✅ built (not browser-smoke-tested) |
| Git: folder scaffold + branch propagation on enable | ✅ built + git test |
| Phase 2: grammar parser + derived board + dashboard task UI | ✅ built, tested, browser-verified |
| Phase 2: thin `tasks_*` tools | ⬜ deferred (doctrine-only still holds; add only if agent proves sloppy) |
| Calendar feature | ⬜ coming-soon stub only |
| User-defined (non-built-in) features | ⬜ not started |

---

## What shipped in Phase 1

A directory can enable/disable built-in **features** (file-first "structured memory"
modules). Tasks is the first, doctrine-only. Enablement is DB-backed and toggled in the UI,
independent of folder presence (disable ≠ delete). Each enabled feature's doctrine reaches
the agent through `memory_load`, framed as a kind of memory it can keep — composed as
**server base doctrine + the folder's `_feature.md` user-preference overlay**. A super admin
can live-edit any doctrine in memory (not persisted).

### File map (what to read to resume)

| File | Role |
|------|------|
| `server/internal/feature/feature.go` | `Feature` descriptor + `Registry` + `Builtins()` |
| `server/internal/feature/tasks.go` | tasks built-in: base doctrine + prefs template |
| `server/internal/feature/calendar.go` | calendar built-in (ComingSoon) |
| `server/internal/feature/doctrine.go` | `RegisterDoctrines` (seeds Live store) |
| `server/internal/doctrine/live.go` `ids.go` | in-memory editable doctrine store + id helpers |
| `server/internal/config/config.go` | `Directory.Features []DirectoryFeature` |
| `server/internal/account/schema.go` | schema v8 + `features` column |
| `server/internal/account/store.go` | `ensureUserDirectoryColumns` adds `features` |
| `server/internal/account/user_data.go` | features column upsert/scan (+ marshal helpers) |
| `server/internal/registry/registry.go` | `SetDirectoryFeatureForActor` (toggle, scaffold, git propagate) |
| `server/internal/mcp/mcp.go` | `featureMemorySection` + global doctrine from Live |
| `server/internal/ui/ui.go` | `featureToggles`, directory PATCH `feature` branch |
| `server/internal/ui/admin_doctrine.go` | `/api/admin/doctrines` endpoints |
| `server/internal/ui/assets/{index.html,script.js}` | directory Features toggle row |
| `server/internal/ui/assets/{admin.html,admin.js}` | Doctrines editor |

### Behavior details worth remembering
- **Enable** writes `<folder>/_feature.md` (a preferences template). The backend's `Write`
  does `MkdirAll`, so the folder is created on existing directories.
- **Git directories:** on enable we `Flush()` the main backend (push the folder to the
  directory branch), then flush each cached per-connector branch backend so they `syncBase`
  (merge main) and pick up the folder at once. Connectors that have never opened the
  directory fork fresh from main on first open, so they get it too.
- **Coming-soon features** (calendar) are listed but rejected on enable.
- **Doctrine override** is in-memory only; a restart reverts to the compiled default.

### Tests
`feature` registry; `doctrine.Live`; registry toggle (local: enable scaffolds, disable
preserves; unknown/coming-soon rejected); registry **git** propagation (folder lands on main
*and* a connector branch); mcp composition (enabled surfaces base+overlay, disabled hidden);
account v7→v8 migration. Full `go test ./server/...` + `go vet` green.

### NOT verified
- The two UI surfaces have **not** been clicked through in a browser (no live run in this
  environment). Smoke test: enable Tasks on a directory → confirm `tasks/_feature.md`
  appears → add a preference line → confirm it shows in an agent's `memory_load`; then
  `/admin` → Doctrines → edit/apply/reset.

---

## What shipped in Phase 2

The structured layer is in: a hardcoded tasks grammar, a board derived live from the files,
and a real dashboard task view. The files stay the single source of truth — parse is for
display, edits are surgical line ops, so an agent's or human's notes/formatting survive a
dashboard round-trip.

### File map (Phase 2 additions)

| File | Role |
|------|------|
| `server/internal/tasks/tasks.go` | grammar parser + board derivation + surgical line edits (`ParseFile`, `BuildList`, `BuildBoard`, `ToggleLine`, `AppendTask`) — no deps |
| `server/internal/tasks/tasks_test.go` | parser/toggle/board/append unit tests |
| `server/internal/ui/tasks.go` | `GET/POST /api/directories/<id>/tasks` — board read + toggle/add mutations, path-safety guard |
| `server/internal/ui/tasks_test.go` | endpoint tests (board, toggle, stale-guard, add-new-list, path-escape, disabled) |
| `server/internal/registry/registry.go` | `DirectoryViewForUser` now populates `CanWrite` so the UI can authorize edits |
| `server/internal/ui/ui.go` | dispatches the `tasks` sub-resource (reads **and** writes) ahead of the GET-only switch |
| `server/internal/ui/assets/{index.html,script.js,style.css}` | Tasks button + Tasks sheet (board buckets, per-list cards, checkboxes, subtasks, due/prio/tag chips, add task, new list) |
| `server/internal/ui/assets/icons/list-checks.svg` | Tasks button icon |

### How it works
- **Parser** (`ParseFile`): a checklist line `- [ ] title due:YYYY-MM-DD prio:high #tag`
  becomes a `Task`; indented `- [ ]` lines are subtasks; any other indented line (or a
  `note:` line) attaches to the task as a verbatim note; a leading `[text](file.md)` is
  parsed as a promotion link. Line numbers are 1-based over the file **as stored** (front
  matter included), so the UI can target the exact line.
- **Board** (`BuildBoard`): open top-level tasks bucketed Overdue / Due-this-week / Later /
  No-date by `due:`, plus per-list open/total counts. Recomputed on every GET — never a
  trusted stored index.
- **Edit safety:** `ToggleLine` flips only the box marker on one line and refuses if the
  client's `expect` (the raw line it last saw) no longer matches — a stale board cannot
  toggle the wrong task. `AppendTask` adds a line; both go through the backend's managed
  `Write` (which adds/keeps the `memd:` stats block exactly as for any memory file). The UI
  re-fetches after each mutation, so the front-matter the first write injects only shifts
  line numbers *between* fetches, which the `expect` guard absorbs.
- **Auth:** GET requires directory view access; POST requires `CanWrite` (owner or
  write-role team member). Files are constrained to `tasks/<name>.md` (no traversal, no
  nested folders, no `_` markers).

### Tests + verification
`go test ./server/...` + `go vet` green. Beyond unit/endpoint tests, the full stack was
**browser-verified** (headless Chromium): logged in as a non-admin user, enabled Tasks,
opened the dashboard (board buckets + list cards + subtasks + the italic note + chips all
render), clicked a checkbox → POST toggle → reload → row struck through and `- [x]`
persisted on disk; added a task and created a new list via the UI.

## What's next (beyond Phase 2)

1. **(Optional) thin `tasks_*` tools** — only if the agent proves sloppy editing markdown
   in real use; doctrine-only still holds for now.
2. **Promoted-task files** — surface single-task files (YAML front matter: status/due/prio)
   in the board alongside list lines; today only checklist lines in list files are parsed.
3. **Board-as-MEMORY.md / `_board.md`** — optionally let the agent persist the derived board
   so memd's existing preload surfaces it for free (still regenerated, never trusted blind).
4. **Calendar feature** (its own design for recurrence/timezones/all-day); user-defined
   features in the file browser; optional one-way external mirror (Google/Zoho) — later, not
   a dependency.

### Open questions carried forward (design doc §7)
Board refresh timing (now: derived live on read; persisting it is item 3 above); task
identity (line + `expect` guard today, optional `^id` token later); calendar file
conventions.

---

## How to resume
1. Read the design doc (decisions) then this file (state).
2. Phases 1 + 2 are on `main`. The tasks dashboard is live for any tasks-enabled directory.
3. Next natural step is promoted-task files (item 2) or the calendar feature.
