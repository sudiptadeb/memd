# Agent Integrations

Different agents connect to memd through different surfaces.

## Local Coding Agents

Use either:

- the memd skill at `skills/memd/SKILL.md`
- a global instruction block pointing to `memd.md`
- the repo directly

Prompt:

```text
Use memd. Start by reading memd.md.
```

The agent reads and edits Markdown files normally.

## Claude Web

Best remote path: custom connector using a remote MCP server URL.

Alternative path: upload or sync relevant memory files into a Claude Project.

## ChatGPT

Best remote path: remote MCP connector where available.

Alternative paths:

- project files
- custom GPT knowledge
- pasted context from selected memd files

## Gemini

Gemini CLI can use local files and MCP-style integrations.

For Gemini web, use uploaded files, Drive context, or pasted context unless custom remote tool support is available in the active environment.

## Cursor, Windsurf, Copilot, IDE Agents

Use local repo access and project instruction files.

Point their startup instructions at `memd.md`.

## Install Prompt Pattern

For terminal agents, give this prompt:

```text
Install memd from https://github.com/sudiptadeb/memd.
Clone it to ~/.memd if needed.
Follow ~/.memd/memd/install.md.
Do not overwrite my existing instructions.
```

## Integration Rule

Every integration should eventually reduce to:

1. read memd entrypoint
2. select relevant memory directories
3. read/search Markdown memory
4. write Markdown changes if allowed
5. commit or propose changes through Git
