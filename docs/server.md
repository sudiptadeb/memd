# Running memd

memd is a Go server you run locally. It exposes Markdown memory directories to AI agents over MCP.

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

If the directory is empty, memd writes a starter `index.md` on first connect.

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
- Quick mode: rotates per run.
- Configured mode: persists per connector until you rotate or revoke it.

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
config.toml      # connectors, directories, descriptions
tokens           # connector tokens, mode 0600
workdirs/
  <id>/          # working copy for each Git-backed directory
```

## Git Directory Behavior

- **On startup:** clone if missing, pull from `main` (or the configured branch).
- **On read:** served from the working copy.
- **On write:** edit, `git add`, `git commit -m <message>`, `git push`.
- **On conflict:** the server stops writing to that directory and surfaces an error via `memory_status`. You resolve in the working copy. v1 has no automatic merge.
- Committer identity (name + email) comes from the directory's config.

Local-folder directories: just file I/O, no Git.

## Security Notes

- Localhost-bound. Anyone with a shell on your machine can read the web UI and the tokens file. Lock your laptop.
- Connector URLs are passwords. Don't paste them in shared logs, screenshots, or chats.
- The `tokens` file is mode `0600`. Be deliberate before syncing the config dir between machines.
- v1 has no admin auth and no remote-access story. Public hosting is out of scope until v2.
