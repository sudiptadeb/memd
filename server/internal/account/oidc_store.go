package account

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// OIDCIdentity is the subset of verified ID-token claims used to provision a
// cloud account on OIDC login, plus the provider slot the login came through.
type OIDCIdentity struct {
	// ProviderID is the stable id of the configured provider slot. It — not
	// the issuer URL — is the identity boundary, so the same IdP behind a new
	// domain keeps resolving to the same accounts.
	ProviderID        string
	Issuer            string // `iss` — recorded for display/audit
	Subject           string // `sub` — stable within the provider
	Email             string
	Name              string
	PreferredUsername string
}

// UserByOIDCIdentity looks up a user by the provider+subject identity pair.
func (s *Store) UserByOIDCIdentity(ctx context.Context, providerID, subject string) (User, error) {
	providerID = strings.TrimSpace(providerID)
	subject = strings.TrimSpace(subject)
	if providerID == "" || subject == "" {
		return User{}, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT `+userColumns+`
		  `+userJoin+`
		 WHERE u.provider_id = ? AND u.subject = ?`, providerID, subject)
	user, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return user, err
}

// UpsertOIDCUser provisions the cloud account for a verified OIDC identity and
// records the login. Resolution order:
//
//  1. Existing cloud account already keyed on provider+subject -> refresh
//     display fields and the recorded issuer URL.
//  2. Otherwise auto-provision a fresh cloud account keyed on provider+subject.
//
// Identity is keyed on the provider slot + `sub`; the issuer URL, email, and
// name are stored for display only and never used to link a local account.
func (s *Store) UpsertOIDCUser(ctx context.Context, id OIDCIdentity) (User, error) {
	if ok, err := s.IsInitialized(ctx); err != nil || !ok {
		if err != nil {
			return User{}, err
		}
		return User{}, ErrNotInitialized
	}
	providerID := strings.TrimSpace(id.ProviderID)
	if providerID == "" {
		return User{}, errors.New("oidc provider id is required")
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

	userID, err := resolveOIDCUserID(ctx, tx, providerID, issuer, subject, id, now)
	if err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	return s.UserByID(ctx, userID)
}

func resolveOIDCUserID(ctx context.Context, tx *sql.Tx, providerID, issuer, subject string, id OIDCIdentity, now string) (string, error) {
	// 1. Already linked to this provider+subject. The issuer column tracks the
	// URL the user last signed in through.
	var existingID string
	err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE provider_id = ? AND subject = ?`, providerID, subject).Scan(&existingID)
	if err == nil {
		if _, err := tx.ExecContext(ctx,
			`UPDATE users SET email = ?, issuer = ?, display_name = COALESCE(NULLIF(display_name, ''), ?), last_login_at = ?, updated_at = ? WHERE id = ?`,
			id.Email, issuer, id.Name, now, now, existingID); err != nil {
			return "", err
		}
		return existingID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}

	// 2. Auto-provision a fresh cloud account keyed on provider+subject. Local
	// accounts are never linked automatically by username or email.
	username, err := uniqueUsername(ctx, tx, oidcUsername(id, subject))
	if err != nil {
		return "", err
	}
	newUserID := newID("usr")
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO users(id, username, username_norm, password_hash, display_name, email, issuer, subject, provider_id, disabled, created_at, updated_at, password_changed_at, last_login_at)
		VALUES (?, ?, ?, '', ?, ?, ?, ?, ?, 0, ?, ?, ?, ?)`,
		newUserID, username, normalizeUsername(username), id.Name, id.Email, issuer, subject, providerID, now, now, now, now); err != nil {
		return "", err
	}
	return newUserID, nil
}

// UnlinkUserOIDC detaches a user from its SSO identity, freeing the
// provider+subject pair for another account. Intended for cleaning up
// accidentally auto-provisioned duplicates: the unlinked account can no longer
// sign in via SSO (and, without a password, not at all).
func (s *Store) UnlinkUserOIDC(ctx context.Context, userID string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET provider_id = '', issuer = '', subject = NULL, updated_at = ? WHERE id = ?`,
		nowString(), userID)
	if err != nil {
		return err
	}
	return rowsAffectedOrNotFound(res)
}

// AdoptOIDCUsersIntoProvider links orphaned OIDC users (provider_id = '') whose
// recorded issuer matches fromIssuer to the given provider slot. It returns the
// number adopted and the usernames skipped because another account already
// holds the same subject under that provider. This is the admin repair tool for
// accounts orphaned before provider-slot keying existed.
func (s *Store) AdoptOIDCUsersIntoProvider(ctx context.Context, providerID, fromIssuer string) (int, []string, error) {
	providerID = strings.TrimSpace(providerID)
	fromIssuer = strings.TrimRight(strings.TrimSpace(fromIssuer), "/")
	if providerID == "" || fromIssuer == "" {
		return 0, nil, errors.New("provider id and issuer are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, nil, err
	}
	defer rollback(tx)
	rows, err := tx.QueryContext(ctx, `
		SELECT u.username FROM users u
		 WHERE u.provider_id = '' AND u.issuer = ? AND u.subject IS NOT NULL AND u.subject != ''
		   AND EXISTS (SELECT 1 FROM users v WHERE v.provider_id = ? AND v.subject = u.subject)`,
		fromIssuer, providerID)
	if err != nil {
		return 0, nil, err
	}
	var skipped []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return 0, nil, err
		}
		skipped = append(skipped, name)
	}
	if err := rows.Err(); err != nil {
		return 0, nil, err
	}
	if err := rows.Close(); err != nil {
		return 0, nil, err
	}
	res, err := tx.ExecContext(ctx, `
		UPDATE users SET provider_id = ?, updated_at = ?
		 WHERE provider_id = '' AND issuer = ? AND subject IS NOT NULL AND subject != ''
		   AND NOT EXISTS (SELECT 1 FROM users v WHERE v.provider_id = ? AND v.subject = users.subject)`,
		providerID, nowString(), fromIssuer, providerID)
	if err != nil {
		return 0, nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil, err
	}
	if err := tx.Commit(); err != nil {
		return 0, nil, err
	}
	return int(n), skipped, nil
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
