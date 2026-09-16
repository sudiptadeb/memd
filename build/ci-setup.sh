#!/usr/bin/env bash
# One-shot setup of GitHub Actions deploys for a memd host. Run once, as root,
# on a server that already runs memd per docs/self-hosting.md:
#
#   sudo bash <app-root>/repo/build/ci-setup.sh [--host <public-ip-or-dns>] [--pubkey <file>]
#
# It reads the app user and app root from the installed memd.service, then:
#
#   1. installs memd-restart.path + memd-restart.service, so a binary swap in
#      <app-root>/releases/current/ restarts memd without sudo;
#   2. pins a deploy key in the app user's authorized_keys to the single forced
#      command build/ci-deploy.sh (no pty, no forwarding), generating an ed25519
#      key pair unless --pubkey supplies a public key you generated elsewhere;
#   3. prints the four values to store as GitHub Actions secrets.
#
# Re-running is safe: units are rewritten, an existing ci-deploy entry in
# authorized_keys is replaced, nothing else is touched.

set -euo pipefail

HOST=""
PUBKEY_FILE=""
while (( $# )); do
  case "$1" in
    --host)   HOST="$2"; shift 2 ;;
    --pubkey) PUBKEY_FILE="$2"; shift 2 ;;
    -h|--help) sed -n '2,20p' "$0"; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

[[ "$(id -u)" == "0" ]] || { echo "run as root (sudo)" >&2; exit 1; }

# ---- Discover the installed service ----------------------------------------

unit_prop() { systemctl show -p "$1" --value memd; }

APP_USER="$(unit_prop User)"
EXEC_START="$(unit_prop ExecStart | sed -n 's/.*path=\([^ ;]*\).*/\1/p')"
[[ -n "$APP_USER" && -n "$EXEC_START" ]] || {
  echo "memd.service is not installed or has no User=/ExecStart=; follow docs/self-hosting.md first" >&2
  exit 1
}
# ExecStart is <app-root>/current/memd or <app-root>/releases/current/memd.
APP_ROOT="${EXEC_START%/current/memd}"
APP_ROOT="${APP_ROOT%/releases}"
REPO="$APP_ROOT/repo"
BINARY="$APP_ROOT/releases/current/memd"
DEPLOY_SCRIPT="$REPO/build/ci-deploy.sh"
PORT="$(unit_prop ExecStart | sed -n 's/.*--port[= ]\([0-9]*\).*/\1/p')"
PORT="${PORT:-7878}"

APP_HOME="$(getent passwd "$APP_USER" | cut -d: -f6)"

[[ -d "$REPO/.git" ]] || { echo "no git checkout at $REPO" >&2; exit 1; }
[[ -f "$DEPLOY_SCRIPT" ]] || { echo "$DEPLOY_SCRIPT missing; pull main in $REPO first" >&2; exit 1; }
chmod 755 "$DEPLOY_SCRIPT"

# ci-deploy.sh resets the checkout to origin/main on every run. If main does
# not carry the script yet (setup was run from a branch), the first deploy
# removes the forced command out from under itself and every later run fails
# with "No such file or directory".
if runuser -u "$APP_USER" -- git -C "$REPO" fetch --quiet origin main 2>/dev/null \
   && ! runuser -u "$APP_USER" -- git -C "$REPO" cat-file -e origin/main:build/ci-deploy.sh 2>/dev/null; then
  echo "WARNING: build/ci-deploy.sh is not on origin/main yet; merge it to main before the" >&2
  echo "         first deploy, or that deploy will delete the forced command it runs as" >&2
fi

echo "app user : $APP_USER"
echo "app root : $APP_ROOT"
echo "binary   : $BINARY"
echo "port     : $PORT"

