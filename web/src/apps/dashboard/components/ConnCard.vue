<template>
  <article
    class="card connector-card"
    :class="expanded ? 'expanded' : ''"
    @click="expanded = !expanded"
    @keydown.enter.prevent="expanded = !expanded"
    @keydown.space.prevent="expanded = !expanded"
    tabindex="0"
    role="button"
    :aria-expanded="expanded ? 'true' : 'false'"
  >
    <div class="card-head">
      <span class="card-name">{{ connector.name }}</span>
      <span class="dot accent" v-if="connector.team_id">{{ connector.team_name || teamLabel }}</span>
      <span class="dot">{{ connector.kind === "http" ? "http" : "mcp" }}</span>
      <span class="dot" :class="connector.write ? 'accent' : ''">{{
        connector.write ? "read/write" : "read-only"
      }}</span>
      <span class="spacer"></span>
      <MIcon name="chevron-right" class="expand-icon" />
    </div>
    <div class="card-meta">Access to <b>{{ directoryNames }}</b></div>
    <div class="connector-fold" v-if="expanded" @click.stop>
      <div class="connector-instructions">
        <div class="instruction-line">
          {{
            connector.kind === "http"
              ? "Use these for HTTP-capable agents that cannot speak MCP."
              : "Use these for MCP clients that support request headers."
          }}
        </div>
        <div class="instruction-grid">
          <span>URL</span>
          <code>{{ instructionURL }}</code>
          <span>Header</span>
          <code>{{ connector.auth_header }}</code>
        </div>
        <div class="instruction-line muted">
          {{
            connector.kind === "http"
              ? "Start with GET /memory_load using the Authorization header."
              : "Configure Streamable HTTP with the URL and Authorization header."
          }}
        </div>
      </div>
      <div class="url-row">
        <code :class="revealed ? 'revealed' : ''">{{ revealed ? connector.url : truncate(connector.url) }}</code>
        <div class="url-actions">
          <button type="button" @click="revealed = !revealed" :title="revealed ? 'Hide URL' : 'Reveal URL'">
            <MIcon v-if="!revealed" name="eye" />
            <MIcon v-else name="eye-off" />
            <span>{{ revealed ? "Hide" : "Reveal" }}</span>
          </button>
          <button type="button" @click="copyUrl">
            <MIcon name="copy" />
            Copy
          </button>
          <button type="button" @click="copyAuth">
            <MIcon name="copy" />
            Copy auth
          </button>
          <button type="button" v-if="connector.kind === 'http'" @click="copySkill">
            <MIcon name="copy" />
            Copy skill
          </button>
        </div>
      </div>
      <div class="card-foot">
        <span class="spacer"></span>
        <button class="btn ghost" type="button" v-if="connector.can_manage" @click="emit('edit', connector)">
          <MIcon name="pencil" />
          Edit
        </button>
        <button class="btn ghost" type="button" v-if="connector.can_manage" @click="rotate">
          <MIcon name="refresh-cw" />
          Rotate
        </button>
        <button class="btn danger" type="button" v-if="connector.can_manage" @click="remove">
          <MIcon name="trash-2" />
          Delete
        </button>
      </div>
    </div>
  </article>
</template>

<script setup lang="ts">
import { computed, ref } from "vue";
import MIcon from "@/shared/components/MIcon.vue";
import { connectors as connectorsApi, ApiError } from "@/shared/api";
import { toast } from "@/shared/bus";
import { copyToClipboard } from "@/shared/utils";
import {
  connectorInstructionURL,
  connectorPath,
  publicURL,
  tokenlessConnectorURL,
  truncate,
} from "./connUrls";
import type { ConnectorView, DirectoryView } from "@/shared/types";

// One connector card: collapsed summary that expands to instructions, the
// (revealable) secret URL, copy helpers, and manage actions.
const props = defineProps<{
  connector: ConnectorView;
  directories: DirectoryView[];
  teamLabel: string;
}>();
const emit = defineEmits<{ (e: "edit", connector: ConnectorView): void; (e: "changed"): void }>();

const expanded = ref(false);
const revealed = ref(false);

const instructionURL = computed(() => connectorInstructionURL(props.connector));

const directoryNames = computed(() => {
  const c = props.connector;
  if (c.directory_names) return c.directory_names;
  const names = (c.directory_ids || []).map((id) => {
    const directory = props.directories.find((d) => d.id === id);
    return directory ? directory.name : "(missing)";
  });
  return names.length ? names.join(", ") : "(none)";
});

async function copy(text: string, label: string): Promise<void> {
  const ok = await copyToClipboard(text);
  toast(ok ? label : "Copy failed", ok ? "success" : "error");
}

function copyUrl(): void {
  void copy(publicURL(props.connector.url), "URL copied");
}

function copyAuth(): void {
  void copy("URL: " + tokenlessConnectorURL(props.connector) + "\n" + props.connector.auth_header, "Auth copied");
}

// Fetch the HTTP connector's generated skill file and copy its body.
async function copySkill(): Promise<void> {
  try {
    const path = connectorPath(props.connector);
    if (!path) {
      throw new Error("missing connector path");
    }
    const response = await fetch(path, { cache: "no-store" });
    if (!response.ok) {
      throw new Error(response.statusText || "request failed");
    }
    await copyToClipboard(await response.text());
    toast("Skill copied", "success");
  } catch {
    toast("Copy failed", "error");
  }
}

async function rotate(): Promise<void> {
  const message =
    "Rotate token for " +
    props.connector.name +
    "? The current URL stops working immediately and you will need to paste the new one into the agent.";
  if (!window.confirm(message)) return;
  try {
    await connectorsApi.rotate(props.connector.id);
    toast("Token rotated — reveal the connector for its new URL", "success");
    emit("changed");
  } catch (e) {
    toast(e instanceof ApiError ? e.message : String(e), "error");
    emit("changed");
  }
}

async function remove(): Promise<void> {
  if (!window.confirm("Delete connector " + props.connector.name + "?")) return;
  try {
    await connectorsApi.remove(props.connector.id);
    emit("changed");
  } catch (e) {
    toast("Could not delete connector: " + (e instanceof ApiError ? e.message : String(e)), "error");
    emit("changed");
  }
}
</script>
