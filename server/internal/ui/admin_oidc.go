package ui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/sudiptadeb/memd/server/internal/account"
	"github.com/sudiptadeb/memd/server/internal/logs"
	"github.com/sudiptadeb/memd/server/internal/oidc"
)

// oidcConfigView is the admin-facing representation of the OIDC settings. The
// client secret is never sent back to the browser; only its presence is.
type oidcConfigView struct {
	Enabled               bool   `json:"enabled"`
	IssuerURL             string `json:"issuer_url"`
	ClientID              string `json:"client_id"`
	HasClientSecret       bool   `json:"has_client_secret"`
	RedirectURI           string `json:"redirect_uri"`
	Scopes                string `json:"scopes"`
	PostLogoutRedirectURI string `json:"post_logout_redirect_uri"`
	Active                bool   `json:"active"` // provider currently loaded
}

// adminOIDCAPI lets a super admin read and update the IdP configuration, which
// is persisted in the database and applied to the running provider live.
func (h *Handler) adminOIDCAPI(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		settings, _, err := h.accounts.GetOIDCSettings(r.Context())
		if err != nil {
			httpErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"oidc": viewFromSettings(settings, h.oidcEnabled())})
	case http.MethodPut:
		h.updateOIDC(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *Handler) updateOIDC(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled               bool    `json:"enabled"`
		IssuerURL             string  `json:"issuer_url"`
		ClientID              string  `json:"client_id"`
		ClientSecret          *string `json:"client_secret"` // pointer: nil/absent keeps the stored secret
		RedirectURI           string  `json:"redirect_uri"`
		Scopes                string  `json:"scopes"`
		PostLogoutRedirectURI string  `json:"post_logout_redirect_uri"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpErr(w, http.StatusBadRequest, err)
		return
	}

	// Start from the stored settings so an omitted client secret is preserved.
	current, _, err := h.accounts.GetOIDCSettings(r.Context())
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err)
		return
	}
	settings := account.OIDCSettings{
		Enabled:               body.Enabled,
		IssuerURL:             strings.TrimSpace(body.IssuerURL),
		ClientID:              strings.TrimSpace(body.ClientID),
		ClientSecret:          current.ClientSecret,
		RedirectURI:           strings.TrimSpace(body.RedirectURI),
		Scopes:                strings.TrimSpace(body.Scopes),
		PostLogoutRedirectURI: strings.TrimSpace(body.PostLogoutRedirectURI),
	}
	if body.ClientSecret != nil {
		settings.ClientSecret = *body.ClientSecret
	}

	if !settings.Enabled {
		if err := h.accounts.SaveOIDCSettings(r.Context(), settings); err != nil {
			httpErr(w, http.StatusInternalServerError, err)
			return
		}
		h.oidc.Disable()
		logs.Info("oidc disabled by super admin")
		writeJSON(w, http.StatusOK, map[string]any{"oidc": viewFromSettings(settings, false)})
		return
	}

	if settings.IssuerURL == "" || settings.ClientID == "" || settings.RedirectURI == "" || settings.ClientSecret == "" {
		httpErr(w, http.StatusBadRequest, errors.New("issuer URL, client ID, client secret, and redirect URI are required"))
		return
	}

	// Validate by performing discovery before persisting, and swap the live
	// provider only if it succeeds.
	if err := h.oidc.Configure(r.Context(), configFromSettings(settings)); err != nil {
		httpErr(w, http.StatusBadGateway, err)
		return
	}
	if err := h.accounts.SaveOIDCSettings(r.Context(), settings); err != nil {
		httpErr(w, http.StatusInternalServerError, err)
		return
	}
	logs.Info("oidc configured by super admin (issuer=%s)", settings.IssuerURL)
	writeJSON(w, http.StatusOK, map[string]any{"oidc": viewFromSettings(settings, true)})
}

// configFromSettings maps the persisted settings to a normalized oidc.Config.
func configFromSettings(s account.OIDCSettings) oidc.Config {
	return oidc.Config{
		IssuerURL:             strings.TrimRight(strings.TrimSpace(s.IssuerURL), "/"),
		ClientID:              strings.TrimSpace(s.ClientID),
		ClientSecret:          s.ClientSecret,
		RedirectURI:           strings.TrimSpace(s.RedirectURI),
		Scopes:                oidc.ParseScopes(s.Scopes),
		PostLogoutRedirectURI: strings.TrimSpace(s.PostLogoutRedirectURI),
	}
}

func viewFromSettings(s account.OIDCSettings, active bool) oidcConfigView {
	return oidcConfigView{
		Enabled:               s.Enabled,
		IssuerURL:             s.IssuerURL,
		ClientID:              s.ClientID,
		HasClientSecret:       s.ClientSecret != "",
		RedirectURI:           s.RedirectURI,
		Scopes:                s.Scopes,
		PostLogoutRedirectURI: s.PostLogoutRedirectURI,
		Active:                active,
	}
}

// LoadOIDCFromStore initializes the manager from persisted settings at startup,
// seeding them from environment variables on first boot when none are stored.
// It returns the manager (OIDC may be disabled) and never fails startup on a
// provider/discovery error — it logs and leaves OIDC disabled so the local
// login path keeps working.
func LoadOIDCFromStore(ctx context.Context, store *account.Store) *oidc.Manager {
	mgr := oidc.NewManager()
	settings, ok, err := store.GetOIDCSettings(ctx)
	if err != nil {
		logs.Warn("read oidc settings: %v", err)
		return mgr
	}
	if !ok {
		// First boot: seed from environment if provided.
		if seeded, seededOK := seedOIDCFromEnv(); seededOK {
			settings = seeded
			if err := store.SaveOIDCSettings(ctx, settings); err != nil {
				logs.Warn("seed oidc settings from env: %v", err)
			}
		}
	}
	if !settings.Enabled {
		return mgr
	}
	if err := mgr.Configure(ctx, configFromSettings(settings)); err != nil {
		logs.Warn("oidc discovery failed; SSO disabled until reconfigured: %v", err)
	}
	return mgr
}

// seedOIDCFromEnv converts environment configuration into persisted settings.
func seedOIDCFromEnv() (account.OIDCSettings, bool) {
	cfg, configured, err := oidc.ConfigFromEnv()
	if err != nil || !configured {
		if err != nil {
			logs.Warn("oidc env config: %v", err)
		}
		return account.OIDCSettings{}, false
	}
	return account.OIDCSettings{
		Enabled:               true,
		IssuerURL:             cfg.IssuerURL,
		ClientID:              cfg.ClientID,
		ClientSecret:          cfg.ClientSecret,
		RedirectURI:           cfg.RedirectURI,
		Scopes:                strings.Join(cfg.Scopes, " "),
		PostLogoutRedirectURI: cfg.PostLogoutRedirectURI,
	}, true
}
