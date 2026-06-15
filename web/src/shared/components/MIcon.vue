<template>
  <svg v-if="href" class="icon" aria-hidden="true"><use :href="href" /></svg>
</template>

<script setup lang="ts">
import { computed } from "vue";

// The SVG icons are sprite files (each contains <symbol id="icon">). Vite hashes
// them as URL assets; we reference the fragment so <use> pulls the symbol. Glob
// patterns can't use the "@" alias, so the path is relative to this file.
const modules = import.meta.glob("../assets/icons/*.svg", {
  eager: true,
  query: "?url",
  import: "default",
}) as Record<string, string>;

const urlByName: Record<string, string> = {};
for (const path in modules) {
  const name = path.split("/").pop()!.replace(/\.svg$/, "");
  urlByName[name] = modules[path];
}

const props = defineProps<{ name: string }>();
const href = computed(() => {
  const url = urlByName[props.name];
  return url ? `${url}#icon` : "";
});
</script>
