package account

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sudiptadeb/memd/server/internal/config"
)

func TestStoreInitAndSuperAdminLogin(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)

	initialized, err := store.IsInitialized(ctx)
	if err != nil {
		t.Fatalf("IsInitialized: %v", err)
	}
	if initialized {
		t.Fatalf("new database should not be initialized")
	}
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	admin, err := store.CreateSuperAdmin(ctx, "Admin", "correct horse battery staple")
	if err != nil {
		t.Fatalf("CreateSuperAdmin: %v", err)
	}
	if !admin.SuperAdmin {
		t.Fatalf("created admin should be marked super admin")
	}

	got, err := store.AuthenticateLocal(ctx, "admin", "correct horse battery staple")
	if err != nil {
		t.Fatalf("AuthenticateLocal: %v", err)
	}
	if got.ID != admin.ID || !got.SuperAdmin || got.LastLoginAt == nil {
		t.Fatalf("authenticated user mismatch: %+v", got)
	}
	if _, err := store.AuthenticateLocal(ctx, "admin", "wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password err = %v, want ErrInvalidCredentials", err)
	}
}

func TestCreateTeamAddsOwnerMembership(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	owner, err := store.CreateLocalUser(ctx, CreateUserInput{Username: "sudi", Password: "correct horse battery staple"})
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	team, err := store.CreateTeam(ctx, CreateTeamInput{Name: "Family Memory", OwnerUserID: owner.ID})
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if team.Slug != "family-memory" {
		t.Fatalf("slug = %q, want family-memory", team.Slug)
	}
	teams, err := store.ListTeams(ctx)
	if err != nil {
		t.Fatalf("ListTeams: %v", err)
	}
	if len(teams) != 1 || teams[0].ID != team.ID {
		t.Fatalf("teams = %+v, want created team", teams)
	}
	members, err := store.ListTeamMembers(ctx, team.ID)
	if err != nil {
		t.Fatalf("ListTeamMembers: %v", err)
	}
	if len(members) != 1 || members[0].UserID != owner.ID || members[0].Role != RoleOwner {
		t.Fatalf("members = %+v, want initial owner", members)
	}
}

func TestCreateTeamRejectsSuperAdminOwner(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	admin, err := store.CreateSuperAdmin(ctx, "admin", "correct horse battery staple")
	if err != nil {
		t.Fatalf("CreateSuperAdmin: %v", err)
	}
	if _, err := store.CreateTeam(ctx, CreateTeamInput{Name: "Ops", OwnerUserID: admin.ID}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("CreateTeam err = %v, want ErrForbidden", err)
	}
}

func TestTeamInviteAcceptUsesLimitAndDoesNotDoubleConsume(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	owner, err := store.CreateLocalUser(ctx, CreateUserInput{Username: "owner", Password: "owner-pass"})
	if err != nil {
		t.Fatalf("CreateLocalUser owner: %v", err)
	}
	member, err := store.CreateLocalUser(ctx, CreateUserInput{Username: "member", Password: "member-pass"})
	if err != nil {
		t.Fatalf("CreateLocalUser member: %v", err)
	}
	team, err := store.CreateTeam(ctx, CreateTeamInput{Name: "Family", OwnerUserID: owner.ID})
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	maxUses := 1
	created, err := store.CreateTeamInvite(ctx, CreateTeamInviteInput{
		TeamID:          team.ID,
		CreatedByUserID: owner.ID,
		Role:            RoleMember,
		MaxUses:         &maxUses,
	})
	if err != nil {
		t.Fatalf("CreateTeamInvite: %v", err)
	}
	if _, err := store.AcceptTeamInvite(ctx, created.Token, member.ID); err != nil {
		t.Fatalf("AcceptTeamInvite: %v", err)
	}
	invite, err := store.TeamInviteByToken(ctx, created.Token)
	if err != nil {
		t.Fatalf("TeamInviteByToken: %v", err)
	}
	if invite.UseCount != 1 {
		t.Fatalf("UseCount = %d, want 1", invite.UseCount)
	}
	if _, err := store.AcceptTeamInvite(ctx, created.Token, member.ID); err != nil {
		t.Fatalf("re-accept should not consume another use: %v", err)
	}
	invite, err = store.TeamInviteByToken(ctx, created.Token)
	if err != nil {
		t.Fatalf("TeamInviteByToken after re-accept: %v", err)
	}
	if invite.UseCount != 1 {
		t.Fatalf("UseCount after re-accept = %d, want 1", invite.UseCount)
	}
	other, err := store.CreateLocalUser(ctx, CreateUserInput{Username: "other", Password: "other-pass"})
	if err != nil {
		t.Fatalf("CreateLocalUser other: %v", err)
	}
	if _, err := store.AcceptTeamInvite(ctx, created.Token, other.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("maxed invite err = %v, want ErrForbidden", err)
	}
}

