<template>
  <section class="app-section tasks-section">
    <div class="section-head">
      <div class="titles">
        <h2>
          Tasks
          <span class="count" v-if="loaded" v-text="`${openCount} open`"></span>
        </h2>
        <span class="desc">
          Things to do, kept as files in your directories. The files stay the source of truth.
        </span>
      </div>
      <span class="spacer"></span>
      <label
        class="hide-done-toggle"
        v-if="loaded && groups.length"
        title="Hide completed tasks from the lists"
      >
        <input type="checkbox" v-model="hideDone" @change="onToggleHideDone" />
        <span>Hide completed</span>
      </label>
      <select
        class="mini-select"
        aria-label="Filter by directory"
        v-model="filter"
        v-if="allDirs.length > 1"
      >
        <option value="">All directories</option>
        <option v-for="d in allDirs" :key="d.id" :value="d.id" v-text="d.name"></option>
      </select>
      <button class="btn ghost" type="button" title="Refresh" @click="load">
        <MIcon name="refresh-cw" />
        <span class="btn-label">Refresh</span>
      </button>
    </div>

    <div class="picker-error" v-if="loadErr" v-text="loadErr"></div>
    <div class="file-loading" v-if="loading">Loading tasks…</div>

    <div class="empty" v-if="loaded && !groups.length">
      <div class="empty-icon"><MIcon name="list-checks" /></div>
      <h4>No tasks yet</h4>
      <p>
        Enable <b>Tasks</b> on a directory (in Directories), then add tasks here or let an agent
        keep them for you.
      </p>
    </div>

    <div class="tasks-group" v-for="group in groups" :key="group.id">
      <div class="tasks-group-head" v-if="allDirs.length > 1 || !filter">
        <MIcon name="folder-open" />
        <span class="tasks-group-name" v-text="group.name"></span>
        <span class="task-list-counts" v-text="`${groupOpen(group)} open`"></span>
      </div>

      <div class="task-board" v-if="boardBuckets(group.board).length">
        <div
          class="board-bucket"
          v-for="bucket in boardBuckets(group.board)"
          :key="bucket.key"
          :class="bucket.key"
        >
          <div class="board-bucket-head">
            <span class="board-bucket-name" v-text="bucket.label"></span>
            <span class="count" v-text="bucket.items.length"></span>
          </div>
          <div class="board-item" v-for="t in bucket.items" :key="`${t.file}:${t.line}`">
            <input
              type="checkbox"
              :checked="t.done"
              :disabled="!group.can_write"
              :aria-label="`Toggle ${t.title}`"
              @change="toggleTask(group, t)"
            />
            <span class="board-item-title" v-text="t.title"></span>
            <span
              class="due-chip"
              v-if="t.due"
              :class="dueClass(t.due)"
              v-text="formatDue(t.due)"
            ></span>
            <span class="task-list-ref" v-text="listName(group, t.file)"></span>
          </div>
        </div>
      </div>

      <div class="task-lists">
        <TaskListCard
          v-for="list in group.lists"
          :key="list.file"
          :list="list"
          :dir-id="group.id"
          :can-write="group.can_write"
          :hide-done="hideDone"
          @toggle="(t) => toggleTask(group, t)"
          @add="(file, title) => addTask(group, file, title)"
        />

        <form class="task-new-list" v-if="group.can_write" @submit.prevent="addList(group)">
          <input
            class="input"
            type="text"
            v-model="newListNames[group.id]"
            placeholder="New list name (e.g. home renovation)"
          />
          <button class="btn secondary" type="submit">
            <MIcon name="plus" />
            New list
          </button>
        </form>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { tasks as tasksApi, directories, ApiError } from "@/shared/api";
import { toast } from "@/shared/bus";
import MIcon from "@/shared/components/MIcon.vue";
import TaskListCard from "./TaskListCard.vue";
import type { DirectoryTasks, Task, TaskBoard } from "@/shared/types";

const HIDE_DONE_KEY = "memd-tasks-hide-done";

const route = useRoute();
const router = useRouter();

const allDirs = ref<DirectoryTasks[]>([]);
const loading = ref(false);
const loaded = ref(false);
const loadErr = ref("");
const filter = ref<string>(initialFilter());
const hideDone = ref(readHideDone());
const newListNames = reactive<Record<string, string>>({});

interface Bucket {
  key: "overdue" | "due_soon" | "later" | "no_date";
  label: string;
  items: Task[];
}

function initialFilter(): string {
  const dir = route.query.dir;
  if (typeof dir === "string") return dir;
  return "";
}

