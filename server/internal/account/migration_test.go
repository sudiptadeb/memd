package account

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
)

// TestInitUpgradesV2Database simulates a pre-OIDC (schema v2) database — a users
// table without the email/issuer/subject columns — and verifies Init adds them
// while preserving local accounts as local-only users.
func TestInitUpgradesV2Database(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "memd.db")
	dsn := "file:" + path

	// Hand-build a minimal v2 schema with a legacy user.
	raw, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	stmts := []string{
		`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`,
		`CREATE TABLE users (
			id TEXT PRIMARY KEY,
			username TEXT NOT NULL,
			username_norm TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			display_name TEXT NOT NULL DEFAULT '',
			disabled INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			password_changed_at TEXT NOT NULL,
			last_login_at TEXT
		)`,
		`CREATE TABLE super_admins (user_id TEXT PRIMARY KEY, created_at TEXT NOT NULL, created_by_user_id TEXT)`,
		`INSERT INTO schema_migrations(version, applied_at) VALUES (2, '2026-01-01T00:00:00Z')`,
		`INSERT INTO users(id, username, username_norm, password_hash, display_name, created_at, updated_at, password_changed_at)
		   VALUES ('usr_legacy', 'Legacy', 'legacy', 'x', 'Legacy User', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
	}
	for _, s := range stmts {
		if _, err := raw.ExecContext(ctx, s); err != nil {
			t.Fatalf("seed v2 schema: %v", err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close seed db: %v", err)
	}

	// Open via the store and run Init: this should upgrade the schema in place.
	store, err := Open(ctx, DBConfig{Driver: "sqlite", DSN: dsn, Source: "test", SQLitePath: path})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Init (upgrade): %v", err)
	}

	// The legacy user must still be readable through the new projection.
	user, err := store.UserByID(ctx, "usr_legacy")
	if err != nil {
		t.Fatalf("UserByID after upgrade: %v", err)
	}
	if user.Username != "Legacy" || user.Issuer != "" || user.Subject != "" {
		t.Fatalf("unexpected legacy user after upgrade: %+v", user)
	}

	// A matching OIDC login must create a separate cloud account, not link the
	// legacy local account by username/email.
	cloud, err := store.UpsertOIDCUser(ctx, OIDCIdentity{Issuer: "https://idp.example.com", Subject: "idp|legacy", Email: "legacy@example.com", PreferredUsername: "legacy"})
	if err != nil {
		t.Fatalf("UpsertOIDCUser: %v", err)
	}
	if cloud.ID == "usr_legacy" || cloud.Issuer != "https://idp.example.com" || cloud.Subject != "idp|legacy" {
		t.Fatalf("cloud account not provisioned separately: %+v", cloud)
	}
}

func TestInitUpgradesV3OIDCUsersToIssuerSubject(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "memd.db")
	dsn := "file:" + path

	settings, err := json.Marshal(OIDCSettings{
		Enabled:      true,
		IssuerURL:    "https://idp.example.com/",
		ClientID:     "client",
		ClientSecret: "secret",
		RedirectURI:  "https://app.example.com/auth/callback",
		Scopes:       "openid profile email",
	})
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}

	raw, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	stmts := []string{
		`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`,
		`CREATE TABLE users (
			id TEXT PRIMARY KEY,
			username TEXT NOT NULL,
			username_norm TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL DEFAULT '',
			display_name TEXT NOT NULL DEFAULT '',
			disabled INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			password_changed_at TEXT NOT NULL,
			last_login_at TEXT,
			email TEXT NOT NULL DEFAULT '',
			subject TEXT
		)`,
		`CREATE TABLE app_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE super_admins (user_id TEXT PRIMARY KEY, created_at TEXT NOT NULL, created_by_user_id TEXT)`,
		`CREATE UNIQUE INDEX idx_users_subject ON users(subject) WHERE subject IS NOT NULL`,
		`INSERT INTO schema_migrations(version, applied_at) VALUES (3, '2026-01-01T00:00:00Z')`,
		`INSERT INTO users(id, username, username_norm, password_hash, display_name, email, subject, created_at, updated_at, password_changed_at)
		   VALUES ('usr_cloud', 'Ada', 'ada', '', 'Ada Lovelace', 'ada@example.com', 'idp|ada', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
	}
	for _, s := range stmts {
		if _, err := raw.ExecContext(ctx, s); err != nil {
			t.Fatalf("seed v3 schema: %v", err)
		}
	}
	if _, err := raw.ExecContext(ctx, `INSERT INTO app_settings(key, value, updated_at) VALUES (?, ?, ?)`, settingKeyOIDC, string(settings), "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("seed oidc settings: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close seed db: %v", err)
	}

	store, err := Open(ctx, DBConfig{Driver: "sqlite", DSN: dsn, Source: "test", SQLitePath: path})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Init (upgrade): %v", err)
	}

	user, err := store.UserByOIDCIdentity(ctx, "https://idp.example.com", "idp|ada")
	if err != nil {
		t.Fatalf("UserByOIDCIdentity after upgrade: %v", err)
	}
	if user.ID != "usr_cloud" || user.Issuer != "https://idp.example.com" {
		t.Fatalf("unexpected upgraded OIDC user: %+v", user)
	}
}

