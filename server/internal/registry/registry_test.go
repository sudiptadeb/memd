package registry

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/sudiptadeb/memd/server/internal/account"
	"github.com/sudiptadeb/memd/server/internal/config"
)

func TestRotateConnector_ReplacesToken(t *testing.T) {
	r := NewEphemeral()
	c, err := r.AddConnector(config.Connector{Name: "claude", DirectoryIDs: []string{"x"}, Write: true})
	if err != nil {
		t.Fatalf("AddConnector: %v", err)
	}
	oldToken := c.Token

	rot, err := r.RotateConnector(c.ID)
	if err != nil {
		t.Fatalf("RotateConnector: %v", err)
	}
	if rot.Token == "" || rot.Token == oldToken {
		t.Fatalf("token did not change: old=%q new=%q", oldToken, rot.Token)
	}
	if rot.Name != c.Name || rot.Write != c.Write || len(rot.DirectoryIDs) != 1 || rot.DirectoryIDs[0] != "x" {
		t.Fatalf("non-token fields mutated: %+v", rot)
	}
}

func TestRotateConnector_OldTokenStopsResolving(t *testing.T) {
	r := NewEphemeral()
	c, _ := r.AddConnector(config.Connector{Name: "claude", DirectoryIDs: []string{"x"}})
	oldToken := c.Token

	rot, err := r.RotateConnector(c.ID)
	if err != nil {
		t.Fatalf("RotateConnector: %v", err)
	}
	if got := r.ConnectorByToken(oldToken); got != nil {
		t.Fatalf("old token should not resolve, got %+v", got)
	}
	if got := r.ConnectorByToken(rot.Token); got == nil || got.ID != c.ID {
		t.Fatalf("new token should resolve to the same connector, got %+v", got)
	}
}

func TestRotateConnector_UnknownID(t *testing.T) {
	r := NewEphemeral()
	if _, err := r.RotateConnector("does-not-exist"); err == nil {
		t.Fatalf("rotating an unknown id should fail")
	}
}

func TestUpdateConnector_ChangesNameDirsWrite_PreservesTokenAndID(t *testing.T) {
	r := NewEphemeral()
	r.cfg.Directories = []config.Directory{
		{ID: "d1", Name: "one"},
		{ID: "d2", Name: "two"},
	}
	c, _ := r.AddConnector(config.Connector{Name: "claude", DirectoryIDs: []string{"d1"}, Write: false})
	oldToken := c.Token
	oldID := c.ID

	updated, err := r.UpdateConnector(c.ID, "claude-code", config.ConnectorKindHTTP, []string{"d1", "d2"}, true)
	if err != nil {
		t.Fatalf("UpdateConnector: %v", err)
	}
	if updated.Name != "claude-code" {
		t.Fatalf("Name = %q, want claude-code", updated.Name)
	}
	if len(updated.DirectoryIDs) != 2 || updated.DirectoryIDs[0] != "d1" || updated.DirectoryIDs[1] != "d2" {
		t.Fatalf("DirectoryIDs = %v, want [d1 d2]", updated.DirectoryIDs)
	}
	if !updated.Write {
		t.Fatalf("Write should be true")
	}
	if updated.EffectiveKind() != config.ConnectorKindHTTP {
		t.Fatalf("Kind = %q, want http", updated.EffectiveKind())
	}
	if updated.Token != oldToken {
		t.Fatalf("Token mutated: old=%q new=%q", oldToken, updated.Token)
	}
	if updated.ID != oldID {
		t.Fatalf("ID mutated: old=%q new=%q", oldID, updated.ID)
	}
}

func TestUpdateConnector_RejectsBadInput(t *testing.T) {
	r := NewEphemeral()
	r.cfg.Directories = []config.Directory{{ID: "d1", Name: "one"}}
	c, _ := r.AddConnector(config.Connector{Name: "claude", DirectoryIDs: []string{"d1"}})

	cases := []struct {
		name string
		id   string
		nm   string
		kind string
		dirs []string
	}{
		{"unknown id", "does-not-exist", "x", "mcp", []string{"d1"}},
		{"empty name", c.ID, "", "mcp", []string{"d1"}},
		{"empty dirs", c.ID, "x", "mcp", nil},
		{"unknown directory id", c.ID, "x", "mcp", []string{"d-nope"}},
		{"unknown kind", c.ID, "x", "smtp", []string{"d1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := r.UpdateConnector(tc.id, tc.nm, tc.kind, tc.dirs, true); err == nil {
				t.Fatalf("UpdateConnector(%q, %q, %q, %v) should fail", tc.id, tc.nm, tc.kind, tc.dirs)
			}
		})
	}
}

