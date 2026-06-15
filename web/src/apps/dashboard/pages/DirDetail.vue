<template>
  <section class="app-section">
    <div class="detail-loading" v-if="loading">Loading directory…</div>

    <div class="empty" v-else-if="!directory">
      <div class="empty-icon"><MIcon name="folder-search" /></div>
      <h4>Directory not found</h4>
      <p>This directory may have been removed, or you no longer have access to it.</p>
      <RouterLink class="btn primary" to="/directories">
        <MIcon name="arrow-left" />
        Back to directories
      </RouterLink>
    </div>

    <template v-else>
      <header class="detail-head">
        <RouterLink class="detail-back" to="/directories" title="Back to directories" aria-label="Back to directories">
          <MIcon name="arrow-left" />
        </RouterLink>
        <div class="detail-titles">
          <div class="detail-title">{{ directory.name }}</div>
          <div class="detail-sub">
            <span>{{ directory.backend === "git" ? "Git repository" : "Local folder" }}</span>
            <template v-if="directory.team_name || teamName(directory.team_id)">
              · {{ directory.team_name || teamName(directory.team_id) }}
            </template>
            <template v-else>· Personal</template>
          </div>
        </div>
        <div class="detail-actions" v-if="directory.can_manage">
          <button class="btn ghost" type="button" @click="editOpen = true">
            <MIcon name="pencil" />
            Edit
          </button>
          <button class="btn danger" type="button" @click="remove" :disabled="deleting">
            <MIcon name="trash-2" />
            Delete
          </button>
        </div>
      </header>

      <!-- Overview ------------------------------------------------------- -->
      <div class="card detail-card">
        <div class="eyebrow">Overview</div>
        <p class="card-desc detail-desc" v-if="directory.description">{{ directory.description }}</p>
        <p class="card-desc detail-desc muted" v-else>No description.</p>
        <code class="card-path">{{ directory.detail }}</code>
        <div class="error-msg" v-if="directory.error">
          <MIcon name="triangle-alert" />
          <span>{{ directory.error }}</span>
        </div>
      </div>

      <!-- Main connector (git + owned only) ----------------------------- -->
      <div class="card detail-card" v-if="directory.owned && directory.backend === 'git'">
        <div class="eyebrow">Main connector</div>
        <p class="field-hint">
          Pick the one connector that writes this directory's branch (main) directly. Every other connector works
          on its own branch. Leave as default to let your own connectors write main and everyone else branch.
        </p>
        <select
          class="mini-select wide-select"
          aria-label="Main-branch connector"
          :value="directory.owner_connector_id || ''"
          @change="setOwnerConnector(($event.target as HTMLSelectElement).value)"
        >
          <option value="">Default — my connectors write main, others branch</option>
          <option v-for="connector in ownConnectors" :key="connector.id" :value="connector.id">
            {{ connector.name }}
          </option>
        </select>
      </div>

      <!-- Features ------------------------------------------------------- -->
      <div class="card detail-card" v-if="directory.can_manage && directory.features && directory.features.length">
        <div class="eyebrow">Features</div>
        <p class="field-hint">
          Enable structured-memory features for this directory (tasks, calendar, …). Disabling keeps the folder
          and its data — it just stops surfacing the feature to agents.
        </p>
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
              @change="setFeature(feature.key, ($event.target as HTMLInputElement).checked)"
            />
            <span>{{ feature.name }}</span>
            <span class="dot soon" v-if="feature.coming_soon">soon</span>
          </label>
        </div>
      </div>

      <!-- Team scope ----------------------------------------------------- -->
      <div class="card detail-card" v-if="directory.can_manage && manageableTeams.length">
        <div class="eyebrow">Team scope</div>
        <p class="field-hint">
          Team members can use a shared directory with their own connectors. Personal directories stay private to
          you.
        </p>
        <select
          class="mini-select wide-select"
          aria-label="Share directory with team"
          :value="directory.team_id || ''"
          @change="setTeam(($event.target as HTMLSelectElement).value)"
        >
          <option value="">Personal — only you</option>
          <option v-for="team in manageableTeams" :key="team.id" :value="team.id">{{ team.name }}</option>
        </select>
      </div>

      <!-- Files ---------------------------------------------------------- -->
      <div class="card detail-card" v-if="!directory.error">
        <div class="detail-card-head">
          <div class="eyebrow">Files</div>
          <span class="spacer"></span>
          <button class="btn ghost" type="button" @click="browseOpen = true">
            <MIcon name="folder-open" />
            Browse files
          </button>
        </div>
        <p class="field-hint">Browse the files this directory serves to agents.</p>
      </div>
    </template>
  </section>

  <DirEditForm :open="editOpen" :directory="directory" @close="editOpen = false" @saved="onSaved" />
  <DirFiles :open="browseOpen" :directory="directory" @close="browseOpen = false" />
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useRoute, useRouter, RouterLink } from "vue-router";
import MIcon from "@/shared/components/MIcon.vue";
import DirEditForm from "../components/DirEditForm.vue";
import DirFiles from "../components/DirFiles.vue";
import { connectors as connectorsApi, directories as directoriesApi, teams as teamsApi, ApiError } from "@/shared/api";
import { toast } from "@/shared/bus";
import type { ConnectorView, DirectoryView, Team } from "@/shared/types";

