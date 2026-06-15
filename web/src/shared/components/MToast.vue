<template>
  <div class="m-toast-host" aria-live="polite" aria-atomic="false">
    <div v-for="t in toasts" :key="t.id" class="m-toast" :class="t.kind" role="status">
      {{ t.message }}
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref } from "vue";
import { bus, type ToastKind } from "@/shared/bus";

interface ToastItem {
  id: number;
  message: string;
  kind: ToastKind;
}

const toasts = ref<ToastItem[]>([]);
let seq = 0;

function onToast(p: { message: string; kind?: ToastKind; duration?: number }): void {
  const id = ++seq;
  toasts.value.push({ id, message: p.message, kind: p.kind ?? "info" });
  const duration = p.duration ?? 4000;
  window.setTimeout(() => {
    toasts.value = toasts.value.filter((t) => t.id !== id);
  }, duration);
}

onMounted(() => bus.on("toast", onToast));
onUnmounted(() => bus.off("toast", onToast));
</script>

<style scoped>
.m-toast-host {
  position: fixed;
  left: 50%;
  bottom: 1.25rem;
  transform: translateX(-50%);
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  z-index: 1000;
  pointer-events: none;
}
.m-toast {
  pointer-events: auto;
  min-width: 16rem;
  max-width: min(90vw, 28rem);
  padding: 0.7rem 1rem;
  border-radius: 0.5rem;
  font-size: 0.9rem;
  color: #fff;
  background: #1f2937;
  box-shadow: 0 8px 28px rgba(0, 0, 0, 0.28);
  animation: m-toast-in 0.16s ease-out;
}
.m-toast.success { background: #15803d; }
.m-toast.error { background: #b91c1c; }
.m-toast.info { background: #1f2937; }
@keyframes m-toast-in {
  from { opacity: 0; transform: translateY(0.5rem); }
  to { opacity: 1; transform: translateY(0); }
}
</style>
