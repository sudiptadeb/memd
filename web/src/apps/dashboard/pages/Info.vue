<template>
  <section class="app-section info-section">
    <!-- Hero: one promise, stated plainly, with room to breathe. -->
    <header class="info-hero">
      <span class="eyebrow">How it works</span>
      <h2 class="info-title">Local memory, served to your agents</h2>
      <p class="info-lead">
        memd exposes selected local memory directories through scoped connector URLs.
      </p>
    </header>

    <!-- The core building blocks. Reuses the global .info-grid (collapses to a
         single column on narrow screens) with scoped polish per card. -->
    <div class="info-grid">
      <article class="info-card">
        <div class="info-card-head">
          <span class="info-card-icon"><MIcon name="folder-open" /></span>
          <h3>Directories</h3>
        </div>
        <p>Folders or git repositories that memd can read, index, and serve to connected agents.</p>
      </article>
      <article class="info-card">
        <div class="info-card-head">
          <span class="info-card-icon"><MIcon name="list-checks" /></span>
          <h3>Tasks</h3>
        </div>
        <p>
          Turn on <strong>Tasks</strong> for any directory to keep structured task memory — Markdown
          checklists with due dates, priorities, and subtasks — and manage it from a cross-directory
          board. Files stay the source of truth.
        </p>
      </article>
      <article class="info-card">
        <div class="info-card-head">
          <span class="info-card-icon"><MIcon name="plug" /></span>
          <h3>Connectors</h3>
        </div>
        <p>
          Scoped URLs for agents. Each connector chooses directories, access type, and read/write
          permissions.
        </p>
      </article>
      <article class="info-card">
        <div class="info-card-head">
          <span class="info-card-icon"><MIcon name="users" /></span>
          <h3>Teams</h3>
        </div>
        <p>
          Share selected directories and connectors with people you invite. Shared items show the
          team name.
        </p>
      </article>
      <article class="info-card">
        <div class="info-card-head">
          <span class="info-card-icon"><MIcon name="activity" /></span>
          <h3>Activity</h3>
        </div>
        <p>
          A live stream of local server events, useful for checking whether agents are reaching
          memd.
        </p>
      </article>
    </div>

    <!-- The two explainer cards. Span the full width so the longer prose and the
         wire-up command have room. -->
    <div class="info-grid info-grid-wide">
      <article class="info-card">
        <div class="info-card-head">
          <span class="info-card-icon"><MIcon name="info" /></span>
          <h3>The mental model</h3>
        </div>
        <p>
          Your memory lives as plain files you control — a folder on disk or a private Git repo. Each
          directory keeps a <code>MEMORY.md</code> as its entry point, and memd indexes the rest. A
          connector is one scoped URL that hands chosen directories to an agent; teams let you share
          those directories and connectors with people you invite.
        </p>
      </article>
      <article class="info-card">
        <div class="info-card-head">
          <span class="info-card-icon"><MIcon name="plug" /></span>
          <h3>Wire up an agent</h3>
        </div>
        <p>
          Create a connector, then paste its URL into your agent. In Claude Code:
        </p>
        <!-- The connector command as a real, copyable code block. Copy mirrors the
             house pattern in ConnCard: copyToClipboard + a toast. -->
        <div class="code-block">
          <code>{{ connectorCommand }}</code>
          <button
            class="icon-btn code-copy"
            type="button"
            :title="copied ? 'Copied' : 'Copy command'"
            :aria-label="copied ? 'Command copied' : 'Copy command'"
            @click="copyCommand"
          >
            <MIcon :name="copied ? 'check' : 'copy'" />
          </button>
        </div>
        <p>
          Any other MCP client takes the same URL in its server settings. From the next conversation
          on, the agent loads and updates memory on its own.
        </p>
      </article>
    </div>
  </section>
</template>

<script setup lang="ts">
// Static "How it works" view: the mental model behind memd (directories,
// connectors, teams, MEMORY.md) and how to wire an agent. Mostly presentational;
// the only state is the copy-to-clipboard affordance for the wire-up command.
import { ref } from "vue";
import MIcon from "@/shared/components/MIcon.vue";
import { copyToClipboard } from "@/shared/utils";
import { toast } from "@/shared/bus";

