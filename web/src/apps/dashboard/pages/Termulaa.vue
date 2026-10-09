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
          native Windows build — on Windows, install it inside WSL2. Mint a token here, then run
          one command on the machine: it installs termulaa, pairs it with this server and keeps
          it running.
        </span>
      </div>
    </div>

    <article class="setup-card">
      <div class="setup-card-head">
        <span class="step">Step 1</span>
        <h3>Mint a pairing token</h3>
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
          <input id="rc-ttl" class="input" v-model.number="mintTTL" type="number" min="1" max="36500" />
          <span v-if="longLivedToken" class="ttl-note">
            Long enough that expiry will not retire this token. Rotating
            MEMD_RC_TOKEN_SECRET is then the only way to revoke it.
          </span>
        </div>
        <button class="btn primary" type="submit" :disabled="minting">
          <MIcon :name="minting ? 'refresh-cw' : 'plus'" :class="minting ? 'spin' : ''" />
          Mint token
        </button>
      </form>
      <p class="setup-hint" v-if="!minted">
        The install command for the machine appears here once the token is minted.
      </p>
    </article>

    <article class="setup-card" v-if="minted">
      <div class="setup-card-head">
        <span class="step">Step 2</span>
        <h3>Run on the machine</h3>
      </div>
      <div class="mint-once">
        <MIcon name="triangle-alert" />
        <span>
          This token is shown once and never stored here — copy the command now. The token expires
          {{ formatDate(minted.expires_at) }}.
        </span>
      </div>
      <div class="seg-control setup-tabs" role="tablist" aria-label="Operating system">
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
        <code>{{ installCommand }}</code>
        <button
          class="icon-btn code-copy"
          type="button"
          :title="copiedKey === 'install' ? 'Copied' : 'Copy command'"
          :aria-label="copiedKey === 'install' ? 'Command copied' : 'Copy install command'"
          @click="copy(installCommand, 'install')"
        >
          <MIcon :name="copiedKey === 'install' ? 'check' : 'copy'" />
        </button>
      </div>
      <div class="pair-status" role="status">
        <span class="dot" :class="mintedAgent ? 'success' : ''">
          {{ mintedAgent ? pluralize(mintedAgent.tunnels, "tunnel") + " up" : "waiting for the agent" }}
        </span>
        <span v-if="mintedAgent">Connected — the machine is paired and running.</span>
        <span v-else>This updates by itself once the agent dials in.</span>
      </div>
      <p class="setup-hint">{{ activeInstall.hint }}</p>
      <p class="setup-hint">
        Already paired, or the token expired? Mint a new token and re-run the command: it
        re-pairs, restarts only the tunnel agent and leaves open terminals alone.
      </p>
      <p class="setup-hint" v-if="mintedOpenURL">
        Once the agent connects, its terminal opens at
        <a :href="mintedOpenURL" target="_blank" rel="noopener noreferrer">{{ mintedOpenText }}</a>
        — and it will show up in the sessions list above.
      </p>
      <p class="setup-hint" v-else>
        Once the agent connects, it will show up in the sessions list above.
      </p>

      <details class="go-alt">
        <summary>Go toolchain instead</summary>
        <div class="go-alt-body">
          <div class="code-block">
            <code>{{ goCommand }}</code>
            <button
              class="icon-btn code-copy"
              type="button"
              :title="copiedKey === 'go' ? 'Copied' : 'Copy commands'"
              :aria-label="copiedKey === 'go' ? 'Commands copied' : 'Copy Go install commands'"
              @click="copy(goCommand, 'go')"
            >
              <MIcon :name="copiedKey === 'go' ? 'check' : 'copy'" />
            </button>
          </div>
          <p class="setup-hint">
            Builds from source and saves the pairing to ~/.termulaa/rc.json. No service is set
            up: start the terminal server with "termulaa" and the agent with "termulaa -rc"
            yourself, and keep both running.
          </p>
        </div>
      </details>
    </article>
  </section>

  <section class="app-section">
    <div class="section-head">
      <div class="titles">
        <h2>What keeps running</h2>
        <span class="desc">
          How long a machine stays reachable depends on how the install command could start it.
          It uses the system's service manager where one serves the account, and otherwise
          falls back to running detached — the installer picks that by itself and says so.
        </span>
      </div>
    </div>

    <div class="mtable-wrap persist-table">
      <table class="mtable mtable-stack">
        <thead>
          <tr>
            <th>Started as</th>
            <th>Close the terminal</th>
            <th>Log out</th>
            <th>Restart the machine</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="row in persistence" :key="row.setup">
            <td data-label="Started as"><span class="cell-strong">{{ row.setup }}</span></td>
            <td data-label="Close the terminal">{{ row.terminal }}</td>
            <td data-label="Log out">{{ row.logout }}</td>
            <td data-label="Restart the machine">{{ row.restart }}</td>
          </tr>
        </tbody>
      </table>
    </div>

    <article class="setup-card">
      <ul class="persist-notes">
        <li v-for="note in persistenceNotes" :key="note.topic">
          <b>{{ note.topic }}</b>
          {{ note.text }}
        </li>
      </ul>
    </article>
  </section>

  <section class="app-section">
    <div class="section-head">
      <div class="titles">
        <h2>Updating</h2>
        <span class="desc">Nothing updates by itself — an update happens only when you run one of these.</span>
      </div>
    </div>

    <article class="setup-card">
      <ul class="persist-notes">
        <li>
          <b>Re-run the install command.</b>
          The full update: new binary, services restarted onto it. Open terminals stay open — a
          terminal server already on the new version is left untouched.
        </li>
        <li>
          <b>termulaa --update-check.</b>
          Downloads the latest release, verifies it and swaps the binary in place. Running
          services keep the old binary until they are restarted.
        </li>
      </ul>
    </article>
  </section>

  <section class="app-section">
    <div class="section-head">
      <div class="titles">
        <h2>Keyboard shortcuts</h2>
        <span class="desc">
          Inside a terminal tab. {{ shortcutGuideKey }} or the ? in the corner shows this list
          there, and every shortcut can be rebound.
        </span>
      </div>
    </div>

    <article class="setup-card">
      <ul class="shortcut-list">
        <li class="shortcut-row" v-for="s in shortcuts" :key="s.label">
          <span class="shortcut-label">{{ s.label }}</span>
          <kbd class="shortcut-key">{{ s.keys }}</kbd>
        </li>
      </ul>
      <p class="setup-hint" v-if="!isMac">
        These use Ctrl, so Ctrl+D (shell EOF) and Ctrl+W (word erase) go to termulaa instead of
        the shell — rebind them if you need those.
      </p>
    </article>
  </section>

  <section class="app-section">
    <div class="section-head">
      <div class="titles">
        <h2>termulaa on your phone</h2>
        <span class="desc">
          The Android app shows your machines, opens their terminals, and notifies you when a
          session produces output while you're away — useful when agents are running on remote
          machines. It signs in with the same memd account.
        </span>
      </div>
    </div>

    <article class="setup-card phone-card">
      <MIcon name="smartphone" class="phone-icon" />
      <div class="phone-body">
        <div class="phone-actions">
          <a
            class="btn primary"
            href="https://github.com/sudiptadeb/termulaa/releases/latest/download/termulaa.apk"
            download
          >
            <MIcon name="download" />
            Download the APK
          </a>
          <button class="btn secondary" type="button" :disabled="pairing" @click="pair">
            <MIcon :name="pairing ? 'refresh-cw' : 'plug'" :class="pairing ? 'spin' : ''" />
            Pair the app
          </button>
        </div>
        <p class="setup-hint">
          Android only. Sideloaded — your phone will ask you to allow installs from your browser.
          Source in the
          <a href="https://github.com/sudiptadeb/termulaa" target="_blank" rel="noopener noreferrer"
            >termulaa repo</a
          >.
        </p>

        <template v-if="pairCode">
          <span class="field-label">Enter this code in the app</span>
          <div class="pair-code-row">
            <code class="pair-code">{{ groupedPairCode }}</code>
            <span class="pair-countdown">expires in {{ pairCountdown }}</span>
            <button
              class="icon-btn"
              type="button"
              title="New code"
              aria-label="Mint a new pairing code"
              :disabled="pairing"
              @click="pair"
            >
              <MIcon name="refresh-cw" :class="pairing ? 'spin' : ''" />
            </button>
          </div>
          <p class="setup-hint">
            The code is single use and pairs the app with your account — no password needed, so
            this works with Google/SSO sign-in too.
            <a class="pair-link" :href="pairDeepLink"
              >Reading this on the phone? Tap here to pair.</a
            >
          </p>
        </template>
        <p class="setup-hint" v-else-if="pairExpired">
          The pairing code expired. Mint a new one with "Pair the app".
        </p>
      </div>
    </article>

    <article class="setup-card" v-if="phones.length">
      <div class="setup-card-head">
        <h3>Paired phones</h3>
      </div>
      <ul class="phone-list">
        <li class="phone-row" v-for="phone in phones" :key="phone.id">
          <MIcon name="smartphone" class="phone-row-icon" />
          <span class="phone-label">{{ phone.label || "(unnamed)" }}</span>
          <span class="phone-meta">
            paired {{ formatDate(phone.created_at) }}
            <template v-if="phone.last_used_at">
              · last used {{ formatDate(phone.last_used_at) }}</template
            >
          </span>
          <span class="spacer"></span>
          <button class="btn ghost" type="button" @click="revokePhone(phone)">Revoke</button>
        </li>
      </ul>
      <p class="setup-hint">
        Revoking un-pairs the phone: its next sign-in refresh fails and the app signs itself out.
      </p>
    </article>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { useRouter } from "vue-router";
