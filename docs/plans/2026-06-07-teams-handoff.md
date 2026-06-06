# Teams Work Handoff

Date: 2026-06-07

This handoff is for the next slice after local auth and user-scoped SQL data.
The goal is to add simple friends/family team management without losing the
current invariants: super admins manage accounts only, regular users own memory
data, and memory content stays in user-controlled folders or Git repositories.

## Current State

- Local login and sessions are implemented.
- Super admins are created only from the server process and use `/admin` to
  create/disable users and reset passwords.
- Super-admin accounts cannot own, import, export, create, or update
  directories/connectors.
- Regular users own directories/connectors.
- Configured mode loads directories/connectors from SQL, not `config.json`.
- Legacy `config.json` can be exported into a `memd-user-data` bundle and
  imported into any regular user.
- The account schema already has `teams`, `team_members`, and nullable `team_id`
  columns on `user_directories` and `user_connectors`.

Existing team store functions:

- `CreateTeam(ctx, CreateTeamInput{Name, Slug, OwnerUserID})`
- `AddTeamMember(ctx, teamID, userID, role, actorUserID)`
- `ListTeams(ctx)`

Existing role constants:

- `owner`
- `admin`
- `member`
- `viewer`

## Important Schema Caveat

The v2 directory/connector data model is still fundamentally user-namespaced:

- `user_directories` primary key is `(owner_user_id, id)`.
- `user_connectors` primary key is `(owner_user_id, id)`.
- `user_connector_directories` foreign keys require the same
  `owner_user_id` for a connector and the directories it references.

That means a connector owned by user B cannot directly attach to a team
directory owned by user A without either:

1. Keeping team directories/connectors under one owning user for the MVP, or
2. Doing a schema v3 migration to make directory/connector IDs globally
   addressable or team-namespaced.

Recommended MVP: avoid schema churn first. Treat `owner_user_id` as the creator
namespace and `team_id` as shared visibility. Only team owners/admins manage
team directories/connectors. Members/viewers can see and copy shared connector
URLs, but do not create their own connectors against another user's team
directory until a schema v3 decision is made.

## Product Decisions To Lock First

1. Who creates teams?
   Recommended for friends/family: super admins can create teams and choose a
   regular user as initial owner. Regular owners can then manage membership.

2. Can super admins be team members?
   Recommended: no. Keep super admins out of memory ownership. Use regular
   accounts for real memory work.

3. What do roles mean in the MVP?
   Recommended:
   - `owner`: manage team settings, members, roles, and team data.
   - `admin`: manage members except owners, and manage team data.
   - `member`: view team data and copy/use shared connectors.
   - `viewer`: view team data and copy/use read-only shared connectors.

4. Should members create team connectors?
   Recommended MVP: no, unless the connector and directory are both created in
   that same member's namespace. Revisit after schema v3 if this becomes
   important.

## Suggested Implementation Plan

### 1. Finish The Account Store

Add store methods in `server/internal/account`:

- `CreateTeamForActor(ctx, input)` with separate `CreatedByUserID` and
  `OwnerUserID`.
- `TeamByID(ctx, teamID)`.
- `ListTeamsForUser(ctx, userID)`.
- `ListTeamMembers(ctx, teamID)`.
- `SetTeamMemberRole(ctx, teamID, userID, role, actorUserID)`.
- `RemoveTeamMember(ctx, teamID, userID)`.
- `UserTeamRole(ctx, teamID, userID)`.
- Permission helpers such as `CanManageTeam`, `CanManageTeamData`, and
  `CanViewTeam`.

Rules to enforce in the store:

- Team owners and members must be regular users, not super admins.
- Disabled users should not be added to teams.
- Every team must keep at least one `owner`.
- Only valid roles are accepted.
- Duplicate team slugs should return `ErrAlreadyExists`.

### 2. Add Team API Endpoints

Add APIs in `server/internal/ui/auth.go` or a new `teams.go`:

- `GET /api/teams`
- `POST /api/teams`
- `GET /api/teams/{id}`
- `PATCH /api/teams/{id}`
- `DELETE /api/teams/{id}` if deletion is in scope
- `GET /api/teams/{id}/members`
- `POST /api/teams/{id}/members`
- `PATCH /api/teams/{id}/members/{user_id}`
- `DELETE /api/teams/{id}/members/{user_id}`

