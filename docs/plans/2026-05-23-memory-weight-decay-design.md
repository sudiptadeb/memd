# memd — memory weight, decay, and consolidation

Date: 2026-05-23
Status: design (approved, implementation pending)

## Problem

memd today is a passive store. Pages are written when durable knowledge emerges, read on demand, and reorganised on count/age thresholds. There is no notion of:

- which memories are load-bearing vs. peripheral,
- which can decay,
- which deserve cementing.

The agent is meant to do all that synthesis manually under the ADD/UPDATE/DELETE/NONE doctrine. Discipline-only systems drift.

## Approach

Add a small server-managed metadata layer that lives in per-page front matter. Agents see the metadata (the new prompts need it to make judgement calls) but never write the reserved keys. Layer prompt passes on top of that substrate to do consolidation, forgetting, retrieval, maintenance, and external import.

## Schema

Every page (including `MEMORY.md`) carries YAML front matter with a server-managed `memd:` subtree:

```yaml
---
memd:
  created_at: 2026-04-10      # set once on first write
  updated_at: 2026-05-22      # bumped on memory_write that changes body
  last_read_at: 2026-05-23    # bumped on memory_read
  access_count: 17            # incremented on memory_read
topic: dlp
priority: load-bearing
tags: [scanner, performance]
related: [feedback-nftables-rule-order]
---

# page body...
```

Rules:

- The `memd:` subtree is **server-reserved**. The server writes it; the agent reads it. If an agent's `memory_write` payload contains a `memd:` block, the server discards it and writes its own authoritative one.
- Every other top-level key is **agent-managed**. The doctrine documents suggested keys (`topic`, `tags`, `priority`, `superseded_by`, `related`). Agents may add others; the server passes them through untouched.
- If a page has no front matter (legacy pages), the server adds one on first read or write, initialising `memd:` and leaving the body untouched.

## Access semantics

| Operation                | `created_at` | `updated_at` | `last_read_at` | `access_count` |
|-------------------------|-------------|-------------|----------------|----------------|
| `memory_read(path)`     | —           | —           | today          | +1             |
| `memory_write` new page | today       | today       | today          | 0              |
| `memory_write` existing | —           | today       | —              | —              |
| `memory_load`           | —           | —           | —              | —              |
| `memory_list`           | —           | —           | —              | —              |
| `memory_search`         | —           | —           | —              | —              |

A search hit is **not** an access. The agent only "reads" when it calls `memory_read(path)` on a hit.

Page rename is handled as `memory_write` to the new path (stats carry over from old path; old path is deleted). No `last_moved` field — `updated_at` covers that.

## Persistence

FS persistence and git sync are decoupled.

### File system: write-through

Every `memory_read` (FM stat bump) and `memory_write` (content + FM) writes to disk immediately. The server never holds dirty FM state in memory.

### Git: debounced + safety-flushed

Two configurable timers per directory (defaults shown):

- `wait_for_writes: 5m` — after a `memory_write`, wait this long for further writes. Any new write resets the timer. On expiry, `git add` dirty pages, commit, push.
- `save_every: 10m` — periodic safety net. If the working copy has any dirty files (e.g. read-only session where reads have bumped FM stats), commit them. Independent of `wait_for_writes`; whichever fires first wins.

Additional flush triggers:

- **Graceful shutdown** — flush pending commits immediately.
- **Prompt passes** (`reorganise`, `housekeep`, `dream`) — flush as the first step so the pass starts from a clean baseline.

Commit messages:

- Default: `memd: session checkpoint (N pages)`.
- If `memory_write` is called with an explicit `message`, that becomes the commit message for the commit that includes it.

## Prompt slate

Five user-invokable prompts, each named after a real-world activity:

| Prompt           | Activity                | What it does |
|------------------|-------------------------|--------------|
| `reorganise`     | Rearranging shelves     | Restructure existing memory: group root pages into folders, rewrite MEMORY.md as a curated sectioned index, bump `last_reorganised`. (Already exists.) |
| `harvest`        | Bringing in the crop    | Gather memory from external sources (Claude auto-memory, Cursor rules, raw notes, another memd directory). Per item: search existing pages, decide ADD/UPDATE/DELETE/NONE, integrate. |
| `dream`          | Sleep consolidation     | This-session pass: walk pages whose `last_read_at` is today, cement what was used (move into MEMORY.md top sections, link from other pages); walk pages whose `last_read_at` is old, propose archive or delete. Uses `memd:` stats. |
| `recall <topic>` | Reminiscing             | Focused retrieval: `memory_search`, walk linked pages, synthesise an answer rather than dumping raw hits. |
| `housekeep`      | Daily tidying           | Find structural drift: dangling links, MEMORY.md entries pointing to deleted files, missing FM, stale `last_reorganised`. Fix in place with approval. |

The earlier `forget` candidate is absorbed into `dream`.

## Doctrine updates

The doctrine served as MCP `instructions` needs:

1. New section documenting the `memd:` front matter subtree, the reserved/agent-managed split, and suggested agent keys.
2. Update the "MCP Tools You Have" section noting that `memory_read` mutates stats and `memory_write` preserves them.
3. Update "User-Invokable Prompts" to list `reorganise`, `harvest`, `dream`, `recall`, `housekeep`.
4. Update "Reorganisation" section to point at `dream`/`housekeep` for the focused passes; reorganise stays as the structural one.

## Implementation outline

Detailed plan is a follow-up. High-level changes:

- `server/internal/storage/frontmatter.go` (new) — parse/serialise YAML FM, model the `memd:` subtree, strip server keys from agent input.
- `server/internal/storage/local.go` + `git.go` — Read/Write call into the FM helpers; Read updates `last_read_at`/`access_count`; Write updates `updated_at` and preserves stats; first write sets `created_at`.
- `server/internal/storage/git.go` — add per-directory debounce + safety-flush mechanism. Decouple write-to-disk from commit/push.
- `server/internal/mcp/mcp.go` — register four new prompts (`harvest`, `dream`, `recall`, `housekeep`).
- `server/internal/doctrine/doctrine.md` (+ keep `docs/doctrine.md` mirror) — see above.
- Config — surface `wait_for_writes` and `save_every` on directories; defaults `5m` and `10m`.

No protocol changes. Tool surface stays the same (7 tools).

## Trade-offs accepted

- **Crash between debounce and flush** loses any uncommitted FM stats and any unsynchronised body changes. Mitigated by FS write-through (content is always durable; only the git mirror lags). Acceptable for a personal memory system.
- **Per-backend timing differs.** Local backend has no debounce — every write hits disk and that's it. Git backend coalesces. Data shape identical.
- **`harvest` is the most complex prompt** — it operates on external sources outside memd's storage abstraction. v1 accepts paste-in content via the prompt and lets the agent walk it; richer source-fetchers (mem0 / Cursor / Claude auto-memory readers) are follow-ups.
- **Stats are best-effort signal**, not durable knowledge. Lost stats don't lose memory.
