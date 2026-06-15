<template>
  <section class="app-section invite-accept-section">
    <div class="section-head">
      <div class="titles">
        <h2>Team invite</h2>
        <span class="desc">Join a team to share directories and connectors with its members.</span>
      </div>
    </div>

    <div class="file-loading" v-if="loading">Loading invite…</div>

    <div class="picker-error" v-else-if="loadErr" v-text="loadErr"></div>

    <div class="invite-callout" v-else-if="preview">
      <div class="label" v-text="preview.valid ? 'Join ' + preview.team_name : 'Team invite'"></div>
      <div
        class="sub"
        v-text="
          preview.valid
            ? 'Role: ' + roleLabel(preview.role)
            : preview.error || 'This invite is no longer valid.'
        "
      ></div>

      <div class="invite-callout-meta" v-if="preview.valid">
        <span class="dot" v-text="roleLabel(preview.role)"></span>
        <span class="sub" v-text="usage"></span>
        <span class="sub" v-text="expiry"></span>
      </div>

      <p class="sub" v-if="preview.valid && !signedIn">Sign in to accept this invite.</p>

      <span class="err" v-if="acceptErr" v-text="acceptErr"></span>

      <div class="invite-callout-actions">
        <button class="btn ghost" type="button" @click="goBack">Cancel</button>
        <button
          class="btn primary"
          type="button"
          v-if="preview.valid && signedIn"
          :disabled="accepting"
          @click="accept"
        >
          Join team
        </button>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useRoute, useRouter } from "vue-router";
import { invites, ApiError } from "@/shared/api";
import { useSession } from "@/shared/session";
import { toast } from "@/shared/bus";
import type { InvitePreview } from "@/shared/types";

const route = useRoute();
const router = useRouter();
const { user } = useSession();

const token = computed(() => {
  const raw = route.params.token;
  return Array.isArray(raw) ? (raw[0] ?? "") : (raw ?? "");
});

const preview = ref<InvitePreview | null>(null);
const loading = ref(true);
const loadErr = ref("");
const accepting = ref(false);
const acceptErr = ref("");

const signedIn = computed(() => user.value != null);

function roleLabel(role?: string): string {
  return role || "member";
}

const usage = computed(() => {
  if (!preview.value) return "";
  const limit = preview.value.max_uses ? String(preview.value.max_uses) : "unlimited";
  return `${preview.value.use_count || 0} / ${limit}`;
});

const expiry = computed(() => {
  if (!preview.value?.expires_at) return "No expiry";
  return new Date(preview.value.expires_at).toLocaleString();
});

async function load(): Promise<void> {
  loading.value = true;
  loadErr.value = "";
  preview.value = null;
  if (!token.value) {
    loadErr.value = "invite not found";
    loading.value = false;
    return;
  }
  try {
    const data = await invites.preview(token.value);
    preview.value = data.invite ?? null;
  } catch (error) {
    loadErr.value = error instanceof ApiError ? error.message : "invite not found";
  } finally {
    loading.value = false;
  }
}

async function accept(): Promise<void> {
  if (!token.value || !signedIn.value) return;
  accepting.value = true;
  acceptErr.value = "";
  try {
    const data = await invites.accept(token.value);
    toast(data.team ? `Joined ${data.team.name}` : "Joined team", "success");
    await router.push("/teams");
  } catch (error) {
    acceptErr.value = error instanceof ApiError ? error.message : "join failed";
  } finally {
    accepting.value = false;
  }
}

function goBack(): void {
  void router.push("/teams");
}

onMounted(load);
</script>

<style scoped>
.invite-accept-section {
  max-width: 32rem;
}
.invite-callout-meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 0.5rem;
  margin-top: 0.5rem;
}
.invite-callout-actions {
  display: flex;
  justify-content: flex-end;
  gap: 0.5rem;
  margin-top: 0.75rem;
}
</style>
