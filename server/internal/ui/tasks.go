package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/sudiptadeb/memd/server/internal/account"
	"github.com/sudiptadeb/memd/server/internal/config"
	"github.com/sudiptadeb/memd/server/internal/logs"
	"github.com/sudiptadeb/memd/server/internal/storage"
	"github.com/sudiptadeb/memd/server/internal/tasks"
)

// directoryTasksAPI serves the rich task dashboard for a tasks-enabled
// directory: GET returns the parsed lists and a derived board; POST applies a
// surgical edit (toggle a checkbox or append a task). The files under the
// feature's folder remain the single source of truth — GET re-derives the board
// on every call and POST rewrites only the affected line.
func (h *Handler) directoryTasksAPI(w http.ResponseWriter, r *http.Request, user *account.User, id string) {
	dv := h.directoryForViewer(w, user, id)
	if dv == nil {
		return
	}
	feat, ok := h.features.Lookup("tasks")
	if !ok {
		httpErr(w, http.StatusInternalServerError, fmt.Errorf("tasks feature unavailable"))
		return
	}
	if !featureEnabled(dv.Directory, "tasks") {
		httpErr(w, http.StatusBadRequest, fmt.Errorf("tasks is not enabled for this directory"))
		return
	}
	folder := feat.Folder
	switch r.Method {
	case http.MethodGet:
		h.tasksGet(w, dv.Backend, folder)
	case http.MethodPost:
		if !dv.CanWrite {
			httpErr(w, http.StatusForbidden, fmt.Errorf("you do not have write access to this directory"))
			return
		}
		h.tasksMutate(w, r, user, dv.Backend, folder)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// tasksGet reads every list file under the tasks folder, parses each into a
// List, and returns the lists plus the derived board.
func (h *Handler) tasksGet(w http.ResponseWriter, backend storage.Backend, folder string) {
	lists := collectTaskLists(backend, folder)
	board := tasks.BuildBoard(lists, time.Now().UTC())
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"folder": folder, "lists": lists, "board": board})
}

// collectTaskLists reads and parses every list file under the tasks folder.
// A missing folder yields an empty (non-nil) slice.
func collectTaskLists(backend storage.Backend, folder string) []tasks.List {
	lists := []tasks.List{}
	entries, err := backend.ListPath(folder)
	if err != nil {
		return lists
	}
	for _, e := range entries {
		if e.IsDir || !tasks.IsListFile(e.Name) {
			continue
		}
		content, rerr := backend.ReadRaw(e.Path)
		if rerr != nil {
			continue
		}
		lists = append(lists, tasks.BuildList(e.Path, tasks.DisplayName(e.Name), content))
	}
	return lists
}

// tasksAllAPI aggregates the task dashboard across every directory the user can
// view that has tasks enabled, so the Tasks sidenav view can show all work in
// one place, filterable to a single directory client-side.
func (h *Handler) tasksAllAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	user := userFromContext(r.Context())
	if user == nil {
		httpErr(w, http.StatusUnauthorized, fmt.Errorf("not authenticated"))
		return
	}
	feat, ok := h.features.Lookup("tasks")
	if !ok {
		httpErr(w, http.StatusInternalServerError, fmt.Errorf("tasks feature unavailable"))
		return
	}
	folder := feat.Folder
	now := time.Now().UTC()
	type dirTasks struct {
		ID       string       `json:"id"`
		Name     string       `json:"name"`
		CanWrite bool         `json:"can_write"`
		Lists    []tasks.List `json:"lists"`
		Board    tasks.Board  `json:"board"`
	}
	out := []dirTasks{}
	for _, d := range h.reg.DirectoriesForUser(user.ID) {
		if !featureEnabled(d, "tasks") {
			continue
		}
		dv := h.reg.DirectoryViewForUser(user.ID, d.ID)
		if dv == nil || dv.Backend == nil {
			continue
		}
		lists := collectTaskLists(dv.Backend, folder)
		out = append(out, dirTasks{
			ID:       d.ID,
			Name:     d.Name,
			CanWrite: dv.CanWrite,
			Lists:    lists,
			Board:    tasks.BuildBoard(lists, now),
		})
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"directories": out})
}

// tasksMutate applies one task edit (toggle a checkbox or append a task).
func (h *Handler) tasksMutate(w http.ResponseWriter, r *http.Request, user *account.User, backend storage.Backend, folder string) {
	var body struct {
		Action   string `json:"action"`
		File     string `json:"file"`
		Line     int    `json:"line"`
		Expect   string `json:"expect"`
		Title    string `json:"title"`
		ListName string `json:"list_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpErr(w, http.StatusBadRequest, err)
		return
	}
	switch body.Action {
	case "toggle":
		file, err := safeTaskFile(folder, body.File)
		if err != nil {
			httpErr(w, http.StatusBadRequest, err)
			return
		}
		content, err := backend.ReadRaw(file)
		if err != nil {
			httpErr(w, http.StatusNotFound, err)
			return
		}
		out, err := tasks.ToggleLine(content, body.Line, body.Expect)
		if err != nil {
			httpErr(w, http.StatusConflict, err)
			return
		}
		if err := backend.Write(file, out, "memd: toggle task"); err != nil {
			httpErr(w, http.StatusInternalServerError, err)
			return
		}
		logs.InfoUser(user.ID, "tasks: toggled %s:%d", file, body.Line)
		writeJSON(w, http.StatusOK, map[string]string{"file": file})
	case "add":
		file := body.File
		if file == "" {
			name := strings.TrimSpace(body.ListName)
			if name == "" {
				name = "inbox"
			}
			file = folder + "/" + slugList(name) + ".md"
		}
		file, err := safeTaskFile(folder, file)
		if err != nil {
			httpErr(w, http.StatusBadRequest, err)
			return
		}
		if strings.TrimSpace(body.Title) == "" {
			httpErr(w, http.StatusBadRequest, fmt.Errorf("task title is required"))
			return
		}
		var content []byte
		if existing, rerr := backend.ReadRaw(file); rerr == nil {
			content = existing
		}
		out := tasks.AppendTask(content, body.Title)
		if err := backend.Write(file, out, "memd: add task"); err != nil {
			httpErr(w, http.StatusInternalServerError, err)
			return
		}
		logs.InfoUser(user.ID, "tasks: added task to %s", file)
		writeJSON(w, http.StatusOK, map[string]string{"file": file})
	default:
		httpErr(w, http.StatusBadRequest, fmt.Errorf("unknown action: %q", body.Action))
	}
}

// featureEnabled reports whether the given feature key is enabled on dir.
func featureEnabled(dir config.Directory, key string) bool {
	for _, f := range dir.Features {
		if f.Key == key {
			return f.Enabled
		}
	}
	return false
}

// safeTaskFile validates that file is a markdown list file directly inside the
// tasks folder (no traversal, no nested folders, no underscore markers).
func safeTaskFile(folder, file string) (string, error) {
	clean := path.Clean(strings.TrimSpace(file))
	if clean == "" || clean == "." {
		return "", fmt.Errorf("file is required")
	}
	if !strings.HasPrefix(clean, folder+"/") {
		return "", fmt.Errorf("file must be inside %s/", folder)
	}
	rest := strings.TrimPrefix(clean, folder+"/")
	if strings.Contains(rest, "/") {
		return "", fmt.Errorf("task lists live directly in %s/", folder)
	}
	if !tasks.IsListFile(rest) {
		return "", fmt.Errorf("not a task list file: %s", rest)
	}
	return clean, nil
}

// slugList turns a user-supplied list name into a safe filename stem.
func slugList(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteRune('-')
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		s = "list"
	}
	return s
}