# The build runs as the app user; make sure the toolchain is there before the
# first workflow run finds out.
# Same PATH ci-deploy.sh uses under sshd's forced command (no login profile).
CI_PATH="$APP_HOME/go/bin:$APP_HOME/.local/bin:/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin"
missing=0
for tool in go npm git curl; do
  if ! runuser -u "$APP_USER" -- env PATH="$CI_PATH" bash -c "command -v $tool" >/dev/null 2>&1; then
    echo "WARNING: '$tool' not on PATH for $APP_USER; ci-deploy.sh will fail until it is" >&2
    missing=1
  fi
done
(( missing )) && echo "         (Go must satisfy the 'go' line in $REPO/go.mod)" >&2

# ---- 1. Restart-on-binary-change path unit ---------------------------------

cat > /etc/systemd/system/memd-restart.service <<EOF
[Unit]
Description=Restart memd after a deploy replaced its binary
After=memd.service

[Service]
Type=oneshot
ExecStart=/bin/systemctl restart memd.service
EOF

cat > /etc/systemd/system/memd-restart.path <<EOF
[Unit]
Description=Watch the deployed memd binary and restart the service when it changes

[Path]
PathChanged=$BINARY
Unit=memd-restart.service

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now memd-restart.path
echo "installed memd-restart.path (watching $BINARY)"

# ---- 2. Deploy key pinned to the forced command ----------------------------

SSH_DIR="$APP_HOME/.ssh"
AUTH_KEYS="$SSH_DIR/authorized_keys"
install -d -m 700 -o "$APP_USER" -g "$APP_USER" "$SSH_DIR"
touch "$AUTH_KEYS"
chown "$APP_USER:$APP_USER" "$AUTH_KEYS"
chmod 600 "$AUTH_KEYS"

PRIVATE_KEY=""
if [[ -n "$PUBKEY_FILE" ]]; then
  PUBKEY="$(cat "$PUBKEY_FILE")"
else
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  ssh-keygen -q -t ed25519 -N "" -C "memd-ci-deploy" -f "$tmp/key"
  PUBKEY="$(cat "$tmp/key.pub")"
  PRIVATE_KEY="$(cat "$tmp/key")"
fi

# Replace any earlier ci-deploy entry, then append the new one.
grep -v "command=\"$DEPLOY_SCRIPT\"" "$AUTH_KEYS" > "$AUTH_KEYS.new" || true
printf 'restrict,command="%s" %s\n' "$DEPLOY_SCRIPT" "$PUBKEY" >> "$AUTH_KEYS.new"
mv "$AUTH_KEYS.new" "$AUTH_KEYS"
chown "$APP_USER:$APP_USER" "$AUTH_KEYS"
chmod 600 "$AUTH_KEYS"
echo "pinned deploy key in $AUTH_KEYS to $DEPLOY_SCRIPT"

# ---- 3. Values for the GitHub secrets --------------------------------------

[[ -n "$HOST" ]] || HOST="$(hostname -I 2>/dev/null | awk '{print $1}')"
KNOWN_HOSTS="$(ssh-keyscan -t ed25519 -p 22 "$HOST" 2>/dev/null || true)"
[[ -n "$KNOWN_HOSTS" ]] || KNOWN_HOSTS="$HOST $(awk '{print $1, $2}' /etc/ssh/ssh_host_ed25519_key.pub)"

cat <<EOF

Done. Store these as repository secrets (Settings -> Secrets and variables ->
Actions) in the memd repo:

DEPLOY_HOST
$HOST

DEPLOY_USER
$APP_USER

DEPLOY_KNOWN_HOSTS
$KNOWN_HOSTS
EOF

if [[ -n "$PRIVATE_KEY" ]]; then
  cat <<EOF

DEPLOY_SSH_KEY  (the whole block, including the BEGIN/END lines; it is shown
once and not kept on this server)
$PRIVATE_KEY
EOF
else
  echo
  echo "DEPLOY_SSH_KEY: the private half of $PUBKEY_FILE"
fi

cat <<EOF

Optionally set the repository variable DEPLOY_URL (Settings -> Secrets and
variables -> Actions -> Variables) to the public site, e.g.
https://memd.example.com, and the workflow will verify it after each deploy.

Then push to main, or run "Deploy to production" from the Actions tab.
EOF
