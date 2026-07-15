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

        <!-- Personal directories -->
        <template v-if="personalDirs.length">
          <div class="check-group-label" v-if="teamGroups.length">Your directories</div>
          <div class="check-list">
            <label
              v-for="directory in personalDirs"
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
        </template>

        <!-- One group per team: the follow-the-team toggle, then its
             directories (implicitly included while the team is followed). -->
        <template v-for="group in teamGroups" :key="group.team.id">
          <div class="check-group-label">{{ group.team.name }}</div>
          <div class="check-list">
            <label class="check-row" :class="form.attach_teams.includes(group.team.id) ? 'on' : ''">
              <input
                type="checkbox"
                :value="group.team.id"
                :checked="form.attach_teams.includes(group.team.id)"
                @change="toggleTeam(group.team.id, $event)"
              />
              <div>
                <div class="label">All {{ group.team.name }} read-only shared directories</div>
                <span class="sub">
                  Teammates' memories you can read but not write, including ones shared later.
                </span>
              </div>
            </label>
            <label
              v-for="directory in group.dirs"
              :key="directory.id"
              class="check-row check-row-nested"
              :class="form.selected.includes(directory.id) || coveredByTeam(directory) ? 'on' : ''"
            >
              <input
                type="checkbox"
                :value="directory.id"
                :checked="form.selected.includes(directory.id) || coveredByTeam(directory)"
                :disabled="coveredByTeam(directory)"
                @change="toggle(directory.id, $event)"
              />
              <div>
                <div class="label">
                  {{ directory.name }}
                  <span
                    class="dot"
                    v-if="!directory.can_write"
                    title="Served read-only: the owner shared it without write access"
                  >
                    read-only
                  </span>
                </div>
                <span class="sub">{{ directory.detail }}</span>
              </div>
            </label>
          </div>
        </template>
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
  teams: Team[];
  directories: DirectoryView[];
}>();
const emit = defineEmits<{ (e: "close"): void; (e: "saved"): void }>();

interface ConnFormState {
  id: string;
  team_id: string;
  name: string;
  kind: ConnectorKind;
  selected: string[];
  attach_teams: string[];
  write: boolean;
}

const isEdit = computed(() => props.mode === "edit");
const formId = computed(() => (isEdit.value ? "edit-conn-form" : "add-conn-form"));
const originalName = ref("");

function defaults(): ConnFormState {
  return { id: "", team_id: "", name: "", kind: "mcp", selected: [], attach_teams: [], write: true };
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

// Teams the connector may follow: any team the user belongs to for a personal
// connector, only its own team for a team-scoped one.
const followableTeams = computed<Team[]>(() => {
  const scope = form.team_id || "";
  if (scope === "") return props.teams;
  return props.teams.filter((t) => t.id === scope);
});

// The picker groups directories by where they come from: the user's own
// first, then one group per team. Each team group is one "all read-only
// shares" toggle (teammates' memories, current and future) plus individual
// rows for the team directories this user can write — writable memory stays
// a deliberate, explicit choice. A read-only directory that is still
// explicitly selected (from before the toggle existed) stays visible until
// deselected or covered by the toggle.
const personalDirs = computed<DirectoryView[]>(() => attachable.value.filter((d) => !d.team_id));
const teamGroups = computed(() =>
  followableTeams.value.map((team) => ({
    team,
    dirs: attachable.value.filter(
      (d) => d.team_id === team.id && (d.can_write || form.selected.includes(d.id)),
    ),
  })),
);

// A read-only directory covered by a followed team is included implicitly;
// writable directories are never covered by the team toggle.
function coveredByTeam(d: DirectoryView): boolean {
  return Boolean(d.team_id) && !d.can_write && form.attach_teams.includes(d.team_id || "");
}

function toggleTeam(id: string, event: Event): void {
  const checked = (event.target as HTMLInputElement).checked;
  const index = form.attach_teams.indexOf(id);
  if (checked && index === -1) {
    form.attach_teams.push(id);
    // Covered read-only directories no longer need an explicit selection.
    const covered = new Set(
      props.directories.filter((d) => d.team_id === id && !d.can_write).map((d) => d.id),
    );
    form.selected = form.selected.filter((did) => !covered.has(did));
  } else if (!checked && index >= 0) {
    form.attach_teams.splice(index, 1);
  }
}

const submitDisabled = computed(
  () =>
    submitting.value ||
    (!form.selected.length && !form.attach_teams.length) ||
    (isEdit.value && !form.name),
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
        attach_teams: (c.attach_teams || []).slice(),
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

// Switching team scope drops any now-unattachable selections and any followed
// teams outside the new scope.
function onTeamChange(event: Event): void {
  form.team_id = (event.target as HTMLSelectElement).value || "";
  const allowed = new Set(attachable.value.map((d) => d.id));
  form.selected = form.selected.filter((id) => allowed.has(id));
  const followable = new Set(followableTeams.value.map((t) => t.id));
  form.attach_teams = form.attach_teams.filter((id) => followable.has(id));
}

async function submit(): Promise<void> {
  err.value = "";
  submitting.value = true;
  const body: ConnectorRequest = {
    name: form.name,
    team_id: form.team_id || "",
    kind: form.kind,
    directory_ids: form.selected,
    attach_teams: form.attach_teams,
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

<style scoped>
/* Group headers inside the directory picker ("Your directories", team names). */
.check-group-label {
  margin: 0.65rem 0 0.35rem;
  font-size: 0.72rem;
  font-weight: 600;
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: var(--fg-3);
}
.check-group-label:first-of-type {
  margin-top: 0.1rem;
}
/* A team's individual directories sit under its follow-the-team toggle. */
.check-row-nested {
  margin-left: 1.25rem;
}
.check-row input:disabled {
  opacity: 0.6;
}
</style>
