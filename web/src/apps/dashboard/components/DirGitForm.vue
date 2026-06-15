<template>
  <div class="git-fields">
    <div class="field">
      <label class="field-label">Remote URL<span class="req">*</span></label>
      <input class="input" v-model="model.remote_url" required placeholder="https://github.com/you/memory.git" />
      <div class="field-hint">Use an HTTPS remote for hosted memd. SSH remotes are best kept for local runs.</div>
    </div>
    <div class="field">
      <label class="field-label">Branch</label>
      <input class="input" v-model="model.branch" placeholder="main" />
    </div>
    <div class="field">
      <label class="field-label">Base path in repo</label>
      <input class="input" v-model="model.base_path" placeholder="repo root" />
    </div>
    <div class="field">
      <label class="field-label">Author name</label>
      <input class="input" v-model="model.author_name" placeholder="memd" />
    </div>
    <div class="field">
      <label class="field-label">Author email</label>
      <input class="input" v-model="model.author_email" placeholder="memd@localhost" />
    </div>
    <div class="field">
      <label class="field-label">Git username</label>
      <input class="input" v-model="model.auth_username" autocomplete="username" placeholder="your GitHub username" />
      <div class="field-hint">For GitHub, your normal username works with a personal access token.</div>
    </div>
    <div class="field">
      <label class="field-label">Personal access token</label>
      <input
        class="input"
        v-model="model.auth_token"
        type="password"
        autocomplete="off"
        placeholder="github_pat_..."
      />
      <div class="field-hint">
        GitHub: selected repo + Contents read/write. GitLab: write_repository. Stored with your memd account data.
      </div>
    </div>
    <div class="field">
      <label class="field-label">SSH key path</label>
      <input class="input" v-model="model.ssh_key_path" placeholder="local use only" />
      <div class="field-hint">Legacy local option. Hosted deployments should use HTTPS plus a token.</div>
    </div>
    <div class="field git-check">
      <button
        class="btn secondary"
        type="button"
        @click="runCheck"
        :disabled="checking || !model.remote_url"
        :aria-busy="checking ? 'true' : 'false'"
      >
        <MIcon name="refresh-cw" :class="checking ? 'spin' : ''" />
        <span>{{ checking ? "Checking..." : "Test connection" }}</span>
      </button>
      <div class="field-hint">Checks read, local commit, temporary branch push, and cleanup.</div>
      <div class="git-check-results" v-if="checkResults.length">
        <div v-for="check in checkResults" :key="check.id" class="git-check-row" :class="check.ok ? 'ok' : 'fail'">
          <MIcon :name="check.ok ? 'check' : 'triangle-alert'" />
          <div>
            <b>{{ check.label }}</b>
            <span>{{ check.ok ? check.detail : check.error }}</span>
          </div>
        </div>
      </div>
      <span class="err" v-if="checkErr">{{ checkErr }}</span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from "vue";
import MIcon from "@/shared/components/MIcon.vue";
import { git, ApiError } from "@/shared/api";
import { toast } from "@/shared/bus";
import type { GitCheckResult, GitConfig } from "@/shared/types";

// The git backend's config form plus its non-destructive "Test connection"
// report. `model` is the reactive GitConfig owned by the parent create form.
const props = defineProps<{ model: GitConfig }>();

const checking = ref(false);
const checkErr = ref("");
const checkResults = ref<GitCheckResult[]>([]);

// Mirror the Alpine `gitDirectoryPayload`: fill in the defaults the backend
// expects when fields are blank, without mutating what the user typed.
function payload(): GitConfig {
  const m = props.model;
  return {
    remote_url: m.remote_url,
    branch: m.branch || "main",
    base_path: m.base_path || "",
    author_name: m.author_name || "memd",
    author_email: m.author_email || "memd@localhost",
    auth_username: m.auth_username || "",
    auth_token: m.auth_token || "",
    ssh_key_path: m.ssh_key_path || "",
  };
}

async function runCheck(): Promise<void> {
  checkErr.value = "";
  checkResults.value = [];
  checking.value = true;
  try {
    const report = await git.check(payload());
    checkResults.value = report.checks || [];
    if (report.ok) {
      toast("Git connection verified", "success");
    } else {
      checkErr.value = "Connection check failed";
    }
  } catch (e) {
    checkErr.value = e instanceof ApiError ? e.message : String(e);
  } finally {
    checking.value = false;
  }
}

// Let the parent reset results when it reopens the form.
defineExpose({
  reset(): void {
    checking.value = false;
    checkErr.value = "";
    checkResults.value = [];
  },
});
</script>
