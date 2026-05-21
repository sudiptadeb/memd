# Connect Agents To memd

Use this protocol when connecting an AI tool to a memd memory repo.

## Core Rule

The Markdown memory directories are the source of truth.

Adapters only provide access.

## Local Agents

For local or IDE agents:

1. Clone the memory repo.
2. Tell the agent: `Use memd. Start by reading memd.md.`
3. Let the agent read and edit files directly.
4. Use Git commits and pull requests for review when needed.

## Web Agents

For hosted agents:

1. Use a remote MCP adapter when the agent supports remote MCP.
2. Use project knowledge or uploaded files when MCP is unavailable.
3. Paste relevant memd files when the agent has no connector or file access.

## MCP Adapter

An MCP adapter should map one connector URL to a configured memory source.

The source may be:

- a whole repo
- a subdirectory inside a repo
- a single memory directory

For write access, the adapter should write to a connector-specific branch or working copy and open pull requests back to the main branch.

## Connector URL Tokens

A unique connector URL is a token. Treat it like a password.

Minimum rules:

- generate long random tokens
- allow revocation and rotation
- redact tokens from logs
- scope tokens to the smallest useful memory source
- never expose Git credentials to agents

