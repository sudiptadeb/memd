package account

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// TestInitUpgradesV2Database simulates a pre-OIDC (schema v2) database — a users
// table without the email/subject columns — and verifies Init adds them, builds
// the subject index, and that an existing account is then linkable by username.
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
	if user.Username != "Legacy" || user.Subject != "" {
		t.Fatalf("unexpected legacy user after upgrade: %+v", user)
	}

	// And it must be linkable by username on first OIDC login.
	linked, err := store.UpsertOIDCUser(ctx, OIDCIdentity{Subject: "idp|legacy", Email: "legacy@example.com", PreferredUsername: "legacy"})
	if err != nil {
		t.Fatalf("UpsertOIDCUser: %v", err)
	}
	if linked.ID != "usr_legacy" || linked.Subject != "idp|legacy" {
		t.Fatalf("legacy account not linked: %+v", linked)
	}
}
