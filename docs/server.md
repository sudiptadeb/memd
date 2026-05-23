# Running memd

memd is a Go server you run locally. It exposes Markdown memory directories to AI agents over MCP.

## Current v1 Behavior

What works today:

- Local + Git backends. Git commits are debounced (one per session, not per write).
- Web UI for managing directories and connectors. Each connector gets a token-in-URL credential.
- MCP Streamable HTTP, the five workflows (`reorganise`, `harvest`, `dream`, `recall`, `housekeep`), per-page stats.
- Localhost-only binding. No admin auth, no remote-access story, no public hosting.

What's planned (not yet implemented): connector token rotation, skills/hooks injection, public hosting mode, source readers for `harvest`. See [README.md](../README.md) Roadmap.

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

If the directory is empty (no Markdown at the root), memd writes a starter `MEMORY.md` on first connect. memd never modifies a directory that already has Markdown at its root.

### Configured Mode — multiple directories, multiple agents

```bash
memd serve
```

The server starts, opens the web UI in your browser (default `http://127.0.0.1:7878`), and runs until you stop it.

In the UI:

1. **Add directories.** Pick local folder or Git repo. For Git: paste the URL, branch, base path inside the repo, and pick an SSH key path or PAT env var. memd clones into a working copy under the config dir.
2. **Add connectors.** One per agent (e.g. "Claude Code", "Codex CLI"). Pick which directories the connector can see and whether write is allowed. memd generates a unique MCP URL and shows it to you once.
3. **Wire up agents.** Paste each URL into the matching agent's MCP config — see below.

## CLI

```
memd <directory>             # quick mode
memd serve [--port PORT]     # configured mode + web UI
memd version
```

Both modes bind to `127.0.0.1`. There is no way to expose the server publicly in v1.

## MCP URL Shape

```
http://127.0.0.1:<port>/mcp/<token>
```

- Token is 32 random characters.
- The URL **is the credential**. Treat it like a password.
- Quick mode: a fresh token per run.
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

Any agent supporting MCP Streamable HTTP with a bearer-in-URL endpoint works the same way.

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
- **Shown once** in the web UI at creation time, embedded in the MCP URL. memd does not redisplay it later — paste it straight into your agent.
- **Revoked** by deleting the connector from the web UI (`DELETE /api/connectors/{id}`). The token is removed from `config.json` and any future request bearing it returns 401.
- **Rotation** is not implemented in v1. To rotate, delete the connector and create a new one, then update the agent's MCP config with the new URL.

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
