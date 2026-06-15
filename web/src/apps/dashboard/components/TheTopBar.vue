<template>
  <header class="topbar">
    <router-link class="brand" to="/" aria-label="memd home">
      <img class="logo logo-light" :src="logoLight" alt="memd" />
      <img class="logo logo-dark" :src="logoDark" alt="memd" />
    </router-link>
    <div class="brand-sub">Local memory server for AI agents</div>
    <span class="spacer"></span>

    <!-- Mobile-only quick switcher in the otherwise empty middle of the top
         bar; the sidebar equivalents live behind the hamburger. -->
    <nav class="quicknav" aria-label="Quick navigation">
      <router-link
        v-if="!isSuperAdmin"
        class="quicknav-link"
        active-class="on"
        to="/teams"
        title="Teams"
        aria-label="Teams"
      >
        <MIcon name="users" />
      </router-link>
      <router-link
        class="quicknav-link"
        active-class="on"
        to="/directories"
        title="Directories"
        aria-label="Directories"
      >
        <MIcon name="folder-open" />
      </router-link>
      <router-link
        class="quicknav-link"
        active-class="on"
        to="/tasks"
        title="Tasks"
        aria-label="Tasks"
      >
        <MIcon name="list-checks" />
      </router-link>
      <router-link
        class="quicknav-link"
        active-class="on"
        to="/connectors"
        title="Connectors"
        aria-label="Connectors"
      >
        <MIcon name="plug" />
      </router-link>
    </nav>

    <a class="top-link" href="/admin/" v-if="isSuperAdmin">Admin</a>
    <span class="bind">{{ host }}</span>
    <div class="user-pill">
      <span>{{ displayName }}</span>
      <button type="button" @click="onLogout">Log out</button>
    </div>
    <button
      class="top-icon top-layout"
      type="button"
      @click="toggleLayout"
      :title="layout === 'wide' ? 'Use centered layout' : 'Use full-width layout'"
    >
      <MIcon v-if="layout === 'wide'" name="minimize" />
      <MIcon v-else name="maximize" />
    </button>
    <button
      class="top-icon top-theme"
      type="button"
      @click="toggleTheme"
      :title="theme === 'light' ? 'Switch to dark' : 'Switch to light'"
    >
      <MIcon v-if="theme === 'light'" name="moon" />
      <MIcon v-else name="sun" />
    </button>
    <button
      class="top-icon hamburger"
      type="button"
      @click="emit('toggle-nav')"
      :class="navOpen ? 'on' : ''"
      :aria-expanded="navOpen ? 'true' : 'false'"
      title="Open navigation"
    >
      <MIcon v-if="!navOpen" name="menu" />
      <MIcon v-else name="x" />
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

// The top bar: brand, mobile quick-switch, admin shortcut, the signed-in user
// pill (with log-out), theme + layout-width toggles, and the hamburger that
// drives the mobile nav drawer (state owned by the shell, passed as `navOpen`).

defineProps<{ navOpen: boolean }>();
const emit = defineEmits<{ (e: "toggle-nav"): void }>();

const { user, isSuperAdmin, logout } = useSession();
const { theme, layout, toggleTheme, toggleLayout } = useTheme();

const host = window.location.host;
const displayName = computed(() => {
  const u = user.value;
  return u ? u.display_name || u.username : "";
});

async function onLogout(): Promise<void> {
  const url = await logout();
  if (url) {
    window.location.href = url;
  } else {
    window.location.reload();
  }
}
</script>

<style scoped>
/* The mobile quick-switch reuses the original .quicknav button look; here the
   items are <router-link> anchors, so mirror the same sizing/active styles the
   global stylesheet defines for `.quicknav button`. */
.quicknav-link {
  position: relative;
  display: none;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  width: 38px;
  height: 34px;
  color: var(--fg-3);
  border-radius: var(--radius-md);
}
.quicknav-link.on {
  color: var(--fg-1);
  background: var(--surface-2);
}
.quicknav-link.on :deep(.icon) {
  color: var(--accent);
}
.quicknav-link :deep(.icon) {
  width: 17px;
  height: 17px;
}
@media (max-width: 920px) {
  .quicknav-link {
    display: inline-flex;
  }
}
</style>
