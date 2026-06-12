# LinkedIn post — memd now at memd.debkosh.com

Draft for announcing memd's public home and inviting people to use it.

## Copy-paste version (Unicode bold hook, LinkedIn composer)

𝗘𝘃𝗲𝗿𝘆 𝗔𝗜 𝘁𝗼𝗼𝗹 𝗜 𝘂𝘀𝗲 𝘀𝗵𝗶𝗽𝗽𝗲𝗱 𝗶𝘁𝘀 𝗼𝘄𝗻 𝗺𝗲𝗺𝗼𝗿𝘆 𝗳𝗲𝗮𝘁𝘂𝗿𝗲 𝗶𝗻 𝘁𝗵𝗲 𝗽𝗮𝘀𝘁 𝘆𝗲𝗮𝗿.

ChatGPT remembers things about me now. So does Claude, in a format ChatGPT will never see. What I teach one agent is invisible to the next, and the more agents I run, the worse it gets.

The consensus answer is to wait for the vendors to integrate with each other. I built the opposite: memd, a small Go server that treats memory as plain files. Markdown in a folder, or in a private Git repo you control. memd serves those files over MCP, so Claude Code, Codex CLI, Cursor and ChatGPT all read and write the same memory. Teach it once, every agent knows it.

It doesn't just store. Five workflows keep the memory organised, named after what they actually do... dream cements what got used this session and fades what didn't, housekeep fixes dangling links and drift. A whole session of edits lands as one clean git commit, so you can review or revert anything the agent does.

I've run my own work and personal memory on it for weeks. This post was drafted by an agent reading from that memory.

memd now lives at memd.debkosh.com, the source is MIT licensed at github.com/sudiptadeb/memd. If you run more than one AI agent and you're tired of repeating yourself, give it a look. Feedback welcome, especially the critical kind.

## Plain-text version (for AI-detector pass)

Every AI tool I use shipped its own memory feature in the past year.

ChatGPT remembers things about me now. So does Claude, in a format ChatGPT will never see. What I teach one agent is invisible to the next, and the more agents I run, the worse it gets.

The consensus answer is to wait for the vendors to integrate with each other. I built the opposite: memd, a small Go server that treats memory as plain files. Markdown in a folder, or in a private Git repo you control. memd serves those files over MCP, so Claude Code, Codex CLI, Cursor and ChatGPT all read and write the same memory. Teach it once, every agent knows it.

It doesn't just store. Five workflows keep the memory organised, named after what they actually do... dream cements what got used this session and fades what didn't, housekeep fixes dangling links and drift. A whole session of edits lands as one clean git commit, so you can review or revert anything the agent does.

I've run my own work and personal memory on it for weeks. This post was drafted by an agent reading from that memory.

memd now lives at memd.debkosh.com, the source is MIT licensed at github.com/sudiptadeb/memd. If you run more than one AI agent and you're tired of repeating yourself, give it a look. Feedback welcome, especially the critical kind.

## Pre-publish checklist

- [ ] AI-detector pass, target under 15%.
- [ ] Confirm what memd.debkosh.com shows a stranger (landing page vs bare endpoint) and that the management UI is not reachable on the public ingress.
- [ ] Decide whether to keep the "drafted by an agent reading from that memory" line.
