package account

const latestSchemaVersion = 8

var schemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS users (
		id TEXT PRIMARY KEY,
		username TEXT NOT NULL,
		username_norm TEXT NOT NULL UNIQUE,
		password_hash TEXT NOT NULL DEFAULT '',
		display_name TEXT NOT NULL DEFAULT '',
		disabled INTEGER NOT NULL DEFAULT 0 CHECK (disabled IN (0, 1)),
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		password_changed_at TEXT NOT NULL,
		last_login_at TEXT,
		email TEXT NOT NULL DEFAULT '',
		issuer TEXT NOT NULL DEFAULT '',
		subject TEXT
	)`,
	`CREATE TABLE IF NOT EXISTS app_settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS super_admins (
		user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
		created_at TEXT NOT NULL,
		created_by_user_id TEXT REFERENCES users(id) ON DELETE SET NULL
	)`,
	`CREATE TABLE IF NOT EXISTS teams (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		slug TEXT NOT NULL UNIQUE,
		created_by_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS team_members (
		team_id TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
		user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		role TEXT NOT NULL CHECK (role IN ('owner', 'admin', 'member', 'viewer')),
		created_at TEXT NOT NULL,
		created_by_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
		PRIMARY KEY (team_id, user_id)
	)`,
	`CREATE INDEX IF NOT EXISTS idx_team_members_user_id ON team_members(user_id)`,
	`CREATE TABLE IF NOT EXISTS team_invites (
		id TEXT PRIMARY KEY,
		team_id TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
		token_hash TEXT NOT NULL UNIQUE,
		role TEXT NOT NULL CHECK (role IN ('admin', 'member', 'viewer')),
		max_uses INTEGER CHECK (max_uses IS NULL OR max_uses > 0),
		use_count INTEGER NOT NULL DEFAULT 0 CHECK (use_count >= 0),
		expires_at TEXT,
		revoked_at TEXT,
		created_by_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_team_invites_team_id ON team_invites(team_id)`,
	`CREATE TABLE IF NOT EXISTS team_invite_uses (
		invite_id TEXT NOT NULL REFERENCES team_invites(id) ON DELETE CASCADE,
		user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		used_at TEXT NOT NULL,
		PRIMARY KEY (invite_id, user_id)
	)`,
	`CREATE TABLE IF NOT EXISTS user_directories (
		owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		id TEXT NOT NULL,
		team_id TEXT REFERENCES teams(id) ON DELETE SET NULL,
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
		features TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		PRIMARY KEY (owner_user_id, id)
	)`,
	`CREATE TABLE IF NOT EXISTS user_connectors (
		owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		id TEXT NOT NULL,
		team_id TEXT REFERENCES teams(id) ON DELETE SET NULL,
		name TEXT NOT NULL,
		kind TEXT NOT NULL CHECK (kind IN ('mcp', 'http')),
		token TEXT NOT NULL,
		write INTEGER NOT NULL DEFAULT 0 CHECK (write IN (0, 1)),
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		PRIMARY KEY (owner_user_id, id)
	)`,
	// directory_owner_user_id is the directory's owner, which differs from the
	// connector's owner when a team member attaches a teammate's shared
	// directory.
	`CREATE TABLE IF NOT EXISTS user_connector_directories (
		owner_user_id TEXT NOT NULL,
		connector_id TEXT NOT NULL,
		directory_id TEXT NOT NULL,
		directory_owner_user_id TEXT NOT NULL,
		PRIMARY KEY (owner_user_id, connector_id, directory_id),
		FOREIGN KEY (owner_user_id, connector_id) REFERENCES user_connectors(owner_user_id, id) ON DELETE CASCADE,
		FOREIGN KEY (directory_owner_user_id, directory_id) REFERENCES user_directories(owner_user_id, id) ON DELETE CASCADE
	)`,
}
