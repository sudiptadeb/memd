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
        v-if="loaded && allDirs.length"
        title="Hide completed tasks from the table"
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

    <!-- Quick add: one task into a chosen directory + list. -->
    <form class="task-quick-add" v-if="loaded && writableDirs.length" @submit.prevent="addTask">
      <input
        class="input"
        type="text"
        v-model="add.title"
        placeholder="Add a task…"
        aria-label="New task title"
      />
      <select
        class="mini-select"
        v-model="add.dirId"
        aria-label="Add to directory"
        v-if="writableDirs.length > 1"
        @change="onAddDirChange"
      >
        <option v-for="d in writableDirs" :key="d.id" :value="d.id" v-text="d.name"></option>
      </select>
      <input
        class="input add-list"
        type="text"
        v-model="add.list"
        list="task-add-lists"
        placeholder="List (e.g. inbox)"
        aria-label="Target list"
      />
      <datalist id="task-add-lists">
        <option v-for="name in addListOptions" :key="name" :value="name"></option>
      </datalist>
      <button class="btn secondary" type="submit" :disabled="!add.title.trim() || add.saving">
        <MIcon name="plus" />
        <span class="btn-label">Add</span>
      </button>
    </form>

    <div class="empty" v-if="loaded && !allDirs.length">
      <div class="empty-icon"><MIcon name="list-checks" /></div>
      <h4>No tasks yet</h4>
      <p>
        Enable <b>Tasks</b> on a directory (in Directories), then add tasks here or let an agent
        keep them for you.
      </p>
    </div>

    <div class="empty" v-else-if="loaded && allDirs.length && !rows.length">
      <div class="empty-icon"><MIcon name="check" /></div>
      <h4 v-if="hideDone">Nothing open</h4>
      <h4 v-else>No tasks here</h4>
      <p v-if="hideDone">Every task in view is done. Untick “Hide completed” to see them.</p>
      <p v-else>Add your first task above to get started.</p>
    </div>

    <div class="mtable-wrap" v-if="rows.length">
      <table class="mtable mtable-stack tasks-table">
        <thead>
          <tr>
            <th class="shrink" scope="col"><span class="sr-only">Done</span></th>
            <th scope="col">Task</th>
            <th class="shrink" scope="col">Directory</th>
            <th class="shrink" scope="col">List</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="row in rows"
            :key="row.key"
            :class="{ 'task-done': row.task.done, 'is-sub': row.sub }"
          >
            <td class="shrink" data-label="Done">
              <input
                type="checkbox"
                :checked="row.task.done"
                :disabled="!row.canWrite"
                :aria-label="`Toggle ${row.task.title}`"
                @change="toggleTask(row)"
              />
            </td>
            <td data-label="Task">
              <div class="task-cell" :class="{ indent: row.sub }">
                <MIcon class="sub-mark" name="corner-left-up" v-if="row.sub" />
                <span class="task-title">
                  <a
                    v-if="row.task.link"
                    :href="rawFileURL(row)"
                    target="_blank"
                    rel="noopener"
                    v-text="row.task.title"
                  ></a>
                  <span v-else v-text="row.task.title"></span>
                </span>
                <span
                  class="due-chip"
                  v-if="row.task.due"
                  :class="dueClass(row.task.due)"
                  v-text="formatDue(row.task.due)"
                ></span>
                <span
                  class="prio-chip"
                  v-if="row.task.prio"
                  :class="`prio-${row.task.prio}`"
                  v-text="row.task.prio"
                ></span>
                <span
                  class="tag-chip"
                  v-for="tag in row.task.tags || []"
                  :key="tag"
                  v-text="`#${tag}`"
                ></span>
              </div>
              <div class="task-notes" v-if="(row.task.notes || []).length">
                <div class="task-note" v-for="(n, ni) in row.task.notes" :key="ni" v-text="n"></div>
              </div>
            </td>
            <td class="shrink cell-strong" data-label="Directory" v-text="row.dirName"></td>
            <td class="shrink cell-muted" data-label="List" v-text="row.listName"></td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { tasks as tasksApi, directories, ApiError } from "@/shared/api";
import { toast } from "@/shared/bus";
import MIcon from "@/shared/components/MIcon.vue";
import type { DirectoryTasks, Task } from "@/shared/types";

const HIDE_DONE_KEY = "memd-tasks-hide-done";

