package ui

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/sudiptadeb/memd/server/internal/logs"
)

// synckoshState is what /synckosh needs to draw: the signed-in user, or none,
// and their buckets. It goes into the page itself, so the page shows at once
// instead of asking for it after its script loads.
type synckoshState struct {
	User    *sessionUser     `json:"user"`
	Buckets []syncBucketView `json:"buckets"`
	Error   string           `json:"error,omitempty"`
}

// synckoshPage serves the sync page's shell with its state in a JSON script
// element. The page holds the user's tokens, so the browser keeps no copy.
func (h *Handler) synckoshPage(shell []byte) http.HandlerFunc {
	head := []byte("</head>")
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}
		state := synckoshState{Buckets: []syncBucketView{}}
		if user, ok := h.currentUser(w, r); ok {
			su := sessionUserFromAccount(user)
			state.User = &su
			views, err := h.syncBucketViews(r.Context(), user.ID)
			if err != nil {
				logs.Error("synckosh: list buckets: %v", err)
				state.Error = "Could not load your buckets. Reload to try again."
			} else {
				state.Buckets = views
			}
		}
		// json.Marshal escapes <, > and &, so nothing in it can end the script.
		data, err := json.Marshal(state)
		if err != nil {
			httpErr(w, http.StatusInternalServerError, err)
			return
		}
		tag := append(append([]byte(`<script id="synckosh-state" type="application/json">`), data...), []byte("</script>")...)
		page := bytes.Replace(shell, head, append(tag, head...), 1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(page)
	}
}
