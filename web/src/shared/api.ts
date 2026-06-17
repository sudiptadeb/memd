// Typed HTTP client for memd's Go backend.
//
// Every function here maps to one route registered in `Handler.Mount()`
// (server/internal/ui/ui.go) and speaks the exact JSON contract those handlers
// expose. Request/response shapes come from ./types, whose field names mirror
// the Go `json:"..."` tags (snake_case). The backend authenticates with a
// sealed session cookie, so all requests are sent with `credentials:
// "same-origin"`. On a non-2xx response the backend returns `{ "error": "..." }`;
// `request` decodes that and throws an `ApiError` carrying the status + message.

import type {
  AddTeamMemberRequest,
  AdminUserResponse,
  AdminUsersResponse,
  AllTasksResponse,
  ApiErrorBody,
  BrowseResponse,
  ConnectorRequest,
  ConnectorSecretResponse,
  CreateAdminUserRequest,
  CreateDirectoryRequest,
  CreateDirectoryResponse,
  CreateTeamInviteRequest,
  CreateTeamInviteResponse,
  CreateTeamRequest,
  DirectoryFilesResponse,
  DirectoryTasksResponse,
  GraphResponse,
  Doctrine,
  DoctrineMutateResponse,
  DoctrinesResponse,
  GitCheckReport,
  GitConfig,
  InvitePreview,
  LogsResponse,
  LoginResponse,
  LogoutResponse,
  OIDCConfigResponse,
  OIDCRelinkRequest,
  OIDCRelinkResponse,
  OkResponse,
  SaveDoctrineRequest,
  SaveOIDCRequest,
  SessionResponse,
  SetTeamMemberRoleRequest,
  SetUserDisabledRequest,
  SetUserPasswordRequest,
  Team,
  TaskMutateRequest,
  TaskMutateResponse,
  UpdateDirectoryRequest,
  UpdateDirectoryResponse,
  UpdateTeamRequest,
  UserDataBundle,
} from "./types";

// --- Core --------------------------------------------------------------------

// ApiError is thrown for any non-2xx response. `.status` is the HTTP status and
// `.message` is the backend's `{ "error": "..." }` text (or a status fallback).
export class ApiError extends Error {
  readonly status: number;
  // The decoded response body, when one was present (useful for richer errors).
  readonly body: unknown;

  constructor(status: number, message: string, body?: unknown) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.body = body;
    // Restore the prototype chain for instanceof across transpile targets.
    Object.setPrototypeOf(this, ApiError.prototype);
  }
}

// A JSON value passed as a request body. Strings/FormData/etc. are sent as-is;
// plain objects and arrays are JSON-encoded with a JSON content type.
type JsonBody = Record<string, unknown> | unknown[];

function isPlainJsonBody(body: BodyInit | JsonBody | null | undefined): body is JsonBody {
  if (body === null || body === undefined) return false;
  if (typeof body === "string") return false;
  if (body instanceof FormData) return false;
  if (body instanceof Blob) return false;
  if (body instanceof ArrayBuffer) return false;
  if (body instanceof URLSearchParams) return false;
  if (ArrayBuffer.isView(body)) return false;
  return typeof body === "object";
}

// Init like RequestInit, but `body` may also be a plain object/array that the
// client JSON-encodes for you.
interface ApiInit extends Omit<RequestInit, "body"> {
  body?: BodyInit | JsonBody | null;
}