func TestInitUpgradesV4GitDirectoriesToPATFields(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "memd.db")
	dsn := "file:" + path

	raw, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	stmts := []string{
		`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`,
		`CREATE TABLE users (
			id TEXT PRIMARY KEY,
			username TEXT NOT NULL,
			username_norm TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL DEFAULT '',
			display_name TEXT NOT NULL DEFAULT '',
			disabled INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			password_changed_at TEXT NOT NULL,
			last_login_at TEXT,
			email TEXT NOT NULL DEFAULT '',
			issuer TEXT NOT NULL DEFAULT '',
			subject TEXT
		)`,
		`CREATE TABLE user_directories (
			owner_user_id TEXT NOT NULL,
			id TEXT NOT NULL,
			team_id TEXT,
			name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			backend TEXT NOT NULL CHECK (backend IN ('local', 'git')),
			local_path TEXT NOT NULL DEFAULT '',
			git_remote_url TEXT NOT NULL DEFAULT '',
			git_branch TEXT NOT NULL DEFAULT '',
			git_base_path TEXT NOT NULL DEFAULT '',
			git_author_name TEXT NOT NULL DEFAULT '',
			git_author_email TEXT NOT NULL DEFAULT '',
			git_ssh_key_path TEXT NOT NULL DEFAULT '',
			git_wait_for_writes TEXT NOT NULL DEFAULT '',
			git_save_every TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (owner_user_id, id)
		)`,
		`INSERT INTO schema_migrations(version, applied_at) VALUES (4, '2026-01-01T00:00:00Z')`,
		`INSERT INTO users(id, username, username_norm, created_at, updated_at, password_changed_at)
		   VALUES ('usr_owner', 'Owner', 'owner', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		`INSERT INTO user_directories(owner_user_id, id, name, backend, git_remote_url, git_branch, created_at, updated_at)
		   VALUES ('usr_owner', 'dir_git', 'Git Memory', 'git', 'https://github.com/acme/memory.git', 'main', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
	}
	for _, s := range stmts {
		if _, err := raw.ExecContext(ctx, s); err != nil {
			t.Fatalf("seed v4 schema: %v", err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close seed db: %v", err)
	}

	store, err := Open(ctx, DBConfig{Driver: "sqlite", DSN: dsn, Source: "test", SQLitePath: path})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Init (upgrade): %v", err)
	}

	dirs, err := store.ListUserDirectories(ctx, "usr_owner")
	if err != nil {
		t.Fatalf("ListUserDirectories: %v", err)
	}
	if len(dirs) != 1 || dirs[0].Git == nil {
		t.Fatalf("unexpected directories after upgrade: %+v", dirs)
	}
	if dirs[0].Git.AuthUsername != "" || dirs[0].Git.AuthToken != "" {
		t.Fatalf("new git auth fields should default empty: %+v", dirs[0].Git)
	}
}
