package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sudiptadeb/memd/server/internal/account"
)

func TestAppPairRedeemAndSessionFlow(t *testing.T) {
	accounts := openTestAccountStore(t)
	user, err := accounts.CreateLocalUser(context.Background(), account.CreateUserInput{Username: "ada", Password: "correct horse battery staple"})
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	mux, handler := newTestUI(t, accounts)

	// Pair requires a signed-in session.
	anonRec := httptest.NewRecorder()
	mux.ServeHTTP(anonRec, httptest.NewRequest(http.MethodPost, "/api/app/pair", nil))
	if anonRec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous pair status = %d, want 401", anonRec.Code)
	}

	code := mintPairCode(t, mux, handler, user)
	if len(code) != pairingCodeLength {
		t.Fatalf("code = %q, want %d chars", code, pairingCodeLength)
	}
	for _, r := range code {
		if !strings.ContainsRune(pairingCodeAlphabet, r) {
			t.Fatalf("code %q holds %q, outside the pairing alphabet", code, r)
		}
	}

	// Redeem without any cookie, using the grouped lower-case form the app may
	// pass through verbatim.
	grouped := strings.ToLower(code[:3] + "-" + code[3:6] + "-" + code[6:])
	redeemRec := httptest.NewRecorder()
	mux.ServeHTTP(redeemRec, httptest.NewRequest(http.MethodPost, "/api/app/redeem",
		bytes.NewBufferString(`{"code":"`+grouped+`","label":"Pixel 9"}`)))
	if redeemRec.Code != http.StatusOK {
		t.Fatalf("redeem status = %d, body=%s", redeemRec.Code, redeemRec.Body.String())
	}
	var redeemBody struct {
		Token string `json:"token"`
		User  struct {
			ID       string `json:"id"`
			Username string `json:"username"`
		} `json:"user"`
	}
	if err := json.Unmarshal(redeemRec.Body.Bytes(), &redeemBody); err != nil {
		t.Fatalf("redeem JSON: %v", err)
	}
	if !strings.HasPrefix(redeemBody.Token, "mat_") {
		t.Fatalf("token = %q, want mat_ prefix", redeemBody.Token)
	}
	if redeemBody.User.ID != user.ID || redeemBody.User.Username != "ada" {
		t.Fatalf("redeem user = %+v, want ada", redeemBody.User)
	}
	// Redeem sets a usable session cookie.
	sessionCookie := cookieByName(t, redeemRec, sessionCookieName)
	if data, ok := handler.sessions.Read(requestWithCookie(sessionCookie)); !ok || data.UserID != user.ID {
		t.Fatalf("redeem session cookie unreadable or wrong user: ok=%v data=%+v", ok, data)
	}

	// The same code is single-use: a second redeem is a 401.
	secondRec := httptest.NewRecorder()
	mux.ServeHTTP(secondRec, httptest.NewRequest(http.MethodPost, "/api/app/redeem",
		bytes.NewBufferString(`{"code":"`+code+`","label":"again"}`)))
	if secondRec.Code != http.StatusUnauthorized {
		t.Fatalf("second redeem status = %d, want 401", secondRec.Code)
	}

	// The token trades for a fresh session cookie.
	sessRec := httptest.NewRecorder()
	sessReq := httptest.NewRequest(http.MethodPost, "/api/app/session", nil)
	sessReq.Header.Set("Authorization", "Bearer "+redeemBody.Token)
	mux.ServeHTTP(sessRec, sessReq)
	if sessRec.Code != http.StatusOK {
		t.Fatalf("session status = %d, body=%s", sessRec.Code, sessRec.Body.String())
	}
	fresh := cookieByName(t, sessRec, sessionCookieName)
	if data, ok := handler.sessions.Read(requestWithCookie(fresh)); !ok || data.UserID != user.ID {
		t.Fatalf("app session cookie unreadable or wrong user: ok=%v data=%+v", ok, data)
	}
	// The fresh cookie authenticates a normal session-guarded API.
	listReq := httptest.NewRequest(http.MethodGet, "/api/app/tokens", nil)
	listReq.AddCookie(fresh)
	listRec := httptest.NewRecorder()
	mux.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list with app-session cookie status = %d, body=%s", listRec.Code, listRec.Body.String())
	}
	if !strings.Contains(listRec.Body.String(), "Pixel 9") {
		t.Fatalf("token list missing label: %s", listRec.Body.String())
	}
	// /api/app/session must update last_used_at.
	var listBody struct {
		Tokens []struct {
			ID         string `json:"id"`
			LastUsedAt string `json:"last_used_at"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("list JSON: %v", err)
	}
	if len(listBody.Tokens) != 1 || listBody.Tokens[0].LastUsedAt == "" {
		t.Fatalf("tokens after session = %+v, want one with last_used_at", listBody.Tokens)
	}
}

func TestAppRedeemRejectsBadExpiredAndReplacedCodes(t *testing.T) {
	accounts := openTestAccountStore(t)
	user, err := accounts.CreateLocalUser(context.Background(), account.CreateUserInput{Username: "ada", Password: "correct horse battery staple"})
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	mux, handler := newTestUI(t, accounts)

	// Garbage code.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/app/redeem", bytes.NewBufferString(`{"code":"NOPE-NOPE","label":"x"}`)))
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "invalid or expired code") {
		t.Fatalf("garbage redeem = %d %s, want 401 invalid or expired code", rec.Code, rec.Body.String())
	}

	// A new mint replaces the previous code.
	old := mintPairCode(t, mux, handler, user)
	current := mintPairCode(t, mux, handler, user)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/app/redeem", bytes.NewBufferString(`{"code":"`+old+`","label":"x"}`)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("replaced code redeem status = %d, want 401", rec.Code)
	}

	// An expired code is a 401 even though it is still stored.
	handler.appPairing.mu.Lock()
	entry := handler.appPairing.byCode[current]
	entry.expiresAt = time.Now().Add(-time.Second)
	handler.appPairing.byCode[current] = entry
	handler.appPairing.mu.Unlock()
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/app/redeem", bytes.NewBufferString(`{"code":"`+current+`","label":"x"}`)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expired code redeem status = %d, want 401", rec.Code)
	}
}

func TestAppRedeemThrottledPerIP(t *testing.T) {
	accounts := openTestAccountStore(t)
	mux, _ := newTestUI(t, accounts)

	status := func(remoteAddr string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/app/redeem", bytes.NewBufferString(`{"code":"WRONGCODE","label":"x"}`))
		req.RemoteAddr = remoteAddr
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}
	for i := 0; i < redeemAttemptsPerWindow; i++ {
		if got := status("10.0.0.9:1234"); got != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want 401", i, got)
		}
	}
	if got := status("10.0.0.9:1234"); got != http.StatusTooManyRequests {
		t.Fatalf("over-limit status = %d, want 429", got)
	}
	// Another IP is unaffected.
	if got := status("10.0.0.10:1234"); got != http.StatusUnauthorized {
		t.Fatalf("other IP status = %d, want 401", got)
	}
}

func TestAppSessionRejectsUnknownRevokedAndDisabled(t *testing.T) {
	ctx := context.Background()
	accounts := openTestAccountStore(t)
	user, err := accounts.CreateLocalUser(ctx, account.CreateUserInput{Username: "ada", Password: "correct horse battery staple"})
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	mux, _ := newTestUI(t, accounts)

	trySession := func(token string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/app/session", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}

	if got := trySession(""); got != http.StatusUnauthorized {
		t.Fatalf("no bearer status = %d, want 401", got)
	}
	if got := trySession("mat_not-a-real-token"); got != http.StatusUnauthorized {
		t.Fatalf("unknown token status = %d, want 401", got)
	}

	tok, secret, err := accounts.CreateAppToken(ctx, user.ID, "phone")
	if err != nil {
		t.Fatalf("CreateAppToken: %v", err)
	}
	if got := trySession(secret); got != http.StatusOK {
		t.Fatalf("valid token status = %d, want 200", got)
	}
	if err := accounts.RevokeAppToken(ctx, user.ID, tok.ID); err != nil {
		t.Fatalf("RevokeAppToken: %v", err)
	}
	if got := trySession(secret); got != http.StatusUnauthorized {
		t.Fatalf("revoked token status = %d, want 401", got)
	}

	// A disabled user's still-active token stops working too.
	_, secret2, err := accounts.CreateAppToken(ctx, user.ID, "phone-2")
	if err != nil {
		t.Fatalf("CreateAppToken 2: %v", err)
	}
	if err := accounts.SetUserDisabled(ctx, user.ID, true); err != nil {
		t.Fatalf("SetUserDisabled: %v", err)
	}
	if got := trySession(secret2); got != http.StatusUnauthorized {
		t.Fatalf("disabled user token status = %d, want 401", got)
	}
}

func TestAppTokenListAndDelete(t *testing.T) {
	ctx := context.Background()
	accounts := openTestAccountStore(t)
	ada, err := accounts.CreateLocalUser(ctx, account.CreateUserInput{Username: "ada", Password: "correct horse battery staple"})
	if err != nil {
		t.Fatalf("CreateLocalUser ada: %v", err)
	}
	bob, err := accounts.CreateLocalUser(ctx, account.CreateUserInput{Username: "bob", Password: "correct horse battery staple"})
	if err != nil {
		t.Fatalf("CreateLocalUser bob: %v", err)
	}
	mux, handler := newTestUI(t, accounts)

	tok, secret, err := accounts.CreateAppToken(ctx, ada.ID, "Pixel 9")
	if err != nil {
		t.Fatalf("CreateAppToken: %v", err)
	}

	// The list is empty-array (not null) for a user with no tokens.
	emptyReq := httptest.NewRequest(http.MethodGet, "/api/app/tokens", nil)
	addSession(t, handler, emptyReq, bob)
	emptyRec := httptest.NewRecorder()
	mux.ServeHTTP(emptyRec, emptyReq)
	if emptyRec.Code != http.StatusOK || !strings.Contains(emptyRec.Body.String(), `"tokens":[]`) {
		t.Fatalf("empty list = %d %s, want 200 with empty array", emptyRec.Code, emptyRec.Body.String())
	}

	// Bob cannot delete ada's token.
	crossReq := httptest.NewRequest(http.MethodDelete, "/api/app/tokens/"+tok.ID, nil)
	addSession(t, handler, crossReq, bob)
	crossRec := httptest.NewRecorder()
	mux.ServeHTTP(crossRec, crossReq)
	if crossRec.Code != http.StatusNotFound {
		t.Fatalf("cross-user delete status = %d, want 404", crossRec.Code)
	}

	// Ada revokes her phone from the dashboard.
	delReq := httptest.NewRequest(http.MethodDelete, "/api/app/tokens/"+tok.ID, nil)
	addSession(t, handler, delReq, ada)
	delRec := httptest.NewRecorder()
	mux.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, body=%s", delRec.Code, delRec.Body.String())
	}
	if _, err := accounts.AppTokenByToken(ctx, secret); err == nil {
		t.Fatalf("token still resolves after dashboard revoke")
	}

	// Anonymous delete by id is rejected.
	anonRec := httptest.NewRecorder()
	mux.ServeHTTP(anonRec, httptest.NewRequest(http.MethodDelete, "/api/app/tokens/"+tok.ID, nil))
	if anonRec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous delete status = %d, want 401", anonRec.Code)
	}
}

func TestAppTokenDeleteSelfUsesBearer(t *testing.T) {
	ctx := context.Background()
	accounts := openTestAccountStore(t)
	user, err := accounts.CreateLocalUser(ctx, account.CreateUserInput{Username: "ada", Password: "correct horse battery staple"})
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	mux, _ := newTestUI(t, accounts)

	_, secret, err := accounts.CreateAppToken(ctx, user.ID, "phone")
	if err != nil {
		t.Fatalf("CreateAppToken: %v", err)
	}

	// No bearer: 401.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/app/tokens/self", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("self delete without bearer status = %d, want 401", rec.Code)
	}

	// Bearer sign-out revokes the token.
	req := httptest.NewRequest(http.MethodDelete, "/api/app/tokens/self", nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("self delete status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if _, err := accounts.AppTokenByToken(ctx, secret); err == nil {
		t.Fatalf("token still resolves after self revoke")
	}
	// Repeating the sign-out (already revoked) is a 401, not a crash.
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("second self delete status = %d, want 401", rec.Code)
	}
}

// mintPairCode signs in as user (session cookie) and mints a pairing code.
func mintPairCode(t *testing.T, mux *http.ServeMux, handler *Handler, user account.User) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/app/pair", nil)
	addSession(t, handler, req, user)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("pair status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Code      string `json:"code"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("pair JSON: %v", err)
	}
	if body.Code == "" || body.ExpiresAt == "" {
		t.Fatalf("pair response missing fields: %s", rec.Body.String())
	}
	exp, err := time.Parse(time.RFC3339, body.ExpiresAt)
	if err != nil {
		t.Fatalf("pair expires_at %q: %v", body.ExpiresAt, err)
	}
	if until := time.Until(exp); until <= 0 || until > pairingCodeTTL+time.Minute {
		t.Fatalf("pair expires_at %q out of the expected window", body.ExpiresAt)
	}
	return body.Code
}

func cookieByName(t *testing.T, rec *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == name && c.Value != "" {
			return c
		}
	}
	t.Fatalf("response did not set cookie %q", name)
	return nil
}

func requestWithCookie(c *http.Cookie) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(c)
	return req
}
