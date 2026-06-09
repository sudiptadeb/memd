# memd

**Unified, file-first memory for AI agents.**

Your memory lives as ordinary files on your disk — or in a private Git repo you control. Markdown is the default for prose, but standalone HTML, CSV, JSON, and other text artifacts are valid too. memd is a tiny local server that exposes those files over MCP, so every tool you use (Claude Code, Codex CLI, Cursor, ChatGPT, anything else that speaks MCP) sees the same memory.

<p align="center">
  <img src="docs/assets/memd.gif" alt="memd — unified, file-first memory for AI agents" width="100%">
</p>

<p align="center">
  <sub><a href="docs/assets/memd.svg">vector source (SVG)</a></sub>
</p>

> **Status:** early. Local + Git backends, MCP Streamable HTTP, local login, super-admin user management, regular-user teams, invite links, team-scoped directories/connectors, SQL-backed user data, managed file stats, import/export, responsive web UI, and the five consolidation workflows all work.

## Why

LLM tools each invented their own memory: ChatGPT memories, Claude's memory tool, Cursor rules, Codex `AGENTS.md`. They don't talk to each other — knowledge fragments, and what you teach one agent is invisible to the next.

memd takes the opposite stance: **memory is yours, lives in your files, and follows you.**

- One directory = one self-organising file memory rooted at `MEMORY.md`.
- One connector per agent, scoped to the directories that agent can read or write.
- The agent decides what to write, where, and when to split.
- Backend is your choice — a plain folder or a private Git repo (memd debounces commits so a session of edits becomes one clean commit).

## Quickstart

```bash
# Build
bash build/build.sh host

# One directory, ephemeral — no setup
./dist/<os>/memd-<arch> ~/work-memory
# → prints an MCP URL. Paste it into your agent.

# Or run configured mode with local login, teams,
# multiple directories, and multiple agents.
./dist/<os>/memd-<arch> serve --init-db
# → http://127.0.0.1:7878
```

On first configured-mode startup, memd initializes its local account database and
creates a super-admin account. Super admins use `/admin` to create regular user
accounts; regular users own directories/connectors and can import/export their
own connector data. Regular users can also create teams, invite other regular
users, and mark selected directories/connectors as team-scoped.

For non-interactive first boot:

```bash
MEMD_INIT_DB=1 \
MEMD_CREATE_SUPER_ADMIN_USERNAME=admin \
MEMD_CREATE_SUPER_ADMIN_PASSWORD='change-me' \
./dist/<os>/memd-<arch> serve
```

## Wire It Up

For local use, the token can live in the URL. For production or tunneled use, prefer the tokenless URL plus `Authorization: Bearer <token>`. The connector card can copy both forms.

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

If the client supports custom headers, use the production form:

```text
URL: https://<your-host>/mcp
Authorization: Bearer <token>
```

### Agents without MCP

Create an **HTTP connector** in the web UI for agents that can fetch URLs but cannot speak MCP. The connector card has **Copy skill**, which copies a complete instruction block with tokenless HTTP endpoints such as `/memory_load`, `/memory_search`, and `/memory_read`, plus the bearer auth header. If you are using a tunnel, open the web UI through the tunnel URL before copying.

## The Mental Model