// request issues a fetch to a memd API path and decodes the JSON response as T.
// It JSON-encodes plain-object/array bodies, sends the session cookie, decodes
// 2xx JSON (returning `undefined as T` for 204/empty bodies), and throws an
// ApiError carrying the backend's `{error}` message on any non-2xx status.
export async function request<T>(path: string, init: ApiInit = {}): Promise<T> {
  const { body, headers, ...rest } = init;
  const finalHeaders = new Headers(headers);
  let finalBody: BodyInit | null | undefined;

  if (isPlainJsonBody(body)) {
    if (!finalHeaders.has("Content-Type")) {
      finalHeaders.set("Content-Type", "application/json");
    }
    finalBody = JSON.stringify(body);
  } else {
    finalBody = body ?? undefined;
  }

  if (!finalHeaders.has("Accept")) {
    finalHeaders.set("Accept", "application/json");
  }

  const res = await fetch(path, {
    credentials: "same-origin",
    ...rest,
    headers: finalHeaders,
    body: finalBody,
  });

  // No content: 204, or a 2xx with an empty/non-JSON body. Callers that type
  // these as `void`/`OkResponse` tolerate the undefined.
  if (res.status === 204) {
    return undefined as T;
  }

  const text = await res.text();
  const data = text ? safeJsonParse(text) : undefined;

  if (!res.ok) {
    const message = errorMessage(data, res.status, res.statusText);
    throw new ApiError(res.status, message, data);
  }

  return data as T;
}

function safeJsonParse(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    // Non-JSON payload (e.g. a raw file or a plain-text error): surface the text.
    return text;
  }
}

function errorMessage(data: unknown, status: number, statusText: string): string {
  if (data && typeof data === "object" && "error" in data) {
    const err = (data as ApiErrorBody).error;
    if (typeof err === "string" && err) return err;
  }
  if (typeof data === "string" && data) return data;
  return statusText || `request failed with status ${status}`;
}

// query builds a "?a=1&b=2" string from a params object, dropping
// null/undefined values and coercing the rest to strings. Returns "" when empty.
export function query(params: Record<string, string | number | boolean | null | undefined>): string {
  const sp = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value === null || value === undefined) continue;
    sp.set(key, String(value));
  }
  const s = sp.toString();
  return s ? `?${s}` : "";
}

// --- Session & auth (ui/session.go, ui/auth.go, ui/oidc.go) ------------------

export const session = {
  // GET /api/session — ui.sessionAPI. Auth config + current user (null if out).
  get(): Promise<SessionResponse> {
    return request<SessionResponse>("/api/session");
  },
};

export const auth = {
  // POST /api/auth/login — ui.loginAPI. Local username/password sign-in.
  login(username: string, password: string): Promise<LoginResponse> {
    return request<LoginResponse>("/api/auth/login", {
      method: "POST",
      body: { username, password },
    });
  },

  // POST /api/auth/logout — ui.logoutAPI. Clears the session; may return an
  // IdP RP-initiated `logout_url` for SSO sessions.
  logout(): Promise<LogoutResponse> {
    return request<LogoutResponse>("/api/auth/logout", { method: "POST" });
  },

  // GET /auth/login — ui.oidcLogin. Server-side redirect into the OIDC flow;
  // navigate the browser here rather than fetching. `returnTo` is a same-site,
  // path-only return target enforced by the backend (safeReturnTo).
  ssoLoginUrl(returnTo?: string): string {
    return `/auth/login${returnTo ? query({ return_to: returnTo }) : ""}`;
  },
};

// --- Directories (ui/ui.go, ui/files.go, ui/tasks.go) ------------------------

