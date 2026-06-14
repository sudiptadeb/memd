package account

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/sudiptadeb/memd/server/internal/config"
)

// TestInitAddsFeaturesColumn simulates a pre-feature (schema v7) user_directories
// table — one without the `features` column — and verifies Init adds it and that
// a directory's enabled features then round-trip.
func TestInitAddsFeaturesColumn(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "memd.db")
	dsn := sqliteDSNForPath(path)

	raw, err := sql.Open("sqlite", "file:"+path)
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
		// v7-era user_directories: every column up to git_save_every, but NO features.
		`CREATE TABLE user_directories (
			owner_user_id TEXT NOT NULL,
			id TEXT NOT NULL,
			team_id TEXT,
			owner_connector_id TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			backend TEXT NOT NULL CHECK (backend IN ('local', 'git')),
			local_path TEXT NOT NULL DEFAULT '',
			git_remote_url TEXT NOT NULL DEFAULT '',
			git_branch TEXT NOT NULL DEFAULT '',
			git_base_path TEXT NOT NULL DEFAULT '',
			git_author_name TEXT NOT NULL DEFAULT '',
			git_author_email TEXT NOT NULL DEFAULT '',
			git_auth_username TEXT NOT NULL DEFAULT '',
			git_auth_token TEXT NOT NULL DEFAULT '',
			git_ssh_key_path TEXT NOT NULL DEFAULT '',
			git_wait_for_writes TEXT NOT NULL DEFAULT '',
			git_save_every TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (owner_user_id, id)
		)`,
		`INSERT INTO schema_migrations(version, applied_at) VALUES (7, '2026-01-01T00:00:00Z')`,
		`INSERT INTO users(id, username, username_norm, created_at, updated_at, password_changed_at)
		   VALUES ('usr_owner', 'Owner', 'owner', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		`INSERT INTO user_directories(owner_user_id, id, name, backend, local_path, created_at, updated_at)
		   VALUES ('usr_owner', 'dir1', 'Notes', 'local', '/tmp/notes', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
	}
	for _, s := range stmts {
		if _, err := raw.ExecContext(ctx, s); err != nil {
			t.Fatalf("seed v7 schema: %v", err)
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

	// The pre-existing directory survives with no features.
	dirs, err := store.ListUserDirectories(ctx, "usr_owner")
	if err != nil {
		t.Fatalf("ListUserDirectories: %v", err)
	}
	if len(dirs) != 1 || len(dirs[0].Features) != 0 {
		t.Fatalf("unexpected directories after upgrade: %+v", dirs)
	}

	// Enabling a feature now round-trips through the new column.
	dirs[0].Features = []config.DirectoryFeature{{Key: "tasks", Enabled: true}}
	if err := store.UpsertUserDirectory(ctx, "usr_owner", dirs[0]); err != nil {
		t.Fatalf("UpsertUserDirectory: %v", err)
	}
	reloaded, err := store.ListUserDirectories(ctx, "usr_owner")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(reloaded) != 1 || len(reloaded[0].Features) != 1 ||
		reloaded[0].Features[0].Key != "tasks" || !reloaded[0].Features[0].Enabled {
		t.Fatalf("features did not round-trip: %+v", reloaded)
	}
}
