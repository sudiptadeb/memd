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
		Issuer:            "https://idp.example.com",
		Subject:           "idp|abc",
		Email:             "ada@example.com",
		Name:              "Ada Lovelace",
		PreferredUsername: "ada",
	})
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if first.Issuer != "https://idp.example.com" || first.Subject != "idp|abc" || first.Email != "ada@example.com" || first.DisplayName != "Ada Lovelace" {
		t.Fatalf("unexpected provisioned user: %+v", first)
	}
	if first.SuperAdmin {
		t.Fatalf("new OIDC user should not be admin without claim/allowlist")
	}

	// Second login with the same subject must reuse the same record.
	second, err := store.UpsertOIDCUser(ctx, OIDCIdentity{Issuer: "https://idp.example.com", Subject: "idp|abc", Email: "ada@new.example.com"})
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

func TestUpsertOIDCUserDoesNotLinkExistingLocalAccount(t *testing.T) {
	ctx := context.Background()
	store := openOIDCTestStore(t)

	admin, err := store.CreateSuperAdmin(ctx, "boss", "correct horse battery staple")
	if err != nil {
		t.Fatalf("CreateSuperAdmin: %v", err)
	}

	cloud, err := store.UpsertOIDCUser(ctx, OIDCIdentity{
		Issuer:            "https://idp.example.com",
		Subject:           "idp|boss-sub",
		Email:             "boss@example.com",
		PreferredUsername: "boss",
	})
	if err != nil {
		t.Fatalf("cloud upsert: %v", err)
	}
	if cloud.ID == admin.ID {
		t.Fatalf("OIDC login linked local account %s", admin.ID)
	}
	if cloud.Username != "boss-2" {
		t.Fatalf("expected unique cloud username boss-2, got %q", cloud.Username)
	}
	if cloud.SuperAdmin {
		t.Fatalf("OIDC account unexpectedly became super admin")
	}

	// Subsequent logins resolve by issuer+subject.
	again, err := store.UpsertOIDCUser(ctx, OIDCIdentity{Issuer: "https://idp.example.com", Subject: "idp|boss-sub", PreferredUsername: "boss"})
	if err != nil {
		t.Fatalf("re-login: %v", err)
	}
	if again.ID != cloud.ID {
		t.Fatalf("issuer+subject re-login resolved to a different user")
	}
}

func TestUpsertOIDCUserKeysByIssuerAndSubject(t *testing.T) {
	ctx := context.Background()
	store := openOIDCTestStore(t)

	first, err := store.UpsertOIDCUser(ctx, OIDCIdentity{Issuer: "https://idp-a.example.com", Subject: "same-sub", PreferredUsername: "sam"})
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	second, err := store.UpsertOIDCUser(ctx, OIDCIdentity{Issuer: "https://idp-b.example.com", Subject: "same-sub", PreferredUsername: "sam"})
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if second.ID == first.ID {
		t.Fatalf("same subject from a different issuer reused user %s", first.ID)
	}
	if first.SuperAdmin || second.SuperAdmin {
		t.Fatalf("OIDC provisioning should not grant super admin: first=%v second=%v", first.SuperAdmin, second.SuperAdmin)
	}
}