func TestExpiredAndRevokedInvitesCannotBeAccepted(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	owner, err := store.CreateLocalUser(ctx, CreateUserInput{Username: "owner", Password: "owner-pass"})
	if err != nil {
		t.Fatalf("CreateLocalUser owner: %v", err)
	}
	member, err := store.CreateLocalUser(ctx, CreateUserInput{Username: "member", Password: "member-pass"})
	if err != nil {
		t.Fatalf("CreateLocalUser member: %v", err)
	}
	team, err := store.CreateTeam(ctx, CreateTeamInput{Name: "Family", OwnerUserID: owner.ID})
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	past := time.Now().Add(-time.Hour)
	expired, err := store.CreateTeamInvite(ctx, CreateTeamInviteInput{
		TeamID:          team.ID,
		CreatedByUserID: owner.ID,
		Role:            RoleMember,
		ExpiresAt:       &past,
	})
	if err != nil {
		t.Fatalf("CreateTeamInvite expired: %v", err)
	}
	if _, err := store.AcceptTeamInvite(ctx, expired.Token, member.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expired invite err = %v, want ErrForbidden", err)
	}
	active, err := store.CreateTeamInvite(ctx, CreateTeamInviteInput{
		TeamID:          team.ID,
		CreatedByUserID: owner.ID,
		Role:            RoleMember,
	})
	if err != nil {
		t.Fatalf("CreateTeamInvite active: %v", err)
	}
	if err := store.RevokeTeamInvite(ctx, active.Invite.ID, owner.ID); err != nil {
		t.Fatalf("RevokeTeamInvite: %v", err)
	}
	if _, err := store.AcceptTeamInvite(ctx, active.Token, member.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked invite err = %v, want ErrForbidden", err)
	}
}

func TestOnlyOwnersDemoteAdminsAndDeleteTeams(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	owner, err := store.CreateLocalUser(ctx, CreateUserInput{Username: "owner", Password: "owner-pass"})
	if err != nil {
		t.Fatalf("CreateLocalUser owner: %v", err)
	}
	admin, err := store.CreateLocalUser(ctx, CreateUserInput{Username: "admin", Password: "admin-pass"})
	if err != nil {
		t.Fatalf("CreateLocalUser admin: %v", err)
	}
	member, err := store.CreateLocalUser(ctx, CreateUserInput{Username: "member", Password: "member-pass"})
	if err != nil {
		t.Fatalf("CreateLocalUser member: %v", err)
	}
	team, err := store.CreateTeam(ctx, CreateTeamInput{Name: "Family", OwnerUserID: owner.ID})
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if err := store.AddTeamMember(ctx, team.ID, admin.ID, RoleAdmin, owner.ID); err != nil {
		t.Fatalf("AddTeamMember admin: %v", err)
	}
	if err := store.AddTeamMember(ctx, team.ID, member.ID, RoleMember, admin.ID); err != nil {
		t.Fatalf("AddTeamMember member: %v", err)
	}
	if err := store.SetTeamMemberRole(ctx, team.ID, admin.ID, RoleMember, admin.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("admin demote err = %v, want ErrForbidden", err)
	}
	if err := store.DeleteTeam(ctx, team.ID, admin.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("admin delete err = %v, want ErrForbidden", err)
	}
	if err := store.SetTeamMemberRole(ctx, team.ID, admin.ID, RoleMember, owner.ID); err != nil {
		t.Fatalf("owner demote admin: %v", err)
	}
	if err := store.DeleteTeam(ctx, team.ID, owner.ID); err != nil {
		t.Fatalf("owner delete team: %v", err)
	}
}

func TestUninitializedStoreRejectsUserCreate(t *testing.T) {
	store := openTestStore(t)
	_, err := store.CreateLocalUser(context.Background(), CreateUserInput{Username: "a", Password: "b"})
	if !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("CreateLocalUser err = %v, want ErrNotInitialized", err)
	}
}

