# Structured Memory: Features & Tasks

Beyond freeform notes rooted at `MEMORY.md`, a memd directory can enable
**features** — file-first *kinds of structured memory* the agent keeps for you,
each living in its own folder with its own doctrine and (for built-ins) a rich
dashboard. **Tasks** is the first built-in. **Calendar** is registered but
coming soon.

The guiding idea: a feature is **portable, user-owned, structured memory**, not a
SaaS integration. A `tasks/` folder of Markdown checklists is the agent
*remembering* what you need to do — as plain files you own, identical across every
MCP client — not a to-do SaaS you log into.

## The model: a feature is a self-describing folder

A feature is just another folder in a directory's root (e.g. `tasks/`), marked by
a `_feature.md` doctrine file:

```
<directory>/
  MEMORY.md            # the default "memory" feature
  memory/              # detail files
  tasks/               # the "tasks" feature
    _feature.md        # your preferences (layered on top of the built-in rules)
    inbox.md           # loose tasks
    home-renovation.md # a named list
    paint-bedroom.md   # a task that outgrew its line → its own file
```

- **Enablement is DB-backed and toggled in the UI**, independent of folder
  presence. Disabling a feature keeps the folder and its files — **disable is not
  delete** — it just stops surfacing the feature to agents and hides its
  dashboard. The DB is the source of truth for "is this on"; the folder is the
  source of truth for the data.
- **Enabling scaffolds `tasks/_feature.md`** as a short *preferences template*
  (not a copy of the built-in rules). For git directories the new folder is
  pushed to the directory branch and propagated to connector branches right away.
- **Doctrine is two layers for built-ins:** a stable **base doctrine shipped in
  the server** (how the feature works, the grammar) plus the per-folder
  `_feature.md` **preference overlay** you (or the agent, self-improving) edit —
  e.g. *"always schedule tasks an hour earlier than the real deadline,"* *"tag
  anything work-related with #work."* At `memory_load` memd composes base +
  overlay for each enabled feature.

### Two tiers

| | **Built-in features** (tasks, calendar) | **User-created features** (later) |
|---|---|---|
| Doctrine | shipped in the server + a `_feature.md` overlay | a prose `_feature.md` is the whole doctrine |
| Parsing | memd hardcodes the grammar | none |
| Dashboard | rich custom UI (board, cards, checkboxes) | plain file browser |

Because only the built-ins are parsed (in code, not from the editable file), an
agent refining the prose doctrine can never break the dashboard.

## What the agent sees

Each enabled feature reaches the agent through `memory_load`, in a single
**Structured memory** section: the base doctrine is rendered once (with an
"Enabled in: …" list of directories), and each directory carries its own derived
state plus its `_feature.md` preferences. For tasks the derived state is a live
summary — `N open · N done · N overdue · N due soon` plus the overdue / due-soon
lines — recomputed from the files on every load using the same grammar/board the
dashboard uses (no second parser, no agent-maintained index to trust). Scan reads
never bump managed stats or trigger a git commit.

## Tasks

### The grammar (small, todo.txt-flavored)

```markdown
- [ ] Paint the bedroom  due:2026-06-20 prio:high #home
    - [ ] buy paint
    - [ ] tape edges
    note: Asha wants matte, not gloss
```

- A task is a Markdown checklist line: `- [ ]` (open) / `- [x]` (done).
- Trailing tokens are structured fields: `due:YYYY-MM-DD`, `prio:high|med|low`,
  `#tag`.
- Indented `- [ ]` lines are subtasks.
- Any other indented line (e.g. `note:`) is free text — preserved verbatim, never
  dropped. This escape hatch keeps the format human.

Loose tasks live in `tasks/inbox.md`; group related tasks into named list files
(`tasks/home-renovation.md`). **Filenames are stable nouns** — they never encode
status, priority, or due date, so they don't churn as a task changes.

### Lifecycle: a task graduates

> Born as a **line** in a list → gains **indented detail** in place → **promoted
> to its own file** only when it outgrows the list, leaving the line as a link:
> `- [ ] [Paint the bedroom](paint-bedroom.md)`.

Promoted task files use YAML front matter (`status`, `due`, `prio`) — which lives
in the same managed front-matter block as memd's stats — with the notes in the
body.

### The board

Orientation comes from a derived **board**, not from filenames: open work grouped
by deadline (Overdue / Due this week / Later / No date) with per-list open/total
counts. The files are the single source of truth; the board is recomputed from
them on every read, never trusted blindly.

### Three writers, one file — the edit rule

The same file is read and written by the **agent**, the **human** (in a text
editor), and the **dashboard**. The rule that keeps them from clobbering each
other: **code edits by surgical line operations, never blind re-serialization.**
Checking a box in the UI rewrites *just that line* (`[ ]` → `[x]`); it does not
parse the whole file to structs and write it back, so notes, formatting, and
order survive. Parse-to-model is for display only. Each UI edit also carries the
exact line it last saw, so a stale board can never toggle the wrong task.

## The Tasks dashboard (web UI)

A top-level **Tasks** view (in the sidebar and mobile quick-switcher) shows every
tasks-enabled directory you can see, grouped by directory:

- **Board buckets** at the top of each group — Overdue, Due this week, Later, No
  date.
- **Per-list cards** with checkboxes, subtasks, and due / priority / tag chips.
  Checking a box does the surgical line edit and re-derives the board.
- **Add a task** to any list, and **New list** to create one.
- **Hide completed** drops done tasks/subtasks from the lists (display-only; the
  files keep them).
- A **directory filter** narrows the view to one directory; a directory card's
  **Tasks** button jumps straight here pre-filtered to it.
- **URL-persisted:** the view and filter live in the hash (`#tasks=all` /
  `#tasks=<directory-id>`), so reload and shareable deep-links work. Every other
  main view is hashed too (`#view=connectors`, …).

Write access follows the same rules as the rest of the directory: the owner and
write-role team members can edit; viewers see a read-only board.

## Super-admin doctrine editor

A super admin can live-edit the global doctrine and each feature's base doctrine
in `/admin` → **Doctrines**. Edits apply **in memory only** — they affect what
connected agents receive immediately but are not persisted, so a restart reverts
to the compiled defaults. This is for tuning wording against real agents, not for
durable configuration.

## Roadmap for features

- [x] Feature framework (DB-backed enablement, folder scaffold, doctrine overlay)
- [x] Tasks: grammar parser, derived board, surfaced in `memory_load`
- [x] Tasks dashboard: cross-directory board/lists, surgical edits, hide-completed,
      URL persistence
- [ ] Promoted-task files surfaced in the board alongside list lines
- [ ] Optional thin `tasks_*` tools (only if agents prove sloppy editing markdown)
- [ ] Calendar feature (its own design for recurrence / timezones / all-day)
- [ ] User-created features surfaced in the file browser
- [ ] Optional one-way external mirror/export (Google / Zoho) — never a dependency

See [`plans/2026-06-14-feature-folders-design.md`](plans/2026-06-14-feature-folders-design.md)
for the design decisions and
[`plans/2026-06-14-feature-folders-progress.md`](plans/2026-06-14-feature-folders-progress.md)
for build status.
