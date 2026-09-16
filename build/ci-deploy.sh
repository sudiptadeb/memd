#!/usr/bin/env bash
# memd CI deploy. This is the forced command behind the GitHub Actions deploy
# key (see build/ci-setup.sh and .github/workflows/deploy.yml): whatever the
# workflow asks for over SSH, sshd runs this script instead, as the app user.
#
# It syncs the checkout to origin/main, builds, swaps the binary into place,
# and then proves the service came up on the new binary. If it did not, the
# previous binary is restored and the run fails, so a green workflow means the
# service is serving, not merely that a file was copied.
#
# The service restart is not done here. A systemd path unit (memd-restart.path,
# installed by ci-setup.sh) watches releases/current/memd and restarts memd
# whenever the binary changes, so this script needs no sudo.
#
# Layout (docs/self-hosting.md "Target Layout"): this file lives at
# <app-root>/repo/build/ci-deploy.sh, and everything else is derived from that.
#
# Env overrides: MEMD_PORT (default 7878), MEMD_HEALTH_TIMEOUT seconds (default 60).

set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP_ROOT="$(dirname "$REPO")"
RELEASES="$APP_ROOT/releases"
CURRENT="$RELEASES/current"
PREVIOUS="$RELEASES/previous"
BRANCH="main"
PORT="${MEMD_PORT:-7878}"
HEALTH_TIMEOUT="${MEMD_HEALTH_TIMEOUT:-60}"
SERVICE="memd"

# sshd runs a forced command with a minimal PATH, so the usual toolchain
# locations are added explicitly rather than relying on a login profile.
export PATH="$HOME/go/bin:$HOME/.local/bin:/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin"

log() { printf '%s\n' "$*"; }
die() { printf 'ci-deploy: %s\n' "$*" >&2; exit 1; }

# The forced command ignores the argument the workflow passes, but record it so
# the journal shows what was asked for.
log "ci-deploy: requested '${SSH_ORIGINAL_COMMAND:-<none>}' at $(date -u +%Y-%m-%dT%H:%M:%SZ)"

service_pid() { systemctl show -p MainPID --value "$SERVICE" 2>/dev/null || echo 0; }

healthy() {
  curl -fsS -o /dev/null --max-time 5 "http://127.0.0.1:${PORT}/"
}

# Wait until the service has restarted (MainPID moved on from $1) and answers.
wait_healthy() {
  local old_pid="$1" deadline=$(( $(date +%s) + HEALTH_TIMEOUT )) pid
  while (( $(date +%s) < deadline )); do
    pid="$(service_pid)"
    if [[ "$pid" != "0" && "$pid" != "$old_pid" ]] && healthy; then
      return 0
    fi
    sleep 2
  done
  return 1
}

# ---- 1. Sync source ---------------------------------------------------------

cd "$REPO"
git fetch --quiet --prune origin "$BRANCH"
git checkout --quiet --force "$BRANCH"
git reset --quiet --hard "origin/$BRANCH"
sha="$(git rev-parse --short HEAD)"
log "HEAD is now at $(git log -1 --format='%h %s')"

# ---- 2. Build ---------------------------------------------------------------

export VERSION="${VERSION:-0.1.0-${sha}}"
bash build/build.sh clean
bash build/build.sh host

mapfile -t binaries < <(find "$REPO/dist/linux" -maxdepth 1 -type f -name 'memd-amd64-*' | sort)
if [[ "${#binaries[@]}" -ne 1 ]]; then
  printf '%s\n' "${binaries[@]}" >&2
  die "expected exactly one linux amd64 memd binary, found ${#binaries[@]}"
fi
binary="${binaries[0]}"

# ---- 3. Swap the binary into place -----------------------------------------

mkdir -p "$CURRENT" "$PREVIOUS"
have_previous=0
if [[ -x "$CURRENT/memd" ]]; then
  cp "$CURRENT/memd" "$PREVIOUS/memd"
  [[ -f "$CURRENT/release.txt" ]] && cp "$CURRENT/release.txt" "$PREVIOUS/release.txt"
  have_previous=1
fi

old_pid="$(service_pid)"

cp "$binary" "$CURRENT/memd.new"
chmod 755 "$CURRENT/memd.new"
mv "$CURRENT/memd.new" "$CURRENT/memd"
ln -sfn "$CURRENT" "$APP_ROOT/current"

{
  echo "deployed_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "git_sha=${sha}"
  echo "version=${VERSION}"
  echo "source_binary=${binary}"
} > "$CURRENT/release.txt"

log "deployed memd"
cat "$CURRENT/release.txt"

# ---- 4. Prove the service came up on the new binary ------------------------

log "waiting for ${SERVICE} to come up on the new binary (old pid ${old_pid})..."
if wait_healthy "$old_pid"; then
  log "health check passed: $("$CURRENT/memd" version 2>/dev/null || echo "memd ${VERSION}") serving on :${PORT}"
  exit 0
fi

log "health check FAILED after ${HEALTH_TIMEOUT}s" >&2
systemctl status "$SERVICE" --no-pager --lines=20 >&2 || true

if (( have_previous )); then
  log "restoring previous binary..." >&2
  failed_pid="$(service_pid)"
  cp "$PREVIOUS/memd" "$CURRENT/memd.rollback"
  chmod 755 "$CURRENT/memd.rollback"
  mv "$CURRENT/memd.rollback" "$CURRENT/memd"
  if [[ -f "$PREVIOUS/release.txt" ]]; then
    cp "$PREVIOUS/release.txt" "$CURRENT/release.txt"
  fi
  if wait_healthy "$failed_pid"; then
    log "rolled back: previous binary is serving again" >&2
  else
    log "rollback did NOT come up either; manual intervention needed" >&2
  fi
fi

die "deploy of ${sha} failed"
