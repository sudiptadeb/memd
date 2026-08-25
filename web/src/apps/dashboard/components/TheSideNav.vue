<template>
  <aside
    class="sidebar"
    :class="open ? 'open' : ''"
    :aria-hidden="isMobile && !open ? 'true' : 'false'"
    :inert="isMobile && !open"
    aria-label="Primary navigation"
  >
    <div class="drawer-head">
      <router-link class="brand" to="/" aria-label="memd home" @click="emit('close')">
        <img class="logo logo-light" :src="logoLight" alt="memd" />
        <img class="logo logo-dark" :src="logoDark" alt="memd" />
      </router-link>
      <button class="icon-btn" type="button" @click="emit('close')" title="Close navigation">
        <MIcon name="x" />
      </button>
    </div>

    <nav class="sidebar-nav">
      <div class="sidebar-group">
        <div class="sidebar-label">Navigate</div>
        <router-link
          v-for="item in items"
          :key="item.to"
          class="nav-item"
          active-class="on"
          :to="item.to"
          :title="item.title"
          @click="emit('close')"
        >
          <MIcon :name="item.icon" />
          <span>{{ item.label }}</span>
          <b v-if="item.badge">{{ item.badge }}</b>
        </router-link>
      </div>
    </nav>

    <!-- Account + log-out, shown only inside the mobile drawer; on desktop the
         top-bar user pill carries this and `.mobile-account` is display:none. -->
    <div class="mobile-account" v-if="user">
      <div class="mobile-account-id">
        <span class="mobile-account-name">{{ displayName }}</span>
        <span class="mobile-account-sub" v-if="subText">{{ subText }}</span>
      </div>
      <button type="button" class="mobile-account-logout" @click="onLogout">
        Log out
      </button>
    </div>
  </aside>
</template>

<script setup lang="ts">
import { computed, onUnmounted, watch } from "vue";
import logoLight from "@/shared/assets/logo-light.png";
import logoDark from "@/shared/assets/logo-dark.png";
import MIcon from "@/shared/components/MIcon.vue";
import { useSession } from "@/shared/session";
import { useRcAgents } from "../rcAgents";

// Primary navigation: a persistent rail on desktop, a slide-in drawer on mobile.
// Drawer visibility is owned by the shell and passed in as `open`; each link (and
// the close button / brand) asks the shell to close the drawer via `close`.

defineProps<{ open: boolean; isMobile: boolean }>();
const emit = defineEmits<{ (e: "close"): void }>();

const { user, isSuperAdmin, rcEnabled, logout } = useSession();
const { liveCount, startBadgePolling, stopBadgePolling } = useRcAgents();

// The termulaa badge shows the live session count. Poll only while the server
// advertises the feature and the nav is mounted — no leaked intervals, and
// nothing ever fires against a server without the rc routes. `immediate` also
// covers (re)mounting with the flag already known.
watch(
  rcEnabled,
  (on) => {
    if (on) startBadgePolling();
    else stopBadgePolling();
  },
  { immediate: true },
);
onUnmounted(stopBadgePolling);

const displayName = computed(() => {
  const u = user.value;
  return u ? u.display_name || u.username : "";
});

// Secondary line: the @username (what teammates need to add this user to a
// team) when it differs from the name shown, else the email — never the same
// string twice.
const subText = computed(() => {
  const u = user.value;
  if (!u) return "";
  if (u.username !== displayName.value) return "@" + u.username;
  return u.email || "";
});

async function onLogout(): Promise<void> {
  emit("close");
  const url = await logout();
  if (url) {
    window.location.href = url;
  } else {
    window.location.reload();
  }
}

interface NavItem {
  to: string;
  icon: string;
  label: string;
  title: string;
  // Small count pill on the right (e.g. live termulaa sessions); 0 hides it.
  badge?: number;
}

const items = computed<NavItem[]>(() => {
  const list: NavItem[] = [
    { to: "/info", icon: "info", label: "How it works", title: "How it works" },
  ];
  // Teams management is for regular users; super admins manage globally in /admin.
  if (!isSuperAdmin.value) {
    list.push({ to: "/teams", icon: "users", label: "Teams", title: "Teams" });
  }
  list.push(
    { to: "/directories", icon: "folder-open", label: "Directories", title: "Directories" },
    { to: "/tasks", icon: "list-checks", label: "Tasks", title: "Tasks" },
    { to: "/connectors", icon: "plug", label: "Connectors", title: "Connectors" },
    { to: "/activity", icon: "activity", label: "Activity", title: "Activity" },
  );
  // Only when the server actually mounts the rc routes (opt-in feature).
  if (rcEnabled.value) {
    list.push({
      to: "/termulaa",
      icon: "terminal",
      label: "termulaa",
      title: "termulaa remote terminals",
      badge: liveCount.value,
    });
  }
  return list;
});
</script>
