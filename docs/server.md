# Running memd

memd is a Go server you run locally. It exposes file-based memory directories to AI agents over MCP. Markdown is the default for prose, but HTML, CSV, JSON, and other text files can be stored as memory artifacts too.

## Current v1 Behavior

What works today:

- Local + Git backends. Git commits are debounced (one per session, not per write).
- Responsive web UI with separate views for How it works, Teams, Directories,
  Connectors, and Activity. Desktop keeps a side navigation rail; smaller
  screens use a hamburger drawer and show Activity as a normal page.
- Web UI for managing directories and MCP/HTTP connectors. Each connector gets a bearer token credential; token-in-URL remains supported for local/legacy use.
- Configured mode bootstraps a local account/team metadata database. The default database is cgo-free SQLite; `MEMD_DATABASE_URL` can point at another SQL URL once additional drivers are linked.
- The web UI requires login: local accounts (super-admin created) or
  IdP-agnostic OIDC single sign-on. Super admins open `/admin` to create users,
  disable/enable accounts, reset passwords, and configure SSO.
- Regular users can create teams, invite other regular users with copyable
  invite links, and mark selected directories/connectors as team-scoped.
- MCP Streamable HTTP, the five workflows (`reorganise`, `harvest`, `dream`, `recall`, `housekeep`), managed file stats for Markdown/HTML.
- Plain HTTP connector endpoints for agents that can fetch URLs but cannot speak MCP; the UI can copy a ready-to-paste skill/instruction block.
- Localhost-bound serving. Expose it through a local tunnel or a TLS reverse
  proxy when remote agents or web clients need access.

What's planned (not yet implemented): skills/hooks injection, OAuth-based
Git-provider integrations, separate UI / MCP listeners for larger hosted
deployments, and source readers for `harvest`. See [README.md](../README.md)
Roadmap.

## Two Modes

### Quick Mode — one directory, no config

```bash
memd ~/work-memory
```

The server starts on a random localhost port and prints an MCP URL:

```
memd serving ~/work-memory

  http://127.0.0.1:48173/mcp/9f2c4a8e...

Press Ctrl-C to stop.
```

Each run gets a fresh URL. The server dies when you stop it; nothing persists outside the directory itself.

If the directory is empty or has no Markdown at the root, memd writes a starter `MEMORY.md` on first connect. Existing HTML/CSV/JSON/text files are left untouched.

### Configured Mode — multiple directories, multiple agents

```bash
memd serve
```

The server starts, opens the web UI in your browser (default `http://127.0.0.1:7878`), and runs until you stop it.

In the UI:

1. **Use the main views.** The app has How it works, Teams, Directories,
   Connectors, and Activity. On smaller screens, open the hamburger drawer to
   switch views; dark mode remains in the top bar.
2. **Create teams when needed.** Regular users can create teams, manage members,
   and create invite links with optional expiry and max-use count.
3. **Add directories.** Pick local folder or Git repo. For Git: use an HTTPS
   remote, choose a branch and base path inside the repo, then enter a Git
   username and personal access token with repo access. SSH-key Git auth remains
   available for local runs, but it is unsuitable guidance for end-user
   deployments because it is difficult to provision, rotate, and scope
   consistently.
   memd clones into a working copy under the config dir. Team owners/admins can
   mark a directory as team-scoped.
4. **Add connectors.** One per agent (e.g. "Claude Code", "Codex CLI"). Pick
   MCP or HTTP, which directories the connector can see, and whether write is
   allowed. memd generates a unique token and shows token-in-URL plus
   header-auth forms. Team owners/admins can mark a connector as team-scoped.
5. **Import/export user data.** Signed-in users can export or import their own
   directories and connectors from the Directories toolbar. Super admins do not
   manage this from the admin page.
6. **Wire up agents.** Paste MCP URLs into MCP configs, use **Copy auth** when
   the client supports headers, or use **Copy skill** for HTTP connectors.

## CLI

```
memd <directory>             # quick mode
memd serve [--port PORT]     # configured mode + web UI
memd serve --init-db         # initialize account DB, prompting for first super admin
memd data export --user USER --out FILE
memd data import --user USER --in FILE [--replace]
memd data export-legacy-config --out FILE
memd version
```

Both modes bind to `127.0.0.1`. For remote agents, web-hosted MCP clients, or
team access, put memd behind a trusted local tunnel or a TLS reverse proxy and
use connector header auth where the client supports it.

