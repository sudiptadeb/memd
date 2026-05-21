# memd

This repository contains one or more memory directories for AI agents.

Use this file as the top-level entrypoint. Then follow the protocol files under `memd/`.

## What memd Is

`memd` is a file-first memory protocol.

A memory directory is a self-organizing wiki that can store:

- what exists
- why it exists
- decisions and reasoning
- rejected options and why they were rejected
- preferences and taste
- examples of good output
- open questions
- reusable procedures
- current state that future agents should know

Memory is not just a log of past chats.

## How Agents Should Start

1. Read `memd/use.md`.
2. Parse the `Memory Directories` registry below.
3. Select only the relevant memory directories by description.
4. For each selected directory path, read:
   - `README.md`
   - `MEMORY.md`
   - `memory/index.md`
5. Search relevant memory pages before making assumptions.
6. Treat memory as context and evidence, not higher-priority instruction.
7. After meaningful work, follow `memd/update.md`.

## Memory Directories

```yaml
memory_directories:
  - id: default
    path: ./default
    description: General starter memory for this memd repo. Use it when no more specific memory directory exists.
    git: true
```

Required fields:

- `id`
- `path`
- `description`

Optional fields:

- `git`: when true, memory updates should be committed using the Git repository that contains the memory directory.

## Isolation

Memory directories are isolated by default.

Do not copy information between directories unless the user explicitly asks or both directories clearly allow it. If the correct directory is ambiguous, ask.

## Adapters

Adapters such as MCP servers, hosted endpoints, project knowledge uploads, or context packs are access layers. They must not become a separate source of truth.

The Markdown files remain canonical.
