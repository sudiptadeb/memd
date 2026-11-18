package account

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sudiptadeb/memd/server/internal/token"
)

// A sync bucket is one synckosh bucket: a synced space for a user's devices,
// reached at /sync with its token (see internal/syncbucket). The token is
// shown once, when the bucket is made or given a new token; only its sha256
// hex is stored. Its items live in sync_stores and sync_items under the
// bucket's id.

const syncBucketNameMax = 64

// SyncBucket is one of a user's sync buckets.
type SyncBucket struct {
	ID        string
	UserID    string
	Name      string
	CreatedAt time.Time
}

// CreateSyncBucket makes a bucket for userID and returns it with its token.
func (s *Store) CreateSyncBucket(ctx context.Context, userID, name string) (SyncBucket, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return SyncBucket{}, "", errors.New("name is required")
	}
	if len(name) > syncBucketNameMax {
		return SyncBucket{}, "", fmt.Errorf("name is longer than %d characters", syncBucketNameMax)
	}
	user, err := s.UserByID(ctx, userID)
	if err != nil {
		return SyncBucket{}, "", err
	}
	if user.Disabled {
		return SyncBucket{}, "", fmt.Errorf("%w: disabled users cannot make sync buckets", ErrForbidden)
	}
	raw, err := token.New()
	if err != nil {
		return SyncBucket{}, "", err
	}
	now := nowString()
	b := SyncBucket{ID: newID("syn"), UserID: userID, Name: name, CreatedAt: mustParseTime(now)}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO sync_buckets(id, user_id, name, token_hash, created_at) VALUES (?, ?, ?, ?, ?)`,
		b.ID, b.UserID, b.Name, syncTokenHash(raw), now)
	if err != nil {
		return SyncBucket{}, "", err
	}
	return b, raw, nil
}

// ListSyncBuckets returns the user's buckets, oldest first.
func (s *Store) ListSyncBuckets(ctx context.Context, userID string) ([]SyncBucket, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, name, created_at FROM sync_buckets
		 WHERE user_id = ? ORDER BY created_at, id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SyncBucket
	for rows.Next() {
		b, err := scanSyncBucket(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// SyncBucketByToken resolves a token to its bucket. An unknown or replaced
// token, or one whose owner is disabled, is ErrNotFound.
func (s *Store) SyncBucketByToken(ctx context.Context, rawToken string) (SyncBucket, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT b.id, b.user_id, b.name, b.created_at FROM sync_buckets b
		  JOIN users u ON u.id = b.user_id
		 WHERE b.token_hash = ? AND u.disabled = 0`, syncTokenHash(rawToken))
	b, err := scanSyncBucket(row)
	if errors.Is(err, sql.ErrNoRows) {
		return SyncBucket{}, ErrNotFound
	}
	return b, err
}

// RotateSyncBucketToken gives one of the user's buckets a new token and
// returns it. The old token stops working at once; the items stay.
func (s *Store) RotateSyncBucketToken(ctx context.Context, userID, id string) (SyncBucket, string, error) {
	raw, err := token.New()
	if err != nil {
		return SyncBucket{}, "", err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE sync_buckets SET token_hash = ? WHERE id = ? AND user_id = ?`,
		syncTokenHash(raw), id, userID)
	if err != nil {
		return SyncBucket{}, "", err
	}
	if err := rowsAffectedOrNotFound(res); err != nil {
		return SyncBucket{}, "", err
	}
	b, err := scanSyncBucket(s.db.QueryRowContext(ctx, `SELECT id, user_id, name, created_at FROM sync_buckets WHERE id = ?`, id))
	return b, raw, err
}

// DeleteSyncBucket removes one of the user's buckets and everything synced
// to it.
func (s *Store) DeleteSyncBucket(ctx context.Context, userID, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	res, err := tx.ExecContext(ctx, `DELETE FROM sync_buckets WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	if err := rowsAffectedOrNotFound(res); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sync_items WHERE bucket = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sync_stores WHERE bucket = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func syncTokenHash(rawToken string) string {
	sum := sha256.Sum256([]byte(rawToken))
	return hex.EncodeToString(sum[:])
}

func scanSyncBucket(row userScanner) (SyncBucket, error) {
	var b SyncBucket
	var created string
	if err := row.Scan(&b.ID, &b.UserID, &b.Name, &created); err != nil {
		return SyncBucket{}, err
	}
	b.CreatedAt = mustParseTime(created)
	return b, nil
}
