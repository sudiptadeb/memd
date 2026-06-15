package ui

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/sudiptadeb/memd/server/internal/account"
	"github.com/sudiptadeb/memd/server/internal/config"
	"github.com/sudiptadeb/memd/server/internal/doctrine"
	"github.com/sudiptadeb/memd/server/internal/feature"
	"github.com/sudiptadeb/memd/server/internal/logs"
	"github.com/sudiptadeb/memd/server/internal/oidc"
	"github.com/sudiptadeb/memd/server/internal/registry"
	"github.com/sudiptadeb/memd/server/internal/storage"
)

// The web UI is two independently-built Vue apps under dist/<app> (produced by
// build/build.sh → web/ vite build). `all:` is required: Vite emits some chunks
// whose names start with "_", which a plain embed would skip.
//
//go:embed all:dist
var distFS embed.FS

// Handler is the web UI handler.
type Handler struct {
	reg      *registry.Registry
	accounts *account.Store
	sessions *SessionManager
	oidc     *oidc.Manager
	live     *doctrine.Live // live-editable doctrines (super-admin only)
	features *feature.Registry
	baseURL  string
}

// New builds the web UI handler. sessions carries the cookie-sealing key, oidc
// is the runtime-swappable IdP provider (may be disabled), and live is the
// in-memory doctrine store a super admin can edit at runtime.
func New(reg *registry.Registry, accounts *account.Store, baseURL string, sessions *SessionManager, oidcMgr *oidc.Manager, live *doctrine.Live) *Handler {
	if oidcMgr == nil {
		oidcMgr = oidc.NewManager()
	}
	if live == nil {
		live = doctrine.NewLive()
	}
	return &Handler{
		reg:      reg,
		accounts: accounts,
		sessions: sessions,
		oidc:     oidcMgr,
		live:     live,
		features: feature.Builtins(),
		baseURL:  baseURL,
	}
}

func (h *Handler) Mount(mux *http.ServeMux) {
	// Each app is self-contained under dist/<app>: the dashboard is served at the
	// root, the admin console under /admin/. Content-hashed bundles are immutable;
	// the two app shells revalidate so a deploy is picked up at once.
	dashFS := mustSub(distFS, "dist/dashboard")
	adminFS := mustSub(distFS, "dist/admin")
	mux.Handle("/assets/", immutable(http.FileServer(http.FS(dashFS))))
	mux.Handle("/admin/assets/", immutable(http.StripPrefix("/admin", http.FileServer(http.FS(adminFS)))))
	// History-mode SPA fallbacks: any non-asset, non-API path serves the app's
	// index.html so deep links and reloads boot the SPA, which then client-routes.
	mux.HandleFunc("/admin", spaPage(mustReadFile(adminFS, "index.html")))
	mux.HandleFunc("/admin/", spaPage(mustReadFile(adminFS, "index.html")))
	mux.HandleFunc("/", spaPage(mustReadFile(dashFS, "index.html")))
	mux.HandleFunc("/api/session", h.sessionAPI)
	mux.HandleFunc("/api/auth/login", h.loginAPI)
	mux.HandleFunc("/api/auth/logout", h.logoutAPI)
	mux.HandleFunc("/auth/login", h.oidcLogin)
	mux.HandleFunc("/auth/callback", h.oidcCallback)
	mux.HandleFunc("/api/data", h.requireUser(h.userDataAPI))
	mux.HandleFunc("/api/admin/users", h.requireSuperAdmin(h.adminUsersAPI))
	mux.HandleFunc("/api/admin/users/", h.requireSuperAdmin(h.adminUserAPI))
	mux.HandleFunc("/api/admin/oidc", h.requireSuperAdmin(h.adminOIDCAPI))
	mux.HandleFunc("/api/admin/oidc/relink", h.requireSuperAdmin(h.adminOIDCRelinkAPI))
	mux.HandleFunc("/api/admin/doctrines", h.requireSuperAdmin(h.adminDoctrinesAPI))
	mux.HandleFunc("/api/admin/doctrines/", h.requireSuperAdmin(h.adminDoctrineAPI))
	mux.HandleFunc("/api/teams", h.requireUser(h.teamsAPI))
	mux.HandleFunc("/api/teams/", h.requireUser(h.teamAPI))
	mux.HandleFunc("/api/team-invites/", h.teamInviteAPI)
	mux.HandleFunc("/api/directories", h.requireUser(h.directoriesAPI))
	mux.HandleFunc("/api/directories/", h.requireUser(h.directoryAPI))
	mux.HandleFunc("/api/tasks", h.requireUser(h.tasksAllAPI))
	mux.HandleFunc("/api/git/check", h.requireUser(h.gitCheckAPI))
	mux.HandleFunc("/api/connectors", h.requireUser(h.connectorsAPI))
	mux.HandleFunc("/api/connectors/", h.requireUser(h.connectorAPI))
	mux.HandleFunc("/api/browse", h.requireUser(h.browseAPI))
	mux.HandleFunc("/api/logs", h.requireUser(h.logsAPI))
}

