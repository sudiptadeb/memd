// Package syncbucket serves synckosh sync buckets at /sync. A bucket's token
// reaches it two ways, as a connector's does: "Authorization: Bearer <token>"
// on /sync/v1/..., or in the path, /sync/<token>/v1/.... The token decides the
// bucket; nothing else in a request can name one. Items arrive sealed by the
// devices and are stored as they came, in memd's database (account.SyncStore).
package syncbucket

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/debkosh/memd/server/internal/account"
	"github.com/debkosh/memd/server/internal/logs"
	"github.com/debkosh/synckosh/server"
)

// Prefix is where the sync endpoint is mounted.
const Prefix = "/sync"

const (
	// cleanupAfter is how long deletes are kept for devices that are away.
	// A device away longer starts over from the server's copy.
	cleanupAfter = 90 * 24 * time.Hour
	// cleanupEvery is how often deletes older than cleanupAfter are removed.
	cleanupEvery = 24 * time.Hour
)

// Handler is the sync endpoint.
type Handler struct {
	accounts *account.Store
	srv      *server.Server
}

type bucketKey struct{}

// New makes the endpoint over the account database, with synckosh's default
// limits.
func New(accounts *account.Store) *Handler {
	srv := server.New(accounts.SyncStore(), func(r *http.Request) (string, error) {
		bucket, _ := r.Context().Value(bucketKey{}).(string)
		if bucket == "" {
			return "", errors.New("no bucket")
		}
		return bucket, nil
	})
	srv.CleanupAfter = cleanupAfter
	return &Handler{accounts: accounts, srv: srv}
}

// Mount registers the endpoint on mux.
func (h *Handler) Mount(mux *http.ServeMux) {
	mux.Handle(Prefix+"/", h)
}

// ServeHTTP finds the bucket from the token and hands synckosh the request
// with the prefix and any path token taken off, so neither reaches its logs.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, Prefix)
	tok := bearerToken(r)
	if first, tail, _ := strings.Cut(strings.TrimPrefix(rest, "/"), "/"); first != "v1" {
		if tok != "" && tok != first {
			unauthorized(w)
			return
		}
		tok, rest = first, "/"+tail
	}
	if tok == "" {
		unauthorized(w)
		return
	}
	bucket, err := h.accounts.SyncBucketByToken(r.Context(), tok)
	if errors.Is(err, account.ErrNotFound) {
		unauthorized(w)
		return
	}
	if err != nil {
		logs.Error("sync: look up bucket: %v", err)
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	r2 := r.WithContext(context.WithValue(r.Context(), bucketKey{}, bucket.ID))
	r2.URL = &url.URL{Path: rest, RawQuery: r.URL.RawQuery}
	r2.RequestURI = ""
	h.srv.ServeHTTP(w, r2)
}

// Start removes old deletes once a day until ctx is done.
func (h *Handler) Start(ctx context.Context) {
	go func() {
		t := time.NewTicker(cleanupEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if n, err := h.srv.Cleanup(); err != nil {
					logs.Warn("sync: cleanup: %v", err)
				} else if n > 0 {
					logs.Info("sync: cleanup removed %d deleted items", n)
				}
			}
		}
	}()
}

func bearerToken(r *http.Request) string {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	const prefix = "bearer "
	if len(auth) <= len(prefix) || !strings.EqualFold(auth[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(auth[len(prefix):])
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "not signed in"})
}