export const directories = {
  // GET /api/directories — ui.directoriesAPI. Lists the user's directories.
  list(): Promise<{ directories: import("./types").DirectoryView[] }> {
    return request<{ directories: import("./types").DirectoryView[] }>("/api/directories");
  },

  // POST /api/directories — ui.directoriesAPI. Creates a directory.
  create(body: CreateDirectoryRequest): Promise<CreateDirectoryResponse> {
    return request<CreateDirectoryResponse>("/api/directories", {
      method: "POST",
      body: body as unknown as JsonBody,
    });
  },

  // PATCH /api/directories/:id — ui.directoryAPI. Partial update (name,
  // description, team scope, owner connector, or one feature toggle).
  update(id: string, body: UpdateDirectoryRequest): Promise<UpdateDirectoryResponse> {
    return request<UpdateDirectoryResponse>(`/api/directories/${encodeURIComponent(id)}`, {
      method: "PATCH",
      body: body as unknown as JsonBody,
    });
  },

  // PATCH /api/directories/:id — ui.directoryAPI (feature branch). Convenience
  // wrapper that toggles a single built-in feature on/off.
  setFeature(id: string, key: string, enabled: boolean): Promise<UpdateDirectoryResponse> {
    return request<UpdateDirectoryResponse>(`/api/directories/${encodeURIComponent(id)}`, {
      method: "PATCH",
      body: { feature: { key, enabled } },
    });
  },

  // DELETE /api/directories/:id — ui.directoryAPI. Removes a directory (204).
  remove(id: string): Promise<void> {
    return request<void>(`/api/directories/${encodeURIComponent(id)}`, { method: "DELETE" });
  },

  // GET /api/directories/:id/files?path= — ui.directoryFilesAPI. Lists children.
  files(id: string, path = ""): Promise<DirectoryFilesResponse> {
    return request<DirectoryFilesResponse>(
      `/api/directories/${encodeURIComponent(id)}/files${query({ path })}`,
    );
  },

  // GET /api/directories/:id/graph — ui.directoryGraphAPI. The directory's link
  // graph (nodes, edges, orphans, broken links) for the visual navigator.
  graph(id: string): Promise<GraphResponse> {
    return request<GraphResponse>(`/api/directories/${encodeURIComponent(id)}/graph`);
  },

  // GET /api/directories/:id/raw?path= — ui.directoryRawAPI. Serves one file's
  // bytes for the viewer. Returns the raw text; pass render/download to change
  // the server's content-type/disposition handling.
  async raw(
    id: string,
    path: string,
    opts: { render?: boolean; download?: boolean } = {},
  ): Promise<string> {
    const qs = query({
      path,
      render: opts.render ? 1 : undefined,
      download: opts.download ? 1 : undefined,
    });
    const res = await fetch(`/api/directories/${encodeURIComponent(id)}/raw${qs}`, {
      credentials: "same-origin",
    });
    const text = await res.text();
    if (!res.ok) {
      throw new ApiError(res.status, errorMessage(safeJsonParse(text), res.status, res.statusText));
    }
    return text;
  },

  // The URL of a directory file's raw bytes, for <img>/<iframe> src or a link.
  rawUrl(id: string, path: string, opts: { render?: boolean; download?: boolean } = {}): string {
    const qs = query({
      path,
      render: opts.render ? 1 : undefined,
      download: opts.download ? 1 : undefined,
    });
    return `/api/directories/${encodeURIComponent(id)}/raw${qs}`;
  },

  // GET /api/directories/:id/tasks — ui.directoryTasksAPI. The tasks dashboard.
  tasks(id: string): Promise<DirectoryTasksResponse> {
    return request<DirectoryTasksResponse>(`/api/directories/${encodeURIComponent(id)}/tasks`);
  },

  // POST /api/directories/:id/tasks — ui.directoryTasksAPI (mutate). Toggles a
  // checkbox or appends a task; returns the file that was written.
  mutateTasks(id: string, body: TaskMutateRequest): Promise<TaskMutateResponse> {
    return request<TaskMutateResponse>(`/api/directories/${encodeURIComponent(id)}/tasks`, {
      method: "POST",
      body: body as unknown as JsonBody,
    });
  },
};

// --- Connectors (ui/ui.go) ---------------------------------------------------

export const connectors = {
  // GET /api/connectors — ui.connectorsAPI. Lists the user's connectors.
  list(): Promise<{ connectors: import("./types").ConnectorView[] }> {
    return request<{ connectors: import("./types").ConnectorView[] }>("/api/connectors");
  },

  // POST /api/connectors — ui.connectorsAPI. Creates a connector; returns its
  // URL, auth URL, and minted secret metadata.
  create(body: ConnectorRequest): Promise<ConnectorSecretResponse> {
    return request<ConnectorSecretResponse>("/api/connectors", {
      method: "POST",
      body: body as unknown as JsonBody,
    });
  },

  // PUT /api/connectors/:id — ui.connectorAPI. Replaces a connector's settings.
  update(id: string, body: ConnectorRequest): Promise<{ id: string }> {
    return request<{ id: string }>(`/api/connectors/${encodeURIComponent(id)}`, {
      method: "PUT",
      body: body as unknown as JsonBody,
    });
  },

  // DELETE /api/connectors/:id — ui.connectorAPI. Removes a connector (204).
  remove(id: string): Promise<void> {
    return request<void>(`/api/connectors/${encodeURIComponent(id)}`, { method: "DELETE" });
  },

  // POST /api/connectors/:id/rotate — ui.connectorAPI. Rotates the token; returns
  // the new URL + secret metadata.
  rotate(id: string): Promise<ConnectorSecretResponse> {
    return request<ConnectorSecretResponse>(`/api/connectors/${encodeURIComponent(id)}/rotate`, {
      method: "POST",
    });
  },
};

