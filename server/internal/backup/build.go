package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sudiptadeb/memd/server/internal/account"
	"github.com/sudiptadeb/memd/server/internal/version"
)

const (
	// FormatName / FormatVersion identify the archive layout in manifest.json.
	FormatName    = "memd-backup"
	FormatVersion = 1

	// Names of the entries inside the tar payload.
	entryManifest = "manifest.json"
	entryDB       = "memd.db"
	entryEnv      = "env"

	// FileExt is the extension of archive files in the backup repository.
	FileExt = ".memdbk"
	// nameTimeLayout is the timestamp embedded in archive file names.
	nameTimeLayout = "20060102T150405Z"

	// maxEntrySize bounds a single tar entry on restore so a crafted archive
	// cannot expand into unbounded memory.
	maxEntrySize = 4 << 30
)

// envKeys are the process environment variables captured into the archive's
// env entry when set, so a restore can rebuild the service environment.
var envKeys = []string{"MEMD_SESSION_SECRET", "MEMD_SESSION_MAX_AGE"}

// ErrUnsupported is returned when the account database is not a file-backed
// SQLite database (in-memory), which cannot be snapshotted.
var ErrUnsupported = errors.New("backups require a file-backed sqlite database")

// Manifest describes an archive.
type Manifest struct {
	Format      string    `json:"format"`
	Version     int       `json:"version"`
	CreatedAt   time.Time `json:"created_at"`
	MemdVersion string    `json:"memd_version"`
	Hostname    string    `json:"hostname"`
}

// Archive is a sealed backup ready to be written or streamed.
type Archive struct {
	Name     string
	Data     []byte
	Manifest Manifest
}

// Contents is what Inspect reports about an archive without restoring it.
type Contents struct {
	Manifest Manifest
	DBSize   int64
	HasEnv   bool
}

// ArchiveName returns the repository file name for an archive created at t.
func ArchiveName(t time.Time) string {
	return "memd-" + t.UTC().Format(nameTimeLayout) + FileExt
}

// ParseArchiveName extracts the creation time from a name produced by
// ArchiveName. The bool is false for any other file name.
func ParseArchiveName(name string) (time.Time, bool) {
	if !strings.HasPrefix(name, "memd-") || !strings.HasSuffix(name, FileExt) {
		return time.Time{}, false
	}
	stamp := strings.TrimSuffix(strings.TrimPrefix(name, "memd-"), FileExt)
	t, err := time.Parse(nameTimeLayout, stamp)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// Build snapshots the store's database, verifies the snapshot, packs it with a
// manifest and the captured environment, and seals the result under
// passphrase.
func Build(ctx context.Context, store *account.Store, passphrase string) (Archive, error) {
	if store == nil || !Supported(store) {
		return Archive{}, ErrUnsupported
	}
	if passphrase == "" {
		return Archive{}, errors.New("passphrase required")
	}
	tmp, err := os.MkdirTemp("", "memd-backup-*")
	if err != nil {
		return Archive{}, err
	}
	defer os.RemoveAll(tmp)

	snap := filepath.Join(tmp, entryDB)
	if err := store.VacuumInto(ctx, snap); err != nil {
		return Archive{}, err
	}
	if err := integrityCheck(ctx, snap); err != nil {
		return Archive{}, fmt.Errorf("snapshot verification: %w", err)
	}
	db, err := os.ReadFile(snap)
	if err != nil {
		return Archive{}, err
	}

	created := time.Now().UTC().Truncate(time.Second)
	host, _ := os.Hostname()
	manifest := Manifest{
		Format:      FormatName,
		Version:     FormatVersion,
		CreatedAt:   created,
		MemdVersion: version.Value,
		Hostname:    host,
	}
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Archive{}, err
	}

	var payload bytes.Buffer
	gz := gzip.NewWriter(&payload)
	tw := tar.NewWriter(gz)
	entries := []struct {
		name string
		data []byte
	}{
		{entryManifest, manifestJSON},
		{entryDB, db},
	}
	if env := captureEnv(); len(env) > 0 {
		entries = append(entries, struct {
			name string
			data []byte
		}{entryEnv, env})
	}
	for _, e := range entries {
		hdr := &tar.Header{
			Name:    e.name,
			Mode:    0o600,
			Size:    int64(len(e.data)),
			ModTime: created,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return Archive{}, err
		}
		if _, err := tw.Write(e.data); err != nil {
			return Archive{}, err
		}
	}
	if err := tw.Close(); err != nil {
		return Archive{}, err
	}
	if err := gz.Close(); err != nil {
		return Archive{}, err
	}

	sealed, err := Encrypt(passphrase, payload.Bytes())
	if err != nil {
		return Archive{}, err
	}
	return Archive{Name: ArchiveName(created), Data: sealed, Manifest: manifest}, nil
}

