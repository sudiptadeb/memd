package account

import (
	"context"
	"path/filepath"
	"testing"
)

func openOIDCTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "memd.db")
	store, err := Open(context.Background(), DBConfig{Driver: "sqlite", DSN: sqliteDSNForPath(path), Source: "test", SQLitePath: path})
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
		ProviderID:        "idp_test",
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
	second, err := store.UpsertOIDCUser(ctx, OIDCIdentity{ProviderID: "idp_test", Issuer: "https://idp.example.com", Subject: "idp|abc", Email: "ada@new.example.com"})
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
		ProviderID:        "idp_test",
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

	// Subsequent logins resolve by provider+subject.
	again, err := store.UpsertOIDCUser(ctx, OIDCIdentity{ProviderID: "idp_test", Issuer: "https://idp.example.com", Subject: "idp|boss-sub", PreferredUsername: "boss"})
	if err != nil {
		t.Fatalf("re-login: %v", err)
	}
	if again.ID != cloud.ID {
		t.Fatalf("provider+subject re-login resolved to a different user")
	}
}

func TestUpsertOIDCUserKeysByProviderAndSubject(t *testing.T) {
	ctx := context.Background()
	store := openOIDCTestStore(t)

	// Different provider slots never share accounts, even for the same subject.
	first, err := store.UpsertOIDCUser(ctx, OIDCIdentity{ProviderID: "idp_a", Issuer: "https://idp-a.example.com", Subject: "same-sub", PreferredUsername: "sam"})
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	second, err := store.UpsertOIDCUser(ctx, OIDCIdentity{ProviderID: "idp_b", Issuer: "https://idp-b.example.com", Subject: "same-sub", PreferredUsername: "sam"})
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if second.ID == first.ID {
		t.Fatalf("same subject from a different provider reused user %s", first.ID)
	}
	if first.SuperAdmin || second.SuperAdmin {
		t.Fatalf("OIDC provisioning should not grant super admin: first=%v second=%v", first.SuperAdmin, second.SuperAdmin)
	}
}

func TestUpsertOIDCUserSurvivesIssuerURLChange(t *testing.T) {
	ctx := context.Background()
	store := openOIDCTestStore(t)

	// Same provider slot, new issuer URL (custom domain): the account must
	// resolve to the same user and record the new URL.
	before, err := store.UpsertOIDCUser(ctx, OIDCIdentity{ProviderID: "idp_test", Issuer: "https://accounts.example.com", Subject: "sub|sd", PreferredUsername: "sudipta"})
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	after, err := store.UpsertOIDCUser(ctx, OIDCIdentity{ProviderID: "idp_test", Issuer: "https://auth.custom-domain.com", Subject: "sub|sd", PreferredUsername: "sudipta"})
	if err != nil {
		t.Fatalf("post-rename upsert: %v", err)
	}
	if after.ID != before.ID {
		t.Fatalf("issuer URL change split the account: %s != %s", after.ID, before.ID)
	}
	if after.Issuer != "https://auth.custom-domain.com" {
		t.Fatalf("issuer not refreshed after rename: %q", after.Issuer)
	}
}

func TestUnlinkUserOIDCFreesIdentityForAnotherAccount(t *testing.T) {
	ctx := context.Background()
	store := openOIDCTestStore(t)

	dup, err := store.UpsertOIDCUser(ctx, OIDCIdentity{ProviderID: "idp_test", Issuer: "https://idp.example.com", Subject: "sub|dup", PreferredUsername: "dup"})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if err := store.UnlinkUserOIDC(ctx, dup.ID); err != nil {
		t.Fatalf("UnlinkUserOIDC: %v", err)
	}
	unlinked, err := store.UserByID(ctx, dup.ID)
	if err != nil {
		t.Fatalf("UserByID: %v", err)
	}
	if unlinked.ProviderID != "" || unlinked.Issuer != "" || unlinked.Subject != "" {
		t.Fatalf("identity not cleared: %+v", unlinked)
	}
	// The freed identity provisions a fresh account on next login.
	fresh, err := store.UpsertOIDCUser(ctx, OIDCIdentity{ProviderID: "idp_test", Issuer: "https://idp.example.com", Subject: "sub|dup", PreferredUsername: "dup"})
	if err != nil {
		t.Fatalf("re-provision after unlink: %v", err)
	}
	if fresh.ID == dup.ID {
		t.Fatalf("unlinked account was re-linked instead of provisioning fresh")
	}
}

func TestAdoptOIDCUsersIntoProvider(t *testing.T) {
	ctx := context.Background()
	store := openOIDCTestStore(t)

	// An orphan: provisioned under an issuer that is no longer configured,
	// before provider-slot keying existed.
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO users(id, username, username_norm, password_hash, display_name, email, issuer, subject, provider_id, disabled, created_at, updated_at, password_changed_at)
		VALUES ('usr_orphan', 'sudipta', 'sudipta', '', '', 's@example.com', 'https://old.example.com', 'sub|sd', '', 0, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}
	// A second orphan whose subject is already taken under the current provider.
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO users(id, username, username_norm, password_hash, display_name, email, issuer, subject, provider_id, disabled, created_at, updated_at, password_changed_at)
		VALUES ('usr_clash', 'clash', 'clash', '', '', '', 'https://old.example.com', 'sub|taken', '', 0, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seed clash orphan: %v", err)
	}
	if _, err := store.UpsertOIDCUser(ctx, OIDCIdentity{ProviderID: "idp_test", Issuer: "https://new.example.com", Subject: "sub|taken", PreferredUsername: "taken"}); err != nil {
		t.Fatalf("provision holder of taken subject: %v", err)
	}

	adopted, skipped, err := store.AdoptOIDCUsersIntoProvider(ctx, "idp_test", "https://old.example.com")
	if err != nil {
		t.Fatalf("AdoptOIDCUsersIntoProvider: %v", err)
	}
	if adopted != 1 || len(skipped) != 1 || skipped[0] != "clash" {
		t.Fatalf("adopted=%d skipped=%v, want 1 adopted and clash skipped", adopted, skipped)
	}
	relinked, err := store.UserByOIDCIdentity(ctx, "idp_test", "sub|sd")
	if err != nil {
		t.Fatalf("UserByOIDCIdentity after adopt: %v", err)
	}
	if relinked.ID != "usr_orphan" {
		t.Fatalf("adopted identity resolved to %s, want usr_orphan", relinked.ID)
	}
}
