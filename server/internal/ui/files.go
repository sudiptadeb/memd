package ui

import (
	"fmt"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/sudiptadeb/memd/server/internal/account"
	"github.com/sudiptadeb/memd/server/internal/registry"
	"github.com/sudiptadeb/memd/server/internal/storage"
)

// rawFileLimit caps how many bytes the raw endpoint serves. Memory files are
// small by design; anything larger is almost certainly not a memory file and
// should not be streamed through the UI session.
const rawFileLimit = 10 << 20 // 10 MiB

// directoryForViewer resolves a directory the user may look inside, writing
// the error response itself when access fails.
func (h *Handler) directoryForViewer(w http.ResponseWriter, user *account.User, id string) *registry.DirectoryView {
	dv := h.reg.DirectoryViewForUser(user.ID, id)
	if dv == nil {
		httpErr(w, http.StatusNotFound, fmt.Errorf("directory not found"))
		return nil
	}
	if dv.Backend == nil {
		httpErr(w, http.StatusServiceUnavailable, fmt.Errorf("directory backend is not available"))
		return nil
	}
	return dv
}

// directoryFilesAPI lists the direct children at a relative path inside a
// directory the user can view. Traversal safety is enforced by the backend.
func (h *Handler) directoryFilesAPI(w http.ResponseWriter, r *http.Request, user *account.User, id string) {
	dv := h.directoryForViewer(w, user, id)
	if dv == nil {
		return
	}
	rel := strings.TrimSpace(r.URL.Query().Get("path"))
	entries, err := dv.Backend.ListPath(rel)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err)
		return
	}
	if entries == nil {
		entries = []storage.DirEntry{}
	}
	if rel == "." {
		rel = ""
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"path": rel, "entries": entries})
}

// directoryRawAPI serves one file's bytes for the in-UI viewer and the
// open-in-new-tab link. Memory content is untrusted agent-written data, so
// markup formats are never served with an executable content type: everything
// is text/plain except a small allowlist of binary media, and every response
// carries a sandbox CSP as a second layer.
func (h *Handler) directoryRawAPI(w http.ResponseWriter, r *http.Request, user *account.User, id string) {
	dv := h.directoryForViewer(w, user, id)
	if dv == nil {
		return
	}
	rel := strings.TrimSpace(r.URL.Query().Get("path"))
	if rel == "" {
		httpErr(w, http.StatusBadRequest, fmt.Errorf("path is required"))
		return
	}
	content, err := dv.Backend.ReadRaw(rel)
	if err != nil {
		httpErr(w, http.StatusNotFound, err)
		return
	}
	if len(content) > rawFileLimit {
		httpErr(w, http.StatusRequestEntityTooLarge, fmt.Errorf("file is larger than %d bytes", rawFileLimit))
		return
	}
	w.Header().Set("Content-Type", rawContentType(rel))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self'")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", "inline; filename="+strconv.Quote(path.Base(rel)))
	_, _ = w.Write(content)
}

// rawContentType picks a safe content type by extension. HTML, SVG, XML and
// every unknown format render as plain text so stored markup cannot run
// scripts on the UI origin.
func rawContentType(rel string) string {
	switch strings.ToLower(path.Ext(rel)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".pdf":
		return "application/pdf"
	default:
		return "text/plain; charset=utf-8"
	}
}