Permission shape:

- Super admins can create teams and assign the first owner, but should not own
  data.
- Team owners/admins can manage members and team data.
- Regular users see only teams they belong to.
- Regular users cannot add themselves to teams.

### 3. Wire Team Scope Into Directory/Connector Data

Add `TeamID string json:"team_id,omitempty"` to `config.Directory` and
`config.Connector`, or introduce UI/account-specific view structs if we want to
avoid expanding config types further.

Then update:

- SQL list/upsert paths in `server/internal/account/user_data.go` to read/write
  `team_id`.
- `NewUserDataBundle` to clear `TeamID`, so user import/export remains personal
  and importable into any regular user.
- Directory and connector create/update request bodies to accept optional
  `team_id`.
- The registry read paths to support:
  - personal objects where `team_id` is empty and `owner_user_id == user.ID`
  - team objects where `team_id` is in the user's memberships

Keep MCP connector serving strict: a connector should only serve the directories
listed on that connector, and team visibility in the UI must not grant an MCP
token more directory access than its saved directory IDs.

### 4. Update The Main UI

In `assets/index.html` and `assets/script.js`:

- Add a scope control near the main toolbar:
  - Personal
  - Team selector for teams the user belongs to
- Show team badges on directory and connector cards.
- When creating a directory/connector, persist the selected scope.
- Hide team-management controls from super-admin-only accounts.
- For team members/viewers, disable controls they cannot use instead of hiding
  every object. This keeps shared team state understandable.

### 5. Update The Admin UI

In the separate `/admin` app:

- Add a Teams tab next to Users.
- Let super admins create a team with:
  - team name
  - optional slug
  - initial owner selected from regular enabled users
- Let super admins inspect membership and add/remove users if desired.

Keep user data import/export out of `/admin`.

### 6. Tests To Add

Account store tests:

- Team creation rejects super-admin owner.
- Team creation adds initial owner membership.
- Slug uniqueness.
- Add/remove member behavior.
- Cannot remove the final owner.
- Disabled users cannot be added.

UI/API tests:

- Regular user cannot list teams they are not in.
- Super admin can create a team with a regular owner.
- Team owner/admin can add members.
- Member/viewer cannot manage membership.

Registry/data tests:

- Personal directories remain visible only to their owner.
- Team directories are visible to members.
- Super admins still cannot create/import/export data.
- Connectors do not gain access to unlisted team directories.

Regression tests:

- Existing `memd data export/import` remains personal and strips internal owner
  and team IDs.
- Existing non-team directory/connector flows still pass.

## Acceptance Criteria For MVP

- A super admin can create a team and choose a regular owner.
- A team owner/admin can add a regular user as member or viewer.
- A regular user can switch between Personal and Team scope in the main UI.
- Team-scoped directories/connectors are visible to team members according to
  role.
- Personal directories/connectors remain private to their owning user.
- Super admins remain data-less account administrators.
- Existing user import/export keeps working and does not leak team ownership
  metadata into portable bundles.
- `go test ./...` and `bash build/build.sh host` pass.

## Files Most Likely To Change

- `server/internal/account/schema.go`
- `server/internal/account/store.go`
- `server/internal/account/user_data.go`
- `server/internal/account/store_test.go`
- `server/internal/config/config.go`
- `server/internal/registry/registry.go`
- `server/internal/registry/registry_test.go`
- `server/internal/ui/auth.go`
- `server/internal/ui/ui.go`
- `server/internal/ui/ui_test.go`
- `server/internal/ui/assets/admin.html`
- `server/internal/ui/assets/admin.js`
- `server/internal/ui/assets/index.html`
- `server/internal/ui/assets/script.js`
- `server/internal/ui/assets/style.css`
- `docs/server.md`
- `README.md`

## Open Schema Question For Later

If the product needs every team member to create their own connectors against
shared team directories, do schema v3 before building that feature. The cleanest
direction is likely globally addressable directory/connector IDs, with
ownership expressed by `owner_user_id` and `team_id` columns rather than by
composite primary keys. That migration is heavier than the team-management MVP,
so keep it separate unless member-created team connectors are required now.
