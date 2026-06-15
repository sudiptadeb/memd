<template>
  <!-- Session not yet resolved: a minimal splash so we never flash the login
       screen at an already-signed-in user (or vice versa). -->
  <div v-if="!checked" class="boot-splash" role="status" aria-live="polite">
    <span class="boot-spinner" aria-hidden="true"></span>
  </div>

  <!-- Signed out: the full-page landing/login. -->
  <LoginScreen v-else-if="!user" />

  <!-- Signed in: the application shell. -->
  <template v-else>
    <TheTopBar :nav-open="navOpen" @toggle-nav="toggleNav" />
    <div class="body logs-hidden">
      <TheSideNav :open="navOpen" :is-mobile="isMobile" @close="closeNav" />
      <main class="main">
        <router-view />
      </main>
    </div>
    <div class="nav-scrim" :class="navOpen ? 'open' : ''" @click="closeNav"></div>
  </template>

  <!-- Mounted once, regardless of auth state, so any view can raise a toast or
       drive the full-page loader through the shared bus. -->
  <MToast />
  <MLoader />
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from "vue";
import { useRoute } from "vue-router";
import LoginScreen from "./components/LoginScreen.vue";
import TheTopBar from "./components/TheTopBar.vue";
import TheSideNav from "./components/TheSideNav.vue";
import MToast from "@/shared/components/MToast.vue";
import MLoader from "@/shared/components/MLoader.vue";
import { useSession } from "@/shared/session";

// The dashboard application shell: resolves the session once, then renders the
// splash / login / signed-in shell accordingly. It owns the mobile nav-drawer
// open state (the topbar hamburger toggles it, the sidenav + scrim close it) and
// closes the drawer on every route change.

const { user, checked, refresh } = useSession();
const route = useRoute();

const navOpen = ref(false);

function toggleNav(): void {
  navOpen.value = !navOpen.value;
}
function closeNav(): void {
  navOpen.value = false;
}

// Close the drawer whenever the route changes (a nav link was followed).
watch(
  () => route.fullPath,
  () => {
    navOpen.value = false;
  },
);

// Track the mobile breakpoint (matches the original isMobileNav()): the sidenav
// uses it to mark itself inert/hidden while the drawer is closed on small screens.
const mobileQuery = window.matchMedia("(max-width: 920px)");
const isMobile = ref(mobileQuery.matches);
function onMediaChange(e: MediaQueryListEvent): void {
  isMobile.value = e.matches;
}

onMounted(() => {
  mobileQuery.addEventListener("change", onMediaChange);
  void refresh();
});

onUnmounted(() => {
  mobileQuery.removeEventListener("change", onMediaChange);
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
