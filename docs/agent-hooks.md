# Hard guards for memory operations

The memd doctrine is context, not enforcement: the instruction payload shapes how
an agent uses memory, but it cannot guarantee any particular behavior. When a rule
must always hold — never delete, never overwrite load-bearing files without a
human in the loop — put it in a client-side hook or a read-only connector, not in
prose the agent is free to reinterpret.

## Claude Code: a PreToolUse hook

Claude Code runs a `PreToolUse` hook before each tool call. A `command` hook reads
the call on stdin and can return a permission decision, so you can require
approval for destructive memory tools and flag risky overwrites.

Add to `~/.claude/settings.json` (or the project `.claude/settings.json`):

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "mcp__memd__memory_delete|mcp__memd__memory_delete_folder|mcp__memd__memory_write",
        "hooks": [
          {
            "type": "command",
            "command": "$HOME/.claude/hooks/memd-guard.sh"
          }
        ]
      }
    ]
  }
}
```

The matcher is matched against the full MCP tool name (`mcp__<server>__<tool>`) as
a string or regex; the `|` form covers all three tools at once. The hook receives
the call as JSON on stdin (`tool_name`, `tool_input`, …) and returns a decision on
stdout. `memd-guard.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

input=$(cat)
tool=$(printf '%s' "$input" | jq -r '.tool_name')

ask() {
  jq -n --arg r "$1" '{
    hookSpecificOutput: {
      hookEventName: "PreToolUse",
      permissionDecision: "ask",
      permissionDecisionReason: $r
    }
  }'
  exit 0
}

case "$tool" in
  mcp__memd__memory_delete|mcp__memd__memory_delete_folder)
    ask "Destructive memd operation ($tool) — approve before deleting memory."
    ;;
  mcp__memd__memory_write)
    # Overwriting a load-bearing file deserves a human look.
    path=$(printf '%s' "$input" | jq -r '.tool_input.path // empty')
    dir=$(printf '%s' "$input" | jq -r '.tool_input.directory_id // empty')
    target="${MEMD_ROOT:-$HOME/memory}/$path"
    if [[ -n "$path" && -f "$target" ]] \
        && grep -qE '^priority:[[:space:]]*load-bearing' "$target"; then
      ask "memory_write would overwrite load-bearing file $dir/$path — confirm."
    fi
    ;;
esac

# Anything not flagged falls through to the normal permission flow.
exit 0
```

`permissionDecision: "ask"` prompts the user; `"deny"` rejects outright (pair it
with `permissionDecisionReason`); omitting the field lets the default flow run.
Make the script executable (`chmod +x`). The load-bearing check greps the target
file on disk; point `MEMD_ROOT` at the connector's directory if it is not under
`~/memory`. For Git-backed directories that path is the working copy under memd's
config dir.

## Read-only connectors: the zero-config guard

If an agent should never write at all, you do not need a hook. Create its
connector with write disabled (the per-connector write toggle). A read-only grant
cannot delete, overwrite, or be poisoned into trusted memory by a prompt
injection, and it is the right grant for shared or team reference directories the
agent only consults. See the Safety section in [README.md](../README.md).

## Other clients

The same tool names gate the same operations everywhere. In clients without
Claude Code's hooks, use their permission or approval config to require
confirmation for `memd` writes and deletes — Codex CLI and Cursor both let you
mark tools as ask-before-run. For agents that should not write, a read-only memd
connector is the portable answer.

## Read more

- [Claude Code hooks](https://code.claude.com/docs/en/hooks) — full reference for
  matchers, events, and the `hookSpecificOutput` decision shape.
