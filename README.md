# memd

**Unified, file-first memory for AI agents.**

Your memory lives as ordinary files on your disk — or in a private Git repo you control. Markdown is the default for prose, but standalone HTML, CSV, JSON, and other text artifacts are valid too. memd is a small server that exposes those files over MCP, so every tool you use (Claude Code, Codex CLI, Cursor, ChatGPT, anything else that speaks MCP) sees the same memory.

<p align="center">
  <img src="docs/assets/memd.gif" alt="memd — unified, file-first memory for AI agents" width="100%">
</p>

<p align="center">
  <sub><a href="docs/assets/memd.svg">vector source (SVG)</a></sub>
</p>

> **Status:** early. Local + Git backends, MCP Streamable HTTP, local login **plus IdP-agnostic OIDC single sign-on**, super-admin user management, regular-user teams, invite links, team-scoped directories/connectors, SQL-backed user data, managed file stats, import/export, responsive web UI, and the five consolidation workflows all work.

## Why

LLM tools each invented their own memory: ChatGPT memories, Claude's memory tool, Cursor rules, Codex `AGENTS.md`. They don't talk to each other — knowledge fragments, and what you teach one agent is invisible to the next.

memd takes the opposite stance: **memory is yours, lives in your files, and follows you.**

- One directory = one self-organising file memory rooted at `MEMORY.md`.
- One connector per agent, scoped to the directories that agent can read or write.
- The agent decides what to write, where, and when to split.
- Backend is your choice — a plain folder or a private Git repo (memd debounces commits so a session of edits becomes one clean commit).

## Quickstart

```bash
# Build
bash build/build.sh host

# One directory, ephemeral — no setup
./dist/<os>/memd-<arch>-v<version> ~/work-memory
# → prints an MCP URL. Paste it into your agent.

# Or run configured mode with local login, teams,
# multiple directories, and multiple agents.
./dist/<os>/memd-<arch>-v<version> serve --init-db
# → http://127.0.0.1:7878
```

`build/build.sh host` stamps the version into the filename, so the binary lands
at e.g. `./dist/darwin/memd-arm64-v0.1.0-dev` (override with `VERSION=...`).

On first configured-mode startup, memd initializes its local account database and
creates a super-admin account. Super admins use `/admin` to create regular user
accounts; regular users own directories/connectors and can import/export their
own connector data. Regular users can also create teams, invite other regular
users, and mark selected directories/connectors as team-scoped.

