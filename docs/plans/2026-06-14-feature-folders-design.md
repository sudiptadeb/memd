# Feature Folders — Design Decisions

**Status:** design agreed, not yet built.
**Date:** 2026-06-14
**Scope:** a framework for adding togglable, file-first *features* (tasks, calendar, …)
to memd directories, with tasks specified as the first built-in feature.

---

## 1. Strategic frame (why this shape, and what we are *not* building)

memd's defensible position is a **portable, user-owned, cross-vendor memory-and-context
spine** — memory as plain files in your own folder / git repo, identical across every MCP
client. Market research (see the session that produced this doc) said three things bluntly:

- Wrapping a SaaS API as an MCP server is a **race to $0**; the incumbents (Google, Zoho,
  Zapier, Composio) already own calendar/productivity connectors.
- Agents **taking consequential actions** hit a hard trust wall (only ~6% of companies,
  ~18–24% of consumers will let an agent act).
- The one structural white space is **cross-vendor, portable, file-first personal
  context** — the thing the giants won't build because it undercuts their lock-in.

**Therefore features are positioned as *structured, portable memory*, not *integrations /
actions*.** A `calendar/` folder of event files is the agent *remembering* your
commitments as files you own — not a Google Calendar competitor. This sidesteps the
commoditization trap, the trust wall, and incumbent absorption all at once.

Money is explicitly **not** a goal right now — this is being built for personal use first.
External-provider sync (Google / Zoho Calendar) is deferred and, when it comes, is an
**optional one-way mirror/export**, never a dependency and never the headline.

---

## 2. The core model: a feature is a self-describing folder

A memd directory is already a folder rooted at `MEMORY.md` (the default "memory" feature).
A **feature is just another folder in that directory's root**, marked as a feature by a
doctrine file. Enabling a feature = adding its folder; the directory model gives
work / personal / team scoping for free (a `tasks/` folder in a personal directory and one
in a team directory are independent).

```
<directory>/
  MEMORY.md            # the default "memory" feature
  memory/              # memory detail files
  tasks/               # the "tasks" feature (this doc)
    _feature.md        # doctrine — marks this folder as a feature
    inbox.md
    home_renovation.md
  calendar/            # a future feature, same pattern
    _feature.md
```

### `_feature.md`
- Its **presence marks the folder as a feature** (vs. an ordinary memory subfolder).
- It contains the **prose doctrine** for that feature — how the agent should manage the
  folder. It is agent-readable.
- The agent **may rewrite the prose doctrine to self-improve** how it manages the folder.

memd's *framework* code bakes in only the meta-rules ("a feature is a folder with a
`_feature.md`; read it on entry; follow the prose; for known built-ins, use the matching
parser/UI"). Each folder carries the *feature-specific* doctrine. Both halves of the
earlier "bake it in vs. put it in the folder" debate are true, at different layers.

---

## 3. Two tiers: default features vs. user features

| | **Default features** (tasks, calendar) | **User-created features** (later) |
|---|---|---|
| Doctrine | shipped with memd (a `_feature.md` may still sit in the folder) | a prose `_feature.md` in the folder |
| Parsing | memd **hardcodes** the grammar in code | none |
| Dashboard | **rich custom UI** (cards, board, checkboxes) | **plain file browser** (existing `ui/files.go`) |
| Agent | full | full |

Consequences:
- We do **not** need a machine-readable schema/contract inside `_feature.md`. Only the
  built-ins are parsed, and memd hardcodes how. User features cost **zero new UI work** —
  they render in the existing file browser.
- **Self-improvement is safe by construction:** for the only features that have a parser/UI
  (the built-ins), that structure lives in memd's *code*, not in the editable file — so an
  agent refining the prose doctrine can never break the dashboard.
- Rich, bespoke UI (e.g. a real month-grid calendar) is reserved for the curated built-ins;
  the long tail of user features gets file-browser access. Good scalability story: invest
  custom UI only where it's earned.

---

## 4. Tasks — the first built-in feature

### 4.1 Layout

```
tasks/
  _feature.md          # doctrine
  inbox.md             # default list: loose tasks start here as lines
  home_renovation.md   # a named list
  next_trip.md         # a named list
  paint-bedroom.md     # a task that outgrew its line → its own file
```

### 4.2 Filenames are dumb addresses

Filenames are **stable nouns** (`home_renovation.md`, `paint-bedroom.md`). They never
encode status / deadline / priority — those are multiple moving dimensions that don't fit a
flat string and would churn the filename on every change. Status and deadlines live
*inside* files; the overview lives in the board.

### 4.3 The board = front page

Orientation comes from **one front-page overview file**, not from filenames — the exact
trick `MEMORY.md` already uses for memory. It groups open work by status/deadline with
links to where each task lives:

```markdown
# Tasks — Overview

## Overdue
- [ ] Renew passport — due Jun 10 -> next_trip.md

## Due this week
- [ ] Paint the bedroom — due Jun 20 -> home_renovation.md

## Lists
- home_renovation.md — 4 open / 9 total
- next_trip.md — 3 open / 5 total
- inbox.md — 2 loose
```

