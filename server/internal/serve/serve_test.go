package serve

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sudiptadeb/memd/server/internal/account"
)

func TestEnsureAccountDBInitWithSuperAdminOptions(t *testing.T) {
	ctx := context.Background()
	store := openTestAccountStore(t)
	stdin := openDevNull(t)
	var stdout bytes.Buffer

	err := ensureAccountDB(ctx, store, Options{
		InitDB:                   true,
		CreateSuperAdminUsername: "root",
		CreateSuperAdminPassword: "correct horse battery staple",
		Stdin:                    stdin,
		Stdout:                   &stdout,
	})
	if err != nil {
		t.Fatalf("ensureAccountDB: %v", err)
	}
	admin, err := store.AuthenticateLocal(ctx, "root", "correct horse battery staple")
	if err != nil {
		t.Fatalf("AuthenticateLocal: %v", err)
	}
	if !admin.SuperAdmin {
		t.Fatalf("created user should be super admin: %+v", admin)
	}
}

func TestEnsureAccountDBNonInteractiveMissingInitFails(t *testing.T) {
	store := openTestAccountStore(t)
	err := ensureAccountDB(context.Background(), store, Options{
		Stdin:  openDevNull(t),
		Stdout: &bytes.Buffer{},
	})
	if err == nil || errors.Is(err, account.ErrNotInitialized) {
		t.Fatalf("ensureAccountDB err = %v, want setup guidance error", err)
	}
}

func openTestAccountStore(t *testing.T) *account.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "memd.db")
	store, err := account.Open(context.Background(), account.DBConfig{
		Driver:     "sqlite",
		DSN:        "file:" + path,
		Source:     "test",
		SQLitePath: path,
	})
	if err != nil {
		t.Fatalf("account.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func openDevNull(t *testing.T) *os.File {
	t.Helper()
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open dev null: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func TestWithSecurityHeaders(t *testing.T) {
	h := withSecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "same-origin",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("header %s = %q, want %q", k, got, v)
		}
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP missing frame-ancestors 'none': %q", csp)
	}
}

func TestWithMaxBodyRejectsOversizedBody(t *testing.T) {
	h := withMaxBody(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "too big", http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	}), 8)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("0123456789"))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", rec.Code)
	}
}
