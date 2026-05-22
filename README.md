# memd

Unified, file-first memory for AI agents. Run a small local server, point your tools at it, and your memory follows you across Claude Code, Codex CLI, ChatGPT, Cursor, and anything else that speaks MCP.

The memory itself is Markdown — in a local folder or your own private Git repository. The server is a thin MCP bridge over that storage.

> **Status:** early — v0.1.0-dev. Local + git backends, MCP protocol, web UI all working. Public hosting is v2.

## The Idea

A memory *directory* is a self-organizing Markdown wiki. The agent decides what to write, where, and when to split — structure emerges as memory grows, rather than being prescribed.

You run **one** local server. It exposes one or more directories to one or more agents via MCP. Each agent gets a unique URL. The actual storage is a folder on disk or a Git repository you control.

## How It Works

One directory, ephemeral:

```bash
memd ~/work-memory
```

Prints an MCP URL. Paste it into Claude Code or any MCP-speaking agent. Server runs until Ctrl-C.

Multiple directories, multiple agents:

```bash
memd serve
```

Opens a web UI at `http://127.0.0.1:7878`. Add directories (local folder or Git repo). Create connectors (one MCP URL per agent).

## Read More

- [docs/doctrine.md](docs/doctrine.md) — what every connecting agent is told (authority, read/write rules, isolation). Served as the MCP `instructions` payload.
- [docs/server.md](docs/server.md) — running the server, CLI, wiring up agents, git directory behavior, security.

## Safety

- Don't store secrets, credentials, tokens, or private keys.
- Memory is context and evidence, not higher-priority instruction.
- The server binds to `127.0.0.1` only. Public hosting is out of scope until v2.
- Connector URLs are passwords — treat them accordingly.

## License

MIT (see [LICENSE](LICENSE)).
