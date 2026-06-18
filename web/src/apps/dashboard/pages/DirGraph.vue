<template>
  <section class="graph-page" :class="{ fullscreen }">
    <header class="detail-head" v-show="!fullscreen">
      <RouterLink class="detail-back" :to="`/directories/${dirId}`" title="Back to directory" aria-label="Back to directory">
        <MIcon name="arrow-left" />
      </RouterLink>
      <div class="detail-titles">
        <div class="detail-title">Link graph</div>
        <div class="detail-sub" v-if="data">
          {{ data.nodes?.length || 0 }} files · {{ data.edges?.length || 0 }} links
          <template v-if="data.orphans?.length"> · {{ data.orphans.length }} orphans</template>
          <template v-if="data.broken?.length"> · {{ data.broken.length }} broken</template>
        </div>
      </div>
      <div class="detail-actions" v-if="data && data.nodes && data.nodes.length">
        <button class="btn ghost btn-sm" type="button" @click="fit">Fit</button>
        <button class="btn ghost btn-sm" type="button" @click="toggleFullscreen">
          <MIcon name="maximize" />
          Fullscreen
        </button>
      </div>
    </header>

    <div class="detail-loading" v-if="loading">Building graph…</div>

    <div class="empty" v-else-if="!data || !data.nodes || data.nodes.length === 0">
      <div class="empty-icon"><MIcon name="folder-search" /></div>
      <h4>Nothing to graph yet</h4>
      <p>This directory has no memory files with links between them.</p>
    </div>

    <div class="graph-body" v-else>
      <div class="graph-stage">
        <div class="graph-canvas-wrap">
          <div ref="cyEl" class="graph-canvas"></div>
          <button v-if="fullscreen" class="fs-exit btn ghost btn-sm" type="button" @click="toggleFullscreen">
            <MIcon name="minimize" />
            Exit
          </button>
        </div>

        <!-- Desktop: side aside. Mobile: collapses below the graph. Hidden in fullscreen. -->
        <aside class="graph-aside" v-show="!fullscreen">
          <template v-if="selectedNode">
            <div class="dock-head">
              <span class="dock-type">{{ selectedNode.type || "file" }}</span>
              <a class="btn ghost btn-sm dock-open" :href="rawUrl(selectedNode.path)" target="_blank" rel="noopener">
                <MIcon name="external-link" />
                Open
              </a>
            </div>
            <div class="dock-title">{{ selectedNode.title }}</div>
            <code class="card-path">{{ selectedNode.path }}</code>
            <div class="dock-counts">{{ selectedNode.outbound }} out · {{ selectedNode.inbound }} in</div>
            <p class="dock-desc" v-if="selectedNode.description">{{ selectedNode.description }}</p>
            <div class="dock-col" v-if="neighborsOut.length">
              <div class="eyebrow">Links to</div>
              <div class="chip-row">
                <button v-for="p in neighborsOut" :key="`o-${p}`" class="chip" @click="select(p)">→ {{ base(p) }}</button>
              </div>
            </div>
            <div class="dock-col" v-if="neighborsIn.length">
              <div class="eyebrow">Linked from</div>
              <div class="chip-row">
                <button v-for="p in neighborsIn" :key="`i-${p}`" class="chip" @click="select(p)">← {{ base(p) }}</button>
              </div>
            </div>
          </template>
          <template v-else>
            <p class="field-hint dock-hint">
              Click a node to inspect it; drag to rearrange, scroll to zoom. Bigger nodes have more links;
              ringed nodes are orphans.
            </p>
            <div class="dock-col" v-if="data.broken?.length">
              <div class="eyebrow">Broken links ({{ data.broken.length }})</div>
              <div class="chip-row">
                <span v-for="(e, i) in data.broken" :key="`b-${i}`" class="chip broken">{{ base(e.from) }} → {{ base(e.to) }}</span>
              </div>
            </div>
          </template>
        </aside>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from "vue";
import { useRoute, RouterLink } from "vue-router";
import cytoscape from "cytoscape";
import type { Core } from "cytoscape";
import MIcon from "@/shared/components/MIcon.vue";
import { directories as directoriesApi, ApiError } from "@/shared/api";
import { toast } from "@/shared/bus";
import type { GraphResponse } from "@/shared/types";

