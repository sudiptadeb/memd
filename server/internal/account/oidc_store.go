package account

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// OIDCIdentity is the subset of verified ID-token claims used to provision a
// cloud account on OIDC login. It is IdP-agnostic: every field is a standard
// OIDC claim.
type OIDCIdentity struct {
	Issuer            string // `iss` — the identity-provider boundary
	Subject           string // `sub` — stable within the issuer
	Email             string
	Name              string
	PreferredUsername string
}

// UserByOIDCIdentity looks up a user by the issuer+subject identity pair.
func (s *Store) UserByOIDCIdentity(ctx context.Context, issuer, subject string) (User, error) {
	issuer = strings.TrimRight(strings.TrimSpace(issuer), "/")
	subject = strings.TrimSpace(subject)
	if issuer == "" || subject == "" {
		return User{}, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT `+userColumns+`
		  `+userJoin+`
		 WHERE u.issuer = ? AND u.subject = ?`, issuer, subject)
	user, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return user, err
}

// UpsertOIDCUser provisions the cloud account for a verified OIDC identity and
// records the login. Resolution order:
//
//  1. Existing cloud account already keyed on issuer+subject -> refresh display fields.
//  2. Otherwise auto-provision a fresh cloud account keyed on issuer+subject.
//
// Identity is always keyed on `iss` + `sub`; email/name are stored for display
// only and never used to link a local account.
func (s *Store) UpsertOIDCUser(ctx context.Context, id OIDCIdentity) (User, error) {
	if ok, err := s.IsInitialized(ctx); err != nil || !ok {
		if err != nil {
			return User{}, err
		}
		return User{}, ErrNotInitialized
	}
	issuer := strings.TrimRight(strings.TrimSpace(id.Issuer), "/")
	if issuer == "" {
		return User{}, errors.New("oidc issuer is required")
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

	userID, err := resolveOIDCUserID(ctx, tx, issuer, subject, id, now)
	if err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	return s.UserByID(ctx, userID)
}

func resolveOIDCUserID(ctx context.Context, tx *sql.Tx, issuer, subject string, id OIDCIdentity, now string) (string, error) {
	// 1. Already linked to this issuer+subject.
	var existingID string
	err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE issuer = ? AND subject = ?`, issuer, subject).Scan(&existingID)
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

	// 2. Auto-provision a fresh cloud account keyed on issuer+subject. Local
	// accounts are never linked automatically by username or email.
	username, err := uniqueUsername(ctx, tx, oidcUsername(id, subject))
	if err != nil {
		return "", err
	}
	newUserID := newID("usr")
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO users(id, username, username_norm, password_hash, display_name, email, issuer, subject, disabled, created_at, updated_at, password_changed_at, last_login_at)
		VALUES (?, ?, ?, '', ?, ?, ?, ?, 0, ?, ?, ?, ?)`,
		newUserID, username, normalizeUsername(username), id.Name, id.Email, issuer, subject, now, now, now, now); err != nil {
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