For a repeatable Linux deployment behind nginx and HTTPS, see
[Self-Hosting memd](self-hosting.md).

Configured mode account bootstrap:

```bash
# Interactive first run.
memd serve

# Non-interactive first run.
MEMD_INIT_DB=1 \
MEMD_CREATE_SUPER_ADMIN_USERNAME=sudi \
MEMD_CREATE_SUPER_ADMIN_PASSWORD='change-me' \
memd serve

# Add another super admin from the server process only. There is no API route
# for creating super admins.
memd serve --create-super-admin alice
```

Prefer the password prompt or `MEMD_CREATE_SUPER_ADMIN_PASSWORD` over
`--super-admin-password`; command-line arguments can be visible to other local
processes.

## Account Metadata Database

Configured mode stores production metadata in a SQL database:

- local users and password hashes
- super-admin markers
- teams and team memberships
- team invite token hashes and invite use records
- user-owned directories and connectors, including connector tokens, Git PATs,
  and optional team scope
- future Git branch state and sync jobs

Memory data itself should remain in user-owned Git repositories. The database is
only the control plane.

By default, memd uses:

```text
~/Library/Application Support/memd/memd.db   # macOS
~/.config/memd/memd.db                       # Linux
%APPDATA%\memd\memd.db                       # Windows
```

Override it with `MEMD_DATABASE_URL`:

```bash
MEMD_DATABASE_URL=sqlite:///var/lib/memd/memd.db memd serve
```

SQLite is opened through the pure-Go `modernc.org/sqlite` driver, so the normal
`CGO_ENABLED=0` build still works. memd adds conservative SQLite defaults when
they are not already present in the DSN: foreign keys on, WAL journal mode,
normal synchronous mode, a busy timeout, and immediate write transactions. The
connection pool is limited to one open connection to avoid SQLite writer-lock
surprises in this metadata workload.

Other SQL connection URLs are parsed but not opened yet; future builds can link
additional drivers behind the account/team store boundary.

## Login And Users

Configured mode serves the UI shell publicly, then asks `/api/session` whether a
login session exists and whether SSO is enabled. Directory, connector, browse,
logs, and admin JSON APIs require that session. MCP and plain HTTP connector
endpoints still use connector bearer tokens; they do not use browser sessions.

memd supports two sign-in methods:

- **Local accounts** — username/password, created by a super admin. There is no
  self-signup. This is the default when no IdP is configured, and a backup when
  SSO is on.
