# memd MCP Adapter

The MCP adapter is optional.

It lets hosted agents such as Claude web or ChatGPT access a memd memory repo through a remote MCP server. It is not the source of truth.

## Target Model

```text
unique connector URL
  -> configured memory source
  -> connector branch or working copy
  -> local writes served immediately
  -> periodic sync opens/updates pull request
```

## Connector Mapping

A connector URL maps to:

- repository URL
- base path
- connector branch
- read or write mode
- display name

Example:

```json
{
  "name": "Claude Web Default",
  "repo": "git@github.com:user/memory.git",
  "base_path": "directories/default",
  "branch": "memd/connector-claude-web-default",
  "mode": "read_write"
}
```

## Minimal Tools

The adapter should expose tools similar to:

- `memory_context`
- `memory_search`
- `memory_read`
- `memory_write`
- `memory_status`

## Git Behavior

For write-enabled connectors:

1. Keep a connector-specific branch or worktree.
2. Serve local connector changes immediately.
3. Pull from main regularly.
4. Push connector branch regularly.
5. Open or update a pull request into main.

Other connectors should not read another connector's unmerged branch by default.

