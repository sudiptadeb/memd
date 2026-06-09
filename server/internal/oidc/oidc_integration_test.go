package oidc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

// mockIdP is a minimal OIDC provider: discovery, JWKS, and a token endpoint
// whose output is selected by the authorization "code" so a single server can
// exercise valid, expired, wrong-nonce, and tampered cases.
type mockIdP struct {
	t      *testing.T
	server *httptest.Server
	key    *rsa.PrivateKey
	kid    string
	nonce  string
}

func newMockIdP(t *testing.T) *mockIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa key: %v", err)
	}
	idp := &mockIdP{t: t, key: key, kid: "test-key", nonce: "n0nce"}

	mux := http.NewServeMux()
	idp.server = httptest.NewServer(mux)

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		writeJSONResp(w, map[string]any{
			"issuer":                                idp.server.URL,
			"authorization_endpoint":                idp.server.URL + "/authorize",
			"token_endpoint":                        idp.server.URL + "/token",
			"jwks_uri":                              idp.server.URL + "/jwks",
			"end_session_endpoint":                  idp.server.URL + "/logout",
			"id_token_signing_alg_values_supported": []string{"RS256"},
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		set := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
			Key:       &key.PublicKey,
			KeyID:     idp.kid,
			Algorithm: "RS256",
			Use:       "sig",
		}}}
		writeJSONResp(w, set)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		code := r.Form.Get("code")
		if r.Form.Get("grant_type") == "refresh_token" {
			code = "ok" // refreshed ID token
		}
		idToken := idp.mintForCode(code)
		writeJSONResp(w, map[string]any{
			"access_token":  "access-token",
			"token_type":    "Bearer",
			"refresh_token": "refresh-token",
			"expires_in":    3600,
			"id_token":      idToken,
		})
	})
	t.Cleanup(idp.server.Close)
	return idp
}

func (idp *mockIdP) mintForCode(code string) string {
	now := time.Now()
	claims := map[string]any{
		"iss":    idp.server.URL,
		"sub":    "idp|user-1",
		"aud":    "client",
		"iat":    now.Unix(),
		"exp":    now.Add(time.Hour).Unix(),
		"nonce":  idp.nonce,
		"email":  "ada@example.com",
		"name":   "Ada Lovelace",
		"groups": []string{"users", "admins"},
	}
	switch code {
	case "expired":
		claims["exp"] = now.Add(-time.Hour).Unix()
	case "badnonce":
		claims["nonce"] = "wrong"
	}
	token := idp.sign(claims)
	if code == "tampered" {
		// Corrupt the payload segment so the signature no longer matches.
		parts := strings.Split(token, ".")
		parts[1] = parts[1][:len(parts[1])-2] + "AA"
		token = strings.Join(parts, ".")
	}
	return token
}

func (idp *mockIdP) sign(claims map[string]any) string {
	idp.t.Helper()
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: idp.key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", idp.kid),
	)
	if err != nil {
		idp.t.Fatalf("new signer: %v", err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		idp.t.Fatalf("marshal claims: %v", err)
	}
	obj, err := signer.Sign(payload)
	if err != nil {
		idp.t.Fatalf("sign: %v", err)
	}
	serialized, err := obj.CompactSerialize()
	if err != nil {
		idp.t.Fatalf("serialize: %v", err)
	}
	return serialized
}

func (idp *mockIdP) provider(t *testing.T) *Provider {
	t.Helper()
	p, err := New(context.Background(), Config{
		IssuerURL:    idp.server.URL,
		ClientID:     "client",
		ClientSecret: "secret",
		RedirectURI:  "https://app.example.com/auth/callback",
		Scopes:       []string{"openid", "profile", "email"},
		GroupsClaim:  "groups",
		AdminGroup:   "admins",
	})
	if err != nil {
		t.Fatalf("New provider (discovery): %v", err)
	}
	return p
}

func writeJSONResp(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func TestProviderAuthCodeURLHasPKCEAndNonce(t *testing.T) {
	idp := newMockIdP(t)
	p := idp.provider(t)
	u := p.AuthCodeURL("state-123", "nonce-abc", "verifier-value-1234567890")
	for _, want := range []string{"state=state-123", "nonce=nonce-abc", "code_challenge=", "code_challenge_method=S256"} {
		if !strings.Contains(u, want) {
			t.Fatalf("auth URL missing %q: %s", want, u)
		}
	}
}

func TestExchangeValidToken(t *testing.T) {
	idp := newMockIdP(t)
	p := idp.provider(t)
	tokens, err := p.Exchange(context.Background(), "ok", "verifier-value-1234567890", idp.nonce)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if tokens.Identity.Subject != "idp|user-1" {
		t.Fatalf("subject = %q", tokens.Identity.Subject)
	}
	if tokens.Identity.Email != "ada@example.com" || tokens.Identity.Name != "Ada Lovelace" {
		t.Fatalf("identity display fields wrong: %+v", tokens.Identity)
	}
	if !tokens.Identity.Admin {
		t.Fatalf("expected admin via group claim")
	}
	if tokens.RefreshToken != "refresh-token" {
		t.Fatalf("refresh token = %q", tokens.RefreshToken)
	}
}

func TestExchangeRejectsExpiredToken(t *testing.T) {
	idp := newMockIdP(t)
	p := idp.provider(t)
	if _, err := p.Exchange(context.Background(), "expired", "verifier-value-1234567890", idp.nonce); err == nil {
		t.Fatalf("expected expired token to be rejected")
	}
}

func TestExchangeRejectsWrongNonce(t *testing.T) {
	idp := newMockIdP(t)
	p := idp.provider(t)
	if _, err := p.Exchange(context.Background(), "badnonce", "verifier-value-1234567890", idp.nonce); err == nil {
		t.Fatalf("expected nonce mismatch to be rejected")
	}
}

func TestExchangeRejectsTamperedToken(t *testing.T) {
	idp := newMockIdP(t)
	p := idp.provider(t)
	if _, err := p.Exchange(context.Background(), "tampered", "verifier-value-1234567890", idp.nonce); err == nil {
		t.Fatalf("expected tampered (bad signature) token to be rejected")
	}
}

func TestRefreshIssuesNewToken(t *testing.T) {
	idp := newMockIdP(t)
	p := idp.provider(t)
	tokens, err := p.Refresh(context.Background(), "refresh-token")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if tokens.Identity.Subject != "idp|user-1" {
		t.Fatalf("refreshed subject = %q", tokens.Identity.Subject)
	}
}

func TestLogoutURL(t *testing.T) {
	idp := newMockIdP(t)
	p, err := New(context.Background(), Config{
		IssuerURL:             idp.server.URL,
		ClientID:              "client",
		ClientSecret:          "secret",
		RedirectURI:           "https://app.example.com/auth/callback",
		Scopes:                []string{"openid"},
		PostLogoutRedirectURI: "https://app.example.com/",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := p.LogoutURL("id-token-hint")
	if !strings.Contains(got, "/logout") || !strings.Contains(got, "post_logout_redirect_uri=") || !strings.Contains(got, "id_token_hint=id-token-hint") {
		t.Fatalf("logout URL wrong: %s", got)
	}
}
