<template>
  <div v-if="active" class="m-loader-overlay" role="status" aria-live="polite" aria-label="Loading">
    <div class="m-loader-spinner" />
  </div>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref } from "vue";
import { bus } from "@/shared/bus";

// Full-page loader driven by the event bus: setLoading(true/false) anywhere.
const active = ref(false);
function onLoading(v: boolean): void {
  active.value = v;
}
onMounted(() => bus.on("loading", onLoading));
onUnmounted(() => bus.off("loading", onLoading));
</script>

<style scoped>
.m-loader-overlay {
  position: fixed;
  inset: 0;
  display: grid;
  place-items: center;
  background: rgba(0, 0, 0, 0.18);
  z-index: 1100;
}
.m-loader-spinner {
  width: 2.5rem;
  height: 2.5rem;
  border-radius: 50%;
  border: 3px solid rgba(255, 255, 255, 0.5);
  border-top-color: #fff;
  animation: m-spin 0.7s linear infinite;
}
@keyframes m-spin {
  to { transform: rotate(360deg); }
}
</style>
