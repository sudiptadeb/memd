# Feature Folders — Progress & Next Steps

**Companion to:** [`2026-06-14-feature-folders-design.md`](2026-06-14-feature-folders-design.md) (the design + decisions).
**This file:** what is built, what is verified, and the Phase 2 plan.
**As of:** 2026-06-14. **Branch:** `claude/connector-integration-framework-m35efg` (merged to `main`, fast-forward).

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
| Phase 2: tasks board + grammar parser + dashboard task UI | ⬜ not started |
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

## Phase 2 plan — make tasks real (parser + board + dashboard UI)

Phase 1 is doctrine-only: the agent manages task files with the existing `memory_*` tools.
Phase 2 adds the structured layer so the dashboard can render a clean task UI.

1. **Task grammar parser** (new, in `feature` or a `feature/tasks` subpackage)
   - Parse a list file into tasks: `- [ ] title due:YYYY-MM-DD prio:high #tag` + indented
     subtasks + free `note:` lines. Parse promoted task files via YAML front matter
     (reuse `storage/frontmatter.go`).
   - **Round-trip safety:** parse-to-model for display; **edit by surgical line ops**
     (toggle just the `[ ]`→`[x]`, rewrite one line's tokens) — never blind re-serialize.
   - Identity: file+line read-modify-write to start; optional `^id` token later.

2. **Board / overview** — derive a front-page summary (open by deadline/status, links) from
   the files; decide refresh trigger (on write vs. a tidy pass). Files are source of truth.

3. **Dashboard task UI** (`ui` + assets) — a real task view for tasks-enabled directories:
   list/board of cards with checkboxes, due chips, subtasks; check/edit calls a new
   tasks API that does the surgical line edit. This is the "clean interface" the design
   targets.

4. **(Optional) thin `tasks_*` tools** — only if the agent proves sloppy editing markdown
   in real use; Phase 1 deliberately defers them.

5. **Then:** calendar feature (its own design for recurrence/timezones/all-day);
   user-defined features surfaced in the existing file browser; optional one-way external
   mirror (Google/Zoho) — explicitly later and not a dependency.

### Open questions carried forward (design doc §7)
Board refresh timing; task identity (line vs `^id`); calendar file conventions; whether to
add `tasks_*` tools.

---

## How to resume
1. Read the design doc (decisions) then this file (state).
2. Branch `claude/connector-integration-framework-m35efg` == `main`.
3. Start Phase 2 at the **task grammar parser** — everything else (board, UI) builds on it.
