// TypeScript mirror of memd's Go HTTP/JSON contract.
//
// Every interface here corresponds to a Go struct serialised by the
// `server/internal/ui` handlers (and the types they embed from
// `server/internal/config`, `server/internal/account`, `server/internal/tasks`,
// `server/internal/storage`, `server/internal/logs`, `server/internal/doctrine`).
// JSON field names match the Go `json:"..."` tags exactly (snake_case).
// Fields with `,omitempty` on the Go side are optional (`?`) here.

// --- Enumerations ---------------------------------------------------------

// Team membership role. account.RoleOwner / RoleAdmin / RoleMember / RoleViewer.
export type TeamRole = "owner" | "admin" | "member" | "viewer";

// Invites may only grant admin/member/viewer (never owner). account.validInviteRole.
export type InviteRole = "admin" | "member" | "viewer";

// Connector transport. config.ConnectorKindMCP / ConnectorKindHTTP.
export type ConnectorKind = "mcp" | "http";

// Directory storage backend. config.Directory.Backend.
export type DirectoryBackend = "local" | "git";

// Log severity. logs.Entry.Level (Info/Warn/Error).
export type LogLevel = "info" | "warn" | "error";

// Task priority after normalisation. tasks.normalizePrio.
export type TaskPriority = "high" | "med" | "low";

// --- Session / auth (ui/auth.go, ui/session.go) ---------------------------

// ui.sessionUser — the signed-in account, as returned by /api/session and /api/auth/login.
export interface SessionUser {
  id: string;
  username: string;
  display_name: string;
  email?: string;
  super_admin: boolean;
  // True for password (non-OIDC) accounts: they may pick a custom local
  // directory path and use the filesystem browser.
  local: boolean;
}

// ui.authConfig — which sign-in options the front-end should render.
export interface AuthConfig {
  oidc_enabled: boolean;
}

// ui.uiFeatures — which optional server features are live. The front-end must
// not probe an optional route instead: disabled paths answer 404 or fall
// through to the SPA catch-all and answer 200 with index.html.
export interface UiFeatures {
  // The termulaa reverse-tunnel rendezvous (/rc/api/*), in its EFFECTIVE
  // state: on by default, toggled at runtime from the admin console, and
  // forced off by the MEMD_RC=0 kill switch.
  rc: boolean;
}

// Response of GET /api/session (ui.sessionAPI). `user` is null when signed out.
export interface SessionResponse {
  auth: AuthConfig;
  features: UiFeatures;
  user: SessionUser | null;
}

// Response of POST /api/auth/login (ui.loginAPI).
export interface LoginResponse {
  user: SessionUser;
}

// Response of POST /api/auth/logout (ui.logoutAPI). `logout_url` is present only
// for SSO sessions that have an RP-initiated logout endpoint.
export interface LogoutResponse {
  ok: boolean;
  logout_url?: string;
}

// --- Directories (ui/ui.go: directoryView, featureToggle) -----------------

// ui.featureToggle — per-directory state of one built-in feature.
export interface FeatureToggle {
  key: string;
  name: string;
  enabled: boolean;
  coming_soon: boolean;
}

// ui.directoryView — a memory directory as shown to a user.
export interface DirectoryView {
  id: string;
  owner_user_id?: string;
  team_id?: string;
  team_name?: string;
  owner_connector_id?: string;
  name: string;
  description?: string;
  backend: DirectoryBackend;
  detail: string;
  // For git backends: a browsable https URL for the repo (credentials and the
  // trailing ".git" stripped). Empty/omitted for local folders or unrecognised
  // remotes. The card and detail page surface it as an "open repository" link.
  repo_url?: string;
  error?: string;
  // "" / absent = team members with write roles may write; "readonly" = only
  // the owner writes. Only meaningful on team-shared directories.
  share_mode?: DirectoryShareMode;
  owned: boolean;
  can_manage: boolean;
  can_attach: boolean;
  // Whether the current user could write this directory through a write
  // connector (owner, or write-capable team role on a read-write share).
  can_write: boolean;
  features: FeatureToggle[];
}

export type DirectoryShareMode = "" | "readonly";

