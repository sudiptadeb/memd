package ui

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/sudiptadeb/memd/server/internal/account"
	"github.com/sudiptadeb/memd/server/internal/config"
	"github.com/sudiptadeb/memd/server/internal/logs"
	"github.com/sudiptadeb/memd/server/internal/oidc"
	"github.com/sudiptadeb/memd/server/internal/registry"
)

//go:embed assets/*
var fsys embed.FS

// Handler is the web UI handler.
type Handler struct {
	reg      *registry.Registry
	accounts *account.Store
	sessions *SessionManager
	oidc     *oidc.Manager
	baseURL  string
}

// New builds the web UI handler. sessions carries the cookie-sealing key and
// oidc is the runtime-swappable IdP provider (may be disabled).
func New(reg *registry.Registry, accounts *account.Store, baseURL string, sessions *SessionManager, oidcMgr *oidc.Manager) *Handler {
	if oidcMgr == nil {
		oidcMgr = oidc.NewManager()
	}
	return &Handler{
		reg:      reg,
		accounts: accounts,
		sessions: sessions,
		oidc:     oidcMgr,
		baseURL:  baseURL,
	}
}

func (h *Handler) Mount(mux *http.ServeMux) {
	mux.HandleFunc("/", h.index)
	mux.HandleFunc("/admin", h.adminIndex)
	mux.Handle("/assets/", http.FileServer(http.FS(fsys)))
	mux.HandleFunc("/api/session", h.sessionAPI)
	mux.HandleFunc("/api/auth/login", h.loginAPI)
	mux.HandleFunc("/api/auth/logout", h.logoutAPI)
	mux.HandleFunc("/auth/login", h.oidcLogin)
	mux.HandleFunc("/auth/callback", h.oidcCallback)
	mux.HandleFunc("/api/data", h.requireUser(h.userDataAPI))
	mux.HandleFunc("/api/admin/users", h.requireSuperAdmin(h.adminUsersAPI))
	mux.HandleFunc("/api/admin/users/", h.requireSuperAdmin(h.adminUserAPI))
	mux.HandleFunc("/api/admin/oidc", h.requireSuperAdmin(h.adminOIDCAPI))
	mux.HandleFunc("/api/teams", h.requireUser(h.teamsAPI))
	mux.HandleFunc("/api/teams/", h.requireUser(h.teamAPI))
	mux.HandleFunc("/api/team-invites/", h.teamInviteAPI)
	mux.HandleFunc("/api/directories", h.requireUser(h.directoriesAPI))
	mux.HandleFunc("/api/directories/", h.requireUser(h.directoryAPI))
	mux.HandleFunc("/api/connectors", h.requireUser(h.connectorsAPI))
	mux.HandleFunc("/api/connectors/", h.requireUser(h.connectorAPI))
	mux.HandleFunc("/api/browse", h.requireUser(h.browseAPI))
	mux.HandleFunc("/api/logs", h.requireUser(h.logsAPI))
}

type pageData struct {
	Directories []directoryView
	Connectors  []connectorView
}

type directoryView struct {
	ID          string `json:"id"`
	OwnerUserID string `json:"owner_user_id,omitempty"`
	TeamID      string `json:"team_id,omitempty"`
	TeamName    string `json:"team_name,omitempty"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Backend     string `json:"backend"`
	Detail      string `json:"detail"`
	Error       string `json:"error,omitempty"`
	CanManage   bool   `json:"can_manage"`
	CanAttach   bool   `json:"can_attach"`
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
	CanManage      bool     `json:"can_manage"`
}

func (h *Handler) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	b, err := fsys.ReadFile("assets/index.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b)
}

func (h *Handler) adminIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/admin" {
		http.NotFound(w, r)
		return
	}
	b, err := fsys.ReadFile("assets/admin.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b)
}

func (h *Handler) pageData(ownerUserID string) pageData {
	teams, _ := h.accounts.ListTeamsForUser(context.Background(), ownerUserID)
	teamNameByID := make(map[string]string, len(teams))
	manageableTeam := make(map[string]bool, len(teams))
	for _, team := range teams {
		teamNameByID[team.ID] = team.Name
		if team.Role == account.RoleOwner || team.Role == account.RoleAdmin {
			manageableTeam[team.ID] = true
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
			ID:          d.ID,
			OwnerUserID: d.OwnerUserID,
			TeamID:      d.TeamID,
			TeamName:    teamNameByID[d.TeamID],
			Name:        d.Name,
			Description: d.Description,
			Backend:     d.Backend,
			Detail:      detail,
			Error:       errMsg,
			CanManage:   d.OwnerUserID == ownerUserID || (d.TeamID != "" && manageableTeam[d.TeamID]),
			CanAttach:   d.OwnerUserID == ownerUserID,
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
			CanManage:      c.OwnerUserID == ownerUserID || (c.TeamID != "" && manageableTeam[c.TeamID]),
		})
	}
	return pageData{Directories: dirViews, Connectors: cViews}
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
	id, err := h.reg.AddDirectoryForUser(user.ID, config.Directory{
		Name:        body.Name,
		TeamID:      body.TeamID,
		Description: body.Description,
		Backend:     body.Backend,
		LocalPath:   body.LocalPath,
		Git:         body.Git,
	})
	if err != nil {
		logs.Error("add directory %q failed: %v", body.Name, err)
		httpErr(w, http.StatusBadRequest, err)
		return
	}
	logs.Info("added directory %q (id=%s, backend=%s)", body.Name, id, body.Backend)
	writeJSON(w, http.StatusOK, map[string]string{"id": id})
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
	id := r.URL.Path[len("/api/directories/"):]
	switch r.Method {
	case http.MethodDelete:
		if err := h.reg.DeleteDirectoryForActor(user.ID, id); err != nil {
			httpErr(w, http.StatusBadRequest, err)
			return
		}
		logs.Info("deleted directory id=%s", id)
		w.WriteHeader(http.StatusNoContent)
	case http.MethodPatch:
		var body struct {
			TeamID string `json:"team_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httpErr(w, http.StatusBadRequest, err)
			return
		}
		d, err := h.reg.UpdateDirectoryTeamForActor(user.ID, id, body.TeamID)
		if err != nil {
			httpErr(w, statusForAccountError(err), err)
			return
		}
		logs.Info("updated directory team scope id=%s team=%s", id, d.TeamID)
		writeJSON(w, http.StatusOK, map[string]string{"id": d.ID, "team_id": d.TeamID})
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
		logs.Error("add connector %q failed: %v", body.Name, err)
		httpErr(w, http.StatusBadRequest, err)
		return
	}
	logs.Info("added connector %q (id=%s, kind=%s, %d directories, write=%v)", body.Name, c.ID, c.EffectiveKind(), len(body.DirectoryIDs), body.Write)
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
			httpErr(w, http.StatusBadRequest, err)
			return
		}
		logs.Info("deleted connector id=%s", id)
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
			httpErr(w, http.StatusBadRequest, err)
			return
		}
		logs.Info("updated connector %q (id=%s, kind=%s, %d directories, write=%v)", c.Name, id, c.EffectiveKind(), len(c.DirectoryIDs), c.Write)
		writeJSON(w, http.StatusOK, map[string]string{"id": c.ID})
	case action == "rotate" && r.Method == http.MethodPost:
		c, err := h.reg.RotateConnectorForActor(user.ID, id)
		if err != nil {
			httpErr(w, http.StatusBadRequest, err)
			return
		}
		logs.Info("rotated connector %q (id=%s)", c.Name, id)
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
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	since := int64(-1)
	if s := r.URL.Query().Get("since"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			since = v
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": logs.Since(since)})
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
