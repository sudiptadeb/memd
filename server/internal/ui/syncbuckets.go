package ui

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
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
	case id != "" && action == "export" && r.Method == http.MethodPost:
		h.exportSyncBucket(w, r, user.ID, id)
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

// Exports are limited per IP and per account, so neither many accounts behind
// one IP nor one account behind many IPs can keep the server busy, and each
// bucket is exported one at a time.
const (
	syncExportsPerWindow = 3
	syncExportWindow     = time.Hour
)

// syncExportManifest is an export's first line. The lines after it are the
// bucket's items as the server keeps them, sealed, one JSON object each.
type syncExportManifest struct {
	Format     string `json:"format"`
	Protocol   int    `json:"protocol"`
	Bucket     string `json:"bucket"`
	CreatedAt  string `json:"created_at"`
	ExportedAt string `json:"exported_at"`
	Items      int    `json:"items"`
	Bytes      int64  `json:"bytes"`
	// Newest is the newest item's timestamp, in milliseconds.
	Newest int64 `json:"newest"`
}

// exportSyncBucket streams everything the server holds for a bucket as JSON
// lines: a manifest, then each item, still sealed. The token is not in it.
func (h *Handler) exportSyncBucket(w http.ResponseWriter, r *http.Request, userID, id string) {
	for _, key := range []string{"user:" + userID, "ip:" + clientIP(r)} {
		if !h.exportLimiter.allow(key) {
			tooMany(w, h.exportLimiter.retryIn(key), "export limit reached")
			return
		}
	}
	if _, busy := h.exporting.LoadOrStore(id, true); busy {
		tooMany(w, time.Minute, "this bucket is being exported already")
		return
	}
	defer h.exporting.Delete(id)

	now := time.Now().UTC()
	enc := json.NewEncoder(w)
	err := h.accounts.ExportSyncBucket(r.Context(), userID, id,
		func(ex account.SyncBucketExport) error {
			name := exportFileName(ex.Bucket.Name, now)
			w.Header().Set("Content-Type", "application/x-ndjson")
			w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
			w.Header().Set("Cache-Control", "no-store")
			return enc.Encode(syncExportManifest{
				Format: "synckosh-export", Protocol: 1, Bucket: ex.Bucket.Name,
				CreatedAt: ex.Bucket.CreatedAt.UTC().Format(time.RFC3339), ExportedAt: now.Format(time.RFC3339),
				Items: ex.Items, Bytes: ex.Bytes, Newest: ex.Newest,
			})
		},
		func(raw []byte) error {
			if _, err := w.Write(raw); err != nil {
				return err
			}
			_, err := w.Write([]byte("\n"))
			return err
		})
	if errors.Is(err, account.ErrNotFound) {
		httpErr(w, http.StatusNotFound, err)
		return
	}
	if err != nil {
		// Once the manifest is out the status is sent; the download just ends.
		logs.Warn("sync: export bucket id=%s: %v", id, err)
		return
	}
	logs.InfoUser(userID, "exported sync bucket id=%s", id)
}

func tooMany(w http.ResponseWriter, wait time.Duration, msg string) {
	w.Header().Set("Retry-After", strconv.Itoa(int((wait+time.Second-1)/time.Second)))
	httpErr(w, http.StatusTooManyRequests, errors.New(msg))
}

// exportFileName is <bucket>-<date>.synckosh.jsonl, with the bucket's name cut
// down to letters, digits, "-" and "_".
func exportFileName(bucket string, at time.Time) string {
	var b strings.Builder
	for _, c := range bucket {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
			b.WriteRune(c)
		case c == ' ':
			b.WriteByte('-')
		}
	}
	name := b.String()
	if name == "" {
		name = "bucket"
	}
	return name + "-" + at.Format("2006-01-02") + ".synckosh.jsonl"
}