// Team IDs a connector follows: all directories shared with these teams are
// attached dynamically, including ones shared after the connector was created.
export type AttachTeams = string[];

// config.Git — the git backend configuration for a directory. Sent in create /
// git-check request bodies and (without secrets) is part of stored directories.
export interface GitConfig {
  remote_url: string;
  branch: string;
  base_path: string;
  author_name: string;
  author_email: string;
  auth_username?: string;
  auth_token?: string;
  ssh_key_path?: string;
  // Go duration strings ("5m", "30s").
  wait_for_writes?: string;
  save_every?: string;
}

// config.DirectoryFeature — the per-directory enable record (used inside the
// user-data export bundle's config.Directory).
export interface DirectoryFeature {
  key: string;
  enabled: boolean;
  settings?: Record<string, string>;
}

// config.Directory — the stored shape, as carried by the user-data bundle.
export interface DirectoryConfig {
  id: string;
  owner_user_id?: string;
  team_id?: string;
  owner_connector_id?: string;
  name: string;
  description: string;
  backend: DirectoryBackend;
  local_path?: string;
  git?: GitConfig;
  created_at: string;
  features?: DirectoryFeature[];
}

// Body of POST /api/directories (ui.directoriesAPI).
export interface CreateDirectoryRequest {
  name: string;
  team_id?: string;
  share_mode?: DirectoryShareMode;
  description?: string;
  backend: DirectoryBackend;
  local_path?: string;
  git?: GitConfig | null;
}

// Body of PATCH /api/directories/:id (ui.directoryAPI). Every field is optional;
// only the fields present are changed. `feature` toggles one feature on/off.
export interface UpdateDirectoryRequest {
  name?: string;
  description?: string;
  team_id?: string;
  share_mode?: DirectoryShareMode;
  owner_connector_id?: string;
  feature?: { key: string; enabled: boolean };
}

// Response of PATCH /api/directories/:id.
export interface UpdateDirectoryResponse {
  id: string;
  name: string;
  description: string;
  team_id: string;
  share_mode?: DirectoryShareMode;
  owner_connector_id: string;
}

// Response of POST /api/directories (the new directory's id).
export interface CreateDirectoryResponse {
  id: string;
}

// --- Connectors (ui/ui.go: connectorView) ---------------------------------

// ui.connectorView — a connector as shown to a user.
export interface ConnectorView {
  id: string;
  owner_user_id?: string;
  team_id?: string;
  team_name?: string;
  name: string;
  kind: ConnectorKind;
  url: string;
  auth_url: string;
  auth_header: string;
  write: boolean;
  directory_ids: string[];
  directory_names: string;
  // Teams this connector follows: their shared directories attach dynamically.
  attach_teams?: string[];
  attach_team_names?: string[];
  owned: boolean;
  can_manage: boolean;
}

// config.Connector — the stored shape, as carried by the user-data bundle.
export interface ConnectorConfig {
  id: string;
  owner_user_id?: string;
  team_id?: string;
  name: string;
  kind?: ConnectorKind;
  token: string;
  directory_ids: string[];
  write: boolean;
  created_at: string;
}

// Body of POST /api/connectors and PUT /api/connectors/:id.
export interface ConnectorRequest {
  name: string;
  team_id?: string;
  kind?: ConnectorKind;
  directory_ids: string[];
  attach_teams?: string[];
  write: boolean;
}

// Response of POST /api/connectors and POST /api/connectors/:id/rotate.
export interface ConnectorSecretResponse {
  id: string;
  url: string;
  auth_url: string;
  auth_type: "bearer";
}

// --- Teams (ui/teams.go) --------------------------------------------------

// ui.teamView.
export interface Team {
  id: string;
  name: string;
  slug: string;
  role: TeamRole;
  can_manage: boolean;
  can_delete: boolean;
}

// ui.teamMemberView.
export interface TeamMember {
  user_id: string;
  username: string;
  display_name: string;
  role: TeamRole;
}

// ui.teamInviteView — an invite as listed for managers.
export interface TeamInvite {
  id: string;
  role: InviteRole;
  max_uses?: number | null;
  use_count: number;
  expires_at?: string;
  revoked_at?: string;
  created_at: string;
}

