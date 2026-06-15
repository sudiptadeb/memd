<template>
  <svg
    v-if="icon"
    class="icon"
    :viewBox="icon.viewBox"
    aria-hidden="true"
    v-html="icon.inner"
  />
</template>

<script setup lang="ts">
import { computed } from "vue";

// Inline the icon path data directly into a real <svg>. The source files are
// sprite wrappers — <svg><symbol id="icon" viewBox="...">…paths…</symbol></svg> —
// so we extract the viewBox and the symbol's inner markup and render them inline.
//
// We deliberately do NOT use <use href="icon.svg#icon">: external <use>
// references don't render in Chromium/Safari, and Vite inlines these small SVGs
// as data: URIs (under assetsInlineLimit) whose #fragment never resolves. The
// content is our own build-time asset, so v-html is safe here.
const raw = import.meta.glob("../assets/icons/*.svg", {
  eager: true,
  query: "?raw",
  import: "default",
}) as Record<string, string>;

interface IconSvg {
  viewBox: string;
  inner: string;
}

const icons: Record<string, IconSvg> = {};
for (const path in raw) {
  const name = path.split("/").pop()!.replace(/\.svg$/, "");
  const svg = raw[path];
  const viewBox = /viewBox="([^"]+)"/.exec(svg)?.[1] ?? "0 0 24 24";
  const symbol = /<symbol[^>]*>([\s\S]*?)<\/symbol>/.exec(svg);
  const inner = (symbol ? symbol[1] : svg.replace(/<\/?svg[^>]*>/gi, "")).trim();
  icons[name] = { viewBox, inner };
}

const props = defineProps<{ name: string }>();
const icon = computed<IconSvg | undefined>(() => icons[props.name]);
</script>