func TestUserDataExportImportsIntoAnotherUser(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	alice, err := store.CreateLocalUser(ctx, CreateUserInput{Username: "alice", Password: "alice-pass"})
	if err != nil {
		t.Fatalf("CreateLocalUser alice: %v", err)
	}
	bob, err := store.CreateLocalUser(ctx, CreateUserInput{Username: "bob", Password: "bob-pass"})
	if err != nil {
		t.Fatalf("CreateLocalUser bob: %v", err)
	}
	team, err := store.CreateTeam(ctx, CreateTeamInput{Name: "Alice Team", OwnerUserID: alice.ID})
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	dir := config.Directory{
		ID:          "dir1",
		OwnerUserID: alice.ID,
		TeamID:      team.ID,
		Name:        "Family Notes",
		Description: "shared notes",
		Backend:     "local",
		LocalPath:   t.TempDir(),
	}
	if err := store.UpsertUserDirectory(ctx, alice.ID, dir); err != nil {
		t.Fatalf("UpsertUserDirectory: %v", err)
	}
	conn := config.Connector{
		ID:           "conn1",
		OwnerUserID:  alice.ID,
		TeamID:       team.ID,
		Name:         "Claude",
		Kind:         config.ConnectorKindMCP,
		Token:        "tok_123",
		Write:        true,
		DirectoryIDs: []string{dir.ID},
	}
	if err := store.UpsertUserConnector(ctx, alice.ID, conn); err != nil {
		t.Fatalf("UpsertUserConnector: %v", err)
	}

	bundle, err := store.ExportUserData(ctx, alice.ID)
	if err != nil {
		t.Fatalf("ExportUserData: %v", err)
	}
	if got := bundle.Directories[0].OwnerUserID; got != "" {
		t.Fatalf("exported directory owner = %q, want empty", got)
	}
	if got := bundle.Directories[0].TeamID; got != "" {
		t.Fatalf("exported directory team = %q, want empty", got)
	}
	if got := bundle.Connectors[0].OwnerUserID; got != "" {
		t.Fatalf("exported connector owner = %q, want empty", got)
	}
	if got := bundle.Connectors[0].TeamID; got != "" {
		t.Fatalf("exported connector team = %q, want empty", got)
	}

	if err := store.ImportUserData(ctx, bob.ID, bundle, false); err != nil {
		t.Fatalf("ImportUserData: %v", err)
	}
	dirs, err := store.ListUserDirectories(ctx, bob.ID)
	if err != nil {
		t.Fatalf("ListUserDirectories bob: %v", err)
	}
	if len(dirs) != 1 || dirs[0].ID != dir.ID || dirs[0].OwnerUserID != bob.ID {
		t.Fatalf("bob directories = %+v", dirs)
	}
	if dirs[0].TeamID != "" {
		t.Fatalf("bob imported directory team = %q, want empty", dirs[0].TeamID)
	}
	connectors, err := store.ListUserConnectors(ctx, bob.ID)
	if err != nil {
		t.Fatalf("ListUserConnectors bob: %v", err)
	}
	if len(connectors) != 1 || connectors[0].ID != conn.ID || connectors[0].OwnerUserID != bob.ID || connectors[0].DirectoryIDs[0] != dir.ID {
		t.Fatalf("bob connectors = %+v", connectors)
	}
	if connectors[0].TeamID != "" {
		t.Fatalf("bob imported connector team = %q, want empty", connectors[0].TeamID)
	}
}

func TestUserDataRejectsSuperAdminOwner(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	admin, err := store.CreateSuperAdmin(ctx, "admin", "correct horse battery staple")
	if err != nil {
		t.Fatalf("CreateSuperAdmin: %v", err)
	}
	if _, err := store.ExportUserData(ctx, admin.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("ExportUserData err = %v, want ErrForbidden", err)
	}
	bundle := NewUserDataBundle(
		[]config.Directory{{ID: "dir1", Name: "Notes", Backend: "local", LocalPath: t.TempDir()}},
		[]config.Connector{{ID: "conn1", Name: "Agent", Kind: config.ConnectorKindMCP, Token: "tok_123", DirectoryIDs: []string{"dir1"}}},
	)
	if err := store.ImportUserData(ctx, admin.ID, bundle, false); !errors.Is(err, ErrForbidden) {
		t.Fatalf("ImportUserData err = %v, want ErrForbidden", err)
	}
	if err := store.UpsertUserDirectory(ctx, admin.ID, bundle.Directories[0]); !errors.Is(err, ErrForbidden) {
		t.Fatalf("UpsertUserDirectory err = %v, want ErrForbidden", err)
	}
	if err := store.UpsertUserConnector(ctx, admin.ID, bundle.Connectors[0]); !errors.Is(err, ErrForbidden) {
		t.Fatalf("UpsertUserConnector err = %v, want ErrForbidden", err)
	}
}

func TestParseDatabaseURL(t *testing.T) {
	cfg, err := ParseDatabaseURL("sqlite:///tmp/memd-test.db?_pragma=journal_mode(DELETE)")
	if err != nil {
		t.Fatalf("ParseDatabaseURL: %v", err)
	}
	if cfg.Driver != "sqlite" || cfg.SQLitePath != "/tmp/memd-test.db" {
		t.Fatalf("cfg = %+v", cfg)
	}
	if !strings.Contains(cfg.DSN, "journal_mode(DELETE)") {
		t.Fatalf("custom query was not preserved: %s", cfg.DSN)
	}
	if strings.Contains(cfg.DSN, "journal_mode(WAL)") {
		t.Fatalf("custom journal_mode should suppress default WAL: %s", cfg.DSN)
	}
	if !strings.Contains(cfg.DSN, "foreign_keys(1)") {
		t.Fatalf("sqlite defaults were not added: %s", cfg.DSN)
	}

	cfg, err = ParseDatabaseURL("postgres://example/db")
	if err != nil {
		t.Fatalf("ParseDatabaseURL postgres: %v", err)
	}
	if cfg.Driver != "postgres" {
		t.Fatalf("driver = %q, want postgres", cfg.Driver)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "memd.db")
	store, err := Open(context.Background(), DBConfig{
		Driver:     "sqlite",
		DSN:        sqliteDSNForPath(path),
		Source:     "test",
		SQLitePath: path,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
