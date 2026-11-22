<template>
  <div class="wrap">
    <header class="top">
      <a class="home" href="https://projectb.debkosh.com">
        <svg class="mark" viewBox="0 0 100 100" aria-hidden="true">
          <defs>
            <linearGradient id="pb-mark" x1="0" y1="0" x2="1" y2="1">
              <stop offset="0" stop-color="#0d7d86" />
              <stop offset="1" stop-color="#05515a" />
            </linearGradient>
          </defs>
          <rect width="100" height="100" rx="22" fill="url(#pb-mark)" />
          <path d="M28,20 H53 Q71,20 71,36 Q71,45 63,48.5 Q75,52 75,64 Q75,80 55,80 H28 Z" fill="#fff" />
          <circle cx="28" cy="20" r="5" fill="#5ff5cf" />
          <circle cx="28" cy="80" r="5" fill="#5ff5cf" />
        </svg>
        <span class="brand">Project B</span>
      </a>
      <span class="label">Sync</span>
      <span class="spacer"></span>
      <div class="who" v-if="user">
        <span class="email">{{ user.email || user.display_name || user.username }}</span>
        <button class="textbtn" type="button" @click="signOut">Sign out</button>
      </div>
    </header>

    <main v-if="loaded && !user">
      <div class="eyebrow">Sync · end-to-end encrypted</div>
      <h1>Sync your Project B devices through a server that can't read them.</h1>
      <p class="lede">
        Make a sync bucket here, paste its address into Project B on each device, and your bookmarks, passwords
        and history stay in step. Everything is encrypted on your device before it leaves.
      </p>
      <a class="gsi" :href="signInURL">
        <svg viewBox="0 0 48 48" aria-hidden="true">
          <path fill="#EA4335" d="M24 9.5c3.54 0 6.71 1.22 9.21 3.6l6.85-6.85C35.9 2.38 30.47 0 24 0 14.62 0 6.51 5.38 2.56 13.22l7.98 6.19C12.43 13.72 17.74 9.5 24 9.5z" />
          <path fill="#4285F4" d="M46.98 24.55c0-1.57-.15-3.09-.38-4.55H24v9.02h12.94c-.58 2.96-2.26 5.48-4.78 7.18l7.73 6c4.51-4.18 7.09-10.36 7.09-17.65z" />
          <path fill="#FBBC05" d="M10.53 28.59c-.48-1.45-.76-2.99-.76-4.59s.27-3.14.76-4.59l-7.98-6.19C.92 16.46 0 20.12 0 24c0 3.88.92 7.54 2.56 10.78l7.97-6.19z" />
          <path fill="#34A853" d="M24 48c6.48 0 11.93-2.13 15.89-5.81l-7.73-6c-2.15 1.45-4.92 2.3-8.16 2.3-6.26 0-11.57-4.22-13.47-9.91l-7.98 6.19C6.51 42.62 14.62 48 24 48z" />
        </svg>
        Sign in with Google
      </a>

      <h2>Don't trust this server. You don't have to.</h2>
      <div class="trust">
        <div>
          <h3>What it can't do</h3>
          <ul>
            <li>Read what you sync. It's encrypted with a key only your devices hold.</li>
            <li>Forge, change or delete your items without your devices noticing.</li>
          </ul>
        </div>
        <div>
          <h3>What it still sees</h3>
          <ul>
            <li>What kinds of things you sync, how many, their sizes, and when.</li>
            <li>It can hold items back, stop answering, or show a device an older copy.</li>
            <li>It can guess at your passphrase, so choose a long one.</li>
          </ul>
        </div>
      </div>
      <p class="note" style="margin-top: 14px">
        Details in <a :href="sealingURL">how synckosh encrypts your data</a>.
      </p>

      <h2>Rather run your own?</h2>
      <p class="note">
        The server is open source and small. <a :href="selfHostURL">Self-host it</a> and point Project B at your own
        address instead.
      </p>
    </main>

    <main v-else-if="loaded && user">
      <section class="fresh" v-if="fresh && fresh.token_url">
        <h2>“{{ fresh.name }}” is ready</h2>
        <p class="note">
          This address is the key to your bucket. Keep it like a password: anyone with it can sync to this bucket,
          though not read it.
        </p>
        <div class="addr">
          <code class="shown">{{ fresh.token_url }}</code>
          <button class="btn small" type="button" @click="copy(fresh.token_url)">Copy</button>
        </div>
        <div class="actions">
          <a class="btn primary" :href="syncDeepLink(fresh.token_url)">Open in Project B</a>
          <button class="btn" type="button" @click="fresh = null">Done</button>
        </div>
        <ol class="steps">
          <li>On this device, Open in Project B fills in Sync settings for you.</li>
          <li>On your other devices, paste the address in Settings › Sync.</li>
          <li>The first device sets a passphrase; the others join with it.</li>
        </ol>
      </section>

      <div class="listhead">
        <h1>Your sync buckets</h1>
        <span class="spacer"></span>
        <button class="btn primary small" type="button" @click="startNew" v-if="!adding">+ New bucket</button>
      </div>
      <form class="newform" v-if="adding" @submit.prevent="create">
        <input ref="nameInput" v-model="newName" maxlength="64" placeholder="Name, like Personal" required />
        <button class="btn primary small" type="submit" :disabled="busy || !newName.trim()">Create</button>
        <button class="btn small" type="button" @click="adding = false">Cancel</button>
      </form>
      <p class="err" v-if="error">{{ error }}</p>
      <p class="note">
        One bucket per set of devices that share data. Counts are what the server can see, never what's inside.
      </p>

      <article class="bucket" v-for="b in buckets" :key="b.id">
        <div class="row">
          <span class="name">{{ b.name }}</span>
          <span class="spacer"></span>
          <span class="state" v-if="b.stats && recent(b.stats.newest)">last change {{ ago(b.stats.newest) }}</span>
          <span class="state idle" v-else-if="b.stats">quiet for {{ ago(b.stats.newest, false) }}</span>
          <span class="state idle" v-else>nothing synced yet</span>
        </div>
        <div class="stats" v-if="b.stats">
          <div class="stat" v-for="c in b.stats.collections" :key="c.name">
            <b>{{ c.items.toLocaleString() }}</b>{{ c.name }}
          </div>
          <div class="stat"><b>{{ size(b.stats.bytes) }}</b>stored</div>
        </div>
        <template v-if="b.token_url">
          <div class="addr">
            <code :class="{ shown: shown[b.id] }">{{ shown[b.id] ? b.token_url : masked(b.token_url) }}</code>
            <button class="btn small" type="button" @click="shown[b.id] = !shown[b.id]">
              {{ shown[b.id] ? "Hide" : "Show" }}
            </button>
            <button class="btn small" type="button" @click="copy(b.token_url)">Copy</button>
          </div>
          <div class="actions">
            <a class="btn small" :href="syncDeepLink(b.token_url)">Open in Project B</a>
            <span class="spacer"></span>
            <span class="quiet">
              <button class="textbtn" type="button" @click="exportOpen[b.id] = !exportOpen[b.id]">Export…</button>
              <button class="textbtn" type="button" @click="reset(b)">Reset</button>
              <button class="textbtn danger" type="button" @click="remove(b)">Delete</button>
            </span>
          </div>
          <div class="export" v-if="exportOpen[b.id]">
            <span class="note">Everything this server holds for this bucket, still encrypted. Only your devices can open it.</span>
            <button class="btn small" type="button" :disabled="exporting[b.id]" @click="download(b)">
              {{ exporting[b.id] ? "Preparing…" : "Download" }}
            </button>
          </div>
          <p class="err" v-if="exportErr[b.id]" style="margin: 8px 0 0">{{ exportErr[b.id] }}</p>
        </template>
        <div class="actions" v-else>
          <span class="note" style="margin: 0">This bucket's address can be shown again after a new token, on the memd dashboard.</span>
          <span class="spacer"></span>
          <button class="textbtn danger" type="button" @click="remove(b)">Delete</button>
        </div>
      </article>
      <div class="empty" v-if="!buckets.length">No buckets yet. Make one, then add its address to Project B.</div>

      <footer class="foot">
        <span>Hosted on {{ host }}</span>
        <a :href="sealingURL">What the server sees</a>
        <a :href="selfHostURL">Self-host</a>
      </footer>
    </main>
  </div>