// --- Teams (ui/teams.go) -----------------------------------------------------

export const teams = {
  // GET /api/teams — ui.teamsAPI. Teams the user belongs to.
  list(): Promise<{ teams: Team[] }> {
    return request<{ teams: Team[] }>("/api/teams");
  },

  // POST /api/teams — ui.teamsAPI. Creates a team (caller becomes owner).
  create(body: CreateTeamRequest): Promise<{ team: Team }> {
    return request<{ team: Team }>("/api/teams", {
      method: "POST",
      body: body as unknown as JsonBody,
    });
  },

  // GET /api/teams/:id — ui.singleTeamAPI. One team with the caller's role.
  get(id: string): Promise<{ team: Team }> {
    return request<{ team: Team }>(`/api/teams/${encodeURIComponent(id)}`);
  },

  // PATCH /api/teams/:id — ui.singleTeamAPI. Renames/re-slugs a team.
  update(id: string, body: UpdateTeamRequest): Promise<{ team: Team }> {
    return request<{ team: Team }>(`/api/teams/${encodeURIComponent(id)}`, {
      method: "PATCH",
      body: body as unknown as JsonBody,
    });
  },

  // DELETE /api/teams/:id — ui.singleTeamAPI. Deletes a team (owner only).
  remove(id: string): Promise<OkResponse> {
    return request<OkResponse>(`/api/teams/${encodeURIComponent(id)}`, { method: "DELETE" });
  },

  members: {
    // GET /api/teams/:id/members — ui.teamMembersAPI. Lists team members.
    list(teamId: string): Promise<{ members: import("./types").TeamMember[] }> {
      return request<{ members: import("./types").TeamMember[] }>(
        `/api/teams/${encodeURIComponent(teamId)}/members`,
      );
    },

    // POST /api/teams/:id/members — ui.teamMembersAPI. Adds a member by user_id
    // or username.
    add(teamId: string, body: AddTeamMemberRequest): Promise<OkResponse> {
      return request<OkResponse>(`/api/teams/${encodeURIComponent(teamId)}/members`, {
        method: "POST",
        body: body as unknown as JsonBody,
      });
    },

    // PATCH /api/teams/:id/members/:memberId — ui.teamMembersAPI. Changes a
    // member's role.
    setRole(teamId: string, memberId: string, body: SetTeamMemberRoleRequest): Promise<OkResponse> {
      return request<OkResponse>(
        `/api/teams/${encodeURIComponent(teamId)}/members/${encodeURIComponent(memberId)}`,
        { method: "PATCH", body: body as unknown as JsonBody },
      );
    },

    // DELETE /api/teams/:id/members/:memberId — ui.teamMembersAPI. Removes a
    // member.
    remove(teamId: string, memberId: string): Promise<OkResponse> {
      return request<OkResponse>(
        `/api/teams/${encodeURIComponent(teamId)}/members/${encodeURIComponent(memberId)}`,
        { method: "DELETE" },
      );
    },
  },

  invites: {
    // GET /api/teams/:id/invites — ui.teamInvitesAPI. Lists a team's invites.
    list(teamId: string): Promise<{ invites: import("./types").TeamInvite[] }> {
      return request<{ invites: import("./types").TeamInvite[] }>(
        `/api/teams/${encodeURIComponent(teamId)}/invites`,
      );
    },

    // POST /api/teams/:id/invites — ui.teamInvitesAPI. Creates an invite link.
    create(teamId: string, body: CreateTeamInviteRequest): Promise<CreateTeamInviteResponse> {
      return request<CreateTeamInviteResponse>(
        `/api/teams/${encodeURIComponent(teamId)}/invites`,
        { method: "POST", body: body as unknown as JsonBody },
      );
    },

    // POST /api/teams/:id/invites/:inviteId/revoke — ui.teamInvitesAPI. Revokes
    // an invite.
    revoke(teamId: string, inviteId: string): Promise<OkResponse> {
      return request<OkResponse>(
        `/api/teams/${encodeURIComponent(teamId)}/invites/${encodeURIComponent(inviteId)}/revoke`,
        { method: "POST" },
      );
    },
  },
};

