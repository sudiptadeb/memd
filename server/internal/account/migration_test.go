package account

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/sudiptadeb/memd/server/internal/config"
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
	cloud, err := store.UpsertOIDCUser(ctx, OIDCIdentity{ProviderID: "idp_test", Issuer: "https://idp.example.com", Subject: "idp|legacy", Email: "legacy@example.com", PreferredUsername: "legacy"})
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

	// The upgrade mints a provider id for the configured IdP and adopts the
	// existing OIDC user into it.
	upgraded, ok, err := store.GetOIDCSettings(ctx)
	if err != nil || !ok || upgraded.ProviderID == "" {
		t.Fatalf("settings after upgrade: %+v ok=%v err=%v", upgraded, ok, err)
	}
	user, err := store.UserByOIDCIdentity(ctx, upgraded.ProviderID, "idp|ada")
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

// TestInitUpgradesV6ConnectorDirectories simulates the schema where connector
// directory mappings could only reference directories owned by the connector's
// owner, and verifies the rebuild keeps existing rows and accepts cross-owner
// (team-shared) attachments afterwards.
func TestInitUpgradesV6ConnectorDirectories(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "memd.db")
	dsn := sqliteDSNForPath(path)

	// Seed over a pragma-less connection, as builds from the FK-off era did —
	// that is how orphaned mappings got into real databases.
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
		`CREATE TABLE user_connectors (
			owner_user_id TEXT NOT NULL,
			id TEXT NOT NULL,
			team_id TEXT,
			name TEXT NOT NULL,
			kind TEXT NOT NULL CHECK (kind IN ('mcp', 'http')),
			token TEXT NOT NULL,
			write INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (owner_user_id, id)
		)`,
		`CREATE TABLE user_connector_directories (
			owner_user_id TEXT NOT NULL,
			connector_id TEXT NOT NULL,
			directory_id TEXT NOT NULL,
			PRIMARY KEY (owner_user_id, connector_id, directory_id),
			FOREIGN KEY (owner_user_id, connector_id) REFERENCES user_connectors(owner_user_id, id) ON DELETE CASCADE,
			FOREIGN KEY (owner_user_id, directory_id) REFERENCES user_directories(owner_user_id, id) ON DELETE CASCADE
		)`,
		`INSERT INTO schema_migrations(version, applied_at) VALUES (6, '2026-01-01T00:00:00Z')`,
		`INSERT INTO users(id, username, username_norm, created_at, updated_at, password_changed_at)
		   VALUES ('usr_owner', 'Owner', 'owner', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		`INSERT INTO user_directories(owner_user_id, id, name, backend, local_path, created_at, updated_at)
		   VALUES ('usr_owner', 'dir1', 'Own', 'local', '/tmp/own', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		`INSERT INTO user_connectors(owner_user_id, id, name, kind, token, created_at, updated_at)
		   VALUES ('usr_owner', 'conn1', 'Agent', 'mcp', 'tok_1', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		`INSERT INTO user_connector_directories(owner_user_id, connector_id, directory_id)
		   VALUES ('usr_owner', 'conn1', 'dir1')`,
		// Orphaned mappings from an era without enforced foreign keys: the
		// rebuild must drop these instead of aborting startup. The old table
		// enforces FKs only on connections with the pragma set, so plain
		// inserts here go through.
		`INSERT INTO user_connector_directories(owner_user_id, connector_id, directory_id)
		   VALUES ('usr_owner', 'conn_gone', 'dir1')`,
		`INSERT INTO user_connector_directories(owner_user_id, connector_id, directory_id)
		   VALUES ('usr_owner', 'conn1', 'dir_gone')`,
	}
	for _, s := range stmts {
		if _, err := raw.ExecContext(ctx, s); err != nil {
			t.Fatalf("seed v6 schema: %v", err)
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

	// The pre-existing self-owned mapping survives the rebuild.
	conns, err := store.ListUserConnectors(ctx, "usr_owner")
	if err != nil {
		t.Fatalf("ListUserConnectors: %v", err)
	}
	if len(conns) != 1 || len(conns[0].DirectoryIDs) != 1 || conns[0].DirectoryIDs[0] != "dir1" {
		t.Fatalf("unexpected connectors after upgrade: %+v", conns)
	}

	// A team member can now attach the owner's shared directory: this insert
	// crossed the old foreign key and failed with a raw constraint error.
	member, err := store.CreateLocalUser(ctx, CreateUserInput{Username: "member", Password: "member-pass"})
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	team, err := store.CreateTeam(ctx, CreateTeamInput{Name: "Family", OwnerUserID: "usr_owner"})
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if err := store.AddTeamMember(ctx, team.ID, member.ID, RoleMember, "usr_owner"); err != nil {
		t.Fatalf("AddTeamMember: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE user_directories SET team_id = ? WHERE id = 'dir1'`, team.ID); err != nil {
		t.Fatalf("share dir1: %v", err)
	}
	if err := store.UpsertUserConnector(ctx, member.ID, config.Connector{
		ID: "conn_member", Name: "member-agent", Kind: config.ConnectorKindMCP, Token: "tok_2",
		DirectoryIDs: []string{"dir1"},
	}); err != nil {
		t.Fatalf("member connector on shared directory: %v", err)
	}
	memberConns, err := store.ListUserConnectors(ctx, member.ID)
	if err != nil {
		t.Fatalf("ListUserConnectors(member): %v", err)
	}
	if len(memberConns) != 1 || len(memberConns[0].DirectoryIDs) != 1 || memberConns[0].DirectoryIDs[0] != "dir1" {
		t.Fatalf("unexpected member connectors: %+v", memberConns)
	}
}

