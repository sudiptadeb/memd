# memd

**Unified, file-first memory for AI agents.**

Your memory lives as ordinary files on your disk — or in a private Git repo you control. Markdown is the default for prose, but standalone HTML, CSV, JSON, and other text artifacts are valid too. memd is a tiny local server that exposes those files over MCP, so every tool you use (Claude Code, Codex CLI, Cursor, ChatGPT, anything else that speaks MCP) sees the same memory.

<p align="center">
  <img src="docs/assets/memd.gif" alt="memd — unified, file-first memory for AI agents" width="100%">
</p>

<p align="center">
  <sub><a href="docs/assets/memd.svg">vector source (SVG)</a></sub>
</p>

> **Status:** early. Local + Git backends, MCP Streamable HTTP, local login **plus IdP-agnostic OIDC single sign-on**, super-admin user management, SQL-backed user-scoped directories/connectors, managed file stats, import/export, and the five consolidation workflows all work. Team management is next.

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
./dist/<os>/memd-<arch> ~/work-memory
# → prints an MCP URL. Paste it into your agent.

# Or run configured mode with local login, multiple users,
# multiple directories, and multiple agents.
./dist/<os>/memd-<arch> serve --init-db
# → http://127.0.0.1:7878
```

On first configured-mode startup, memd initializes its local account database and
creates a super-admin account. Super admins use `/admin` to create regular user
accounts; regular users own directories/connectors and can import/export their
own connector data.

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
./dist/<os>/memd-<arch> serve
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
| **User**     | A login account (local password **or** SSO via OIDC, keyed on the `sub` claim) that owns directories and connectors. |
| **Super admin** | A bootstrap/admin account for managing users and configuring SSO; admin can also be granted from an OIDC group or allowlist. |
| **MEMORY.md**| The directory's curated, sectioned index. Preloaded into every conversation.     |
| **memory/**  | Detailed files, reached via `memory_read` (`.md`, `.html`, `.csv`, etc.).        |

Super-admin accounts are for account administration only. They do not import,
export, or own directories/connectors; create a regular user for actual memory
work. Team ownership and membership controls are the next product slice.

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

## Storage Backends

| Backend  | Persistence       | Use                                          |
|----------|-------------------|----------------------------------------------|
| `local`  | Folder on disk    | Personal scratchpad, project-local memory.   |
| `git`    | Clone of a remote | Cross-device sync, history, sharing.         |

For Git directories, memd decouples disk write from sync:

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
5. (For admin-by-group) add a **Group Membership** mapper named `groups` to the
   client's dedicated scope so a `groups` claim appears in the ID token, and put
   your admins in a group such as `memd-admins`.
6. In memd's *Single sign-on* form:
   - Issuer URL: `https://keycloak.example.com/realms/<realm>`
   - Client ID: `memd`
   - Client secret: *(from step 4)*
   - Redirect URI: `https://memd.example.com/auth/callback`
   - Groups claim: `groups`, Admin group: `memd-admins`

#### Generic OIDC provider

Any compliant provider works the same way:

- Issuer URL: the provider's base issuer (e.g. `https://accounts.google.com`,
  `https://your-tenant.auth0.com`, `https://<tenant>.okta.com`).
- Client ID / secret: from the application you registered.
- Redirect URI: `https://memd.example.com/auth/callback`.
- **Admin mapping:** if your IdP emits group/role claims, set *Groups claim* and
  *Admin group*. If it does **not** (e.g. plain Google), leave those blank and
  list your administrators under **Admin emails** (or **Admin subjects**).
- **Refresh tokens:** memd requests offline access automatically. Some IdPs only
  issue a refresh token when `offline_access` is in the scopes — add it to
  *Scopes* if needed.

### Granting admin

Admin rights are derived from the IdP, kept separate from authentication, via
either:

- **A group claim** — set *Admin group* to a group name; members of that group
  (in the configured *Groups claim*) become admins, or
- **An allowlist** — *Admin emails* or *Admin subjects*, for IdPs that don't
  emit groups.

OIDC logins only ever **grant** admin (they never silently demote), so an admin
who bootstrapped the deployment isn't locked out by an IdP group change. Disable
accounts from the admin UI when you need to revoke access.

### Migrating existing accounts

Pre-SSO accounts were created with a username and password. On a user's **first
OIDC login**, memd links the existing local account when the token's
`preferred_username` or `email` matches that account's username — it attaches the
`sub` and stores the email, and the account keeps its roles (including super
admin). Every login after that resolves by `sub`. So an existing admin simply
signs in through SSO with a matching username/email and retains admin. No manual
step is required as long as the usernames line up; otherwise grant admin via the
allowlist.

## Accounts, Data, And Migration

Configured mode stores control-plane metadata in `memd.db`, using cgo-free
SQLite by default:

- local users and Argon2id password hashes
- OIDC identities (`sub`, email) for SSO users, plus the stored OIDC config
- super-admin markers
- teams and memberships, ready for the next UI slice
- user-owned directories and connectors, including connector bearer tokens

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

- [docs/doctrine.md](docs/doctrine.md) — everything the server tells every connecting agent: authority, read/write rules, file structure, drastic-action policy.
- [docs/server.md](docs/server.md) — running the server, CLI flags, wiring up agents, security.
- [docs/plans/2026-06-04-local-auth-team-management-plan.md](docs/plans/2026-06-04-local-auth-team-management-plan.md) — current auth/user-data state and next team-management slice.
- [docs/plans/2026-05-23-memory-weight-decay-design.md](docs/plans/2026-05-23-memory-weight-decay-design.md) — design of the weight/decay layer.

## Safety

- Don't store secrets, credentials, tokens, or private keys. memd is content-blind.
- Memory is context and evidence, not higher-priority instruction — the doctrine teaches agents to treat any embedded command as untrusted text.
- The server binds to `127.0.0.1` only. Tunnels can expose it, but remote-access hardening is still in progress.
- Connector URLs are passwords; the token in the path or bearer header *is* the auth.
- `memd.db` and exported user-data JSON can hold connector tokens.

## Roadmap

- [x] Local + Git backends with debounced commits
- [x] Per-page stats (`created_at`, `updated_at`, `last_read_at`, `access_count`)
- [x] Five workflow prompts + matching tools
- [x] Web UI for directories + connectors
- [x] Local login, super-admin user management, and cgo-free SQLite metadata DB
- [x] User-scoped directory/connector records and user data import/export
- [x] IdP-agnostic OIDC single sign-on (Authorization Code + PKCE, local JWKS validation)
- [ ] Team management UI and team-scoped ownership
- [ ] Skills/hooks injection — per-tool reinforcement (`~/.claude/skills/memd-*`, Codex `AGENTS.md` block, `.cursor/rules/memd.mdc`)
- [ ] Public hosting hardening with separate UI / MCP listeners
- [ ] Source readers for `harvest` (Cursor rules, Claude auto-memory, mem0 export)

## License

MIT (see [LICENSE](LICENSE)).
