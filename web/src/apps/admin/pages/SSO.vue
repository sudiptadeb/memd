<template>
  <section class="admin-section">
    <div class="section-head">
      <div class="titles">
        <span class="step">Super admin</span>
        <h2>Single sign-on (OIDC)</h2>
        <span class="desc">
          Point memd at any OpenID Connect identity provider. Endpoints and keys come from discovery
          — no per-provider code.
        </span>
      </div>
      <span class="spacer"></span>
      <span class="dot accent" v-if="form.active">active</span>
    </div>

    <form class="oidc-form" @submit.prevent="save">
      <label class="toggle-row" @click.prevent="form.enabled = !form.enabled">
        <div class="label">
          Enable SSO
          <div class="sub">
            When on, the login page leads with SSO and local accounts remain a backup.
          </div>
        </div>
        <div class="toggle" :class="form.enabled ? 'on' : ''"></div>
      </label>

      <div class="oidc-grid" v-show="form.enabled">
        <div class="field">
          <label class="field-label">Issuer URL<span class="req">*</span></label>
          <input
            class="input"
            v-model="form.issuer_url"
            placeholder="https://idp.example.com/realms/memd"
          />
          <div class="field-hint">
            Discovery base. memd fetches <code>/.well-known/openid-configuration</code>. Changing the
            URL of the <em>same</em> provider keeps everyone signed up — accounts are not keyed to the
            URL.
          </div>
        </div>
        <div class="field">
          <label class="field-label">Redirect URI<span class="req">*</span></label>
          <input
            class="input"
            v-model="form.redirect_uri"
            placeholder="https://memd.example.com/auth/callback"
          />
          <div class="field-hint">Register this exact URL at your IdP.</div>
        </div>
        <div class="field">
          <label class="field-label">Client ID<span class="req">*</span></label>
          <input class="input" v-model="form.client_id" autocomplete="off" />
        </div>
        <div class="field">
          <label class="field-label"
            >Client secret<span class="req" v-if="!form.has_client_secret">*</span></label
          >
          <input
            class="input"
            type="password"
            v-model="form.client_secret"
            autocomplete="new-password"
            :placeholder="form.has_client_secret ? '•••••• (stored — leave blank to keep)' : ''"
          />
        </div>
        <div class="field">
          <label class="field-label">Scopes</label>
          <input class="input" v-model="form.scopes" placeholder="openid profile email" />
          <div class="field-hint">
            Space-separated. Add <code>offline_access</code> if your IdP needs it for refresh tokens.
          </div>
        </div>
        <div class="field">
          <label class="field-label">Post-logout redirect URI</label>
          <input
            class="input"
            v-model="form.post_logout_redirect_uri"
            placeholder="https://memd.example.com/"
          />
        </div>
      </div>

      <label
        class="toggle-row"
        v-show="form.enabled"
        @click.prevent="form.replace_provider = !form.replace_provider"
      >
        <div class="label">
          This is a different identity provider
          <div class="sub">
            Only check when switching to a genuinely new IdP: existing SSO accounts are unlinked and
            sign-ins start fresh. Leave off when the same IdP moves to a new URL.
          </div>
        </div>
        <div class="toggle" :class="form.replace_provider ? 'on' : ''"></div>
      </label>

      <span class="err" v-if="form.err">{{ form.err }}</span>
      <span class="ok-msg" v-if="form.msg">{{ form.msg }}</span>

      <div class="oidc-actions">
        <button class="btn primary" type="submit" :disabled="form.saving">
          {{ form.saving ? "Saving…" : "Save SSO settings" }}
        </button>
      </div>
    </form>

    <form class="oidc-form relink-form" v-if="hasOrphanedSSOUsers" @submit.prevent="relink">
      <div class="field">
        <label class="field-label">Re-link stranded SSO accounts</label>
        <div class="field-hint">
          Some accounts were signed up through an issuer URL that is no longer configured. If that
          was this same provider under its old URL, re-link them so those people keep their data on
          next sign-in.
        </div>
        <div class="relink-row">
          <input
            class="input"
            v-model="relinkForm.from_issuer"
            placeholder="https://old-idp.example.com"
            required
          />
          <button class="btn secondary" type="submit" :disabled="relinkForm.submitting">
            Re-link
          </button>
        </div>
        <span class="err" v-if="relinkForm.err">{{ relinkForm.err }}</span>
        <span class="ok-msg" v-if="relinkForm.msg">{{ relinkForm.msg }}</span>
      </div>
    </form>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import { admin, ApiError } from "@/shared/api";