// ui.invitePreviewView — the public-facing preview at GET /api/team-invites/:token.
export interface InvitePreview {
  team_id: string;
  team_name: string;
  role: InviteRole;
  max_uses?: number | null;
  use_count: number;
  expires_at?: string;
  valid: boolean;
  error?: string;
}

// Body of POST /api/teams.
export interface CreateTeamRequest {
  name: string;
  slug?: string;
}

// Body of PATCH /api/teams/:id.
export interface UpdateTeamRequest {
  name: string;
  slug?: string;
}

// Body of POST /api/teams/:id/members. Provide user_id or username.
export interface AddTeamMemberRequest {
  user_id?: string;
  username?: string;
  role: TeamRole;
}

// Body of PATCH /api/teams/:id/members/:memberId.
export interface SetTeamMemberRoleRequest {
  role: TeamRole;
}

// Body of POST /api/teams/:id/invites. expires_at is RFC3339 or a unix-seconds
// string; empty/omitted means no expiry.
export interface CreateTeamInviteRequest {
  role: InviteRole;
  expires_at?: string;
  max_uses?: number | null;
}

// Response of POST /api/teams/:id/invites.
export interface CreateTeamInviteResponse {
  invite: TeamInvite;
  token: string;
  invite_url: string;
}

// --- Tasks (ui/tasks.go + tasks package) ----------------------------------

// tasks.Task — one checklist item. Subtasks reuse the same shape.
export interface Task {
  file: string;
  line: number;
  raw: string;
  done: boolean;
  title: string;
  due?: string;
  prio?: TaskPriority;
  tags?: string[];
  link?: string;
  subtasks?: Task[];
  notes?: string[];
}

// tasks.List — one parsed list file with its top-level tasks.
export interface TaskList {
  file: string;
  name: string;
  tasks: Task[];
  open: number;
  total: number;
}

// tasks.ListSummary — one line of the board's list index.
export interface TaskListSummary {
  file: string;
  name: string;
  open: number;
  total: number;
}

// tasks.Board — the derived overview, open work bucketed by deadline.
export interface TaskBoard {
  overdue: Task[];
  due_soon: Task[];
  later: Task[];
  no_date: Task[];
  lists: TaskListSummary[];
}

// Response of GET /api/directories/:id/tasks (ui.tasksGet).
export interface DirectoryTasksResponse {
  folder: string;
  lists: TaskList[];
  board: TaskBoard;
}

// One directory's tasks within the all-directories aggregate (ui.tasksAllAPI).
export interface DirectoryTasks {
  id: string;
  name: string;
  can_write: boolean;
  lists: TaskList[];
  board: TaskBoard;
}

// Response of GET /api/tasks (ui.tasksAllAPI).
export interface AllTasksResponse {
  directories: DirectoryTasks[];
}

// Body of POST /api/directories/:id/tasks (ui.tasksMutate).
// action "toggle": flips line `line` in `file`, guarded by `expect`.
// action "add": appends a task `title` to `file` (or list_name → a slugged file).
export interface TaskMutateRequest {
  action: "toggle" | "add";
  file?: string;
  line?: number;
  expect?: string;
  title?: string;
  list_name?: string;
}

// Response of a task mutation: the file that was written.
export interface TaskMutateResponse {
  file: string;
}

// --- Files & filesystem browse (ui/files.go, ui/ui.go) --------------------

// storage.DirEntry — one child of a directory path.
export interface DirEntry {
  name: string;
  path: string;
  is_dir: boolean;
}

// Response of GET /api/directories/:id/files (ui.directoryFilesAPI).
export interface DirectoryFilesResponse {
  path: string;
  entries: DirEntry[];
}

// One node in the link graph (graph.Node).
export interface GraphNode {
  path: string;
  type?: string;
  title: string;
  description?: string;
  inbound: number;
  outbound: number;
}

// One directed markdown link between two files (graph.Edge).
export interface GraphEdge {
  from: string;
  to: string;
  broken: boolean;
}

// Response of GET /api/directories/:id/graph (ui.directoryGraphAPI / graph.Graph).
export interface GraphResponse {
  nodes: GraphNode[];
  edges: GraphEdge[];
  orphans: string[];
  broken: GraphEdge[];
}

