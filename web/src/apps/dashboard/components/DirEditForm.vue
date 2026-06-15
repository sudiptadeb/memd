<template>
  <aside
    class="sheet"
    :class="open ? 'open' : ''"
    :aria-hidden="!open"
    :inert="!open"
    @keydown.escape.stop="emit('close')"
  >
    <header class="sheet-head">
      <div>
        <h3>Edit directory</h3>
        <div class="sub">{{ originalName }}</div>
      </div>
      <span class="spacer"></span>
      <button class="icon-btn" type="button" @click="emit('close')" title="Close">
        <MIcon name="x" />
      </button>
    </header>

    <form class="sheet-body" id="edit-dir-form" @submit.prevent="submit">
      <div class="field">
        <label class="field-label">Name<span class="req">*</span></label>
        <input class="input" v-model="name" required placeholder="work-notes" />
        <div class="field-hint">Short identifier for this memory directory.</div>
      </div>

      <div class="field">
        <label class="field-label">Description</label>
        <input class="input" v-model="description" placeholder="What this directory holds" />
      </div>

      <span class="err" v-if="err">{{ err }}</span>
    </form>

    <footer class="sheet-foot">
      <span class="spacer"></span>
      <button class="btn ghost" type="button" @click="emit('close')">Cancel</button>
      <button class="btn primary" type="submit" form="edit-dir-form" :disabled="submitting || !name">
        Save changes
      </button>
    </footer>
  </aside>
</template>

<script setup lang="ts">
import { ref, watch } from "vue";
import MIcon from "@/shared/components/MIcon.vue";
import { directories, ApiError } from "@/shared/api";
import type { DirectoryView } from "@/shared/types";

// Edits a directory's name + description.
const props = defineProps<{ open: boolean; directory: DirectoryView | null }>();
const emit = defineEmits<{ (e: "close"): void; (e: "saved"): void }>();

const originalName = ref("");
const name = ref("");
const description = ref("");
const err = ref("");
const submitting = ref(false);

// Seed the fields from the directory each time the sheet opens.
watch(
  () => props.open,
  (isOpen) => {
    if (isOpen && props.directory) {
      originalName.value = props.directory.name;
      name.value = props.directory.name;
      description.value = props.directory.description || "";
      err.value = "";
      submitting.value = false;
    }
  },
);

async function submit(): Promise<void> {
  if (!props.directory) return;
  err.value = "";
  submitting.value = true;
  try {
    await directories.update(props.directory.id, {
      name: name.value,
      description: description.value,
    });
    emit("saved");
  } catch (e) {
    err.value = e instanceof ApiError ? e.message : String(e);
  } finally {
    submitting.value = false;
  }
}
</script>
