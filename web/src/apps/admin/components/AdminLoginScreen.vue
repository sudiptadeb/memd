<template>
  <main class="login-shell">
    <div class="login-panel">
      <div class="login-head">
        <span class="eyebrow">memd admin</span>
        <h1>Sign in</h1>
      </div>

      <span class="err" v-if="err">{{ err }}</span>

      <!-- Default flow: single sign-on via the configured identity provider. -->
      <div class="sso-block" v-if="oidcEnabled">
        <button
          class="btn primary login-submit"
          type="button"
          @click="ssoLogin"
          :disabled="ssoRedirecting"
          :aria-busy="ssoRedirecting ? 'true' : 'false'"
        >
          <MIcon v-if="ssoRedirecting" name="refresh-cw" class="spin" />
          <span>{{
            ssoRedirecting ? "Taking you to your sign-in provider..." : "Sign in with your organization"
          }}</span>
        </button>
        <div class="login-note sso-note">
          You will be redirected to your organization's identity provider.
        </div>
        <button class="linklike" type="button" @click="showLocalLogin = !showLocalLogin">
          {{ showLocalLogin ? "Hide local sign-in" : "Use a local account instead" }}
        </button>
      </div>

      <!-- Backup: local accounts created by a super admin. No self-signup. -->
      <form class="local-login" v-show="showLocalLogin" @submit.prevent="submit">
        <div class="login-note" v-if="oidcEnabled">
          Local sign-in is a backup for super-admin-created accounts. You cannot create an account
          here.
        </div>
        <div class="field">
          <label class="field-label">Username</label>
          <input class="input" v-model="username" autocomplete="username" required />
        </div>
        <div class="field">
          <label class="field-label">Password</label>
          <input
            class="input"
            v-model="password"
            type="password"
            autocomplete="current-password"
            required
          />
        </div>
        <button
          class="btn login-submit"
          :class="oidcEnabled ? 'secondary' : 'primary'"
          type="submit"
          :disabled="submitting"
        >
          Sign in
        </button>
      </form>
    </div>
  </main>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import MIcon from "@/shared/components/MIcon.vue";
import { useSession } from "@/shared/session";
import { ApiError } from "@/shared/api";

// Full-page admin login. Local username/password via useSession().login(); SSO
// (when configured) navigates the browser into the server-side OIDC flow. Mirrors
// the Alpine admin login-panel markup so it reuses the shared login CSS.

const { auth, login, ssoLoginUrl } = useSession();

const oidcEnabled = computed(() => auth.value.oidc_enabled);
// When SSO is the default, the local form stays tucked away as a backup.
const showLocalLogin = ref(!auth.value.oidc_enabled);
const ssoRedirecting = ref(false);

const username = ref("");
const password = ref("");
const submitting = ref(false);
const err = ref("");

// Surface a server-side OIDC error passed back as ?login_error=..., then scrub it
// from the URL (mirrors the Alpine readLoginError()).
function readLoginError(): void {
  const params = new URLSearchParams(window.location.search);
  const e = params.get("login_error");
  if (e) {
    err.value = e;
    params.delete("login_error");
    const qs = params.toString();
    window.history.replaceState({}, "", window.location.pathname + (qs ? `?${qs}` : ""));
  }
}

// Back-navigation from the IdP restores the page from bfcache with stale state;
// reset so the SSO button is usable again.
function onPageShow(event: PageTransitionEvent): void {
  if (event.persisted) {
    ssoRedirecting.value = false;
  }
}

onMounted(() => {
  readLoginError();
  window.addEventListener("pageshow", onPageShow);
});

onUnmounted(() => {
  window.removeEventListener("pageshow", onPageShow);
});

async function submit(): Promise<void> {
  err.value = "";
  submitting.value = true;
  try {
    await login(username.value, password.value);
    // On success the shell swaps to the admin view; nothing more to do here.
  } catch (e) {
    err.value = e instanceof ApiError ? e.message : "login failed";
  } finally {
    submitting.value = false;
  }
}

function ssoLogin(): void {
  if (ssoRedirecting.value) return;
  ssoRedirecting.value = true;
  window.location.href = ssoLoginUrl();
}
</script>