- **Single sign-on (OIDC)** — any OpenID Connect provider. Configured by a super
  admin in `/admin` → *Single sign-on*; see the
  [Authentication & SSO section in the README](../README.md#authentication--sso-oidc).
  Users are keyed on the `sub` claim and auto-provisioned on first login.

The first super admin is created by the server startup process (this is also the
SSO bootstrap — sign in locally once, then configure your IdP):

```bash
memd serve --create-super-admin alice
```

The web UI does not expose a "make super admin" action for local accounts.
Super admins open `/admin`, a separate Alpine app from the memory UI, to:

- create regular local users, disable/enable accounts, reset passwords
- configure OIDC single sign-on (issuer, client credentials, admin mapping)

For SSO, OIDC only authenticates cloud accounts. Super-admin access remains a
local-account flow, and team ownership/admin roles are maintained inside memd
rather than derived from IdP claims.

Sessions live in an HttpOnly, Secure, SameSite=Lax **encrypted cookie** (no
server-side session store); each request re-validates the cookie and reloads the
user so disable/role changes take effect immediately. Set `MEMD_SESSION_SECRET`
so sessions survive restarts and stay valid across replicas; if it is unset,
memd uses an ephemeral key and restarting signs everyone out. The absolute
session lifetime defaults to 24h (`MEMD_SESSION_MAX_AGE`). OIDC sessions refresh
the ID token silently while under that cap.

Super-admin accounts are account-management identities only. They cannot own,
import, export, create, or update directories/connectors, create teams, or
accept team invites. Create a regular user for actual memory work.

## Teams, Invites, And Shared Scope

Regular users can create teams from the main UI. The creator becomes the initial
`owner`. Super admins remain outside team membership.

Roles:

- `owner`: all team privileges, plus demoting/removing admins and deleting the
  team.
- `admin`: manage members, invite links, and team-scoped directories/connectors.
- `member`: view team-scoped directories/connectors.
- `viewer`: lower-access membership role for shared read-oriented use.

Owners/admins can create invite links. Each invite can have:

- no expiry or an explicit expiry timestamp
- unlimited uses or a max-use count

Invite links can be revoked. The database stores only a hash of the opaque
invite token. The plain invite URL is returned only when the invite is created,
so copy it then.

Accepting an invite is idempotent for existing members: accepting again succeeds
without consuming another use. Expired, revoked, and maxed-out invites cannot add
new members.

Team owners/admins can mark directories/connectors as team-scoped. Team members
can see team-scoped objects in the UI, but connector serving stays strict: an
MCP/HTTP token can only reach the directory IDs saved on that connector. The
current data model keeps directories/connectors in the creator's user namespace,
so ordinary members do not create their own connectors against another user's
team directory yet.

## User Data Import And Export

Directories and connectors are scoped to the signed-in user. A normal user can
export their own bundle from `/api/data` or the UI, then import it into another
account. The bundle contains connector bearer tokens, so handle it as a secret.
Team scope is stripped from exported/imported user bundles so the bundle remains
portable between regular users and does not leak team ownership metadata.

The CLI supports the same migration flow:

```bash
# Export one SQL-backed user's directories/connectors.
memd data export --user alice --out alice-memd-user-data.json

# Import into any existing local user.
memd data import --user bob --in alice-memd-user-data.json --replace

# Convert the old config.json registry into an importable bundle.
memd data export-legacy-config --out current-legacy-user-data.json
```

Legacy `config.json` is now only an import source for configured mode. The SQL
store is the source of truth after import.

## Connector URL Shapes

Production/header-auth forms:

```
http://127.0.0.1:<port>/mcp
http://127.0.0.1:<port>/http/<endpoint>
Authorization: Bearer <token>
```

Local/legacy token-in-URL forms:

```
http://127.0.0.1:<port>/mcp/<token>
http://127.0.0.1:<port>/http/<token>
```

- Token is 32 random characters.
- The bearer token **is the credential**. Treat it like a password.
- MCP connectors use `/mcp`; HTTP connectors use `/http`.
- Header auth and token-in-URL auth both work. Prefer header auth for production so tokens do not appear in access logs, browser history, or screenshots.
- Quick mode: a fresh MCP token per run.
- Configured mode: persists per connector until you delete it. See [Connector Tokens](#connector-tokens) below.

## Wiring Up Agents

### Claude Code

```bash
claude mcp add --transport http memd "http://127.0.0.1:48173/mcp/9f2c4a..."
```

### Codex CLI

In `~/.codex/config.toml`:

```toml
[mcp_servers.memd]
url = "http://127.0.0.1:48173/mcp/9f2c4a..."
transport = "http"
```

Any agent supporting MCP Streamable HTTP with a token-in-URL endpoint works the same way.

When the client supports request headers, use the cleaner production form:

```text
URL: http://127.0.0.1:48173/mcp
Authorization: Bearer 9f2c4a...
```

### HTTP connector for non-MCP agents

Some cloud agents can fetch URLs but cannot connect to MCP. Create an HTTP connector in the web UI and use **Copy skill** on the connector card. The copied instructions include the tokenless URL base, `Authorization: Bearer <token>`, and endpoints for `memory_load`, `memory_list`, `memory_read`, `memory_search`, status, and workflows. Write-enabled HTTP connectors expose write operations through POST only. If you are using a tunnel, open the web UI through the tunnel URL before copying so the pasted skill uses the reachable host.

## Where Config Lives

| Platform | Path |
|----------|------|
| macOS | `~/Library/Application Support/memd/` |
| Linux | `~/.config/memd/` |
| Windows | `%APPDATA%\memd\` |

Layout inside that path:

```
config.json      # legacy directory/connector registry, import source only
memd.db          # local account/user/directory/connector metadata database
workdirs/
  <id>/          # working copy for each Git-backed directory
```

## Connector Tokens

- **Stored** inside `memd.db` on each connector's `token` field. Legacy `config.json` exports can also contain tokens.
- **Shown in the web UI** embedded in the local/legacy connector URL and in the bearer auth header copied by **Copy auth**. Treat it as secret and paste it only into the intended agent.
- **Revoked** by deleting the connector from the web UI (`DELETE /api/connectors/{id}`). The token is removed from the user's SQL connector row and any future request bearing it returns 404.
- **Rotated** with the "Rotate token" button in the web UI (`POST /api/connectors/{id}/rotate`). The connector keeps its ID, name, directory access, and write flag; only the token changes. The previous URL stops authenticating immediately — paste the new one into the agent.

## Git Directory Behavior

- **Auth:** use an HTTPS remote plus a personal access token that has repo
  access to the target repository. Enter the Git username and PAT in the
  directory form. If a credential-bearing HTTPS URL is pasted, memd strips the
  credentials out of the stored remote and keeps them in the directory auth
  fields. OAuth-based Git-provider integrations may replace manual PAT setup
  later.
- **Connection check:** before saving, click **Test connection**. memd clones to
  a temporary workspace, verifies remote read access, creates a local test
  commit, pushes a temporary `memd-connection-check/*` branch, then deletes it.
  This proves the token can read and push a branch for PR/MR-style workflows
  without modifying the configured memory branch.
- **On startup:** clone if missing, then sync from `main` (or the configured
  branch) with `git pull --rebase --autostash` when that remote branch exists.
- **On read:** served from the working copy.
- **On write:** the file lands in the working copy immediately. The commit + push is **debounced** — a `wait_for_writes` timer (default `5m`) is armed, and any further write resets it. When the timer expires, memd stages changes, pulls with rebase/autostash if the remote branch exists, commits, and pushes. A fresh empty remote is initialized by the first push.
- **Safety flush** every `save_every` (default `10m`) commits whatever's dirty, so read-only sessions that only churn front-matter stats still sync.
- **Graceful shutdown** flushes any pending commits before exit.
- **On conflict/auth error:** local writes remain in the working copy and the
  backend surfaces the error via `memory_status`. Resolve the working copy or
  update credentials, then flush again. memd automatically handles clean
  remote-advanced cases with rebase, but it does not auto-resolve content
  conflicts.
- Committer identity (name + email) comes from the directory's config.

Local-folder directories: just file I/O, no Git, no debounce.

### GitHub PAT Setup

Prefer a
[fine-grained personal access token](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens#creating-a-fine-grained-personal-access-token)
for each Git-backed memory repository:

1. In GitHub, open Settings → Developer settings → Personal access tokens →
   Fine-grained tokens → Generate new token.
2. Set a descriptive token name and an expiration.
3. Select the resource owner that owns the memory repository.
4. Under repository access, choose **Only select repositories** and select the
   memory repository.
5. Under repository permissions, set **Contents** to **Read and write**. Leave
   unrelated permissions unset.
6. Generate the token, then paste it into memd's **Personal access token** field
   with the GitHub username that owns the token.

Notes:

- GitHub fine-grained tokens automatically include repository metadata read
  access. The key permission memd needs for clone/pull/commit/push is
  **Contents: Read and write**.
- Organization repositories may require owner approval for fine-grained PATs.
  Organizations using SAML SSO may also require the token to be authorized.
- memd pushes directly to the configured branch during normal sync. If branch
  protection blocks direct pushes, use a branch that the token owner can push to
  or adjust the repository rules. The connection check verifies temporary branch
  push/delete, not protected-branch bypass.
- If a fine-grained token cannot be used, a classic PAT with the `repo` scope is
  the fallback for private repositories. Prefer fine-grained tokens because they
  can be restricted to one repository.

### GitLab Token Setup

For GitLab, prefer a project access token when your GitLab tier and project
settings allow it; otherwise use a personal access token:

1. Create a token with an expiration.
2. Grant `write_repository`. GitLab defines this scope as repository read/write
   access for pull and push over HTTPS.
3. Paste the GitLab HTTPS remote into memd.
4. Use the token as the **Personal access token**. GitLab accepts any non-empty
   username for Git-over-HTTPS token auth; `oauth2` is a common value.
5. Run **Test connection**. A passing PR/MR branch check means memd can push a
   temporary branch that can be used for merge-request workflows.

Protected branches and project push rules still apply. If the configured branch
rejects direct pushes, configure memd to sync a branch that the token can push
to, or adjust repository rules.

## Security Notes

- Localhost-bound. Expose memd through a TLS reverse proxy or trusted tunnel for
  remote use, and keep host-level access restricted because anyone with a shell
  on the host can read local config and database files.
- Connector URLs are passwords. Don't paste them in shared logs, screenshots, or chats.
- Team invite URLs are join credentials until they expire, are revoked, or hit their max-use count.
- `memd.db` and exported user-data JSON can hold connector tokens and Git PATs.
  Be deliberate before syncing or sharing them.
