<template>
  <section class="app-section">
    <div class="section-head">
      <div class="titles">
        <h2>termulaa <span class="count" v-if="loaded">{{ liveCount }}</span></h2>
        <span class="desc">
          Browser terminals on your own machines. The termulaa agent dials out to this server —
          no inbound ports — and each connected machine shows up here as a session.
        </span>
      </div>
      <span class="spacer"></span>
      <button class="btn secondary" type="button" v-if="agents.length" @click="toggleSetup">
        <MIcon :name="showSetup ? 'x' : 'plus'" />
        <span class="btn-label">{{ showSetup ? "Hide setup" : "Set up another machine" }}</span>
      </button>
    </div>

    <div class="cards" v-if="agents.length">
      <component
        v-for="agent in agents"
        :key="agent.id"
        :is="sessionHref(agent) ? 'a' : 'article'"
        class="card session-card"
        :class="agent.tunnels > 0 ? '' : 'muted-card'"
        :href="sessionHref(agent) || undefined"
        :target="sessionHref(agent) ? '_blank' : undefined"
        :rel="sessionHref(agent) ? 'noopener noreferrer' : undefined"
        :title="sessionHref(agent) ? 'Open terminal in a new tab' : undefined"
      >
        <div class="card-head">
          <MIcon name="terminal" class="session-icon" />
          <span class="card-name">{{ agent.label || "(unnamed)" }}</span>
          <span class="dot" :class="agent.tunnels > 0 ? 'success' : ''">
            {{ agent.tunnels > 0 ? pluralize(agent.tunnels, "tunnel") + " up" : "offline" }}
          </span>
          <span class="spacer"></span>
          <MIcon v-if="sessionHref(agent)" name="external-link" class="open-icon" />
        </div>
        <div class="card-meta">
          <code class="session-id">{{ agent.id }}</code> · local port <b>{{ agent.port }}</b> ·
          connected {{ formatDate(agent.connected_at) }}
        </div>
      </component>
    </div>

    <div class="empty" v-else-if="loaded">
      <div class="empty-icon"><MIcon name="terminal" /></div>
      <h4>No active sessions</h4>
      <p>
        Install the termulaa agent on a machine and pair it below. Its terminal appears here the
        moment the agent connects.
      </p>
    </div>
  </section>

  <section class="app-section" v-if="showSetup">
    <div class="section-head">
      <div class="titles">
        <h2>Set up a machine</h2>
        <span class="desc">
          termulaa runs on macOS and Linux. Its PTY layer is POSIX-only, so there is no
          native Windows build — on Windows, install it inside WSL2. Install it, then pair
          it with a token minted here.
        </span>
      </div>
    </div>

    <article class="setup-card">
      <div class="setup-card-head">
        <span class="step">Step 1</span>
        <h3>Install termulaa</h3>
      </div>
      <div class="seg-control setup-tabs" role="tablist" aria-label="Installation method">
        <button
          v-for="tab in installTabs"
          :key="tab.id"
          type="button"
          role="tab"
          :aria-selected="installTab === tab.id ? 'true' : 'false'"
          :class="installTab === tab.id ? 'on' : ''"
          @click="installTab = tab.id"
        >
          {{ tab.label }}
        </button>
      </div>
      <div class="code-block">
        <code>{{ activeInstall.command }}</code>
        <button
          class="icon-btn code-copy"
          type="button"
          :title="copiedKey === 'install' ? 'Copied' : 'Copy command'"
          :aria-label="copiedKey === 'install' ? 'Command copied' : 'Copy install command'"
          @click="copy(activeInstall.command, 'install')"
        >
          <MIcon :name="copiedKey === 'install' ? 'check' : 'copy'" />
        </button>
      </div>
      <p class="setup-hint">{{ activeInstall.hint }}</p>
    </article>

    <article class="setup-card">
      <div class="setup-card-head">
        <span class="step">Step 2</span>
        <h3>Pair it with this server</h3>
      </div>
      <form class="mint-form" @submit.prevent="mint">
        <div class="mint-field">
          <label class="field-label" for="rc-label">Label</label>
          <input
            id="rc-label"
            class="input"
            v-model="mintLabel"
            maxlength="64"
            placeholder="my-laptop"
            autocomplete="off"
          />
        </div>
        <div class="mint-field mint-ttl">
          <label class="field-label" for="rc-ttl">Valid for (days)</label>
          <input id="rc-ttl" class="input" v-model.number="mintTTL" type="number" min="1" max="90" />
        </div>
        <button class="btn primary" type="submit" :disabled="minting">
          <MIcon :name="minting ? 'refresh-cw' : 'plus'" :class="minting ? 'spin' : ''" />
          Mint token
        </button>
      </form>

      <template v-if="minted">
        <div class="mint-once">
          <MIcon name="triangle-alert" />
          <span>
            This token is shown once and never stored here — copy the command now. The token expires
            {{ formatDate(minted.expires_at) }}.
          </span>
        </div>
        <span class="field-label">Run on the machine</span>
        <div class="code-block">
          <code>{{ pairCommand }}</code>
          <button
            class="icon-btn code-copy"
            type="button"
            :title="copiedKey === 'pair' ? 'Copied' : 'Copy command'"
            :aria-label="copiedKey === 'pair' ? 'Command copied' : 'Copy pairing command'"
            @click="copy(pairCommand, 'pair')"
          >
            <MIcon :name="copiedKey === 'pair' ? 'check' : 'copy'" />
          </button>
        </div>
        <p class="setup-hint" v-if="mintedOpenURL">
          Once the agent connects, its terminal opens at
          <a :href="mintedOpenURL" target="_blank" rel="noopener noreferrer">{{ mintedOpenText }}</a>
          — and it will show up in the sessions list above.
        </p>
        <p class="setup-hint" v-else>
          Once the agent connects, it will show up in the sessions list above.
        </p>
      </template>
    </article>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { useRouter } from "vue-router";
