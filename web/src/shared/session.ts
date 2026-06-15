import { computed, ref } from "vue";
import { auth as authApi, session as sessionApi } from "@/shared/api";
import type { AuthConfig, SessionUser } from "@/shared/types";

// Shared session state (module-level singletons). The app shell calls refresh()
// once on mount; components read `user`/`checked` and call login()/logout().

const user = ref<SessionUser | null>(null);
const authConfig = ref<AuthConfig>({ oidc_enabled: false });
const checked = ref(false);

async function refresh(): Promise<void> {
  try {
    const res = await sessionApi.get();
    user.value = res.user;
    authConfig.value = res.auth;
  } finally {
    checked.value = true;
  }
}

async function login(username: string, password: string): Promise<void> {
  const res = await authApi.login(username, password);
  user.value = res.user;
}

async function logout(): Promise<string | undefined> {
  const res = await authApi.logout();
  user.value = null;
  return res.logout_url;
}

export function useSession() {
  return {
    user,
    auth: authConfig,
    checked,
    isSuperAdmin: computed(() => user.value?.super_admin === true),
    refresh,
    login,
    logout,
    ssoLoginUrl: authApi.ssoLoginUrl,
  };
}
