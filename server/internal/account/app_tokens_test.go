package account

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAppTokenCreateLookupAndFormat(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	user, err := store.CreateLocalUser(ctx, CreateUserInput{Username: "ada", Password: "correct horse battery staple"})
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}

	tok, secret, err := store.CreateAppToken(ctx, user.ID, "  Pixel 9  ")
	if err != nil {
		t.Fatalf("CreateAppToken: %v", err)
	}
	if !strings.HasPrefix(secret, "mat_") || len(secret) != len("mat_")+43 {
		t.Fatalf("secret format = %q (len %d), want mat_ + 43 chars", secret, len(secret))
	}
	if tok.Label != "Pixel 9" {
		t.Fatalf("label = %q, want trimmed %q", tok.Label, "Pixel 9")
	}

	got, err := store.AppTokenByToken(ctx, secret)
	if err != nil {
		t.Fatalf("AppTokenByToken: %v", err)
	}
	if got.ID != tok.ID || got.UserID != user.ID || got.RevokedAt != nil {
		t.Fatalf("lookup mismatch: %+v", got)
	}
	if _, err := store.AppTokenByToken(ctx, "mat_definitely-not-a-token"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown token err = %v, want ErrNotFound", err)
	}

	// The raw secret must not be recoverable from the database.
	var stored string
	if err := store.db.QueryRowContext(ctx, `SELECT token_hash FROM app_tokens WHERE id = ?`, tok.ID).Scan(&stored); err != nil {
		t.Fatalf("read token_hash: %v", err)
	}
	if stored == secret || strings.Contains(stored, secret) {
		t.Fatalf("raw secret stored in the database")
	}
	if len(stored) != 64 { // sha256 hex
		t.Fatalf("token_hash = %q, want sha256 hex", stored)
	}
}

func TestAppTokenLabelTruncatedAndDisabledUserRejected(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	user, err := store.CreateLocalUser(ctx, CreateUserInput{Username: "ada", Password: "correct horse battery staple"})
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}

	long := strings.Repeat("x", 200)
	tok, _, err := store.CreateAppToken(ctx, user.ID, long)
	if err != nil {
		t.Fatalf("CreateAppToken: %v", err)
	}
	if len(tok.Label) != 64 {
		t.Fatalf("label length = %d, want 64", len(tok.Label))
	}

	if err := store.SetUserDisabled(ctx, user.ID, true); err != nil {
		t.Fatalf("SetUserDisabled: %v", err)
	}
	if _, _, err := store.CreateAppToken(ctx, user.ID, "phone"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("disabled user err = %v, want ErrForbidden", err)
	}
	if _, _, err := store.CreateAppToken(ctx, "usr_missing", "phone"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown user err = %v, want ErrNotFound", err)
	}
}

func TestAppTokenListAndRevoke(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	ada, err := store.CreateLocalUser(ctx, CreateUserInput{Username: "ada", Password: "correct horse battery staple"})
	if err != nil {
		t.Fatalf("CreateLocalUser ada: %v", err)
	}
	bob, err := store.CreateLocalUser(ctx, CreateUserInput{Username: "bob", Password: "correct horse battery staple"})
	if err != nil {
		t.Fatalf("CreateLocalUser bob: %v", err)
	}

	first, firstSecret, err := store.CreateAppToken(ctx, ada.ID, "first")
	if err != nil {
		t.Fatalf("CreateAppToken first: %v", err)
	}
	second, _, err := store.CreateAppToken(ctx, ada.ID, "second")
	if err != nil {
		t.Fatalf("CreateAppToken second: %v", err)
	}
	if _, _, err := store.CreateAppToken(ctx, bob.ID, "bobs"); err != nil {
		t.Fatalf("CreateAppToken bobs: %v", err)
	}

	toks, err := store.ListAppTokens(ctx, ada.ID)
	if err != nil {
		t.Fatalf("ListAppTokens: %v", err)
	}
	if len(toks) != 2 {
		t.Fatalf("len(tokens) = %d, want 2 (only ada's)", len(toks))
	}

	// Another user cannot revoke ada's token by id.
	if err := store.RevokeAppToken(ctx, bob.ID, first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-user revoke err = %v, want ErrNotFound", err)
	}
	if err := store.RevokeAppToken(ctx, ada.ID, first.ID); err != nil {
		t.Fatalf("RevokeAppToken: %v", err)
	}
	// Revoked tokens disappear from the list and stop resolving.
	toks, err = store.ListAppTokens(ctx, ada.ID)
	if err != nil {
		t.Fatalf("ListAppTokens after revoke: %v", err)
	}
	if len(toks) != 1 || toks[0].ID != second.ID {
		t.Fatalf("tokens after revoke = %+v, want only second", toks)
	}
	if _, err := store.AppTokenByToken(ctx, firstSecret); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked token lookup err = %v, want ErrNotFound", err)
	}
	// Double revoke is ErrNotFound, not a silent success.
	if err := store.RevokeAppToken(ctx, ada.ID, first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double revoke err = %v, want ErrNotFound", err)
	}
}

