<template>
  <section class="app-section">
    <header class="detail-head">
      <RouterLink class="detail-back" :to="`/directories/${dirId}`" title="Back to directory" aria-label="Back to directory">
        <MIcon name="arrow-left" />
      </RouterLink>
      <div class="detail-titles">
        <div class="detail-title">Link graph</div>
        <div class="detail-sub" v-if="data">
          {{ data.nodes.length }} files · {{ data.edges.length }} links
          <template v-if="data.orphans.length"> · {{ data.orphans.length }} orphans</template>
          <template v-if="data.broken.length"> · {{ data.broken.length }} broken</template>
        </div>
      </div>
    </header>

    <div class="detail-loading" v-if="loading">Building graph…</div>

    <div class="empty" v-else-if="!data || data.nodes.length === 0">
      <div class="empty-icon"><MIcon name="folder-search" /></div>
      <h4>Nothing to graph yet</h4>
      <p>This directory has no memory files with links between them.</p>
    </div>

    <template v-else>
      <div class="graph-wrap card">
        <svg
          ref="svgEl"
          class="graph-svg"
          :viewBox="`0 0 ${W} ${H}`"
          @mousemove="onDrag"
          @mouseup="endDrag"
          @mouseleave="endDrag"
        >
          <line
            v-for="(e, i) in edges"
            :key="i"
            :x1="pos[e.from]?.x" :y1="pos[e.from]?.y"
            :x2="pos[e.to]?.x" :y2="pos[e.to]?.y"
            class="edge"
            :class="{ broken: e.broken, dim: selected && !touches(e) }"
          />
          <g
            v-for="n in nodes"
            :key="n.path"
            class="node"
            :class="{ orphan: orphanSet.has(n.path), sel: selected === n.path, dim: selected && !adjacent(n.path) }"
            :transform="`translate(${pos[n.path]?.x},${pos[n.path]?.y})`"
            @mousedown.prevent="startDrag(n.path, $event)"
            @click="select(n.path)"
          >
            <circle :r="radius(n)" />
            <text :y="radius(n) + 12">{{ shortLabel(n) }}</text>
          </g>
        </svg>

        <aside class="graph-side" v-if="selectedNode">
          <div class="eyebrow">{{ selectedNode.type || "file" }}</div>
          <div class="side-title">{{ selectedNode.title }}</div>
          <code class="card-path">{{ selectedNode.path }}</code>
          <p class="side-desc" v-if="selectedNode.description">{{ selectedNode.description }}</p>
          <div class="side-counts">
            {{ selectedNode.outbound }} out · {{ selectedNode.inbound }} in
          </div>
          <a class="btn ghost" :href="rawUrl(selectedNode.path)" target="_blank" rel="noopener">
            <MIcon name="external-link" />
            Open file
          </a>
          <div class="side-list" v-if="neighborsOut.length">
            <div class="eyebrow">Links to</div>
            <button v-for="p in neighborsOut" :key="`o-${p}`" class="side-link" @click="select(p)">→ {{ base(p) }}</button>
          </div>
          <div class="side-list" v-if="neighborsIn.length">
            <div class="eyebrow">Linked from</div>
            <button v-for="p in neighborsIn" :key="`i-${p}`" class="side-link" @click="select(p)">← {{ base(p) }}</button>
          </div>
        </aside>
        <aside class="graph-side muted" v-else>
          <p class="field-hint">Click a node to inspect it. Drag to rearrange. Bigger nodes have more links; ringed nodes are orphans; red dashed lines are broken links.</p>
          <div class="side-list" v-if="data.broken.length">
            <div class="eyebrow">Broken links</div>
            <div v-for="(e, i) in data.broken" :key="`b-${i}`" class="broken-row">{{ base(e.from) }} → {{ base(e.to) }}</div>
          </div>
        </aside>
      </div>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from "vue";
import { useRoute, RouterLink } from "vue-router";
import MIcon from "@/shared/components/MIcon.vue";
import { directories as directoriesApi, ApiError } from "@/shared/api";
import { toast } from "@/shared/bus";
import type { GraphResponse, GraphNode } from "@/shared/types";

