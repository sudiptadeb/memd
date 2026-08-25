import { computed, ref } from "vue";
import { rc as rcApi } from "@/shared/api";
import type { RcAgent } from "@/shared/types";

// Live termulaa agent state, shared between the side nav (count badge) and the
// termulaa page (session list) — a module-level singleton like the session
// store. Callers must only refresh when the session advertises `features.rc`;
// with the feature off, /rc/api/agents falls through to the SPA catch-all.

const agents = ref<RcAgent[]>([]);
const viewHost = ref("");
// True once one agents fetch has succeeded; before that the UI shows neither
// a session list nor an "empty" claim it cannot back.
const loaded = ref(false);

// Only agents with at least one live tunnel count as live sessions — zero
// tunnels reads as offline, never as a reachable terminal.
const liveCount = computed(() => agents.value.filter((a) => a.tunnels > 0).length);

async function refresh(): Promise<void> {
  const res = await rcApi.agents();
  agents.value = res.agents || [];
  viewHost.value = res.view_host || "";
  loaded.value = true;
}

// Slow background poll keeping the nav badge honest (a stale count would
// fabricate liveness). The termulaa page layers its own faster poll on top of
// the same state via refresh(). Skipped while the tab is hidden; the side nav
// starts/stops it with its own lifecycle, so nothing outlives the shell.
let badgeTimer: number | undefined;

function startBadgePolling(): void {
  if (badgeTimer !== undefined) return;
  void refresh().catch(() => {});
  badgeTimer = window.setInterval(() => {
    if (document.hidden) return;
    void refresh().catch(() => {});
  }, 30_000);
}

function stopBadgePolling(): void {
  if (badgeTimer === undefined) return;
  window.clearInterval(badgeTimer);
  badgeTimer = undefined;
}

export function useRcAgents() {
  return { agents, viewHost, loaded, liveCount, refresh, startBadgePolling, stopBadgePolling };
}
