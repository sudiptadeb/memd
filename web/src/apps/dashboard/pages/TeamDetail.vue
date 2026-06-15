<template>
  <section class="app-section">
    <!-- Loading -->
    <div class="detail-loading" v-if="loading">
      <MIcon name="refresh-cw" />
      <span>Loading team…</span>
    </div>

    <!-- Not found / load error -->
    <div class="empty" v-else-if="!team">
      <div class="empty-icon"><MIcon name="users" /></div>
      <h4>Team not found</h4>
      <p v-text="loadErr || 'This team may have been deleted, or you no longer have access.'"></p>
      <router-link class="btn secondary" to="/teams">
        <MIcon name="arrow-left" />
        Back to teams
      </router-link>
    </div>

    <template v-else>
      <!-- Header chrome -->
      <div class="detail-head">
        <router-link class="detail-back" to="/teams" title="Back to teams">
          <MIcon name="arrow-left" />
        </router-link>
        <div class="detail-titles">
          <h1 class="detail-title" v-text="team.name"></h1>
          <p class="detail-sub">
            <span v-text="roleLabel(team.role)"></span>
            <template v-if="team.slug"> · @<span v-text="team.slug"></span></template>
          </p>
        </div>
        <div class="detail-actions">
          <button
            class="btn secondary"
            type="button"
            v-if="team.can_manage"
            @click="openRename"
          >
            <MIcon name="pencil" />
            <span class="btn-label">Rename</span>
          </button>
          <button
            class="btn danger"
            type="button"
            v-if="team.can_delete"
            @click="removeTeam"
          >
            <MIcon name="trash-2" />
            <span class="btn-label">Delete team</span>
          </button>
        </div>
      </div>

      <!-- Members -->
      <div class="detail-block">
        <div class="section-head">
          <div class="titles">
            <h2>Members <span class="count" v-text="members.length"></span></h2>
            <span class="desc">People who can reach this team's shared directories.</span>
          </div>
        </div>

        <div class="mtable-wrap" v-if="members.length">
          <table class="mtable mtable-stack">
            <thead>
              <tr>
                <th>Member</th>
                <th class="shrink">Role</th>
                <th class="shrink" v-if="team.can_manage"></th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="member in members" :key="member.user_id">
                <td data-label="Member">
                  <div class="member-cell">
                    <span class="cell-strong" v-text="member.display_name || member.username"></span>
                    <span class="cell-muted">@<span v-text="member.username"></span></span>
                  </div>
                </td>
                <td class="shrink" data-label="Role">
                  <select
                    class="mini-select"
                    aria-label="Change member role"
                    v-if="team.can_manage && member.role !== 'owner'"
                    :value="member.role"
                    @change="updateMemberRole(member, ($event.target as HTMLSelectElement).value)"
                  >
                    <option value="admin">admin</option>
                    <option value="member">member</option>
                    <option value="viewer">viewer</option>
                  </select>
                  <span class="cell-muted" v-else v-text="member.role"></span>
                </td>
                <td class="shrink" data-label="" v-if="team.can_manage">
                  <button
                    class="icon-btn danger"
                    type="button"
                    v-if="member.user_id !== currentUserId && member.role !== 'owner'"
                    title="Remove member"
                    @click="removeMember(member)"
                  >
                    <MIcon name="trash-2" />
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>

        <!-- Add member -->
        <form class="add-row" v-if="team.can_manage" @submit.prevent="addMember">
          <input
            class="input"
            aria-label="Username to add"
            v-model="memberForm.username"
            placeholder="username"
          />
          <select class="input compact-input" aria-label="Member role" v-model="memberForm.role">
            <option value="admin">admin</option>
            <option value="member">member</option>
            <option value="viewer">viewer</option>
          </select>
          <button
            class="btn secondary"
            type="submit"
            :disabled="memberForm.submitting || !memberForm.username"
          >
            <MIcon name="plus" />
            Add
          </button>
        </form>
        <span class="err" v-if="memberForm.err" v-text="memberForm.err"></span>
      </div>

      <!-- Invite links -->
      <div class="detail-block" v-if="team.can_manage">
        <div class="section-head">
          <div class="titles">
            <h2>Invite links <span class="count" v-text="invitesList.length"></span></h2>
            <span class="desc">Share a link so people can join without an explicit add.</span>
          </div>
        </div>

        <form class="invite-form" @submit.prevent="createInvite">
          <select class="input compact-input" aria-label="Invite role" v-model="inviteForm.role">
            <option value="admin">admin</option>
            <option value="member">member</option>
            <option value="viewer">viewer</option>
          </select>
          <input
            class="input"
            type="datetime-local"
            aria-label="Invite expiry"
            v-model="inviteForm.expires_at"
          />
          <input
            class="input compact-input"
            type="number"
            min="1"
            aria-label="Max uses"
            v-model="inviteForm.max_uses"
            placeholder="Max uses"
          />
          <button class="btn secondary" type="submit" :disabled="inviteForm.submitting">
            <MIcon name="plus" />
            Create
          </button>
        </form>

        <div class="created-link" v-if="createdInviteURL">
          <MIcon name="check" />
          <code v-text="createdInviteURL"></code>
          <span class="spacer"></span>
          <button class="btn ghost" type="button" title="Copy invite link" @click="copyInvite">
            <MIcon name="copy" />
            Copy
          </button>
        </div>
        <span class="err" v-if="inviteForm.err" v-text="inviteForm.err"></span>

        <div class="mtable-wrap" v-if="invitesList.length">
          <table class="mtable mtable-stack">
            <thead>
              <tr>
                <th class="shrink">Role</th>
                <th class="shrink num">Uses</th>
                <th>Expires</th>
                <th class="shrink">Status</th>
                <th class="shrink"></th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="invite in invitesList" :key="invite.id">
                <td class="shrink" data-label="Role">
                  <span class="cell-strong" v-text="invite.role"></span>
                </td>
                <td class="shrink num" data-label="Uses" v-text="inviteUsage(invite)"></td>
                <td data-label="Expires">
                  <span class="cell-muted" v-text="inviteExpiry(invite)"></span>
                </td>
                <td class="shrink" data-label="Status">
                  <span class="dot" :class="statusClass(invite)" v-text="inviteStatus(invite)"></span>
                </td>
                <td class="shrink" data-label="">
                  <button
                    class="btn ghost"
                    type="button"
                    v-if="isActive(invite)"
                    @click="revokeInvite(invite)"
                  >
                    Revoke
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <p class="empty-note" v-else>No invite links yet.</p>
      </div>
    </template>

    <!-- Rename dialog -->
    <div class="modal-scrim" :class="{ open: showRename }" :inert="!showRename" @click="closeRename">
      <div class="modal" @click.stop>
        <header class="modal-head">
          <h3>Rename team</h3>
        </header>
        <form class="modal-body" id="rename-team-form" @submit.prevent="renameTeam">
          <div class="field">
            <label class="field-label" for="rename-team-name">Name<span class="req">*</span></label>
            <input id="rename-team-name" class="input" v-model="renameForm.name" required />
          </div>
          <div class="field">
            <label class="field-label" for="rename-team-slug">Slug</label>
            <input id="rename-team-slug" class="input" v-model="renameForm.slug" placeholder="family-memory" />
            <span class="field-hint">Optional. A short, URL-friendly handle for the team.</span>
          </div>
          <span class="err" v-if="renameForm.err" v-text="renameForm.err"></span>
        </form>
        <footer class="modal-foot">
          <span class="spacer"></span>
          <button class="btn ghost" type="button" @click="closeRename">Cancel</button>
          <button
            class="btn primary"
            type="submit"
            form="rename-team-form"
            :disabled="renameForm.submitting || !renameForm.name"
          >
            Save
          </button>
        </footer>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { teams as teamsApi, ApiError } from "@/shared/api";
