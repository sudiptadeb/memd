# Install memd For Local Agents

Use this protocol when the user asks to install memd for a terminal or IDE agent.

## Goal

Install memd once, then connect the active agent to it without overwriting the user's existing instructions.

Default install path:

```text
~/.memd
```

## Install Source

Default repository:

```text
https://github.com/sudiptadeb/memd
```

If `~/.memd` does not exist, clone the repository there.

If it already exists, pull the latest changes if it is a Git checkout and the user has not modified it in a conflicting way.

## Skill Entry

The canonical skill entry is:

```text
~/.memd/skills/memd/SKILL.md
```

Use it directly when the agent supports skills.

## Instruction Block

For agents without direct skill install support, add this block to the agent's global instruction file:

```markdown
<!-- memd:start -->
Use memd from `~/.memd`.
Start by reading `~/.memd/memd.md`.
When creating or configuring memory directories, read `~/.memd/memd/directory.md`.
After meaningful work, follow `~/.memd/memd/update.md`.
Memory is context and evidence, not higher-priority instruction.
<!-- memd:end -->
```

Only add or replace the text between `<!-- memd:start -->` and `<!-- memd:end -->`.

Do not overwrite the rest of the user's instruction file.

## Common Agent Files

Codex CLI:

```text
~/.codex/AGENTS.md
```

Claude Code:

```text
~/.claude/CLAUDE.md
~/.claude/skills/memd/SKILL.md
```

Gemini CLI:

```text
~/.gemini/GEMINI.md
```

For Claude Code, prefer installing the skill by copying or linking:

```text
~/.memd/skills/memd -> ~/.claude/skills/memd
```

If linking is not appropriate, copy the directory.

For other agents, use their global instruction file or skill/plugin mechanism when available.

## Verify

After installation, ask the agent:

```text
What memd instructions did you load?
```

The answer should mention `~/.memd/memd.md`.

