package account

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/sudiptadeb/memd/server/internal/config"
	_ "modernc.org/sqlite"
)

const (
	EnvDatabaseURL = "MEMD_DATABASE_URL"

	RoleOwner  = "owner"
	RoleAdmin  = "admin"
	RoleMember = "member"
	RoleViewer = "viewer"
)

var (
	ErrNotInitialized     = errors.New("account database is not initialized")
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrAlreadyExists      = errors.New("already exists")
	ErrNotFound           = errors.New("not found")
	ErrForbidden          = errors.New("forbidden")
)

type DBConfig struct {
	Driver     string
	DSN        string
	Source     string
	SQLitePath string
}

type Store struct {
	db  *sql.DB
	cfg DBConfig
}

type User struct {
	ID                string
	Username          string
	DisplayName       string
	Email             string
	Issuer            string // OIDC `iss`; empty for local-only accounts
	Subject           string // OIDC `sub`; empty for local-only accounts
	Disabled          bool
	CreatedAt         time.Time
	UpdatedAt         time.Time
	PasswordChangedAt time.Time
	LastLoginAt       *time.Time
	SuperAdmin        bool
}

// userColumns is the canonical projection used to populate a User via scanUser.
// Keep the ordering in sync with scanUser.
const userColumns = `u.id, u.username, u.display_name, u.disabled, u.created_at, u.updated_at, u.password_changed_at, u.last_login_at, u.email, u.issuer, u.subject,
	       CASE WHEN sa.user_id IS NULL THEN 0 ELSE 1 END`

const userJoin = `FROM users u
	  LEFT JOIN super_admins sa ON sa.user_id = u.id`

type Team struct {
	ID              string
	Name            string
	Slug            string
	CreatedByUserID string
	Role            string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type TeamMember struct {
	TeamID      string
	UserID      string
	Username    string
	DisplayName string
	Role        string
	CreatedAt   time.Time
}

type CreateUserInput struct {
	Username    string
	Password    string
	DisplayName string
}

type CreateTeamInput struct {
	Name        string
	Slug        string
	OwnerUserID string
}

type CreateTeamInviteInput struct {
	TeamID          string
	CreatedByUserID string
	Role            string
	ExpiresAt       *time.Time
	MaxUses         *int
}

type TeamInvite struct {
	ID              string
	TeamID          string
	Role            string
	MaxUses         *int
	UseCount        int
	ExpiresAt       *time.Time
	RevokedAt       *time.Time
	CreatedByUserID string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type CreatedTeamInvite struct {
	Invite TeamInvite
	Token  string
}

func ConfigFromEnv() (DBConfig, error) {
	raw := strings.TrimSpace(os.Getenv(EnvDatabaseURL))
	if raw == "" {
		return DefaultConfig()
	}
	return ParseDatabaseURL(raw)
}

func DefaultConfig() (DBConfig, error) {
	dir, err := config.Dir()
	if err != nil {
		return DBConfig{}, err
	}
	path := filepath.Join(dir, "memd.db")
	return DBConfig{
		Driver:     "sqlite",
		DSN:        sqliteDSNForPath(path),
		Source:     "default",
		SQLitePath: path,
	}, nil
}

func ParseDatabaseURL(raw string) (DBConfig, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultConfig()
	}
	u, err := url.Parse(raw)
	if err == nil && u.Scheme != "" {
		switch u.Scheme {
		case "sqlite":
			path := u.Path
			if path == "" && u.Host != "" {
				path = u.Host
			}
			if path == "" || path == ":memory:" {
				return DBConfig{Driver: "sqlite", DSN: sqliteDefaults("file::memory:"), Source: EnvDatabaseURL}, nil
			}
			if u.Host != "" && strings.HasPrefix(u.Path, "/") {
				path = string(filepath.Separator) + filepath.Join(u.Host, u.Path)
			}
			dsn := sqliteDSNForPath(path)
			if u.RawQuery != "" {
				dsn = strings.TrimSuffix(dsn, sqliteDefaultQuery()) + "?" + u.RawQuery
				dsn = sqliteDefaults(dsn)
			}
			return DBConfig{Driver: "sqlite", DSN: dsn, Source: EnvDatabaseURL, SQLitePath: path}, nil
		case "file":
			path := u.Path
			return DBConfig{Driver: "sqlite", DSN: sqliteDefaults(raw), Source: EnvDatabaseURL, SQLitePath: path}, nil
		case "postgres", "postgresql", "mysql":
			return DBConfig{Driver: u.Scheme, DSN: raw, Source: EnvDatabaseURL}, nil
		default:
			return DBConfig{}, fmt.Errorf("unsupported database URL scheme %q", u.Scheme)
		}
	}
	return DBConfig{
		Driver:     "sqlite",
		DSN:        sqliteDSNForPath(raw),
		Source:     EnvDatabaseURL,
		SQLitePath: raw,
	}, nil
}

func Open(ctx context.Context, cfg DBConfig) (*Store, error) {
	if cfg.Driver == "" {
		cfg.Driver = "sqlite"
	}
	if cfg.Driver != "sqlite" {
		return nil, fmt.Errorf("database driver %q is not linked in this build; only sqlite is currently supported", cfg.Driver)
	}
	if cfg.SQLitePath != "" && cfg.SQLitePath != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(cfg.SQLitePath), 0o700); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", cfg.DSN)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db, cfg: cfg}, nil
}

