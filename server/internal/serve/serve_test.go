package serve

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/sudiptadeb/memd/server/internal/account"
)

func TestEnsureAccountDBInitWithSuperAdminOptions(t *testing.T) {
	ctx := context.Background()
	store := openTestAccountStore(t)
	stdin := openDevNull(t)
	var stdout bytes.Buffer

	err := ensureAccountDB(ctx, store, Options{
		InitDB:                   true,
		CreateSuperAdminUsername: "root",
		CreateSuperAdminPassword: "correct horse battery staple",
		Stdin:                    stdin,
		Stdout:                   &stdout,
	})
	if err != nil {
		t.Fatalf("ensureAccountDB: %v", err)
	}
	admin, err := store.AuthenticateLocal(ctx, "root", "correct horse battery staple")
	if err != nil {
		t.Fatalf("AuthenticateLocal: %v", err)
	}
	if !admin.SuperAdmin {
		t.Fatalf("created user should be super admin: %+v", admin)
	}
}

func TestEnsureAccountDBNonInteractiveMissingInitFails(t *testing.T) {
	store := openTestAccountStore(t)
	err := ensureAccountDB(context.Background(), store, Options{
		Stdin:  openDevNull(t),
		Stdout: &bytes.Buffer{},
	})
	if err == nil || errors.Is(err, account.ErrNotInitialized) {
		t.Fatalf("ensureAccountDB err = %v, want setup guidance error", err)
	}
}

func openTestAccountStore(t *testing.T) *account.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "memd.db")
	store, err := account.Open(context.Background(), account.DBConfig{
		Driver:     "sqlite",
		DSN:        "file:" + path,
		Source:     "test",
		SQLitePath: path,
	})
	if err != nil {
		t.Fatalf("account.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func openDevNull(t *testing.T) *os.File {
	t.Helper()
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open dev null: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}