// The visual navigator: a force-directed render of one directory's link graph.
// The layout runs client-side as a small cooling simulation — fine for the
// dozens-to-low-hundreds of files a memory directory holds.
const route = useRoute();
const dirId = computed(() => String(route.params.dirId || ""));

const W = 760;
const H = 520;

const data = ref<GraphResponse | null>(null);
const loading = ref(true);
const selected = ref<string | null>(null);

// Node positions, mutated in place by the simulation and by dragging.
const pos = reactive<Record<string, { x: number; y: number; vx: number; vy: number }>>({});

const nodes = computed(() => data.value?.nodes || []);
const edges = computed(() => data.value?.edges || []);
const orphanSet = computed(() => new Set(data.value?.orphans || []));
const selectedNode = computed<GraphNode | null>(
  () => nodes.value.find((n) => n.path === selected.value) || null,
);

const adj = reactive<Record<string, Set<string>>>({});

const neighborsOut = computed(() =>
  selected.value ? edges.value.filter((e) => e.from === selected.value).map((e) => e.to) : [],
);
const neighborsIn = computed(() =>
  selected.value ? edges.value.filter((e) => e.to === selected.value).map((e) => e.from) : [],
);

function base(p: string): string {
  const parts = p.split("/");
  return parts[parts.length - 1];
}
function shortLabel(n: GraphNode): string {
  const t = n.title || base(n.path);
  return t.length > 22 ? t.slice(0, 21) + "…" : t;
}
function radius(n: GraphNode): number {
  return 5 + Math.min(10, Math.sqrt(n.inbound + n.outbound) * 2.5);
}
function rawUrl(path: string): string {
  return directoriesApi.rawUrl(dirId.value, path);
}
function touches(e: { from: string; to: string }): boolean {
  return e.from === selected.value || e.to === selected.value;
}
function adjacent(path: string): boolean {
  if (!selected.value) return true;
  return path === selected.value || adj[selected.value]?.has(path) || false;
}
function select(path: string): void {
  selected.value = selected.value === path ? null : path;
}

// --- Force simulation --------------------------------------------------------
let raf = 0;
let alpha = 1;

function tick(): void {
  const ns = nodes.value;
  const k = 1; // base unit
  // Repulsion (every pair pushes apart).
  for (let i = 0; i < ns.length; i++) {
    const a = pos[ns[i].path];
    for (let j = i + 1; j < ns.length; j++) {
      const b = pos[ns[j].path];
      let dx = a.x - b.x;
      let dy = a.y - b.y;
      let d2 = dx * dx + dy * dy;
      if (d2 < 0.01) {
        dx = Math.random() - 0.5;
        dy = Math.random() - 0.5;
        d2 = 0.01;
      }
      const force = (2200 * k) / d2;
      const d = Math.sqrt(d2);
      const fx = (dx / d) * force;
      const fy = (dy / d) * force;
      a.vx += fx;
      a.vy += fy;
      b.vx -= fx;
      b.vy -= fy;
    }
  }
  // Spring attraction along edges.
  for (const e of edges.value) {
    const a = pos[e.from];
    const b = pos[e.to];
    if (!a || !b) continue;
    const dx = b.x - a.x;
    const dy = b.y - a.y;
    const d = Math.sqrt(dx * dx + dy * dy) || 1;
    const force = (d - 90) * 0.02;
    const fx = (dx / d) * force;
    const fy = (dy / d) * force;
    a.vx += fx;
    a.vy += fy;
    b.vx -= fx;
    b.vy -= fy;
  }
  // Gravity to centre + integrate, damped and clamped to the viewBox.
  for (const n of ns) {
    const p = pos[n.path];
    if (p === dragging) continue;
    p.vx += (W / 2 - p.x) * 0.005;
    p.vy += (H / 2 - p.y) * 0.005;
    p.vx *= 0.85;
    p.vy *= 0.85;
    p.x += p.vx * alpha;
    p.y += p.vy * alpha;
    p.x = Math.max(20, Math.min(W - 20, p.x));
    p.y = Math.max(20, Math.min(H - 20, p.y));
  }
  alpha *= 0.992;
  if (alpha > 0.02) {
    raf = requestAnimationFrame(tick);
  }
}

// --- Dragging ----------------------------------------------------------------
const svgEl = ref<SVGSVGElement | null>(null);
let dragging: { x: number; y: number; vx: number; vy: number } | null = null;

