<template>
  <section class="app-section">
    <div class="section-head">
      <div class="titles">
        <h2>Connectors <span class="count">{{ connectors.length }}</span></h2>
        <span class="desc">Each connector is one URL scoped to selected directories and optional write access.</span>
      </div>
      <span class="spacer"></span>
      <button
        class="btn secondary"
        type="button"
        v-if="canCreate"
        @click="openCreate"
        :disabled="!hasAttachable"
        :title="hasAttachable ? 'Add connector' : 'Add a directory first'"
      >
        <MIcon name="plus" />
        <span class="btn-label">Add connector</span>
      </button>
    </div>

    <div class="cards" v-if="connectors.length">
      <ConnCard
        v-for="connector in sortedConnectors"
        :key="connector.id"
        :connector="connector"
        :directories="directories"
        :team-label="teamName(connector.team_id)"
        @edit="openEdit"
        @changed="reload"
      />
    </div>

    <div class="empty" v-else>
      <div class="empty-icon"><MIcon name="plug" /></div>
      <h4>No connectors yet</h4>
      <p>A connector issues a URL an agent can use to reach memd. Each one is scoped to specific directories.</p>
      <button class="btn primary" type="button" v-if="canCreate" @click="openCreate" :disabled="!hasAttachable">
        <MIcon name="plus" />
        Add your first connector
      </button>
      <p class="empty-note" v-if="!hasAttachable">Add a directory first.</p>
    </div>
  </section>

  <ConnForm
    :open="formOpen"
    :mode="formMode"
    :connector="editing"
    :manageable-teams="manageableTeams"
    :directories="directories"
    @close="formOpen = false"
    @saved="onSaved"
  />
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import MIcon from "@/shared/components/MIcon.vue";
import ConnCard from "../components/ConnCard.vue";
import ConnForm from "../components/ConnForm.vue";
import { connectors as connectorsApi, directories as directoriesApi, teams as teamsApi, ApiError } from "@/shared/api";
import { toast } from "@/shared/bus";
import { useSession } from "@/shared/session";
import type { ConnectorView, DirectoryView, Team } from "@/shared/types";

const { user } = useSession();

const connectors = ref<ConnectorView[]>([]);
const directories = ref<DirectoryView[]>([]);
const teams = ref<Team[]>([]);

const formOpen = ref(false);
const formMode = ref<"create" | "edit">("create");
const editing = ref<ConnectorView | null>(null);

const canCreate = computed(() => !!user.value && !user.value.super_admin);
const manageableTeams = computed(() => teams.value.filter((t) => t.can_manage));

// A connector can be created only if there's at least one directory the user can
// attach (personal scope).
const hasAttachable = computed(() => directories.value.some((d) => d.can_attach));

function teamName(teamID?: string): string {
  if (!teamID) return "";
  const team = teams.value.find((t) => t.id === teamID);
  return team ? team.name : "";
}

const sortedConnectors = computed(() => {
  return connectors.value.slice().sort((a, b) => {
    const teamA = a.team_id ? teamName(a.team_id) || "Team" : "";
    const teamB = b.team_id ? teamName(b.team_id) || "Team" : "";
    if (teamA !== teamB) {
      if (!teamA) return -1;
      if (!teamB) return 1;
      return teamA.localeCompare(teamB);
    }
    return (a.name || "").localeCompare(b.name || "");
  });
});

async function reload(): Promise<void> {
  try {
    const [conns, dirs, tms] = await Promise.all([
      connectorsApi.list(),
      directoriesApi.list(),
      teamsApi.list(),
    ]);
    connectors.value = conns.connectors || [];
    directories.value = dirs.directories || [];
    teams.value = tms.teams || [];
  } catch (e) {
    toast(e instanceof ApiError ? e.message : String(e), "error");
  }
}

function openCreate(): void {
  editing.value = null;
  formMode.value = "create";
  formOpen.value = true;
}

function openEdit(connector: ConnectorView): void {
  editing.value = connector;
  formMode.value = "edit";
  formOpen.value = true;
}

async function onSaved(): Promise<void> {
  formOpen.value = false;
  await reload();
}

onMounted(reload);
</script>
