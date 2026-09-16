package backup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sudiptadeb/memd/server/internal/account"
)

func openTestStore(t *testing.T, path string) *account.Store {
	t.Helper()
	cfg, err := account.ParseDatabaseURL(path)
	if err != nil {
		t.Fatalf("ParseDatabaseURL: %v", err)
	}
	store, err := account.Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := store.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// TestBuildRestoreRoundTripKeepsData: an archive built from a live store
// restores to a database that still holds the accounts created before the
// backup, and the env entry lands next to it.
func TestBuildRestoreRoundTripKeepsData(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store := openTestStore(t, filepath.Join(root, "live", "memd.db"))
	admin, err := store.CreateSuperAdmin(ctx, "admin", "correct horse battery staple")
	if err != nil {
		t.Fatalf("CreateSuperAdmin: %v", err)
	}
	t.Setenv("MEMD_SESSION_SECRET", "test-session-secret")
	t.Setenv("MEMD_SESSION_MAX_AGE", "12h")

	archive, err := Build(ctx, store, "a passphrase long enough")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, ok := ParseArchiveName(archive.Name); !ok {
		t.Fatalf("archive name %q does not parse", archive.Name)
	}
	if archive.Manifest.Format != FormatName || archive.Manifest.Version != FormatVersion {
		t.Fatalf("manifest = %+v", archive.Manifest)
	}

	contents, err := Inspect(archive.Data, "a passphrase long enough")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if contents.DBSize == 0 || !contents.HasEnv {
		t.Fatalf("contents = %+v, want db bytes and env", contents)
	}

	dest := filepath.Join(root, "restored", "memd.db")
	if err := Restore(archive.Data, "a passphrase long enough", dest, false); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	env, err := os.ReadFile(filepath.Join(root, "restored", "env.restored"))
	if err != nil {
		t.Fatalf("env.restored missing: %v", err)
	}
	if !strings.Contains(string(env), "MEMD_SESSION_SECRET=test-session-secret\n") || !strings.Contains(string(env), "MEMD_SESSION_MAX_AGE=12h\n") {
		t.Fatalf("env.restored = %q", env)
	}

	restored := openTestStore(t, dest)
	got, err := restored.UserByID(ctx, admin.ID)
	if err != nil {
		t.Fatalf("UserByID on restored db: %v", err)
	}
	if got.Username != "admin" || !got.SuperAdmin {
		t.Fatalf("restored user = %+v", got)
	}
}

func TestRestoreRefusesToOverwriteWithoutForce(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store := openTestStore(t, filepath.Join(root, "live", "memd.db"))
	archive, err := Build(ctx, store, "a passphrase long enough")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	dest := filepath.Join(root, "out", "memd.db")
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Restore(archive.Data, "a passphrase long enough", dest, false); err == nil {
		t.Fatal("Restore over an existing file without --force succeeded")
	}
	if err := Restore(archive.Data, "a passphrase long enough", dest, true); err != nil {
		t.Fatalf("Restore --force: %v", err)
	}
	if err := Restore(archive.Data, "not the passphrase", dest, true); err == nil {
		t.Fatal("Restore with the wrong passphrase succeeded")
	}
}

func TestBuildRejectsInMemoryStore(t *testing.T) {
	cfg, err := account.ParseDatabaseURL("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	store, err := account.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if Supported(store) {
		t.Fatal("in-memory store reported as supported")
	}
	if _, err := Build(context.Background(), store, "a passphrase long enough"); err != ErrUnsupported {
		t.Fatalf("Build on in-memory store: err=%v, want ErrUnsupported", err)
	}
}

func TestArchiveNameRoundTrip(t *testing.T) {
	at := time.Date(2026, 9, 16, 3, 0, 7, 0, time.UTC)
	name := ArchiveName(at)
	if name != "memd-20260916T030007Z.memdbk" {
		t.Fatalf("ArchiveName = %q", name)
	}
	got, ok := ParseArchiveName(name)
	if !ok || !got.Equal(at) {
		t.Fatalf("ParseArchiveName(%q) = %v, %v", name, got, ok)
	}
	if _, ok := ParseArchiveName("LATEST"); ok {
		t.Fatal("LATEST parsed as an archive name")
	}
}