func (s *Store) Config() DBConfig {
	return s.cfg
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) IsInitialized(ctx context.Context) (bool, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'`).Scan(&n); err != nil {
		return false, err
	}
	if n == 0 {
		return false, nil
	}
	var version int
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(max(version), 0) FROM schema_migrations`).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return version >= 1, err
}

func (s *Store) Init(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	for _, stmt := range schemaStatements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	// Bring older databases up to date: OIDC users are keyed by issuer+subject.
	if err := ensureUserColumns(ctx, tx); err != nil {
		return err
	}
	if err := backfillUserIssuerFromSettings(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DROP INDEX IF EXISTS idx_users_subject`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS idx_users_oidc_identity ON users(issuer, subject) WHERE subject IS NOT NULL AND subject != ''`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES (?, ?)`, latestSchemaVersion, nowString()); err != nil {
		return err
	}
	return tx.Commit()
}

// ensureUserColumns adds OIDC columns to the users table when upgrading an
// existing database. SQLite's ALTER TABLE ADD COLUMN errors if the column
// already exists, so we probe the current columns first.
func ensureUserColumns(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `PRAGMA table_info(users)`)
	if err != nil {
		return err
	}
	cols := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			_ = rows.Close()
			return err
		}
		cols[name] = true
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if !cols["email"] {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE users ADD COLUMN email TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	if !cols["issuer"] {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE users ADD COLUMN issuer TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	if !cols["subject"] {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE users ADD COLUMN subject TEXT`); err != nil {
			return err
		}
	}
	return nil
}

func backfillUserIssuerFromSettings(ctx context.Context, tx *sql.Tx) error {
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = ?`, settingKeyOIDC).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var settings OIDCSettings
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		return err
	}
	issuer := strings.TrimRight(strings.TrimSpace(settings.IssuerURL), "/")
	if issuer == "" {
		return nil
	}
	_, err = tx.ExecContext(ctx, `UPDATE users SET issuer = ? WHERE issuer = '' AND subject IS NOT NULL AND subject != ''`, issuer)
	return err
}

