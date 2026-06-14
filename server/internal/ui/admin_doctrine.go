package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/sudiptadeb/memd/server/internal/logs"
)

// adminDoctrinesAPI lists the live-editable doctrines (super admin only): the
// global MCP instructions plus each built-in feature's base doctrine.
func (h *Handler) adminDoctrinesAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"doctrines": h.live.List()})
}

// adminDoctrineAPI sets (PUT) or resets (DELETE) one doctrine. Overrides live in
// memory only and revert to the compiled default on restart — there is no
// persistence here by design.
func (h *Handler) adminDoctrineAPI(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Path[len("/api/admin/doctrines/"):]
	if decoded, err := url.PathUnescape(id); err == nil {
		id = decoded
	}
	if id == "" {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodPut, http.MethodPost:
		var body struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httpErr(w, http.StatusBadRequest, err)
			return
		}
		if !h.live.Set(id, body.Text) {
			httpErr(w, http.StatusNotFound, fmt.Errorf("unknown doctrine: %s", id))
			return
		}
		logs.Info("super admin overrode doctrine %q (in-memory, not persisted)", id)
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "overridden": true})
	case http.MethodDelete:
		if !h.live.Reset(id) {
			httpErr(w, http.StatusNotFound, fmt.Errorf("unknown doctrine: %s", id))
			return
		}
		logs.Info("super admin reset doctrine %q to default", id)
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "overridden": false})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
