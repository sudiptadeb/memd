package account

import (
	"context"
	"testing"

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
