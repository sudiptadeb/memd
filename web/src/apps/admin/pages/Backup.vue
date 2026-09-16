<template>
  <section class="admin-section">
    <div class="section-head">
      <div class="titles">
        <span class="step">Super admin</span>
        <h2>Encrypted backup</h2>
        <span class="desc">
          Once a day, and whenever you ask, memd seals a snapshot of its own database with a
          passphrase and commits it to a Git repository you control.
        </span>
      </div>
      <span class="spacer"></span>
      <span class="dot accent" v-if="saved.enabled && saved.supported">scheduled</span>
    </div>

    <form class="oidc-form" @submit.prevent="save">
      <label class="toggle-row" @click.prevent="form.enabled = !form.enabled">
        <div class="label">
          Enable daily backup
          <div class="sub">
            Requires a repository URL, an access token and a passphrase. The schedule re-arms as
            soon as you save — no restart.
          </div>
        </div>
        <div class="toggle" :class="form.enabled ? 'on' : ''"></div>
      </label>

      <div class="status-note" v-if="loaded && !saved.supported">
        <MIcon name="triangle-alert" />
        <span>
          This instance's account database is in-memory, so there is nothing on disk to snapshot.
          Point <code>MEMD_DATABASE_URL</code> at a SQLite file to enable backups.
        </span>
      </div>

      <div class="oidc-grid">
        <div class="field oidc-wide">
          <label class="field-label">Repository URL<span class="req">*</span></label>
          <input
            class="input"
            v-model="form.remote_url"
            placeholder="https://github.com/you/memd-backups.git"
            autocomplete="off"
          />
          <div class="field-hint">
            HTTPS only. Use a private repository: archives are encrypted, but they are still your
            whole memd database.
          </div>
        </div>
        <div class="field">
          <label class="field-label">Branch</label>
          <input class="input" v-model="form.branch" placeholder="main" autocomplete="off" />
        </div>
        <div class="field">
          <label class="field-label">Username</label>
          <input class="input" v-model="form.auth_username" autocomplete="off" />
          <div class="field-hint">
            Git username for the token (GitHub accepts any value with a fine-grained token).
          </div>
        </div>
        <div class="field">
          <label class="field-label"
            >Personal access token<span class="req" v-if="!saved.has_auth_token">*</span></label
          >
          <input
            class="input"
            type="password"
            v-model="form.auth_token"
            autocomplete="new-password"
            :placeholder="saved.has_auth_token ? '•••••• (stored — leave blank to keep)' : ''"
          />
          <div class="field-hint">Needs write access to the repository's contents.</div>
        </div>
        <div class="field">
          <label class="field-label"
            >Passphrase<span class="req" v-if="!saved.has_passphrase">*</span></label
          >
          <input
            class="input"
            type="password"
            v-model="form.passphrase"
            autocomplete="new-password"
            :placeholder="saved.has_passphrase ? '•••••• (stored — leave blank to keep)' : ''"
          />
          <div class="field-hint">
            At least 12 characters. An archive can only be opened with the passphrase that was in
            force when it was made — memd does not keep old ones. Lose it and those backups are
            gone.
          </div>
        </div>
        <div class="field">
          <label class="field-label">Daily time (UTC)</label>
          <input class="input" v-model="form.daily_at_utc" placeholder="03:00" autocomplete="off" />
          <div class="field-hint">24-hour clock, HH:MM.</div>
        </div>
        <div class="field">
          <label class="field-label">Retention days</label>
          <input
            class="input"
            type="number"
            min="0"
            v-model.number="form.retention_days"
            autocomplete="off"
          />
          <div class="field-hint">
            Archives older than this are removed from the repository after each run. 0 keeps all.
          </div>
        </div>
      </div>

      <span class="err" v-if="form.err">{{ form.err }}</span>
      <span class="ok-msg" v-if="form.msg">{{ form.msg }}</span>
      <span class="err" v-if="check.err">{{ check.err }}</span>
      <span class="ok-msg" v-if="check.msg">{{ check.msg }}</span>

      <div class="oidc-actions backup-actions">
        <button
          class="btn secondary"
          type="button"
          :disabled="check.busy || !form.remote_url"
          @click="testConnection"
        >
          {{ check.busy ? "Testing…" : "Test connection" }}
        </button>
        <button class="btn secondary" type="button" :disabled="!canRun || run.busy" @click="runNow">
          <MIcon name="upload" />
          {{ run.busy ? "Backing up…" : "Back up now" }}
        </button>
        <a
          class="btn secondary"
          :class="{ disabled: !canDownload }"
          :href="canDownload ? admin.backup.downloadUrl() : undefined"
          :aria-disabled="!canDownload"
          download
        >
          <MIcon name="download" />
          Download backup
        </a>
        <button class="btn primary" type="submit" :disabled="form.saving || !loaded">
          {{ form.saving ? "Saving…" : "Save" }}
        </button>
      </div>
    </form>

    <div class="backup-status" v-if="loaded">
      <div class="field-label">Status</div>
      <dl class="backup-kv">
        <dt>Last backup</dt>
        <dd v-if="!saved.status.last_run_at">never</dd>
        <dd v-else>
          <span class="dot" :class="saved.status.last_ok ? 'success' : 'danger'">
            {{ saved.status.last_ok ? "ok" : "failed" }}
          </span>
          {{ fmtTime(saved.status.last_run_at) }}
          <span class="muted" v-if="saved.status.last_trigger">({{ saved.status.last_trigger }})</span>
          <div class="err" v-if="!saved.status.last_ok && saved.status.last_error">
            {{ saved.status.last_error }}
          </div>
        </dd>
        <template v-if="saved.status.last_ok && saved.status.last_archive">
          <dt>Archive</dt>
          <dd>
            <code>backups/{{ saved.status.last_archive }}</code>
            <span class="muted">{{ fmtBytes(saved.status.last_size) }}</span>
          </dd>
          <dt>Commit</dt>
          <dd><code>{{ saved.status.last_commit }}</code></dd>
        </template>
        <dt>Next scheduled run</dt>
        <dd v-if="saved.running">running now…</dd>
        <dd v-else-if="saved.next_run_at">{{ fmtTime(saved.next_run_at) }}</dd>
        <dd v-else class="muted">not scheduled</dd>
      </dl>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import { admin, ApiError } from "@/shared/api";
