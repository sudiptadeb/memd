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
        <h3>Add directory</h3>
        <div class="sub">A folder memd will serve to agents.</div>
      </div>
      <span class="spacer"></span>
      <button class="icon-btn" type="button" @click="emit('close')" title="Close">
        <MIcon name="x" />
      </button>
    </header>

    <form class="sheet-body" id="add-dir-form" @submit.prevent="submit">
      <div class="field">
        <label class="field-label">Name<span class="req">*</span></label>
        <input class="input" v-model="form.name" required placeholder="work-notes" />
        <div class="field-hint">Short identifier for this memory directory.</div>
      </div>

      <div class="field">
        <label class="field-label">Description</label>
        <input class="input" v-model="form.description" placeholder="What this directory holds" />
      </div>

      <div class="field" v-if="teams.length">
        <label class="field-label">Share with team</label>
        <select class="input" v-model="form.team_id">
          <option value="">Personal — only you</option>
          <option v-for="team in teams" :key="team.id" :value="team.id">{{ team.name }}</option>
        </select>
        <div class="field-hint">
          Team members can use shared directories with their own connectors. You can change this later on the
          directory card.
        </div>
      </div>

      <div class="field">
        <label class="field-label">Backend</label>
        <div class="seg-control">
          <button type="button" :class="form.backend === 'local' ? 'on' : ''" @click="form.backend = 'local'">
            <MIcon name="hard-drive" />
            Local folder
          </button>
          <button type="button" :class="form.backend === 'git' ? 'on' : ''" @click="form.backend = 'git'">
            <MIcon name="git-branch" />
            Git repository
          </button>
        </div>
      </div>

      <div class="field" v-if="form.backend === 'local' && canBrowseFs">
        <label class="field-label">Path<span class="req">*</span></label>
        <div class="input-group">
          <input class="input" v-model="form.local_path" required placeholder="/Users/you/memory" />
          <button type="button" @click="pickerOpen = true">
            <MIcon name="folder-search" />
            Browse
          </button>
        </div>
        <div class="field-hint">Must be readable by the memd process.</div>
      </div>

      <div class="field" v-if="form.backend === 'local' && !canBrowseFs">
        <div class="field-hint">
          memd will create and manage a private folder for this directory. To store it at a specific path, bring a
          Git repository or sign in with a local account.
        </div>
      </div>

      <DirGitForm v-if="form.backend === 'git'" ref="gitForm" :model="form.git" />

      <span class="err" v-if="err">{{ err }}</span>
    </form>

    <footer class="sheet-foot">
      <span class="spacer"></span>
      <button class="btn ghost" type="button" @click="emit('close')">Cancel</button>
      <button class="btn primary" type="submit" form="add-dir-form" :disabled="submitting">Add directory</button>
    </footer>
  </aside>

  <DirFsBrowser :open="pickerOpen" @close="pickerOpen = false" @select="onPickPath" />
</template>

<script setup lang="ts">
import { reactive, ref, watch } from "vue";
import MIcon from "@/shared/components/MIcon.vue";
import DirGitForm from "./DirGitForm.vue";
import DirFsBrowser from "./DirFsBrowser.vue";
import { directories, ApiError } from "@/shared/api";
import type { CreateDirectoryRequest, DirectoryBackend, GitConfig, Team } from "@/shared/types";

// The "Add directory" sheet. Creates a local or git directory; for local
// accounts / super admins it offers the server-filesystem picker.
const props = defineProps<{ open: boolean; teams: Team[]; canBrowseFs: boolean }>();
const emit = defineEmits<{ (e: "close"): void; (e: "created"): void }>();

interface DirFormState {
  name: string;
  team_id: string;
  description: string;
  backend: DirectoryBackend;
  local_path: string;
  git: GitConfig;
}

function emptyGit(): GitConfig {
  return {
    remote_url: "",
    branch: "main",
    base_path: "",
    author_name: "memd",
    author_email: "memd@localhost",
    auth_username: "",
    auth_token: "",
    ssh_key_path: "",
  };
}

function defaults(): DirFormState {
  return {
    name: "",
    team_id: "",
    description: "",
    backend: "local",
    local_path: "",
    git: emptyGit(),
  };
}

const form = reactive<DirFormState>(defaults());
const err = ref("");
const submitting = ref(false);
const pickerOpen = ref(false);
const gitForm = ref<InstanceType<typeof DirGitForm> | null>(null);

// Reset to a clean form whenever the sheet opens.
watch(
  () => props.open,
  (isOpen) => {
    if (isOpen) {
      Object.assign(form, defaults());
      err.value = "";
      submitting.value = false;
      pickerOpen.value = false;
      gitForm.value?.reset();
    }
  },
);

function onPickPath(path: string): void {
  form.local_path = path;
  pickerOpen.value = false;
}

async function submit(): Promise<void> {
  err.value = "";
  submitting.value = true;
  const body: CreateDirectoryRequest = {
    name: form.name,
    team_id: form.team_id || "",
    description: form.description,
    backend: form.backend,
  };
  if (form.backend === "local") {
    body.local_path = form.local_path;
  } else {
    body.git = {
      remote_url: form.git.remote_url,
      branch: form.git.branch || "main",
      base_path: form.git.base_path || "",
      author_name: form.git.author_name || "memd",
      author_email: form.git.author_email || "memd@localhost",
      auth_username: form.git.auth_username || "",
      auth_token: form.git.auth_token || "",
      ssh_key_path: form.git.ssh_key_path || "",
    };
  }
  try {
    await directories.create(body);
    emit("created");
  } catch (e) {
    err.value = e instanceof ApiError ? e.message : String(e);
  } finally {
    submitting.value = false;
  }
}
</script>
