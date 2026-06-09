package oidc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Provider wraps a discovered OIDC issuer. The underlying go-oidc verifier holds
// a remote JWKS key set that is fetched lazily, cached, and refreshed on key
// rotation, so ID tokens are validated locally against the IdP's keys.
type Provider struct {
	cfg                Config
	oauth2             *oauth2.Config
	verifier           *gooidc.IDTokenVerifier
	endSessionEndpoint string
}

// Identity is the verified, IdP-agnostic view of an authenticated user.
type Identity struct {
	Subject           string
	Email             string
	Name              string
	PreferredUsername string
	Groups            []string
	Admin             bool
}

// Tokens is the result of a successful code exchange or refresh.
type Tokens struct {
	Identity      Identity
	RawIDToken    string
	RefreshToken  string
	IDTokenExpiry time.Time
}

// New performs OIDC discovery against the issuer and builds a Provider. All
// endpoints (authorization, token, JWKS, end_session) are derived from the
// discovery document — none are hardcoded.
func New(ctx context.Context, cfg Config) (*Provider, error) {
	provider, err := gooidc.NewProvider(ctx, cfg.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery for %q: %w", cfg.IssuerURL, err)
	}
	var discovery struct {
		EndSessionEndpoint string `json:"end_session_endpoint"`
	}
	// Best-effort: not every IdP advertises RP-initiated logout.
	_ = provider.Claims(&discovery)

	oauthCfg := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		RedirectURL:  cfg.RedirectURI,
		Endpoint:     provider.Endpoint(),
		Scopes:       cfg.Scopes,
	}
	return &Provider{
		cfg:                cfg,
		oauth2:             oauthCfg,
		verifier:           provider.Verifier(&gooidc.Config{ClientID: cfg.ClientID}),
		endSessionEndpoint: strings.TrimSpace(discovery.EndSessionEndpoint),
	}, nil
}

// Config returns the resolved configuration.
func (p *Provider) Config() Config { return p.cfg }

// AuthCodeURL builds the IdP redirect for the Authorization Code + PKCE flow.
// The caller is responsible for persisting state, nonce, and the PKCE verifier
// (in a short-lived signed cookie) and checking them on callback.
func (p *Provider) AuthCodeURL(state, nonce, pkceVerifier string) string {
	return p.oauth2.AuthCodeURL(state,
		gooidc.Nonce(nonce),
		oauth2.S256ChallengeOption(pkceVerifier),
		oauth2.AccessTypeOffline, // request a refresh token where the IdP supports it
	)
}

// Exchange completes the callback: it swaps the code (with the PKCE verifier)
// for tokens, validates the ID token locally against the JWKS (signature, iss,
// aud, exp), and verifies the nonce.
func (p *Provider) Exchange(ctx context.Context, code, pkceVerifier, expectedNonce string) (*Tokens, error) {
	tok, err := p.oauth2.Exchange(ctx, code, oauth2.VerifierOption(pkceVerifier))
	if err != nil {
		return nil, fmt.Errorf("token exchange: %w", err)
	}
	return p.tokensFrom(ctx, tok, expectedNonce)
}

// Refresh silently obtains a new ID token from a refresh token. A nonce is not
// re-checked (none is issued on refresh), but the new ID token is still fully
// validated against the JWKS.
func (p *Provider) Refresh(ctx context.Context, refreshToken string) (*Tokens, error) {
	src := p.oauth2.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken})
	tok, err := src.Token()
	if err != nil {
		return nil, fmt.Errorf("token refresh: %w", err)
	}
	return p.tokensFrom(ctx, tok, "")
}

func (p *Provider) tokensFrom(ctx context.Context, tok *oauth2.Token, expectedNonce string) (*Tokens, error) {
	rawIDToken, ok := tok.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return nil, errors.New("response did not contain an id_token")
	}
	idToken, err := p.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("verify id token: %w", err)
	}
	if expectedNonce != "" && idToken.Nonce != expectedNonce {
		return nil, errors.New("id token nonce mismatch")
	}

	var claims claimsJSON
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("decode id token claims: %w", err)
	}
	identity := Identity{
		Subject:           idToken.Subject,
		Email:             claims.Email,
		Name:              claims.displayName(),
		PreferredUsername: claims.PreferredUsername,
		Groups:            claims.groups(p.cfg.GroupsClaim),
	}
	identity.Admin = p.computeAdmin(identity)

	return &Tokens{
		Identity:      identity,
		RawIDToken:    rawIDToken,
		RefreshToken:  tok.RefreshToken,
		IDTokenExpiry: idToken.Expiry,
	}, nil
}

// LogoutURL returns the IdP's RP-initiated logout URL with a post-logout
// redirect, or "" when the IdP does not advertise an end_session_endpoint.
func (p *Provider) LogoutURL(idTokenHint string) string {
	if p.endSessionEndpoint == "" {
		return ""
	}
	u, err := url.Parse(p.endSessionEndpoint)
	if err != nil {
		return ""
	}
	q := u.Query()
	if idTokenHint != "" {
		q.Set("id_token_hint", idTokenHint)
	}
	if p.cfg.PostLogoutRedirectURI != "" {
		q.Set("post_logout_redirect_uri", p.cfg.PostLogoutRedirectURI)
		q.Set("client_id", p.cfg.ClientID)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// computeAdmin derives admin rights from the allowlists or the configured group
// claim. This is authorization, kept separate from authentication.
func (p *Provider) computeAdmin(id Identity) bool {
	for _, s := range p.cfg.AdminSubjects {
		if s == id.Subject {
			return true
		}
	}
	for _, e := range p.cfg.AdminEmails {
		if id.Email != "" && strings.EqualFold(e, id.Email) {
			return true
		}
	}
	if p.cfg.AdminGroup != "" {
		for _, g := range id.Groups {
			if g == p.cfg.AdminGroup {
				return true
			}
		}
	}
	return false
}

// claimsJSON captures the standard claims plus a flexible groups bucket. The
// groups claim name is configurable, so groups are pulled from a generic map.
type claimsJSON struct {
	Email             string `json:"email"`
	Name              string `json:"name"`
	GivenName         string `json:"given_name"`
	FamilyName        string `json:"family_name"`
	PreferredUsername string `json:"preferred_username"`

	extra map[string]any
}

func (c *claimsJSON) UnmarshalJSON(data []byte) error {
	type alias claimsJSON
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*c = claimsJSON(a)
	return json.Unmarshal(data, &c.extra)
}

func (c claimsJSON) displayName() string {
	if c.Name != "" {
		return c.Name
	}
	full := strings.TrimSpace(c.GivenName + " " + c.FamilyName)
	if full != "" {
		return full
	}
	return c.PreferredUsername
}

// groups extracts group/role names from the configured claim, accepting either
// a JSON array of strings or a single string.
func (c claimsJSON) groups(claim string) []string {
	raw, ok := c.extra[claim]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		if v == "" {
			return nil
		}
		return []string{v}
	default:
		return nil
	}
}