// immutable marks content-hashed bundles as long-lived: the hash in the filename
// is the cache-buster, so they never need revalidation.
func immutable(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		next.ServeHTTP(w, r)
	})
}

// spaPage serves an app's index.html for any GET/HEAD under its route prefix, so
// history-mode deep links and reloads boot the SPA (which then client-routes).
// The HTML revalidates (no-cache) so a deploy's new asset hashes are seen at once.
func spaPage(indexHTML []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(indexHTML)
	}
}

// mustSub / mustReadFile fail fast at startup if the embedded dist is malformed.
// build/build.sh always builds the apps before the binary, so a failure here
// means a broken build that should not start serving.
func mustSub(f embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(f, dir)
	if err != nil {
		panic("ui: embedded dist missing " + dir + ": " + err.Error())
	}
	return sub
}

func mustReadFile(fsys fs.FS, name string) []byte {
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		panic("ui: embedded dist missing file " + name + ": " + err.Error())
	}
	return b
}

type pageData struct {
	Directories []directoryView
	Connectors  []connectorView
}

type directoryView struct {
	ID               string `json:"id"`
	OwnerUserID      string `json:"owner_user_id,omitempty"`
	TeamID           string `json:"team_id,omitempty"`
	TeamName         string `json:"team_name,omitempty"`
	OwnerConnectorID string `json:"owner_connector_id,omitempty"`
	Name             string `json:"name"`
	Description      string `json:"description,omitempty"`
	Backend          string `json:"backend"`
	Detail           string `json:"detail"`
	Error            string `json:"error,omitempty"`
	Owned            bool   `json:"owned"`
	CanManage        bool   `json:"can_manage"`
	CanAttach        bool   `json:"can_attach"`

	Features []featureToggle `json:"features"`
}

// featureToggle is the per-directory state of one built-in feature for the UI.
type featureToggle struct {
	Key        string `json:"key"`
	Name       string `json:"name"`
	Enabled    bool   `json:"enabled"`
	ComingSoon bool   `json:"coming_soon"`
}

type connectorView struct {
	ID             string   `json:"id"`
	OwnerUserID    string   `json:"owner_user_id,omitempty"`
	TeamID         string   `json:"team_id,omitempty"`
	TeamName       string   `json:"team_name,omitempty"`
	Name           string   `json:"name"`
	Kind           string   `json:"kind"`
	URL            string   `json:"url"`
	AuthURL        string   `json:"auth_url"`
	AuthHeader     string   `json:"auth_header"`
	Write          bool     `json:"write"`
	DirectoryIDs   []string `json:"directory_ids"`
	DirectoryNames string   `json:"directory_names"`
	Owned          bool     `json:"owned"`
	CanManage      bool     `json:"can_manage"`
}

