<template>
  <section class="admin-section">
    <div class="section-head">
      <div class="titles">
        <span class="step">Super admin</span>
        <h2>Users <span class="count">{{ users.length }}</span></h2>
        <span class="desc">
          Create local backup accounts, disable access, and reset passwords. SSO users are
          provisioned automatically on first login.
        </span>
      </div>
      <span class="spacer"></span>
      <button class="btn secondary" type="button" @click="load">
        <MIcon name="refresh-cw" />
        Refresh
      </button>
      <button class="btn primary" type="button" @click="openSheet">
        <MIcon name="plus" />
        Add user
      </button>
    </div>

    <div class="empty load-state" v-if="loading">
      <div class="empty-icon"><MIcon name="activity" /></div>
      <h4>Loading users</h4>
    </div>

    <div class="empty error-state" v-else-if="loadErr">
      <div class="empty-icon"><MIcon name="triangle-alert" /></div>
      <h4>Could not load users</h4>
      <p>{{ loadErr }}</p>
    </div>

    <div class="cards user-cards" v-else>
      <article
        v-for="target in users"
        :key="target.id"
        class="card user-card"
        :class="target.disabled ? 'muted-card' : ''"
      >
        <div class="card-head">
          <div class="user-avatar">{{ avatarInitial(target) }}</div>
          <div class="user-main">
            <div class="card-name">{{ displayName(target) }}</div>
            <div class="card-meta">{{ target.username }}</div>
          </div>
          <span class="dot accent" v-if="target.super_admin">super admin</span>
          <span class="dot" v-if="target.sso_linked" :title="target.issuer">SSO</span>
          <span
            class="dot danger"
            v-if="target.sso_orphan"
            :title="`Signed up via ${target.issuer}, which is no longer configured`"
            >SSO orphan</span
          >
          <span class="dot danger" v-if="target.disabled">disabled</span>
          <span class="spacer"></span>
          <button
            class="btn ghost"
            type="button"
            v-if="target.sso_linked || target.sso_orphan"
            @click="unlinkSSO(target)"
          >
            Unlink SSO
          </button>
          <button class="btn ghost" type="button" @click="resetPassword(target)">
            Reset password
          </button>
          <button
            class="btn"
            :class="target.disabled ? 'secondary' : 'danger'"
            type="button"
            @click="toggleDisabled(target)"
          >
            {{ target.disabled ? "Enable" : "Disable" }}
          </button>
        </div>
        <div class="user-meta-grid">
          <span>Created</span>
          <code>{{ formatDate(target.created_at) }}</code>
          <span>Last login</span>
          <code>{{ target.last_login_at ? formatDate(target.last_login_at) : "never" }}</code>
        </div>
      </article>
    </div>
  </section>

  <!-- Add-user slide-out sheet (the create form). -->
  <div class="scrim" :class="sheetOpen ? 'open' : ''" @click="closeSheet"></div>

  <aside
    class="sheet"
    :class="sheetOpen ? 'open' : ''"
    :aria-hidden="!sheetOpen"
    @keydown.escape.stop="closeSheet"
  >
    <header class="sheet-head">
      <div>
        <h3>Add user</h3>
        <div class="sub">Create a regular local login account.</div>
      </div>
      <span class="spacer"></span>
      <button class="icon-btn" type="button" @click="closeSheet" title="Close">
        <MIcon name="x" />
      </button>
    </header>

    <form class="sheet-body" id="add-user-form" @submit.prevent="createUser">
      <div class="field">
        <label class="field-label">Username<span class="req">*</span></label>
        <input class="input" v-model="userForm.username" required autocomplete="off" />
      </div>
      <div class="field">
        <label class="field-label">Display name</label>
        <input class="input" v-model="userForm.display_name" autocomplete="off" />
      </div>
      <div class="field">
        <label class="field-label">Temporary password<span class="req">*</span></label>
        <input
          class="input"
          type="password"
          v-model="userForm.password"
          required
          autocomplete="new-password"
        />
      </div>
      <span class="err" v-if="userForm.err">{{ userForm.err }}</span>
    </form>

    <footer class="sheet-foot">
      <span class="spacer"></span>
      <button class="btn ghost" type="button" @click="closeSheet">Cancel</button>
      <button
        class="btn primary"
        type="submit"
        form="add-user-form"
        :disabled="userForm.submitting || !userForm.username || !userForm.password"
      >
        Create user
      </button>
    </footer>
  </aside>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from "vue";
import { admin, ApiError } from "@/shared/api";
import type { AdminUser } from "@/shared/types";
import { toast } from "@/shared/bus";
import { formatDate } from "@/shared/utils";
import MIcon from "@/shared/components/MIcon.vue";

// Super-admin user management: list accounts, create local logins, enable/disable
// access, reset passwords, and unlink SSO identities. Ports memdAdminApp()'s user
// section + add-user sheet.

const users = ref<AdminUser[]>([]);
const loading = ref(false);
const loadErr = ref("");

const sheetOpen = ref(false);
const userForm = reactive({
  username: "",
  display_name: "",
  password: "",
  err: "",
  submitting: false,
});

function displayName(target: AdminUser): string {
  return target.display_name || target.username;
}

function avatarInitial(target: AdminUser): string {
  return displayName(target).slice(0, 1).toUpperCase();
}

function errMessage(e: unknown, fallback: string): string {
  return e instanceof ApiError ? e.message : fallback;
}

async function load(): Promise<void> {
  loading.value = true;
  loadErr.value = "";
  try {
    const data = await admin.users.list();
    users.value = data.users ?? [];
  } catch (e) {
    loadErr.value = errMessage(e, "could not load users");
  } finally {
    loading.value = false;
  }
}

function openSheet(): void {
  userForm.username = "";
  userForm.display_name = "";
  userForm.password = "";
  userForm.err = "";
  userForm.submitting = false;
  sheetOpen.value = true;
}

function closeSheet(): void {
  sheetOpen.value = false;
}

async function createUser(): Promise<void> {
  userForm.err = "";
  userForm.submitting = true;
  try {
    await admin.users.create({
      username: userForm.username,
      password: userForm.password,
      display_name: userForm.display_name || undefined,
    });
    closeSheet();
    toast("User created", "success");
    await load();
  } catch (e) {
    userForm.err = errMessage(e, "create failed");
  } finally {
    userForm.submitting = false;
  }
}

async function toggleDisabled(target: AdminUser): Promise<void> {
  if (target.super_admin && !target.disabled) {
    if (!window.confirm(`Disable super admin ${target.username}?`)) {
      return;
    }
  }
  try {
    await admin.users.setDisabled(target.id, !target.disabled);
    await load();
  } catch (e) {
    toast(errMessage(e, "update failed"), "error");
  }
}

async function resetPassword(target: AdminUser): Promise<void> {
  const password = window.prompt(`New password for ${target.username}`);
  if (!password) {
    return;
  }
  try {
    await admin.users.setPassword(target.id, password);
    toast("Password reset", "success");
  } catch (e) {
    toast(errMessage(e, "password reset failed"), "error");
  }
}

async function unlinkSSO(target: AdminUser): Promise<void> {
  const warning =
    `Unlink SSO from ${target.username}? The account can no longer sign in through the identity provider` +
    (target.sso_linked ? " and its identity slot is freed for another account." : ".");
  if (!window.confirm(warning)) {
    return;
  }
  try {
    await admin.users.unlinkOidc(target.id);
    toast("SSO identity unlinked", "success");
    await load();
  } catch (e) {
    toast(errMessage(e, "unlink failed"), "error");
  }
}

onMounted(load);
</script>
