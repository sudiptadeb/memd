<template>
  <section class="app-section info-section">
    <div class="section-head">
      <div class="titles">
        <h2>How it works</h2>
        <span class="desc">
          memd exposes selected local memory directories through scoped connector URLs.
        </span>
      </div>
    </div>

    <div class="info-grid">
      <article class="info-card">
        <div class="empty-icon"><MIcon name="folder-open" /></div>
        <h3>Directories</h3>
        <p>Folders or git repositories that memd can read, index, and serve to connected agents.</p>
      </article>
      <article class="info-card">
        <div class="empty-icon"><MIcon name="list-checks" /></div>
        <h3>Tasks</h3>
        <p>
          Turn on <strong>Tasks</strong> for any directory to keep structured task memory — Markdown
          checklists with due dates, priorities, and subtasks — and manage it from a cross-directory
          board. Files stay the source of truth.
        </p>
      </article>
      <article class="info-card">
        <div class="empty-icon"><MIcon name="plug" /></div>
        <h3>Connectors</h3>
        <p>
          Scoped URLs for agents. Each connector chooses directories, access type, and read/write
          permissions.
        </p>
      </article>
      <article class="info-card">
        <div class="empty-icon"><MIcon name="users" /></div>
        <h3>Teams</h3>
        <p>
          Share selected directories and connectors with people you invite. Shared items show the
          team name.
        </p>
      </article>
      <article class="info-card">
        <div class="empty-icon"><MIcon name="activity" /></div>
        <h3>Activity</h3>
        <p>
          A live stream of local server events, useful for checking whether agents are reaching
          memd.
        </p>
      </article>
    </div>

    <div class="info-grid">
      <article class="info-card">
        <div class="empty-icon"><MIcon name="info" /></div>
        <h3>The mental model</h3>
        <p>
          Your memory lives as plain files you control — a folder on disk or a private Git repo. Each
          directory keeps a <code>MEMORY.md</code> as its entry point, and memd indexes the rest. A
          connector is one scoped URL that hands chosen directories to an agent; teams let you share
          those directories and connectors with people you invite.
        </p>
      </article>
      <article class="info-card">
        <div class="empty-icon"><MIcon name="plug" /></div>
        <h3>Wire up an agent</h3>
        <p>
          Create a connector, then paste its URL into your agent. In Claude Code:
          <code>claude mcp add --transport http memd "&lt;connector URL&gt;"</code>. Any other MCP
          client takes the same URL in its server settings. From the next conversation on, the agent
          loads and updates memory on its own.
        </p>
      </article>
    </div>
  </section>
</template>

<script setup lang="ts">
// Static "How it works" view: the mental model behind memd (directories,
// connectors, teams, MEMORY.md) and how to wire an agent. Ported faithfully from
// the Alpine app's info-section; purely presentational, no state.
import MIcon from "@/shared/components/MIcon.vue";
</script>