// The directory detail page: the full management surface that used to crowd the
// card — overview, main-branch connector, feature toggles, team scope, the file
// browser, and edit/delete. There's no single-get endpoint, so it loads the
// list and finds the directory by the route id.
const route = useRoute();
const router = useRouter();

const directory = ref<DirectoryView | null>(null);
const connectors = ref<ConnectorView[]>([]);
const teams = ref<Team[]>([]);
const loading = ref(true);
const deleting = ref(false);

const editOpen = ref(false);
const browseOpen = ref(false);

const dirId = computed(() => String(route.params.dirId || ""));

const manageableTeams = computed(() => teams.value.filter((t) => t.can_manage));

// Your own connectors that already attach this directory — the candidates for
// its main-branch connector.
const ownConnectors = computed(() => {
  const dir = directory.value;
  if (!dir) return [];
  return connectors.value.filter((c) => c.owned && (c.directory_ids || []).includes(dir.id));
});

function teamName(teamID?: string): string {
  if (!teamID) return "";
  const team = teams.value.find((t) => t.id === teamID);
  return team ? team.name : "";
}

async function load(): Promise<void> {
  loading.value = true;
  try {
    const [dirs, conns, tms] = await Promise.all([
      directoriesApi.list(),
      connectorsApi.list(),
      teamsApi.list(),
    ]);
    directory.value = (dirs.directories || []).find((d) => d.id === dirId.value) || null;
    connectors.value = conns.connectors || [];
    teams.value = tms.teams || [];
  } catch (e) {
    toast(e instanceof ApiError ? e.message : String(e), "error");
  } finally {
    loading.value = false;
  }
}

async function setOwnerConnector(connectorID: string): Promise<void> {
  if (!directory.value) return;
  try {
    await directoriesApi.update(directory.value.id, { owner_connector_id: connectorID || "" });
    await load();
  } catch (e) {
    toast(e instanceof ApiError ? e.message : String(e), "error");
    await load();
  }
}

async function setFeature(key: string, enabled: boolean): Promise<void> {
  if (!directory.value) return;
  try {
    await directoriesApi.setFeature(directory.value.id, key, enabled);
    await load();
  } catch (e) {
    toast(e instanceof ApiError ? e.message : String(e), "error");
    await load();
  }
}

async function setTeam(teamID: string): Promise<void> {
  if (!directory.value) return;
  try {
    await directoriesApi.update(directory.value.id, { team_id: teamID || "" });
    await load();
  } catch (e) {
    toast(e instanceof ApiError ? e.message : String(e), "error");
    await load();
  }
}

async function remove(): Promise<void> {
  const dir = directory.value;
  if (!dir) return;
  if (!window.confirm(`Delete directory ${dir.name}? Connectors using it will lose access.`)) {
    return;
  }
  deleting.value = true;
  try {
    await directoriesApi.remove(dir.id);
    toast("Directory deleted", "success");
    void router.push("/directories");
  } catch (e) {
    toast(e instanceof ApiError ? e.message : String(e), "error");
    deleting.value = false;
  }
}

async function onSaved(): Promise<void> {
  editOpen.value = false;
  await load();
}

onMounted(load);
</script>

<style scoped>
.detail-loading {
  padding: 2rem 0;
  color: var(--fg-3);
  font-size: 0.9rem;
}

.detail-card {
  gap: 10px;
}

.detail-card-head {
  display: flex;
  gap: 10px;
  align-items: center;
}

.detail-desc {
  font-size: 14px;
  white-space: normal;
}

.detail-desc.muted {
  color: var(--fg-3);
}

/* Detail page has room — let the selects breathe past the card-sized cap. */
.wide-select {
  max-width: 420px;
  width: 100%;
  height: 32px;
}

.dot.soon {
  padding: 2px 7px;
  color: var(--fg-3);
  font-size: 10.5px;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  background: var(--surface-2);
}

.dot.soon::before {
  display: none;
}
</style>