import { toast } from "@/shared/bus";
import { useSession } from "@/shared/session";
import { copyToClipboard, formatDate } from "@/shared/utils";
import { publicURL } from "../components/connUrls";
import MIcon from "@/shared/components/MIcon.vue";
import type { Team, TeamMember, TeamInvite, InviteRole, TeamRole } from "@/shared/types";

const route = useRoute();
const router = useRouter();

const teamId = computed(() => String(route.params.teamId ?? ""));

const { user } = useSession();
const currentUserId = computed(() => user.value?.id ?? "");

const team = ref<Team | null>(null);
const members = ref<TeamMember[]>([]);
const invitesList = ref<TeamInvite[]>([]);
const loading = ref(true);
const loadErr = ref("");
const createdInviteURL = ref("");

const showRename = ref(false);

const memberForm = reactive({
  username: "",
  role: "member" as TeamRole,
  err: "",
  submitting: false,
});

const inviteForm = reactive({
  role: "member" as InviteRole,
  expires_at: "",
  max_uses: "",
  err: "",
  submitting: false,
});

const renameForm = reactive({
  name: "",
  slug: "",
  err: "",
  submitting: false,
});

function roleLabel(role?: string): string {
  return role || "member";
}

function inviteUsage(invite: TeamInvite): string {
  const limit = invite.max_uses ? String(invite.max_uses) : "∞";
  return `${invite.use_count || 0} / ${limit}`;
}

