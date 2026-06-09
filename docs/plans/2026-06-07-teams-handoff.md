# Teams Work Handoff And Implementation Log

Date: 2026-06-07

Status: implemented 2026-06-08.

This started as the handoff for the slice after local auth and user-scoped SQL
data. The implemented goal is simple friends/family team management without
losing the invariants: super admins manage accounts only, regular users own
memory data, and memory content stays in user-controlled folders or Git
repositories.

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
- The account schema is v3 and includes `teams`, `team_members`,
  `team_invites`, `team_invite_uses`, and nullable `team_id` columns on
  `user_directories` and `user_connectors`.
- Regular users can create teams from the main UI. The creator becomes owner.
- Owners/admins can create and revoke invite links with optional expiry and
  optional max-use count.
- Signed-in regular users can accept valid invite links.
- Owners/admins can mark directories/connectors as team-scoped.
- Team members can see team-scoped directories/connectors in the main UI.
- The main UI is now a responsive shell with How it works, Teams, Directories,
  Connectors, and Activity views. Mobile uses a hamburger drawer and keeps dark
  mode in the top bar.

Implemented team store/API functions include:

- `CreateTeamForActor(ctx, input)`
- `TeamByID(ctx, teamID)`
- `UpdateTeam(ctx, teamID, actorUserID, input)`
- `ListTeamsForUser(ctx, userID)`
- `ListTeamMembers(ctx, teamID)`
- `UserTeamRole(ctx, teamID, userID)`
- `SetTeamMemberRole(ctx, teamID, userID, role, actorUserID)`
- `RemoveTeamMember(ctx, teamID, userID, actorUserID)`
- `DeleteTeam(ctx, teamID, actorUserID)`
- `CanManageTeam`, `CanManageTeamData`, `CanViewTeam`
- `CreateTeamInvite(ctx, input)`
- `ListTeamInvites(ctx, teamID, actorUserID)`
- `RevokeTeamInvite(ctx, inviteID, actorUserID)`
- `TeamInviteByToken(ctx, token)`
- `AcceptTeamInvite(ctx, token, userID)`

Existing role constants:

- `owner`
- `admin`
- `member`
- `viewer`

## Important Schema Caveat

The current directory/connector data model is still fundamentally
user-namespaced:

- `user_directories` primary key is `(owner_user_id, id)`.
- `user_connectors` primary key is `(owner_user_id, id)`.
- `user_connector_directories` foreign keys require the same
  `owner_user_id` for a connector and the directories it references.

That means a connector owned by user B cannot directly attach to a team
directory owned by user A without either:

1. Keeping team directories/connectors under one owning user for the MVP, or
2. Doing a future schema migration to make directory/connector IDs globally
   addressable or team-namespaced.

Implemented MVP: avoid directory/connector primary-key churn first. Treat
`owner_user_id` as the creator namespace and `team_id` as shared visibility.
Only team owners/admins manage team directories/connectors. Members/viewers can
see and copy shared connector URLs, but do not create their own connectors
against another user's team directory until a future global-ID/team-namespace
migration.

## Product Decisions To Lock First

1. Who creates teams?
   Locked: regular users create teams from the main UI. The creating regular
   user becomes the initial `owner`. Super admins remain account-management
   identities and do not create or own teams.

2. Can super admins be team members?
   Locked: no. Keep super admins out of memory ownership and team membership.
   Use regular accounts for real memory work.

3. How do people join teams?
   Locked: team owners/admins create copyable invite links. Each invite link
   can have optional time expiry and optional max-use count. Owners/admins can
   revoke links before expiry. Email/SMS delivery remains deferred; copying the
   link is enough for this slice.

4. What do roles mean in the MVP?
   Locked:
   - `owner`: all admin privileges, plus demoting admins and deleting the team.
   - `admin`: manage team settings, regular members, invite links, and
     directories/connectors marked for the team.
   - `member`: access team directories/connectors that owners/admins have
     marked for the team.
   - `viewer`: lower-access membership role for shared read-oriented use. It is
     not a management role.

   Owners are the only role that can demote/remove admins or delete the team.
   Admins can promote members to admin if we want owner/admin management to be
   otherwise symmetric, but admins cannot demote another admin, remove an owner,
   or delete the team.

5. How do directories become team directories?
   Implemented MVP: team owners/admins mark directories/connectors as
   team-scoped. A regular member sees team-scoped directories because they are a
   member of that team. Keep the original `owner_user_id` as the creator
   namespace until a future global-ID/team-namespace migration.