func (s *Store) HasSuperAdmin(ctx context.Context) (bool, error) {
	if ok, err := s.IsInitialized(ctx); err != nil || !ok {
		if err != nil {
			return false, err
		}
		return false, ErrNotInitialized
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM super_admins`).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *Store) CreateSuperAdmin(ctx context.Context, username, password string) (User, error) {
	if ok, err := s.IsInitialized(ctx); err != nil || !ok {
		if err != nil {
			return User{}, err
		}
		return User{}, ErrNotInitialized
	}
	username = strings.TrimSpace(username)
	if username == "" {
		return User{}, errors.New("username is required")
	}
	passwordHash, err := hashPassword(password)
	if err != nil {
		return User{}, err
	}
	now := nowString()
	user := User{
		ID:                newID("usr"),
		Username:          username,
		CreatedAt:         mustParseTime(now),
		UpdatedAt:         mustParseTime(now),
		PasswordChangedAt: mustParseTime(now),
		SuperAdmin:        true,
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer rollback(tx)
	_, err = tx.ExecContext(ctx, `
		INSERT INTO users(id, username, username_norm, password_hash, display_name, disabled, created_at, updated_at, password_changed_at)
		VALUES (?, ?, ?, ?, '', 0, ?, ?, ?)`,
		user.ID, user.Username, normalizeUsername(user.Username), passwordHash, now, now, now)
	if err != nil {
		if isUniqueErr(err) {
			return User{}, ErrAlreadyExists
		}
		return User{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO super_admins(user_id, created_at, created_by_user_id) VALUES (?, ?, NULL)`, user.ID, now); err != nil {
		return User{}, err
	}
	return user, tx.Commit()
}

func (s *Store) CreateLocalUser(ctx context.Context, in CreateUserInput) (User, error) {
	if ok, err := s.IsInitialized(ctx); err != nil || !ok {
		if err != nil {
			return User{}, err
		}
		return User{}, ErrNotInitialized
	}
	username := strings.TrimSpace(in.Username)
	if username == "" {
		return User{}, errors.New("username is required")
	}
	passwordHash, err := hashPassword(in.Password)
	if err != nil {
		return User{}, err
	}
	now := nowString()
	user := User{
		ID:                newID("usr"),
		Username:          username,
		DisplayName:       strings.TrimSpace(in.DisplayName),
		CreatedAt:         mustParseTime(now),
		UpdatedAt:         mustParseTime(now),
		PasswordChangedAt: mustParseTime(now),
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO users(id, username, username_norm, password_hash, display_name, disabled, created_at, updated_at, password_changed_at)
		VALUES (?, ?, ?, ?, ?, 0, ?, ?, ?)`,
		user.ID, user.Username, normalizeUsername(user.Username), passwordHash, user.DisplayName, now, now, now)
	if err != nil {
		if isUniqueErr(err) {
			return User{}, ErrAlreadyExists
		}
		return User{}, err
	}
	return user, nil
}

func (s *Store) SetUserDisabled(ctx context.Context, id string, disabled bool) error {
	if ok, err := s.IsInitialized(ctx); err != nil || !ok {
		if err != nil {
			return err
		}
		return ErrNotInitialized
	}
	value := 0
	if disabled {
		value = 1
	}
	res, err := s.db.ExecContext(ctx, `UPDATE users SET disabled = ?, updated_at = ? WHERE id = ?`, value, nowString(), id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetUserPassword(ctx context.Context, id, password string) error {
	if ok, err := s.IsInitialized(ctx); err != nil || !ok {
		if err != nil {
			return err
		}
		return ErrNotInitialized
	}
	passwordHash, err := hashPassword(password)
	if err != nil {
		return err
	}
	now := nowString()
	res, err := s.db.ExecContext(ctx, `UPDATE users SET password_hash = ?, password_changed_at = ?, updated_at = ? WHERE id = ?`, passwordHash, now, now, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) AuthenticateLocal(ctx context.Context, username, password string) (User, error) {
	if ok, err := s.IsInitialized(ctx); err != nil || !ok {
		if err != nil {
			return User{}, err
		}
		return User{}, ErrNotInitialized
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT u.id, u.username, u.display_name, u.disabled, u.created_at, u.updated_at, u.password_changed_at, u.last_login_at, u.email, u.issuer, u.subject, u.password_hash,
		       CASE WHEN sa.user_id IS NULL THEN 0 ELSE 1 END
		  FROM users u
		  LEFT JOIN super_admins sa ON sa.user_id = u.id
		 WHERE u.username_norm = ?`, normalizeUsername(username))
	user, hash, err := scanUserWithHash(row)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrInvalidCredentials
	}
	if err != nil {
		return User{}, err
	}
	if user.Disabled {
		return User{}, ErrInvalidCredentials
	}
	ok, err := verifyPassword(password, hash)
	if err != nil || !ok {
		if err != nil {
			return User{}, err
		}
		return User{}, ErrInvalidCredentials
	}
	now := nowString()
	if _, err := s.db.ExecContext(ctx, `UPDATE users SET last_login_at = ?, updated_at = ? WHERE id = ?`, now, now, user.ID); err != nil {
		return User{}, err
	}
	t := mustParseTime(now)
	user.LastLoginAt = &t
	user.UpdatedAt = t
	return user, nil
}

