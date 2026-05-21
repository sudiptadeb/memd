# Update memd

Use this protocol after meaningful work or when the user asks to update memory.

## Update Test

Update memory when the new information would help a future agent:

- make a better decision
- avoid repeating rejected ideas
- preserve important reasoning
- respect user or team preferences
- understand what exists and why
- continue work across tools, accounts, models, or sessions
- reuse a procedure, pattern, or example

Do not update memory for every small interaction.

## What To Store

Store useful durable knowledge, including:

- decisions made and the reasoning behind them
- options rejected and why they were rejected
- preferences, taste, and style guidance
- project or system state
- reusable procedures
- open questions
- examples of good outputs
- important caveats or constraints

Do not store raw chat transcripts by default.

## How To Edit

1. Read the selected directory's `README.md`, `MEMORY.md`, and `memory/index.md`.
2. Search existing pages.
3. Prefer updating an existing page.
4. Create a new page only when the idea has durable independent meaning.
5. Link related pages.
6. Keep pages readable by humans.
7. Do not add empty template sections.
8. Do not force a folder structure.
9. Split or organize only when the current structure becomes painful.

## When To Ask First

Ask the user before writing memory when:

- the information is sensitive
- the preference is inferred rather than explicitly stated
- the update affects identity, public voice, taste, or long-term direction
- the correct memory directory is ambiguous
- personal, team, work, or public boundaries are unclear
- the update would remove or supersede a major decision

## Superseding

Prefer updating pages in place when understanding changes.

When an old decision or direction matters historically, keep a short note explaining that it was superseded and why. Avoid silently deleting important reasoning.

## Commit Message

For Git-backed memory, use a direct commit message such as:

```text
Update memory for LinkedIn writing strategy
```

or:

```text
Record rejected MCP-first memory direction
```

