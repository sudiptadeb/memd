<template>
  <section class="admin-section">
    <div class="section-head">
      <div class="titles">
        <span class="step">Super admin</span>
        <h2>Remote terminals (termulaa rc)</h2>
        <span class="desc">
          memd can act as the rendezvous for termulaa's reverse tunnel: agents on users' machines
          dial out to this server, and browsers reach those terminals through it.
        </span>
      </div>
      <span class="spacer"></span>
      <span class="dot accent" v-if="form.active">active</span>
    </div>

    <form class="oidc-form" @submit.prevent="save">
      <label class="toggle-row" @click.prevent="form.enabled = !form.enabled">
        <div class="label">
          Enable remote terminals
          <div class="sub">
            When on, any signed-in memd user can mint a tunnel token and expose a terminal running
            on their own machine through this server. Turn it off if that is not something this
            deployment should offer.
          </div>
        </div>
        <div class="toggle" :class="form.enabled ? 'on' : ''"></div>
      </label>

      <div class="field-hint">
        The change applies immediately — no restart. Disabling closes every connected tunnel and
        stops serving the terminal pages; re-enabling lets agents reconnect on their own.
        <template v-if="form.view_host">
          Terminals are served on the dedicated view host <code>{{ form.view_host }}</code> (host
          mode).
        </template>
        <template v-else> Terminals are served under <code>/rc/t/&lt;agent&gt;/</code> (path mode). </template>
      </div>

      <div class="status-note" v-if="form.kill_switch">
        <MIcon name="triangle-alert" />
        <span>
          <code>MEMD_RC=0</code> is set in the server's environment — the emergency kill switch.
          The feature stays off no matter what this toggle says, until that variable is removed and
          memd restarts.
        </span>
      </div>
      <div class="status-note" v-else-if="!form.available">
        <MIcon name="triangle-alert" />
        <span>
          No token-signing key is available. Set <code>MEMD_SESSION_SECRET</code> (or a dedicated
          <code>MEMD_RC_TOKEN_SECRET</code>) in the server's environment; without one the feature
          cannot run.
        </span>
      </div>

      <span class="err" v-if="form.err">{{ form.err }}</span>
      <span class="ok-msg" v-if="form.msg">{{ form.msg }}</span>

      <div class="oidc-actions">
        <button class="btn primary" type="submit" :disabled="form.saving || !loaded">
          {{ form.saving ? "Saving…" : "Save" }}
        </button>
      </div>
    </form>
  </section>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from "vue";
import { admin, ApiError } from "@/shared/api";
import type { RCConfig } from "@/shared/types";
import MIcon from "@/shared/components/MIcon.vue";
import { toast } from "@/shared/bus";

// Super-admin control of the reverse-tunnel rendezvous. The feature is on by
// default; this page persists the runtime toggle (it survives deploys) and the
// server applies it immediately. MEMD_RC=0 in the environment is an emergency
// kill switch that wins over the toggle and is surfaced here when set.

const loaded = ref(false);

const form = reactive({
  enabled: true,
  active: false,
  kill_switch: false,
  available: true,
  view_host: "",
  err: "",
  msg: "",
  saving: false,
});

function applyConfig(cfg: RCConfig): void {
  form.enabled = cfg.enabled;
  form.active = cfg.active;
  form.kill_switch = cfg.kill_switch;
  form.available = cfg.available;
  form.view_host = cfg.view_host || "";
}

function errMessage(e: unknown, fallback: string): string {
  return e instanceof ApiError ? e.message : fallback;
}

async function load(): Promise<void> {
  try {
    const data = await admin.rc.get();
    applyConfig(data.rc);
    loaded.value = true;
  } catch (e) {
    form.err = errMessage(e, "could not load remote-terminal settings");
  }
}

async function save(): Promise<void> {
  form.err = "";
  form.msg = "";
  form.saving = true;
  try {
    const data = await admin.rc.save({ enabled: form.enabled });
    applyConfig(data.rc);
    form.msg = data.rc.enabled
      ? data.rc.active
        ? "Remote terminals enabled."
        : "Setting saved — it takes effect once the server environment allows the feature."
      : "Remote terminals disabled; all connected tunnels were closed.";
    toast("Remote-terminal settings saved", "success");
  } catch (e) {
    form.err = errMessage(e, "could not save remote-terminal settings");
  } finally {
    form.saving = false;
  }
}

onMounted(() => {
  void load();
});
</script>

<style scoped>
/* A caution strip for the kill-switch / missing-key states, in the same visual
   family as the tasks board's due_soon bucket. */
.status-note {
  display: flex;
  gap: 8px;
  align-items: flex-start;
  padding: 10px 12px;
  color: var(--fg-1);
  font-size: 12.5px;
  line-height: 1.5;
  border: 1px solid color-mix(in oklab, var(--warning) 40%, var(--border));
  border-radius: var(--radius-sm);
  background: color-mix(in oklab, var(--warning) 7%, transparent);
}
.status-note :deep(.icon) {
  flex: none;
  width: 15px;
  height: 15px;
  margin-top: 2px;
  color: var(--warning);
}
</style>
