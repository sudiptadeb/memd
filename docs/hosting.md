# Hosting memd

The core memd system does not require hosting.

Hosting is only needed when a web agent needs remote access to memory.

## Recommended V1 Hosting Model

```text
private Git repo
  -> hosted MCP adapter
  -> connector URL per client
  -> branch/worktree per connector
  -> pull requests back to main
```

## Hosting Requirements

For the MCP adapter, prefer a host that supports:

- long-running HTTPS service
- environment variables for Git credentials
- persistent disk or volume
- background cron or scheduled jobs
- outbound GitHub access

## Good Fits

- A small VM.
- Railway/Fly-style app platform with volumes.
- Any server where Git can clone, fetch, branch, commit, and push.

## Weaker Fits

Serverless-only platforms can work for read-only adapters, but are awkward for Git worktrees and persistent local state.

If using serverless, consider using GitHub APIs directly instead of local Git checkouts.