// One subdirectory in the server-filesystem browser (ui.browseAPI).
export interface BrowseEntry {
  name: string;
}

// Response of GET /api/browse (ui.browseAPI).
export interface BrowseResponse {
  path: string;
  parent: string;
  dirs: BrowseEntry[];
}

// --- Git connection check (storage/git_check.go) --------------------------

// storage.GitCheckResult — one step of the non-destructive connection check.
export interface GitCheckResult {
  id: string;
  label: string;
  ok: boolean;
  detail?: string;
  error?: string;
}

// storage.GitConnectionReport — response of POST /api/git/check (ui.gitCheckAPI).
export interface GitCheckReport {
  ok: boolean;
  checks: GitCheckResult[];
}

// Body of POST /api/git/check.
export interface GitCheckRequest {
  git: GitConfig;
}

// --- Logs (logs/logs.go) --------------------------------------------------

// logs.Entry — one log record.
export interface LogEntry {
  id: number;
  time: string;
  level: LogLevel;
  message: string;
}

// Response of GET /api/logs (ui.logsAPI).
export interface LogsResponse {
  entries: LogEntry[];
}

// --- Admin: users (ui/auth.go: userViewData) ------------------------------

// ui.userViewData — an account as shown in the super-admin user list.
export interface AdminUser {
  id: string;
  username: string;
  display_name: string;
  disabled: boolean;
  super_admin: boolean;
  created_at: string;
  last_login_at?: string;
  sso_linked: boolean;
  sso_orphan?: boolean;
  issuer?: string;
}

// Body of POST /api/admin/users (ui.adminUsersAPI).
export interface CreateAdminUserRequest {
  username: string;
  password: string;
  display_name?: string;
}

// Body of PATCH /api/admin/users/:id (ui.adminUserAPI).
export interface SetUserDisabledRequest {
  disabled: boolean;
}

// Body of POST /api/admin/users/:id/password (ui.adminUserAPI).
export interface SetUserPasswordRequest {
  password: string;
}

// Response of GET /api/admin/users.
export interface AdminUsersResponse {
  users: AdminUser[];
}

// Response of POST /api/admin/users (the created account).
export interface AdminUserResponse {
  user: AdminUser;
}

// --- Admin: OIDC (ui/admin_oidc.go) ---------------------------------------

// ui.oidcConfigView — the admin-facing OIDC settings. The client secret is
// never returned; only `has_client_secret` reports its presence.
export interface OIDCConfig {
  enabled: boolean;
  issuer_url: string;
  client_id: string;
  has_client_secret: boolean;
  redirect_uri: string;
  scopes: string;
  post_logout_redirect_uri: string;
  active: boolean;
}

// Response of GET / PUT /api/admin/oidc (ui.adminOIDCAPI).
export interface OIDCConfigResponse {
  oidc: OIDCConfig;
}

// Body of PUT /api/admin/oidc (ui.updateOIDC). client_secret is a pointer on the
// Go side: omit (or null) to keep the stored secret, send a string to replace it.
export interface SaveOIDCRequest {
  enabled: boolean;
  issuer_url: string;
  client_id: string;
  client_secret?: string | null;
  redirect_uri: string;
  scopes: string;
  post_logout_redirect_uri: string;
  // Mints a new provider id; existing SSO users stop matching.
  replace_provider?: boolean;
}

// ui.rcConfigView — the admin-facing state of the reverse-tunnel rendezvous
// (termulaa rc). `enabled` is the persisted super-admin setting (defaults to
// on when nothing has been stored); `active` is the effective state right now.
export interface RCConfig {
  enabled: boolean;
  active: boolean;
  // MEMD_RC is explicitly set to an off value in the server's environment,
  // forcing the feature off regardless of `enabled`.
  kill_switch: boolean;
  // A token-signing key exists (MEMD_RC_TOKEN_SECRET or MEMD_SESSION_SECRET).
  // Without one the feature cannot run at all.
  available: boolean;
  // Dedicated viewer hostname in host mode, "" in path mode.
  view_host: string;
}

