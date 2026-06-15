<template>
  <!-- Session not yet resolved: a minimal splash so we never flash the login
       screen at an already-signed-in user (or vice versa). -->
  <div v-if="!checked" class="boot-splash" role="status" aria-live="polite">
    <span class="boot-spinner" aria-hidden="true"></span>
  </div>

  <!-- Signed out: the full-page admin login. -->
  <AdminLoginScreen v-else-if="!user" />

  <!-- Signed in but not a super admin: a dead-end panel back to the memory app. -->
  <template v-else-if="!isSuperAdmin">
    <AdminTopBar />
    <main class="main admin-main">
      <div class="empty error-state">
        <div class="empty-icon"><MIcon name="triangle-alert" /></div>
        <h4>Not authorized — super-admin only</h4>
        <p>
          You're signed in, but this console needs a super-admin account. Ask a super admin, or head
          back to your memory.
        </p>
        <a class="btn secondary" href="/">Back to memory</a>
      </div>
    </main>
  </template>

  <!-- Super admin: the admin shell. -->
  <template v-else>
    <AdminTopBar />
    <main class="main admin-main">
      <router-view />
    </main>
  </template>

  <!-- Mounted once, regardless of auth state, so any view can raise a toast or
       drive the full-page loader through the shared bus. -->
  <MToast />
  <MLoader />
</template>

<script setup lang="ts">
import { onMounted } from "vue";
import AdminLoginScreen from "./components/AdminLoginScreen.vue";
import AdminTopBar from "./components/AdminTopBar.vue";
import MIcon from "@/shared/components/MIcon.vue";
import MToast from "@/shared/components/MToast.vue";
import MLoader from "@/shared/components/MLoader.vue";
import { useSession } from "@/shared/session";

// The admin application shell: resolves the session once on mount, then renders
// the splash / login / not-authorized / super-admin console accordingly.

const { user, checked, isSuperAdmin, refresh } = useSession();

onMounted(() => {
  void refresh();
});
</script>

<style scoped>
.boot-splash {
  display: grid;
  min-height: 100vh;
  place-items: center;
}
.boot-spinner {
  width: 28px;
  height: 28px;
  border: 3px solid var(--border, rgba(127, 127, 127, 0.3));
  border-top-color: var(--accent, #2563eb);
  border-radius: 50%;
  animation: boot-spin 0.8s linear infinite;
}
@keyframes boot-spin {
  to {
    transform: rotate(360deg);
  }
}
</style>
