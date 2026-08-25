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

## Remote Terminal Rendezvous (termulaa rc)

Optional. memd can act as the rendezvous for termulaa's reverse tunnel: a
`termulaa -rc` agent on a user's machine dials out to memd, and browsers reach
that terminal through memd. memd is a reference implementation of the termulaa
rc protocol — the wire details live in the termulaa repository under
`docs/rc-protocol.md`; this section covers only memd's operational side.

The proxied terminal can be served in one of two modes:

| Mode | Where the terminal lives | Viewer auth | Extra DNS/TLS |
|------|--------------------------|-------------|---------------|
| **path mode** (default) | `https://<domain>/rc/t/<agent>/` on memd's existing host | memd's own login session + agent ownership | none |
| **host mode** (optional hardening) | a dedicated view host, e.g. `term.<domain>` | one-time `/?t=<token>` pairing link → `HttpOnly` cookie | a DNS record and a certificate |

With neither `MEMD_RC` nor `MEMD_RC_VIEW_HOST` set the feature is fully inert.

**The security tradeoff, plainly.** Path mode puts a remote shell and the memd
web app on the SAME browser origin, which removes the isolation the browser
would otherwise enforce between them: an XSS bug in either one can drive the
other — read the terminal, inject keystrokes, or call memd's APIs as the
logged-in user. Host mode gives the terminal its own origin and restores that
boundary. Path mode is the pragmatic default because it needs no DNS or TLS
work; choose host mode when worst-case containment matters more than setup
effort.

### Path mode (default)

Enable by adding to `<app-root>/runtime/env`:

```bash
MEMD_RC=1
# Optional: a dedicated token-signing key. When unset, one is derived from
# MEMD_SESSION_SECRET. Setting it lets you rotate tunnel tokens independently.
# MEMD_RC_TOKEN_SECRET=<random-secret>
```

No new DNS record, no new certificate, no new vhost. The only nginx work is on
the **existing** memd vhost: terminal traffic (and the agent's `/rc/tunnel`
connection) is WebSockets, so the main vhost's `location /` needs the upgrade
headers if it does not already have them:

```nginx
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
```

(Harmless for normal requests with nginx ≥ 1.3 — `$http_upgrade` is empty
then.) Keep terminal output unbuffered and idle terminals alive, as in the
main vhost template: `proxy_buffering off;` and generous
`proxy_read_timeout`/`proxy_send_timeout` (3600s). smux keepalives flow every
10 seconds, so the timeout is never approached.

Everything is then on the main host, behind memd's normal login:

- `https://<domain>/rc` — mint tokens, see connected agents, and open each
  agent's terminal via its `/rc/t/<agent>/` link.
- `https://<domain>/rc/t/<agent>/` — the terminal itself. The viewer must be
  logged in to memd **and** be the user the agent's tunnel token was minted
  for; anyone else gets a 404. There is no separate pairing step and no
  viewer cookie — the memd session is the credential. Browser requests from
  any other origin are refused before they reach the tunnel.

Internally memd strips the `/rc/t/<agent>` prefix before proxying and hands it
to termulaa as `X-Forwarded-Prefix`; termulaa renders its pages against that
base path (path mode needs a base-path-aware termulaa build; older builds
require host mode). The proxied responses carry termulaa's own
security headers, not memd's — memd's global CSP does not apply to the viewer
surface (and is unchanged everywhere else).

### Host mode (optional origin isolation)

Set a dedicated view host instead (this selects host mode; `MEMD_RC` is not
needed):

```bash
MEMD_RC_VIEW_HOST=<view-domain>
# MEMD_RC_TOKEN_SECRET=<random-secret>   # same optional knob as above
```

In host mode the path viewer is disabled — the terminal is reachable only on
the view host, which is the point of the mode. Two hostnames are then
involved:

| Host | Placeholder | Serves |
|------|-------------|--------|
| the rendezvous host | `<domain>` (memd's existing hostname) | `/rc` pairing page, `POST /rc/api/tokens`, and the agent's `GET /rc/tunnel` WebSocket — alongside all of memd's normal routes |
| the view host | `<view-domain>`, e.g. `term.memd.example.com` | every path is proxied to the tunneled terminal |

#### DNS and TLS for the view host

The view host needs its own DNS A/CNAME record pointing at the same server —
subdomains do not exist until you create them.

Check certificate coverage before assuming anything. A wildcard matches
exactly **one** label: `*.example.com` covers `term.example.com` but NOT
`term.memd.example.com`; a certificate for `memd.example.com` covers neither.
Inspect what your existing certificate actually contains:

```bash
openssl s_client -connect <domain>:443 -servername <domain> </dev/null 2>/dev/null \
  | openssl x509 -noout -text | grep -A1 "Subject Alternative Name"
```

(Do this from a network path without a TLS-intercepting proxy, or you will be
reading the interceptor's certificate, not your origin's.)

If the view host is not covered, issue a certificate for it:

```bash
sudo certbot --nginx -d <view-domain>
```

Practical upshot for a deployment at `memd.example.com`: a view host of
`term.memd.example.com` (the documented default shape) is two labels deep and
will need its own certificate as above, while `term.example.com` may already
be covered if you hold a `*.example.com` wildcard. Either name works — the
view host is fully configurable.

#### Nginx vhost for the view host

Create `<app-root>/nginx/<view-domain>.conf`, proxying to the **same** memd
port. Terminal traffic is WebSockets, so the upgrade headers and long
timeouts matter:

```nginx
server {
    listen 80;
    server_name <view-domain>;

    location / {
        proxy_pass http://127.0.0.1:<port>;
        proxy_http_version 1.1;

        # WebSocket upgrade for the terminal itself.
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";

        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # Terminal output must not be buffered, and idle terminals must not
        # be cut off.
        proxy_buffering off;
        proxy_request_buffering off;
        proxy_read_timeout 3600;
        proxy_send_timeout 3600;
    }
}
```

`proxy_set_header Host $host` is what routes these requests inside memd: it
compares the incoming `Host` against `MEMD_RC_VIEW_HOST`. Enable the site and
reload as with the main vhost.

The agent's WebSocket (`/rc/tunnel`) arrives on the MAIN host, which needs the
same `Upgrade`/`Connection` lines as in path mode.

### Operating notes

- Users mint tunnel tokens at `https://<domain>/rc` (behind the normal memd
  login). In path mode the terminal link is shown right there; in host mode a
  browser is paired via the one-time `/?t=<token>` link, which moves the
  token into an `HttpOnly` cookie.
- Tokens are stateless and HMAC-signed; there is no server-side token store.
  Expiry (default 30 days, max 90) is the retirement mechanism. Rotating
  `MEMD_RC_TOKEN_SECRET` (or `MEMD_SESSION_SECRET` when no dedicated secret is
  set) invalidates every outstanding tunnel token at once.
- In both modes the tunnel token is the **agent's** credential. In path mode
  it additionally decides which memd user owns the agent — only that user's
  login session can open the terminal.
- The `/rc` page shows each connected agent with its live tunnel count,
  straight from the in-process pool — an agent with no live tunnel shows as
  absent, so what you see is what is actually connected.


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