func TestAppTokenRevokeByToken(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	user, err := store.CreateLocalUser(ctx, CreateUserInput{Username: "ada", Password: "correct horse battery staple"})
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	_, secret, err := store.CreateAppToken(ctx, user.ID, "phone")
	if err != nil {
		t.Fatalf("CreateAppToken: %v", err)
	}
	if err := store.RevokeAppTokenByToken(ctx, secret); err != nil {
		t.Fatalf("RevokeAppTokenByToken: %v", err)
	}
	if _, err := store.AppTokenByToken(ctx, secret); !errors.Is(err, ErrNotFound) {
		t.Fatalf("lookup after self-revoke err = %v, want ErrNotFound", err)
	}
	if err := store.RevokeAppTokenByToken(ctx, secret); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second self-revoke err = %v, want ErrNotFound", err)
	}
	if err := store.RevokeAppTokenByToken(ctx, "mat_unknown"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown self-revoke err = %v, want ErrNotFound", err)
	}
}

func TestAppTokenTouchIsThrottled(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	user, err := store.CreateLocalUser(ctx, CreateUserInput{Username: "ada", Password: "correct horse battery staple"})
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	tok, secret, err := store.CreateAppToken(ctx, user.ID, "phone")
	if err != nil {
		t.Fatalf("CreateAppToken: %v", err)
	}

	// First touch writes.
	if err := store.TouchAppToken(ctx, tok.ID); err != nil {
		t.Fatalf("TouchAppToken: %v", err)
	}
	got, err := store.AppTokenByToken(ctx, secret)
	if err != nil || got.LastUsedAt == nil {
		t.Fatalf("last_used_at not set: %+v err=%v", got, err)
	}
	first := *got.LastUsedAt

	// A touch within the interval is a no-op.
	if err := store.TouchAppToken(ctx, tok.ID); err != nil {
		t.Fatalf("TouchAppToken (throttled): %v", err)
	}
	got, err = store.AppTokenByToken(ctx, secret)
	if err != nil || got.LastUsedAt == nil || !got.LastUsedAt.Equal(first) {
		t.Fatalf("throttled touch changed last_used_at: %+v err=%v", got, err)
	}

	// Backdate past the interval: the next touch writes again.
	old := time.Now().UTC().Add(-2 * appTokenTouchInterval).Format(time.RFC3339Nano)
	if _, err := store.db.ExecContext(ctx, `UPDATE app_tokens SET last_used_at = ? WHERE id = ?`, old, tok.ID); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	if err := store.TouchAppToken(ctx, tok.ID); err != nil {
		t.Fatalf("TouchAppToken (stale): %v", err)
	}
	got, err = store.AppTokenByToken(ctx, secret)
	if err != nil || got.LastUsedAt == nil || !got.LastUsedAt.After(first.Add(-time.Second)) {
		t.Fatalf("stale touch did not update last_used_at: %+v err=%v", got, err)
	}

	if err := store.TouchAppToken(ctx, "apt_missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("touch unknown err = %v, want ErrNotFound", err)
	}
}

// TestInitUpgradesV10AddsAppTokens simulates a schema v10 database (no
// app_tokens table) and verifies Init creates it in place so pairing works
// after the deploy.
func TestInitUpgradesV10AddsAppTokens(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "memd.db")
	dsn := sqliteDSNForPath(path)

	raw, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	stmts := []string{
		`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`,
		`CREATE TABLE users (
			id TEXT PRIMARY KEY,
			username TEXT NOT NULL,
			username_norm TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL DEFAULT '',
			display_name TEXT NOT NULL DEFAULT '',
			disabled INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			password_changed_at TEXT NOT NULL,
			last_login_at TEXT,
			email TEXT NOT NULL DEFAULT '',
			issuer TEXT NOT NULL DEFAULT '',
			subject TEXT,
			provider_id TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE app_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE super_admins (user_id TEXT PRIMARY KEY, created_at TEXT NOT NULL, created_by_user_id TEXT)`,
		`INSERT INTO schema_migrations(version, applied_at) VALUES (10, '2026-01-01T00:00:00Z')`,
		`INSERT INTO users(id, username, username_norm, created_at, updated_at, password_changed_at)
		   VALUES ('usr_ada', 'ada', 'ada', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
	}
	for _, s := range stmts {
		if _, err := raw.ExecContext(ctx, s); err != nil {
			t.Fatalf("seed v10 schema: %v", err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close seed db: %v", err)
	}

	store, err := Open(ctx, DBConfig{Driver: "sqlite", DSN: dsn, Source: "test", SQLitePath: path})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Init (upgrade): %v", err)
	}

	tok, secret, err := store.CreateAppToken(ctx, "usr_ada", "phone")
	if err != nil {
		t.Fatalf("CreateAppToken after upgrade: %v", err)
	}
	got, err := store.AppTokenByToken(ctx, secret)
	if err != nil || got.ID != tok.ID {
		t.Fatalf("AppTokenByToken after upgrade: %+v err=%v", got, err)
	}
}
