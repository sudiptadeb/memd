package account

import (
	"context"
	"errors"
	"testing"

	"github.com/sudiptadeb/synckosh"
	"github.com/sudiptadeb/synckosh/server"
)

func newSyncTestUser(t *testing.T, store *Store, name string) User {
	t.Helper()
	user, err := store.CreateLocalUser(context.Background(), CreateUserInput{Username: name, Password: "correct horse battery staple"})
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	return user
}

func TestSyncBucketTokenLifecycle(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	ada := newSyncTestUser(t, store, "ada")
	bob := newSyncTestUser(t, store, "bob")

	if _, err := store.CreateSyncBucket(ctx, ada.ID, "  "); err == nil {
		t.Fatal("a bucket with no name was made")
	}
	b, err := store.CreateSyncBucket(ctx, ada.ID, " Browser ")
	tok := b.Token
	if err != nil {
		t.Fatalf("CreateSyncBucket: %v", err)
	}
	if b.Name != "Browser" || len(tok) != 32 {
		t.Fatalf("bucket %+v, token length %d", b, len(tok))
	}
	var stored, hash string
	if err := store.db.QueryRowContext(ctx, `SELECT token, token_hash FROM sync_buckets WHERE id = ?`, b.ID).Scan(&stored, &hash); err != nil {
		t.Fatal(err)
	}
	if stored != tok || hash != syncTokenHash(tok) {
		t.Fatal("want the token kept readable, with its hash")
	}

	got, err := store.SyncBucketByToken(ctx, tok)
	if err != nil || got.ID != b.ID || got.UserID != ada.ID {
		t.Fatalf("SyncBucketByToken: %+v %v", got, err)
	}
	if _, err := store.SyncBucketByToken(ctx, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown token: %v", err)
	}

	list, err := store.ListSyncBuckets(ctx, ada.ID)
	if err != nil || len(list) != 1 || list[0].ID != b.ID || list[0].Token != tok {
		t.Fatalf("ListSyncBuckets: %+v %v", list, err)
	}
	if list, _ := store.ListSyncBuckets(ctx, bob.ID); len(list) != 0 {
		t.Fatalf("bob sees %+v", list)
	}

	// Only the owner can replace the token or delete the bucket.
	if _, err := store.RotateSyncBucketToken(ctx, bob.ID, b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob rotating ada's bucket: %v", err)
	}
	if err := store.DeleteSyncBucket(ctx, bob.ID, b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob deleting ada's bucket: %v", err)
	}

	rotated, err := store.RotateSyncBucketToken(ctx, ada.ID, b.ID)
	if err != nil {
		t.Fatalf("RotateSyncBucketToken: %v", err)
	}
	tok2 := rotated.Token
	if tok2 == "" || tok2 == tok {
		t.Fatal("rotate gave no new token")
	}
	if _, err := store.SyncBucketByToken(ctx, tok); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old token after rotate: %v", err)
	}
	if got, err := store.SyncBucketByToken(ctx, tok2); err != nil || got.ID != b.ID {
		t.Fatalf("new token: %+v %v", got, err)
	}

	if err := store.SetUserDisabled(ctx, ada.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SyncBucketByToken(ctx, tok2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a disabled user's token: %v", err)
	}
}