func (h *Handler) pageData(ownerUserID string) pageData {
	teams, _ := h.accounts.ListTeamsForUser(context.Background(), ownerUserID)
	teamNameByID := make(map[string]string, len(teams))
	manageableTeam := make(map[string]bool, len(teams))
	writableTeam := make(map[string]bool, len(teams))
	for _, team := range teams {
		teamNameByID[team.ID] = team.Name
		switch team.Role {
		case account.RoleOwner, account.RoleAdmin:
			manageableTeam[team.ID] = true
			writableTeam[team.ID] = true
		case account.RoleMember:
			writableTeam[team.ID] = true
		}
	}
	dirs := h.reg.DirectoriesForUser(ownerUserID)
	dirNameByID := make(map[string]string, len(dirs))
	dirViews := make([]directoryView, 0, len(dirs))
	for _, d := range dirs {
		dirNameByID[d.ID] = d.Name
		detail := d.LocalPath
		errMsg := ""
		if d.Backend == "git" && d.Git != nil {
			detail = fmt.Sprintf("%s @ %s : %s", config.RedactGitRemoteURL(d.Git.RemoteURL), d.Git.Branch, d.Git.BasePath)
		} else if d.Backend == "local" {
			if info, err := os.Stat(d.LocalPath); err != nil {
				errMsg = err.Error()
			} else if !info.IsDir() {
				errMsg = "not a directory"
			}
		}
		dirViews = append(dirViews, directoryView{
			ID:               d.ID,
			OwnerUserID:      d.OwnerUserID,
			TeamID:           d.TeamID,
			TeamName:         teamNameByID[d.TeamID],
			OwnerConnectorID: d.OwnerConnectorID,
			Name:             d.Name,
			Description:      d.Description,
			Backend:          d.Backend,
			Detail:           detail,
			Error:            errMsg,
			Owned:            d.OwnerUserID == ownerUserID,
			CanManage:        d.OwnerUserID == ownerUserID || (d.TeamID != "" && manageableTeam[d.TeamID]),
			CanAttach:        d.OwnerUserID == ownerUserID || (d.TeamID != "" && writableTeam[d.TeamID]),
			Features:         h.featureToggles(d),
		})
	}
	cs := h.reg.ConnectorsForUser(ownerUserID)
	cViews := make([]connectorView, 0, len(cs))
	for _, c := range cs {
		names := ""
		for i, id := range c.DirectoryIDs {
			if i > 0 {
				names += ", "
			}
			if n, ok := dirNameByID[id]; ok {
				names += n
			} else {
				names += "(missing)"
			}
		}
		if names == "" {
			names = "(none)"
		}
		ids := c.DirectoryIDs
		if ids == nil {
			ids = []string{}
		}
		kind := c.EffectiveKind()
		url := h.connectorURL(c)
		authURL := h.connectorAuthURL(c)
		cViews = append(cViews, connectorView{
			ID:             c.ID,
			OwnerUserID:    c.OwnerUserID,
			TeamID:         c.TeamID,
			TeamName:       teamNameByID[c.TeamID],
			Name:           c.Name,
			Kind:           kind,
			URL:            url,
			AuthURL:        authURL,
			AuthHeader:     "Authorization: Bearer " + c.Token,
			Write:          c.Write,
			DirectoryIDs:   ids,
			DirectoryNames: names,
			Owned:          c.OwnerUserID == ownerUserID,
			CanManage:      c.OwnerUserID == ownerUserID || (c.TeamID != "" && manageableTeam[c.TeamID]),
		})
	}
	return pageData{Directories: dirViews, Connectors: cViews}
}

// featureToggles returns the per-directory state of every built-in feature: its
// key, label, whether it is enabled on this directory, and whether it is still
// coming-soon (registered but not yet usable).
func (h *Handler) featureToggles(d config.Directory) []featureToggle {
	enabled := make(map[string]bool, len(d.Features))
	for _, df := range d.Features {
		enabled[df.Key] = df.Enabled
	}
	var out []featureToggle
	for _, f := range h.features.List() {
		out = append(out, featureToggle{
			Key:        f.Key,
			Name:       f.Name,
			Enabled:    enabled[f.Key],
			ComingSoon: f.ComingSoon,
		})
	}
	return out
}

// --- Directory API ---

