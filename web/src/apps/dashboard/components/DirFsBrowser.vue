<template>
  <div class="modal-scrim" :class="open ? 'open' : ''" :inert="!open" @click="emit('close')">
    <div class="modal" @click.stop>
      <header class="modal-head">
        <h3>Choose a folder</h3>
        <div class="sub">Pick the directory memd should serve to agents.</div>
      </header>
      <div class="path-bar">{{ path || "/" }}</div>
      <div class="dir-rows">
        <div class="dir-row up" v-if="parent" @click="browse(parent)">
          <MIcon name="corner-left-up" />
          .. (up)
        </div>
        <div v-for="entry in entries" :key="entry.name" class="dir-row" @click="browse(joinPath(path, entry.name))">
          <MIcon name="folder" />
          <span>{{ entry.name }}</span>
          <MIcon name="chevron-right" class="arrow" />
        </div>
        <div class="picker-empty" v-if="!entries.length && !parent">(no subdirectories)</div>
        <div class="picker-error" v-if="err">{{ err }}</div>
      </div>
      <footer class="modal-foot">
        <span class="selected">{{ path }}</span>
        <button class="btn ghost" type="button" @click="emit('close')">Cancel</button>
        <button class="btn primary" type="button" @click="emit('select', path)">Select</button>
      </footer>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch } from "vue";
import MIcon from "@/shared/components/MIcon.vue";
import { browse as browseApi, ApiError } from "@/shared/api";
import type { BrowseEntry } from "@/shared/types";

// Server-filesystem folder picker (local accounts / super admins). Browses
// subdirectories via /api/browse and emits the chosen absolute path.
const props = defineProps<{ open: boolean }>();
const emit = defineEmits<{ (e: "close"): void; (e: "select", path: string): void }>();

const path = ref("");
const parent = ref("");
const entries = ref<BrowseEntry[]>([]);
const err = ref("");

async function browse(target: string): Promise<void> {
  try {
    const data = await browseApi.list(target);
    path.value = data.path;
    entries.value = data.dirs || [];
    parent.value = data.parent || "";
    err.value = "";
  } catch (e) {
    err.value = e instanceof ApiError ? e.message : String(e);
  }
}

function joinPath(base: string, name: string): string {
  if (!base || base === "/") {
    return "/" + name;
  }
  return base + "/" + name;
}

// Start at the home dir each time the modal opens.
watch(
  () => props.open,
  (isOpen) => {
    if (isOpen) {
      path.value = "";
      parent.value = "";
      entries.value = [];
      err.value = "";
      void browse("");
    }
  },
);
</script>