import type { AdminUser, OIDCConfig, SaveOIDCRequest } from "@/shared/types";
import { toast } from "@/shared/bus";

// Super-admin OIDC configuration: the provider settings form (write-only client
// secret, replace-provider switch) plus a re-link tool for accounts stranded on a
// prior issuer URL. Ports memdAdminApp()'s OIDC + relink sections.

const form = reactive({
  enabled: false,
  issuer_url: "",
  client_id: "",
  client_secret: "",
  has_client_secret: false,
  redirect_uri: "",
  scopes: "",
  post_logout_redirect_uri: "",
  replace_provider: false,
  active: false,
  err: "",
  msg: "",
  saving: false,
});

const relinkForm = reactive({
  from_issuer: "",
  err: "",
  msg: "",
  submitting: false,
});

// The relink tool only matters when accounts are stranded on a prior issuer; we
// derive that from the user list, mirroring the Alpine hasOrphanedSSOUsers getter.
const users = ref<AdminUser[]>([]);
const hasOrphanedSSOUsers = computed(() => users.value.some((u) => u.sso_orphan));

function errMessage(e: unknown, fallback: string): string {
  return e instanceof ApiError ? e.message : fallback;
}

// Reset the form from a freshly fetched/saved config. The client secret is never
// returned, so the input always starts blank ("leave blank to keep").
function applyConfig(cfg: OIDCConfig): void {
  form.enabled = cfg.enabled;
  form.issuer_url = cfg.issuer_url || "";
  form.client_id = cfg.client_id || "";
  form.client_secret = "";
  form.has_client_secret = cfg.has_client_secret;
  form.redirect_uri = cfg.redirect_uri || "";
  form.scopes = cfg.scopes || "";
  form.post_logout_redirect_uri = cfg.post_logout_redirect_uri || "";
  form.active = cfg.active;
  form.replace_provider = false;
}

async function loadConfig(): Promise<void> {
  try {
    const data = await admin.oidc.get();
    applyConfig(data.oidc);
  } catch (e) {
    form.err = errMessage(e, "could not load OIDC settings");
  }
}

async function loadUsers(): Promise<void> {
  try {
    const data = await admin.users.list();
    users.value = data.users ?? [];
  } catch {
    // Non-fatal: without the list we simply don't surface the relink tool.
    users.value = [];
  }
}

async function save(): Promise<void> {
  form.err = "";
  form.msg = "";
  if (form.replace_provider) {
    const warning =
      "Replace the identity provider? Every existing SSO account is unlinked and the next sign-in creates a fresh account. Only do this when switching to a genuinely different IdP.";
    if (!window.confirm(warning)) {
      return;
    }
  }
  form.saving = true;
  const body: SaveOIDCRequest = {
    enabled: form.enabled,
    issuer_url: form.issuer_url,
    client_id: form.client_id,
    redirect_uri: form.redirect_uri,
    scopes: form.scopes,
    post_logout_redirect_uri: form.post_logout_redirect_uri,
    replace_provider: form.replace_provider,
  };
  // Only send the secret when the admin typed a new one; otherwise omit it so the
  // stored value is kept (client_secret is a pointer on the Go side).
  if (form.client_secret) {
    body.client_secret = form.client_secret;
  }
  try {
    const data = await admin.oidc.save(body);
    applyConfig(data.oidc);
    form.msg = data.oidc.enabled ? "OIDC saved and applied." : "OIDC disabled.";
    toast("OIDC settings saved", "success");
  } catch (e) {
    form.err = errMessage(e, "could not save OIDC settings");
  } finally {
    form.saving = false;
  }
}

async function relink(): Promise<void> {
  relinkForm.err = "";
  relinkForm.msg = "";
  relinkForm.submitting = true;
  try {
    const data = await admin.oidc.relink({ from_issuer: relinkForm.from_issuer });
    const skipped = data.skipped ?? [];
    let msg = `Re-linked ${data.adopted ?? 0} account(s).`;
    if (skipped.length) {
      msg += ` Skipped (subject already taken — unlink the duplicate first): ${skipped.join(", ")}`;
    }
    relinkForm.msg = msg;
    await loadUsers();
  } catch (e) {
    relinkForm.err = errMessage(e, "re-link failed");
  } finally {
    relinkForm.submitting = false;
  }
}

onMounted(async () => {
  await Promise.all([loadConfig(), loadUsers()]);
});
</script>