function readHideDone(): boolean {
  try {
    return window.localStorage.getItem(HIDE_DONE_KEY) === "1";
  } catch {
    return false;
  }
}

// Directory groups to render, honouring the current filter.
const groups = computed<DirectoryTasks[]>(() => {
  if (!filter.value) return allDirs.value;
  return allDirs.value.filter((g) => g.id === filter.value);
});

const openCount = computed(() => groups.value.reduce((sum, g) => sum + groupOpen(g), 0));

function groupOpen(group: DirectoryTasks): number {
  return (group.lists || []).reduce((sum, l) => sum + (l.open || 0), 0);
}

function boardBuckets(board?: TaskBoard): Bucket[] {
  const b = board || ({} as Partial<TaskBoard>);
  return (
    [
      { key: "overdue", label: "Overdue", items: b.overdue || [] },
      { key: "due_soon", label: "Due this week", items: b.due_soon || [] },
      { key: "later", label: "Later", items: b.later || [] },
      { key: "no_date", label: "No date", items: b.no_date || [] },
    ] as Bucket[]
  ).filter((bucket) => bucket.items.length);
}

function listName(group: DirectoryTasks, file: string): string {
  const list = (group.lists || []).find((l) => l.file === file);
  if (list) return list.name;
  const base = (file || "").split("/").pop() || "";
  return base.replace(/\.md$/i, "");
}

function formatDue(due?: string): string {
  if (!due) return "";
  const parts = due.split("-");
  if (parts.length !== 3) return due;
  const months = [
    "Jan", "Feb", "Mar", "Apr", "May", "Jun",
    "Jul", "Aug", "Sep", "Oct", "Nov", "Dec",
  ];
  const m = parseInt(parts[1], 10) - 1;
  if (m < 0 || m > 11) return due;
  return `${months[m]} ${parseInt(parts[2], 10)}`;
}

function dueClass(due?: string): string {
  if (!due) return "";
  const today = new Date();
  const t = new Date(today.getFullYear(), today.getMonth(), today.getDate());
  const d = new Date(`${due}T00:00:00`);
  if (Number.isNaN(d.getTime())) return "";
  const days = Math.round((d.getTime() - t.getTime()) / 86400000);
  if (days < 0) return "overdue";
  if (days <= 7) return "soon";
  return "later";
}

async function load(): Promise<void> {
  loading.value = true;
  loadErr.value = "";
  try {
    const data = await tasksApi.all();
    allDirs.value = data.directories || [];
    loaded.value = true;
  } catch (error) {
    loadErr.value = error instanceof ApiError ? error.message : "failed to load tasks";
  } finally {
    loading.value = false;
  }
}

async function toggleTask(group: DirectoryTasks, task: Task): Promise<void> {
  try {
    await directories.mutateTasks(group.id, {
      action: "toggle",
      file: task.file,
      line: task.line,
      expect: task.raw,
    });
    await load();
  } catch (error) {
    toast(error instanceof ApiError ? error.message : "toggle failed", "error");
    await load();
  }
}

async function addTask(group: DirectoryTasks, file: string, title: string): Promise<void> {
  const trimmed = title.trim();
  if (!trimmed) return;
  try {
    await directories.mutateTasks(group.id, { action: "add", file, title: trimmed });
    await load();
  } catch (error) {
    toast(error instanceof ApiError ? error.message : "add failed", "error");
  }
}

async function addList(group: DirectoryTasks): Promise<void> {
  const name = (newListNames[group.id] || "").trim();
  if (!name) return;
  const title = window.prompt(`First task for “${name}”:`);
  if (title === null || !title.trim()) return;
  try {
    await directories.mutateTasks(group.id, {
      action: "add",
      list_name: name,
      title: title.trim(),
    });
    newListNames[group.id] = "";
    await load();
  } catch (error) {
    toast(error instanceof ApiError ? error.message : "create list failed", "error");
  }
}

function onToggleHideDone(): void {
  try {
    window.localStorage.setItem(HIDE_DONE_KEY, hideDone.value ? "1" : "0");
  } catch {
    /* storage unavailable; toggle still applies for the session */
  }
}

// Keep the directory filter mirrored in the URL query so a filtered board is
// shareable/bookmarkable (parity with the legacy #tasks=<id> deep link).
function syncFilterURL(value: string): void {
  const query = { ...route.query };
  if (value) query.dir = value;
  else delete query.dir;
  void router.replace({ query });
}

// Persist filter changes to the URL.
watch(filter, syncFilterURL);

onMounted(load);
</script>
