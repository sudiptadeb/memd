<template>
  <section class="app-section">
    <div class="section-head">
      <div class="titles">
        <h2>Sync buckets <span class="count" v-if="loaded">{{ buckets.length }}</span></h2>
        <span class="desc">
          Each bucket is one synced space for your devices. memd keeps what they send sealed and
          cannot read it.
        </span>
      </div>
      <span class="spacer"></span>
      <button class="btn secondary" type="button" @click="openNew">
        <MIcon name="plus" />
        <span class="btn-label">Add bucket</span>
      </button>
    </div>

    <article class="card secret-card" v-if="secret">
      <div class="card-head">
        <span class="card-name">{{ secret.bucket.name }}</span>
        <span class="dot accent">token shown once</span>
        <span class="spacer"></span>
        <button class="icon-btn" type="button" title="Done" aria-label="Hide the token" @click="secret = null">
          <MIcon name="x" />
        </button>
      </div>
      <div class="connector-fold">
        <div class="connector-instructions">
          <div class="instruction-line">Copy the token now: memd keeps only its hash.</div>
          <div class="instruction-grid">
            <span>URL</span>
            <code>{{ publicURL(secret.bucket.url) }}</code>
            <span>Token</span>
            <code>{{ secret.token }}</code>
            <span>Or</span>
            <code>{{ publicURL(secret.token_url) }}</code>
          </div>
          <div class="instruction-line muted">
            Configure the URL with the token as a bearer token, or the one URL that carries it.
          </div>
        </div>
        <div class="card-foot">
          <button class="btn ghost" type="button" @click="copy(publicURL(secret.bucket.url), 'URL copied')">
            <MIcon name="copy" />
            Copy URL
          </button>
          <button class="btn ghost" type="button" @click="copy(secret.token, 'Token copied')">
            <MIcon name="copy" />
            Copy token
          </button>
          <button class="btn ghost" type="button" @click="copy(publicURL(secret.token_url), 'URL with token copied')">
            <MIcon name="copy" />
            Copy URL with token
          </button>
        </div>
      </div>
    </article>

    <div class="cards" v-if="buckets.length">
      <article class="card" v-for="bucket in buckets" :key="bucket.id">
        <div class="card-head">
          <MIcon name="refresh-cw" class="bucket-icon" />
          <span class="card-name">{{ bucket.name }}</span>
        </div>
        <div class="card-meta">
          <code>{{ publicURL(bucket.url) }}</code> · created {{ formatDate(bucket.created_at) }}
        </div>
        <div class="card-foot">
          <span class="spacer"></span>
          <button class="btn ghost" type="button" @click="rotate(bucket)">
            <MIcon name="refresh-cw" />
            New token
          </button>
          <button class="btn danger" type="button" @click="remove(bucket)">
            <MIcon name="trash-2" />
            Delete
          </button>
        </div>
      </article>
    </div>

    <div class="empty" v-else-if="loaded">
      <div class="empty-icon"><MIcon name="refresh-cw" /></div>
      <h4>No sync buckets yet</h4>
      <p>A bucket gives your devices a URL and a token to sync through.</p>
      <button class="btn primary" type="button" @click="openNew">
        <MIcon name="plus" />
        Add your first bucket
      </button>
    </div>

    <div class="modal-scrim" :class="{ open: showNew }" :inert="!showNew" @click="closeNew">
      <div class="modal" @click.stop>
        <header class="modal-head">
          <h3>Add sync bucket</h3>
          <div class="sub">A URL and a token your devices sync through.</div>
        </header>
        <form class="modal-body" id="new-bucket-form" @submit.prevent="create">
          <div class="field">
            <label class="field-label" for="new-bucket-name">Name<span class="req">*</span></label>
            <input
              id="new-bucket-name"
              class="input"
              v-model="form.name"
              required
              maxlength="64"
              placeholder="Browser"
            />
          </div>
          <span class="err" v-if="form.err">{{ form.err }}</span>
        </form>
        <footer class="modal-foot">
          <span class="spacer"></span>
          <button class="btn ghost" type="button" @click="closeNew">Cancel</button>
          <button
            class="btn primary"
            type="submit"
            form="new-bucket-form"
            :disabled="form.submitting || !form.name.trim()"
          >
            Add bucket
          </button>
        </footer>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from "vue";
import MIcon from "@/shared/components/MIcon.vue";
import { sync as syncApi, ApiError } from "@/shared/api";
import { toast } from "@/shared/bus";
import { copyToClipboard, formatDate } from "@/shared/utils";
import { publicURL } from "../components/connUrls";
import type { SyncBucketSecretResponse, SyncBucketView } from "@/shared/types";

// Sync buckets: each is a synckosh bucket at /sync, reached with its token in
// an Authorization header or in the path. The token is only in the answer that
// made it, so it is shown once, here, until dismissed.

const buckets = ref<SyncBucketView[]>([]);
const loaded = ref(false);
const secret = ref<SyncBucketSecretResponse | null>(null);

const showNew = ref(false);
const form = reactive({ name: "", err: "", submitting: false });

function errText(e: unknown): string {
  return e instanceof ApiError ? e.message : String(e);
}

async function reload(): Promise<void> {
  try {
    buckets.value = (await syncApi.list()).buckets || [];
  } catch (e) {
    toast(errText(e), "error");
  } finally {
    loaded.value = true;
  }
}

async function copy(text: string, label: string): Promise<void> {
  const ok = await copyToClipboard(text);
  toast(ok ? label : "Copy failed", ok ? "success" : "error");
}

function openNew(): void {
  form.name = "";
  form.err = "";
  form.submitting = false;
  showNew.value = true;
}

function closeNew(): void {
  showNew.value = false;
}

async function create(): Promise<void> {
  form.err = "";
  form.submitting = true;
  try {
    secret.value = await syncApi.create(form.name.trim());
    closeNew();
    await reload();
  } catch (e) {
    form.err = errText(e);
  } finally {
    form.submitting = false;
  }
}

async function rotate(bucket: SyncBucketView): Promise<void> {
  const message =
    "New token for " + bucket.name + "? The current token stops working at once on every device.";
  if (!window.confirm(message)) return;
  try {
    secret.value = await syncApi.rotate(bucket.id);
  } catch (e) {
    toast(errText(e), "error");
  }
  await reload();
}

async function remove(bucket: SyncBucketView): Promise<void> {
  if (!window.confirm("Delete sync bucket " + bucket.name + " and everything synced to it?")) return;
  try {
    await syncApi.remove(bucket.id);
    if (secret.value?.bucket.id === bucket.id) secret.value = null;
  } catch (e) {
    toast("Could not delete sync bucket: " + errText(e), "error");
  }
  await reload();
}

onMounted(reload);
</script>

<style scoped>
.secret-card {
  margin-bottom: 14px;
}

.bucket-icon {
  width: 16px;
  height: 16px;
  color: var(--fg-3);
}

.card-meta code {
  overflow-wrap: anywhere;
}

.modal-body {
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding: 16px 18px;
  overflow-y: auto;
}
</style>
