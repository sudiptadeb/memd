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
- Schema v2 includes:
  - `users`
  - `super_admins`
  - `teams`
  - `team_members`
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
  directories/connectors. They are only for account administration.
- Regular users own their directories/connectors.
- Configured mode now loads directory/connector records from SQL. Legacy
  `config.json` is only an import source.
- Regular users can import/export their own directory/connector bundle from the
  UI or the CLI:
  - `memd data export --user USER --out FILE`
  - `memd data import --user USER --in FILE [--replace]`
  - `memd data export-legacy-config --out FILE`

## Next Slice: Teams

Detailed handoff: [Teams Work Handoff](2026-06-07-teams-handoff.md).

1. Add team UI for manual team creation.
2. Add team membership assignment and roles (`owner`, `admin`, `member`,
   `viewer`).
3. Decide how user-owned directories/connectors become team-owned or
   team-shared.
4. Add team-scoped directory/connector list filters and API checks.
5. Add Git directory grants:
   - user supplies repo URL and read/write credential
   - memd clones into a controlled workdir
   - memd creates per-connector branches
6. Add connector branch sync modes:
   - raise/update PR
   - auto-merge
   - manual only
7. Add generic OIDC mode behind `auth.mode = local | oidc`.

## Deferred

- Email/SMS invite flows.
- Billing.
- Automatic merge-conflict resolution.
- Full MCP OAuth 2.1 authorization server flow.