6. Should members create team connectors?
   Implemented MVP: no, unless the connector and directory are both created in
   that same member's namespace. Revisit after a global-ID/team-namespace
   migration if this becomes important.

## Implemented Work

### 1. Account Store

Added store methods in `server/internal/account`:

- `CreateTeamForActor(ctx, input)` where the actor is a regular user and
  becomes the initial owner.
- `DeleteTeam(ctx, teamID, actorUserID)`.
- `TeamByID(ctx, teamID)`.
- `ListTeamsForUser(ctx, userID)`.
- `ListTeamMembers(ctx, teamID)`.
- `SetTeamMemberRole(ctx, teamID, userID, role, actorUserID)`.
- `RemoveTeamMember(ctx, teamID, userID)`.
- `UserTeamRole(ctx, teamID, userID)`.
- Permission helpers such as `CanManageTeam`, `CanManageTeamData`, and
  `CanViewTeam`.
- `CreateTeamInvite(ctx, CreateTeamInviteInput{TeamID, CreatedByUserID,
  Role, ExpiresAt, MaxUses})`.
- `ListTeamInvites(ctx, teamID, actorUserID)`.
- `RevokeTeamInvite(ctx, inviteID, actorUserID)`.
- `TeamInviteByToken(ctx, token)`.
- `AcceptTeamInvite(ctx, token, userID)`.

Rules enforced in the store:

- Team creators, owners, admins, members, and invite acceptors must be regular
  users, not super admins.
- Disabled users should not be added to teams.
- Every team must keep at least one `owner`.
- Only valid roles are accepted.
- Duplicate team slugs should return `ErrAlreadyExists`.
- Owners/admins can create and revoke invite links.
- Invite links can have nullable `expires_at` and nullable `max_uses`.
- Expired, revoked, or fully used invite links cannot be accepted.
- Accepting an invite is transactional: if the user is already a member, return
  success without consuming another use; otherwise add the membership and
  increment `use_count` only if the link still has capacity.
- Only owners can demote admins, remove admins, remove owners, or delete the
  team.
- Team owners/admins can mark directories/connectors as team-scoped; ordinary
  members cannot.

Added schema v3 tables:

- `team_invites`
  - `id`
  - `team_id`
  - `token_hash`
  - `role`
  - `max_uses`
  - `use_count`
  - `expires_at`
  - `revoked_at`
  - `created_by_user_id`
  - `created_at`
  - `updated_at`
- `team_invite_uses`
  - `invite_id`
  - `user_id`
  - `used_at`

Store only a hash of the invite token. The copied link should use a high-entropy
opaque token, not the database invite ID.

### 2. Team API Endpoints

Added APIs in `server/internal/ui/teams.go`:

- `GET /api/teams`
- `POST /api/teams`
- `GET /api/teams/{id}`
- `PATCH /api/teams/{id}`
- `DELETE /api/teams/{id}`
- `GET /api/teams/{id}/members`
- `POST /api/teams/{id}/members`
- `PATCH /api/teams/{id}/members/{user_id}`
- `DELETE /api/teams/{id}/members/{user_id}`
- `GET /api/teams/{id}/invites`
- `POST /api/teams/{id}/invites`
- `POST /api/teams/{id}/invites/{invite_id}/revoke`
- `GET /api/team-invites/{token}`
- `POST /api/team-invites/{token}/accept`

Permission shape:

- Regular users can create teams and automatically become owner.
- Super admins cannot create teams or accept team invites.
- Team owners/admins can create/revoke invite links and manage team data.
- Team owners have the additional privilege to demote admins and delete the
  team.
- Regular users see only teams they belong to.
- Regular users can add themselves to teams only by accepting a valid invite
  link.
- Invite previews should expose safe display fields such as team name, role, and
  expiry, but never token hashes.

### 3. Team Scope In Directory/Connector Data

Added `TeamID string json:"team_id,omitempty"` to `config.Directory` and
`config.Connector`.

Updated:

- SQL list/upsert paths in `server/internal/account/user_data.go` to read/write
  `team_id`.
- `NewUserDataBundle` to clear `TeamID`, so user import/export remains personal
  and importable into any regular user.
- Directory and connector create/update request bodies accept optional
  `team_id`.
- The registry read paths support:
  - personal objects where `team_id` is empty and `owner_user_id == user.ID`
  - team objects where `team_id` is in the user's memberships
- Directory/connector update paths ensure only team owners/admins can set, change,
  or clear `team_id`.