import type { BackupConfig, SaveBackupRequest } from "@/shared/types";
import MIcon from "@/shared/components/MIcon.vue";
import { toast } from "@/shared/bus";

// Super-admin configuration of the encrypted daily backup: the target Git
// repository, the sealing passphrase (both write-only, like the OIDC client
// secret), the UTC schedule and retention; plus the actions (test connection,
// back up now, download an archive) and the last-run status.

const loaded = ref(false);

// The last state the server confirmed. Buttons key off this, not off unsaved
// edits, so "Back up now" reflects what a run would actually use.
const saved = reactive<BackupConfig>({
  enabled: false,
  remote_url: "",
  branch: "main",
  auth_username: "",
  has_auth_token: false,
  has_passphrase: false,
  daily_at_utc: "03:00",
  retention_days: 30,
  supported: true,
  running: false,
  next_run_at: null,
  status: {
    last_run_at: null,
    last_ok: false,
    last_error: "",
    last_archive: "",
    last_size: 0,
    last_commit: "",
    last_trigger: "",
  },
});

const form = reactive({
  enabled: false,
  remote_url: "",
  branch: "main",
  auth_username: "",
  auth_token: "",
  passphrase: "",
  daily_at_utc: "03:00",
  retention_days: 30,
  err: "",
  msg: "",
  saving: false,
});

const check = reactive({ busy: false, err: "", msg: "" });
const run = reactive({ busy: false });

const canRun = computed(
  () =>
    saved.supported &&
    saved.enabled &&
    !!saved.remote_url &&
    saved.has_auth_token &&
    saved.has_passphrase &&
    !saved.running,
);
const canDownload = computed(() => saved.supported && saved.has_passphrase);

function errMessage(e: unknown, fallback: string): string {
  return e instanceof ApiError ? e.message : fallback;
}

