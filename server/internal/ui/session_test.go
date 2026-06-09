package ui

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSessionRoundTrip(t *testing.T) {
	sm, err := NewSessionManager("a-secret", time.Hour)
	if err != nil {
		t.Fatalf("NewSessionManager: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	want := sessionData{UserID: "usr_1", Issuer: "https://idp.example.com", Subject: "idp|x", Username: "ada", SuperAdmin: true}
	if err := sm.Issue(rec, req, want); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	cookie := rec.Result().Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie missing HttpOnly/SameSite: %+v", cookie)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.AddCookie(cookie)
	got, ok := sm.Read(req2)
	if !ok {
		t.Fatalf("Read failed for valid cookie")
	}
	if got.UserID != want.UserID || got.Issuer != want.Issuer || got.Subject != want.Subject || !got.SuperAdmin {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}

func TestSessionRejectsTamperedCookie(t *testing.T) {
	sm, err := NewSessionManager("a-secret", time.Hour)
	if err != nil {
		t.Fatalf("NewSessionManager: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if err := sm.Issue(rec, req, sessionData{UserID: "usr_1"}); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	cookie := rec.Result().Cookies()[0]
	// Flip a byte in the sealed value.
	tampered := []byte(cookie.Value)
	tampered[len(tampered)-1] ^= 0x01
	cookie.Value = string(tampered)

	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.AddCookie(cookie)
	if _, ok := sm.Read(req2); ok {
		t.Fatalf("tampered cookie was accepted")
	}
}

func TestSessionRejectsWrongKey(t *testing.T) {
	a, _ := NewSessionManager("secret-a", time.Hour)
	b, _ := NewSessionManager("secret-b", time.Hour)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if err := a.Issue(rec, req, sessionData{UserID: "usr_1"}); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	cookie := rec.Result().Cookies()[0]
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.AddCookie(cookie)
	if _, ok := b.Read(req2); ok {
		t.Fatalf("cookie sealed with a different key was accepted")
	}
}

func TestSessionExpiry(t *testing.T) {
	sm, _ := NewSessionManager("a-secret", time.Hour)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// Already-expired absolute lifetime.
	data := sessionData{UserID: "usr_1", IssuedAt: time.Now().Add(-2 * time.Hour), AbsoluteExpiry: time.Now().Add(-time.Hour)}
	if err := sm.Issue(rec, req, data); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	cookie := rec.Result().Cookies()[0]
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.AddCookie(cookie)
	if _, ok := sm.Read(req2); ok {
		t.Fatalf("expired session was accepted")
	}
}