// --- Team invites by token (ui/teams.go: teamInviteAPI) ----------------------

export const invites = {
  // GET /api/team-invites/:token — ui.teamInviteAPI. Public preview of an invite.
  preview(token: string): Promise<{ invite: InvitePreview }> {
    return request<{ invite: InvitePreview }>(`/api/team-invites/${encodeURIComponent(token)}`);
  },

  // POST /api/team-invites/:token/accept — ui.teamInviteAPI. Accepts an invite
  // for the signed-in user; returns the joined team.
  accept(token: string): Promise<{ team: Team }> {
    return request<{ team: Team }>(`/api/team-invites/${encodeURIComponent(token)}/accept`, {
      method: "POST",
    });
  },
};

// --- Tasks (aggregate) (ui/tasks.go: tasksAllAPI) ----------------------------

export const tasks = {
  // GET /api/tasks — ui.tasksAllAPI. Tasks across every tasks-enabled directory
  // the user can view.
  all(): Promise<AllTasksResponse> {
    return request<AllTasksResponse>("/api/tasks");
  },
};

// --- Git connection check (ui/ui.go: gitCheckAPI) ----------------------------

export const git = {
  // POST /api/git/check — ui.gitCheckAPI. Non-destructive connection report.
  check(gitConfig: GitConfig): Promise<GitCheckReport> {
    return request<GitCheckReport>("/api/git/check", {
      method: "POST",
      body: { git: gitConfig },
    });
  },
};

// --- Filesystem browse (ui/ui.go: browseAPI) ---------------------------------

export const browse = {
  // GET /api/browse?path= — ui.browseAPI. Lists server-filesystem subdirectories
  // (local accounts / super admins only). Empty path starts at the home dir.
  list(path = ""): Promise<BrowseResponse> {
    return request<BrowseResponse>(`/api/browse${query({ path })}`);
  },
};

// --- Logs (ui/ui.go: logsAPI) ------------------------------------------------

export const logs = {
  // GET /api/logs?since= — ui.logsAPI. Log entries with id > since (pass -1, the
  // default, for everything). Users see their own activity; admins see all.
  since(seq = -1): Promise<LogsResponse> {
    return request<LogsResponse>(`/api/logs${query({ since: seq })}`);
  },
};

// --- User data export / import (ui/auth.go: userDataAPI) ---------------------

export const data = {
  // GET /api/data — ui.userDataAPI. Exports the user's directories + connectors.
  export(): Promise<UserDataBundle> {
    return request<UserDataBundle>("/api/data");
  },

  // POST /api/data?replace= — ui.userDataAPI. Imports a bundle. `replace` wipes
  // the user's existing directories/connectors first.
  import(bundle: UserDataBundle, replace = false): Promise<OkResponse> {
    return request<OkResponse>(`/api/data${replace ? query({ replace: 1 }) : ""}`, {
      method: "POST",
      body: bundle as unknown as JsonBody,
    });
  },
};

// --- Admin (super-admin only) ------------------------------------------------