const route = useRoute();
const router = useRouter();

const allDirs = ref<DirectoryTasks[]>([]);
const loading = ref(false);
const loaded = ref(false);
const loadErr = ref("");
const filter = ref<string>(initialFilter());
const hideDone = ref(readHideDone());

// Quick-add target. `dirId`/`list` are kept in sync with the visible directories
// after each load so the form always points at a real, writable destination.
const add = reactive({ title: "", dirId: "", list: "", saving: false });

// One table row. A row is either a top-level task or one of its subtasks
// (`sub`); both carry the directory + list context needed to toggle/label them.
interface Row {
  key: string;
  task: Task;
  sub: boolean;
  dirId: string;
  dirName: string;
  canWrite: boolean;
  listFile: string;
  listName: string;
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

// Directories honouring the current directory filter.
const visibleDirs = computed<DirectoryTasks[]>(() => {
  if (!filter.value) return allDirs.value;
  return allDirs.value.filter((g) => g.id === filter.value);
});

// Writable directories are the valid quick-add targets.
const writableDirs = computed<DirectoryTasks[]>(() => allDirs.value.filter((d) => d.can_write));

// List names already present in the chosen add-target directory, offered as
// datalist suggestions (the user may still type a brand-new list name).
const addListOptions = computed<string[]>(() => {
  const dir = allDirs.value.find((d) => d.id === add.dirId);
  return (dir?.lists || []).map((l) => l.name);
});

// Open-task count across the directories in view (mirrors each list's `open`).
const openCount = computed(() =>
  visibleDirs.value.reduce(
    (sum, d) => sum + (d.lists || []).reduce((n, l) => n + (l.open || 0), 0),
    0,
  ),
);

// How urgent a task is, for sorting: overdue first, then by soonest due date,
// then undated tasks. Lower sorts earlier.
function dueRank(task: Task): number {
  if (!task.due) return Number.MAX_SAFE_INTEGER;
  const d = new Date(`${task.due}T00:00:00`);
  const t = d.getTime();
  return Number.isNaN(t) ? Number.MAX_SAFE_INTEGER - 1 : t;
}

// Flatten every directory → list → task (and subtask) into a single sorted list
// of rows. Completed tasks are dropped when "Hide completed" is on; a subtask is
// rendered directly beneath its parent.
const rows = computed<Row[]>(() => {
  const out: Row[] = [];
  for (const dir of visibleDirs.value) {
    for (const list of dir.lists || []) {
      for (const task of list.tasks || []) {
        if (hideDone.value && task.done) continue;
        const base = {
          dirId: dir.id,
          dirName: dir.name,
          canWrite: dir.can_write,
          listFile: list.file,
          listName: list.name,
        };
        out.push({ key: `${task.file}:${task.line}`, task, sub: false, ...base });
        for (const sub of task.subtasks || []) {
          if (hideDone.value && sub.done) continue;
          out.push({ key: `${sub.file}:${sub.line}`, task: sub, sub: true, ...base });
        }
      }
    }
  }
  // Stable sort keyed on the parent's urgency so subtasks travel with their
  // parent: rank top-level rows; a subtask inherits the row before it.
  const ranked = out.map((row, i) => ({ row, i, rank: row.sub ? -1 : dueRank(row.task) }));
  let lastRank = Number.MAX_SAFE_INTEGER;
  for (const item of ranked) {
    if (item.row.sub) item.rank = lastRank;
    else lastRank = item.rank;
  }
  ranked.sort((a, b) => a.rank - b.rank || a.i - b.i);
  return ranked.map((r) => r.row);
});

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

// Resolve a promoted-task link (relative to its list's folder) to its raw file
// URL so it opens in a new tab.
function rawFileURL(row: Row): string {
  const link = row.task.link || "";
  const file = row.listFile;
  const dir = file.includes("/") ? file.slice(0, file.lastIndexOf("/")) : "";
  const target = dir ? `${dir}/${link}` : link;
  return directories.rawUrl(row.dirId, target);
}

async function load(): Promise<void> {
  loading.value = true;
  loadErr.value = "";
  try {
    const data = await tasksApi.all();
    allDirs.value = data.directories || [];
    loaded.value = true;
    ensureAddTarget();
  } catch (error) {
    loadErr.value = error instanceof ApiError ? error.message : "failed to load tasks";
  } finally {
    loading.value = false;
  }
}

// Keep the quick-add directory pointed at a writable directory, preferring the
// one currently filtered (so "Add" lands where the user is looking).
function ensureAddTarget(): void {
  const writable = writableDirs.value;
  if (!writable.length) {
    add.dirId = "";
    return;
  }
  const filtered = writable.find((d) => d.id === filter.value);
  const current = writable.find((d) => d.id === add.dirId);
  if (filtered) add.dirId = filtered.id;
  else if (!current) add.dirId = writable[0].id;
}

function onAddDirChange(): void {
  // List names differ per directory; clear a stale target on switch.
  add.list = "";
}

async function toggleTask(row: Row): Promise<void> {
  if (!row.canWrite) return;
  try {
    await directories.mutateTasks(row.dirId, {
      action: "toggle",
      file: row.task.file,
      line: row.task.line,
      expect: row.task.raw,
    });
    await load();
  } catch (error) {
    toast(error instanceof ApiError ? error.message : "toggle failed", "error");
    await load();
  }
}

async function addTask(): Promise<void> {
  const title = add.title.trim();
  if (!title || !add.dirId) return;
  const list = add.list.trim();
  add.saving = true;
  try {
    await directories.mutateTasks(add.dirId, {
      action: "add",
      title,
      ...(list ? { list_name: list } : {}),
    });
    add.title = "";
    await load();
  } catch (error) {
    toast(error instanceof ApiError ? error.message : "add failed", "error");
  } finally {
    add.saving = false;
  }
}

function onToggleHideDone(): void {
  try {
    window.localStorage.setItem(HIDE_DONE_KEY, hideDone.value ? "1" : "0");
  } catch {
    /* storage unavailable; toggle still applies for the session */
  }
}

// Keep the directory filter mirrored in the URL query so a filtered table is
// shareable/bookmarkable (parity with the legacy #tasks=<id> deep link).
function syncFilterURL(value: string): void {
  const query = { ...route.query };
  if (value) query.dir = value;
  else delete query.dir;
  void router.replace({ query });
}

// Persist filter changes to the URL and re-aim the quick-add target.
watch(filter, (value) => {
  syncFilterURL(value);
  ensureAddTarget();
});

onMounted(load);
</script>

<style scoped>
/* Compact single-row quick-add that sits between the toolbar and the table. */
.task-quick-add {
  display: flex;
  gap: 8px;
  align-items: center;
  margin: 12px 0;
}
.task-quick-add .input {
  flex: 1;
  min-width: 0;
}
.task-quick-add .add-list {
  flex: 0 0 150px;
}
.task-quick-add .mini-select {
  flex: 0 0 auto;
}

/* Title cell: title plus inline due/priority/tag chips, wrapping on overflow. */
.task-cell {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  min-width: 0;
}
.task-cell.indent {
  padding-left: 18px;
}
.task-cell .sub-mark {
  width: 13px;
  height: 13px;
  margin-left: -16px;
  color: var(--fg-3);
  flex: none;
}
.task-title {
  color: var(--fg-1);
  min-width: 0;
  word-break: break-word;
}
.task-title a {
  color: var(--accent);
  text-decoration: none;
}
.task-title a:hover {
  text-decoration: underline;
}

/* Completed tasks fade and strike through; the file stays untouched. */
.task-done .task-title {
  color: var(--fg-3);
  text-decoration: line-through;
}

.task-notes {
  margin-top: 3px;
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.task-note {
  color: var(--fg-3);
  font-size: 0.78rem;
  line-height: 1.4;
}

.tasks-table input[type="checkbox"] {
  cursor: pointer;
}
.tasks-table input[type="checkbox"]:disabled {
  cursor: default;
}

.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  margin: -1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
  border: 0;
}

@media (max-width: 640px) {
  /* Stacked-card layout: the quick-add controls go full width and the title
     cell aligns left instead of being pushed to the row's right edge. */
  .task-quick-add {
    flex-direction: column;
    align-items: stretch;
  }
  .task-quick-add .add-list,
  .task-quick-add .mini-select {
    flex: 1 1 auto;
    max-width: none;
  }
  .tasks-table .task-cell {
    justify-content: flex-end;
  }
  .task-cell.indent {
    padding-left: 0;
  }
  .task-cell .sub-mark {
    margin-left: 0;
  }
}
</style>