That local super admin is also your **bootstrap for SSO**: sign in locally once,
open `/admin`, and configure any OpenID Connect identity provider (see
[Authentication & SSO](#authentication--sso-oidc)). Once SSO is on, the login
page leads with it and local accounts remain a backup for super-admin-created
logins.

For non-interactive first boot:

```bash
MEMD_INIT_DB=1 \
MEMD_CREATE_SUPER_ADMIN_USERNAME=admin \
MEMD_CREATE_SUPER_ADMIN_PASSWORD='change-me' \
./dist/<os>/memd-<arch>-v<version> serve
```

## Wire It Up

For local use, the token can live in the URL. For production or tunneled use, prefer the tokenless URL plus `Authorization: Bearer <token>`. The connector card can copy both forms.

### Coding agents

**Claude Code**

```bash
claude mcp add memd --transport http "http://127.0.0.1:7878/mcp/<token>"
```

**Codex CLI** — in `~/.codex/config.toml`:

```toml
[mcp_servers.memd]
url = "http://127.0.0.1:7878/mcp/<token>"
transport = "http"
```

**Cursor** — in `.cursor/mcp.json` (project) or `~/.cursor/mcp.json` (global):

```json
{
  "mcpServers": {
    "memd": {
      "url": "http://127.0.0.1:7878/mcp/<token>"
    }
  }
}
```

### Web chats

Web chats run server-side and can't reach `127.0.0.1`. Expose memd via a tunnel (`cloudflared tunnel`, `ngrok http 7878`, `tailscale funnel`) and paste the public HTTPS URL instead.

**Claude.ai** — Settings → Connectors → *Add custom connector* → paste `https://<your-tunnel>/mcp/<token>`.

**ChatGPT** — Settings → Connectors → *Add MCP server* → paste the same URL. Available on Plus / Pro / Enterprise.

**Mistral Le Chat** — Settings → MCP Connectors → *Add server* → paste the URL.

Any MCP client that speaks Streamable HTTP works the same way.

If the client supports custom headers, use the production form:

```text
URL: https://<your-host>/mcp
Authorization: Bearer <token>
```

### Agents without MCP

Create an **HTTP connector** in the web UI for agents that can fetch URLs but cannot speak MCP. The connector card has **Copy skill**, which copies a complete instruction block with tokenless HTTP endpoints such as `/memory_load`, `/memory_search`, and `/memory_read`, plus the bearer auth header. If you are using a tunnel, open the web UI through the tunnel URL before copying.

## The Mental Model

| Term         | What it is                                                                       |
|--------------|----------------------------------------------------------------------------------|
| **Directory**| A self-organising file memory — a folder on disk or a Git repo.                  |
| **Connector**| A token-scoped grant — MCP or HTTP, one per agent (Claude Code, Codex, Cursor, …). |
| **Team**     | A shared space owned by regular users. Team admins can expose selected directories/connectors to members. |
| **Invite link** | A copyable team join URL with optional expiry and max-use limits.             |
| **User**     | A login account (local password **or** SSO via OIDC) that owns directories and connectors. |
| **Super admin** | A bootstrap/admin account for managing users and configuring SSO. |
| **MEMORY.md**| The directory's curated, sectioned index. Preloaded into every conversation.     |
| **memory/**  | Detailed files, reached via `memory_read` (`.md`, `.html`, `.csv`, etc.).        |

Super-admin accounts are for account administration only. They do not import,
export, or own directories/connectors; create a regular user for actual memory
work.

## Teams And The Web UI

Configured mode uses a responsive app shell with primary views for **How it
works**, **Teams**, **Directories**, **Connectors**, and **Activity**. Desktop
keeps the navigation rail visible. Smaller screens collapse navigation into a
hamburger drawer, keep dark mode in the top bar, and show Activity as a normal
page instead of a cramped side panel.

Regular users can create teams from the main UI. The creator becomes `owner`.
Team roles are:

| Role | Privileges |
|------|------------|
| `owner` | Manage everything, demote/remove admins, and delete the team. |
| `admin` | Manage members, invite links, and team-scoped directories/connectors. |
| `member` | Use team directories: build their own connectors against them, read and write. |
| `viewer` | Read-only access to team directories. |

Owners/admins can create copyable invite links with optional expiry and optional
max-use count. Invite links can be revoked. Accepting a valid link adds the
signed-in regular user to the team; super admins cannot join teams.

### Team directories

A team directory stays owned by whoever created it — marking it "team" shares it
with the team, it doesn't move it into a separate bucket. It still appears under
the owner's own (Personal) view *and* under the team, so sharing never makes a
directory vanish from its owner.

Sharing is deliberately simple: an owner or admin marks a directory as a team
directory, and from then on **each team member builds their own connector**
against it. Members don't pass around one shared connector URL — every member
has a distinct connector and token, so the activity log attributes each
read/write to whoever actually did it. Write access follows the team role:
owners, admins, and members can write; viewers are read-only. Connector serving
stays strict: an MCP/HTTP token only reaches the directory IDs saved on that
connector, and only those the connector's owner is still entitled to.

## Self-Organising Memory

memd doesn't just store — it manages. Five workflows, each named after a real-world activity. They appear as slash commands (`/<connector>:reorganise`, etc.) in clients that surface MCP prompts, and as `memd_*` tools in every client:

| Workflow      | Activity              | What it does                                              |
|---------------|-----------------------|-----------------------------------------------------------|
| `reorganise`  | Rearranging shelves   | Restructure: group files into folders, rewrite the index. |
| `harvest`     | Bringing in the crop  | Import knowledge from external sources (Claude / Cursor / notes). |
| `dream`       | Sleep consolidation   | Cement what was used this session; fade what wasn't.      |
| `recall`      | Reminiscing           | Focused retrieval: search → walk links → synthesise.      |
| `housekeep`   | Daily tidying         | Fix drift: dangling links, orphan files, missing Markdown FM. |

Long-running passes auto-dispatch to a background agent when the client supports it (Claude Code's Task tool, Codex's worker, Cursor's background agent), so the main conversation stays responsive.

Workflows act autonomously and report afterwards — they only stop to ask the user before genuinely drastic actions (deleting authored content, removing more than a paragraph, overwriting a managed file tagged `priority: load-bearing`). Everything is in Git; the user can review or revert.

## File Structure

Markdown pages carry YAML front matter with two ownership zones:

```yaml
---
memd:                           # server-managed, read-only for agents
  created_at: 2026-04-10
  updated_at: 2026-05-22
  last_read_at: 2026-05-23
  access_count: 17
topic: dlp                      # agent-managed
priority: load-bearing
tags: [scanner, performance]
related: [feedback-nftables-rule-order]
---

# Page body...
```

The `memd:` subtree powers `dream` for managed files — files with high `access_count` and recent `last_read_at` get cemented into MEMORY.md's top sections; managed files that haven't been read in 90 days can drift to archive. Agents add `topic`, `tags`, `priority`, `superseded_by`, `related`, or anything else useful for the directory's domain.

HTML files carry the same YAML front matter inside a leading `<!-- ... -->` comment, so diagrams and mock UIs can have stats without changing browser rendering. Other text files are stored verbatim. Use them when the artifact is naturally not prose: CSV for tables, JSON/YAML/TOML for structured examples, and plain text for logs or snippets.

memd keeps files within production-validated size budgets. The `MEMORY.md`
preload is truncated at 200 lines / 25KB — `memory_load` says so when it truncates,
which keeps the index curated rather than enumerated. A `memory_write` over 100KB
returns a split-it warning. The doctrine teaches the underlying habit: keep each
file focused on one thing and prefer many small files over a few large ones, so
detail lives in topic files reached on demand instead of bloating the preload.

## Storage Backends

| Backend  | Persistence       | Use                                          |
|----------|-------------------|----------------------------------------------|
| `local`  | Folder on disk    | Personal scratchpad, project-local memory.   |
| `git`    | Clone of a remote | Cross-device sync, history, sharing.         |

For Git directories, memd decouples disk write from sync:

- Use an **HTTPS remote** and a **personal access token with repo access** for
  private repositories. Enter the Git username and PAT in the directory form.
  SSH-key based Git auth is retained for local runs, but it is not recommended
  for end-user deployments because it is hard to provision, rotate, and scope
  safely.
- For GitHub, create a
  [fine-grained personal access token](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens#creating-a-fine-grained-personal-access-token)
  from Settings → Developer settings → Personal access tokens →
  Fine-grained tokens. Limit repository access to the memory repo, set
  repository **Contents** to **Read and write**, and set an expiration. If the
  repo belongs to an organization, the token may need organization approval or
  SAML authorization. Classic tokens should be a fallback only; use the `repo`
  scope when a classic token is required for a private repo.
- For GitLab, use a project access token or personal access token with
  `write_repository`; GitLab defines that as read/write repository access for
  pull and push over HTTPS. Use the token as the password and any non-empty
  username, such as `oauth2`.
- Before adding the directory, use **Test connection** in the Git form. memd
  checks remote read access, local commit/write behavior, and push/delete of a
  temporary branch suitable for PR/MR workflows. It does not bypass branch
  protection; the token owner must still be allowed to push to the configured
  branch memd will sync.
- Do not put PATs in clone URLs, docs, shell history, or memory files. memd
  stores Git credentials with the directory's account data and strips
  credentials out of pasted HTTPS remotes before configuring the working copy.
  Future OAuth-based Git-provider integrations may replace this manual PAT
  setup.
- **FS write: instant** on every `memory_read` and `memory_write`.
- **Commit + push: debounced** by `wait_for_writes` (default `5m`). A session of edits coalesces into one commit.
- **Safety flush** every `save_every` (default `10m`) catches read-only sessions where only front-matter stats churn.
- **Graceful shutdown** flushes whatever's pending.

## Authentication & SSO (OIDC)

memd authenticates the web UI with either **local accounts** (super-admin
created, no self-signup) or **single sign-on via any OpenID Connect provider**.
SSO is IdP-agnostic — Keycloak, Authentik, Okta, Auth0, Google, Entra ID, etc.
There are no per-provider code paths: you supply four values and everything else
(authorization/token/JWKS/end-session endpoints, signing algorithms) is read from
the IdP's discovery document at `<issuer>/.well-known/openid-configuration`.

Under the hood: Authorization Code + **PKCE**, `state` (CSRF) and `nonce`
(replay) on every login, ID tokens validated **locally** against the IdP's JWKS
(signature, `iss`, `aud`, `exp`, `nonce`), users keyed on the **`sub`** claim
and auto-provisioned on first login, sessions in an HttpOnly / Secure /
SameSite=Lax encrypted cookie with silent refresh and an absolute lifetime cap.

### The four values

| Value | What it is |
|-------|------------|
| `OIDC_ISSUER_URL` | Discovery base. memd fetches `<issuer>/.well-known/openid-configuration`. |
| `OIDC_CLIENT_ID` | The client/application ID registered at your IdP. |
| `OIDC_CLIENT_SECRET` | The client secret for that application. |
| `OIDC_REDIRECT_URI` | Must be `https://<your-memd-host>/auth/callback`, registered verbatim at the IdP. |

### Where configuration lives

OIDC configuration is stored in the database and **edited by a super admin in
the web UI** (`/admin` → *Single sign-on*). Save validates the configuration by
performing discovery before it takes effect, and applies live — no restart.

For automated deployments you can **seed** the configuration from environment
variables on first boot (see [`.env.example`](.env.example)); after that, the
database is the source of truth. Secrets belong in your platform's secret store,
never committed. Also set `MEMD_SESSION_SECRET` in production so sessions survive
restarts.

### Configure your IdP

**1. Register memd as an application** at your IdP and set the redirect URI to:

```
https://<your-memd-host>/auth/callback
```

**2. Configure memd** in `/admin` → *Single sign-on*: enable SSO, paste the
issuer URL, client ID, client secret, and the same redirect URI, then save.

#### Keycloak (concrete example)

1. In your realm, **Clients → Create client**: Client type *OpenID Connect*,
   Client ID `memd`.
2. Enable **Client authentication** (makes it confidential) and the
   **Standard flow**.
3. Set **Valid redirect URIs** to `https://memd.example.com/auth/callback`.
4. Copy the secret from **Credentials**.
5. In memd's *Single sign-on* form:
   - Issuer URL: `https://keycloak.example.com/realms/<realm>`
   - Client ID: `memd`
   - Client secret: *(from step 4)*
   - Redirect URI: `https://memd.example.com/auth/callback`

#### Generic OIDC provider

Any compliant provider works the same way:

- Issuer URL: the provider's base issuer (e.g. `https://accounts.google.com`,
  `https://your-tenant.auth0.com`, `https://<tenant>.okta.com`).
- Client ID / secret: from the application you registered.
- Redirect URI: `https://memd.example.com/auth/callback`.
- **Refresh tokens:** memd requests offline access automatically. Some IdPs only
  issue a refresh token when `offline_access` is in the scopes — add it to
  *Scopes* if needed.

### Admin And Roles

OIDC only authenticates cloud accounts. It does not grant super-admin rights,
team ownership, or team admin roles from IdP claims. Super admins remain local
accounts created through the separate local-login bootstrap/admin flow, and team
roles are managed inside memd.

### Migrating existing accounts

Pre-SSO accounts were created with a username and password. On a user's **first
OIDC login**, memd creates a separate cloud account keyed by the token's
`iss` + `sub`. It never links that cloud identity to an existing local account
by username or email. Keep the local super-admin account for administration and
assign any team roles inside memd after the cloud account exists.

## Accounts, Data, And Migration

Configured mode stores control-plane metadata in `memd.db`, using cgo-free
SQLite by default:

- local users and Argon2id password hashes
- OIDC identities (`iss` + `sub`, email) for SSO users, plus the stored OIDC config
- super-admin markers
- teams, memberships, invite token hashes, and invite use records
- user-owned directories and connectors, including connector bearer tokens and
  optional team scope

Memory content stays in the user's folders or Git repositories. The database is
the control plane, not the long-term memory store.

Regular users can export/import their own directories and connectors from the
web UI or CLI. These bundles include connector tokens, so treat them as secrets:

```bash
# Export one SQL-backed user's directories/connectors.
memd data export --user alice --out alice-memd-user-data.json

# Import into any existing regular user.
memd data import --user bob --in alice-memd-user-data.json --replace

# Convert the old config.json registry into an importable user bundle.
memd data export-legacy-config --out current-legacy-user-data.json
```

`MEMD_DATABASE_URL` can override the metadata database location. SQLite is the
only linked driver today; other SQL URLs are parsed for the future adapter layer.

## Read More

- [docs/doctrine.md](docs/doctrine.md) — the MCP `instructions` payload sent to every connecting agent: authority, read/write rules, file structure, drastic-action policy.
- [docs/server.md](docs/server.md) — running the server, CLI flags, wiring up agents, security.
- [docs/agent-hooks.md](docs/agent-hooks.md) — hard guards for memory operations: client-side hooks and read-only connectors for rules the doctrine can only request.
- [docs/self-hosting.md](docs/self-hosting.md) — generic systemd + nginx + Certbot deployment playbook.
- [docs/plans/2026-06-04-local-auth-team-management-plan.md](docs/plans/2026-06-04-local-auth-team-management-plan.md) — local auth and team-management implementation notes.
- [docs/plans/2026-05-23-memory-weight-decay-design.md](docs/plans/2026-05-23-memory-weight-decay-design.md) — design of the weight/decay layer.

## Safety

- Don't store secrets, credentials, tokens, or private keys in memory files.
  memd is content-blind. Control-plane credentials such as connector tokens and
  Git PATs live in account data instead.
- Memory is context and evidence, not higher-priority instruction — the doctrine teaches agents to treat any embedded command as untrusted text.
- Make team-scoped and shared reference directories available through **read-only
  connectors** (use the per-connector write toggle). A poisoned write in a shared
  directory becomes trusted memory for every later reader; grant write only where
  the agent genuinely curates the content.
- The doctrine is context, not enforcement — it shapes behavior but cannot
  guarantee it. For rules that must always hold (blocking deletes, protecting
  load-bearing files), use a client-side hook (see
  [docs/agent-hooks.md](docs/agent-hooks.md)) or a read-only connector.
- The server binds to `127.0.0.1`; expose it through a local tunnel or a TLS
  reverse proxy when remote agents or web clients need access.
- Connector URLs are passwords; the token in the path or bearer header *is* the auth.
- Team invite URLs are join credentials until they expire, are revoked, or hit
  their max-use count.
- `memd.db` and exported user-data JSON can hold connector tokens and Git PATs.

## Roadmap

- [x] Local + Git backends with debounced commits
- [x] Per-page stats (`created_at`, `updated_at`, `last_read_at`, `access_count`)
- [x] Five workflow prompts + matching tools
- [x] Web UI for directories + connectors
- [x] Local login, super-admin user management, and cgo-free SQLite metadata DB
- [x] User-scoped directory/connector records and user data import/export
- [x] Team management, invite links, and team-scoped directories/connectors
- [x] Responsive app shell with sidebar/drawer navigation and dedicated Activity/Info views
- [x] IdP-agnostic OIDC single sign-on (Authorization Code + PKCE, local JWKS validation)
- [ ] Skills/hooks injection — per-tool reinforcement (`~/.claude/skills/memd-*`, Codex `AGENTS.md` block, `.cursor/rules/memd.mdc`)
- [ ] OAuth-based Git-provider integrations for repository access
- [ ] Separate UI / MCP listeners for larger hosted deployments
- [ ] Source readers for `harvest` (Cursor rules, Claude auto-memory, mem0 export)

## License

MIT (see [LICENSE](LICENSE)).