func (h *Handler) directoriesAPI(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		httpErr(w, http.StatusUnauthorized, fmt.Errorf("not authenticated"))
		return
	}
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]any{"directories": h.pageData(user.ID).Directories})
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Name        string      `json:"name"`
		TeamID      string      `json:"team_id"`
		Description string      `json:"description"`
		Backend     string      `json:"backend"`
		LocalPath   string      `json:"local_path"`
		Git         *config.Git `json:"git"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpErr(w, http.StatusBadRequest, err)
		return
	}
	if body.Name == "" || body.Backend == "" {
		httpErr(w, http.StatusBadRequest, fmt.Errorf("name and backend are required"))
		return
	}
	normalizeGitDirectoryAuth(body.Git)
	// Only local accounts may choose a local directory path. OIDC-provisioned
	// users get a name-only directory that memd sandboxes under a managed root.
	allowCustomLocalPath := user.Issuer == ""
	id, err := h.reg.AddDirectoryForUserManaged(user.ID, config.Directory{
		Name:        body.Name,
		TeamID:      body.TeamID,
		Description: body.Description,
		Backend:     body.Backend,
		LocalPath:   body.LocalPath,
		Git:         body.Git,
	}, allowCustomLocalPath)
	if err != nil {
		logs.ErrorUser(user.ID, "add directory %q failed: %v", body.Name, err)
		httpErr(w, http.StatusBadRequest, err)
		return
	}
	logs.InfoUser(user.ID, "added directory %q (id=%s, backend=%s)", body.Name, id, body.Backend)
	writeJSON(w, http.StatusOK, map[string]string{"id": id})
}

func (h *Handler) gitCheckAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Git *config.Git `json:"git"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpErr(w, http.StatusBadRequest, err)
		return
	}
	if body.Git == nil || strings.TrimSpace(body.Git.RemoteURL) == "" {
		httpErr(w, http.StatusBadRequest, fmt.Errorf("git remote URL is required"))
		return
	}
	normalizeGitDirectoryAuth(body.Git)
	report := storage.CheckGitConnection(storage.GitConfig{
		RemoteURL:    body.Git.RemoteURL,
		Branch:       body.Git.Branch,
		BasePath:     body.Git.BasePath,
		AuthorName:   body.Git.AuthorName,
		AuthorEmail:  body.Git.AuthorEmail,
		AuthUsername: body.Git.AuthUsername,
		AuthToken:    body.Git.AuthToken,
		SSHKeyPath:   body.Git.SSHKeyPath,
	})
	writeJSON(w, http.StatusOK, report)
}

func normalizeGitDirectoryAuth(g *config.Git) {
	if g == nil {
		return
	}
	clean, username, token := splitGitRemoteAuth(g.RemoteURL)
	g.RemoteURL = clean
	if g.AuthUsername == "" {
		g.AuthUsername = username
	}
	if g.AuthToken == "" {
		g.AuthToken = token
	}
}

func splitGitRemoteAuth(raw string) (clean, username, token string) {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw, "", ""
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return raw, "", ""
	}
	username = u.User.Username()
	token, _ = u.User.Password()
	u.User = nil
	return u.String(), username, token
}

