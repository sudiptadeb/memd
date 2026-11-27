<template>
  <section class="app-section">
    <div class="section-head">
      <div class="titles">
        <h2>Notification channels <span class="count" v-if="loaded">{{ channels.length }}</span></h2>
        <span class="desc">
          Each channel is an address your scripts and agents send to and your phone listens on. It
          speaks ntfy's protocol, so an app made for ntfy.sh works with this address instead. memd
          keeps each message for a day, for a phone that was away.
        </span>
      </div>
      <span class="spacer"></span>
      <button class="btn secondary" type="button" @click="openNew">
        <MIcon name="plus" />
        <span class="btn-label">Add channel</span>
      </button>
    </div>

    <div class="cards" v-if="channels.length">
      <article
        class="card connector-card"
        v-for="channel in channels"
        :key="channel.id"
        :class="expanded[channel.id] ? 'expanded' : ''"
        @click="toggle(channel.id)"
        @keydown.enter.prevent="toggle(channel.id)"
        @keydown.space.prevent="toggle(channel.id)"
        tabindex="0"
        role="button"
        :aria-expanded="expanded[channel.id] ? 'true' : 'false'"
      >
        <div class="card-head">
          <span class="card-name">{{ channel.name }}</span>
          <span class="spacer"></span>
          <MIcon name="chevron-right" class="expand-icon" />
        </div>
        <div class="card-meta">
          Created {{ formatDate(channel.created_at) }} ·
          {{ channel.messages === 1 ? "1 message" : channel.messages + " messages" }} in the last day
        </div>
        <div class="connector-fold" v-if="expanded[channel.id]" @click.stop>
          <div class="url-row">
            <code :class="revealed[channel.id] ? 'revealed' : ''">{{
              revealed[channel.id] ? publicURL(channel.url) : truncate(publicURL(channel.url))
            }}</code>
            <div class="url-actions">
              <button type="button" @click="revealed[channel.id] = !revealed[channel.id]">
                <MIcon :name="revealed[channel.id] ? 'eye-off' : 'eye'" />
                <span>{{ revealed[channel.id] ? "Hide" : "Reveal" }}</span>
              </button>
              <button type="button" @click="copy(publicURL(channel.url), 'Address copied')">
                <MIcon name="copy" />
                Copy
              </button>
            </div>
          </div>
          <div class="connector-instructions">
            <div class="instruction-grid">
              <span>Send</span>
              <code>POST {{ "<address>" }} with the message as the body, up to 4096 bytes</code>
              <span>Listen</span>
              <code>{{ "<address>" }}/ws?since=&lt;id&gt;, or /json?poll=1&amp;since=&lt;id&gt;</code>
            </div>
            <div class="instruction-line muted">
              The address is the only key: anyone who has it can send and read. Seal messages for
              the phone, and use New token if the address gets out.
            </div>
          </div>
          <div class="card-foot">
            <span class="spacer"></span>
            <button class="btn ghost" type="button" @click="rotate(channel)">
              <MIcon name="refresh-cw" />
              New token
            </button>
            <button class="btn danger" type="button" @click="remove(channel)">
              <MIcon name="trash-2" />
              Delete
            </button>
          </div>
        </div>
      </article>
    </div>

    <div class="empty" v-else-if="loaded">
      <div class="empty-icon"><MIcon name="bell" /></div>
      <h4>No notification channels yet</h4>
      <p>A channel gives your phone an address to listen on, and your agents one to send to.</p>
      <button class="btn primary" type="button" @click="openNew">
        <MIcon name="plus" />
        Add your first channel
      </button>
    </div>

    <div class="modal-scrim" :class="{ open: showNew }" :inert="!showNew" @click="closeNew">
      <div class="modal" @click.stop>
        <header class="modal-head">
          <h3>Add notification channel</h3>
          <div class="sub">An address to send notifications to and listen on.</div>
        </header>
        <form class="modal-body" id="new-channel-form" @submit.prevent="create">
          <div class="field">
            <label class="field-label" for="new-channel-name">Name<span class="req">*</span></label>
            <input
              id="new-channel-name"
              class="input"
              v-model="form.name"
              required
              maxlength="64"
              placeholder="Phone"
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
            form="new-channel-form"
            :disabled="form.submitting || !form.name.trim()"
          >
            Add channel
          </button>
        </footer>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from "vue";
import MIcon from "@/shared/components/MIcon.vue";
import { notify as notifyApi, ApiError } from "@/shared/api";
import { toast } from "@/shared/bus";
import { copyToClipboard, formatDate } from "@/shared/utils";
import { publicURL, truncate } from "../components/connUrls";
import type { NotifyChannelView } from "@/shared/types";

// Notification channels: each is an ntfy-style topic at /notify/<token>. Like
// a sync bucket's card, each expands to its address, hidden until Reveal.

const channels = ref<NotifyChannelView[]>([]);
const loaded = ref(false);
const expanded = reactive<Record<string, boolean>>({});
const revealed = reactive<Record<string, boolean>>({});

const showNew = ref(false);
const form = reactive({ name: "", err: "", submitting: false });

function errText(e: unknown): string {
  return e instanceof ApiError ? e.message : String(e);
}

function toggle(id: string): void {
  expanded[id] = !expanded[id];
}

async function reload(): Promise<void> {
  try {
    channels.value = (await notifyApi.list()).channels || [];
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
    const made = await notifyApi.create(form.name.trim());
    expanded[made.channel.id] = true;
    closeNew();
    await reload();
  } catch (e) {
    form.err = errText(e);
  } finally {
    form.submitting = false;
  }
}

async function rotate(channel: NotifyChannelView): Promise<void> {
  const message =
    "New token for " + channel.name + "? The current address stops working at once, for senders and the phone.";
  if (!window.confirm(message)) return;
  try {
    await notifyApi.rotate(channel.id);
    toast("Address replaced", "success");
  } catch (e) {
    toast(errText(e), "error");
  }
  await reload();
}

async function remove(channel: NotifyChannelView): Promise<void> {
  if (!window.confirm("Delete notification channel " + channel.name + " and its messages?")) return;
  try {
    await notifyApi.remove(channel.id);
  } catch (e) {
    toast("Could not delete notification channel: " + errText(e), "error");
  }
  await reload();
}

onMounted(reload);
</script>

<style scoped>
.modal-body {
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding: 16px 18px;
  overflow-y: auto;
}
</style>
