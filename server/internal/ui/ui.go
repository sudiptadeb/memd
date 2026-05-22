package ui

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/sudiptadeb/memd/server/internal/config"
	"github.com/sudiptadeb/memd/server/internal/logs"
	"github.com/sudiptadeb/memd/server/internal/registry"
)

//go:embed templates/*.html assets/*
var fsys embed.FS

var tmpl = template.Must(template.New("").ParseFS(fsys, "templates/*.html"))

// Handler is the web UI handler.
type Handler struct {
	reg     *registry.Registry
	baseURL string
}

func New(reg *registry.Registry, baseURL string) *Handler {
	return &Handler{reg: reg, baseURL: baseURL}
}

func (h *Handler) Mount(mux *http.ServeMux) {
	mux.HandleFunc("/", h.index)
	mux.Handle("/assets/", http.FileServer(http.FS(fsys)))
	mux.HandleFunc("/api/directories", h.directoriesAPI)
	mux.HandleFunc("/api/directories/", h.directoryAPI)
	mux.HandleFunc("/api/connectors", h.connectorsAPI)
	mux.HandleFunc("/api/connectors/", h.connectorAPI)
	mux.HandleFunc("/api/browse", h.browseAPI)
	mux.HandleFunc("/api/logs", h.logsAPI)
}

type pageData struct {
	Directories []directoryView
	Connectors  []connectorView
}

type directoryView struct {
	ID          string
	Name        string
	Description string
	Backend     string
	Detail      string
}

type connectorView struct {
	ID             string
	Name           string
	URL            string
	Write          bool
	DirectoryNames string
}

func (h *Handler) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if err := tmpl.ExecuteTemplate(w, "index.html", h.pageData()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *Handler) pageData() pageData {
	dirs := h.reg.Directories()
	dirNameByID := make(map[string]string, len(dirs))
	dirViews := make([]directoryView, 0, len(dirs))
	for _, d := range dirs {
		dirNameByID[d.ID] = d.Name
		detail := d.LocalPath
		if d.Backend == "git" && d.Git != nil {
			detail = fmt.Sprintf("%s @ %s : %s", d.Git.RemoteURL, d.Git.Branch, d.Git.BasePath)
		}
		dirViews = append(dirViews, directoryView{
			ID:          d.ID,
			Name:        d.Name,
			Description: d.Description,
			Backend:     d.Backend,
			Detail:      detail,
		})
	}
	cs := h.reg.Connectors()
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
		cViews = append(cViews, connectorView{
			ID:             c.ID,
			Name:           c.Name,
			URL:            fmt.Sprintf("%s/mcp/%s", h.baseURL, c.Token),
			Write:          c.Write,
			DirectoryNames: names,
		})
	}
	return pageData{Directories: dirViews, Connectors: cViews}
}

// --- Directory API ---

func (h *Handler) directoriesAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Name        string      `json:"name"`
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
	id, err := h.reg.AddDirectory(config.Directory{
		Name:        body.Name,
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

func (h *Handler) directoryAPI(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Path[len("/api/directories/"):]
	if r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := h.reg.DeleteDirectory(id); err != nil {
		httpErr(w, http.StatusBadRequest, err)
		return
	}
	logs.Info("deleted directory id=%s", id)
	w.WriteHeader(http.StatusNoContent)
}

// --- Connector API ---

func (h *Handler) connectorsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Name         string   `json:"name"`
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
	c, err := h.reg.AddConnector(config.Connector{
		Name:         body.Name,
		DirectoryIDs: body.DirectoryIDs,
		Write:        body.Write,
	})
	if err != nil {
		logs.Error("add connector %q failed: %v", body.Name, err)
		httpErr(w, http.StatusBadRequest, err)
		return
	}
	logs.Info("added connector %q (id=%s, %d directories, write=%v)", body.Name, c.ID, len(body.DirectoryIDs), body.Write)
	writeJSON(w, http.StatusOK, map[string]string{
		"id":  c.ID,
		"url": fmt.Sprintf("%s/mcp/%s", h.baseURL, c.Token),
	})
}

func (h *Handler) connectorAPI(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Path[len("/api/connectors/"):]
	if r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := h.reg.DeleteConnector(id); err != nil {
		httpErr(w, http.StatusBadRequest, err)
		return
	}
	logs.Info("deleted connector id=%s", id)
	w.WriteHeader(http.StatusNoContent)
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