import MIcon from "@/shared/components/MIcon.vue";
import { rc as rcApi, ApiError } from "@/shared/api";
import { toast } from "@/shared/bus";
import { useSession } from "@/shared/session";
import { copyToClipboard, formatDate, pluralize } from "@/shared/utils";
import { useRcAgents } from "../rcAgents";
import type { MintRcTokenResponse, RcAgent } from "@/shared/types";

// The termulaa section: live remote-terminal sessions (each opens in a new
// tab) plus the install-and-pair flow. The rc feature is opt-in server-side;
// the route guards itself via the /api/session capability flag.

const router = useRouter();
const { checked, rcEnabled } = useSession();
const { agents, viewHost, loaded, liveCount, refresh } = useRcAgents();

// The feature is opt-in: if the server does not mount the rc routes, this
// page has nothing to talk to — bounce to the default view.
watch(
  [checked, rcEnabled],
  () => {
    if (checked.value && !rcEnabled.value) void router.replace("/directories");
  },
  { immediate: true },
);

// --- Sessions ---------------------------------------------------------------

// Where a session opens. Path mode carries a per-agent URL; host mode serves
// every terminal on the dedicated view host. Offline agents (zero tunnels)
// get no link — there is no live terminal to open.
function sessionHref(agent: RcAgent): string {
  if (agent.tunnels <= 0) return "";
  if (agent.url) return new URL(agent.url, window.location.origin).toString();
  if (viewHost.value) return "https://" + viewHost.value + "/";
  return "";
}

// ~5s polling while the page is visible; fully torn down on hide/unmount.
let pollTimer: number | undefined;
let warnedOnce = false;

async function poll(): Promise<void> {
  try {
    await refresh();
    warnedOnce = false;
  } catch (e) {
    if (!warnedOnce) {
      warnedOnce = true;
      toast(e instanceof ApiError ? e.message : String(e), "error");
    }
  }
}

function startPolling(): void {
  if (pollTimer !== undefined) return;
  pollTimer = window.setInterval(() => void poll(), 5000);
}

function stopPolling(): void {
  if (pollTimer === undefined) return;
  window.clearInterval(pollTimer);
  pollTimer = undefined;
}

