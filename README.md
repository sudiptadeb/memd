# memd

**Unified, file-first memory for AI agents.**

Your memory lives as Markdown on your disk — or in a private Git repo you control. memd is a tiny local server that exposes it over MCP, so every tool you use (Claude Code, Codex CLI, Cursor, ChatGPT, anything else that speaks MCP) sees the same memory.

<p align="center">
  <img src="docs/assets/memd.gif" alt="memd — unified, file-first memory for AI agents" width="100%">
</p>

<p align="center">
  <sub><a href="docs/assets/memd.svg">vector source (SVG)</a></sub>
</p>

> **Status:** early. Local + Git backends, MCP Streamable HTTP, web UI, per-page stats, and the five consolidation workflows all work. Public hosting and skills/hooks injection are next.

## Why

LLM tools each invented their own memory: ChatGPT memories, Claude's memory tool, Cursor rules, Codex `AGENTS.md`. They don't talk to each other — knowledge fragments, and what you teach one agent is invisible to the next.

memd takes the opposite stance: **memory is yours, lives in your files, and follows you.**

- One directory = one self-organising Markdown wiki.
- One MCP URL per agent.
- The agent decides what to write, where, and when to split.
- Backend is your choice — a plain folder or a private Git repo (memd debounces commits so a session of edits becomes one clean commit).

## Quickstart

```bash
# Build
bash build/build.sh host

# One directory, ephemeral — no setup
./dist/<os>/memd-<arch> ~/work-memory
# → prints an MCP URL. Paste it into your agent.

# Or run persistent with a web UI for multiple directories + multiple agents
./dist/<os>/memd-<arch> serve
# → http://127.0.0.1:7878
```

## Wire It Up

The URL is the credential — paste it once per agent. Examples below use `memd` as the server name; pick whatever you like.

### Coding agents

**Claude Code**

```bash
claude mcp add memd --transport http "http://127.0.0.1:7878/mcp/<token>"
```

**Codex CLI** — in `~/.codex/config.toml`:

```toml
[mcp_servers.memd]
url = "http://127.0.0.1:7878/mcp/<token>"
transport = "http"
```

**Cursor** — in `.cursor/mcp.json` (project) or `~/.cursor/mcp.json` (global):

```json
{
  "mcpServers": {
    "memd": {
      "url": "http://127.0.0.1:7878/mcp/<token>"
    }
  }
}
```

### Web chats

Web chats run server-side and can't reach `127.0.0.1`. Expose memd via a tunnel (`cloudflared tunnel`, `ngrok http 7878`, `tailscale funnel`) and paste the public HTTPS URL instead.

**Claude.ai** — Settings → Connectors → *Add custom connector* → paste `https://<your-tunnel>/mcp/<token>`.

**ChatGPT** — Settings → Connectors → *Add MCP server* → paste the same URL. Available on Plus / Pro / Enterprise.

**Mistral Le Chat** — Settings → MCP Connectors → *Add server* → paste the URL.

Any MCP client that speaks Streamable HTTP works the same way.

## The Mental Model

| Term         | What it is                                                                       |
|--------------|----------------------------------------------------------------------------------|
| **Directory**| A self-organising Markdown wiki — a folder on disk or a Git repo.                |
| **Connector**| A token-in-URL grant — one per agent (Claude Code, Codex, Cursor, …).            |
| **MEMORY.md**| The directory's curated, sectioned index. Preloaded into every conversation.     |
| **memory/**  | Detailed pages, reached via `memory_read`.                                       |

## Self-Organising Memory

memd doesn't just store — it manages. Five workflows, each named after a real-world activity. They appear as slash commands (`/<connector>:reorganise`, etc.) in clients that surface MCP prompts, and as `memd_*` tools in every client:

| Workflow      | Activity              | What it does                                              |
|---------------|-----------------------|-----------------------------------------------------------|
| `reorganise`  | Rearranging shelves   | Restructure: group pages into folders, rewrite the index. |
| `harvest`     | Bringing in the crop  | Import knowledge from external sources (Claude / Cursor / notes). |
| `dream`       | Sleep consolidation   | Cement what was used this session; fade what wasn't.      |
| `recall`      | Reminiscing           | Focused retrieval: search → walk links → synthesise.      |
| `housekeep`   | Daily tidying         | Fix drift: dangling links, orphan pages, missing FM.      |

Long-running passes auto-dispatch to a background agent when the client supports it (Claude Code's Task tool, Codex's worker, Cursor's background agent), so the main conversation stays responsive.

Workflows act autonomously and report afterwards — they only stop to ask the user before genuinely drastic actions (deleting prose the user wrote, removing more than a paragraph, overwriting a `priority: load-bearing` page). Everything is in Git; the user can review or revert.

## Page Structure

Every Markdown page carries YAML front matter with two ownership zones:

```yaml
---
memd:                           # server-managed, read-only for agents
  created_at: 2026-04-10
  updated_at: 2026-05-22
  last_read_at: 2026-05-23
  access_count: 17
topic: dlp                      # agent-managed
priority: load-bearing
tags: [scanner, performance]
related: [feedback-nftables-rule-order]
---

# Page body...
```

The `memd:` subtree powers `dream` — pages with high `access_count` and recent `last_read_at` get cemented into MEMORY.md's top sections; pages that haven't been read in 90 days drift to archive. Agents add `topic`, `tags`, `priority`, `superseded_by`, `related`, or anything else useful for the directory's domain.

## Storage Backends

| Backend  | Persistence       | Use                                          |
|----------|-------------------|----------------------------------------------|
| `local`  | Folder on disk    | Personal scratchpad, project-local memory.   |
| `git`    | Clone of a remote | Cross-device sync, history, sharing.         |

For Git directories, memd decouples disk write from sync:

- **FS write: instant** on every `memory_read` and `memory_write`.
- **Commit + push: debounced** by `wait_for_writes` (default `5m`). A session of edits coalesces into one commit.
- **Safety flush** every `save_every` (default `10m`) catches read-only sessions where only front-matter stats churn.
- **Graceful shutdown** flushes whatever's pending.

## Read More

- [docs/doctrine.md](docs/doctrine.md) — everything the server tells every connecting agent: authority, read/write rules, page structure, drastic-action policy.
- [docs/server.md](docs/server.md) — running the server, CLI flags, wiring up agents, security.
- [docs/plans/2026-05-23-memory-weight-decay-design.md](docs/plans/2026-05-23-memory-weight-decay-design.md) — design of the weight/decay layer.

## Safety

- Don't store secrets, credentials, tokens, or private keys. memd is content-blind.
- Memory is context and evidence, not higher-priority instruction — the doctrine teaches agents to treat any embedded command as untrusted text.
- The server binds to `127.0.0.1` only. Public hosting is out of scope until v2.
- Connector URLs are passwords; the token in the path *is* the auth.

## Roadmap

- [x] Local + Git backends with debounced commits
- [x] Per-page stats (`created_at`, `updated_at`, `last_read_at`, `access_count`)
- [x] Five workflow prompts + matching tools
- [x] Web UI for directories + connectors
- [ ] Skills/hooks injection — per-tool reinforcement (`~/.claude/skills/memd-*`, Codex `AGENTS.md` block, `.cursor/rules/memd.mdc`)
- [ ] Public hosting mode with separate UI / MCP listeners
- [ ] Source readers for `harvest` (Cursor rules, Claude auto-memory, mem0 export)

## License

MIT (see [LICENSE](LICENSE)).