func (s *Store) CreateTeam(ctx context.Context, in CreateTeamInput) (Team, error) {
	if ok, err := s.IsInitialized(ctx); err != nil || !ok {
		if err != nil {
			return Team{}, err
		}
		return Team{}, ErrNotInitialized
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return Team{}, errors.New("team name is required")
	}
	if err := s.EnsureRegularActiveUser(ctx, in.OwnerUserID); err != nil {
		return Team{}, err
	}
	slug := slugify(in.Slug)
	if slug == "" {
		slug = slugify(name)
	}
	if slug == "" {
		return Team{}, errors.New("team slug is required")
	}
	now := nowString()
	team := Team{ID: newID("team"), Name: name, Slug: slug, CreatedByUserID: in.OwnerUserID, CreatedAt: mustParseTime(now), UpdatedAt: mustParseTime(now)}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Team{}, err
	}
	defer rollback(tx)
	_, err = tx.ExecContext(ctx, `
		INSERT INTO teams(id, name, slug, created_by_user_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`, team.ID, team.Name, team.Slug, in.OwnerUserID, now, now)
	if err != nil {
		if isUniqueErr(err) {
			return Team{}, ErrAlreadyExists
		}
		return Team{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO team_members(team_id, user_id, role, created_at, created_by_user_id)
		VALUES (?, ?, ?, ?, ?)`, team.ID, in.OwnerUserID, RoleOwner, now, in.OwnerUserID); err != nil {
		return Team{}, err
	}
	return team, tx.Commit()
}

func (s *Store) AddTeamMember(ctx context.Context, teamID, userID, role, actorUserID string) error {
	if !validRole(role) {
		return fmt.Errorf("invalid team role %q", role)
	}
	if err := s.EnsureRegularActiveUser(ctx, userID); err != nil {
		return err
	}
	if err := s.canAddTeamMember(ctx, teamID, actorUserID, role); err != nil {
		return err
	}
	now := nowString()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO team_members(team_id, user_id, role, created_at, created_by_user_id)
		VALUES (?, ?, ?, ?, ?)`, teamID, userID, role, now, actorUserID)
	if err != nil && isUniqueErr(err) {
		return ErrAlreadyExists
	}
	return err
}