export const admin = {
  // ui/auth.go: adminUsersAPI / adminUserAPI.
  users: {
    // GET /api/admin/users — ui.adminUsersAPI. Lists all accounts.
    list(): Promise<AdminUsersResponse> {
      return request<AdminUsersResponse>("/api/admin/users");
    },

    // POST /api/admin/users — ui.adminUsersAPI. Creates a local account.
    create(body: CreateAdminUserRequest): Promise<AdminUserResponse> {
      return request<AdminUserResponse>("/api/admin/users", {
        method: "POST",
        body: body as unknown as JsonBody,
      });
    },

    // PATCH /api/admin/users/:id — ui.adminUserAPI. Enables/disables an account.
    setDisabled(id: string, disabled: boolean): Promise<OkResponse> {
      const body: SetUserDisabledRequest = { disabled };
      return request<OkResponse>(`/api/admin/users/${encodeURIComponent(id)}`, {
        method: "PATCH",
        body: body as unknown as JsonBody,
      });
    },

    // POST /api/admin/users/:id/password — ui.adminUserAPI. Sets a new password.
    setPassword(id: string, password: string): Promise<OkResponse> {
      const body: SetUserPasswordRequest = { password };
      return request<OkResponse>(`/api/admin/users/${encodeURIComponent(id)}/password`, {
        method: "POST",
        body: body as unknown as JsonBody,
      });
    },

    // POST /api/admin/users/:id/unlink-oidc — ui.adminUserAPI. Detaches the SSO
    // identity from an account.
    unlinkOidc(id: string): Promise<OkResponse> {
      return request<OkResponse>(`/api/admin/users/${encodeURIComponent(id)}/unlink-oidc`, {
        method: "POST",
      });
    },
  },

  // ui/admin_oidc.go: adminOIDCAPI / adminOIDCRelinkAPI.
  oidc: {
    // GET /api/admin/oidc — ui.adminOIDCAPI. Current OIDC settings (no secret).
    get(): Promise<OIDCConfigResponse> {
      return request<OIDCConfigResponse>("/api/admin/oidc");
    },

    // PUT /api/admin/oidc — ui.adminOIDCAPI (updateOIDC). Saves settings;
    // validated against the IdP before persisting when enabled.
    save(body: SaveOIDCRequest): Promise<OIDCConfigResponse> {
      return request<OIDCConfigResponse>("/api/admin/oidc", {
        method: "PUT",
        body: body as unknown as JsonBody,
      });
    },

    // POST /api/admin/oidc/relink — ui.adminOIDCRelinkAPI. Adopts orphaned SSO
    // users (from a prior issuer) into the current provider.
    relink(body: OIDCRelinkRequest): Promise<OIDCRelinkResponse> {
      return request<OIDCRelinkResponse>("/api/admin/oidc/relink", {
        method: "POST",
        body: body as unknown as JsonBody,
      });
    },
  },

  // ui/admin_doctrine.go: adminDoctrinesAPI / adminDoctrineAPI.
  doctrines: {
    // GET /api/admin/doctrines — ui.adminDoctrinesAPI. Lists live doctrines.
    list(): Promise<DoctrinesResponse> {
      return request<DoctrinesResponse>("/api/admin/doctrines");
    },

    // GET /api/admin/doctrines — ui.adminDoctrinesAPI. Convenience: fetch one
    // doctrine by id from the list (the backend exposes no per-id GET).
    async get(id: string): Promise<Doctrine | undefined> {
      const { doctrines } = await request<DoctrinesResponse>("/api/admin/doctrines");
      return doctrines.find((d) => d.id === id);
    },

    // PUT /api/admin/doctrines/:id — ui.adminDoctrineAPI. Overrides a doctrine's
    // text (in-memory; reverts on restart).
    save(id: string, text: string): Promise<DoctrineMutateResponse> {
      const body: SaveDoctrineRequest = { text };
      return request<DoctrineMutateResponse>(`/api/admin/doctrines/${encodeURIComponent(id)}`, {
        method: "PUT",
        body: body as unknown as JsonBody,
      });
    },

    // DELETE /api/admin/doctrines/:id — ui.adminDoctrineAPI. Resets a doctrine
    // to its compiled default.
    reset(id: string): Promise<DoctrineMutateResponse> {
      return request<DoctrineMutateResponse>(`/api/admin/doctrines/${encodeURIComponent(id)}`, {
        method: "DELETE",
      });
    },
  },
};
