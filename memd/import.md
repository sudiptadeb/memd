# Import Chat History Into memd

Use this protocol when the user asks to initialize or enrich memory from old conversations, exports, notes, or pasted chat history.

## Principle

Do not turn raw chats into memory.

Use old conversations to update the memory wiki with what future agents should know.

## Process

1. Identify the target memory directory.
2. Read the target directory's `README.md`, `MEMORY.md`, and `memory/index.md`.
3. Review the source conversations.
4. Extract durable knowledge:
   - decisions
   - rejected options
   - user preferences
   - writing voice
   - project state
   - procedures
   - examples
   - open questions
5. Search existing memory pages.
6. Update existing pages where possible.
7. Create new pages only when needed.
8. Ask before storing sensitive or inferred personal information.

## Source References

If useful, record lightweight source notes such as:

```text
Source: Claude export, conversation about LinkedIn strategy, 2026-05
```

Do not store complete transcripts unless the user explicitly asks.

## Import From Web Chats

For Claude, ChatGPT, Gemini, and similar systems:

1. Prefer official data export when available.
2. If export is not available, accept pasted conversations in chunks.
3. Process each chunk against this protocol.
4. Keep the final memory human-readable and compact.

