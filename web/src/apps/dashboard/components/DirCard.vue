<template>
  <article class="card" :class="directory.error ? 'error-card' : ''">
    <div class="card-head">
      <div class="card-name">{{ directory.name }}</div>
      <span class="dot accent" v-if="directory.team_id">{{ directory.team_name || teamLabel }}</span>
      <span
        class="dot"
        :class="directory.error ? 'danger' : 'success'"
        :title="directory.error || directory.backend + ' reachable'"
        >{{ directory.backend }}</span
      >
      <span class="spacer"></span>
      <button
        class="btn ghost"
        type="button"
        v-if="!directory.error && tasksEnabled"
        @click="emit('open-tasks', directory)"
      >
        <MIcon name="list-checks" />
        Tasks
      </button>
      <button class="btn ghost" type="button" v-if="!directory.error" @click="emit('browse', directory)">
        <MIcon name="folder-open" />
        Browse
      </button>
      <button class="btn ghost" type="button" v-if="directory.can_manage" @click="emit('edit', directory)">
        <MIcon name="pencil" />
        Edit
      </button>
      <select
        class="mini-select"
        aria-label="Share directory with team"
        v-if="directory.can_manage && manageableTeams.length"
        :value="directory.team_id || ''"
        @change="onTeamChange"
      >
        <option value="">Personal</option>
        <option v-for="team in manageableTeams" :key="team.id" :value="team.id">{{ team.name }}</option>
      </select>
      <button
        class="icon-btn danger"
        type="button"
        v-if="directory.can_manage"
        title="Delete directory"
        @click="onDelete"
      >
        <MIcon name="trash-2" />
      </button>
    </div>
    <div class="card-desc" v-if="directory.description">{{ directory.description }}</div>
    <code class="card-path">{{ directory.detail }}</code>

    <div class="card-row" v-if="directory.owned && directory.backend === 'git'">
      <span
        class="field-label"
        title="Pick the one connector that writes this directory's branch (main) directly. Every other connector works on its own branch. Leave as default to let your own connectors write main and everyone else branch."
        >Main connector</span
      >
      <select
        class="mini-select"
        aria-label="Main-branch connector"
        :value="directory.owner_connector_id || ''"
        @change="onOwnerConnectorChange"
      >
        <option value="">Default — my connectors write main, others branch</option>
        <option v-for="connector in ownConnectors" :key="connector.id" :value="connector.id">
          {{ connector.name }}
        </option>
      </select>
    </div>

    <div class="card-row" v-if="directory.can_manage && directory.features && directory.features.length">
      <span
        class="field-label"
        title="Enable structured-memory features for this directory (tasks, calendar, …). Disabling keeps the folder and its data — it just stops surfacing the feature to agents."
        >Features</span
      >
      <div class="feature-toggles">
        <label
          v-for="feature in directory.features"
          :key="feature.key"
          class="feature-toggle"
          :class="feature.coming_soon ? 'muted' : ''"
        >
          <input
            type="checkbox"
            :checked="feature.enabled"
            :disabled="feature.coming_soon"
            @change="onFeatureChange(feature.key, $event)"
          />
          <span>{{ feature.name }}</span>
          <span class="dot" v-if="feature.coming_soon">soon</span>
        </label>
      </div>
    </div>

    <div class="error-msg" v-if="directory.error">
      <MIcon name="triangle-alert" />
      <span>{{ directory.error }}</span>
    </div>
  </article>
</template>

<script setup lang="ts">
import { computed } from "vue";
import MIcon from "@/shared/components/MIcon.vue";
import { directories, ApiError } from "@/shared/api";
import { toast } from "@/shared/bus";
import type { ConnectorView, DirectoryView, Team } from "@/shared/types";

// One directory card. Sheet-opening actions (browse/edit/tasks) bubble up;
// inline PATCH actions (team scope, main connector, feature toggles, delete)
// are applied here and a `changed` event asks the parent to reload.
const props = defineProps<{
  directory: DirectoryView;
  manageableTeams: Team[];
  ownConnectors: ConnectorView[];
  teamLabel: string;
}>();
const emit = defineEmits<{
  (e: "browse", directory: DirectoryView): void;
  (e: "edit", directory: DirectoryView): void;
  (e: "open-tasks", directory: DirectoryView): void;
  (e: "changed"): void;
}>();

const tasksEnabled = computed(() =>
  (props.directory.features || []).some((f) => f.key === "tasks" && f.enabled),
);

function fail(e: unknown): void {
  toast(e instanceof ApiError ? e.message : String(e), "error");
}

async function onTeamChange(event: Event): Promise<void> {
  const teamID = (event.target as HTMLSelectElement).value;
  try {
    await directories.update(props.directory.id, { team_id: teamID || "" });
    emit("changed");
  } catch (e) {
    fail(e);
    emit("changed");
  }
}

async function onOwnerConnectorChange(event: Event): Promise<void> {
  const connectorID = (event.target as HTMLSelectElement).value;
  try {
    await directories.update(props.directory.id, { owner_connector_id: connectorID || "" });
    emit("changed");
  } catch (e) {
    fail(e);
    emit("changed");
  }
}

async function onFeatureChange(key: string, event: Event): Promise<void> {
  const enabled = (event.target as HTMLInputElement).checked;
  try {
    await directories.setFeature(props.directory.id, key, enabled);
    emit("changed");
  } catch (e) {
    fail(e);
    emit("changed");
  }
}

async function onDelete(): Promise<void> {
  if (
    !window.confirm(
      "Delete directory " + props.directory.name + "? Connectors using it will lose access.",
    )
  ) {
    return;
  }
  try {
    await directories.remove(props.directory.id);
    emit("changed");
  } catch (e) {
    toast("Could not delete directory: " + (e instanceof ApiError ? e.message : String(e)), "error");
    emit("changed");
  }
}
</script>
