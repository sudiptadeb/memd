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
        :team-label="teamName(directory.team_id)"
        @browse="openBrowse"
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
  <DirFiles :open="browseOpen" :directory="browseDir" @close="browseOpen = false" />
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import MIcon from "@/shared/components/MIcon.vue";
import DirCard from "../components/DirCard.vue";
import DirFiles from "../components/DirFiles.vue";
import DirForm from "../components/DirForm.vue";
import { directories as directoriesApi, teams as teamsApi, ApiError } from "@/shared/api";
import { toast } from "@/shared/bus";
import { useSession } from "@/shared/session";
import type { DirectoryView, Team } from "@/shared/types";

const { user, isSuperAdmin } = useSession();

const directories = ref<DirectoryView[]>([]);
const teams = ref<Team[]>([]);

const addOpen = ref(false);

// The file browser is hosted once at the page level; directory cards ask to open
// it for themselves via the @browse event, so we never mount a sheet per card.
const browseOpen = ref(false);
const browseDir = ref<DirectoryView | null>(null);

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

async function reload(): Promise<void> {
  try {
    const [dirs, tms] = await Promise.all([directoriesApi.list(), teamsApi.list()]);
    directories.value = dirs.directories || [];
    teams.value = tms.teams || [];
  } catch (e) {
    toast(e instanceof ApiError ? e.message : String(e), "error");
  }
}

function openAdd(): void {
  addOpen.value = true;
}

function openBrowse(directory: DirectoryView): void {
  browseDir.value = directory;
  browseOpen.value = true;
}

async function onCreated(): Promise<void> {
  addOpen.value = false;
  await reload();
}

onMounted(reload);
</script>
