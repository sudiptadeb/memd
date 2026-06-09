package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sudiptadeb/memd/server/internal/account"
	"github.com/sudiptadeb/memd/server/internal/logs"
	"github.com/sudiptadeb/memd/server/internal/oidc"
	"github.com/sudiptadeb/memd/server/internal/registry"
)

func TestLogsAPIIsNotCached(t *testing.T) {
	reg := registry.NewEphemeral()
	t.Cleanup(func() { _ = reg.Close() })
	accounts := openTestAccountStore(t)
	admin, err := accounts.CreateSuperAdmin(context.Background(), "admin", "correct horse battery staple")
	if err != nil {
		t.Fatalf("CreateSuperAdmin: %v", err)
	}
	mux := http.NewServeMux()
	handler := New(reg, accounts, "http://127.0.0.1:7878", newTestSessions(t), oidc.NewManager())
	handler.Mount(mux)

	logs.Info("activity polling regression marker")
	req := httptest.NewRequest(http.MethodGet, "/api/logs?since=-1", nil)
	recForCookie := httptest.NewRecorder()
	if err := handler.sessions.Issue(recForCookie, req, sessionData{UserID: admin.ID, Username: admin.Username, SuperAdmin: admin.SuperAdmin}); err != nil {
		t.Fatalf("Issue session: %v", err)
	}
	for _, cookie := range recForCookie.Result().Cookies() {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if got := rec.Header().Get("Pragma"); got != "no-cache" {
		t.Fatalf("Pragma = %q, want no-cache", got)
	}
	if !strings.Contains(rec.Body.String(), "activity polling regression marker") {
		t.Fatalf("logs response missing marker: %s", rec.Body.String())
	}
}

func TestLoginAndAdminCreateUser(t *testing.T) {
	accounts := openTestAccountStore(t)
	admin, err := accounts.CreateSuperAdmin(context.Background(), "admin", "correct horse battery staple")
	if err != nil {
		t.Fatalf("CreateSuperAdmin: %v", err)
	}
	mux, _ := newTestUI(t, accounts)

	loginRec := httptest.NewRecorder()
	loginBody := bytes.NewBufferString(`{"username":"admin","password":"correct horse battery staple"}`)
	mux.ServeHTTP(loginRec, httptest.NewRequest(http.MethodPost, "/api/auth/login", loginBody))
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body=%s", loginRec.Code, loginRec.Body.String())
	}
	if len(loginRec.Result().Cookies()) == 0 {
		t.Fatalf("login did not set a session cookie for %s", admin.Username)
	}

	createReq := httptest.NewRequest(http.MethodPost, "/api/admin/users", bytes.NewBufferString(`{"username":"friend","password":"friend-pass","display_name":"Friend"}`))
	for _, cookie := range loginRec.Result().Cookies() {
		createReq.AddCookie(cookie)
	}
	createRec := httptest.NewRecorder()
	mux.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("create user status = %d, body=%s", createRec.Code, createRec.Body.String())
	}
	user, err := accounts.AuthenticateLocal(context.Background(), "friend", "friend-pass")
	if err != nil {
		t.Fatalf("created user cannot log in: %v", err)
	}
	if user.SuperAdmin {
		t.Fatalf("API-created users must not be super admins")
	}
}

func TestAdminUsersRejectsRegularUser(t *testing.T) {
	accounts := openTestAccountStore(t)
	if _, err := accounts.CreateSuperAdmin(context.Background(), "admin", "correct horse battery staple"); err != nil {
		t.Fatalf("CreateSuperAdmin: %v", err)
	}
	regular, err := accounts.CreateLocalUser(context.Background(), account.CreateUserInput{Username: "friend", Password: "friend-pass"})
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	mux, handler := newTestUI(t, accounts)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
	recForCookie := httptest.NewRecorder()
	if err := handler.sessions.Issue(recForCookie, req, sessionData{UserID: regular.ID, Username: regular.Username, SuperAdmin: regular.SuperAdmin}); err != nil {
		t.Fatalf("Issue session: %v", err)
	}
	for _, cookie := range recForCookie.Result().Cookies() {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
}

func TestUserDataAPIRejectsSuperAdmin(t *testing.T) {
	accounts := openTestAccountStore(t)
	admin, err := accounts.CreateSuperAdmin(context.Background(), "admin", "correct horse battery staple")
	if err != nil {
		t.Fatalf("CreateSuperAdmin: %v", err)
	}
	mux, handler := newTestUI(t, accounts)

	req := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	recForCookie := httptest.NewRecorder()
	if err := handler.sessions.Issue(recForCookie, req, sessionData{UserID: admin.ID, Username: admin.Username, SuperAdmin: admin.SuperAdmin}); err != nil {
		t.Fatalf("Issue session: %v", err)
	}
	for _, cookie := range recForCookie.Result().Cookies() {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
}

func TestSessionAPIReportsAnonymous(t *testing.T) {
	accounts := openTestAccountStore(t)
	mux, _ := newTestUI(t, accounts)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/session", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		User any `json:"user"`
		Auth struct {
			OIDCEnabled bool `json:"oidc_enabled"`
		} `json:"auth"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("session response is not JSON: %v", err)
	}
	if body.User != nil {
		t.Fatalf("expected anonymous session, got user=%v", body.User)
	}
	if body.Auth.OIDCEnabled {
		t.Fatalf("expected oidc disabled in test")
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
	if err := store.Init(context.Background()); err != nil {
		t.Fatalf("account.Init: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func newTestUI(t *testing.T, accounts *account.Store) (*http.ServeMux, *Handler) {
	t.Helper()
	reg := registry.NewEphemeral()
	t.Cleanup(func() { _ = reg.Close() })
	mux := http.NewServeMux()
	handler := New(reg, accounts, "http://127.0.0.1:7878", newTestSessions(t), oidc.NewManager())
	handler.Mount(mux)
	return mux, handler
}

func newTestSessions(t *testing.T) *SessionManager {
	t.Helper()
	sm, err := NewSessionManager("test-secret-key", 0)
	if err != nil {
		t.Fatalf("NewSessionManager: %v", err)
	}
	return sm
}
