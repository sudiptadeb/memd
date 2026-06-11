package account

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/sudiptadeb/memd/server/internal/config"
)

const userDataFormat = "memd-user-data"

type UserDataBundle struct {
	Format      string             `json:"format"`
	Version     int                `json:"version"`
	ExportedAt  time.Time          `json:"exported_at"`
	Directories []config.Directory `json:"directories"`
	Connectors  []config.Connector `json:"connectors"`
}

func NewUserDataBundle(dirs []config.Directory, connectors []config.Connector) UserDataBundle {
	cleanDirs := make([]config.Directory, len(dirs))
	for i, d := range dirs {
		d.OwnerUserID = ""
		d.TeamID = ""
		cleanDirs[i] = d
	}
	cleanConnectors := make([]config.Connector, len(connectors))
	for i, c := range connectors {
		c.OwnerUserID = ""
		c.TeamID = ""
		cleanConnectors[i] = c
	}
	return UserDataBundle{
		Format:      userDataFormat,
		Version:     1,
		ExportedAt:  time.Now().UTC(),
		Directories: cleanDirs,
		Connectors:  cleanConnectors,
	}
}

func (b UserDataBundle) validate() error {
	if b.Format != "" && b.Format != userDataFormat {
		return fmt.Errorf("unsupported user data format %q", b.Format)
	}
	if b.Version != 0 && b.Version != 1 {
		return fmt.Errorf("unsupported user data version %d", b.Version)
	}
	dirIDs := map[string]bool{}
	for _, d := range b.Directories {
		if d.ID == "" {
			return errors.New("directory id is required")
		}
		if d.Name == "" {
			return fmt.Errorf("directory %q name is required", d.ID)
		}
		if d.Backend != "local" && d.Backend != "git" {
			return fmt.Errorf("directory %q has unsupported backend %q", d.ID, d.Backend)
		}
		dirIDs[d.ID] = true
	}
	connIDs := map[string]bool{}
	for _, c := range b.Connectors {
		if c.ID == "" {
			return errors.New("connector id is required")
		}
		if connIDs[c.ID] {
			return fmt.Errorf("duplicate connector id %q", c.ID)
		}
		connIDs[c.ID] = true
		if c.Name == "" {
			return fmt.Errorf("connector %q name is required", c.ID)
		}
		kind := c.EffectiveKind()
		if kind != config.ConnectorKindMCP && kind != config.ConnectorKindHTTP {
			return fmt.Errorf("connector %q has unsupported kind %q", c.ID, kind)
		}
		if c.Token == "" {
			return fmt.Errorf("connector %q token is required", c.ID)
		}
		for _, id := range c.DirectoryIDs {
			if !dirIDs[id] {
				return fmt.Errorf("connector %q references unknown directory %q", c.ID, id)
			}
		}
	}
	return nil
}

func (s *Store) UserByUsername(ctx context.Context, username string) (User, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+userColumns+`
		  `+userJoin+`
		 WHERE u.username_norm = ?`, normalizeUsername(username))
	user, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return user, err
}

func (s *Store) ExportUserData(ctx context.Context, ownerUserID string) (UserDataBundle, error) {
	if err := s.EnsureUserDataOwner(ctx, ownerUserID); err != nil {
		return UserDataBundle{}, err
	}
	dirs, err := s.ListUserDirectories(ctx, ownerUserID)
	if err != nil {
		return UserDataBundle{}, err
	}
	connectors, err := s.ListUserConnectors(ctx, ownerUserID)
	if err != nil {
		return UserDataBundle{}, err
	}
	return NewUserDataBundle(dirs, connectors), nil
}