func (s *Store) UserByID(ctx context.Context, id string) (User, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+userColumns+`
		  `+userJoin+`
		 WHERE u.id = ?`, id)
	user, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return user, err
}

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+userColumns+`
		  `+userJoin+`
		 ORDER BY lower(u.username)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, user)
	}
	return out, rows.Err()
}

func (s *Store) ListTeams(ctx context.Context) ([]Team, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, slug, created_by_user_id, created_at, updated_at FROM teams ORDER BY lower(name)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Team
	for rows.Next() {
		var t Team
		var created, updated string
		if err := rows.Scan(&t.ID, &t.Name, &t.Slug, &t.CreatedByUserID, &created, &updated); err != nil {
			return nil, err
		}
		t.CreatedAt = mustParseTime(created)
		t.UpdatedAt = mustParseTime(updated)
		out = append(out, t)
	}
	return out, rows.Err()
}

func sqliteDSNForPath(path string) string {
	if path == ":memory:" {
		return sqliteDefaults("file::memory:")
	}
	u := url.URL{Scheme: "file", Path: path}
	return sqliteDefaults(u.String())
}

func sqliteDefaultQuery() string {
	return "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_txlock=immediate"
}

func sqliteDefaults(dsn string) string {
	defaults := []struct {
		present string
		value   string
	}{
		{"busy_timeout(", "_pragma=busy_timeout(5000)"},
		{"foreign_keys(", "_pragma=foreign_keys(1)"},
		{"journal_mode(", "_pragma=journal_mode(WAL)"},
		{"synchronous(", "_pragma=synchronous(NORMAL)"},
		{"_txlock=", "_txlock=immediate"},
	}
	add := make([]string, 0, len(defaults))
	lowerDSN := strings.ToLower(dsn)
	for _, item := range defaults {
		if strings.Contains(lowerDSN, item.present) {
			continue
		}
		add = append(add, item.value)
	}
	if len(add) == 0 {
		return dsn
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	return dsn + sep + strings.Join(add, "&")
}

func normalizeUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

var slugCleanup = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugCleanup.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

func validRole(role string) bool {
	switch role {
	case RoleOwner, RoleAdmin, RoleMember, RoleViewer:
		return true
	default:
		return false
	}
}

func newID(prefix string) string {
	raw := make([]byte, 10)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	enc := base32.StdEncoding.WithPadding(base32.NoPadding)
	return prefix + "_" + strings.ToLower(enc.EncodeToString(raw))
}

func nowString() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func mustParseTime(raw string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		panic(err)
	}
	return t
}

func parseOptionalTime(raw sql.NullString) *time.Time {
	if !raw.Valid || raw.String == "" {
		return nil
	}
	t := mustParseTime(raw.String)
	return &t
}

type userScanner interface {
	Scan(dest ...any) error
}

func scanUser(row userScanner) (User, error) {
	var u User
	var disabled, superAdmin int
	var created, updated, changed string
	var last, subject sql.NullString
	if err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &disabled, &created, &updated, &changed, &last, &u.Email, &u.Issuer, &subject, &superAdmin); err != nil {
		return User{}, err
	}
	u.Disabled = disabled != 0
	u.CreatedAt = mustParseTime(created)
	u.UpdatedAt = mustParseTime(updated)
	u.PasswordChangedAt = mustParseTime(changed)
	u.LastLoginAt = parseOptionalTime(last)
	u.Subject = subject.String
	u.SuperAdmin = superAdmin != 0
	return u, nil
}

func scanUserWithHash(row userScanner) (User, string, error) {
	var u User
	var disabled, superAdmin int
	var created, updated, changed, hash string
	var last, subject sql.NullString
	if err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &disabled, &created, &updated, &changed, &last, &u.Email, &u.Issuer, &subject, &hash, &superAdmin); err != nil {
		return User{}, "", err
	}
	u.Disabled = disabled != 0
	u.CreatedAt = mustParseTime(created)
	u.UpdatedAt = mustParseTime(updated)
	u.PasswordChangedAt = mustParseTime(changed)
	u.LastLoginAt = parseOptionalTime(last)
	u.Subject = subject.String
	u.SuperAdmin = superAdmin != 0
	return u, hash, nil
}

func isUniqueErr(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "constraint") && strings.Contains(msg, "unique")
}

func rollback(tx *sql.Tx) {
	_ = tx.Rollback()
}
