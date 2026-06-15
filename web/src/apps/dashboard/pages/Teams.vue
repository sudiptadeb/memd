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
      <router-link
        class="card team-card"
        v-for="team in teams"
        :key="team.id"
        :to="`/teams/${team.id}`"
      >
        <div class="card-head">
          <div class="card-name" v-text="team.name"></div>
          <span class="dot accent" v-text="roleLabel(team.role)"></span>
          <span class="spacer"></span>
          <span class="manage-link">
            Manage
            <MIcon name="chevron-right" />
          </span>
        </div>
        <div class="card-meta" v-if="team.slug">@<span v-text="team.slug"></span></div>
      </router-link>
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

    <!-- New team dialog -->
    <div class="modal-scrim" :class="{ open: showNew }" :inert="!showNew" @click="closeNew">
      <div class="modal" @click.stop>
        <header class="modal-head">
          <h3>New team</h3>
          <div class="sub">Invite people and share selected directories with them.</div>
        </header>
        <form class="modal-body" id="new-team-form" @submit.prevent="createTeam">
          <div class="field">
            <label class="field-label" for="new-team-name">Name<span class="req">*</span></label>
            <input
              id="new-team-name"
              class="input"
              v-model="teamForm.name"
              required
              placeholder="Family memory"
            />
          </div>
          <div class="field">
            <label class="field-label" for="new-team-slug">Slug</label>
            <input
              id="new-team-slug"
              class="input"
              v-model="teamForm.slug"
              placeholder="family-memory"
            />
            <span class="field-hint">Optional. A short, URL-friendly handle for the team.</span>
          </div>
          <span class="err" v-if="teamForm.err" v-text="teamForm.err"></span>
        </form>
        <footer class="modal-foot">
          <span class="spacer"></span>
          <button class="btn ghost" type="button" @click="closeNew">Cancel</button>
          <button
            class="btn primary"
            type="submit"
            form="new-team-form"
            :disabled="teamForm.submitting || !teamForm.name"
          >
            Create team
          </button>
        </footer>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from "vue";
import { useRouter } from "vue-router";
import { teams as teamsApi, ApiError } from "@/shared/api";
import MIcon from "@/shared/components/MIcon.vue";
import type { Team } from "@/shared/types";

const router = useRouter();

const teams = ref<Team[]>([]);
const loading = ref(true);
const loadErr = ref("");

const showNew = ref(false);

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

function openNew(): void {
  teamForm.name = "";
  teamForm.slug = "";
  teamForm.err = "";
  teamForm.submitting = false;
  showNew.value = true;
}

function closeNew(): void {
  showNew.value = false;
}

async function createTeam(): Promise<void> {
  teamForm.err = "";
  teamForm.submitting = true;
  try {
    const data = await teamsApi.create({ name: teamForm.name, slug: teamForm.slug });
    closeNew();
    // Land on the new team's detail page so the next step — inviting people —
    // is right in front of the user.
    if (data.team) {
      await router.push(`/teams/${data.team.id}`);
    } else {
      await load();
    }
  } catch (error) {
    teamForm.err = error instanceof ApiError ? error.message : "create failed";
  } finally {
    teamForm.submitting = false;
  }
}

onMounted(load);
</script>

<style scoped>
.team-card {
  cursor: pointer;
}

.team-card:hover .manage-link {
  color: var(--fg-1);
}

.manage-link {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  color: var(--fg-3);
  font-size: 12.5px;
  font-weight: 650;
  white-space: nowrap;
  transition: color var(--dur-fast);
}

.manage-link .icon {
  width: 14px;
  height: 14px;
}

.modal-body {
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding: 16px 18px;
  overflow-y: auto;
}
</style>
