package account

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

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
	admin, err := store.CreateSuperAdmin(ctx, "sudi", "correct horse battery staple")
	if err != nil {
		t.Fatalf("CreateSuperAdmin: %v", err)
	}
	team, err := store.CreateTeam(ctx, CreateTeamInput{Name: "Family Memory", OwnerUserID: admin.ID})
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
	dir := config.Directory{
		ID:          "dir1",
		OwnerUserID: alice.ID,
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
	if got := bundle.Connectors[0].OwnerUserID; got != "" {
		t.Fatalf("exported connector owner = %q, want empty", got)
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
	connectors, err := store.ListUserConnectors(ctx, bob.ID)
	if err != nil {
		t.Fatalf("ListUserConnectors bob: %v", err)
	}
	if len(connectors) != 1 || connectors[0].ID != conn.ID || connectors[0].OwnerUserID != bob.ID || connectors[0].DirectoryIDs[0] != dir.ID {
		t.Fatalf("bob connectors = %+v", connectors)
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
