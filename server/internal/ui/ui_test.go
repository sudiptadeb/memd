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

func TestTeamsAPIRegularUserCreatesTeamAndSuperAdminRejected(t *testing.T) {
	accounts := openTestAccountStore(t)
	admin, err := accounts.CreateSuperAdmin(context.Background(), "admin", "correct horse battery staple")
	if err != nil {
		t.Fatalf("CreateSuperAdmin: %v", err)
	}
	regular, err := accounts.CreateLocalUser(context.Background(), account.CreateUserInput{Username: "friend", Password: "friend-pass"})
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	mux, handler := newTestUI(t, accounts)

	adminReq := httptest.NewRequest(http.MethodPost, "/api/teams", bytes.NewBufferString(`{"name":"Admin Team"}`))
	addSession(t, handler, adminReq, admin)
	adminRec := httptest.NewRecorder()
	mux.ServeHTTP(adminRec, adminReq)
	if adminRec.Code != http.StatusForbidden {
		t.Fatalf("super admin create status = %d, body=%s", adminRec.Code, adminRec.Body.String())
	}

	req := httptest.NewRequest(http.MethodPost, "/api/teams", bytes.NewBufferString(`{"name":"Family Memory"}`))
	addSession(t, handler, req, regular)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("regular create status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Team struct {
			ID   string `json:"id"`
			Role string `json:"role"`
		} `json:"team"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("create response JSON: %v", err)
	}
	if body.Team.ID == "" || body.Team.Role != account.RoleOwner {
		t.Fatalf("created team = %+v, want owner role", body.Team)
	}
}

func TestTeamInviteAPIAcceptsValidInvite(t *testing.T) {
	accounts := openTestAccountStore(t)
	owner, err := accounts.CreateLocalUser(context.Background(), account.CreateUserInput{Username: "owner", Password: "owner-pass"})
	if err != nil {
		t.Fatalf("CreateLocalUser owner: %v", err)
	}
	member, err := accounts.CreateLocalUser(context.Background(), account.CreateUserInput{Username: "member", Password: "member-pass"})
	if err != nil {
		t.Fatalf("CreateLocalUser member: %v", err)
	}
	team, err := accounts.CreateTeam(context.Background(), account.CreateTeamInput{Name: "Family", OwnerUserID: owner.ID})
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	mux, handler := newTestUI(t, accounts)

	createReq := httptest.NewRequest(http.MethodPost, "/api/teams/"+team.ID+"/invites", bytes.NewBufferString(`{"role":"member","max_uses":1}`))
	addSession(t, handler, createReq, owner)
	createRec := httptest.NewRecorder()
	mux.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("create invite status = %d, body=%s", createRec.Code, createRec.Body.String())
	}
	var createBody struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &createBody); err != nil {
		t.Fatalf("create invite JSON: %v", err)
	}
	if createBody.Token == "" {
		t.Fatalf("create invite response missing token: %s", createRec.Body.String())
	}

	previewRec := httptest.NewRecorder()
	mux.ServeHTTP(previewRec, httptest.NewRequest(http.MethodGet, "/api/team-invites/"+createBody.Token, nil))
	if previewRec.Code != http.StatusOK {
		t.Fatalf("preview status = %d, body=%s", previewRec.Code, previewRec.Body.String())
	}

	acceptReq := httptest.NewRequest(http.MethodPost, "/api/team-invites/"+createBody.Token+"/accept", nil)
	addSession(t, handler, acceptReq, member)
	acceptRec := httptest.NewRecorder()
	mux.ServeHTTP(acceptRec, acceptReq)
	if acceptRec.Code != http.StatusOK {
		t.Fatalf("accept status = %d, body=%s", acceptRec.Code, acceptRec.Body.String())
	}
	role, err := accounts.UserTeamRole(context.Background(), team.ID, member.ID)
	if err != nil {
		t.Fatalf("UserTeamRole: %v", err)
	}
	if role != account.RoleMember {
		t.Fatalf("role = %q, want member", role)
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

func addSession(t *testing.T, handler *Handler, req *http.Request, user account.User) {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := handler.sessions.Issue(rec, req, sessionData{UserID: user.ID, Username: user.Username, SuperAdmin: user.SuperAdmin}); err != nil {
		t.Fatalf("Issue session: %v", err)
	}
	for _, cookie := range rec.Result().Cookies() {
		req.AddCookie(cookie)
	}
}

func newTestSessions(t *testing.T) *SessionManager {
	t.Helper()
	sm, err := NewSessionManager("test-secret-key", 0)
	if err != nil {
		t.Fatalf("NewSessionManager: %v", err)
	}
	return sm
}
