package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sudiptadeb/memd/server/internal/account"
)

// fakeRC stands in for *tunnel.Handler behind the RCController interface.
type fakeRC struct {
	enabled  bool
	viewHost string
}

func (f *fakeRC) Enabled() bool      { return f.enabled }
func (f *fakeRC) SetEnabled(on bool) { f.enabled = on }
func (f *fakeRC) ViewHost() string   { return f.viewHost }

func decodeRCView(t *testing.T, rec *httptest.ResponseRecorder) rcConfigView {
	t.Helper()
	var resp struct {
		RC rcConfigView `json:"rc"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode rc view: %v (body=%s)", err, rec.Body.String())
	}
	return resp.RC
}

func sessionRC(t *testing.T, mux *http.ServeMux) bool {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/session", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/session = %d", rec.Code)
	}
	var resp struct {
		Features struct {
			RC bool `json:"rc"`
		} `json:"features"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	return resp.Features.RC
}

// TestAdminRCAPI: the super-admin toggle — default on when nothing is stored,
// PUT persists and applies to the live controller immediately, and
// /api/session reflects the EFFECTIVE state throughout.
func TestAdminRCAPI(t *testing.T) {
	ctx := context.Background()
	accounts := openTestAccountStore(t)
	admin, err := accounts.CreateSuperAdmin(ctx, "admin", "correct horse battery staple")
	if err != nil {
		t.Fatalf("CreateSuperAdmin: %v", err)
	}
	mux, handler := newTestUI(t, accounts)
	rc := &fakeRC{enabled: true}
	handler.SetRC(rc, false, true)

	adminReq := func(method, body string) *httptest.ResponseRecorder {
		t.Helper()
		var req *http.Request
		if body == "" {
			req = httptest.NewRequest(method, "/api/admin/rc", nil)
		} else {
			req = httptest.NewRequest(method, "/api/admin/rc", strings.NewReader(body))
		}
		addSession(t, handler, req, admin)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	// Nothing stored yet: default on, active, no kill switch, key available.
	rec := adminReq(http.MethodGet, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d, body=%s", rec.Code, rec.Body.String())
	}
	view := decodeRCView(t, rec)
	if !view.Enabled || !view.Active || view.KillSwitch || !view.Available {
		t.Fatalf("default view = %+v, want enabled+active", view)
	}
	if !sessionRC(t, mux) {
		t.Fatal("features.rc = false before any toggle, want true")
	}

	// Toggle off: persisted, applied to the controller, session flips.
	rec = adminReq(http.MethodPut, `{"enabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT off = %d, body=%s", rec.Code, rec.Body.String())
	}
	view = decodeRCView(t, rec)
	if view.Enabled || view.Active {
		t.Fatalf("after PUT off view = %+v, want disabled+inactive", view)
	}
	if rc.enabled {
		t.Fatal("controller not disabled by PUT")
	}
	if stored, ok, err := accounts.GetRCSettings(ctx); err != nil || !ok || stored.Enabled {
		t.Fatalf("stored setting after PUT off: %+v ok=%v err=%v, want persisted false", stored, ok, err)
	}
	if sessionRC(t, mux) {
		t.Fatal("features.rc = true after disable, want false")
	}
	rec = adminReq(http.MethodGet, "")
	if view = decodeRCView(t, rec); view.Enabled || view.Active {
		t.Fatalf("GET after disable = %+v, want disabled", view)
	}

	// Toggle back on.
	rec = adminReq(http.MethodPut, `{"enabled":true}`)
	if view = decodeRCView(t, rec); !view.Enabled || !view.Active {
		t.Fatalf("after PUT on view = %+v, want enabled+active", view)
	}
	if !rc.enabled {
		t.Fatal("controller not re-enabled by PUT")
	}
	if !sessionRC(t, mux) {
		t.Fatal("features.rc = false after re-enable, want true")
	}
}

// TestAdminRCAPIKillSwitch: with the feature unable to run (MEMD_RC=0 kill
// switch — controller nil), the toggle still persists for later but the
// effective state stays off and is reported honestly.
func TestAdminRCAPIKillSwitch(t *testing.T) {
	ctx := context.Background()
	accounts := openTestAccountStore(t)
	admin, err := accounts.CreateSuperAdmin(ctx, "admin", "correct horse battery staple")
	if err != nil {
		t.Fatalf("CreateSuperAdmin: %v", err)
	}
	mux, handler := newTestUI(t, accounts)
	handler.SetRC(nil, true, true)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/rc", nil)
	addSession(t, handler, req, admin)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	view := decodeRCView(t, rec)
	if !view.Enabled || view.Active || !view.KillSwitch {
		t.Fatalf("kill-switch GET = %+v, want enabled(stored default) but inactive with kill_switch", view)
	}
	if sessionRC(t, mux) {
		t.Fatal("features.rc = true under kill switch, want false")
	}

	// PUT persists (so the choice applies once the env allows) but cannot
	// activate anything.
	req = httptest.NewRequest(http.MethodPut, "/api/admin/rc", strings.NewReader(`{"enabled":true}`))
	addSession(t, handler, req, admin)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if view = decodeRCView(t, rec); view.Active {
		t.Fatalf("kill-switch PUT view = %+v, want inactive", view)
	}
	if stored, ok, err := accounts.GetRCSettings(ctx); err != nil || !ok || !stored.Enabled {
		t.Fatalf("stored setting under kill switch: %+v ok=%v err=%v, want persisted true", stored, ok, err)
	}
}

// TestAdminRCAPIRequiresSuperAdmin: a regular user is refused, anonymous is
// unauthenticated.
func TestAdminRCAPIRequiresSuperAdmin(t *testing.T) {
	ctx := context.Background()
	accounts := openTestAccountStore(t)
	if _, err := accounts.CreateSuperAdmin(ctx, "admin", "correct horse battery staple"); err != nil {
		t.Fatalf("CreateSuperAdmin: %v", err)
	}
	user, err := accounts.CreateLocalUser(ctx, account.CreateUserInput{Username: "plain", Password: "plain-password"})
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	mux, handler := newTestUI(t, accounts)
	handler.SetRC(&fakeRC{enabled: true}, false, true)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/admin/rc", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous GET = %d, want 401", rec.Code)
	}

	for _, method := range []string{http.MethodGet, http.MethodPut} {
		req := httptest.NewRequest(method, "/api/admin/rc", strings.NewReader(`{"enabled":false}`))
		addSession(t, handler, req, user)
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("non-admin %s = %d, want 403", method, rec.Code)
		}
	}
}