function inviteExpiry(invite: TeamInvite): string {
  if (!invite.expires_at) return "No expiry";
  return formatDate(invite.expires_at);
}

function isExpired(invite: TeamInvite): boolean {
  if (!invite.expires_at) return false;
  const t = new Date(invite.expires_at).getTime();
  return Number.isFinite(t) && t <= Date.now();
}

function isActive(invite: TeamInvite): boolean {
  return !invite.revoked_at && !isExpired(invite);
}

function inviteStatus(invite: TeamInvite): string {
  if (invite.revoked_at) return "revoked";
  if (isExpired(invite)) return "expired";
  return "active";
}

function statusClass(invite: TeamInvite): string {
  if (invite.revoked_at || isExpired(invite)) return "danger";
  return "success";
}

async function load(): Promise<void> {
  const id = teamId.value;
  if (!id) {
    loading.value = false;
    team.value = null;
    return;
  }
  loading.value = true;
  loadErr.value = "";
  try {
    const res = await teamsApi.get(id);
    team.value = res.team;
    await loadDetail(id);
  } catch (error) {
    team.value = null;
    loadErr.value = error instanceof ApiError ? error.message : "failed to load team";
  } finally {
    loading.value = false;
  }
}

async function loadDetail(id: string): Promise<void> {
  try {
    const memberRes = await teamsApi.members.list(id);
    members.value = memberRes.members || [];
  } catch (error) {
    toast(error instanceof ApiError ? error.message : "failed to load members", "error");
  }
  // Invites are manager-only; skip the call (and any 403 noise) otherwise.
  if (team.value?.can_manage) {
    try {
      const inviteRes = await teamsApi.invites.list(id);
      invitesList.value = inviteRes.invites || [];
    } catch (error) {
      toast(error instanceof ApiError ? error.message : "failed to load invites", "error");
    }
  } else {
    invitesList.value = [];
  }
}

async function addMember(): Promise<void> {
  if (!team.value) return;
  memberForm.err = "";
  memberForm.submitting = true;
  try {
    await teamsApi.members.add(team.value.id, {
      username: memberForm.username,
      role: memberForm.role,
    });
    memberForm.username = "";
    memberForm.role = "member";
    await loadDetail(team.value.id);
  } catch (error) {
    memberForm.err = error instanceof ApiError ? error.message : "add failed";
  } finally {
    memberForm.submitting = false;
  }
}

async function updateMemberRole(member: TeamMember, role: string): Promise<void> {
  if (!team.value) return;
  try {
    await teamsApi.members.setRole(team.value.id, member.user_id, { role: role as TeamRole });
    await loadDetail(team.value.id);
  } catch (error) {
    toast(error instanceof ApiError ? error.message : "update failed", "error");
    // Re-sync so the <select> snaps back to the server's truth on failure.
    await loadDetail(team.value.id);
  }
}

async function removeMember(member: TeamMember): Promise<void> {
  if (!team.value) return;
  if (!window.confirm(`Remove ${member.username} from ${team.value.name}?`)) return;
  try {
    await teamsApi.members.remove(team.value.id, member.user_id);
    await loadDetail(team.value.id);
  } catch (error) {
    toast(error instanceof ApiError ? error.message : "remove failed", "error");
  }
}

async function createInvite(): Promise<void> {
  if (!team.value) return;
  inviteForm.err = "";
  inviteForm.submitting = true;
  createdInviteURL.value = "";
  let expiresAt = "";
  if (inviteForm.expires_at) {
    expiresAt = new Date(inviteForm.expires_at).toISOString();
  }
  const maxUses = parseInt(inviteForm.max_uses, 10);
  try {
    const data = await teamsApi.invites.create(team.value.id, {
      role: inviteForm.role,
      expires_at: expiresAt,
      max_uses: Number.isFinite(maxUses) && maxUses > 0 ? maxUses : null,
    });
    createdInviteURL.value = publicURL(data.invite_url || "");
    inviteForm.role = "member";
    inviteForm.expires_at = "";
    inviteForm.max_uses = "";
    await loadDetail(team.value.id);
    if (createdInviteURL.value) {
      const ok = await copyToClipboard(createdInviteURL.value);
      toast(ok ? "Invite copied" : "Invite created", ok ? "success" : "info");
    }
  } catch (error) {
    inviteForm.err = error instanceof ApiError ? error.message : "invite failed";
  } finally {
    inviteForm.submitting = false;
  }
}

