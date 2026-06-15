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
        <h3>{{ isEdit ? "Edit connector" : "Add connector" }}</h3>
        <div class="sub">{{ isEdit ? originalName : "A URL an agent can use to reach memd." }}</div>
      </div>
      <span class="spacer"></span>
      <button class="icon-btn" type="button" @click="emit('close')" title="Close">
        <MIcon name="x" />
      </button>
    </header>

    <form class="sheet-body" :id="formId" @submit.prevent="submit">
      <div class="field">
        <label class="field-label">Name<span class="req">*</span></label>
        <input class="input" v-model="form.name" required placeholder="Claude Code" />
        <div class="field-hint" v-if="!isEdit">Shown in the connector list and logs.</div>
      </div>

      <div class="field" v-if="manageableTeams.length">
        <label class="field-label">Share with team</label>
        <select class="input" :value="form.team_id" @change="onTeamChange">
          <option value="">Personal — only you</option>
          <option v-for="team in manageableTeams" :key="team.id" :value="team.id">{{ team.name }}</option>
        </select>
        <div class="field-hint">
          A team connector is visible to the team and limited to that team's directories.
        </div>
      </div>

      <div class="field">
        <label class="field-label">Type</label>
        <div class="seg-control">
          <button type="button" :class="form.kind === 'mcp' ? 'on' : ''" @click="form.kind = 'mcp'">
            <MIcon name="plug" />
            MCP
          </button>
          <button type="button" :class="form.kind === 'http' ? 'on' : ''" @click="form.kind = 'http'">
            <MIcon name="activity" />
            HTTP
          </button>
        </div>
        <div class="field-hint" v-if="!isEdit">
          Use HTTP for agents that can fetch URLs but cannot connect to MCP.
        </div>
      </div>

      <div class="field">
        <label class="field-label">
          {{ isEdit ? "Directories this connector can see" : "Directories" }}<span v-if="!isEdit" class="req">*</span>
        </label>
        <div class="check-list">
          <label
            v-for="directory in attachable"
            :key="directory.id"
            class="check-row"
            :class="form.selected.includes(directory.id) ? 'on' : ''"
          >
            <input
              type="checkbox"
              :value="directory.id"
              :checked="form.selected.includes(directory.id)"
              @change="toggle(directory.id, $event)"
            />
            <div>
              <div class="label">{{ directory.name }}</div>
              <span class="sub">{{ directory.detail }}</span>
            </div>
          </label>
        </div>
      </div>

      <div class="field">
        <label class="toggle-row" @click.prevent="form.write = !form.write">
          <div class="label">
            Allow writes
            <div class="sub">Read-only connectors can only list and read files.</div>
          </div>
          <div class="toggle" :class="form.write ? 'on' : ''"></div>
        </label>
      </div>

      <span class="err" v-if="err">{{ err }}</span>
    </form>

    <footer class="sheet-foot">
      <span class="spacer"></span>
      <button class="btn ghost" type="button" @click="emit('close')">Cancel</button>
      <button class="btn primary" type="submit" :form="formId" :disabled="submitDisabled">
        {{ isEdit ? "Save changes" : "Create connector" }}
      </button>
    </footer>
  </aside>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import MIcon from "@/shared/components/MIcon.vue";
import { connectors as connectorsApi, ApiError } from "@/shared/api";
import type { ConnectorKind, ConnectorRequest, ConnectorView, DirectoryView, Team } from "@/shared/types";

// Create or edit a connector. The markup is shared; `mode` switches labels,
// validation, and whether we POST a new connector or PUT an existing one.
const props = defineProps<{
  open: boolean;
  mode: "create" | "edit";
  connector: ConnectorView | null;
  manageableTeams: Team[];
  directories: DirectoryView[];
}>();
const emit = defineEmits<{ (e: "close"): void; (e: "saved"): void }>();

interface ConnFormState {
  id: string;
  team_id: string;
  name: string;
  kind: ConnectorKind;
  selected: string[];
  write: boolean;
}

const isEdit = computed(() => props.mode === "edit");
const formId = computed(() => (isEdit.value ? "edit-conn-form" : "add-conn-form"));
const originalName = ref("");

function defaults(): ConnFormState {
  return { id: "", team_id: "", name: "", kind: "mcp", selected: [], write: true };
}

const form = reactive<ConnFormState>(defaults());
const err = ref("");
const submitting = ref(false);

// Directories a connector may reference. A team-scoped connector is limited to
// that team's directories; a personal one may reference anything attachable.
const attachable = computed<DirectoryView[]>(() => {
  const scope = form.team_id || "";
  return props.directories.filter((d) => {
    if (!d.can_attach) return false;
    if (scope === "") return true;
    return (d.team_id || "") === scope;
  });
});

const submitDisabled = computed(
  () => submitting.value || !form.selected.length || (isEdit.value && !form.name),
);

watch(
  () => props.open,
  (isOpen) => {
    if (!isOpen) return;
    err.value = "";
    submitting.value = false;
    if (props.mode === "edit" && props.connector) {
      const c = props.connector;
      originalName.value = c.name;
      Object.assign(form, {
        id: c.id,
        team_id: c.team_id || "",
        name: c.name,
        kind: c.kind || "mcp",
        selected: (c.directory_ids || []).slice(),
        write: Boolean(c.write),
      });
    } else {
      originalName.value = "";
      Object.assign(form, defaults());
    }
  },
);

function toggle(id: string, event: Event): void {
  const checked = (event.target as HTMLInputElement).checked;
  const index = form.selected.indexOf(id);
  if (checked && index === -1) {
    form.selected.push(id);
  } else if (!checked && index >= 0) {
    form.selected.splice(index, 1);
  }
}

// Switching team scope drops any now-unattachable selections.
function onTeamChange(event: Event): void {
  form.team_id = (event.target as HTMLSelectElement).value || "";
  const allowed = new Set(attachable.value.map((d) => d.id));
  form.selected = form.selected.filter((id) => allowed.has(id));
}

async function submit(): Promise<void> {
  err.value = "";
  submitting.value = true;
  const body: ConnectorRequest = {
    name: form.name,
    team_id: form.team_id || "",
    kind: form.kind,
    directory_ids: form.selected,
    write: form.write,
  };
  try {
    if (props.mode === "edit") {
      await connectorsApi.update(form.id, body);
    } else {
      await connectorsApi.create(body);
    }
    emit("saved");
  } catch (e) {
    err.value = e instanceof ApiError ? e.message : String(e);
  } finally {
    submitting.value = false;
  }
}
</script>
