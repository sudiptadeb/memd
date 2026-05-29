# Running memd

memd is a Go server you run locally. It exposes file-based memory directories to AI agents over MCP. Markdown is the default for prose, but HTML, CSV, JSON, and other text files can be stored as memory artifacts too.

## Current v1 Behavior

What works today:

- Local + Git backends. Git commits are debounced (one per session, not per write).
- Web UI for managing directories and MCP/HTTP connectors. Each connector gets a bearer token credential; token-in-URL remains supported for local/legacy use.
- MCP Streamable HTTP, the five workflows (`reorganise`, `harvest`, `dream`, `recall`, `housekeep`), managed file stats for Markdown/HTML.
- Plain HTTP connector endpoints for agents that can fetch URLs but cannot speak MCP; the UI can copy a ready-to-paste skill/instruction block.
- Localhost-only binding. No admin auth, no remote-access story, no public hosting.

What's planned (not yet implemented): skills/hooks injection, public hosting mode, source readers for `harvest`. See [README.md](../README.md) Roadmap.

## Two Modes

### Quick Mode — one directory, no config

```bash
memd ~/work-memory
```

The server starts on a random localhost port and prints an MCP URL:

```
memd serving ~/work-memory

  http://127.0.0.1:48173/mcp/9f2c4a8e...

Press Ctrl-C to stop.
```

Each run gets a fresh URL. The server dies when you stop it; nothing persists outside the directory itself.

If the directory is empty or has no Markdown at the root, memd writes a starter `MEMORY.md` on first connect. Existing HTML/CSV/JSON/text files are left untouched.

### Configured Mode — multiple directories, multiple agents

```bash
memd serve
```

The server starts, opens the web UI in your browser (default `http://127.0.0.1:7878`), and runs until you stop it.

In the UI:

1. **Add directories.** Pick local folder or Git repo. For Git: paste the URL, branch, base path inside the repo, and pick an SSH key path or PAT env var. memd clones into a working copy under the config dir.
2. **Add connectors.** One per agent (e.g. "Claude Code", "Codex CLI"). Pick MCP or HTTP, which directories the connector can see, and whether write is allowed. memd generates a unique token and shows token-in-URL plus header-auth forms.
3. **Wire up agents.** Paste MCP URLs into MCP configs, use **Copy auth** when the client supports headers, or use **Copy skill** for HTTP connectors.

## CLI

```
memd <directory>             # quick mode
memd serve [--port PORT]     # configured mode + web UI
memd version
```

Both modes bind to `127.0.0.1`. There is no way to expose the server publicly in v1.

## Connector URL Shapes

Production/header-auth forms:

```
http://127.0.0.1:<port>/mcp
http://127.0.0.1:<port>/http/<endpoint>
Authorization: Bearer <token>
```

Local/legacy token-in-URL forms:

```
http://127.0.0.1:<port>/mcp/<token>
http://127.0.0.1:<port>/http/<token>
```

- Token is 32 random characters.
- The bearer token **is the credential**. Treat it like a password.
- MCP connectors use `/mcp`; HTTP connectors use `/http`.
- Header auth and token-in-URL auth both work. Prefer header auth for production so tokens do not appear in access logs, browser history, or screenshots.
- Quick mode: a fresh MCP token per run.
- Configured mode: persists per connector until you delete it. See [Connector Tokens](#connector-tokens) below.

## Wiring Up Agents

### Claude Code

```bash
claude mcp add --transport http memd "http://127.0.0.1:48173/mcp/9f2c4a..."
```

### Codex CLI

In `~/.codex/config.toml`:

```toml
[mcp_servers.memd]
url = "http://127.0.0.1:48173/mcp/9f2c4a..."
transport = "http"
```

Any agent supporting MCP Streamable HTTP with a token-in-URL endpoint works the same way.

When the client supports request headers, use the cleaner production form:

```text
URL: http://127.0.0.1:48173/mcp
Authorization: Bearer 9f2c4a...
```

### HTTP connector for non-MCP agents

Some cloud agents can fetch URLs but cannot connect to MCP. Create an HTTP connector in the web UI and use **Copy skill** on the connector card. The copied instructions include the tokenless URL base, `Authorization: Bearer <token>`, and endpoints for `memory_load`, `memory_list`, `memory_read`, `memory_search`, status, and workflows. Write-enabled HTTP connectors expose write operations through POST only. If you are using a tunnel, open the web UI through the tunnel URL before copying so the pasted skill uses the reachable host.

## Where Config Lives

| Platform | Path |
|----------|------|
| macOS | `~/Library/Application Support/memd/` |
| Linux | `~/.config/memd/` |
| Windows | `%APPDATA%\memd\` |

Layout inside that path:

```
config.json      # directories, connectors, tokens — mode 0600
workdirs/
  <id>/          # working copy for each Git-backed directory
```

## Connector Tokens

- **Stored** inside `config.json` on each connector's `token` field. The whole file is written atomically with mode `0600`.
- **Shown in the web UI** embedded in the local/legacy connector URL and in the bearer auth header copied by **Copy auth**. Treat it as secret and paste it only into the intended agent.
- **Revoked** by deleting the connector from the web UI (`DELETE /api/connectors/{id}`). The token is removed from `config.json` and any future request bearing it returns 404.
- **Rotated** with the "Rotate token" button in the web UI (`POST /api/connectors/{id}/rotate`). The connector keeps its ID, name, directory access, and write flag; only the token changes. The previous URL stops authenticating immediately — paste the new one into the agent.

## Git Directory Behavior

- **On startup:** clone if missing, pull from `main` (or the configured branch).
- **On read:** served from the working copy.
- **On write:** the file lands in the working copy immediately. The commit + push is **debounced** — a `wait_for_writes` timer (default `5m`) is armed, and any further write resets it. When the timer expires, memd runs `git add -A`, `git commit -m <message>`, `git push`. A session of edits becomes one clean commit.
- **Safety flush** every `save_every` (default `10m`) commits whatever's dirty, so read-only sessions that only churn front-matter stats still sync.
- **Graceful shutdown** flushes any pending commits before exit.
- **On conflict:** the server stops writing to that directory and surfaces an error via `memory_status`. You resolve in the working copy. v1 has no automatic merge.
- Committer identity (name + email) comes from the directory's config.

Local-folder directories: just file I/O, no Git, no debounce.

## Security Notes

- Localhost-bound. Anyone with a shell on your machine can read the web UI and `config.json`. Lock your laptop.
- Connector URLs are passwords. Don't paste them in shared logs, screenshots, or chats.
- `config.json` is mode `0600` and holds all connector tokens. Be deliberate before syncing the config dir between machines.
- v1 has no admin auth and no remote-access story. Public hosting is out of scope until v2.
