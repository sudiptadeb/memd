# Self-Hosting memd

This playbook describes one conservative way to run `memd serve` on a Linux
server behind nginx and HTTPS. It keeps the application source, releases, and
runtime data in one owned tree while leaving service management and TLS at the
system boundary.

The examples use placeholders. Replace:

| Placeholder | Meaning |
|-------------|---------|
| `<app-user>` | Unix user that owns and runs memd, for example `memd` |
| `<app-root>` | Deployment root, for example `/home/<app-user>/hosted/memd` |
| `<repo-url>` | Git URL for the memd repository |
| `<domain>` | Public hostname, for example `memd.example.com` |
| `<port>` | Local memd port, for example `7878` |

## Target Layout

```
<app-root>/
  repo/              # source checkout
  releases/
    current/         # current binary
    previous/        # one rollback binary, consumed by rollback.sh
  current -> releases/current
  runtime/
    data/            # sqlite DB, XDG config, git working copies
    env              # service environment; chmod 600
    logs/            # optional app-owned logs
  nginx/
    <domain>.conf    # nginx site config source
  deploy.sh
  rollback.sh
```

`repo/` is build input. `runtime/` is persistent state and should be backed up.
`releases/current/memd` is the stable executable path used by systemd.

## Create The Service User

Run as an admin user with `sudo`:

```bash
sudo adduser --disabled-password --gecos "" --shell /bin/bash <app-user>
```

This creates a normal Unix account with a home directory and bash shell, but no
password login. Operators can enter it with:

```bash
sudo -iu <app-user>
```

Do not add the service user to `sudo` unless you intentionally want it to manage
system services.

## Clone And Prepare

Become the service user:

```bash
sudo -iu <app-user>
```

Create the deployment tree:

```bash
mkdir -p <app-root>/{releases/current,releases/previous,runtime/data,runtime/logs,nginx}
cd <app-root>
git clone <repo-url> repo
ln -sfn <app-root>/releases/current <app-root>/current
```

Create the service environment:

```bash
openssl rand -base64 48
nano <app-root>/runtime/env
```

Use the random value as `MEMD_SESSION_SECRET`:

```bash
XDG_CONFIG_HOME=<app-root>/runtime/data/xdg-config
MEMD_DATABASE_URL=sqlite:///<app-root>/runtime/data/memd.db
MEMD_SESSION_SECRET=<random-secret>
MEMD_SESSION_MAX_AGE=168h
```

For private Git-backed memory directories, prefer HTTPS remotes and personal
access tokens with repo access. Users enter their Git username and PAT when they
add the Git directory in the memd UI. Do not commit PATs, put them in clone URLs
saved to shared docs, or rely on SSH keys for end-user deployments. SSH keys are
difficult to provision, rotate, and scope consistently across users. OAuth-based
Git-provider integrations may replace this manual PAT setup later.

For GitHub, use a fine-grained personal access token where possible:

1. Open GitHub Settings → Developer settings → Personal access tokens →
   Fine-grained tokens → Generate new token.
2. Set a name and expiration, choose the resource owner, and select only the
   memory repository.
3. Set repository **Contents** to **Read and write**. Leave unrelated
   permissions unset.
4. Paste the token and GitHub username into memd, then run **Test connection**.
   The check verifies read access, local commit/write behavior, and push/delete
   of a temporary branch for PR/MR-style workflows.

Organization-owned repositories may require token approval or SAML
authorization. Protected branches can still block memd's normal direct push to
the configured branch, even when the temporary branch check passes.

For GitLab, use a project access token where available, or a personal access
token otherwise. Grant `write_repository`, use the token as the password, and
enter any non-empty username such as `oauth2`. GitLab protected branches and
push rules still apply to memd's configured branch.

Lock down the environment file:

```bash
chmod 600 <app-root>/runtime/env
```

## Deploy Script

Create `<app-root>/deploy.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

APP_ROOT="<app-root>"
REPO="$APP_ROOT/repo"
RELEASES="$APP_ROOT/releases"
CURRENT="$RELEASES/current"
PREVIOUS="$RELEASES/previous"

cd "$REPO"

bash build/build.sh clean
bash build/build.sh host

mapfile -t binaries < <(find "$REPO/dist/linux" -maxdepth 1 -type f -name 'memd-amd64-*' | sort)

if [[ "${#binaries[@]}" -ne 1 ]]; then
  echo "expected exactly one linux amd64 memd binary, found ${#binaries[@]}" >&2
  printf '%s\n' "${binaries[@]}" >&2
  exit 1
fi

binary="${binaries[0]}"

mkdir -p "$CURRENT" "$PREVIOUS"

if [[ -x "$CURRENT/memd" ]]; then
  cp "$CURRENT/memd" "$PREVIOUS/memd"
  [[ -f "$CURRENT/release.txt" ]] && cp "$CURRENT/release.txt" "$PREVIOUS/release.txt"
fi

cp "$binary" "$CURRENT/memd.new"
chmod 755 "$CURRENT/memd.new"
mv "$CURRENT/memd.new" "$CURRENT/memd"

ln -sfn "$CURRENT" "$APP_ROOT/current"

{
  echo "deployed_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "git_sha=$(git rev-parse --short HEAD 2>/dev/null || true)"
  echo "source_binary=$binary"
} > "$CURRENT/release.txt"

echo "deployed memd"
cat "$CURRENT/release.txt"
```

