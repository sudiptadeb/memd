package account

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const (
	settingKeyOIDC         = "oidc"
	settingKeyRC           = "rc"
	settingKeyBackup       = "backup"
	settingKeyBackupStatus = "backup_status"
)

// OIDCSettings is the persisted, super-admin-editable OIDC configuration. It is
// stored as a JSON blob in app_settings and is the source of truth for the
// running provider (env vars only seed it on first boot). The client secret is
// stored here alongside the other control-plane secrets in memd.db.
type OIDCSettings struct {
	Enabled bool `json:"enabled"`
	// ProviderID is the stable identity boundary for this provider slot. It is
	// minted once and survives issuer-URL edits, so users stay linked when the
	// same IdP moves to a new domain. Replacing the provider with a genuinely
	// different IdP mints a new id (and with it, fresh accounts).
	ProviderID            string `json:"provider_id,omitempty"`
	IssuerURL             string `json:"issuer_url"`
	ClientID              string `json:"client_id"`
	ClientSecret          string `json:"client_secret"`
	RedirectURI           string `json:"redirect_uri"`
	Scopes                string `json:"scopes"`
	PostLogoutRedirectURI string `json:"post_logout_redirect_uri"`
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

// SaveOIDCSettings persists the OIDC settings, minting a provider id on first
// save. Callers updating an existing configuration must carry the stored
// ProviderID forward (or deliberately clear it to start a fresh provider).
func (s *Store) SaveOIDCSettings(ctx context.Context, cfg OIDCSettings) error {
	if cfg.ProviderID == "" && strings.TrimSpace(cfg.IssuerURL) != "" {
		cfg.ProviderID = newID("idp")
	}
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

// RCSettings is the persisted, super-admin-editable state of the reverse-tunnel
// rendezvous (termulaa rc). It is stored as a JSON blob in app_settings, like
// the OIDC settings. When no value has been stored yet the feature defaults to
// enabled; the MEMD_RC=0 environment kill switch overrides either way.
type RCSettings struct {
	Enabled bool `json:"enabled"`
}

// GetRCSettings returns the stored rc settings. The bool is false when no
// value has been saved yet (callers then apply the enabled-by-default policy).
func (s *Store) GetRCSettings(ctx context.Context) (RCSettings, bool, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = ?`, settingKeyRC).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return RCSettings{}, false, nil
	}
	if err != nil {
		return RCSettings{}, false, err
	}
	var cfg RCSettings
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return RCSettings{}, false, err
	}
	return cfg, true, nil
}

// SaveRCSettings persists the rc settings.
func (s *Store) SaveRCSettings(ctx context.Context, cfg RCSettings) error {
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO app_settings(key, value, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		settingKeyRC, string(data), nowString())
	return err
}

// Backup defaults applied when a field has never been stored.
const (
	DefaultBackupBranch        = "main"
	DefaultBackupDailyAtUTC    = "03:00"
	DefaultBackupRetentionDays = 30
)

// BackupSettings is the persisted, super-admin-editable configuration of the
// encrypted daily backup: the Git repository the archives are pushed to and
// the passphrase they are sealed with. Like the OIDC client secret, the token
// and passphrase live in app_settings alongside the other control-plane
// secrets in memd.db; the admin API never returns them, only their presence.
type BackupSettings struct {
	Enabled      bool   `json:"enabled"`
	RemoteURL    string `json:"remote_url"`
	Branch       string `json:"branch"`
	AuthUsername string `json:"auth_username"`
	AuthToken    string `json:"auth_token"`
	Passphrase   string `json:"passphrase"`
	// DailyAtUTC is the scheduled run time as "HH:MM" on a 24-hour UTC clock.
	DailyAtUTC string `json:"daily_at_utc"`
	// RetentionDays prunes archives older than this many days from the
	// repository after each run; 0 keeps everything.
	RetentionDays int `json:"retention_days"`
}

// Configured reports whether the settings carry everything a run needs: a
// remote, a token to push with, and a passphrase to seal the archive.
func (s BackupSettings) Configured() bool {
	return strings.TrimSpace(s.RemoteURL) != "" && s.AuthToken != "" && s.Passphrase != ""
}

// DefaultBackupSettings is what a fresh instance starts from: disabled, with
// the schedule/branch/retention defaults filled in so the form has values.
func DefaultBackupSettings() BackupSettings {
	return BackupSettings{
		Branch:        DefaultBackupBranch,
		DailyAtUTC:    DefaultBackupDailyAtUTC,
		RetentionDays: DefaultBackupRetentionDays,
	}
}

// GetBackupSettings returns the stored backup settings with empty branch and
// schedule fields defaulted. The bool is false when nothing has been saved
// yet (the defaults are returned in that case).
func (s *Store) GetBackupSettings(ctx context.Context) (BackupSettings, bool, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = ?`, settingKeyBackup).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultBackupSettings(), false, nil
	}
	if err != nil {
		return BackupSettings{}, false, err
	}
	var cfg BackupSettings
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return BackupSettings{}, false, err
	}
	if strings.TrimSpace(cfg.Branch) == "" {
		cfg.Branch = DefaultBackupBranch
	}
	if strings.TrimSpace(cfg.DailyAtUTC) == "" {
		cfg.DailyAtUTC = DefaultBackupDailyAtUTC
	}
	return cfg, true, nil
}

// SaveBackupSettings persists the backup settings as given; callers are
// expected to have validated them and to carry stored secrets forward when the
// request omitted them.
func (s *Store) SaveBackupSettings(ctx context.Context, cfg BackupSettings) error {
	return s.saveSetting(ctx, settingKeyBackup, cfg)
}

// BackupStatus is the outcome of the most recent backup run, persisted so the
// admin console can show it across restarts.
type BackupStatus struct {
	LastRunAt   time.Time `json:"last_run_at"`
	LastOK      bool      `json:"last_ok"`
	LastError   string    `json:"last_error,omitempty"`
	LastArchive string    `json:"last_archive,omitempty"`
	LastSize    int64     `json:"last_size,omitempty"`
	LastCommit  string    `json:"last_commit,omitempty"`
	// LastTrigger is "scheduled" or "manual".
	LastTrigger string `json:"last_trigger,omitempty"`
}

// GetBackupStatus returns the last recorded run. The bool is false when no run
// has been recorded yet.
func (s *Store) GetBackupStatus(ctx context.Context) (BackupStatus, bool, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = ?`, settingKeyBackupStatus).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return BackupStatus{}, false, nil
	}
	if err != nil {
		return BackupStatus{}, false, err
	}
	var st BackupStatus
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		return BackupStatus{}, false, err
	}
	return st, true, nil
}

// SaveBackupStatus records the outcome of a run.
func (s *Store) SaveBackupStatus(ctx context.Context, st BackupStatus) error {
	return s.saveSetting(ctx, settingKeyBackupStatus, st)
}

func (s *Store) saveSetting(ctx context.Context, key string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO app_settings(key, value, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, string(data), nowString())
	return err
}