import MIcon from "@/shared/components/MIcon.vue";
import { app as appApi, rc as rcApi, ApiError } from "@/shared/api";
import { toast } from "@/shared/bus";
import { useSession } from "@/shared/session";
import { copyToClipboard, formatDate, pluralize } from "@/shared/utils";
import { useRcAgents } from "../rcAgents";
import type { AppPairResponse, AppTokenView, MintRcTokenResponse, RcAgent } from "@/shared/types";

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
  // While a pairing code is showing, keep the phones list fresh so the phone
  // appears the moment it redeems the code.
  if (pairCode.value) void refreshPhones();
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
  void refreshPhones();
  document.addEventListener("visibilitychange", onVisibility);
});

onUnmounted(() => {
  stopPolling();
  stopPairTimer();
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
// The command is the same everywhere; the tabs only change the hint.
interface InstallTab {
  id: string;
  label: string;
  hint: string;
}

const serviceHint =
  "Installs or updates termulaa in ~/.local/bin, saves the pairing to ~/.termulaa/rc.json " +
  "(the token is not left on a long-lived command line) and starts the terminal server and " +
  "the tunnel agent as per-user services.";

const installTabs: InstallTab[] = [
  {
    id: "macos",
    label: "macOS",
    hint:
      serviceHint +
      " On macOS these are launchd LaunchAgents. Over SSH into an account with no desktop " +
      "login there is no launchd session to load them into; the installer says so and starts " +
      "both detached instead.",
  },
  {
    id: "linux",
    label: "Linux",
    hint:
      serviceHint +
      " On Linux these are systemd user units. Without a systemd user session the installer " +
      "says so and starts both detached instead.",
  },
  {
    id: "wsl",
    label: "Windows (WSL2)",
    hint:
      "Run this inside your WSL2 distribution — the terminal you get is a WSL shell, not " +
      "PowerShell. The services are systemd user units, so systemd must be enabled " +
      "([boot] systemd=true in /etc/wsl.conf). They stop soon after the last WSL window " +
      "closes (see \"What keeps running\" below).",
  },
];

const installTab = ref(installTabs[0].id);
const activeInstall = computed(
  () => installTabs.find((t) => t.id === installTab.value) ?? installTabs[0],
);

// --- Persistence ------------------------------------------------------------

// What each way the install command can start the agent survives. WSL2 is
// split out because its lifetime is bound to the WSL VM, not to the Linux
// service inside it. "Detached" is the installer's own fallback when no
// service manager serves the account.
const persistence = [
  {
    setup: "macOS service",
    terminal: "Keeps running",
    logout: "Stops",
    restart: "Starts when you log in at the desktop",
  },
  {
    setup: "Linux service",
    terminal: "Keeps running",
    logout: "Keeps running",
    restart: "Starts at boot, no login needed",
  },
  {
    setup: "WSL2 service",
    terminal: "Stops soon after the last WSL window closes",
    logout: "Stops",
    restart: "Starts when WSL is next started",
  },
  {
    setup: "Detached (no service manager)",
    terminal: "Keeps running",
    logout: "Keeps running",
    restart: "Stays down until you re-run the install command",
  },
];

const persistenceNotes = [
  {
    topic: "macOS.",
    text:
      "A Mac starts a user's services only when that user logs in at its desktop. An SSH login " +
      "does not count, and one user logging in does not start another user's services. With " +
      "FileVault on, nothing starts until the disk is unlocked. Starting at boot with nobody " +
      "logged in needs a LaunchDaemon, set up by hand with sudo.",
  },
  {
    topic: "Linux.",
    text:
      "Running without a login relies on lingering, which the installer switches on. If it " +
      "reported that it could not, run \"loginctl enable-linger\" yourself; without it the " +
      "services stop at logout and start at login.",
  },
  {
    topic: "WSL2.",
    text:
      "The services run only while WSL itself is running. By default Windows shuts WSL down " +
      "about a minute after its last window closes, and does not start it at boot. To keep the " +
      "machine reachable, start a hidden WSL process at Windows login and disable the idle " +
      "timeouts in .wslconfig.",
  },
  {
    topic: "Detached.",
    text:
      "Where no service manager serves the account — SSH into a Mac account with no desktop " +
      "login, Linux without a systemd user session — the installer picks this mode by itself " +
      "and says so: it starts both processes with nohup, so they survive closing the terminal " +
      "or an SSH disconnect, but not a reboot.",
  },
  {
    topic: "Your terminals.",
    text:
      "They live in the terminal server, not in the tunnel agent, so restarting the agent loses " +
      "nothing. When the terminal server restarts, running programs end; tabs, scrollback and " +
      "the working directory come back.",
  },
  {
    topic: "Token expiry.",
    text:
      "The agent exits once its token is no longer valid, however it was started. Mint a new " +
      "token and re-run the install command with it.",
  },
];

// --- Keyboard shortcuts -----------------------------------------------------

// termulaa's default bindings (its ui/keybindings.js), shown in the notation
// of the machine this dashboard is open on — that is where the keys are
// pressed. A user's own rebinds live in the terminal page's localStorage and
// are not visible from here.
const isMac = navigator.platform.indexOf("Mac") !== -1;

function shortcutKeys(key: string, shift = false): string {
  if (isMac) return (shift ? "⇧" : "") + "⌘" + key;
  return "Ctrl+" + (shift ? "Shift+" : "") + key;
}

const shortcuts = [
  { label: "Split pane vertically", keys: shortcutKeys("D") },
  { label: "Split pane horizontally", keys: shortcutKeys("D", true) },
  { label: "Close pane", keys: shortcutKeys("W") },
  { label: "Focus next pane", keys: shortcutKeys("]") },
  { label: "Focus previous pane", keys: shortcutKeys("[") },
  { label: "Shortcut guide", keys: shortcutKeys("/") },
];

const shortcutGuideKey = shortcutKeys("/");

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

// A year is where "it expires eventually" stops being a real control.
const longLivedToken = computed(() => Number(mintTTL.value) > 365);

async function mint(): Promise<void> {
  if (minting.value) return;
  minting.value = true;
  try {
    const ttl = Math.min(36500, Math.max(1, Math.floor(mintTTL.value || 30)));
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

// The one command to run on the target machine: install.sh installs or
// updates the binary, saves the pairing and starts both services (or runs them
// detached where no service manager serves the account). The server this
// dashboard is served from is the rendezvous, so the origin comes from the
// address bar — never a hardcoded hostname.
const installScriptURL = "https://raw.githubusercontent.com/sudiptadeb/termulaa/main/install.sh";

const pairFlags = computed(() => {
  if (!minted.value) return "";
  let flags = " --rc-server " + window.location.origin + " --rc-token " + shellQuote(minted.value.token);
  if (mintedLabel.value) flags += " --rc-label " + shellQuote(mintedLabel.value);
  return flags;
});

const installCommand = computed(() => {
  if (!minted.value) return "";
  return "curl -fsSL " + installScriptURL + " | bash -s -- --service" + pairFlags.value;
});

// The no-service alternative: build from source, save the pairing, and leave
// starting "termulaa" and "termulaa -rc" to the user.
const goCommand = computed(() => {
  if (!minted.value) return "";
  let save =
    "termulaa -rc-save -rc-server " + window.location.origin + " -rc-token " + shellQuote(minted.value.token);
  if (mintedLabel.value) save += " -rc-label " + shellQuote(mintedLabel.value);
  return "go install github.com/sudiptadeb/termulaa/src/cmd/termulaa@latest\n" + save;
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

// The live agent started with the freshly minted token, once it has dialled
// in. An agent id is the sha256 of its token and the list carries the first 8
// hex chars; path mode has the full id in open_url, host mode hashes the token
// here (crypto.subtle needs a secure context — without one the status simply
// stays on "waiting").
const mintedAgentId = ref("");

watch(minted, async (m) => {
  mintedAgentId.value = "";
  if (!m) return;
  const fromURL = m.open_url?.match(/\/rc\/t\/([0-9a-f]{8})/);
  if (fromURL) {
    mintedAgentId.value = fromURL[1];
    return;
  }
  if (!window.crypto?.subtle) return;
  const sum = await window.crypto.subtle.digest("SHA-256", new TextEncoder().encode(m.token));
  if (minted.value !== m) return;
  mintedAgentId.value = Array.from(new Uint8Array(sum).slice(0, 4), (b) =>
    b.toString(16).padStart(2, "0"),
  ).join("");
});

const mintedAgent = computed(() => {
  if (!mintedAgentId.value) return undefined;
  return agents.value.find((a) => a.id === mintedAgentId.value && a.tunnels > 0);
});

const mintedOpenText = computed(() => {
  if (!minted.value) return "";
  if (minted.value.open_url) return new URL(minted.value.open_url, window.location.origin).toString();
  if (viewHost.value) return "https://" + viewHost.value + "/";
  return "";
});

// --- Phone app pairing ------------------------------------------------------

// One outstanding code per user: "Pair the app" (and the regenerate button)
// mints a fresh code, replacing the previous one server-side. The code is
// short-lived (5 minutes) and single use, so it is fine to display in clear.
const pairing = ref(false);
const pairCode = ref<AppPairResponse | null>(null);
const pairExpired = ref(false);
const pairRemaining = ref(0); // whole seconds until expiry
let pairTimer: number | undefined;

async function pair(): Promise<void> {
  if (pairing.value) return;
  pairing.value = true;
  try {
    pairCode.value = await appApi.pair();
    pairExpired.value = false;
    tickPairCountdown();
    startPairTimer();
  } catch (e) {
    toast(e instanceof ApiError ? e.message : String(e), "error");
  } finally {
    pairing.value = false;
  }
}

function startPairTimer(): void {
  if (pairTimer !== undefined) return;
  pairTimer = window.setInterval(tickPairCountdown, 1000);
}

function stopPairTimer(): void {
  if (pairTimer === undefined) return;
  window.clearInterval(pairTimer);
  pairTimer = undefined;
}

function tickPairCountdown(): void {
  if (!pairCode.value) return;
  const left = Math.floor((new Date(pairCode.value.expires_at).getTime() - Date.now()) / 1000);
  pairRemaining.value = Math.max(0, left);
  if (left <= 0) {
    pairCode.value = null;
    pairExpired.value = true;
    stopPairTimer();
  }
}

// XXX-XXX-XXX, the form the app shows in its code field.
const groupedPairCode = computed(() => {
  const code = pairCode.value?.code ?? "";
  if (code.length !== 9) return code;
  return `${code.slice(0, 3)}-${code.slice(3, 6)}-${code.slice(6)}`;
});

const pairCountdown = computed(() => {
  const m = Math.floor(pairRemaining.value / 60);
  const s = pairRemaining.value % 60;
  return `${m}:${String(s).padStart(2, "0")}`;
});

// For people reading this dashboard on the phone itself: the app registers
// the termulaa://pair scheme and redeems the prefilled code straight away.
const pairDeepLink = computed(() => {
  if (!pairCode.value) return "";
  return (
    "termulaa://pair?server=" +
    encodeURIComponent(window.location.origin) +
    "&code=" +
    encodeURIComponent(pairCode.value.code)
  );
});

// --- Paired phones ----------------------------------------------------------

const phones = ref<AppTokenView[]>([]);

async function refreshPhones(): Promise<void> {
  try {
    phones.value = (await appApi.tokens()).tokens ?? [];
  } catch {
    // Non-fatal: the list refreshes on the next poll/action.
  }
}

async function revokePhone(phone: AppTokenView): Promise<void> {
  const name = phone.label || "this phone";
  if (!window.confirm(`Un-pair ${name}? The app on it will be signed out.`)) return;
  try {
    await appApi.revoke(phone.id);
    toast("Phone un-paired", "success");
  } catch (e) {
    toast(e instanceof ApiError ? e.message : String(e), "error");
  }
  await refreshPhones();
}
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
  /* Terminal URLs carry a 64-char agent id; let them wrap inside the card. */
  overflow-wrap: anywhere;
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

/* Go toolchain alternative: tucked away under the main command. */
.go-alt summary {
  width: fit-content;
  color: var(--fg-2);
  font-size: 12px;
  cursor: pointer;
}

.go-alt summary:hover {
  color: var(--fg-1);
}

.go-alt-body {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-top: 8px;
}

/* --- Persistence --- */
.persist-table {
  max-width: 760px;
}

.persist-table td {
  color: var(--fg-2);
  font-size: 13px;
}

.persist-notes {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin: 0;
  padding: 0;
  color: var(--fg-3);
  font-size: 12px;
  line-height: 1.5;
  list-style: none;
}

.persist-notes b {
  color: var(--fg-2);
  font-weight: 600;
}

/* Stacked on phones the value sits right of its label; let long ones wrap. */
@media (max-width: 640px) {
  .persist-table td {
    align-items: baseline;
    text-align: right;
  }

  .persist-table td::before {
    flex-shrink: 0;
    white-space: nowrap;
  }
}

/* --- Keyboard shortcuts --- */
.shortcut-list {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
  gap: 6px 24px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.shortcut-row {
  display: flex;
  gap: 12px;
  align-items: center;
  justify-content: space-between;
  color: var(--fg-2);
  font-size: 13px;
}

.shortcut-key {
  flex-shrink: 0;
  padding: 3px 7px;
  color: var(--fg-1);
  font-family: var(--font-mono);
  font-size: 11.5px;
  line-height: 1.3;
  background: var(--surface-2);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
}

/* --- Phone card --- */
.phone-card {
  flex-direction: row;
  gap: 14px;
  align-items: flex-start;
}

.phone-icon {
  flex-shrink: 0;
  width: 22px;
  height: 22px;
  margin-top: 3px;
  color: var(--accent);
}

.phone-body {
  display: flex;
  flex-direction: column;
  gap: 10px;
  align-items: flex-start;
  min-width: 0;
}

.phone-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  align-items: center;
}

/* --- App pairing --- */
.pair-code-row {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  align-items: center;
}

.pair-code {
  padding: 8px 12px;
  color: var(--fg-1);
  font-family: var(--font-mono);
  font-size: 20px;
  letter-spacing: 2px;
  background: var(--surface-2);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  user-select: all;
}

.pair-countdown {
  color: var(--fg-3);
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}

.pair-link {
  color: var(--accent);
}

.pair-link:hover {
  text-decoration: underline;
}

/* --- Paired phones --- */
.phone-list {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.phone-row {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
  padding: 8px 10px;
  background: var(--surface-2);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
}

.phone-row-icon {
  flex-shrink: 0;
  width: 14px;
  height: 14px;
  color: var(--accent);
}

.phone-label {
  color: var(--fg-1);
  font-size: 13px;
  font-weight: 550;
}

.phone-meta {
  color: var(--fg-3);
  font-size: 12px;
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

.ttl-note {
  display: block;
  margin-top: 6px;
  color: var(--fg-3);
  font-size: 12px;
  line-height: 1.4;
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

.pair-status {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
  color: var(--fg-2);
  font-size: 12px;
  line-height: 1.45;
}

.mint-once .icon {
  flex-shrink: 0;
  width: 13px;
  height: 13px;
  margin-top: 2px;
}
</style>
