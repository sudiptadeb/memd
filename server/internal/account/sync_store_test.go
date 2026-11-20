package account

import (
	"context"
	"strings"
	"testing"

	"github.com/sudiptadeb/synckosh"
	"github.com/sudiptadeb/synckosh/server"
	"github.com/sudiptadeb/synckosh/server/storetest"
)

// SyncStore keeps the contract the synckosh server relies on.
func TestSyncStoreContract(t *testing.T) {
	storetest.Run(t, func(t *testing.T) server.Store {
		store := openTestStore(t)
		if err := store.Init(context.Background()); err != nil {
			t.Fatalf("Init: %v", err)
		}
		return store.SyncStore()
	})
}

// A schema v12 database, whose sync_items have no identity, gets the column
// and its index on Init, and keeps its items.
func TestInitGivesSyncItemsAnIdentity(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{
		`DROP INDEX idx_sync_items_identity`,
		`ALTER TABLE sync_items DROP COLUMN identity`,
		`INSERT INTO sync_items(bucket, collection, guid, seq, timestamp, item) VALUES ('b', 'notes', 'old', 1, 1, '{}')`,
	} {
		if _, err := store.db.ExecContext(ctx, s); err != nil {
			t.Fatalf("make a v12 table: %v", err)
		}
	}
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Init on v12: %v", err)
	}
	st := store.SyncStore()
	id := strings.Repeat("a", 64)
	err := st.Update("b", func(tx server.Tx) error {
		if err := tx.Put([]synckosh.Item{{Collection: "notes", Datatype: "note", GUID: "new", Timestamp: 2, Sequence: 2, Identity: id}}); err != nil {
			return err
		}
		it, has, err := tx.Identity("notes", id)
		if err != nil || !has || it.GUID != "new" {
			t.Fatalf("Identity after the upgrade: %+v %v %v", it, has, err)
		}
		_, has, err = tx.Get("notes", "old")
		if err != nil || !has {
			t.Fatalf("the v12 item: %v %v", has, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