function onVisibility(): void {
  if (document.hidden) {
    stopPolling();
  } else {
    void poll();
    startPolling();
  }
}

onMounted(() => {
  void poll();
  startPolling();
  document.addEventListener("visibilitychange", onVisibility);
});

onUnmounted(() => {
  stopPolling();
  document.removeEventListener("visibilitychange", onVisibility);
});

// --- Setup visibility -------------------------------------------------------

// First-run (no sessions yet): setup is the page, expanded. With sessions it
// collapses behind "Set up another machine". An explicit toggle wins; minting
// pins it open so a freshly shown token never vanishes mid-pairing.
const setupOpen = ref<boolean | null>(null);
const showSetup = computed(() => setupOpen.value ?? (loaded.value && agents.value.length === 0));

function toggleSetup(): void {
  setupOpen.value = !showSetup.value;
  if (!setupOpen.value) minted.value = null;
}

// --- Install tabs -----------------------------------------------------------

// termulaa's PTY layer is POSIX-only, so there is no native Windows build.
// WSL2 is a real Linux kernel, so the Linux binary runs there unchanged — the
// terminal you get is a WSL shell, which is normally what is wanted anyway.
const installScript =
  "curl -fsSL https://raw.githubusercontent.com/sudiptadeb/termulaa/main/install.sh | bash -s -- --service";

interface InstallTab {
  id: string;
  label: string;
  command: string;
  hint: string;
}

const installTabs: InstallTab[] = [
  {
    id: "macos",
    label: "macOS",
    command: installScript,
    hint: "Installs the latest release and registers it as a per-user service, so the agent survives reboots.",
  },
  {
    id: "linux",
    label: "Linux",
    command: installScript,
    hint: "Installs the latest release and registers it as a per-user service, so the agent survives reboots.",
  },
  {
    id: "wsl",
    label: "Windows (WSL2)",
    command: installScript,
    hint:
      "Run this inside your WSL2 distribution — the terminal you get is a WSL shell, not PowerShell. " +
      "The agent only runs while WSL is running, and --service needs systemd enabled " +
      "([boot] systemd=true in /etc/wsl.conf).",
  },
  {
    id: "go",
    label: "Go",
    command: "go install github.com/sudiptadeb/termulaa/src/cmd/termulaa@latest",
    hint: "Builds from source with your own Go toolchain. No service is set up — you run the agent yourself.",
  },
];

const installTab = ref(installTabs[0].id);
const activeInstall = computed(
  () => installTabs.find((t) => t.id === installTab.value) ?? installTabs[0],
);

// --- Copy (shared confirmation state) ---------------------------------------

const copiedKey = ref("");
let copiedTimer: ReturnType<typeof setTimeout> | undefined;

async function copy(text: string, key: string): Promise<void> {
  // copyToClipboard degrades gracefully (returns false) when the Clipboard
  // API is unavailable, e.g. in insecure contexts.
  const ok = await copyToClipboard(text);
  toast(ok ? "Copied" : "Copy failed", ok ? "success" : "error");
  if (!ok) return;
  copiedKey.value = key;
  if (copiedTimer) clearTimeout(copiedTimer);
  copiedTimer = setTimeout(() => {
    copiedKey.value = "";
  }, 1500);
}

// --- Minting ----------------------------------------------------------------

const mintLabel = ref("");
const mintTTL = ref(30);
const minting = ref(false);
// The minted token lives only in this ref for the lifetime of the view — it
// is a secret shown once and is never persisted anywhere.
const minted = ref<MintRcTokenResponse | null>(null);
const mintedLabel = ref("");

async function mint(): Promise<void> {
  if (minting.value) return;
  minting.value = true;
  try {
    const ttl = Math.min(90, Math.max(1, Math.floor(mintTTL.value || 30)));
    const res = await rcApi.mintToken({ label: mintLabel.value.trim(), ttl });
    minted.value = res;
    mintedLabel.value = mintLabel.value.trim();
    // Keep the setup section pinned open while the one-time token is showing.
    setupOpen.value = true;
  } catch (e) {
    toast(e instanceof ApiError ? e.message : String(e), "error");
  } finally {
    minting.value = false;
  }
}

