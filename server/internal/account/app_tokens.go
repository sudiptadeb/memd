package account

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// App tokens are the long-lived credentials behind phone-app pairing. The
// dashboard mints a short-lived pairing code (in-memory, ui package); redeeming
// it creates one row here and hands the app the raw secret exactly once. Like
// team invites, only the sha256 hex of the secret is stored. Tokens never
// expire — revocation is the lifecycle.

const (
	appTokenPrefix = "mat_"
	// appTokenLabelMax caps the app-supplied label; longer labels are truncated
	// rather than rejected so an over-eager device name cannot fail a pairing.
	appTokenLabelMax = 64
	// appTokenTouchInterval throttles last_used_at writes: the app refreshes its
	// session at most every ~24h, but a misbehaving client should not turn every
	// request into a write.
	appTokenTouchInterval = time.Minute
)

// AppToken is one paired phone. The raw secret is never stored and never
// reconstructable from this record.
type AppToken struct {
	ID         string
	UserID     string
	Label      string
	CreatedAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

// CreateAppToken mints a new app token for userID and returns the record plus
// the raw secret ("mat_" + 43 base64url chars). The caller shows the secret
// once; only its hash is persisted. Disabled or unknown users are rejected.
func (s *Store) CreateAppToken(ctx context.Context, userID, label string) (AppToken, string, error) {
	user, err := s.UserByID(ctx, userID)
	if err != nil {
		return AppToken{}, "", err
	}
	if user.Disabled {
		return AppToken{}, "", fmt.Errorf("%w: disabled users cannot pair the app", ErrForbidden)
	}
	secret, err := newAppTokenSecret()
	if err != nil {
		return AppToken{}, "", err
	}
	label = strings.TrimSpace(label)
	if len(label) > appTokenLabelMax {
		label = label[:appTokenLabelMax]
	}
	now := nowString()
	tok := AppToken{
		ID:        newID("apt"),
		UserID:    userID,
		Label:     label,
		CreatedAt: mustParseTime(now),
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO app_tokens(id, user_id, token_hash, label, created_at, last_used_at, revoked_at)
		VALUES (?, ?, ?, ?, ?, NULL, NULL)`,
		tok.ID, tok.UserID, appTokenHash(secret), tok.Label, now)
	if err != nil {
		if isUniqueErr(err) {
			return AppToken{}, "", ErrAlreadyExists
		}
		return AppToken{}, "", err
	}
	return tok, secret, nil
}

// AppTokenByToken resolves a raw secret to its active token. Unknown and
// revoked tokens are indistinguishable to the caller: both are ErrNotFound.
func (s *Store) AppTokenByToken(ctx context.Context, rawToken string) (AppToken, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, label, created_at, last_used_at, revoked_at
		  FROM app_tokens
		 WHERE token_hash = ?`, appTokenHash(rawToken))
	tok, err := scanAppToken(row)
	if errors.Is(err, sql.ErrNoRows) {
		return AppToken{}, ErrNotFound
	}
	if err != nil {
		return AppToken{}, err
	}
	if tok.RevokedAt != nil {
		return AppToken{}, ErrNotFound
	}
	return tok, nil
}

// ListAppTokens returns the user's active (non-revoked) tokens, newest first.
func (s *Store) ListAppTokens(ctx context.Context, userID string) ([]AppToken, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, label, created_at, last_used_at, revoked_at
		  FROM app_tokens
		 WHERE user_id = ? AND revoked_at IS NULL
		 ORDER BY created_at DESC, id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AppToken
	for rows.Next() {
		tok, err := scanAppToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, tok)
	}
	return out, rows.Err()
}

// RevokeAppToken revokes one of the user's tokens by id (the dashboard's
// un-pair action). ErrNotFound when the id is not the user's or already revoked.
func (s *Store) RevokeAppToken(ctx context.Context, userID, id string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE app_tokens
		   SET revoked_at = ?
		 WHERE id = ? AND user_id = ? AND revoked_at IS NULL`, nowString(), id, userID)
	if err != nil {
		return err
	}
	return rowsAffectedOrNotFound(res)
}

// RevokeAppTokenByToken revokes the token identified by its raw secret (the
// app's bearer-authorized sign-out). ErrNotFound when unknown or already
// revoked.
func (s *Store) RevokeAppTokenByToken(ctx context.Context, rawToken string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE app_tokens
		   SET revoked_at = ?
		 WHERE token_hash = ? AND revoked_at IS NULL`, nowString(), appTokenHash(rawToken))
	if err != nil {
		return err
	}
	return rowsAffectedOrNotFound(res)
}

// TouchAppToken updates the token's last_used_at, throttled to at most one
// write per appTokenTouchInterval. The read-then-write is racy under
// concurrency, but the only cost is an extra timestamp write.
func (s *Store) TouchAppToken(ctx context.Context, id string) error {
	var last sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT last_used_at FROM app_tokens WHERE id = ?`, id).Scan(&last)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if t := parseOptionalTime(last); t != nil && now.Sub(*t) < appTokenTouchInterval {
		return nil
	}
	_, err = s.db.ExecContext(ctx, `UPDATE app_tokens SET last_used_at = ? WHERE id = ?`, now.Format(time.RFC3339Nano), id)
	return err
}

// newAppTokenSecret returns "mat_" + 43 base64url chars (32 random bytes).
func newAppTokenSecret() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return appTokenPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

func appTokenHash(rawToken string) string {
	sum := sha256.Sum256([]byte(rawToken))
	return hex.EncodeToString(sum[:])
}

func scanAppToken(row userScanner) (AppToken, error) {
	var tok AppToken
	var created string
	var lastUsed, revoked sql.NullString
	if err := row.Scan(&tok.ID, &tok.UserID, &tok.Label, &created, &lastUsed, &revoked); err != nil {
		return AppToken{}, err
	}
	tok.CreatedAt = mustParseTime(created)
	tok.LastUsedAt = parseOptionalTime(lastUsed)
	tok.RevokedAt = parseOptionalTime(revoked)
	return tok, nil
}