func TestAddConnector_DefaultsKindToMCP(t *testing.T) {
	r := NewEphemeral()
	c, err := r.AddConnector(config.Connector{Name: "legacy", DirectoryIDs: []string{"x"}})
	if err != nil {
		t.Fatalf("AddConnector: %v", err)
	}
	if c.Kind != config.ConnectorKindMCP || c.EffectiveKind() != config.ConnectorKindMCP {
		t.Fatalf("kind = %q effective=%q, want mcp", c.Kind, c.EffectiveKind())
	}
}

func TestNewAccountBackedKeepsBrokenDirectoryVisible(t *testing.T) {
	ctx := context.Background()
	store := openRegistryTestStore(t)
	user, err := store.CreateLocalUser(ctx, account.CreateUserInput{Username: "friend", Password: "friend-pass"})
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	missingPath := filepath.Join(t.TempDir(), "missing")
	if err := store.UpsertUserDirectory(ctx, user.ID, config.Directory{
		ID:        "dir1",
		Name:      "Missing",
		Backend:   "local",
		LocalPath: missingPath,
	}); err != nil {
		t.Fatalf("UpsertUserDirectory: %v", err)
	}
	if err := store.UpsertUserConnector(ctx, user.ID, config.Connector{
		ID:           "conn1",
		Name:         "Agent",
		Kind:         config.ConnectorKindMCP,
		Token:        "tok_123",
		DirectoryIDs: []string{"dir1"},
	}); err != nil {
		t.Fatalf("UpsertUserConnector: %v", err)
	}

	r, err := NewAccountBacked(ctx, store)
	if err != nil {
		t.Fatalf("NewAccountBacked: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	dirs := r.DirectoriesForUser(user.ID)
	if len(dirs) != 1 || dirs[0].LocalPath != missingPath {
		t.Fatalf("directories = %+v", dirs)
	}
	conn := r.ConnectorByToken("tok_123")
	if conn == nil {
		t.Fatalf("connector was not loaded")
	}
	if got := r.DirectoriesForConnector(conn); len(got) != 0 {
		t.Fatalf("broken directory should not be served, got %+v", got)
	}
}

func TestImportUserDataKeepsBrokenDirectoryVisible(t *testing.T) {
	ctx := context.Background()
	store := openRegistryTestStore(t)
	user, err := store.CreateLocalUser(ctx, account.CreateUserInput{Username: "friend", Password: "friend-pass"})
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	r, err := NewAccountBacked(ctx, store)
	if err != nil {
		t.Fatalf("NewAccountBacked: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	missingPath := filepath.Join(t.TempDir(), "missing")
	bundle := account.NewUserDataBundle(
		[]config.Directory{{
			ID:        "dir1",
			Name:      "Missing",
			Backend:   "local",
			LocalPath: missingPath,
		}},
		[]config.Connector{{
			ID:           "conn1",
			Name:         "Agent",
			Kind:         config.ConnectorKindMCP,
			Token:        "tok_123",
			DirectoryIDs: []string{"dir1"},
		}},
	)
	if err := r.ImportUserData(user.ID, bundle, false); err != nil {
		t.Fatalf("ImportUserData: %v", err)
	}
	dirs := r.DirectoriesForUser(user.ID)
	if len(dirs) != 1 || dirs[0].LocalPath != missingPath {
		t.Fatalf("directories = %+v", dirs)
	}
	conn := r.ConnectorByToken("tok_123")
	if conn == nil {
		t.Fatalf("connector was not imported")
	}
	if got := r.DirectoriesForConnector(conn); len(got) != 0 {
		t.Fatalf("broken directory should not be served, got %+v", got)
	}
}

func openRegistryTestStore(t *testing.T) *account.Store {
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
	if err := store.Init(context.Background()); err != nil {
		t.Fatalf("account.Init: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