func (s *Store) ImportUserData(ctx context.Context, ownerUserID string, bundle UserDataBundle, replace bool) error {
	if err := bundle.validate(); err != nil {
		return err
	}
	if err := s.EnsureUserDataOwner(ctx, ownerUserID); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if replace {
		if _, err := tx.ExecContext(ctx, `DELETE FROM user_connector_directories WHERE owner_user_id = ?`, ownerUserID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM user_connectors WHERE owner_user_id = ?`, ownerUserID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM user_directories WHERE owner_user_id = ?`, ownerUserID); err != nil {
			return err
		}
	}
	for _, d := range bundle.Directories {
		d.TeamID = ""
		if err := upsertUserDirectory(ctx, tx, ownerUserID, d); err != nil {
			return err
		}
	}
	for _, c := range bundle.Connectors {
		c.TeamID = ""
		if err := upsertUserConnector(ctx, tx, ownerUserID, c); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ListUserDirectories(ctx context.Context, ownerUserID string) ([]config.Directory, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, team_id, name, description, backend, local_path,
		       git_remote_url, git_branch, git_base_path, git_author_name, git_author_email, git_auth_username, git_auth_token, git_ssh_key_path, git_wait_for_writes, git_save_every,
		       created_at
		  FROM user_directories
		 WHERE owner_user_id = ?
		 ORDER BY lower(name)`, ownerUserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []config.Directory
	for rows.Next() {
		var d config.Directory
		var git config.Git
		var teamID sql.NullString
		var created string
		if err := rows.Scan(
			&d.ID, &teamID, &d.Name, &d.Description, &d.Backend, &d.LocalPath,
			&git.RemoteURL, &git.Branch, &git.BasePath, &git.AuthorName, &git.AuthorEmail, &git.AuthUsername, &git.AuthToken, &git.SSHKeyPath, &git.WaitForWrites, &git.SaveEvery,
			&created,
		); err != nil {
			return nil, err
		}
		d.CreatedAt = mustParseTime(created)
		d.OwnerUserID = ownerUserID
		if teamID.Valid {
			d.TeamID = teamID.String
		}
		if d.Backend == "git" {
			d.Git = &git
			d.LocalPath = ""
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) ListUserConnectors(ctx context.Context, ownerUserID string) ([]config.Connector, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, team_id, name, kind, token, write, created_at
		  FROM user_connectors
		 WHERE owner_user_id = ?
		 ORDER BY lower(name)`, ownerUserID)
	if err != nil {
		return nil, err
	}
	closeRows := true
	defer func() {
		if closeRows {
			_ = rows.Close()
		}
	}()
	var out []config.Connector
	for rows.Next() {
		var c config.Connector
		var write int
		var teamID sql.NullString
		var created string
		if err := rows.Scan(&c.ID, &teamID, &c.Name, &c.Kind, &c.Token, &write, &created); err != nil {
			return nil, err
		}
		c.Write = write != 0
		c.CreatedAt = mustParseTime(created)
		c.OwnerUserID = ownerUserID
		if teamID.Valid {
			c.TeamID = teamID.String
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	closeRows = false
	for i := range out {
		dirs, err := s.connectorDirectoryIDs(ctx, ownerUserID, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].DirectoryIDs = dirs
	}
	return out, nil
}

func (s *Store) DeleteUserDirectory(ctx context.Context, ownerUserID, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM user_directories WHERE owner_user_id = ? AND id = ?`, ownerUserID, id)
	if err != nil {
		return err
	}
	return rowsAffectedOrNotFound(res)
}

func (s *Store) DeleteUserConnector(ctx context.Context, ownerUserID, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM user_connectors WHERE owner_user_id = ? AND id = ?`, ownerUserID, id)
	if err != nil {
		return err
	}
	return rowsAffectedOrNotFound(res)
}

func (s *Store) UpsertUserDirectory(ctx context.Context, ownerUserID string, d config.Directory) error {
	if err := s.EnsureUserDataOwner(ctx, ownerUserID); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if err := upsertUserDirectory(ctx, tx, ownerUserID, d); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) UpsertUserConnector(ctx context.Context, ownerUserID string, c config.Connector) error {
	if err := s.EnsureUserDataOwner(ctx, ownerUserID); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if err := upsertUserConnector(ctx, tx, ownerUserID, c); err != nil {
		return err
	}
	return tx.Commit()
}

func upsertUserDirectory(ctx context.Context, tx *sql.Tx, ownerUserID string, d config.Directory) error {
	now := nowString()
	created := now
	if !d.CreatedAt.IsZero() {
		created = d.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	var git config.Git
	if d.Git != nil {
		git = *d.Git
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO user_directories(
			owner_user_id, id, team_id, name, description, backend, local_path,
			git_remote_url, git_branch, git_base_path, git_author_name, git_author_email, git_auth_username, git_auth_token, git_ssh_key_path, git_wait_for_writes, git_save_every,
			created_at, updated_at
		)
		VALUES (?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(owner_user_id, id) DO UPDATE SET
			team_id = excluded.team_id,
			name = excluded.name,
			description = excluded.description,
			backend = excluded.backend,
			local_path = excluded.local_path,
			git_remote_url = excluded.git_remote_url,
			git_branch = excluded.git_branch,
			git_base_path = excluded.git_base_path,
			git_author_name = excluded.git_author_name,
			git_author_email = excluded.git_author_email,
			git_auth_username = excluded.git_auth_username,
			git_auth_token = excluded.git_auth_token,
			git_ssh_key_path = excluded.git_ssh_key_path,
			git_wait_for_writes = excluded.git_wait_for_writes,
			git_save_every = excluded.git_save_every,
			updated_at = excluded.updated_at`,
		ownerUserID, d.ID, d.TeamID, d.Name, d.Description, d.Backend, d.LocalPath,
		git.RemoteURL, git.Branch, git.BasePath, git.AuthorName, git.AuthorEmail, git.AuthUsername, git.AuthToken, git.SSHKeyPath, git.WaitForWrites, git.SaveEvery,
		created, now)
	return err
}

func upsertUserConnector(ctx context.Context, tx *sql.Tx, ownerUserID string, c config.Connector) error {
	now := nowString()
	created := now
	if !c.CreatedAt.IsZero() {
		created = c.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	write := 0
	if c.Write {
		write = 1
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO user_connectors(owner_user_id, id, team_id, name, kind, token, write, created_at, updated_at)
		VALUES (?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?)
		ON CONFLICT(owner_user_id, id) DO UPDATE SET
			team_id = excluded.team_id,
			name = excluded.name,
			kind = excluded.kind,
			token = excluded.token,
			write = excluded.write,
			updated_at = excluded.updated_at`,
		ownerUserID, c.ID, c.TeamID, c.Name, c.EffectiveKind(), c.Token, write, created, now)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_connector_directories WHERE owner_user_id = ? AND connector_id = ?`, ownerUserID, c.ID); err != nil {
		return err
	}
	for _, id := range c.DirectoryIDs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO user_connector_directories(owner_user_id, connector_id, directory_id)
			VALUES (?, ?, ?)`, ownerUserID, c.ID, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) connectorDirectoryIDs(ctx context.Context, ownerUserID, connectorID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT directory_id
		  FROM user_connector_directories
		 WHERE owner_user_id = ? AND connector_id = ?
		 ORDER BY directory_id`, ownerUserID, connectorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (s *Store) EnsureUserDataOwner(ctx context.Context, ownerUserID string) error {
	user, err := s.UserByID(ctx, ownerUserID)
	if err != nil {
		return err
	}
	if user.SuperAdmin {
		return fmt.Errorf("%w: super admin accounts cannot own user data", ErrForbidden)
	}
	return nil
}

func rowsAffectedOrNotFound(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
