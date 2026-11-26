package account

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/debkosh/synckosh"
	"github.com/debkosh/synckosh/server"
)

// SyncStore is the synckosh server.Store over memd's database: sync_stores
// holds each bucket's store id and last sequence number, sync_items the
// newest version of each item as stored, JSON-encoded. The private part is
// sealed by the devices; nothing here opens it. The database has one
// connection and every write runs in an immediate transaction, so an Update
// is its bucket's only writer and a read sees only whole Updates.
type SyncStore struct{ db *sql.DB }

// SyncStore returns the synckosh store kept in this database.
func (s *Store) SyncStore() *SyncStore { return &SyncStore{db: s.db} }

var _ server.Store = (*SyncStore)(nil)

func (s *SyncStore) StoreID(bucket string) (string, error) {
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer rollback(tx)
	var id string
	err = tx.QueryRowContext(ctx, `SELECT store_id FROM sync_stores WHERE bucket = ?`, bucket).Scan(&id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if id != "" {
		return id, nil
	}
	id = server.NewStoreID()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sync_stores(bucket, store_id, last_seq) VALUES (?, ?, 0)
		ON CONFLICT(bucket) DO UPDATE SET store_id = excluded.store_id`, bucket, id); err != nil {
		return "", err
	}
	return id, tx.Commit()
}

func (s *SyncStore) SetStoreID(bucket, id string) error {
	_, err := s.db.Exec(`
		INSERT INTO sync_stores(bucket, store_id, last_seq) VALUES (?, ?, 0)
		ON CONFLICT(bucket) DO UPDATE SET store_id = excluded.store_id`, bucket, id)
	return err
}

func (s *SyncStore) ResetAll() error {
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	rows, err := tx.QueryContext(ctx, `SELECT bucket FROM sync_stores`)
	if err != nil {
		return err
	}
	var buckets []string
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			_ = rows.Close()
			return err
		}
		buckets = append(buckets, b)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, b := range buckets {
		if _, err := tx.ExecContext(ctx, `UPDATE sync_stores SET store_id = ? WHERE bucket = ?`, server.NewStoreID(), b); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Since reads the bucket's items by sequence, of the filter's collections in SQL, and leaves out
// in Go those of datatypes the filter doesn't ask for.
func (s *SyncStore) Since(bucket string, f synckosh.Filter, after uint64, fn func(synckosh.Item) bool) error {
	q := `SELECT item FROM sync_items WHERE bucket = ? AND seq > ?`
	args := []any{bucket, after}
	if len(f.Collections) > 0 {
		q += ` AND collection IN (?` + strings.Repeat(`, ?`, len(f.Collections)-1) + `)`
		for _, c := range f.Collections {
			args = append(args, c)
		}
	}
	rows, err := s.db.Query(q+` ORDER BY seq`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return err
		}
		var it synckosh.Item
		if err := json.Unmarshal(raw, &it); err != nil {
			return err
		}
		if !f.Match(it) {
			continue
		}
		if !fn(it) {
			return nil
		}
	}
	return rows.Err()
}

func (s *SyncStore) Update(bucket string, fn func(server.Tx) error) error {
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if err := fn(&syncTx{ctx: ctx, tx: tx, bucket: bucket}); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SyncStore) Cleanup(deletedBefore int64) (int, error) {
	res, err := s.db.Exec(`DELETE FROM sync_items WHERE deleted = 1 AND deleted_at < ?`, deletedBefore)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

type syncTx struct {
	ctx    context.Context
	tx     *sql.Tx
	bucket string
}

func (t *syncTx) Last() (uint64, error) {
	var n uint64
	err := t.tx.QueryRowContext(t.ctx, `SELECT last_seq FROM sync_stores WHERE bucket = ?`, t.bucket).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return n, err
}

func (t *syncTx) Get(collection, guid string) (synckosh.Item, bool, error) {
	var it synckosh.Item
	var raw []byte
	err := t.tx.QueryRowContext(t.ctx, `SELECT item FROM sync_items WHERE bucket = ? AND collection = ? AND guid = ?`,
		t.bucket, collection, guid).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return it, false, nil
	}
	if err != nil {
		return it, false, err
	}
	return it, true, json.Unmarshal(raw, &it)
}

// Identity finds a live item of the collection by its identity, in the index
// of live items by identity.
func (t *syncTx) Identity(collection, identity string) (synckosh.Item, bool, error) {
	var it synckosh.Item
	var raw []byte
	err := t.tx.QueryRowContext(t.ctx, `SELECT item FROM sync_items
		WHERE bucket = ? AND collection = ? AND identity = ? AND identity != '' AND deleted = 0 LIMIT 1`,
		t.bucket, collection, identity).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return it, false, nil
	}
	if err != nil {
		return it, false, err
	}
	return it, true, json.Unmarshal(raw, &it)
}

func (t *syncTx) Put(items []synckosh.Item) error {
	last, err := t.Last()
	if err != nil {
		return err
	}
	for _, it := range items {
		raw, err := synckosh.Marshal(it)
		if err != nil {
			return err
		}
		deleted := 0
		if it.Deleted {
			deleted = 1
		}
		if _, err := t.tx.ExecContext(t.ctx, `
			INSERT INTO sync_items(bucket, collection, guid, seq, timestamp, deleted, deleted_at, item, identity)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(bucket, collection, guid) DO UPDATE SET
				seq = excluded.seq, timestamp = excluded.timestamp, deleted = excluded.deleted,
				deleted_at = excluded.deleted_at, item = excluded.item, identity = excluded.identity`,
			t.bucket, it.Collection, it.GUID, it.Sequence, it.Timestamp, deleted, it.DeletedAt, raw, it.Identity); err != nil {
			return err
		}
		last = max(last, it.Sequence)
	}
	_, err = t.tx.ExecContext(t.ctx, `
		INSERT INTO sync_stores(bucket, store_id, last_seq) VALUES (?, '', ?)
		ON CONFLICT(bucket) DO UPDATE SET last_seq = excluded.last_seq`, t.bucket, last)
	return err
}

func (t *syncTx) RemoveBefore(collection string, before int64) error {
	_, err := t.tx.ExecContext(t.ctx, `
		DELETE FROM sync_items WHERE bucket = ? AND collection = ? AND timestamp <= ?`,
		t.bucket, collection, before)
	return err
}
