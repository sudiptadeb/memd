package account

import (
	"context"
	"path/filepath"
	"testing"
)

func openOIDCTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "memd.db")
	store, err := Open(context.Background(), DBConfig{Driver: "sqlite", DSN: "file:" + path, Source: "test", SQLitePath: path})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := store.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestUpsertOIDCUserProvisionsAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	store := openOIDCTestStore(t)

	first, err := store.UpsertOIDCUser(ctx, OIDCIdentity{
		Subject:           "idp|abc",
		Email:             "ada@example.com",
		Name:              "Ada Lovelace",
		PreferredUsername: "ada",
	})
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if first.Subject != "idp|abc" || first.Email != "ada@example.com" || first.DisplayName != "Ada Lovelace" {
		t.Fatalf("unexpected provisioned user: %+v", first)
	}
	if first.SuperAdmin {
		t.Fatalf("new OIDC user should not be admin without claim/allowlist")
	}

	// Second login with the same subject must reuse the same record.
	second, err := store.UpsertOIDCUser(ctx, OIDCIdentity{Subject: "idp|abc", Email: "ada@new.example.com"})
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("subject re-login created a new user: %s != %s", second.ID, first.ID)
	}
	if second.Email != "ada@new.example.com" {
		t.Fatalf("email not refreshed: %q", second.Email)
	}

	users, err := store.ListUsers(ctx)
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 1 {
		t.Fatalf("expected 1 user, got %d", len(users))
	}
}

func TestUpsertOIDCUserLinksExistingLocalAccountAndKeepsAdmin(t *testing.T) {
	ctx := context.Background()
	store := openOIDCTestStore(t)

	admin, err := store.CreateSuperAdmin(ctx, "boss", "correct horse battery staple")
	if err != nil {
		t.Fatalf("CreateSuperAdmin: %v", err)
	}

	// First OIDC login whose preferred_username matches the local username.
	linked, err := store.UpsertOIDCUser(ctx, OIDCIdentity{
		Subject:           "idp|boss-sub",
		Email:             "boss@example.com",
		PreferredUsername: "boss",
	})
	if err != nil {
		t.Fatalf("link upsert: %v", err)
	}
	if linked.ID != admin.ID {
		t.Fatalf("expected to link existing account %s, got %s", admin.ID, linked.ID)
	}
	if linked.Subject != "idp|boss-sub" {
		t.Fatalf("subject not attached: %q", linked.Subject)
	}
	if !linked.SuperAdmin {
		t.Fatalf("linked account lost its super-admin rights")
	}

	// Subsequent logins now resolve purely by subject.
	again, err := store.UpsertOIDCUser(ctx, OIDCIdentity{Subject: "idp|boss-sub", PreferredUsername: "boss"})
	if err != nil {
		t.Fatalf("re-login: %v", err)
	}
	if again.ID != admin.ID {
		t.Fatalf("subject re-login resolved to a different user")
	}
}

func TestUpsertOIDCUserGrantsAdminFromClaim(t *testing.T) {
	ctx := context.Background()
	store := openOIDCTestStore(t)

	user, err := store.UpsertOIDCUser(ctx, OIDCIdentity{Subject: "idp|adm", Email: "ops@example.com", Admin: true})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if !user.SuperAdmin {
		t.Fatalf("admin claim did not grant super admin")
	}
}
