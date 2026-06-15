<template>
  <section class="admin-section">
    <div class="section-head">
      <div class="titles">
        <span class="step">Super admin</span>
        <h2>Doctrines <span class="count">{{ doctrines.length }}</span></h2>
        <span class="desc">
          Live-edit the global doctrine and each feature's base doctrine. Changes apply immediately to
          connected agents but are <strong>not saved</strong> — they revert to the built-in defaults
          on restart.
        </span>
      </div>
      <span class="spacer"></span>
      <button class="btn secondary" type="button" @click="load">
        <MIcon name="refresh-cw" />
        Refresh
      </button>
    </div>

    <div class="empty load-state" v-if="loading">
      <div class="empty-icon"><MIcon name="activity" /></div>
      <h4>Loading doctrines</h4>
    </div>

    <div class="empty error-state" v-else-if="loadErr">
      <div class="empty-icon"><MIcon name="triangle-alert" /></div>
      <h4>Could not load doctrines</h4>
      <p>{{ loadErr }}</p>
    </div>

    <div class="cards doctrine-cards" v-else>
      <article v-for="d in doctrines" :key="d.id" class="card doctrine-card">
        <div class="card-head">
          <div class="card-name">{{ d.label }}</div>
          <span class="dot accent" v-if="d.overridden">overridden</span>
          <span class="spacer"></span>
          <code class="card-meta">{{ d.id }}</code>
        </div>
        <div class="field">
          <textarea class="input doctrine-text" rows="10" v-model="d.text" spellcheck="false" />
        </div>
        <div class="card-head">
          <span class="spacer"></span>
          <button
            class="btn ghost"
            type="button"
            @click="resetDoctrine(d)"
            :disabled="!d.overridden"
          >
            Reset to default
          </button>
          <button class="btn primary" type="button" @click="saveDoctrine(d)" :disabled="d.saving">
            Apply (temporary)
          </button>
        </div>
      </article>
    </div>
  </section>
</template>

<script setup lang="ts">
import { onMounted, ref } from "vue";
import { admin, ApiError } from "@/shared/api";
import type { Doctrine } from "@/shared/types";
import { toast } from "@/shared/bus";
import MIcon from "@/shared/components/MIcon.vue";

// Super-admin doctrine editor: live-edit each doctrine's text (applied in-memory,
// reverts on restart) and reset overridden ones to their compiled default. Ports
// memdAdminApp()'s doctrine section.

// The text is locally editable, so each row carries its own editable copy plus a
// per-row saving flag layered onto the server's Doctrine shape.
interface DoctrineRow extends Doctrine {
  saving: boolean;
}

const doctrines = ref<DoctrineRow[]>([]);
const loading = ref(false);
const loadErr = ref("");

function errMessage(e: unknown, fallback: string): string {
  return e instanceof ApiError ? e.message : fallback;
}

async function load(): Promise<void> {
  loading.value = true;
  loadErr.value = "";
  try {
    const data = await admin.doctrines.list();
    doctrines.value = (data.doctrines ?? []).map((d) => ({ ...d, saving: false }));
  } catch (e) {
    loadErr.value = errMessage(e, "could not load doctrines");
  } finally {
    loading.value = false;
  }
}

async function saveDoctrine(d: DoctrineRow): Promise<void> {
  d.saving = true;
  try {
    const res = await admin.doctrines.save(d.id, d.text);
    d.overridden = res.overridden;
    toast("Doctrine applied (temporary)", "success");
  } catch (e) {
    toast(errMessage(e, "could not apply doctrine"), "error");
  } finally {
    d.saving = false;
  }
}

async function resetDoctrine(d: DoctrineRow): Promise<void> {
  try {
    await admin.doctrines.reset(d.id);
    // Reload so the textarea picks up the restored default text + overridden state.
    await load();
    toast("Doctrine reset to default", "success");
  } catch (e) {
    toast(errMessage(e, "could not reset doctrine"), "error");
  }
}

onMounted(load);
</script>