</template>

<script setup lang="ts">
import { nextTick, onMounted, reactive, ref } from "vue";
import { auth, session, sync, ApiError } from "@/shared/api";
import { copyToClipboard } from "@/shared/utils";
import { syncDeepLink } from "@/apps/dashboard/components/syncLink";
import type { SessionUser, SyncBucketView } from "@/shared/types";

const sealingURL = "https://github.com/sudiptadeb/synckosh/blob/main/SPEC.md#what-sealing-protects";
const selfHostURL = "https://github.com/sudiptadeb/memd/blob/main/docs/self-hosting.md";
const signInURL = auth.ssoLoginUrl("/synckosh");
const host = location.host;

const loaded = ref(false);
const user = ref<SessionUser | null>(null);
const buckets = ref<SyncBucketView[]>([]);
const shown = reactive<Record<string, boolean>>({});
const exportOpen = reactive<Record<string, boolean>>({});
const exporting = reactive<Record<string, boolean>>({});
const exportErr = reactive<Record<string, string>>({});
const fresh = ref<SyncBucketView | null>(null);
const adding = ref(false);
const newName = ref("");
const busy = ref(false);
const error = ref("");
const nameInput = ref<HTMLInputElement | null>(null);

function errText(e: unknown): string {
  return e instanceof ApiError ? e.message : String(e);
}