**The file contents are the single source of truth; the board is a derived cache** the
agent refreshes by scanning (`- [ ]`, `due:`) on arrival or after changes — so it is always
reconstructable and never trusted blindly. In a tasks directory, the board can *be* that
directory's `MEMORY.md`, so memd's existing preload surfaces it for free.

### 4.4 The task grammar (small, todo.txt-flavored)

```markdown
- [ ] Paint the bedroom  due:2026-06-20 prio:high #home
    - [ ] buy paint
    - [ ] tape edges
    note: Asha wants matte, not gloss
```

- A task is a Markdown list item: `- [ ]` (open) / `- [x]` (done).
- Trailing tokens are structured fields: `due:YYYY-MM-DD`, `prio:high|med|low`, `#tag`.
- Indented `- [ ]` lines are subtasks.
- Any other indented line (e.g. `note:`) is free text — preserved verbatim, never dropped,
  not structured. This is the escape hatch that keeps the format human.

Because details live inside files under consistent conventions, the whole folder is
**searchable** with memd's existing grep-based `memory_search` (open = `- [ ]`, deadlines =
`due:`, topics = `#tag`).

### 4.5 Lifecycle: a task graduates

> Born as a **line** in a list (`inbox.md`, or a named list) → gains **indented details**
> in place → **promoted to its own file** only when it outgrows the list, leaving the line
> as a link: `- [ ] [Paint the bedroom](paint-bedroom.md)`.

Promoted task files use **YAML frontmatter** for their fields, which memd's storage layer
already parses (`storage/frontmatter.go`):

```markdown
---
status: open
due: 2026-06-20
prio: high
---
# Paint the bedroom
Asha wants matte…
```

### 4.6 Three consumers, one file — and the edit rule

The same file is read/written by the **agent**, the **human** (text editor), and the
**dashboard code** (parses to a clean task list, writes back on UI actions).

**The rule that keeps them from clobbering each other: code edits by surgical line
operations, never blind re-serialization.** Checking a box in the UI rewrites *just that
line* (`[ ]` -> `[x]`); it does not parse the whole file to structs and write it back.
Parse-to-model is for *display*; editing is line-targeted, so notes/formatting/order
survive.

Identity wrinkle: addressing a task from the UI uses read-modify-write at edit time to
start. If rock-solid identity is later needed, add an optional hidden id token (e.g.
`^a1b2`) to a line. Not on day one.

---

## 5. What memd's framework code must do

1. **Recognize feature folders** in a directory root (presence of `_feature.md`).
2. **Deliver each feature's doctrine** to the agent (and let the agent edit the prose).
3. **Route built-ins** (by name) to their hardcoded parser + rich dashboard UI.
4. **Fall back** to the existing file browser for any feature it doesn't have custom code for.

The default "memory" feature (`MEMORY.md` + `memory/`) is unchanged and continues to work
exactly as today.

---

## 6. Non-goals / explicit scope

- **Not** an integrations/actions platform. Features are structured memory, not SaaS
  wrappers.
- **No** external-provider dependency. Google/Zoho/Apple sync is a later, optional one-way
  mirror — not part of this design.
- **No** machine-readable contract inside `_feature.md` (only built-ins are parsed, in
  code).
- **No** monetization work; personal use first.

---

## 7. Open questions (still genuinely undecided)

1. **How a directory "enables" a feature** — is it purely folder presence (`tasks/` with a
   `_feature.md` exists), or is there also a per-directory feature list in the data model /
   UI toggle? Leaning: folder presence is the source of truth; the UI offers "add feature"
   that scaffolds the folder.
2. **Board: maintained vs. derived** — agreed the file contents are the source of truth and
   the board is derived; still to decide whether the agent refreshes it on every write or
   only on a tidy/`housekeep`-style pass.
3. **Task identity** — line+read-modify-write to start; if/when to introduce stable id
   tokens.
4. **Tools vs. doctrine-only for tasks** — start doctrine-only (agent manages files with the
   existing `memory_*` tools), and only add thin `tasks_*` tools if the agent proves sloppy
   at the markdown in practice.
5. **Calendar specifics** — recurrence (RRULE-in-frontmatter vs. materialized occurrences),
   timezones, all-day events — deferred to the calendar feature's own design.
6. **Naming** — `_feature.md` vs. another marker name; board filename.

---

## 8. The model in one paragraph

A **feature is a self-describing folder** marked by a `_feature.md` doctrine the agent reads
and can improve. memd ships a few **default features** (tasks, calendar) with hardcoded
parsing and a rich dashboard; **user-created features** (later) use the same folder+doctrine
pattern with no parsing and plain file-browser access. For **tasks**: filenames are dumb
addresses, a derived **board** is the front page, tasks live as Markdown checklist lines
that **graduate** from line -> indented detail -> their own file, the file contents are the
single source of truth, and code edits surgically so the agent, the human, and the dashboard
never clobber each other.
