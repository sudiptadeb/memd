package oidc

import (
	"reflect"
	"testing"
)

func TestConfigFromEnvUnsetIsNotConfigured(t *testing.T) {
	for _, k := range []string{EnvIssuerURL, EnvClientID, EnvClientSecret, EnvRedirectURI} {
		t.Setenv(k, "")
	}
	_, configured, err := ConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if configured {
		t.Fatalf("expected not configured when all OIDC env vars are empty")
	}
}

func TestConfigFromEnvPartialIsError(t *testing.T) {
	t.Setenv(EnvIssuerURL, "https://idp.example.com")
	t.Setenv(EnvClientID, "")
	t.Setenv(EnvClientSecret, "")
	t.Setenv(EnvRedirectURI, "")
	if _, _, err := ConfigFromEnv(); err == nil {
		t.Fatalf("expected error for partial OIDC configuration")
	}
}

func TestConfigFromEnvFull(t *testing.T) {
	t.Setenv(EnvIssuerURL, "https://idp.example.com/")
	t.Setenv(EnvClientID, "client")
	t.Setenv(EnvClientSecret, "secret")
	t.Setenv(EnvRedirectURI, "https://app/callback")
	t.Setenv(EnvAdminEmails, "a@x.com, b@x.com")
	t.Setenv(EnvGroupsClaim, "")

	cfg, configured, err := ConfigFromEnv()
	if err != nil || !configured {
		t.Fatalf("expected configured, got configured=%v err=%v", configured, err)
	}
	if cfg.IssuerURL != "https://idp.example.com" {
		t.Fatalf("issuer trailing slash not trimmed: %q", cfg.IssuerURL)
	}
	if cfg.GroupsClaim != DefaultGroupsClaim {
		t.Fatalf("groups claim default = %q", cfg.GroupsClaim)
	}
	if !reflect.DeepEqual(cfg.AdminEmails, []string{"a@x.com", "b@x.com"}) {
		t.Fatalf("admin emails = %v", cfg.AdminEmails)
	}
}

func TestParseScopesAlwaysIncludesOpenID(t *testing.T) {
	if got := ParseScopes(""); !reflect.DeepEqual(got, []string{"openid", "profile", "email"}) {
		t.Fatalf("default scopes = %v", got)
	}
	got := ParseScopes("profile email")
	if len(got) == 0 || got[0] != "openid" {
		t.Fatalf("expected openid to be prepended, got %v", got)
	}
}

func TestComputeAdmin(t *testing.T) {
	p := &Provider{cfg: Config{
		AdminSubjects: []string{"sub-1"},
		AdminEmails:   []string{"admin@example.com"},
		AdminGroup:    "memd-admins",
	}}
	cases := []struct {
		name string
		id   Identity
		want bool
	}{
		{"by subject", Identity{Subject: "sub-1"}, true},
		{"by email case-insensitive", Identity{Email: "Admin@Example.com"}, true},
		{"by group", Identity{Groups: []string{"other", "memd-admins"}}, true},
		{"none", Identity{Subject: "x", Email: "y@z.com", Groups: []string{"users"}}, false},
	}
	for _, tc := range cases {
		if got := p.computeAdmin(tc.id); got != tc.want {
			t.Fatalf("%s: computeAdmin = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestClaimsGroupsExtraction(t *testing.T) {
	var c claimsJSON
	if err := c.UnmarshalJSON([]byte(`{"roles":["a","b"],"single":"solo"}`)); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := c.groups("roles"); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("array groups = %v", got)
	}
	if got := c.groups("single"); !reflect.DeepEqual(got, []string{"solo"}) {
		t.Fatalf("string group = %v", got)
	}
	if got := c.groups("missing"); got != nil {
		t.Fatalf("missing groups = %v", got)
	}
}
