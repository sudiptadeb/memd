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

	cfg, configured, err := ConfigFromEnv()
	if err != nil || !configured {
		t.Fatalf("expected configured, got configured=%v err=%v", configured, err)
	}
	if cfg.IssuerURL != "https://idp.example.com" {
		t.Fatalf("issuer trailing slash not trimmed: %q", cfg.IssuerURL)
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
