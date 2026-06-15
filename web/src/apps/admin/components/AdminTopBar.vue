<template>
  <header class="topbar">
    <a class="brand" href="/" aria-label="memd home">
      <img class="logo logo-light" :src="logoLight" alt="memd" />
      <img class="logo logo-dark" :src="logoDark" alt="memd" />
    </a>
    <div class="brand-sub">Super admin</div>

    <!-- Section nav: the three admin surfaces, as router-links. -->
    <nav class="scope-tabs admin-tabs" aria-label="Admin sections">
      <router-link class="admin-tab" active-class="on" to="/users" title="Users">
        <MIcon name="users" />
        <span>Users</span>
      </router-link>
      <router-link class="admin-tab" active-class="on" to="/sso" title="Single sign-on">
        <MIcon name="plug" />
        <span>SSO</span>
      </router-link>
      <router-link class="admin-tab" active-class="on" to="/doctrines" title="Doctrines">
        <MIcon name="info" />
        <span>Doctrines</span>
      </router-link>
    </nav>

    <span class="spacer"></span>

    <a class="top-link" href="/">Memory</a>
    <div class="user-pill">
      <span>{{ displayName }}</span>
      <button type="button" @click="onLogout">Log out</button>
    </div>
    <button
      class="top-icon top-theme"
      type="button"
      @click="toggleTheme"
      :title="theme === 'light' ? 'Switch to dark' : 'Switch to light'"
    >
      <MIcon v-if="theme === 'light'" name="moon" />
      <MIcon v-else name="sun" />
    </button>
  </header>
</template>

<script setup lang="ts">
import { computed } from "vue";
import logoLight from "@/shared/assets/logo-light.png";
import logoDark from "@/shared/assets/logo-dark.png";
import MIcon from "@/shared/components/MIcon.vue";
import { useSession } from "@/shared/session";
import { useTheme } from "@/shared/theme";

// The admin top bar: brand + "Super admin" sub, the section nav, a link back to
// the memory app, the signed-in user pill (with log-out), and the theme toggle.

const { user, logout } = useSession();
const { theme, toggleTheme } = useTheme();

const displayName = computed(() => {
  const u = user.value;
  return u ? u.display_name || u.username : "";
});

async function onLogout(): Promise<void> {
  // logout() clears session state; an SSO session may hand back an RP-initiated
  // logout URL to finish the sign-out at the IdP.
  const url = await logout();
  if (url) {
    window.location.href = url;
  } else {
    window.location.reload();
  }
}
</script>

<style scoped>
/* The section nav reuses the global .scope-tabs look, but its items are
   <router-link> anchors rather than <button>s — mirror the same sizing/active
   styles so it matches the rest of the chrome. */
.admin-tab {
  display: inline-flex;
  gap: 7px;
  align-items: center;
  min-height: 30px;
  padding: 0 11px;
  color: var(--fg-2);
  font-size: 12px;
  font-weight: 650;
  line-height: 1;
  white-space: nowrap;
  border-radius: var(--radius-sm);
}
.admin-tab:hover,
.admin-tab.on {
  color: var(--fg-1);
  background: var(--bg);
}
.admin-tab.on :deep(.icon) {
  color: var(--accent);
}
.admin-tab :deep(.icon) {
  width: 15px;
  height: 15px;
  color: var(--fg-3);
}
</style>