// The literal command an agent operator pastes into Claude Code. <connector URL>
// stays a placeholder — the real URL comes from a connector on the Connectors page.
const connectorCommand = 'claude mcp add --transport http memd "<connector URL>"';

const copied = ref(false);
let copiedTimer: ReturnType<typeof setTimeout> | undefined;

async function copyCommand(): Promise<void> {
  const ok = await copyToClipboard(connectorCommand);
  toast(ok ? "Command copied" : "Copy failed", ok ? "success" : "error");
  if (!ok) return;
  // Briefly swap the copy glyph for a check as confirmation.
  copied.value = true;
  if (copiedTimer) clearTimeout(copiedTimer);
  copiedTimer = setTimeout(() => {
    copied.value = false;
  }, 1500);
}
</script>

<style scoped>
/* --- Hero --- */
.info-hero {
  display: flex;
  flex-direction: column;
  gap: 10px;
  max-width: 720px;
  padding-bottom: 4px;
}

.info-title {
  color: var(--fg-1);
  font-size: 24px;
  font-weight: 680;
  line-height: 1.15;
  letter-spacing: -0.01em;
}

.info-lead {
  max-width: 620px;
  color: var(--fg-2);
  font-size: 14.5px;
  line-height: 1.55;
}

/* --- Cards --- */
/* The wide grid lets the two explainer cards share a single row on desktop and
   stack everywhere narrower (the global .info-grid handles the 1-column case). */
.info-grid-wide {
  grid-template-columns: repeat(2, minmax(0, 1fr));
}

.info-section :deep(.info-card) {
  gap: 10px;
  padding: 18px;
  transition: border-color var(--dur-fast) var(--ease-out),
    transform var(--dur-fast) var(--ease-out);
}

.info-section :deep(.info-card:hover) {
  border-color: var(--border-strong);
  transform: translateY(-1px);
}

.info-card-head {
  display: flex;
  gap: 10px;
  align-items: center;
}

.info-card-head h3 {
  color: var(--fg-1);
  font-size: 14.5px;
  font-weight: 650;
  line-height: 1.2;
}

.info-card-icon {
  display: inline-flex;
  flex: none;
  align-items: center;
  justify-content: center;
  width: 30px;
  height: 30px;
  color: var(--accent);
  background: var(--accent-soft);
  border-radius: var(--radius-md);
}

.info-card-icon :deep(.icon) {
  width: 16px;
  height: 16px;
}

/* Inline code inside card prose: a subtle chip, not a wall. */
.info-card p :deep(code) {
  padding: 1px 5px;
  color: var(--fg-1);
  font-family: var(--font-mono);
  font-size: 12px;
  background: var(--surface-2);
  border-radius: var(--radius-sm);
}

/* --- Wire-up command code block --- */
.code-block {
  display: flex;
  gap: 8px;
  align-items: center;
  padding: 10px 10px 10px 12px;
  background: var(--surface-2);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
}

.code-block code {
  flex: 1;
  min-width: 0;
  overflow-x: auto;
  overflow-y: hidden;
  color: var(--fg-1);
  font-family: var(--font-mono);
  font-size: 12.5px;
  line-height: 1.4;
  white-space: nowrap;
  scrollbar-width: thin;
  -webkit-overflow-scrolling: touch;
}

.code-block code::-webkit-scrollbar {
  height: 6px;
}

.code-block code::-webkit-scrollbar-thumb {
  background: var(--border-strong);
  border-radius: var(--radius-pill);
}

.code-copy {
  flex: none;
}

/* --- Responsive: collapse the wide grid below the dashboard's narrow breakpoint
   so every card is full-width on phones (375px and up). --- */
@media (max-width: 680px) {
  .info-grid-wide {
    grid-template-columns: 1fr;
  }

  .info-title {
    font-size: 21px;
  }
}
</style>
