<template>
  <section class="app-section logs-page-section">
    <header class="logs-head page-logs-head">
      <h2>Activity</h2>
      <span class="live">live</span>
      <span class="spacer"></span>
      <button class="icon-btn" type="button" title="Clear activity" @click="entries = []">
        <MIcon name="eraser" />
      </button>
    </header>
    <div ref="listEl" class="logs-list page-logs-list">
      <div v-for="entry in entries" :key="entry.id" class="log-line" :class="entry.level">
        <span class="time">{{ formatTime(entry.time) }}</span>
        <span class="message">{{ entry.message }}</span>
      </div>
      <div v-if="!entries.length" class="logs-empty">
        <MIcon name="activity" />
        <span>Waiting for activity...</span>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref } from "vue";
import { logs } from "@/shared/api";
import type { LogEntry } from "@/shared/types";
import MIcon from "@/shared/components/MIcon.vue";

// A live stream of local server events. We poll GET /api/logs?since=, tracking the
// highest entry id seen so each poll only returns what's new (server filters id >
// since; -1 yields everything on the first call). This mirrors the Alpine app's
// pollLogs/startLogs loop, including the 200-entry cap and scroll-to-bottom.

const MAX_ENTRIES = 200;
const POLL_MS = 3000;

const entries = ref<LogEntry[]>([]);
const listEl = ref<HTMLElement | null>(null);

let lastId = -1;
let timer: ReturnType<typeof setInterval> | undefined;
let polling = false;

function formatTime(time: string): string {
  const d = new Date(time);
  return Number.isNaN(d.getTime()) ? time : d.toLocaleTimeString();
}

async function pollLogs(): Promise<void> {
  if (polling) return;
  polling = true;
  try {
    const { entries: fresh } = await logs.since(lastId);
    if (!fresh.length) return;
    entries.value = entries.value.concat(fresh).slice(-MAX_ENTRIES);
    lastId = fresh[fresh.length - 1].id;
    await nextTick();
    const el = listEl.value;
    if (el) el.scrollTop = el.scrollHeight;
  } catch {
    // Errors are surfaced elsewhere in the shell; the activity feed stays quiet
    // and simply retries on the next tick.
  } finally {
    polling = false;
  }
}

onMounted(() => {
  void pollLogs();
  timer = setInterval(() => void pollLogs(), POLL_MS);
});

onBeforeUnmount(() => {
  if (timer) clearInterval(timer);
  timer = undefined;
});
</script>