Keep MCP connector serving strict: a connector should only serve the directories
listed on that connector, and team visibility in the UI must not grant an MCP
token more directory access than its saved directory IDs.

### 4. Main UI

In `assets/index.html` and `assets/script.js`:

- Added primary views:
  - How it works
  - Teams
  - Directories
  - Connectors
  - Activity
- Added responsive navigation:
  - desktop side rail
  - mobile hamburger drawer
  - dark-mode control remains in the mobile top bar
  - Activity is a separate page on smaller screens
- Added a scope control in the navigation:
  - Personal
  - Team selector for teams the user belongs to
- Added a Teams/settings surface where a regular user can create a team.
- In the team settings surface, show members, roles, and active invite links.
- Added invite creation controls:
  - optional expiry date/time
  - optional max-use count
  - copy link
  - revoke link
- Show team badges on directory and connector cards.
- When creating or editing a directory/connector, owners/admins can mark it
  for one of their teams.
- Hide team-management controls from super-admin-only accounts.
- For team members/viewers, disable controls they cannot use instead of hiding
  every object. This keeps shared team state understandable.
- Added an invite acceptance state. If the user is not signed in, show login
  first, then continue accepting the same invite. Invite-scoped self-signup can
  be added here if we decide links should onboard people who do not yet have a
  local account.

### 5. Admin UI

In the separate `/admin` app:

- Keep `/admin` focused on account management.
- Do not add super-admin team creation.
- Optional later: read-only team inspection or emergency team deletion for
  support recovery, but keep it outside the MVP unless needed.

Keep user data import/export out of `/admin`.

### 6. Tests Added / Covered

Account store tests:

- Team creation rejects super-admin actors.
- Team creation adds initial owner membership.
- Slug uniqueness.
- Invite creation records expiry and max-use limits.
- Expired invites cannot be accepted.
- Revoked invites cannot be accepted.
- Maxed-out invites cannot be accepted.
- Accepting an invite adds membership and increments `use_count`.
- Re-accepting as an existing member does not consume another use.
- Cannot remove the final owner.
- Disabled users cannot be added.
- Only owners can demote admins or delete teams.

UI/API tests:

- Regular user cannot list teams they are not in.
- Regular user can create a team and becomes owner.
- Super admin cannot create a team.
- Team owner/admin can create and revoke invite links.
- Regular user can join by valid invite link.
- Member/viewer cannot manage membership.
- Admin cannot demote another admin or delete the team.

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

- [x] A regular user can create a team and becomes its owner.
- [x] A team owner/admin can create a copyable invite link with optional expiry and
  optional max-use count.
- [x] A regular user can accept a valid invite link and join the team.
- [x] Expired, revoked, and maxed-out invite links cannot add members.
- [x] Team owners/admins can mark directories/connectors as team-scoped.
- [x] A regular user can switch between Personal and Team scope in the main UI.
- [x] Team-scoped directories/connectors are visible to team members according to
  role.
- [x] Only team owners can demote admins or delete the team.
- [x] Personal directories/connectors remain private to their owning user.
- [x] Super admins remain data-less account administrators.
- [x] Existing user import/export keeps working and does not leak team ownership
  metadata into portable bundles.
- [x] `go test ./...` and `bash build/build.sh host` pass.

## Files Changed

- `server/internal/account/schema.go`
- `server/internal/account/store.go`
- `server/internal/account/teams.go`
- `server/internal/account/user_data.go`
- `server/internal/account/store_test.go`
- `server/internal/config/config.go`
- `server/internal/registry/registry.go`
- `server/internal/registry/registry_test.go`
- `server/internal/ui/auth.go`
- `server/internal/ui/teams.go`
- `server/internal/ui/ui.go`
- `server/internal/ui/ui_test.go`
- `server/internal/ui/assets/index.html`
- `server/internal/ui/assets/script.js`
- `server/internal/ui/assets/style.css`
- `server/internal/ui/assets/icons/menu.svg`
- `server/internal/ui/assets/icons/users.svg`
- `docs/server.md`
- `README.md`

## Open Schema Question For Later

If the product needs every team member to create their own connectors against
shared team directories, do a dedicated schema migration before building that
feature. The cleanest direction is likely globally addressable
directory/connector IDs, with ownership expressed by `owner_user_id` and
`team_id` columns rather than by composite primary keys. That migration is
heavier than the team-management MVP, so keep it separate unless member-created
team connectors are required now.