func (h *Handler) directoryAPI(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		httpErr(w, http.StatusUnauthorized, fmt.Errorf("not authenticated"))
		return
	}
	tail := r.URL.Path[len("/api/directories/"):]
	id, action, _ := strings.Cut(tail, "/")
	// The tasks dashboard is the one sub-resource that both reads (GET) and
	// mutates (POST), so it is dispatched before the GET-only switch below.
	if action == "tasks" {
		h.directoryTasksAPI(w, r, user, id)
		return
	}
	if r.Method == http.MethodGet {
		switch action {
		case "files":
			h.directoryFilesAPI(w, r, user, id)
		case "raw":
			h.directoryRawAPI(w, r, user, id)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}
	if action != "" {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodDelete:
		if err := h.reg.DeleteDirectoryForActor(user.ID, id); err != nil {
			httpErr(w, http.StatusBadRequest, err)
			return
		}
		logs.InfoUser(user.ID, "deleted directory id=%s", id)
		w.WriteHeader(http.StatusNoContent)
	case http.MethodPatch:
		// Pointer fields distinguish "absent" from "set to empty": a PATCH
		// only changes the fields it carries.
		var body struct {
			Name             *string `json:"name"`
			Description      *string `json:"description"`
			TeamID           *string `json:"team_id"`
			OwnerConnectorID *string `json:"owner_connector_id"`
			Feature          *struct {
				Key     string `json:"key"`
				Enabled bool   `json:"enabled"`
			} `json:"feature"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httpErr(w, http.StatusBadRequest, err)
			return
		}
		var d config.Directory
		var err error
		if body.Name != nil || body.Description != nil {
			d, err = h.reg.UpdateDirectoryDetailsForActor(user.ID, id, body.Name, body.Description)
			if err != nil {
				httpErr(w, statusForAccountError(err), err)
				return
			}
			logs.InfoUser(user.ID, "updated directory details id=%s name=%q", id, d.Name)
		}
		if body.TeamID != nil {
			d, err = h.reg.UpdateDirectoryTeamForActor(user.ID, id, *body.TeamID)
			if err != nil {
				httpErr(w, statusForAccountError(err), err)
				return
			}
			logs.InfoUser(user.ID, "updated directory team scope id=%s team=%s", id, d.TeamID)
		}
		if body.OwnerConnectorID != nil {
			d, err = h.reg.UpdateDirectoryOwnerConnectorForActor(user.ID, id, *body.OwnerConnectorID)
			if err != nil {
				httpErr(w, statusForAccountError(err), err)
				return
			}
			logs.InfoUser(user.ID, "updated directory owner connector id=%s connector=%s", id, d.OwnerConnectorID)
		}
		if body.Feature != nil {
			d, err = h.reg.SetDirectoryFeatureForActor(user.ID, id, body.Feature.Key, body.Feature.Enabled)
			if err != nil {
				httpErr(w, statusForAccountError(err), err)
				return
			}
			logs.InfoUser(user.ID, "set directory feature id=%s feature=%s enabled=%v", id, body.Feature.Key, body.Feature.Enabled)
		}
		if body.Name == nil && body.Description == nil && body.TeamID == nil && body.OwnerConnectorID == nil && body.Feature == nil {
			httpErr(w, http.StatusBadRequest, fmt.Errorf("nothing to update"))
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"id": d.ID, "name": d.Name, "description": d.Description, "team_id": d.TeamID, "owner_connector_id": d.OwnerConnectorID})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// --- Connector API ---

func (h *Handler) connectorsAPI(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		httpErr(w, http.StatusUnauthorized, fmt.Errorf("not authenticated"))
		return
	}
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]any{"connectors": h.pageData(user.ID).Connectors})
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Name         string   `json:"name"`
		TeamID       string   `json:"team_id"`
		Kind         string   `json:"kind"`
		DirectoryIDs []string `json:"directory_ids"`
		Write        bool     `json:"write"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpErr(w, http.StatusBadRequest, err)
		return
	}
	if body.Name == "" || len(body.DirectoryIDs) == 0 {
		httpErr(w, http.StatusBadRequest, fmt.Errorf("name and at least one directory are required"))
		return
	}
	c, err := h.reg.AddConnectorForUser(user.ID, config.Connector{
		Name:         body.Name,
		TeamID:       body.TeamID,
		Kind:         body.Kind,
		DirectoryIDs: body.DirectoryIDs,
		Write:        body.Write,
	})
	if err != nil {
		logs.ErrorUser(user.ID, "add connector %q failed: %v", body.Name, err)
		httpErr(w, statusForAccountError(err), err)
		return
	}
	logs.InfoUser(user.ID, "added connector %q (id=%s, kind=%s, %d directories, write=%v)", body.Name, c.ID, c.EffectiveKind(), len(body.DirectoryIDs), body.Write)
	writeJSON(w, http.StatusOK, map[string]string{
		"id":        c.ID,
		"url":       h.connectorURL(c),
		"auth_url":  h.connectorAuthURL(c),
		"auth_type": "bearer",
	})
}

func (h *Handler) connectorAPI(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		httpErr(w, http.StatusUnauthorized, fmt.Errorf("not authenticated"))
		return
	}
	tail := r.URL.Path[len("/api/connectors/"):]
	id, action, _ := strings.Cut(tail, "/")
	switch {
	case action == "" && r.Method == http.MethodDelete:
		if err := h.reg.DeleteConnectorForActor(user.ID, id); err != nil {
			logs.ErrorUser(user.ID, "delete connector id=%s failed: %v", id, err)
			httpErr(w, statusForAccountError(err), err)
			return
		}
		logs.InfoUser(user.ID, "deleted connector id=%s", id)
		w.WriteHeader(http.StatusNoContent)
	case action == "" && r.Method == http.MethodPut:
		var body struct {
			Name         string   `json:"name"`
			TeamID       string   `json:"team_id"`
			Kind         string   `json:"kind"`
			DirectoryIDs []string `json:"directory_ids"`
			Write        bool     `json:"write"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httpErr(w, http.StatusBadRequest, err)
			return
		}
		c, err := h.reg.UpdateConnectorForActor(user.ID, id, body.Name, body.Kind, body.DirectoryIDs, body.Write, body.TeamID)
		if err != nil {
			logs.ErrorUser(user.ID, "update connector id=%s failed: %v", id, err)
			httpErr(w, statusForAccountError(err), err)
			return
		}
		logs.InfoUser(user.ID, "updated connector %q (id=%s, kind=%s, %d directories, write=%v)", c.Name, id, c.EffectiveKind(), len(c.DirectoryIDs), c.Write)
		writeJSON(w, http.StatusOK, map[string]string{"id": c.ID})
	case action == "rotate" && r.Method == http.MethodPost:
		c, err := h.reg.RotateConnectorForActor(user.ID, id)
		if err != nil {
			httpErr(w, http.StatusBadRequest, err)
			return
		}
		logs.InfoUser(user.ID, "rotated connector %q (id=%s)", c.Name, id)
		writeJSON(w, http.StatusOK, map[string]string{
			"id":        c.ID,
			"url":       h.connectorURL(c),
			"auth_url":  h.connectorAuthURL(c),
			"auth_type": "bearer",
		})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *Handler) connectorURL(c config.Connector) string {
	switch c.EffectiveKind() {
	case config.ConnectorKindHTTP:
		return fmt.Sprintf("%s/http/%s", h.baseURL, c.Token)
	default:
		return fmt.Sprintf("%s/mcp/%s", h.baseURL, c.Token)
	}
}

func (h *Handler) connectorAuthURL(c config.Connector) string {
	switch c.EffectiveKind() {
	case config.ConnectorKindHTTP:
		return h.baseURL + "/http"
	default:
		return h.baseURL + "/mcp"
	}
}

// --- Filesystem browse + logs API ---

func (h *Handler) browseAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	// The filesystem browser is a local-deployment convenience for choosing a
	// directory path. Only local (password) accounts and super admins — who can
	// already specify a path — may enumerate the server's filesystem.
	user := userFromContext(r.Context())
	if user == nil || (user.Issuer != "" && !user.SuperAdmin) {
		httpErr(w, http.StatusForbidden, fmt.Errorf("filesystem browsing is only available to local accounts"))
		return
	}
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			httpErr(w, http.StatusInternalServerError, err)
			return
		}
		path = home
	}
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		httpErr(w, http.StatusBadRequest, err)
		return
	}
	info, err := os.Stat(abs)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err)
		return
	}
	if !info.IsDir() {
		httpErr(w, http.StatusBadRequest, fmt.Errorf("not a directory: %s", abs))
		return
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err)
		return
	}
	type dirEntry struct {
		Name string `json:"name"`
	}
	dirs := make([]dirEntry, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		dirs = append(dirs, dirEntry{Name: name})
	}
	sort.Slice(dirs, func(i, j int) bool { return strings.ToLower(dirs[i].Name) < strings.ToLower(dirs[j].Name) })
	parent := filepath.Dir(abs)
	if parent == abs {
		parent = ""
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":   abs,
		"parent": parent,
		"dirs":   dirs,
	})
}

func (h *Handler) logsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	user := userFromContext(r.Context())
	if user == nil {
		httpErr(w, http.StatusUnauthorized, fmt.Errorf("not authenticated"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	since := int64(-1)
	if s := r.URL.Query().Get("since"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			since = v
		}
	}
	// Regular users see only their own activity; super admins see everything.
	entries := logs.SinceForViewer(since, user.ID, user.SuperAdmin)
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

func httpErr(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
