<template>
  <main class="login-shell">
    <div class="login-hero">
      <img class="logo logo-light" :src="logoLight" alt="memd" />
      <img class="logo logo-dark" :src="logoDark" alt="memd" />
      <h1>Unified, file-first memory for AI agents</h1>
      <p class="login-tagline">
        Your memory lives as plain files you control — a folder on disk or a private Git repo. memd
        serves it to Claude Code, Codex, Cursor, and every other agent that speaks MCP, so what you
        teach one agent, they all remember.
      </p>
      <ul class="login-points">
        <li><MIcon name="folder-open" />Plain files, yours</li>
        <li><MIcon name="plug" />One URL per agent</li>
        <li><MIcon name="git-branch" />Local folder or Git</li>
      </ul>

      <!-- Pending team invite: the visitor followed an invite link while signed
           out; tell them signing in completes the join. -->
      <div class="invite-callout" v-if="inviteTeamName">
        <div class="label">You're invited to join {{ inviteTeamName }}</div>
        <div class="sub">Log in below to accept the invite.</div>
      </div>

      <span class="err" v-if="err">{{ err }}</span>

      <!-- Default flow: single sign-on via the configured identity provider. -->
      <div class="sso-block" v-if="oidcEnabled">
        <button
          class="btn primary login-cta"
          type="button"
          @click="ssoLogin"
          :disabled="ssoRedirecting"
          :aria-busy="ssoRedirecting ? 'true' : 'false'"
        >
          <MIcon v-if="ssoRedirecting" name="refresh-cw" class="spin" />
          <span>{{ ssoRedirecting ? "Taking you to your sign-in provider..." : "Log in" }}</span>
        </button>
        <div class="login-note sso-note">
          You'll be taken to your sign-in provider and brought right back.
        </div>
        <button class="linklike" type="button" @click="showLocalLogin = !showLocalLogin">
          {{ showLocalLogin ? "Hide local log-in" : "Use a local account instead" }}
        </button>
      </div>

      <!-- Backup: local accounts created by a super admin. No self-signup. -->
      <form class="local-login" v-show="showLocalLogin" @submit.prevent="submit">
        <div class="login-note" v-if="oidcEnabled">
          Local log-in is a backup for accounts a super admin created. You cannot create an account
          here.
        </div>
        <div class="field">
          <label class="field-label">Username</label>
          <input class="input" v-model="username" autocomplete="username" required autofocus />
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
          class="btn login-cta"
          :class="oidcEnabled ? 'secondary' : 'primary'"
          type="submit"
          :disabled="submitting"
        >
          Log in
        </button>
      </form>
    </div>
  </main>
</template>

<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useRoute } from "vue-router";
import logoLight from "@/shared/assets/logo-light.png";
import logoDark from "@/shared/assets/logo-dark.png";
import MIcon from "@/shared/components/MIcon.vue";
import { useSession } from "@/shared/session";
import { ApiError, invites } from "@/shared/api";
import { peekInviteToken } from "../inviteStash";

// Full-page login. Local username/password via useSession().login(); SSO (when
// configured) navigates the browser into the server-side OIDC flow. Mirrors the
// Alpine login-hero markup so it reuses the same .login-shell/.login-hero CSS.

const { auth, login, ssoLoginUrl } = useSession();

const oidcEnabled = computed(() => auth.value.oidc_enabled);
// When SSO is the default, the local form stays tucked away as a backup.
const showLocalLogin = ref(!auth.value.oidc_enabled);
const ssoRedirecting = ref(false);

const username = ref("");
const password = ref("");
const submitting = ref(false);
const err = ref("");

// When the visitor arrived through an invite link, preview it (public
// endpoint) so the login screen can say which team is waiting for them.
// Watched, not mounted-once: the router's initial navigation resolves
// asynchronously, so the invite route may not be current yet at mount.
const route = useRoute();
const inviteTeamName = ref("");
const previewedToken = ref("");
watch(
  () => (route.name === "invite" && route.params.token ? String(route.params.token) : peekInviteToken()),
  async (token) => {
    if (!token || token === previewedToken.value) return;
    previewedToken.value = token;
    try {
      const data = await invites.preview(token);
      if (data.invite?.valid) {
        inviteTeamName.value = data.invite.team_name || "a team";
      }
    } catch {
      // Invalid/expired invite: the invite page reports details after sign-in.
    }
  },
  { immediate: true },
);

async function submit(): Promise<void> {
  err.value = "";
  submitting.value = true;
  try {
    await login(username.value, password.value);
    // On success the shell swaps to the app; nothing more to do here.
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