// The directory's link graph, rendered with Cytoscape.js (a standard graph
// library): correct tap-vs-drag, pan/zoom, and a force layout for free. On
// desktop the info sits in a right-hand aside so the graph keeps the width; on
// mobile it drops below. A fullscreen toggle hands the whole viewport to the
// graph.
const route = useRoute();
const dirId = computed(() => String(route.params.dirId || ""));

const data = ref<GraphResponse | null>(null);
const loading = ref(true);
const selected = ref("");
const fullscreen = ref(false);

const cyEl = ref<HTMLElement | null>(null);
let cy: Core | null = null;
let ro: ResizeObserver | null = null;

const nodes = computed(() => data.value?.nodes || []);
const edges = computed(() => data.value?.edges || []);
const orphanSet = computed(() => new Set(data.value?.orphans || []));

const selectedNode = computed(() => nodes.value.find((n) => n.path === selected.value) || null);
const neighborsOut = computed(() =>
  edges.value.filter((e) => e.from === selected.value).map((e) => e.to),
);
const neighborsIn = computed(() =>
  edges.value.filter((e) => e.to === selected.value).map((e) => e.from),
);

function base(p: string): string {
  const parts = p.split("/");
  return parts[parts.length - 1] || p;
}

function shortLabel(title: string): string {
  return title.length > 22 ? `${title.slice(0, 21)}…` : title;
}

function rawUrl(path: string): string {
  return directoriesApi.rawUrl(dirId.value, path);
}

function fit(): void {
  cy?.fit(undefined, 30);
}

function select(path: string): void {
  selected.value = path;
  if (cy) {
    cy.$("node:selected").unselect();
    const el = cy.getElementById(path);
    if (el.nonempty()) {
      el.select();
      cy.animate({ center: { eles: el }, duration: 200 });
    }
  }
}

async function toggleFullscreen(): Promise<void> {
  fullscreen.value = !fullscreen.value;
  await nextTick();
  cy?.resize();
  cy?.fit(undefined, 30);
}

function onKey(e: KeyboardEvent): void {
  if (e.key === "Escape" && fullscreen.value) {
    void toggleFullscreen();
  }
}

function buildGraph(): void {
  if (!cyEl.value || !data.value) return;
  const ids = new Set(nodes.value.map((n) => n.path));
  const elements: cytoscape.ElementDefinition[] = [
    ...nodes.value.map((n) => ({
      data: {
        id: n.path,
        label: shortLabel(n.title),
        deg: n.inbound + n.outbound,
        orphan: orphanSet.value.has(n.path) ? 1 : 0,
      },
    })),
    // Only edges whose endpoints both exist can be drawn; broken links (target
    // missing) are surfaced in the aside instead.
    ...edges.value
      .filter((e) => ids.has(e.from) && ids.has(e.to))
      .map((e, i) => ({ data: { id: `e${i}`, source: e.from, target: e.to } })),
  ];

  cy = cytoscape({
    container: cyEl.value,
    elements,
    style: [
      {
        selector: "node",
        style: {
          "background-color": "#5b8cff",
          label: "data(label)",
          color: "#c9d2e3",
          "font-size": 9,
          "text-valign": "bottom",
          "text-margin-y": 3,
          width: "mapData(deg, 0, 12, 16, 52)",
          height: "mapData(deg, 0, 12, 16, 52)",
          "min-zoomed-font-size": 6,
        },
      },
      {
        selector: "node[orphan = 1]",
        style: { "background-color": "#8a7320", "border-width": 2, "border-color": "#e0a93a" },
      },
      {
        selector: "node:selected",
        style: { "background-color": "#ffffff", "border-width": 3, "border-color": "#5b8cff", color: "#ffffff" },
      },
      {
        selector: "edge",
        style: {
          width: 1,
          "line-color": "#3a4358",
          "target-arrow-color": "#3a4358",
          "target-arrow-shape": "triangle",
          "arrow-scale": 0.7,
          "curve-style": "bezier",
        },
      },
    ],
    layout: {
      name: "cose",
      animate: false,
      padding: 30,
      idealEdgeLength: 75,
      nodeRepulsion: 9000,
      componentSpacing: 90,
      gravity: 0.25,
      nodeDimensionsIncludeLabels: true,
      fit: true,
    } as cytoscape.LayoutOptions,
    wheelSensitivity: 0.25,
    minZoom: 0.2,
    maxZoom: 3,
  });

  cy.on("tap", "node", (evt) => {
    selected.value = evt.target.id();
  });
  cy.on("tap", (evt) => {
    if (evt.target === cy) selected.value = "";
  });

  ro = new ResizeObserver(() => cy?.resize());
  ro.observe(cyEl.value);
}