// Response of GET / PUT /api/admin/rc (ui.adminRCAPI).
export interface RCConfigResponse {
  rc: RCConfig;
}

// Body of PUT /api/admin/rc (ui.adminRCAPI).
export interface SaveRCRequest {
  enabled: boolean;
}

// Body of POST /api/admin/oidc/relink (ui.adminOIDCRelinkAPI).
export interface OIDCRelinkRequest {
  from_issuer: string;
}

// Response of POST /api/admin/oidc/relink.
export interface OIDCRelinkResponse {
  adopted: number;
  // Usernames skipped because the subject is already taken under this provider.
  skipped: string[];
}

// --- Admin: doctrines (ui/admin_doctrine.go, doctrine/live.go) ------------

// doctrine.EntryView — one live-editable doctrine.
export interface Doctrine {
  id: string;
  label: string;
  text: string;
  overridden: boolean;
}

// Response of GET /api/admin/doctrines (ui.adminDoctrinesAPI).
export interface DoctrinesResponse {
  doctrines: Doctrine[];
}

// Body of PUT /api/admin/doctrines/:id (ui.adminDoctrineAPI).
export interface SaveDoctrineRequest {
  text: string;
}

// Response of PUT / DELETE /api/admin/doctrines/:id.
export interface DoctrineMutateResponse {
  id: string;
  overridden: boolean;
}

// --- User data export / import (account/user_data.go) ---------------------

// account.UserDataBundle — the export payload (GET /api/data) and import body
// (POST /api/data). Uses the stored config shapes for directories/connectors.
export interface UserDataBundle {
  format: string;
  version: number;
  exported_at: string;
  directories: DirectoryConfig[];
  connectors: ConnectorConfig[];
}

// --- termulaa reverse tunnel (tunnel/handler.go) ---------------------------

// tunnel.agentView — one currently connected termulaa agent, straight from
// live hub state. `tunnels` is the number of live pooled connections; an
// agent with zero tunnels is offline and is normally absent from the list.
export interface RcAgent {
  // First 8 hex chars of the agent id (sha256 of its token).
  id: string;
  label: string;
  // The local port termulaa serves on the agent's machine.
  port: number;
  tunnels: number;
  connected_at: string;
  // Path-mode terminal link (/rc/t/<agentID>/); absent in host mode, where
  // the terminal lives on the dedicated view host instead.
  url?: string;
}

// Response of GET /rc/api/agents (tunnel.agentsAPI). `view_host` is the
// dedicated viewer hostname in host mode, "" in path mode.
export interface RcAgentsResponse {
  agents: RcAgent[];
  view_host: string;
}

// Body of POST /rc/api/tokens (tunnel.mintAPI). `ttl` is in DAYS (0 selects
// the 30-day default; capped at 90).
export interface MintRcTokenRequest {
  label: string;
  ttl: number;
}

// Response of POST /rc/api/tokens. The token is a secret shown exactly once —
// render it, let the user copy it, and never persist it anywhere.
export interface MintRcTokenResponse {
  token: string;
  expires_at: string;
  // Path mode only: the terminal URL this token's agent will appear at.
  open_url?: string;
}

// --- Phone app pairing (ui/apptokens.go) -----------------------------------

// Response of POST /api/app/pair (ui.appPairAPI). One outstanding code per
// user: minting again replaces the previous code. The code is 9 chars, single
// use, and expires after 5 minutes; display it grouped XXX-XXX-XXX (the server
// accepts it with or without dashes and in any case).
export interface AppPairResponse {
  code: string;
  expires_at: string;
}

// ui.appTokenView — one paired phone (a long-lived, revocable app token). The
// token secret itself is only ever returned to the app at redeem time.
export interface AppTokenView {
  id: string;
  label: string;
  created_at: string;
  last_used_at?: string;
}

// Response of GET /api/app/tokens (ui.appTokensAPI).
export interface AppTokensResponse {
  tokens: AppTokenView[];
}

// --- Generic responses ----------------------------------------------------

// The `{ "ok": true }` acknowledgement many mutating handlers return.
export interface OkResponse {
  ok: boolean;
}

// The error envelope every handler emits on failure: `{ "error": "..." }`.
export interface ApiErrorBody {
  error: string;
}
