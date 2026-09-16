package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sudiptadeb/memd/server/internal/account"
	"github.com/sudiptadeb/memd/server/internal/backup"
)

func newBackupTestUI(t *testing.T) (*http.ServeMux, *Handler, *account.Store) {
	t.Helper()
	accounts := openTestAccountStore(t)
	mux, handler := newTestUI(t, accounts)
	runner, err := backup.New(accounts, backup.Options{WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("backup.New: %v", err)
	}
	handler.SetBackup(runner)
	return mux, handler, accounts
}

func decodeBackupView(t *testing.T, rec *httptest.ResponseRecorder) backupConfigView {
	t.Helper()
	var resp struct {
		Backup backupConfigView `json:"backup"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode backup view: %v (body=%s)", err, rec.Body.String())
	}
	return resp.Backup
}

func backupReq(t *testing.T, mux *http.ServeMux, handler *Handler, user account.User, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	addSession(t, handler, req, user)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// TestAdminBackupAPIMasksSecretsAndKeepsThemOnPut: GET never returns the token
// or passphrase (only has_* flags); a PUT that omits them keeps the stored
// values; an explicit empty string clears.
func TestAdminBackupAPIMasksSecretsAndKeepsThemOnPut(t *testing.T) {
	ctx := context.Background()
	mux, handler, accounts := newBackupTestUI(t)
	admin, err := accounts.CreateSuperAdmin(ctx, "admin", "correct horse battery staple")
	if err != nil {
		t.Fatalf("CreateSuperAdmin: %v", err)
	}

	rec := backupReq(t, mux, handler, admin, http.MethodGet, "/api/admin/backup", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET fresh = %d, body=%s", rec.Code, rec.Body.String())
	}
	view := decodeBackupView(t, rec)
	if view.Enabled || view.Branch != "main" || view.DailyAtUTC != "03:00" || view.RetentionDays != 30 || !view.Supported || view.HasAuthToken || view.HasPassphrase {
		t.Fatalf("fresh view = %+v, want defaults", view)
	}

	const token = "ghp_secret_token_value"
	const passphrase = "a passphrase long enough"
	rec = backupReq(t, mux, handler, admin, http.MethodPut, "/api/admin/backup", `{
		"enabled": true, "remote_url": "https://github.com/org/memd-backups.git", "branch": "main",
		"auth_username": "octocat", "auth_token": "`+token+`", "passphrase": "`+passphrase+`",
		"daily_at_utc": "04:30", "retention_days": 14}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT full = %d, body=%s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); strings.Contains(body, token) || strings.Contains(body, passphrase) {
		t.Fatalf("PUT response leaks a secret: %s", body)
	}
	view = decodeBackupView(t, rec)
	if !view.Enabled || !view.HasAuthToken || !view.HasPassphrase || view.DailyAtUTC != "04:30" || view.RetentionDays != 14 || view.AuthUsername != "octocat" {
		t.Fatalf("PUT full view = %+v", view)
	}
	if view.NextRunAt != nil && (view.NextRunAt.Hour() != 4 || view.NextRunAt.Minute() != 30) {
		t.Fatalf("next_run_at = %v, want a 04:30 slot", view.NextRunAt)
	}

	rec = backupReq(t, mux, handler, admin, http.MethodGet, "/api/admin/backup", "")
	if body := rec.Body.String(); strings.Contains(body, token) || strings.Contains(body, passphrase) {
		t.Fatalf("GET leaks a secret: %s", body)
	}

	// Omit both secrets: the stored ones survive.
	rec = backupReq(t, mux, handler, admin, http.MethodPut, "/api/admin/backup", `{
		"enabled": true, "remote_url": "https://github.com/org/memd-backups.git", "branch": "main",
		"auth_username": "octocat", "daily_at_utc": "04:30", "retention_days": 14}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT omit = %d, body=%s", rec.Code, rec.Body.String())
	}
	stored, _, err := accounts.GetBackupSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stored.AuthToken != token || stored.Passphrase != passphrase {
		t.Fatalf("secrets not kept on omit: %+v", stored)
	}

	// Explicit empty strings clear (only allowed while disabled).
	rec = backupReq(t, mux, handler, admin, http.MethodPut, "/api/admin/backup", `{
		"enabled": false, "remote_url": "https://github.com/org/memd-backups.git", "branch": "main",
		"auth_token": "", "passphrase": ""}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT clear = %d, body=%s", rec.Code, rec.Body.String())
	}
	view = decodeBackupView(t, rec)
	if view.HasAuthToken || view.HasPassphrase || view.Enabled {
		t.Fatalf("PUT clear view = %+v", view)
	}
	stored, _, err = accounts.GetBackupSettings(ctx)
	if err != nil || stored.AuthToken != "" || stored.Passphrase != "" {
		t.Fatalf("secrets not cleared: %+v err=%v", stored, err)
	}
}

// TestAdminBackupAPIValidates: transport and completeness rules answer 400,
// and a manual run on a disabled configuration answers 400 too.
func TestAdminBackupAPIValidates(t *testing.T) {
	ctx := context.Background()
	mux, handler, accounts := newBackupTestUI(t)
	admin, err := accounts.CreateSuperAdmin(ctx, "admin", "correct horse battery staple")
	if err != nil {
		t.Fatalf("CreateSuperAdmin: %v", err)
	}
	cases := map[string]string{
		"ssh remote":        `{"enabled": false, "remote_url": "git@github.com:org/repo.git"}`,
		"short passphrase":  `{"enabled": false, "passphrase": "short"}`,
		"enabled w/o token": `{"enabled": true, "remote_url": "https://github.com/org/repo.git", "passphrase": "a passphrase long enough"}`,
		"bad time":          `{"enabled": false, "daily_at_utc": "3pm"}`,
	}
	for name, body := range cases {
		rec := backupReq(t, mux, handler, admin, http.MethodPut, "/api/admin/backup", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: PUT = %d, want 400 (body=%s)", name, rec.Code, rec.Body.String())
		}
	}
	rec := backupReq(t, mux, handler, admin, http.MethodPost, "/api/admin/backup/run", "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("run while disabled = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
	rec = backupReq(t, mux, handler, admin, http.MethodGet, "/api/admin/backup/download", "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("download without passphrase = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
	rec = backupReq(t, mux, handler, admin, http.MethodPost, "/api/admin/backup/check", `{"remote_url": "ssh://git@github.com/org/repo.git"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok":false`) {
		t.Errorf("check with ssh remote = %d %s, want ok:false", rec.Code, rec.Body.String())
	}
}

// TestAdminBackupAPIDownloadStreamsArchive: with a passphrase stored, the
// download endpoint returns a sealed archive that decrypts with it.
func TestAdminBackupAPIDownloadStreamsArchive(t *testing.T) {
	ctx := context.Background()
	mux, handler, accounts := newBackupTestUI(t)
	admin, err := accounts.CreateSuperAdmin(ctx, "admin", "correct horse battery staple")
	if err != nil {
		t.Fatalf("CreateSuperAdmin: %v", err)
	}
	if err := accounts.SaveBackupSettings(ctx, account.BackupSettings{Branch: "main", DailyAtUTC: "03:00", Passphrase: "a passphrase long enough"}); err != nil {
		t.Fatal(err)
	}
	rec := backupReq(t, mux, handler, admin, http.MethodGet, "/api/admin/backup/download", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("download = %d, body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, `attachment; filename="memd-`) || !strings.HasSuffix(cd, `.memdbk"`) {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if _, err := backup.Inspect(rec.Body.Bytes(), "a passphrase long enough"); err != nil {
		t.Fatalf("downloaded archive does not inspect: %v", err)
	}
}

// TestAdminBackupAPIRequiresSuperAdmin: every backup route is 401 anonymous
// and 403 for a signed-in non-admin.
func TestAdminBackupAPIRequiresSuperAdmin(t *testing.T) {
	ctx := context.Background()
	mux, handler, accounts := newBackupTestUI(t)
	if _, err := accounts.CreateSuperAdmin(ctx, "admin", "correct horse battery staple"); err != nil {
		t.Fatalf("CreateSuperAdmin: %v", err)
	}
	user, err := accounts.CreateLocalUser(ctx, account.CreateUserInput{Username: "plain", Password: "plain-password"})
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	routes := []struct{ method, path string }{
		{http.MethodGet, "/api/admin/backup"},
		{http.MethodPut, "/api/admin/backup"},
		{http.MethodPost, "/api/admin/backup/run"},
		{http.MethodPost, "/api/admin/backup/check"},
		{http.MethodGet, "/api/admin/backup/download"},
	}
	for _, rt := range routes {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(rt.method, rt.path, strings.NewReader(`{}`)))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("anonymous %s %s = %d, want 401", rt.method, rt.path, rec.Code)
		}
		rec = backupReq(t, mux, handler, user, rt.method, rt.path, `{"enabled":false}`)
		if rec.Code != http.StatusForbidden {
			t.Errorf("non-admin %s %s = %d, want 403", rt.method, rt.path, rec.Code)
		}
	}
}
