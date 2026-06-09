package account

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// OIDCIdentity is the subset of verified ID-token claims used to provision or
// link a local account on OIDC login. It is IdP-agnostic: every field is a
// standard OIDC claim.
type OIDCIdentity struct {
	Subject           string // `sub` — stable, unique per IdP; the identity key
	Email             string
	Name              string
	PreferredUsername string
	Admin             bool // derived from claims/allowlist by the caller
}

// UserBySubject looks up a user by their OIDC subject.
func (s *Store) UserBySubject(ctx context.Context, subject string) (User, error) {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return User{}, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT `+userColumns+`
		  `+userJoin+`
		 WHERE u.subject = ?`, subject)
	user, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return user, err
}

// SetSuperAdmin grants super-admin to a user. OIDC logins only ever grant (never
// revoke) admin, so an admin who is later removed from the IdP group is not
// silently locked out of an account they bootstrapped; use the admin UI to
// disable accounts instead.
func (s *Store) SetSuperAdmin(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO super_admins(user_id, created_at, created_by_user_id) VALUES (?, ?, NULL)`,
		userID, nowString())
	return err
}

// UpsertOIDCUser provisions or links the local account for a verified OIDC
// identity and records the login. Resolution order:
//
//  1. Existing account already keyed on this subject -> refresh display fields.
//  2. A local account whose username matches the token's preferred_username or
//     email (and which has no subject yet) -> attach the subject (one-time
//     migration of a manually-created account).
//  3. Otherwise auto-provision a fresh account keyed on the subject.
//
// Identity is always keyed on `sub`; email/name are stored for display only.
func (s *Store) UpsertOIDCUser(ctx context.Context, id OIDCIdentity) (User, error) {
	if ok, err := s.IsInitialized(ctx); err != nil || !ok {
		if err != nil {
			return User{}, err
		}
		return User{}, ErrNotInitialized
	}
	subject := strings.TrimSpace(id.Subject)
	if subject == "" {
		return User{}, errors.New("oidc subject is required")
	}
	now := nowString()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer rollback(tx)

	userID, err := resolveOIDCUserID(ctx, tx, subject, id, now)
	if err != nil {
		return User{}, err
	}
	if id.Admin {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO super_admins(user_id, created_at, created_by_user_id) VALUES (?, ?, NULL)`,
			userID, now); err != nil {
			return User{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	return s.UserByID(ctx, userID)
}

func resolveOIDCUserID(ctx context.Context, tx *sql.Tx, subject string, id OIDCIdentity, now string) (string, error) {
	// 1. Already linked to this subject.
	var existingID string
	err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE subject = ?`, subject).Scan(&existingID)
	if err == nil {
		if _, err := tx.ExecContext(ctx,
			`UPDATE users SET email = ?, display_name = COALESCE(NULLIF(display_name, ''), ?), last_login_at = ?, updated_at = ? WHERE id = ?`,
			id.Email, id.Name, now, now, existingID); err != nil {
			return "", err
		}
		return existingID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}

	// 2. Link an existing local account by username == preferred_username/email.
	for _, candidate := range []string{id.PreferredUsername, id.Email} {
		norm := normalizeUsername(candidate)
		if norm == "" {
			continue
		}
		var localID string
		err := tx.QueryRowContext(ctx,
			`SELECT id FROM users WHERE username_norm = ? AND (subject IS NULL OR subject = '')`, norm).Scan(&localID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return "", err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE users SET subject = ?, email = ?, display_name = COALESCE(NULLIF(display_name, ''), ?), last_login_at = ?, updated_at = ? WHERE id = ?`,
			subject, id.Email, id.Name, now, now, localID); err != nil {
			return "", err
		}
		return localID, nil
	}

	// 3. Auto-provision a fresh account keyed on the subject.
	username, err := uniqueUsername(ctx, tx, oidcUsername(id, subject))
	if err != nil {
		return "", err
	}
	newUserID := newID("usr")
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO users(id, username, username_norm, password_hash, display_name, email, subject, disabled, created_at, updated_at, password_changed_at, last_login_at)
		VALUES (?, ?, ?, '', ?, ?, ?, 0, ?, ?, ?, ?)`,
		newUserID, username, normalizeUsername(username), id.Name, id.Email, subject, now, now, now, now); err != nil {
		return "", err
	}
	return newUserID, nil
}

// oidcUsername picks a human-friendly base username from the identity, falling
// back through preferred_username -> email local-part -> a subject-derived id.
func oidcUsername(id OIDCIdentity, subject string) string {
	if u := strings.TrimSpace(id.PreferredUsername); u != "" {
		return u
	}
	if email := strings.TrimSpace(id.Email); email != "" {
		if local, _, ok := strings.Cut(email, "@"); ok && local != "" {
			return local
		}
		return email
	}
	short := subject
	if len(short) > 12 {
		short = short[:12]
	}
	return "user-" + strings.ToLower(short)
}

// uniqueUsername returns base, or base-2, base-3, ... so username_norm stays
// unique without failing the insert.
func uniqueUsername(ctx context.Context, tx *sql.Tx, base string) (string, error) {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "user"
	}
	for attempt := 0; attempt < 1000; attempt++ {
		candidate := base
		if attempt > 0 {
			candidate = fmt.Sprintf("%s-%d", base, attempt+1)
		}
		var n int
		if err := tx.QueryRowContext(ctx,
			`SELECT count(*) FROM users WHERE username_norm = ?`, normalizeUsername(candidate)).Scan(&n); err != nil {
			return "", err
		}
		if n == 0 {
			return candidate, nil
		}
	}
	return "", errors.New("could not derive a unique username")
}
