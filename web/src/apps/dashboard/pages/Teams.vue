<template>
  <section class="app-section">
    <div class="section-head">
      <div class="titles">
        <h2>Teams <span class="count" v-text="teams.length"></span></h2>
        <span class="desc">Create teams, invite people, and share selected directories.</span>
      </div>
      <span class="spacer"></span>
      <button class="btn secondary" type="button" title="New team" @click="openNew">
        <MIcon name="plus" />
        <span class="btn-label">New team</span>
      </button>
    </div>

    <div class="picker-error" v-if="loadErr" v-text="loadErr"></div>

    <div class="cards" v-if="teams.length">
      <article class="card" v-for="team in teams" :key="team.id">
        <div class="card-head">
          <div class="card-name" v-text="team.name"></div>
          <span class="dot accent" v-text="roleLabel(team.role)"></span>
          <span class="spacer"></span>
          <button class="btn ghost" type="button" @click="openTeam(team)">
            <MIcon name="pencil" />
            Manage
          </button>
        </div>
      </article>
    </div>

    <div class="empty" v-else-if="!loading">
      <div class="empty-icon"><MIcon name="users" /></div>
      <h4>No teams yet</h4>
      <p>Create a team to share selected directories with other local users.</p>
      <button class="btn primary" type="button" @click="openNew">
        <MIcon name="plus" />
        Create team
      </button>
    </div>

    <!-- New team sheet -->
    <div class="scrim" :class="{ open: sheet }" @click="closeSheets"></div>

    <aside
      class="sheet"
      :class="{ open: sheet === 'team-new' }"
      :aria-hidden="sheet !== 'team-new'"
      :inert="sheet !== 'team-new'"
      @keydown.escape.stop="closeSheets"
    >
      <header class="sheet-head">
        <div>
          <h3>New team</h3>
          <div class="sub">Invite people and share selected directories with them.</div>
        </div>
        <span class="spacer"></span>
        <button class="icon-btn" type="button" title="Close" @click="closeSheets">
          <MIcon name="x" />
        </button>
      </header>

      <form class="sheet-body" id="new-team-form" @submit.prevent="createTeam">
        <div class="field">
          <label class="field-label">Name<span class="req">*</span></label>
          <input class="input" v-model="teamForm.name" required placeholder="Family memory" />
        </div>
        <div class="field">
          <label class="field-label">Slug</label>
          <input class="input" v-model="teamForm.slug" placeholder="family-memory" />
        </div>
        <span class="err" v-if="teamForm.err" v-text="teamForm.err"></span>
      </form>

      <footer class="sheet-foot">
        <span class="spacer"></span>
        <button class="btn ghost" type="button" @click="closeSheets">Cancel</button>
        <button
          class="btn primary"
          type="submit"
          form="new-team-form"
          :disabled="teamForm.submitting || !teamForm.name"
        >
          Create team
        </button>
      </footer>
    </aside>

    <!-- Manage team sheet -->
    <TeamManageSheet
      :team="activeTeam"
      :open="sheet === 'team'"
      @close="closeSheets"
      @changed="reload"
      @deleted="onTeamDeleted"
    />
  </section>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from "vue";
import { teams as teamsApi, ApiError } from "@/shared/api";
import { toast } from "@/shared/bus";
import MIcon from "@/shared/components/MIcon.vue";
import TeamManageSheet from "./TeamManageSheet.vue";
import type { Team } from "@/shared/types";

const teams = ref<Team[]>([]);
const loading = ref(true);
const loadErr = ref("");

const sheet = ref<"" | "team-new" | "team">("");
const activeTeam = ref<Team | null>(null);

const teamForm = reactive({
  name: "",
  slug: "",
  err: "",
  submitting: false,
});

function roleLabel(role?: string): string {
  return role || "member";
}

async function load(): Promise<void> {
  loading.value = true;
  loadErr.value = "";
  try {
    const data = await teamsApi.list();
    teams.value = data.teams || [];
  } catch (error) {
    loadErr.value = error instanceof ApiError ? error.message : "failed to load teams";
  } finally {
    loading.value = false;
  }
}

// Reload the list and keep the open manage sheet pointed at the fresh team
// object (so role/permission changes propagate into the panel).
async function reload(): Promise<void> {
  await load();
  if (activeTeam.value) {
    const fresh = teams.value.find((t) => t.id === activeTeam.value!.id);
    activeTeam.value = fresh ?? null;
    if (!fresh) sheet.value = "";
  }
}

function openNew(): void {
  teamForm.name = "";
  teamForm.slug = "";
  teamForm.err = "";
  teamForm.submitting = false;
  sheet.value = "team-new";
}

function openTeam(team: Team): void {
  activeTeam.value = team;
  sheet.value = "team";
}

function closeSheets(): void {
  sheet.value = "";
}

async function createTeam(): Promise<void> {
  teamForm.err = "";
  teamForm.submitting = true;
  try {
    const data = await teamsApi.create({ name: teamForm.name, slug: teamForm.slug });
    closeSheets();
    await load();
    // Land in the new team's management sheet so the next step — inviting
    // people — is right in front of the user.
    const created = data.team && teams.value.find((t) => t.id === data.team.id);
    if (created) openTeam(created);
  } catch (error) {
    teamForm.err = error instanceof ApiError ? error.message : "create failed";
  } finally {
    teamForm.submitting = false;
  }
}

function onTeamDeleted(team: Team): void {
  toast(`Deleted ${team.name}`, "success");
  closeSheets();
  activeTeam.value = null;
  void load();
}

onMounted(load);
</script>
