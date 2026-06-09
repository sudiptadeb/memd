// Package oidc implements IdP-agnostic OpenID Connect login for memd: the
// Authorization Code + PKCE flow, OIDC discovery, local ID-token validation
// against the IdP's JWKS, and claim-derived admin authorization. It contains no
// IdP-specific code paths — a deployer points it at any OIDC-compliant provider
// with four config values and everything else comes from discovery.
package oidc

import (
	"fmt"
	"os"
	"strings"
)

// Environment variables. The first four are the only required values; the rest
// tune scopes and logout.
const (
	EnvIssuerURL             = "OIDC_ISSUER_URL"    // discovery base; <issuer>/.well-known/openid-configuration is fetched
	EnvClientID              = "OIDC_CLIENT_ID"     //
	EnvClientSecret          = "OIDC_CLIENT_SECRET" //
	EnvRedirectURI           = "OIDC_REDIRECT_URI"  // must be registered at the IdP
	EnvScopes                = "OIDC_SCOPES"        // optional, space-separated; default "openid profile email"
	EnvPostLogoutRedirectURI = "OIDC_POST_LOGOUT_REDIRECT_URI"
)

// Config is the resolved OIDC configuration for one deployment (single IdP).
type Config struct {
	IssuerURL             string
	ClientID              string
	ClientSecret          string
	RedirectURI           string
	Scopes                []string
	PostLogoutRedirectURI string
}

// ConfigFromEnv reads the OIDC configuration from the environment. The bool
// reports whether OIDC is configured at all: when none of the OIDC variables
// are set it returns (zero, false, nil) and the caller falls back to local
// accounts. When the config is partially set, it returns an error so a
// misconfiguration is not silently ignored.
func ConfigFromEnv() (Config, bool, error) {
	issuer := strings.TrimSpace(os.Getenv(EnvIssuerURL))
	clientID := strings.TrimSpace(os.Getenv(EnvClientID))
	clientSecret := os.Getenv(EnvClientSecret)
	redirect := strings.TrimSpace(os.Getenv(EnvRedirectURI))

	if issuer == "" && clientID == "" && clientSecret == "" && redirect == "" {
		return Config{}, false, nil
	}

	var missing []string
	if issuer == "" {
		missing = append(missing, EnvIssuerURL)
	}
	if clientID == "" {
		missing = append(missing, EnvClientID)
	}
	if clientSecret == "" {
		missing = append(missing, EnvClientSecret)
	}
	if redirect == "" {
		missing = append(missing, EnvRedirectURI)
	}
	if len(missing) > 0 {
		return Config{}, false, fmt.Errorf("OIDC is partially configured; missing %s", strings.Join(missing, ", "))
	}

	cfg := Config{
		IssuerURL:             strings.TrimRight(issuer, "/"),
		ClientID:              clientID,
		ClientSecret:          clientSecret,
		RedirectURI:           redirect,
		Scopes:                ParseScopes(os.Getenv(EnvScopes)),
		PostLogoutRedirectURI: strings.TrimSpace(os.Getenv(EnvPostLogoutRedirectURI)),
	}
	return cfg, true, nil
}

// ParseScopes returns the configured scopes (space-separated), defaulting to
// the standard OIDC set, and always ensures "openid" is present.
func ParseScopes(raw string) []string {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		fields = []string{"openid", "profile", "email"}
	}
	hasOpenID := false
	for _, s := range fields {
		if s == "openid" {
			hasOpenID = true
			break
		}
	}
	if !hasOpenID {
		fields = append([]string{"openid"}, fields...)
	}
	return fields
}
