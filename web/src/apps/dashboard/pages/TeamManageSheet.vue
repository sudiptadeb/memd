<template>
  <aside
    class="sheet wide-sheet"
    :class="{ open }"
    :aria-hidden="!open"
    :inert="!open"
    @keydown.escape.stop="emitClose"
  >
    <header class="sheet-head">
      <div>
        <h3 v-text="team ? team.name : 'Team'"></h3>
        <div class="sub" v-text="team ? roleLabel(team.role) : ''"></div>
      </div>
      <span class="spacer"></span>
      <button
        class="btn danger"
        type="button"
        v-if="team && team.can_delete"
        @click="removeTeam"
      >
        Delete team
      </button>
      <button class="icon-btn" type="button" title="Close" @click="emitClose">
        <MIcon name="x" />
      </button>
    </header>

    <div class="sheet-body" v-if="team">
      <div class="team-panel">
        <div class="panel-title">Members</div>
        <div class="member-list">
          <div class="member-row" v-for="member in members" :key="member.user_id">
            <div>
              <div class="label" v-text="member.display_name || member.username"></div>
              <div class="sub" v-text="member.username"></div>
            </div>
            <span class="spacer"></span>
            <select
              class="mini-select"
              aria-label="Change member role"
              v-if="team.can_manage"
              :value="member.role"
              @change="updateMemberRole(member, ($event.target as HTMLSelectElement).value)"
            >
              <option value="owner">owner</option>
              <option value="admin">admin</option>
              <option value="member">member</option>
              <option value="viewer">viewer</option>
            </select>
            <span class="dot" v-else v-text="member.role"></span>
            <button
              class="icon-btn danger"
              type="button"
              v-if="team.can_manage && member.user_id !== currentUserId"
              title="Remove member"
              @click="removeMember(member)"
            >
              <MIcon name="trash-2" />
            </button>
          </div>
        </div>

        <form class="inline-form" v-if="team.can_manage" @submit.prevent="addMember">
          <input
            class="input"
            aria-label="Username to add"
            v-model="memberForm.username"
            placeholder="username"
          />
          <select class="input compact-input" aria-label="Member role" v-model="memberForm.role">
            <option value="member">member</option>
            <option value="viewer">viewer</option>
            <option value="admin">admin</option>
          </select>
          <button
            class="btn secondary"
            type="submit"
            :disabled="memberForm.submitting || !memberForm.username"
          >
            Add
          </button>
        </form>
        <span class="err" v-if="memberForm.err" v-text="memberForm.err"></span>
      </div>

      <div class="team-panel" v-if="team.can_manage">
        <div class="panel-title">Invite links</div>
        <form class="invite-grid" @submit.prevent="createInvite">
          <select class="input" aria-label="Invite role" v-model="inviteForm.role">
            <option value="member">member</option>
            <option value="viewer">viewer</option>
            <option value="admin" v-if="team.can_delete">admin</option>
          </select>
          <input
            class="input"
            type="datetime-local"
            aria-label="Invite expiry"
            v-model="inviteForm.expires_at"
          />
          <input
            class="input"
            type="number"
            min="1"
            aria-label="Max uses"
            v-model="inviteForm.max_uses"
            placeholder="Max uses"
          />
          <button class="btn secondary" type="submit" :disabled="inviteForm.submitting">
            Create
          </button>
        </form>
        <div class="created-link" v-if="createdInviteURL">
          <code v-text="createdInviteURL"></code>
          <button class="btn ghost" type="button" @click="copyInvite">Copy</button>
        </div>
        <span class="err" v-if="inviteForm.err" v-text="inviteForm.err"></span>
        <div class="invite-list">
          <div
            class="invite-row"
            v-for="invite in invitesList"
            :key="invite.id"
            :class="{ 'muted-card': invite.revoked_at }"
          >
            <span class="dot" v-text="invite.role"></span>
            <span class="sub" v-text="inviteUsage(invite)"></span>
            <span class="sub" v-text="inviteExpiry(invite)"></span>
            <span class="spacer"></span>
            <button
              class="btn ghost"
              type="button"
              v-if="!invite.revoked_at"
              @click="revokeInvite(invite)"
            >
              Revoke
            </button>
          </div>
        </div>
      </div>
    </div>
  </aside>
</template>

<script setup lang="ts">
import { reactive, ref, watch } from "vue";
import { teams as teamsApi, ApiError } from "@/shared/api";
import { useSession } from "@/shared/session";
import { toast } from "@/shared/bus";
import { copyToClipboard } from "@/shared/utils";
import MIcon from "@/shared/components/MIcon.vue";
import type { Team, TeamMember, TeamInvite, InviteRole, TeamRole } from "@/shared/types";

const props = defineProps<{
  team: Team | null;
  open: boolean;
}>();

const emit = defineEmits<{
  (e: "close"): void;
  (e: "changed"): void;
  (e: "deleted", team: Team): void;
}>();

const { user } = useSession();
const currentUserId = ref(user.value?.id ?? "");
watch(user, (u) => (currentUserId.value = u?.id ?? ""));

const members = ref<TeamMember[]>([]);
const invitesList = ref<TeamInvite[]>([]);
const createdInviteURL = ref("");

const memberForm = reactive({
  username: "",
  role: "member" as TeamRole,
  err: "",
  submitting: false,
});

const inviteForm = reactive({
  role: "member" as InviteRole,
  expires_at: "",
  max_uses: "" as string,
  err: "",
  submitting: false,
});