func TestDeleteSyncBucketRemovesItsItems(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	ada := newSyncTestUser(t, store, "ada")
	keep, _ := store.CreateSyncBucket(ctx, ada.ID, "keep")
	gone, _ := store.CreateSyncBucket(ctx, ada.ID, "gone")
	st := store.SyncStore()
	for _, b := range []string{keep.ID, gone.ID} {
		if _, err := st.StoreID(b); err != nil {
			t.Fatal(err)
		}
		err := st.Update(b, func(tx server.Tx) error {
			return tx.Put([]synckosh.Item{{Collection: "notes", Datatype: "note", GUID: "n1", Timestamp: 1, Sequence: 1}})
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	if err := store.DeleteSyncBucket(ctx, ada.ID, gone.ID); err != nil {
		t.Fatalf("DeleteSyncBucket: %v", err)
	}
	count := func(table, bucket string) int {
		var n int
		if err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM `+table+` WHERE bucket = ?`, bucket).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count("sync_items", gone.ID) != 0 || count("sync_stores", gone.ID) != 0 {
		t.Fatal("the deleted bucket's data is still there")
	}
	if count("sync_items", keep.ID) != 1 || count("sync_stores", keep.ID) != 1 {
		t.Fatal("deleting one bucket touched another")
	}
	if list, _ := store.ListSyncBuckets(ctx, ada.ID); len(list) != 1 || list[0].ID != keep.ID {
		t.Fatalf("after delete: %+v", list)
	}
}

// A bucket made while only the token's hash was kept: Init adds the token
// column, the bucket shows no token, its old token still syncs, and a new
// token is readable.
func TestHashOnlySyncBucket(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	if _, err := store.db.ExecContext(ctx, `CREATE TABLE sync_buckets (
		id TEXT PRIMARY KEY,
		user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		name TEXT NOT NULL,
		token_hash TEXT NOT NULL UNIQUE,
		created_at TEXT NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	ada := newSyncTestUser(t, store, "ada")
	old := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, err := store.db.ExecContext(ctx, `INSERT INTO sync_buckets(id, user_id, name, token_hash, created_at) VALUES ('syn_old', ?, 'old', ?, ?)`,
		ada.ID, syncTokenHash(old), nowString()); err != nil {
		t.Fatal(err)
	}
	if list, err := store.ListSyncBuckets(ctx, ada.ID); err != nil || len(list) != 1 || list[0].Token != "" {
		t.Fatalf("list: %+v %v", list, err)
	}
	if b, err := store.SyncBucketByToken(ctx, old); err != nil || b.ID != "syn_old" {
		t.Fatalf("the old token: %+v %v", b, err)
	}
	b, err := store.RotateSyncBucketToken(ctx, ada.ID, "syn_old")
	if err != nil || b.Token == "" {
		t.Fatalf("rotate: %+v %v", b, err)
	}
	if list, _ := store.ListSyncBuckets(ctx, ada.ID); list[0].Token != b.Token {
		t.Fatal("the new token is not shown")
	}
}

func TestResetSyncBucket(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	ada := newSyncTestUser(t, store, "ada")
	bob := newSyncTestUser(t, store, "bob")
	reset, _ := store.CreateSyncBucket(ctx, ada.ID, "reset")
	other, _ := store.CreateSyncBucket(ctx, ada.ID, "other")
	st := store.SyncStore()
	ids := map[string]string{}
	for _, b := range []string{reset.ID, other.ID} {
		id, err := st.StoreID(b)
		if err != nil {
			t.Fatal(err)
		}
		ids[b] = id
		err = st.Update(b, func(tx server.Tx) error {
			return tx.Put([]synckosh.Item{{Collection: "notes", Datatype: "note", GUID: "n1", Timestamp: 1, Sequence: 1}})
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	items := func(bucket string) int {
		n := 0
		if err := st.Since(bucket, "", 0, func(synckosh.Item) bool { n++; return true }); err != nil {
			t.Fatal(err)
		}
		return n
	}

	if err := store.ResetSyncBucket(ctx, bob.ID, reset.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob resetting ada's bucket: %v", err)
	}
	if items(reset.ID) != 1 {
		t.Fatal("bob's reset removed items")
	}
	if err := store.ResetSyncBucket(ctx, ada.ID, reset.ID); err != nil {
		t.Fatalf("ResetSyncBucket: %v", err)
	}
	if items(reset.ID) != 0 {
		t.Fatal("the reset bucket still has items")
	}
	if id, _ := st.StoreID(reset.ID); id == "" || id == ids[reset.ID] {
		t.Fatalf("store id after reset: %q, was %q", id, ids[reset.ID])
	}
	if items(other.ID) != 1 {
		t.Fatal("resetting one bucket touched another's items")
	}
	if id, _ := st.StoreID(other.ID); id != ids[other.ID] {
		t.Fatal("resetting one bucket changed another's store id")
	}
	if b, err := store.SyncBucketByToken(ctx, reset.Token); err != nil || b.ID != reset.ID {
		t.Fatalf("the token after reset: %+v %v", b, err)
	}
}
