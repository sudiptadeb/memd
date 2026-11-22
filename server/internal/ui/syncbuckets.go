package ui

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/sudiptadeb/memd/server/internal/account"
	"github.com/sudiptadeb/memd/server/internal/logs"
)

// Sync buckets (internal/syncbucket serves them at /sync). The dashboard
// lists the user's buckets with their tokens, makes one, gives one a new token
// and deletes one. Each answer is the signed-in user's own buckets only.

type syncBucketView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
	// URL is the bucket's address for the header form: the token goes in
	// "Authorization: Bearer <token>".
	URL string `json:"url"`
	// Token and TokenURL, the address that carries the token, are empty for a
	// bucket made while only the token's hash was kept, until a new token.
	Token    string `json:"token,omitempty"`
	TokenURL string `json:"token_url,omitempty"`
	// Stats is what the server holds for the bucket, in the list only, and
	// absent while the bucket holds nothing.
	Stats *syncStatsView `json:"stats,omitempty"`
}

type syncStatsView struct {
	Collections []syncCollectionView `json:"collections"`
	Bytes       int64                `json:"bytes"`
	// Newest is the newest item's timestamp, in milliseconds.
	Newest int64 `json:"newest"`
}

type syncCollectionView struct {
	Name  string `json:"name"`
	Items int    `json:"items"`
}

func (h *Handler) syncURL() string { return h.baseURL + "/sync" }

func (h *Handler) syncBucketView(b account.SyncBucket) syncBucketView {
	v := syncBucketView{
		ID:        b.ID,
		Name:      b.Name,
		CreatedAt: b.CreatedAt.UTC().Format(time.RFC3339),
		URL:       h.syncURL(),
		Token:     b.Token,
	}
	if b.Token != "" {
		v.TokenURL = h.syncURL() + "/" + b.Token
	}
	return v
}

// GET /api/sync/buckets lists the user's buckets, each with what the server
// holds for it; POST makes one.
func (h *Handler) syncBucketsAPI(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		httpErr(w, http.StatusUnauthorized, errors.New("not authenticated"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		buckets, err := h.accounts.ListSyncBuckets(r.Context(), user.ID)
		if err != nil {
			httpErr(w, http.StatusInternalServerError, err)
			return
		}
		stats, err := h.accounts.SyncBucketStatsFor(r.Context(), user.ID)
		if err != nil {
			httpErr(w, http.StatusInternalServerError, err)
			return
		}
		views := make([]syncBucketView, 0, len(buckets))
		for _, b := range buckets {
			v := h.syncBucketView(b)
			if st, ok := stats[b.ID]; ok {
				cols := make([]syncCollectionView, 0, len(st.Collections))
				for _, c := range st.Collections {
					cols = append(cols, syncCollectionView{Name: c.Name, Items: c.Items})
				}
				v.Stats = &syncStatsView{Collections: cols, Bytes: st.Bytes, Newest: st.Newest}
			}
			views = append(views, v)
		}
		writeJSON(w, http.StatusOK, map[string]any{"buckets": views})
	case http.MethodPost:
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httpErr(w, http.StatusBadRequest, err)
			return
		}
		b, err := h.accounts.CreateSyncBucket(r.Context(), user.ID, body.Name)
		if err != nil {
			httpErr(w, statusForAccountError(err), err)
			return
		}
		logs.InfoUser(user.ID, "added sync bucket %q (id=%s)", b.Name, b.ID)
		writeJSON(w, http.StatusOK, map[string]any{"bucket": h.syncBucketView(b)})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// POST /api/sync/buckets/{id}/rotate gives a bucket a new token, ending the
// old one; POST /api/sync/buckets/{id}/reset empties the server's copy, so the
// devices are set up again; DELETE /api/sync/buckets/{id} deletes the bucket
// and its items.
func (h *Handler) syncBucketAPI(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		httpErr(w, http.StatusUnauthorized, errors.New("not authenticated"))
		return
	}
	id, action, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/api/sync/buckets/"), "/")
	switch {
	case id != "" && action == "rotate" && r.Method == http.MethodPost:
		b, err := h.accounts.RotateSyncBucketToken(r.Context(), user.ID, id)
		if err != nil {
			httpErr(w, statusForAccountError(err), err)
			return
		}
		logs.InfoUser(user.ID, "rotated sync bucket token %q (id=%s)", b.Name, b.ID)
		writeJSON(w, http.StatusOK, map[string]any{"bucket": h.syncBucketView(b)})
	case id != "" && action == "reset" && r.Method == http.MethodPost:
		if err := h.accounts.ResetSyncBucket(r.Context(), user.ID, id); err != nil {
			httpErr(w, statusForAccountError(err), err)
			return
		}
		logs.InfoUser(user.ID, "reset sync bucket id=%s", id)
		w.WriteHeader(http.StatusNoContent)
	case id != "" && action == "" && r.Method == http.MethodDelete:
		if err := h.accounts.DeleteSyncBucket(r.Context(), user.ID, id); err != nil {
			httpErr(w, statusForAccountError(err), err)
			return
		}
		logs.InfoUser(user.ID, "deleted sync bucket id=%s", id)
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}