async function revokeInvite(invite: TeamInvite): Promise<void> {
  if (!team.value) return;
  try {
    await teamsApi.invites.revoke(team.value.id, invite.id);
    await loadDetail(team.value.id);
  } catch (error) {
    toast(error instanceof ApiError ? error.message : "revoke failed", "error");
  }
}

async function copyInvite(): Promise<void> {
  if (!createdInviteURL.value) return;
  const ok = await copyToClipboard(createdInviteURL.value);
  toast(ok ? "Invite copied" : "Copy failed", ok ? "success" : "error");
}

function openRename(): void {
  if (!team.value) return;
  renameForm.name = team.value.name;
  renameForm.slug = team.value.slug;
  renameForm.err = "";
  renameForm.submitting = false;
  showRename.value = true;
}

function closeRename(): void {
  showRename.value = false;
}

async function renameTeam(): Promise<void> {
  if (!team.value) return;
  renameForm.err = "";
  renameForm.submitting = true;
  try {
    const res = await teamsApi.update(team.value.id, {
      name: renameForm.name,
      slug: renameForm.slug,
    });
    team.value = res.team;
    closeRename();
  } catch (error) {
    renameForm.err = error instanceof ApiError ? error.message : "rename failed";
  } finally {
    renameForm.submitting = false;
  }
}

async function removeTeam(): Promise<void> {
  if (!team.value) return;
  if (
    !window.confirm(
      `Delete team ${team.value.name}? Team-scoped directories and connectors become personal to their original owners.`,
    )
  ) {
    return;
  }
  try {
    await teamsApi.remove(team.value.id);
    await router.push("/teams");
  } catch (error) {
    toast(error instanceof ApiError ? error.message : "delete failed", "error");
  }
}

// Reload when navigating between team detail pages (the component is reused).
watch(teamId, () => {
  createdInviteURL.value = "";
  void load();
});

onMounted(load);
</script>

<style scoped>
/* The detail page reads better as a capped column than stretched across a wide
   monitor — the members/invites tables otherwise spread their columns far apart. */
.app-section {
  max-width: 880px;
}

.detail-loading {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 24px 0;
  color: var(--fg-3);
  font-size: 13px;
}

.detail-loading .icon {
  width: 15px;
  height: 15px;
  animation: detail-spin 0.9s linear infinite;
}

@keyframes detail-spin {
  to {
    transform: rotate(360deg);
  }
}

.detail-block {
  margin-top: 1.6rem;
}

.detail-block .section-head {
  margin-bottom: 0.85rem;
}

.member-cell {
  display: flex;
  flex-direction: column;
  gap: 1px;
  min-width: 0;
}

.member-cell .cell-muted {
  font-size: 0.8rem;
}

/* Row of inputs shared by the add-member and create-invite forms. */
.add-row,
.invite-form {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 0.75rem;
}

.add-row .input,
.invite-form .input {
  min-width: 0;
}

.add-row > .input:first-child {
  flex: 1 1 12rem;
}

.invite-form > input[type="datetime-local"] {
  flex: 1 1 12rem;
}

.created-link {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 0.75rem;
  padding: 8px 12px;
  background: var(--accent-soft);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
}

.created-link .icon {
  flex-shrink: 0;
  width: 14px;
  height: 14px;
  color: var(--success);
}

.created-link code {
  overflow: hidden;
  color: var(--fg-1);
  font-family: var(--font-mono);
  font-size: 12px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.modal-body {
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding: 16px 18px;
  overflow-y: auto;
}

@media (max-width: 640px) {
  .add-row,
  .invite-form {
    flex-direction: column;
    align-items: stretch;
  }

  /* In a stacked column the desktop `flex: 1 1 12rem` would turn 12rem into a
     row HEIGHT, ballooning these inputs — reset to their natural height. */
  .add-row > .input:first-child,
  .invite-form > input[type="datetime-local"] {
    flex: 0 0 auto;
  }

  .add-row .btn,
  .invite-form .btn {
    justify-content: center;
  }
}
</style>
