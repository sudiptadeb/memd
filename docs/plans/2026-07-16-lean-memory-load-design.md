# Lean `memory_load`: progressive disclosure for structured memory

**Date:** 2026-07-16 · **Status:** shipped in this change

## Problem

`memory_load` is the one payload every session pays for, and an audit of a real
load showed three kinds of avoidable weight:

1. **The full Tasks doctrine (~35 lines / ~2.3KB) inlined on every load** —
   grammar, promotion rules, board conventions — even in sessions that never
   touch tasks. The doctrine is procedure, not state; agents only need it when
   they actually create or edit tasks.
2. **Preferences shouted, not appended.** Each tasks-enabled directory
   re-printed its entire `tasks/_feature.md` under a `Preferences (...)` banner:
   managed `memd:` front matter (4 lines of server stats), the scaffold title,
   and — in the common case where the user never edited the file — the whole
   untouched template, per directory.
3. **Server-owned front matter in every preloaded `MEMORY.md`.** The `memd:`
   stats block is unwritable by the agent (`memory_write` discards it), so it
   was pure noise. Worse, the preload used `Read`, so every load bumped
   `last_read_at`/`access_count`, rewrote the file, and (on git backends)
   produced a commit — write amplification for a read-only operation.

For a two-directory setup this cost roughly 3.5–4KB (~1k tokens) per load,
plus another ~0.9KB of task grammar duplicated in the global doctrine sent to
every client at `initialize`.

## Industry research (2026-07)

How other systems split "always in context" vs "on demand":

- **Anthropic context engineering**: aim for "the smallest possible set of
  high-signal tokens"; keep lightweight identifiers in context and pull content
  just-in-time; adherence measurably drops as always-loaded text grows.
  (anthropic.com/engineering/effective-context-engineering-for-ai-agents)
- **Claude Skills / Cursor "Agent Requested" rules — the canonical pattern**:
  a name + one-line description is always loaded; the body is fetched only when
  the model decides it's relevant. (platform.claude.com Agent Skills docs;
  cursor.com/docs/context/rules)
- **Claude Code auto memory**: only `MEMORY.md` is preloaded, hard-capped at
  200 lines / 25KB (memd already mirrors this budget); topic files are read on
  demand; "if an entry is a multi-step procedure, move it to a skill."
  (code.claude.com/docs/en/memory)
- **Anthropic memory tool**: preloads *nothing* — a ~5-line behavioral protocol
  plus a directory listing on first call; content is always fetched.
- **Letta/MemGPT**: user preferences live in a small, always-pinned,
  size-capped core block (default 2,000 chars/block); everything else is
  archival and must be queried via tools. (docs.letta.com memory-blocks)
- **ChatGPT memory**: injects everything every message — but survives only by
  keeping entries to one-line facts, and hedges preference sections as
  "assumed preferences… use them to improve response quality", not commands.
- **mem0/Zep**: nothing standing in context; per-turn retrieval injected as a
  clearly labeled block; Zep additionally keeps the stable prefix stable for
  prompt-cache friendliness.

Cross-cutting: **index/summary always, body on demand; the always-loaded tier
is hard-capped and enforced mechanically; descriptions are the routing layer;
procedures live behind the trigger while short preferences may stay in the
core, hedged and labeled.**

## Design

1. **New tool `memory_feature_guide(feature, directory_id?)`** — the on-demand
   body. Returns the feature's base doctrine (live-editable via `/admin` →
   Doctrines as before), then per enabled directory: the live task state and
   the user's preference overlay layered on top ("base + overlay" composition
   now happens here, not in the preload). Also exposed as a GET endpoint on
   HTTP connectors. Guarded by the load-first nudge like other content tools.
2. **`memory_load` keeps only the trigger.** The *Structured memory* section is
   now a 2-line intro plus one bullet per enabled kind
   (`` `tasks` — things the user needs to do… Enabled in: … ``) with an
   instruction to call the guide before creating/editing that kind. The
   per-directory live state (`N open · N done · N overdue · N due soon` +
   flagged lines) stays — it's cheap, volatile, and high-signal.
3. **Preferences are appended quietly, only when real.** The `_feature.md`
   overlay is rendered without managed front matter; scaffold guidance now
   lives in an HTML comment so an untouched template distills to nothing (the
   legacy plain-text scaffold is recognised and suppressed too). When the user
   has written rules, they appear as bare bullets under a one-line hedged
   label: `Preferences (tasks/_feature.md — apply where relevant; the current
   request wins):` — the ChatGPT/Letta framing that counters over-application.
4. **Preload hygiene for `MEMORY.md`.** The preload now uses `ReadRaw` (a load
   is an automatic scan, not a deliberate access — no stat bump, no rewrite,
   no git commit per session) and strips the server-owned `memd:` block while
   keeping agent-authored front-matter keys (`last_reorganised`, `entries`, …).
5. **The global doctrine slims to match.** The task grammar inlined in the MCP
   `instructions` payload is replaced by a pointer to `memory_feature_guide`
   (~17 lines saved in every client's system prompt).

## Effect

For the audited two-directory setup: ~3.5KB (~1k tokens) less per
`memory_load`, ~0.9KB less doctrine per client initialize, no stat-churn
commits from loads, and — per the adherence research — a shorter, higher-signal
preload that the model is more likely to actually follow. Costs one extra tool
round-trip in sessions that genuinely work with tasks; the guide returns
everything needed in one call.

## Later (not in this change)

- Consider byte-capping the preference overlay (Letta caps core blocks at
  2,000 chars) with a "more in `_feature.md`" pointer on overflow.
- A "smaller still" degradation path when a connector sees many directories:
  preload only directory names + root `MEMORY.md`, pure memory-tool style.
- Have `housekeep` flag over-length preference overlays and doctrine drift.
