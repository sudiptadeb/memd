// A signed-out visitor opening an invite link can't reach the invite page (the
// login screen replaces the router until the session resolves), and the SSO
// round-trip drops the URL hash entirely. Stash the token in sessionStorage so
// the app can return to the invite right after sign-in.

const KEY = "memd.pending-invite";

export function stashInviteToken(token: string): void {
  try {
    sessionStorage.setItem(KEY, token);
  } catch {
    // Storage unavailable (private mode / quota): the invite link still works
    // for local logins, which never leave the page.
  }
}

export function peekInviteToken(): string {
  try {
    return sessionStorage.getItem(KEY) || "";
  } catch {
    return "";
  }
}

export function takeInviteToken(): string {
  const token = peekInviteToken();
  if (token) {
    try {
      sessionStorage.removeItem(KEY);
    } catch {
      // ignore
    }
  }
  return token;
}
