package account

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

const settingKeyOIDC = "oidc"

// OIDCSettings is the persisted, super-admin-editable OIDC configuration. It is
// stored as a JSON blob in app_settings and is the source of truth for the
// running provider (env vars only seed it on first boot). The client secret is
// stored here alongside the other control-plane secrets in memd.db.
type OIDCSettings struct {
	Enabled               bool     `json:"enabled"`
	IssuerURL             string   `json:"issuer_url"`
	ClientID              string   `json:"client_id"`
	ClientSecret          string   `json:"client_secret"`
	RedirectURI           string   `json:"redirect_uri"`
	Scopes                string   `json:"scopes"`
	GroupsClaim           string   `json:"groups_claim"`
	AdminGroup            string   `json:"admin_group"`
	AdminSubjects         []string `json:"admin_subjects"`
	AdminEmails           []string `json:"admin_emails"`
	PostLogoutRedirectURI string   `json:"post_logout_redirect_uri"`
}

// GetOIDCSettings returns the stored OIDC settings. The bool is false when no
// configuration has been saved yet.
func (s *Store) GetOIDCSettings(ctx context.Context) (OIDCSettings, bool, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = ?`, settingKeyOIDC).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return OIDCSettings{}, false, nil
	}
	if err != nil {
		return OIDCSettings{}, false, err
	}
	var cfg OIDCSettings
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return OIDCSettings{}, false, err
	}
	return cfg, true, nil
}

// SaveOIDCSettings persists the OIDC settings.
func (s *Store) SaveOIDCSettings(ctx context.Context, cfg OIDCSettings) error {
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO app_settings(key, value, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		settingKeyOIDC, string(data), nowString())
	return err
}