// Supported reports whether the store's database can be snapshotted.
func Supported(store *account.Store) bool {
	if store == nil {
		return false
	}
	path := store.Config().SQLitePath
	return path != "" && path != ":memory:"
}

// Inspect decrypts an archive and reports its manifest and contents.
func Inspect(blob []byte, passphrase string) (Contents, error) {
	entries, err := unpack(blob, passphrase)
	if err != nil {
		return Contents{}, err
	}
	manifest, err := manifestFrom(entries)
	if err != nil {
		return Contents{}, err
	}
	db, ok := entries[entryDB]
	if !ok {
		return Contents{}, errors.New("archive has no memd.db entry")
	}
	_, hasEnv := entries[entryEnv]
	return Contents{Manifest: manifest, DBSize: int64(len(db)), HasEnv: hasEnv}, nil
}

// Restore decrypts blob and writes its database to destDB, refusing to
// overwrite an existing file unless force is set. When the archive carries an
// env entry it is written next to the database as env.restored. The restored
// database is verified with PRAGMA integrity_check before returning.
func Restore(blob []byte, passphrase, destDB string, force bool) error {
	if strings.TrimSpace(destDB) == "" {
		return errors.New("destination path required")
	}
	entries, err := unpack(blob, passphrase)
	if err != nil {
		return err
	}
	if _, err := manifestFrom(entries); err != nil {
		return err
	}
	db, ok := entries[entryDB]
	if !ok {
		return errors.New("archive has no memd.db entry")
	}
	if _, err := os.Stat(destDB); err == nil {
		if !force {
			return fmt.Errorf("%s already exists; pass --force to overwrite", destDB)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destDB), 0o700); err != nil {
		return err
	}
	// A stale WAL/SHM pair belonging to the previous database would be
	// replayed into the restored file on first open; remove them.
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if err := os.Remove(destDB + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.WriteFile(destDB, db, 0o600); err != nil {
		return err
	}
	if err := integrityCheck(context.Background(), destDB); err != nil {
		return fmt.Errorf("restored database verification: %w", err)
	}
	if env, ok := entries[entryEnv]; ok {
		envPath := filepath.Join(filepath.Dir(destDB), "env.restored")
		if err := os.WriteFile(envPath, env, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func unpack(blob []byte, passphrase string) (map[string][]byte, error) {
	if passphrase == "" {
		return nil, errors.New("passphrase required")
	}
	plain, err := Decrypt(passphrase, blob)
	if err != nil {
		return nil, err
	}
	gz, err := gzip.NewReader(bytes.NewReader(plain))
	if err != nil {
		return nil, fmt.Errorf("%w: payload is not gzip", ErrWrongPassphrase)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	entries := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if hdr.Size < 0 || hdr.Size > maxEntrySize {
			return nil, fmt.Errorf("archive entry %s has unreasonable size %d", hdr.Name, hdr.Size)
		}
		data, err := io.ReadAll(io.LimitReader(tr, hdr.Size))
		if err != nil {
			return nil, fmt.Errorf("read archive entry %s: %w", hdr.Name, err)
		}
		entries[filepath.Base(hdr.Name)] = data
	}
	return entries, nil
}

func manifestFrom(entries map[string][]byte) (Manifest, error) {
	raw, ok := entries[entryManifest]
	if !ok {
		return Manifest{}, errors.New("archive has no manifest.json entry")
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return Manifest{}, fmt.Errorf("manifest: %w", err)
	}
	if m.Format != FormatName {
		return Manifest{}, fmt.Errorf("manifest format %q is not %q", m.Format, FormatName)
	}
	if m.Version != FormatVersion {
		return Manifest{}, fmt.Errorf("manifest version %d is not supported (want %d)", m.Version, FormatVersion)
	}
	return m, nil
}

func captureEnv() []byte {
	var b strings.Builder
	for _, key := range envKeys {
		if v, ok := os.LookupEnv(key); ok && v != "" {
			b.WriteString(key)
			b.WriteString("=")
			b.WriteString(v)
			b.WriteString("\n")
		}
	}
	return []byte(b.String())
}

// integrityCheck opens path read-only on a fresh connection and requires
// PRAGMA integrity_check to answer "ok".
func integrityCheck(ctx context.Context, path string) error {
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var result string
	if err := db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("integrity_check reported %q", result)
	}
	return nil
}