// TestInitBackfillsProviderIDPreferringOldestAccount reproduces the
// issuer-rename incident: the original account (old issuer) and an accidental
// duplicate (new issuer, same subject) both exist. The upgrade must hand the
// provider identity to the original account, leaving the empty duplicate
// unlinked for cleanup.
func TestInitBackfillsProviderIDPreferringOldestAccount(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "memd.db")
	dsn := sqliteDSNForPath(path)

	settings, err := json.Marshal(OIDCSettings{
		Enabled:      true,
		IssuerURL:    "https://auth.custom-domain.com",
		ClientID:     "client",
		ClientSecret: "secret",
		RedirectURI:  "https://app.example.com/auth/callback",
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
			issuer TEXT NOT NULL DEFAULT '',
			subject TEXT
		)`,
		`CREATE TABLE app_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`INSERT INTO schema_migrations(version, applied_at) VALUES (6, '2026-01-01T00:00:00Z')`,
		`INSERT INTO users(id, username, username_norm, email, issuer, subject, created_at, updated_at, password_changed_at)
		   VALUES ('usr_original', 'sudipta', 'sudipta', 's@example.com', 'https://accounts.example.com', 'sub|sd', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		`INSERT INTO users(id, username, username_norm, email, issuer, subject, created_at, updated_at, password_changed_at)
		   VALUES ('usr_dup', 'sudipta-2', 'sudipta-2', 's@example.com', 'https://auth.custom-domain.com', 'sub|sd', '2026-06-01T00:00:00Z', '2026-06-01T00:00:00Z', '2026-06-01T00:00:00Z')`,
	}
	for _, s := range stmts {
		if _, err := raw.ExecContext(ctx, s); err != nil {
			t.Fatalf("seed schema: %v", err)
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

	upgraded, ok, err := store.GetOIDCSettings(ctx)
	if err != nil || !ok || upgraded.ProviderID == "" {
		t.Fatalf("settings after upgrade: %+v ok=%v err=%v", upgraded, ok, err)
	}
	// The next login through the (renamed) provider resolves to the original
	// account with all its data, not the empty duplicate.
	user, err := store.UserByOIDCIdentity(ctx, upgraded.ProviderID, "sub|sd")
	if err != nil {
		t.Fatalf("UserByOIDCIdentity: %v", err)
	}
	if user.ID != "usr_original" {
		t.Fatalf("provider identity went to %s, want usr_original", user.ID)
	}
	dup, err := store.UserByID(ctx, "usr_dup")
	if err != nil {
		t.Fatalf("UserByID(usr_dup): %v", err)
	}
	if dup.ProviderID != "" {
		t.Fatalf("duplicate should stay unlinked, got provider %q", dup.ProviderID)
	}
}