Make it executable and deploy the first build:

```bash
chmod +x <app-root>/deploy.sh
<app-root>/deploy.sh
```

## Rollback Script

Create `<app-root>/rollback.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

APP_ROOT="<app-root>"
RELEASES="$APP_ROOT/releases"
CURRENT="$RELEASES/current"
PREVIOUS="$RELEASES/previous"

if [[ ! -x "$PREVIOUS/memd" ]]; then
  echo "no previous release available; rollback already used or no previous deployment exists" >&2
  exit 1
fi

if [[ ! -d "$CURRENT" ]]; then
  echo "current release directory is missing: $CURRENT" >&2
  exit 1
fi

cp "$PREVIOUS/memd" "$CURRENT/memd.rollback"
chmod 755 "$CURRENT/memd.rollback"
mv "$CURRENT/memd.rollback" "$CURRENT/memd"

if [[ -f "$PREVIOUS/release.txt" ]]; then
  cp "$PREVIOUS/release.txt" "$CURRENT/release.txt"
else
  {
    echo "rolled_back_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    echo "source=previous"
  } > "$CURRENT/release.txt"
fi

rm -f "$PREVIOUS/memd" "$PREVIOUS/release.txt"

echo "rolled back memd"
echo "previous release consumed; another rollback is not available"
cat "$CURRENT/release.txt"
```

Make it executable:

```bash
chmod +x <app-root>/rollback.sh
```

Rollback changes the binary only. Restart the service separately after rollback.

## Systemd Service

Exit back to the admin user and create `/etc/systemd/system/memd.service`:

```ini
[Unit]
Description=memd server
After=network-online.target
Wants=network-online.target

[Service]
User=<app-user>
Group=<app-user>
WorkingDirectory=<app-root>/repo
EnvironmentFile=<app-root>/runtime/env
ExecStart=<app-root>/current/memd serve --port <port>
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
```

Enable it:

```bash
sudo systemctl daemon-reload
sudo systemctl enable memd
```

## First Database Bootstrap

Initialize the account database once from an interactive shell as the service
user. This lets memd prompt for the first super-admin password without putting
it in a process list or shell history:

```bash
sudo -iu <app-user>
set -a
source <app-root>/runtime/env
set +a
<app-root>/current/memd serve --init-db --create-super-admin <admin-username>
```

After memd prints the local web UI URL, press `Ctrl-C`. Then start the service:

```bash
exit
sudo systemctl start memd
sudo systemctl status memd --no-pager
```

Verify the local listener:

```bash
curl -I http://127.0.0.1:<port>
```

## Nginx And TLS

Install nginx and Certbot's nginx plugin:

```bash
sudo apt update
sudo apt install -y nginx certbot python3-certbot-nginx
```

Create `<app-root>/nginx/<domain>.conf`:

```nginx
server {
    listen 80;
    server_name <domain>;

    location / {
        proxy_pass http://127.0.0.1:<port>;
        proxy_http_version 1.1;

        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        proxy_buffering off;
        proxy_request_buffering off;
        proxy_read_timeout 3600;
        proxy_send_timeout 3600;
    }
}
```

Enable the site:

```bash
sudo ln -s <app-root>/nginx/<domain>.conf /etc/nginx/sites-enabled/<domain>.conf
sudo nginx -t
sudo systemctl reload nginx
```

Ask Certbot to issue the certificate and update nginx:

```bash
sudo certbot --nginx -d <domain>
```

Choose HTTP-to-HTTPS redirect when prompted. Certbot will add the TLS listener,
certificate paths, and redirect handling. Confirm nginx still passes
`X-Forwarded-Proto`; memd uses it to mark session cookies secure behind the
reverse proxy.

Test:

```bash
curl -I http://<domain>
curl -I https://<domain>
```

Expected:

```
HTTP -> 301 redirect to HTTPS
HTTPS -> 200 OK from memd
```

## Future Deploys

Run the source update and deploy as the service user:

```bash
sudo -iu <app-user>
cd <app-root>/repo
git pull --ff-only
<app-root>/deploy.sh
exit
sudo systemctl restart memd
```

Rollback:

```bash
sudo -iu <app-user>
<app-root>/rollback.sh
exit
sudo systemctl restart memd
```

The rollback script consumes the previous release. Running it twice without a
new deploy fails intentionally.

## Backups

Back up at least:

```
<app-root>/runtime/data/
```

This contains the account database, connector records, Git PATs, and cloned Git
working copies. Connector tokens and Git PATs are credentials, so treat backups
as sensitive.

Memory content should ideally live in user-owned Git repositories configured in
the UI. Configure private repositories as HTTPS remotes backed by repo-scoped
PATs entered in the Git directory form. Back up local-folder memory directories
separately if you use them.

## Security Notes

- Keep memd bound to `127.0.0.1`; expose it through nginx over HTTPS.
- Set `MEMD_SESSION_SECRET` before serving real users.
- Prefer connector header auth: `Authorization: Bearer <token>`.
- Avoid token-in-URL connector forms for public deployments when the client can
  send headers.
- Use HTTPS Git remotes plus repo-scoped PATs for Git-backed directories. Treat
  those PATs as credentials and rotate them through your Git provider when
  needed.
- Do not give the service user `sudo` unless you need it.
- Keep the host patched and monitor nginx/systemd logs.
