<template>
  <section class="task-list-card">
    <header class="task-list-head">
      <span class="task-list-name" v-text="list.name"></span>
      <span class="task-list-counts" v-text="`${list.open} open · ${list.total} total`"></span>
    </header>
    <div class="task-rows">
      <div
        class="task-row"
        v-for="t in visibleTasks"
        :key="t.line"
        :class="{ done: t.done }"
      >
        <div class="task-row-main">
          <input
            type="checkbox"
            :checked="t.done"
            :disabled="!canWrite"
            :aria-label="`Toggle ${t.title}`"
            @change="$emit('toggle', t)"
          />
          <span class="task-title">
            <a
              v-if="t.link"
              :href="rawFileURL(t.link)"
              target="_blank"
              rel="noopener"
              v-text="t.title"
            ></a>
            <span v-else v-text="t.title"></span>
          </span>
          <span
            class="due-chip"
            v-if="t.due"
            :class="dueClass(t.due)"
            v-text="formatDue(t.due)"
          ></span>
          <span
            class="prio-chip"
            v-if="t.prio"
            :class="`prio-${t.prio}`"
            v-text="t.prio"
          ></span>
          <span class="tag-chip" v-for="tag in t.tags || []" :key="tag" v-text="`#${tag}`"></span>
        </div>
        <div class="task-subs" v-if="(t.subtasks || []).length">
          <label
            class="task-sub"
            v-for="s in t.subtasks"
            :key="s.line"
            :class="{ done: s.done }"
          >
            <input
              type="checkbox"
              :checked="s.done"
              :disabled="!canWrite"
              @change="$emit('toggle', s)"
            />
            <span v-text="s.title"></span>
          </label>
        </div>
        <div class="task-notes" v-if="(t.notes || []).length">
          <div class="task-note" v-for="(n, ni) in t.notes" :key="ni" v-text="n"></div>
        </div>
      </div>
      <div class="task-empty" v-if="!visibleTasks.length">
        {{ list.tasks.length ? "All done here 🎉" : "No tasks in this list yet." }}
      </div>
    </div>
    <form class="task-add" v-if="canWrite" @submit.prevent="submitAdd">
      <input class="input" type="text" v-model="newTitle" placeholder="Add a task…" />
      <button class="btn ghost" type="submit" title="Add task">
        <MIcon name="plus" />
      </button>
    </form>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from "vue";
import { directories } from "@/shared/api";
import MIcon from "@/shared/components/MIcon.vue";
import type { Task, TaskList } from "@/shared/types";

const props = defineProps<{
  list: TaskList;
  dirId: string;
  canWrite: boolean;
  hideDone: boolean;
}>();

const emit = defineEmits<{
  (e: "toggle", task: Task): void;
  (e: "add", file: string, title: string): void;
}>();

const newTitle = ref("");

// Tasks to render, dropping completed ones (and completed subtasks) when "Hide
// completed" is on. Display-only — the files are untouched.
const visibleTasks = computed<Task[]>(() => {
  if (!props.hideDone) return props.list.tasks;
  return (props.list.tasks || [])
    .filter((t) => !t.done)
    .map((t) => ({ ...t, subtasks: (t.subtasks || []).filter((s) => !s.done) }));
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

// Resolve a promoted-task link (relative to the list's folder) to its raw file
// URL so it opens in a new tab.
function rawFileURL(link: string): string {
  const file = props.list.file;
  const dir = file.includes("/") ? file.slice(0, file.lastIndexOf("/")) : "";
  const target = dir ? `${dir}/${link}` : link;
  return directories.rawUrl(props.dirId, target);
}

function submitAdd(): void {
  const title = newTitle.value.trim();
  if (!title) return;
  emit("add", props.list.file, title);
  newTitle.value = "";
}
</script>