function resetForms(): void {
  memberForm.username = "";
  memberForm.role = "member";
  memberForm.err = "";
  memberForm.submitting = false;
  inviteForm.role = "member";
  inviteForm.expires_at = "";
  inviteForm.max_uses = "";
  inviteForm.err = "";
  inviteForm.submitting = false;
  createdInviteURL.value = "";
}

function roleLabel(role?: string): string {
  return role || "member";
}

function inviteUsage(invite: TeamInvite): string {
  const limit = invite.max_uses ? String(invite.max_uses) : "unlimited";
  return `${invite.use_count || 0} / ${limit}`;
}

function inviteExpiry(invite: TeamInvite): string {
  if (!invite.expires_at) return "No expiry";
  return new Date(invite.expires_at).toLocaleString();
}

// The server builds invite URLs against its 127.0.0.1 bind; rewrite them to the
// public origin the user is actually browsing.
function publicURL(rawURL: string): string {
  try {
    const url = new URL(rawURL, window.location.origin);
    return window.location.origin + url.pathname + url.search + url.hash;
  } catch {
    return rawURL || "";
  }
}

async function loadDetail(teamId: string): Promise<void> {
  try {
    const [memberRes, inviteRes] = await Promise.all([
      teamsApi.members.list(teamId),
      teamsApi.invites.list(teamId).catch(() => ({ invites: [] as TeamInvite[] })),
    ]);
    members.value = memberRes.members || [];
    invitesList.value = inviteRes.invites || [];
  } catch (error) {
    memberForm.err = error instanceof ApiError ? error.message : "load failed";
  }
}

async function addMember(): Promise<void> {
  if (!props.team) return;
  memberForm.err = "";
  memberForm.submitting = true;
  try {
    await teamsApi.members.add(props.team.id, {
      username: memberForm.username,
      role: memberForm.role,
    });
    memberForm.username = "";
    memberForm.role = "member";
    await loadDetail(props.team.id);
  } catch (error) {
    memberForm.err = error instanceof ApiError ? error.message : "add failed";
  } finally {
    memberForm.submitting = false;
  }
}

async function updateMemberRole(member: TeamMember, role: string): Promise<void> {
  if (!props.team) return;
  try {
    await teamsApi.members.setRole(props.team.id, member.user_id, { role: role as TeamRole });
    await loadDetail(props.team.id);
    emit("changed");
  } catch (error) {
    toast(error instanceof ApiError ? error.message : "update failed", "error");
    await loadDetail(props.team.id);
  }
}

async function removeMember(member: TeamMember): Promise<void> {
  if (!props.team) return;
  if (!window.confirm(`Remove ${member.username} from ${props.team.name}?`)) return;
  try {
    await teamsApi.members.remove(props.team.id, member.user_id);
    await loadDetail(props.team.id);
    emit("changed");
  } catch (error) {
    toast(error instanceof ApiError ? error.message : "remove failed", "error");
  }
}

async function createInvite(): Promise<void> {
  if (!props.team) return;
  inviteForm.err = "";
  inviteForm.submitting = true;
  createdInviteURL.value = "";
  let expiresAt = "";
  if (inviteForm.expires_at) {
    expiresAt = new Date(inviteForm.expires_at).toISOString();
  }
  const maxUses = parseInt(inviteForm.max_uses, 10);
  try {
    const data = await teamsApi.invites.create(props.team.id, {
      role: inviteForm.role,
      expires_at: expiresAt,
      max_uses: Number.isFinite(maxUses) && maxUses > 0 ? maxUses : null,
    });
    createdInviteURL.value = publicURL(data.invite_url || "");
    inviteForm.role = "member";
    inviteForm.expires_at = "";
    inviteForm.max_uses = "";
    await loadDetail(props.team.id);
    if (createdInviteURL.value) {
      await copyToClipboard(createdInviteURL.value);
      toast("Invite copied", "success");
    }
  } catch (error) {
    inviteForm.err = error instanceof ApiError ? error.message : "invite failed";
  } finally {
    inviteForm.submitting = false;
  }
}

async function revokeInvite(invite: TeamInvite): Promise<void> {
  if (!props.team) return;
  try {
    await teamsApi.invites.revoke(props.team.id, invite.id);
    await loadDetail(props.team.id);
  } catch (error) {
    toast(error instanceof ApiError ? error.message : "revoke failed", "error");
  }
}

async function copyInvite(): Promise<void> {
  if (!createdInviteURL.value) return;
  const ok = await copyToClipboard(createdInviteURL.value);
  toast(ok ? "Invite copied" : "Copy failed", ok ? "success" : "error");
}

async function removeTeam(): Promise<void> {
  if (!props.team) return;
  if (
    !window.confirm(
      `Delete team ${props.team.name}? Team-scoped directories and connectors become personal to their original owners.`,
    )
  ) {
    return;
  }
  const team = props.team;
  try {
    await teamsApi.remove(team.id);
    emit("deleted", team);
  } catch (error) {
    toast(error instanceof ApiError ? error.message : "delete failed", "error");
  }
}

function emitClose(): void {
  emit("close");
}

// Load member/invite detail whenever a team is opened.
watch(
  () => (props.open ? props.team?.id : null),
  (id) => {
    if (id) {
      resetForms();
      void loadDetail(id);
    } else {
      members.value = [];
      invitesList.value = [];
    }
  },
  { immediate: true },
);
</script>