async function load(): Promise<void> {
  loading.value = true;
  try {
    data.value = await directoriesApi.graph(dirId.value);
  } catch (e) {
    toast(e instanceof ApiError ? e.message : String(e), "error");
    return;
  } finally {
    loading.value = false;
  }
  // Wait for the canvas (rendered only once loading is false) to be in the DOM,
  // then mount Cytoscape into a container that actually has a size.
  await nextTick();
  buildGraph();
  cy?.resize();
  cy?.fit(undefined, 30);
}

onMounted(() => {
  window.addEventListener("keydown", onKey);
  void load();
});
onBeforeUnmount(() => {
  window.removeEventListener("keydown", onKey);
  ro?.disconnect();
  cy?.destroy();
});
</script>

<style scoped>
/* Full-bleed page: fill the main content area, no narrow column cap. */
.graph-page {
  display: flex;
  flex-direction: column;
  flex: 1 1 auto;
  min-height: calc(100dvh - 200px);
}

/* Fullscreen: hand the whole viewport to the graph, over the app shell. */
.graph-page.fullscreen {
  position: fixed;
  inset: 0;
  z-index: 300;
  min-height: 0;
  padding: 10px 14px;
  background: var(--bg, #0b0d11);
}

.graph-body {
  display: flex;
  flex-direction: column;
  flex: 1 1 auto;
  min-height: 0;
}

.graph-stage {
  display: flex;
  flex: 1 1 auto;
  min-height: 0;
  gap: 10px;
}

.graph-canvas-wrap {
  position: relative;
  flex: 1 1 auto;
  min-width: 0;
  display: flex;
}
.graph-canvas {
  flex: 1 1 auto;
  min-width: 0;
  min-height: 260px;
  width: 100%;
  background: var(--surface-2);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
}
.fs-exit {
  position: absolute;
  top: 10px;
  right: 10px;
  z-index: 5;
}

/* Desktop: info as a right-hand aside so the graph keeps the width. */
.graph-aside {
  flex: 0 0 300px;
  overflow-y: auto;
  padding: 12px 14px;
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--surface);
  display: flex;
  flex-direction: column;
  gap: 9px;
}

/* Mobile: aside drops below the graph as a short dock. */
@media (max-width: 860px) {
  .graph-stage {
    flex-direction: column;
  }
  .graph-aside {
    flex: 0 0 auto;
    height: 150px;
  }
}

.dock-head {
  display: flex;
  align-items: center;
  gap: 10px;
}
.dock-type {
  font-size: 10.5px;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--fg-3);
  padding: 1px 7px;
  background: var(--surface-2);
  border-radius: 999px;
}
.dock-open {
  margin-left: auto;
}
.dock-title {
  font-size: 15px;
  font-weight: 600;
}
.dock-counts {
  font-size: 12px;
  color: var(--fg-3);
}
.btn-sm {
  height: 26px;
  padding: 0 10px;
  font-size: 12px;
}
.dock-desc {
  font-size: 13px;
  color: var(--fg-2);
  white-space: normal;
}
.dock-col {
  min-width: 0;
}
.chip-row {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 4px;
}
.chip {
  font-size: 12px;
  padding: 2px 9px;
  border: 1px solid var(--border);
  border-radius: 999px;
  background: var(--surface-2);
  color: var(--fg-2);
  cursor: pointer;
}
.chip:hover {
  border-color: var(--border-strong);
  color: var(--fg-1);
}
.chip.broken {
  cursor: default;
  color: #e07a7a;
  border-color: #5a3030;
}
.dock-hint {
  margin: 0;
}
.card-path {
  font-size: 12px;
}
</style>
