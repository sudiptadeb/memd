<template>
  <section class="app-section">
    <div class="section-head">
      <div class="titles">
        <h2>Directories <span class="count">{{ directories.length }}</span></h2>
        <span class="desc">
          Folders on this machine, or a git repo, that memd can serve to connected agents. Directories shared
          through a team show the team name.
        </span>
      </div>
      <span class="spacer"></span>
      <button class="btn secondary" type="button" v-if="canCreate" @click="openAdd" title="Add directory">
        <MIcon name="plus" />
        <span class="btn-label">Add directory</span>
      </button>
    </div>

    <div class="cards" v-if="directories.length">
      <DirCard
        v-for="directory in sortedDirectories"
        :key="directory.id"
        :directory="directory"
        :manageable-teams="manageableTeams"
        :own-connectors="ownConnectorsForDirectory(directory)"
        :team-label="teamName(directory.team_id)"
        @browse="openBrowse"
        @edit="openEdit"
        @open-tasks="goToTasks"
        @changed="reload"
      />
    </div>

    <div class="empty" v-else>
      <div class="empty-icon"><MIcon name="folder-open" /></div>
      <h4>No directories yet</h4>
      <p>Add a local folder or git repo. memd will expose it to connectors you create.</p>
      <button class="btn primary" type="button" v-if="canCreate" @click="openAdd">
        <MIcon name="plus" />
        Add your first directory
      </button>
    </div>
  </section>

  <DirForm :open="addOpen" :teams="manageableTeams" :can-browse-fs="canBrowseFs" @close="addOpen = false" @created="onCreated" />
  <DirEditForm :open="editOpen" :directory="editing" @close="editOpen = false" @saved="onSaved" />
  <DirFiles :open="browseOpen" :directory="browsing" @close="browseOpen = false" />
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useRouter } from "vue-router";
import MIcon from "@/shared/components/MIcon.vue";
import DirCard from "../components/DirCard.vue";
import DirForm from "../components/DirForm.vue";
import DirEditForm from "../components/DirEditForm.vue";
import DirFiles from "../components/DirFiles.vue";
import { connectors as connectorsApi, directories as directoriesApi, teams as teamsApi, ApiError } from "@/shared/api";
import { toast } from "@/shared/bus";
import { useSession } from "@/shared/session";
import type { ConnectorView, DirectoryView, Team } from "@/shared/types";

const router = useRouter();
const { user, isSuperAdmin } = useSession();

const directories = ref<DirectoryView[]>([]);
const connectors = ref<ConnectorView[]>([]);
const teams = ref<Team[]>([]);

const addOpen = ref(false);
const editOpen = ref(false);
const browseOpen = ref(false);
const editing = ref<DirectoryView | null>(null);
const browsing = ref<DirectoryView | null>(null);

// Add buttons are hidden for super admins (they manage everyone's, not their
// own), matching the Alpine UI.
const canCreate = computed(() => !!user.value && !user.value.super_admin);
// The server-filesystem picker is for local accounts and super admins only.
const canBrowseFs = computed(() => !!user.value && (user.value.local || isSuperAdmin.value));

const manageableTeams = computed(() => teams.value.filter((t) => t.can_manage));

function teamName(teamID?: string): string {
  if (!teamID) return "";
  const team = teams.value.find((t) => t.id === teamID);
  return team ? team.name : "";
}

// Personal items first, then team-shared items grouped per team, names within.
const sortedDirectories = computed(() => {
  return directories.value.slice().sort((a, b) => {
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

// Your own connectors that already attach this directory — the candidates for
// its main-branch connector.
function ownConnectorsForDirectory(directory: DirectoryView): ConnectorView[] {
  return connectors.value.filter(
    (c) => c.owned && (c.directory_ids || []).includes(directory.id),
  );
}

async function reload(): Promise<void> {
  try {
    const [dirs, conns, tms] = await Promise.all([
      directoriesApi.list(),
      connectorsApi.list(),
      teamsApi.list(),
    ]);
    directories.value = dirs.directories || [];
    connectors.value = conns.connectors || [];
    teams.value = tms.teams || [];
  } catch (e) {
    toast(e instanceof ApiError ? e.message : String(e), "error");
  }
}

function openAdd(): void {
  addOpen.value = true;
}

function openEdit(directory: DirectoryView): void {
  editing.value = directory;
  editOpen.value = true;
}

function openBrowse(directory: DirectoryView): void {
  browsing.value = directory;
  browseOpen.value = true;
}

function goToTasks(directory: DirectoryView): void {
  void router.push({ name: "tasks", query: { dir: directory.id } });
}

async function onCreated(): Promise<void> {
  addOpen.value = false;
  await reload();
}

async function onSaved(): Promise<void> {
  editOpen.value = false;
  await reload();
}

onMounted(reload);
</script>
