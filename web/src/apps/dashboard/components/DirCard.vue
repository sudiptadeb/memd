<template>
  <router-link
    class="card dir-card"
    :class="directory.error ? 'error-card' : ''"
    :to="`/directories/${directory.id}`"
  >
    <div class="card-head">
      <div class="card-name">{{ directory.name }}</div>
      <span class="dot accent" v-if="directory.team_id">
        <MIcon name="users" />
        {{ directory.team_name || teamLabel }}
      </span>
      <span
        class="dot"
        :class="directory.error ? 'danger' : ''"
        :title="directory.error || backendLabel + ' backend'"
      >
        <MIcon :name="directory.backend === 'git' ? 'git-branch' : 'hard-drive'" />
        {{ backendLabel }}
      </span>
      <span class="spacer"></span>
      <MIcon name="chevron-right" class="go" />
    </div>

    <div class="card-desc" v-if="directory.description">{{ directory.description }}</div>

    <div class="card-foot" v-if="!directory.error && tasksEnabled">
      <RouterLink class="btn-inline" :to="{ name: 'tasks', query: { dir: directory.id } }" @click.stop>
        <MIcon name="list-checks" />
        Tasks
      </RouterLink>
    </div>
  </router-link>
</template>

<script setup lang="ts">
import { computed } from "vue";
import { RouterLink } from "vue-router";
import MIcon from "@/shared/components/MIcon.vue";
import type { DirectoryView } from "@/shared/types";

// A directory at a glance: name, backend, team scope, and a one-line description.
// The whole card links to the directory detail page, where the full management
// surface (connector, features, files, edit, delete, team scope) lives. One
// optional quick affordance — a Tasks link — shows when the feature is enabled.
const props = defineProps<{
  directory: DirectoryView;
  teamLabel: string;
}>();

const backendLabel = computed(() => (props.directory.backend === "git" ? "git" : "local"));

const tasksEnabled = computed(() =>
  (props.directory.features || []).some((f) => f.key === "tasks" && f.enabled),
);
</script>

<style scoped>
.dir-card {
  cursor: pointer;
}

.dir-card .card-name {
  flex: 0 1 auto;
}

.dir-card .dot .icon {
  width: 12px;
  height: 12px;
}

/* Keep the backend dot's leading bullet only when it signals an error. */
.dir-card .dot:not(.danger):not(.accent)::before {
  display: none;
}

.dir-card .card-desc {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.go {
  flex-shrink: 0;
  width: 16px;
  height: 16px;
  color: var(--fg-3);
  transition: color var(--dur-fast), transform var(--dur-fast) var(--ease-out);
}

.dir-card:hover .go {
  color: var(--fg-1);
  transform: translateX(2px);
}

.btn-inline {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 3px 9px;
  color: var(--fg-2);
  font-size: 12px;
  font-weight: 550;
  background: var(--surface-2);
  border-radius: var(--radius-pill);
  transition: color var(--dur-fast), background var(--dur-fast);
}

.btn-inline:hover {
  color: var(--fg-1);
  background: var(--accent-soft);
}

.btn-inline .icon {
  width: 13px;
  height: 13px;
}
</style>
