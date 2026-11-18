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

	if _, _, err := store.CreateSyncBucket(ctx, ada.ID, "  "); err == nil {
		t.Fatal("a bucket with no name was made")
	}
	b, tok, err := store.CreateSyncBucket(ctx, ada.ID, " Browser ")
	if err != nil {
		t.Fatalf("CreateSyncBucket: %v", err)
	}
	if b.Name != "Browser" || len(tok) != 32 {
		t.Fatalf("bucket %+v, token length %d", b, len(tok))
	}
	var stored string
	if err := store.db.QueryRowContext(ctx, `SELECT token_hash FROM sync_buckets WHERE id = ?`, b.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == tok || stored != syncTokenHash(tok) {
		t.Fatalf("stored %q: want the token's hash, not the token", stored)
	}

	got, err := store.SyncBucketByToken(ctx, tok)
	if err != nil || got.ID != b.ID || got.UserID != ada.ID {
		t.Fatalf("SyncBucketByToken: %+v %v", got, err)
	}
	if _, err := store.SyncBucketByToken(ctx, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown token: %v", err)
	}

	list, err := store.ListSyncBuckets(ctx, ada.ID)
	if err != nil || len(list) != 1 || list[0].ID != b.ID {
		t.Fatalf("ListSyncBuckets: %+v %v", list, err)
	}
	if list, _ := store.ListSyncBuckets(ctx, bob.ID); len(list) != 0 {
		t.Fatalf("bob sees %+v", list)
	}

	// Only the owner can replace the token or delete the bucket.
	if _, _, err := store.RotateSyncBucketToken(ctx, bob.ID, b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob rotating ada's bucket: %v", err)
	}
	if err := store.DeleteSyncBucket(ctx, bob.ID, b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob deleting ada's bucket: %v", err)
	}

	_, tok2, err := store.RotateSyncBucketToken(ctx, ada.ID, b.ID)
	if err != nil {
		t.Fatalf("RotateSyncBucketToken: %v", err)
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
	keep, _, _ := store.CreateSyncBucket(ctx, ada.ID, "keep")
	gone, _, _ := store.CreateSyncBucket(ctx, ada.ID, "gone")
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
