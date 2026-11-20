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
	"github.com/sudiptadeb/synckosh/server"
)

// A sync bucket is one synckosh bucket: a synced space for a user's devices,
// reached at /sync with its token (see internal/syncbucket). The token is kept
// readable, like a connector's, so its owner can set up another device from
// the dashboard; requests are looked up by its sha256 hex. Its items live in
// sync_stores and sync_items under the bucket's id.

const syncBucketNameMax = 64

// SyncBucket is one of a user's sync buckets.
type SyncBucket struct {
	ID     string
	UserID string
	Name   string
	// Token is empty for a bucket made while only the token's hash was kept;
	// its old token still works, and a new token makes one to show.
	Token     string
	CreatedAt time.Time
}

// CreateSyncBucket makes a bucket for userID, with a new token.
func (s *Store) CreateSyncBucket(ctx context.Context, userID, name string) (SyncBucket, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return SyncBucket{}, errors.New("name is required")
	}
	if len(name) > syncBucketNameMax {
		return SyncBucket{}, fmt.Errorf("name is longer than %d characters", syncBucketNameMax)
	}
	user, err := s.UserByID(ctx, userID)
	if err != nil {
		return SyncBucket{}, err
	}
	if user.Disabled {
		return SyncBucket{}, fmt.Errorf("%w: disabled users cannot make sync buckets", ErrForbidden)
	}
	raw, err := token.New()
	if err != nil {
		return SyncBucket{}, err
	}
	now := nowString()
	b := SyncBucket{ID: newID("syn"), UserID: userID, Name: name, Token: raw, CreatedAt: mustParseTime(now)}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO sync_buckets(id, user_id, name, token, token_hash, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		b.ID, b.UserID, b.Name, raw, syncTokenHash(raw), now)
	if err != nil {
		return SyncBucket{}, err
	}
	return b, nil
}

// ListSyncBuckets returns the user's buckets, oldest first.
func (s *Store) ListSyncBuckets(ctx context.Context, userID string) ([]SyncBucket, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, name, token, created_at FROM sync_buckets
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
		SELECT b.id, b.user_id, b.name, b.token, b.created_at FROM sync_buckets b
		  JOIN users u ON u.id = b.user_id
		 WHERE b.token_hash = ? AND u.disabled = 0`, syncTokenHash(rawToken))
	b, err := scanSyncBucket(row)
	if errors.Is(err, sql.ErrNoRows) {
		return SyncBucket{}, ErrNotFound
	}
	return b, err
}

// RotateSyncBucketToken gives one of the user's buckets a new token. The old
// token stops working at once; the items stay.
func (s *Store) RotateSyncBucketToken(ctx context.Context, userID, id string) (SyncBucket, error) {
	raw, err := token.New()
	if err != nil {
		return SyncBucket{}, err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE sync_buckets SET token = ?, token_hash = ? WHERE id = ? AND user_id = ?`,
		raw, syncTokenHash(raw), id, userID)
	if err != nil {
		return SyncBucket{}, err
	}
	if err := rowsAffectedOrNotFound(res); err != nil {
		return SyncBucket{}, err
	}
	return scanSyncBucket(s.db.QueryRowContext(ctx, `SELECT id, user_id, name, token, created_at FROM sync_buckets WHERE id = ?`, id))
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

// ResetSyncBucket makes the server forget one of the user's buckets' items
// and gives the bucket a new store id, as synckosh's server.ResetStore does,
// in one transaction. The sealed copies of its key go too, so every device
// still in the bucket stops on its next sync and is set up again, with a new
// key. The bucket and its token stay.
func (s *Store) ResetSyncBucket(ctx context.Context, userID, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	var one int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM sync_buckets WHERE id = ? AND user_id = ?`, id, userID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sync_items WHERE bucket = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sync_stores(bucket, store_id, last_seq) VALUES (?, ?, 0)
		ON CONFLICT(bucket) DO UPDATE SET store_id = excluded.store_id`, id, server.NewStoreID()); err != nil {
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
	if err := row.Scan(&b.ID, &b.UserID, &b.Name, &b.Token, &created); err != nil {
		return SyncBucket{}, err
	}
	b.CreatedAt = mustParseTime(created)
	return b, nil
}