function shellQuote(value: string): string {
  return "'" + value.replace(/'/g, "'\\''") + "'";
}

// The one command to run on the target machine. The server this dashboard is
// served from is the rendezvous, so the origin comes from the address bar —
// never a hardcoded hostname.
const pairCommand = computed(() => {
  if (!minted.value) return "";
  let cmd =
    "termulaa -rc -rc-server " + window.location.origin + " -rc-token " + shellQuote(minted.value.token);
  if (mintedLabel.value) cmd += " -rc-label " + shellQuote(mintedLabel.value);
  return cmd;
});

// Where the paired terminal will open: the token's path-mode URL, or the
// dedicated view host (with the pairing token) in host mode.
const mintedOpenURL = computed(() => {
  if (!minted.value) return "";
  if (minted.value.open_url) return new URL(minted.value.open_url, window.location.origin).toString();
  if (viewHost.value) {
    return "https://" + viewHost.value + "/?t=" + encodeURIComponent(minted.value.token);
  }
  return "";
});

const mintedOpenText = computed(() => {
  if (!minted.value) return "";
  if (minted.value.open_url) return new URL(minted.value.open_url, window.location.origin).toString();
  if (viewHost.value) return "https://" + viewHost.value + "/";
  return "";
});
</script>

<style scoped>
/* --- Sessions --- */
.session-card {
  cursor: default;
}

a.session-card {
  cursor: pointer;
}

.session-icon {
  flex-shrink: 0;
  width: 15px;
  height: 15px;
  color: var(--accent);
}

.muted-card .session-icon {
  color: var(--fg-3);
}

.open-icon {
  flex-shrink: 0;
  width: 14px;
  height: 14px;
  color: var(--fg-3);
  transition: color var(--dur-fast);
}

a.session-card:hover .open-icon {
  color: var(--fg-1);
}

.session-id {
  font-family: var(--font-mono);
  font-size: 11.5px;
}

/* --- Setup cards --- */
.setup-card {
  display: flex;
  flex-direction: column;
  gap: 10px;
  max-width: 760px;
  padding: 16px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
}

.setup-card-head {
  display: flex;
  gap: 10px;
  align-items: baseline;
}

.setup-card-head h3 {
  color: var(--fg-1);
  font-size: 14px;
  font-weight: 650;
  line-height: 1.2;
}

.setup-tabs {
  max-width: 340px;
}

.setup-hint {
  color: var(--fg-3);
  font-size: 12px;
  line-height: 1.5;
}

.setup-hint a {
  color: var(--accent);
}

.setup-hint a:hover {
  text-decoration: underline;
}

/* Command code block: house pattern from the How-it-works page. */
.code-block {
  display: flex;
  gap: 8px;
  align-items: center;
  padding: 10px 10px 10px 12px;
  background: var(--surface-2);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
}

.code-block code {
  flex: 1;
  min-width: 0;
  color: var(--fg-1);
  font-family: var(--font-mono);
  font-size: 12px;
  line-height: 1.5;
  /* Wrap rather than scroll: these are commands people are asked to paste into
     a shell, and a tail hidden off-screen is a tail they cannot vet. */
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}


.code-copy {
  flex-shrink: 0;
}

/* --- Mint form --- */
.mint-form {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  align-items: flex-end;
}

.mint-field {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: 5px;
  min-width: 160px;
}

.mint-field.mint-ttl {
  flex: none;
  width: 130px;
  min-width: 0;
}

.mint-once {
  display: flex;
  gap: 8px;
  align-items: flex-start;
  padding: 8px 10px;
  color: var(--warning);
  font-size: 12px;
  line-height: 1.45;
  background: color-mix(in oklab, var(--warning) 9%, transparent);
  border-radius: var(--radius-sm);
}

.mint-once .icon {
  flex-shrink: 0;
  width: 13px;
  height: 13px;
  margin-top: 2px;
}
</style>