function applyConfig(cfg: BackupConfig): void {
  Object.assign(saved, cfg);
  form.enabled = cfg.enabled;
  form.remote_url = cfg.remote_url || "";
  form.branch = cfg.branch || "main";
  form.auth_username = cfg.auth_username || "";
  form.auth_token = "";
  form.passphrase = "";
  form.daily_at_utc = cfg.daily_at_utc || "03:00";
  form.retention_days = cfg.retention_days;
}

function fmtTime(iso: string): string {
  const d = new Date(iso);
  return isNaN(d.getTime()) ? iso : d.toLocaleString();
}

function fmtBytes(n: number): string {
  if (!n) return "";
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KiB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MiB`;
}

async function load(): Promise<void> {
  try {
    const data = await admin.backup.get();
    applyConfig(data.backup);
    loaded.value = true;
  } catch (e) {
    form.err = errMessage(e, "could not load backup settings");
  }
}

async function save(): Promise<void> {
  form.err = "";
  form.msg = "";
  check.err = "";
  check.msg = "";
  form.saving = true;
  const body: SaveBackupRequest = {
    enabled: form.enabled,
    remote_url: form.remote_url,
    branch: form.branch,
    auth_username: form.auth_username,
    daily_at_utc: form.daily_at_utc,
    retention_days: Number(form.retention_days) || 0,
  };
  // Secrets are only sent when typed; omitted fields keep the stored values
  // (pointers on the Go side).
  if (form.auth_token) body.auth_token = form.auth_token;
  if (form.passphrase) body.passphrase = form.passphrase;
  try {
    const data = await admin.backup.save(body);
    applyConfig(data.backup);
    form.msg = data.backup.enabled
      ? `Backup enabled — next run ${data.backup.next_run_at ? fmtTime(data.backup.next_run_at) : "pending"}.`
      : "Backup settings saved (disabled).";
    toast("Backup settings saved", "success");
  } catch (e) {
    form.err = errMessage(e, "could not save backup settings");
  } finally {
    form.saving = false;
  }
}

async function testConnection(): Promise<void> {
  check.err = "";
  check.msg = "";
  form.err = "";
  form.msg = "";
  check.busy = true;
  try {
    // Send what is on the form so an admin can test before saving; blank
    // secrets fall back to the stored ones server-side.
    const data = await admin.backup.check({
      remote_url: form.remote_url,
      branch: form.branch,
      auth_username: form.auth_username,
      auth_token: form.auth_token || undefined,
    });
    if (data.ok) {
      check.msg = data.message;
    } else {
      check.err = data.message;
    }
  } catch (e) {
    check.err = errMessage(e, "connection test failed");
  } finally {
    check.busy = false;
  }
}

async function runNow(): Promise<void> {
  form.err = "";
  form.msg = "";
  check.err = "";
  check.msg = "";
  run.busy = true;
  try {
    const data = await admin.backup.run();
    applyConfig(data.backup);
    form.msg = `Backup pushed: ${data.backup.status.last_archive}`;
    toast("Backup pushed", "success");
  } catch (e) {
    form.err = errMessage(e, "backup failed");
    // A failed run still records its status; refresh so it shows.
    await load();
  } finally {
    run.busy = false;
  }
}

onMounted(() => {
  void load();
});
</script>

<style scoped>
/* Same caution strip as the termulaa page's kill-switch note. */
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
.backup-actions {
  gap: 8px;
  flex-wrap: wrap;
}
.backup-actions .btn :deep(.icon) {
  width: 14px;
  height: 14px;
}
.backup-actions a.btn.disabled {
  pointer-events: none;
  opacity: 0.5;
}
.backup-status {
  margin-top: 20px;
  padding-top: 16px;
  border-top: 1px solid var(--line);
}
.backup-kv {
  display: grid;
  grid-template-columns: max-content minmax(0, 1fr);
  gap: 8px 16px;
  margin: 10px 0 0;
  font-size: 12.5px;
}
.backup-kv dt {
  color: var(--fg-3);
}
.backup-kv dd {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
  margin: 0;
}
.backup-kv .muted {
  color: var(--fg-3);
}
.backup-kv .err {
  flex-basis: 100%;
}
</style>