async function reload(): Promise<void> {
  try {
    buckets.value = (await sync.list()).buckets || [];
  } catch (e) {
    error.value = errText(e);
  }
}

async function startNew(): Promise<void> {
  adding.value = true;
  newName.value = "";
  error.value = "";
  await nextTick();
  nameInput.value?.focus();
}

async function create(): Promise<void> {
  busy.value = true;
  error.value = "";
  try {
    fresh.value = (await sync.create(newName.value.trim())).bucket;
    adding.value = false;
    await reload();
  } catch (e) {
    error.value = errText(e);
  } finally {
    busy.value = false;
  }
}

async function reset(b: SyncBucketView): Promise<void> {
  const q = "Reset " + b.name + "? The server forgets what it holds, and each device asks to set sync up again.";
  if (!window.confirm(q)) return;
  try {
    await sync.reset(b.id);
  } catch (e) {
    error.value = errText(e);
  }
  await reload();
}

async function remove(b: SyncBucketView): Promise<void> {
  if (!window.confirm("Delete " + b.name + " and everything synced to it? Its address stops working.")) return;
  try {
    await sync.remove(b.id);
    if (fresh.value?.id === b.id) fresh.value = null;
  } catch (e) {
    error.value = errText(e);
  }
  await reload();
}

async function download(b: SyncBucketView): Promise<void> {
  exporting[b.id] = true;
  exportErr[b.id] = "";
  try {
    const res = await sync.export(b.id);
    if (res.status === 429) {
      const min = Math.max(1, Math.ceil(Number(res.headers.get("Retry-After") || 60) / 60));
      exportErr[b.id] = "Too many exports for now. Try again in " + min + (min === 1 ? " minute." : " minutes.");
      return;
    }
    if (!res.ok) {
      exportErr[b.id] = "Export failed (" + res.status + ").";
      return;
    }
    const name = /filename="([^"]+)"/.exec(res.headers.get("Content-Disposition") || "")?.[1] || "bucket.synckosh.jsonl";
    const url = URL.createObjectURL(await res.blob());
    const a = document.createElement("a");
    a.href = url;
    a.download = name;
    a.click();
    URL.revokeObjectURL(url);
    exportOpen[b.id] = false;
  } catch (e) {
    exportErr[b.id] = errText(e);
  } finally {
    exporting[b.id] = false;
  }
}

async function copy(text: string): Promise<void> {
  if (!(await copyToClipboard(text))) error.value = "Copy failed. Show the address and copy it by hand.";
}

async function signOut(): Promise<void> {
  const out = await auth.logout();
  if (out.logout_url) location.href = out.logout_url;
  else location.reload();
}

// The address with its token hidden: everything up to the last "/".
function masked(url: string): string {
  return url.slice(0, url.lastIndexOf("/") + 1) + "•".repeat(20);
}

function recent(ms: number): boolean {
  return Date.now() - ms < 24 * 3600 * 1000;
}

function ago(ms: number, withAgo = true): string {
  const min = Math.max(0, Math.round((Date.now() - ms) / 60000));
  const days = Math.round(min / 1440);
  const text =
    min < 1 ? "now" : min < 60 ? min + " min" : min < 1440 ? Math.round(min / 60) + " h" : days + (days === 1 ? " day" : " days");
  if (text === "now") return withAgo ? "just now" : "a moment";
  return withAgo ? text + " ago" : text;
}

function size(bytes: number): string {
  if (bytes < 1024) return bytes + " B";
  if (bytes < 1024 * 1024) return Math.round(bytes / 1024) + " KB";
  return (bytes / (1024 * 1024)).toFixed(1) + " MB";
}

onMounted(async () => {
  try {
    user.value = (await session.get()).user;
    if (user.value) await reload();
  } catch (e) {
    error.value = errText(e);
  } finally {
    loaded.value = true;
  }
});
</script>