| Term         | What it is                                                                       |
|--------------|----------------------------------------------------------------------------------|
| **Directory**| A self-organising file memory — a folder on disk or a Git repo.                  |
| **Connector**| A token-scoped grant — MCP or HTTP, one per agent (Claude Code, Codex, Cursor, …). |
| **Team**     | A shared space owned by regular users. Team admins can expose selected directories/connectors to members. |
| **Invite link** | A copyable team join URL with optional expiry and max-use limits.             |
| **User**     | A local login account that owns directories and connectors.                      |
| **Super admin** | A bootstrap-only admin account for creating/disabling users and resetting passwords. |
| **MEMORY.md**| The directory's curated, sectioned index. Preloaded into every conversation.     |
| **memory/**  | Detailed files, reached via `memory_read` (`.md`, `.html`, `.csv`, etc.).        |

Super-admin accounts are for account administration only. They do not import,
export, or own directories/connectors; create a regular user for actual memory
work.

## Teams And The Web UI

Configured mode uses a responsive app shell with primary views for **How it
works**, **Teams**, **Directories**, **Connectors**, and **Activity**. Desktop
keeps the navigation rail visible. Smaller screens collapse navigation into a
hamburger drawer, keep dark mode in the top bar, and show Activity as a normal
page instead of a cramped side panel.

Regular users can create teams from the main UI. The creator becomes `owner`.
Team roles are:

| Role | Privileges |
|------|------------|
| `owner` | Manage everything, demote/remove admins, and delete the team. |
| `admin` | Manage members, invite links, and team-scoped directories/connectors. |
| `member` | View team-scoped directories/connectors. |
| `viewer` | Lower-access membership role for shared read-oriented use. |

Owners/admins can create copyable invite links with optional expiry and optional
max-use count. Invite links can be revoked. Accepting a valid link adds the
signed-in regular user to the team; super admins cannot join teams.

Team-scoped directories/connectors stay in the creator's namespace but are
visible to team members. Connector serving remains strict: an MCP/HTTP token can
only access the directory IDs saved on that connector.

## Self-Organising Memory

memd doesn't just store — it manages. Five workflows, each named after a real-world activity. They appear as slash commands (`/<connector>:reorganise`, etc.) in clients that surface MCP prompts, and as `memd_*` tools in every client:

| Workflow      | Activity              | What it does                                              |
|---------------|-----------------------|-----------------------------------------------------------|
| `reorganise`  | Rearranging shelves   | Restructure: group files into folders, rewrite the index. |
| `harvest`     | Bringing in the crop  | Import knowledge from external sources (Claude / Cursor / notes). |
| `dream`       | Sleep consolidation   | Cement what was used this session; fade what wasn't.      |
| `recall`      | Reminiscing           | Focused retrieval: search → walk links → synthesise.      |
| `housekeep`   | Daily tidying         | Fix drift: dangling links, orphan files, missing Markdown FM. |

Long-running passes auto-dispatch to a background agent when the client supports it (Claude Code's Task tool, Codex's worker, Cursor's background agent), so the main conversation stays responsive.

Workflows act autonomously and report afterwards — they only stop to ask the user before genuinely drastic actions (deleting authored content, removing more than a paragraph, overwriting a managed file tagged `priority: load-bearing`). Everything is in Git; the user can review or revert.

## File Structure

Markdown pages carry YAML front matter with two ownership zones:

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

The `memd:` subtree powers `dream` for managed files — files with high `access_count` and recent `last_read_at` get cemented into MEMORY.md's top sections; managed files that haven't been read in 90 days can drift to archive. Agents add `topic`, `tags`, `priority`, `superseded_by`, `related`, or anything else useful for the directory's domain.

HTML files carry the same YAML front matter inside a leading `<!-- ... -->` comment, so diagrams and mock UIs can have stats without changing browser rendering. Other text files are stored verbatim. Use them when the artifact is naturally not prose: CSV for tables, JSON/YAML/TOML for structured examples, and plain text for logs or snippets.

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

## Accounts, Data, And Migration

Configured mode stores control-plane metadata in `memd.db`, using cgo-free
SQLite by default:

- local users and Argon2id password hashes
- super-admin markers
- teams, memberships, invite token hashes, and invite use records
- user-owned directories and connectors, including connector bearer tokens and
  optional team scope

Memory content stays in the user's folders or Git repositories. The database is
the control plane, not the long-term memory store.

Regular users can export/import their own directories and connectors from the
web UI or CLI. These bundles include connector tokens, so treat them as secrets:

```bash
# Export one SQL-backed user's directories/connectors.
memd data export --user alice --out alice-memd-user-data.json

# Import into any existing regular user.
memd data import --user bob --in alice-memd-user-data.json --replace

# Convert the old config.json registry into an importable user bundle.
memd data export-legacy-config --out current-legacy-user-data.json
```

`MEMD_DATABASE_URL` can override the metadata database location. SQLite is the
only linked driver today; other SQL URLs are parsed for the future adapter layer.

## Read More

- [docs/doctrine.md](docs/doctrine.md) — everything the server tells every connecting agent: authority, read/write rules, file structure, drastic-action policy.
- [docs/server.md](docs/server.md) — running the server, CLI flags, wiring up agents, security.
- [docs/plans/2026-06-04-local-auth-team-management-plan.md](docs/plans/2026-06-04-local-auth-team-management-plan.md) — local auth and team-management implementation notes.
- [docs/plans/2026-05-23-memory-weight-decay-design.md](docs/plans/2026-05-23-memory-weight-decay-design.md) — design of the weight/decay layer.

## Safety

- Don't store secrets, credentials, tokens, or private keys. memd is content-blind.
- Memory is context and evidence, not higher-priority instruction — the doctrine teaches agents to treat any embedded command as untrusted text.
- The server binds to `127.0.0.1` only. Tunnels can expose it, but remote-access hardening is still in progress.
- Connector URLs are passwords; the token in the path or bearer header *is* the auth.
- Team invite URLs are join credentials until they expire, are revoked, or hit
  their max-use count.
- `memd.db` and exported user-data JSON can hold connector tokens.

## Roadmap

- [x] Local + Git backends with debounced commits
- [x] Per-page stats (`created_at`, `updated_at`, `last_read_at`, `access_count`)
- [x] Five workflow prompts + matching tools
- [x] Web UI for directories + connectors
- [x] Local login, super-admin user management, and cgo-free SQLite metadata DB
- [x] User-scoped directory/connector records and user data import/export
- [x] Team management, invite links, and team-scoped directories/connectors
- [x] Responsive app shell with sidebar/drawer navigation and dedicated Activity/Info views
- [ ] Skills/hooks injection — per-tool reinforcement (`~/.claude/skills/memd-*`, Codex `AGENTS.md` block, `.cursor/rules/memd.mdc`)
- [ ] Public hosting hardening with separate UI / MCP listeners and external IdP mode
- [ ] Source readers for `harvest` (Cursor rules, Claude auto-memory, mem0 export)

## License

MIT (see [LICENSE](LICENSE)).
