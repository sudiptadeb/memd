# Local Auth And Team Management Plan

Date: 2026-06-04

Scope: friends/family production-lite hosting. memd should not own memory data;
memory stays in user-controlled Git repositories. memd owns only the control
plane: users, teams, repo grants, connector credentials, branch mappings, sync
state, and audit events.

## Research Decisions

- Default DB: SQLite through `modernc.org/sqlite`. It is a `database/sql`
  driver and cgo-free, so it keeps the existing `CGO_ENABLED=0` build and simple
  VPS deployment.
- Avoid `mattn/go-sqlite3` for the default because it requires cgo and a C
  compiler toolchain.
- Keep the store behind a SQL boundary. `MEMD_DATABASE_URL` is the public knob;
  SQLite is linked now, and Postgres/MySQL can be added later by linking drivers
  and implementing dialect-specific migrations where needed.
- Password hashes use Argon2id via `golang.org/x/crypto/argon2`, stored in PHC
  string format with per-password random salts. Parameters follow OWASP's
  current Argon2id minimum: 19 MiB memory, 2 iterations, parallelism 1.
- For SQLite, use WAL, foreign keys, normal synchronous mode, busy timeout, and
  one open DB connection. This metadata workload is small; predictable locking is
  more important than maximizing concurrent writes.

## Current Implemented State

- `server/internal/account` owns the local account/team SQL store.
- Schema v3 includes:
  - `users`
  - `super_admins`
  - `teams`
  - `team_members`
  - `team_invites`
  - `team_invite_uses`
  - `user_directories`
  - `user_connectors`
  - `user_connector_directories`
  - `schema_migrations`
- `memd serve` opens the account DB before starting the HTTP server.
- If the DB is missing, interactive startup asks whether to initialize it.
- `--init-db` / `MEMD_INIT_DB=1` handles non-interactive initialization.
- First initialization creates a super-admin account.
- Additional super admins can be created only through process startup:
  `--create-super-admin`, `--super-admin-password`, or the matching env vars.
  There is intentionally no API route for this.
- The web UI has local login backed by an in-memory 24-hour session cookie.
- Directory, connector, browse, logs, and admin JSON APIs require login.
- Super admins use the separate `/admin` Alpine app to create regular users,
  disable/enable users, and reset passwords.
- Super-admin accounts cannot own, import, export, create, or update
  directories/connectors, create teams, or accept team invites. They are only
  for account administration.
- Regular users own their directories/connectors.
- Configured mode now loads directory/connector records from SQL. Legacy
  `config.json` is only an import source.
- Regular users can import/export their own directory/connector bundle from the
  UI or the CLI:
  - `memd data export --user USER --out FILE`
  - `memd data import --user USER --in FILE [--replace]`
  - `memd data export-legacy-config --out FILE`
- Regular users can create teams. The creator becomes the initial `owner`.
- Team roles are `owner`, `admin`, `member`, and `viewer`.
- Owners/admins can create and revoke copyable invite links with optional expiry
  and optional max-use count.
- Existing members can re-accept an invite without consuming another use.
- Owners/admins can mark directories/connectors as team-scoped.
- Team members can see team-scoped directories/connectors in the main UI.
- MCP/HTTP connector serving remains limited to the directory IDs saved on that
  connector.
- User data import/export strips team scope so portable bundles stay personal.
- The main UI is now a responsive shell with views for How it works, Teams,
  Directories, Connectors, and Activity. Desktop uses a side navigation rail;
  smaller screens use a hamburger drawer and show Activity as a page.

## Implemented Team Slice

Detailed handoff: [Teams Work Handoff](2026-06-07-teams-handoff.md).

1. Regular users create teams. The creator becomes the initial team
   `owner`.
2. Team roles:
   - `owner`: all admin privileges, plus demoting admins and deleting the team.
   - `admin`: manage team settings, members, invites, and team directories.
   - `member`: access team directories/connectors marked for the team.
   - `viewer`: lower-access membership role for shared read-oriented use.
3. Invite links that owners/admins can create, copy, and revoke. Each link
   can have optional time expiry and optional max-use count.
4. Invite acceptance flow. A valid invite adds the accepting regular user to
   the team, consuming one use only after membership is created.
5. Team owners/admins mark directories/connectors as team-scoped. Being a
   team member grants UI access to the team's marked directories, while MCP
   connector tokens remain limited to their saved directory list.
6. Team-scoped directory/connector list filters and API checks.

## Deferred

- Git directory grants:
  - user supplies repo URL and read/write credential
  - memd clones into a controlled workdir
  - memd creates per-connector branches
- Connector branch sync modes:
  - raise/update PR
  - auto-merge
  - manual only
- Generic OIDC mode behind `auth.mode = local | oidc`.
- Email/SMS invite delivery.
- Billing.
- Automatic merge-conflict resolution.
- Full MCP OAuth 2.1 authorization server flow.