function toSvg(ev: MouseEvent): { x: number; y: number } {
  const el = svgEl.value;
  if (!el) return { x: 0, y: 0 };
  const r = el.getBoundingClientRect();
  return { x: ((ev.clientX - r.left) / r.width) * W, y: ((ev.clientY - r.top) / r.height) * H };
}
function startDrag(path: string, ev: MouseEvent): void {
  dragging = pos[path];
  onDrag(ev);
}
function onDrag(ev: MouseEvent): void {
  if (!dragging) return;
  const p = toSvg(ev);
  dragging.x = p.x;
  dragging.y = p.y;
  dragging.vx = 0;
  dragging.vy = 0;
  if (alpha < 0.05) {
    alpha = 0.3;
    cancelAnimationFrame(raf);
    raf = requestAnimationFrame(tick);
  }
}
function endDrag(): void {
  dragging = null;
}

async function load(): Promise<void> {
  loading.value = true;
  try {
    const g = await directoriesApi.graph(dirId.value);
    data.value = g;
    // Seed positions on a circle + build adjacency.
    const n = g.nodes.length || 1;
    g.nodes.forEach((node, i) => {
      const angle = (i / n) * Math.PI * 2;
      pos[node.path] = {
        x: W / 2 + Math.cos(angle) * 180,
        y: H / 2 + Math.sin(angle) * 180,
        vx: 0,
        vy: 0,
      };
      adj[node.path] = new Set();
    });
    for (const e of g.edges) {
      adj[e.from]?.add(e.to);
      adj[e.to]?.add(e.from);
    }
    alpha = 1;
    cancelAnimationFrame(raf);
    raf = requestAnimationFrame(tick);
  } catch (e) {
    toast(e instanceof ApiError ? e.message : String(e), "error");
  } finally {
    loading.value = false;
  }
}

onMounted(load);
onBeforeUnmount(() => cancelAnimationFrame(raf));
</script>

<style scoped>
.app-section {
  max-width: 1100px;
}
.detail-loading {
  padding: 2rem 0;
  color: var(--fg-3);
  font-size: 0.9rem;
}
.graph-wrap {
  display: flex;
  gap: 16px;
  padding: 12px;
  align-items: stretch;
}
.graph-svg {
  flex: 1 1 auto;
  width: 100%;
  height: 540px;
  background: var(--surface-2);
  border-radius: 8px;
  user-select: none;
  cursor: grab;
}
.edge {
  stroke: var(--fg-4, #c4c4c4);
  stroke-width: 1;
  opacity: 0.5;
}
.edge.broken {
  stroke: var(--danger, #d23);
  stroke-dasharray: 4 3;
  opacity: 0.8;
}
.edge.dim {
  opacity: 0.08;
}
.node {
  cursor: pointer;
}
.node circle {
  fill: var(--accent, #4571ff);
  stroke: var(--surface-1, #fff);
  stroke-width: 1.5;
}
.node.orphan circle {
  fill: var(--surface-1, #fff);
  stroke: var(--accent, #4571ff);
  stroke-width: 2;
}
.node.sel circle {
  fill: var(--danger, #d23);
}
.node.dim {
  opacity: 0.2;
}
.node text {
  text-anchor: middle;
  font-size: 9px;
  fill: var(--fg-2, #555);
  pointer-events: none;
}
.graph-side {
  flex: 0 0 280px;
  display: flex;
  flex-direction: column;
  gap: 10px;
  overflow-y: auto;
  max-height: 540px;
}
.graph-side.muted .field-hint {
  font-size: 13px;
}
.side-title {
  font-weight: 600;
  font-size: 15px;
}
.side-desc {
  font-size: 13px;
  color: var(--fg-2);
}
.side-counts {
  font-size: 12px;
  color: var(--fg-3);
}
.side-list {
  display: flex;
  flex-direction: column;
  gap: 3px;
}
.side-link {
  text-align: left;
  background: none;
  border: none;
  color: var(--accent, #4571ff);
  font-size: 13px;
  cursor: pointer;
  padding: 2px 0;
}
.side-link:hover {
  text-decoration: underline;
}
.broken-row {
  font-size: 12px;
  color: var(--danger, #d23);
}
</style>
