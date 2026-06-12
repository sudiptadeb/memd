package registry

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sudiptadeb/memd/server/internal/account"
	"github.com/sudiptadeb/memd/server/internal/config"
)

func TestAddDirectoryManagedSandbox(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HOME", tmp)
	reg := NewEphemeral()

	// OIDC-style user (custom path not allowed), name only: memd sandboxes it.
	id, err := reg.AddDirectoryForUserManaged("usrManaged", config.Directory{Name: "notes", Backend: "local"}, false)
	if err != nil {
		t.Fatalf("managed name-only directory: %v", err)
	}
	var got config.Directory
	for _, d := range reg.Directories() {
		if d.ID == id {
			got = d
		}
	}
	if got.LocalPath == "" {
		t.Fatal("managed directory has no LocalPath")
	}
	if !strings.Contains(got.LocalPath, filepath.Join("usrManaged", id)) {
		t.Errorf("managed path %q is not under the per-user/dir namespace", got.LocalPath)
	}
	if info, err := os.Stat(got.LocalPath); err != nil || !info.IsDir() {
		t.Errorf("managed path was not created as a directory: %v", err)
	}

	// Same user supplying a path: rejected.
	if _, err := reg.AddDirectoryForUserManaged("usrManaged", config.Directory{Name: "x", Backend: "local", LocalPath: t.TempDir()}, false); err == nil {
		t.Error("custom local path should be rejected when not allowed")
	}

	// Local user supplying a path: honoured.
	chosen := t.TempDir()
	id3, err := reg.AddDirectoryForUserManaged("usrLocal", config.Directory{Name: "y", Backend: "local", LocalPath: chosen}, true)
	if err != nil {
		t.Fatalf("local user custom path: %v", err)
	}
	for _, d := range reg.Directories() {
		if d.ID == id3 && d.LocalPath != chosen {
			t.Errorf("custom path = %q, want %q", d.LocalPath, chosen)
		}
	}
}

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

func TestTeamScopedDirectoriesVisibleToMembers(t *testing.T) {
	ctx := context.Background()
	store := openRegistryTestStore(t)
	owner, err := store.CreateLocalUser(ctx, account.CreateUserInput{Username: "owner", Password: "owner-pass"})
	if err != nil {
		t.Fatalf("CreateLocalUser owner: %v", err)
	}
	member, err := store.CreateLocalUser(ctx, account.CreateUserInput{Username: "member", Password: "member-pass"})
	if err != nil {
		t.Fatalf("CreateLocalUser member: %v", err)
	}
	team, err := store.CreateTeam(ctx, account.CreateTeamInput{Name: "Family", OwnerUserID: owner.ID})
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if err := store.AddTeamMember(ctx, team.ID, member.ID, account.RoleMember, owner.ID); err != nil {
		t.Fatalf("AddTeamMember: %v", err)
	}
	dir := config.Directory{
		ID:        "dir1",
		TeamID:    team.ID,
		Name:      "Shared",
		Backend:   "local",
		LocalPath: t.TempDir(),
	}
	if err := store.UpsertUserDirectory(ctx, owner.ID, dir); err != nil {
		t.Fatalf("UpsertUserDirectory: %v", err)
	}
	conn := config.Connector{
		ID:           "conn1",
		TeamID:       team.ID,
		Name:         "Agent",
		Kind:         config.ConnectorKindMCP,
		Token:        "tok_123",
		DirectoryIDs: []string{dir.ID},
	}
	if err := store.UpsertUserConnector(ctx, owner.ID, conn); err != nil {
		t.Fatalf("UpsertUserConnector: %v", err)
	}
	r, err := NewAccountBacked(ctx, store)
	if err != nil {
		t.Fatalf("NewAccountBacked: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	memberDirs := r.DirectoriesForUser(member.ID)
	if len(memberDirs) != 1 || memberDirs[0].ID != dir.ID || memberDirs[0].OwnerUserID != owner.ID {
		t.Fatalf("member directories = %+v, want shared owner dir", memberDirs)
	}
	memberConnectors := r.ConnectorsForUser(member.ID)
	if len(memberConnectors) != 1 || memberConnectors[0].ID != conn.ID || memberConnectors[0].OwnerUserID != owner.ID {
		t.Fatalf("member connectors = %+v, want shared owner connector", memberConnectors)
	}
	views := r.DirectoriesForConnector(&memberConnectors[0])
	if len(views) != 1 || views[0].Directory.ID != dir.ID {
		t.Fatalf("connector directories = %+v, want shared dir", views)
	}
}

func TestMemberConnectorOnTeamDirectory(t *testing.T) {
	ctx := context.Background()
	store := openRegistryTestStore(t)
	owner, err := store.CreateLocalUser(ctx, account.CreateUserInput{Username: "owner", Password: "owner-pass"})
	if err != nil {
		t.Fatalf("CreateLocalUser owner: %v", err)
	}
	member, err := store.CreateLocalUser(ctx, account.CreateUserInput{Username: "member", Password: "member-pass"})
	if err != nil {
		t.Fatalf("CreateLocalUser member: %v", err)
	}
	viewer, err := store.CreateLocalUser(ctx, account.CreateUserInput{Username: "viewer", Password: "viewer-pass"})
	if err != nil {
		t.Fatalf("CreateLocalUser viewer: %v", err)
	}
	stranger, err := store.CreateLocalUser(ctx, account.CreateUserInput{Username: "stranger", Password: "stranger-pass"})
	if err != nil {
		t.Fatalf("CreateLocalUser stranger: %v", err)
	}
	team, err := store.CreateTeam(ctx, account.CreateTeamInput{Name: "Family", OwnerUserID: owner.ID})
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if err := store.AddTeamMember(ctx, team.ID, member.ID, account.RoleMember, owner.ID); err != nil {
		t.Fatalf("AddTeamMember member: %v", err)
	}
	if err := store.AddTeamMember(ctx, team.ID, viewer.ID, account.RoleViewer, owner.ID); err != nil {
		t.Fatalf("AddTeamMember viewer: %v", err)
	}
	dir := config.Directory{
		ID:        "dir1",
		TeamID:    team.ID,
		Name:      "Shared",
		Backend:   "local",
		LocalPath: t.TempDir(),
	}
	if err := store.UpsertUserDirectory(ctx, owner.ID, dir); err != nil {
		t.Fatalf("UpsertUserDirectory: %v", err)
	}
	r, err := NewAccountBacked(ctx, store)
	if err != nil {
		t.Fatalf("NewAccountBacked: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	// A member can create their own (personal) connector against the teammate's
	// shared directory, and it serves with write access.
	memberConn, err := r.AddConnectorForUser(member.ID, config.Connector{
		Name:         "member-agent",
		Kind:         config.ConnectorKindMCP,
		DirectoryIDs: []string{dir.ID},
		Write:        true,
	})
	if err != nil {
		t.Fatalf("member AddConnectorForUser: %v", err)
	}
	if memberConn.OwnerUserID != member.ID || memberConn.TeamID != "" {
		t.Fatalf("member connector = %+v, want owned by member, no team scope", memberConn)
	}
	views := r.DirectoriesForConnector(&memberConn)
	if len(views) != 1 || views[0].Directory.ID != dir.ID {
		t.Fatalf("member connector dirs = %+v, want shared dir", views)
	}
	if !views[0].CanWrite {
		t.Fatalf("member should have write access to shared dir")
	}

	// A viewer can attach the shared directory read-only, but not with write.
	viewerConn, err := r.AddConnectorForUser(viewer.ID, config.Connector{
		Name:         "viewer-agent",
		Kind:         config.ConnectorKindMCP,
		DirectoryIDs: []string{dir.ID},
		Write:        false,
	})
	if err != nil {
		t.Fatalf("viewer read-only AddConnectorForUser: %v", err)
	}
	vviews := r.DirectoriesForConnector(&viewerConn)
	if len(vviews) != 1 || vviews[0].CanWrite {
		t.Fatalf("viewer connector dirs = %+v, want read-only access", vviews)
	}
	if _, err := r.AddConnectorForUser(viewer.ID, config.Connector{
		Name:         "viewer-writer",
		Kind:         config.ConnectorKindMCP,
		DirectoryIDs: []string{dir.ID},
		Write:        true,
	}); err == nil {
		t.Fatal("viewer should not be able to create a write connector on a shared dir")
	}

	// A non-member cannot reference the shared directory at all.
	if _, err := r.AddConnectorForUser(stranger.ID, config.Connector{
		Name:         "stranger-agent",
		Kind:         config.ConnectorKindMCP,
		DirectoryIDs: []string{dir.ID},
		Write:        false,
	}); err == nil {
		t.Fatal("non-member should not be able to reference a team directory")
	}
}

func TestMemberConnectorWritesToOwnBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git executable not available")
	}
	ctx := context.Background()
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HOME", tmp)

	store := openRegistryTestStore(t)
	owner, err := store.CreateLocalUser(ctx, account.CreateUserInput{Username: "owner", Password: "owner-pass"})
	if err != nil {
		t.Fatalf("CreateLocalUser owner: %v", err)
	}
	member, err := store.CreateLocalUser(ctx, account.CreateUserInput{Username: "alice", Password: "alice-pass"})
	if err != nil {
		t.Fatalf("CreateLocalUser member: %v", err)
	}
	team, err := store.CreateTeam(ctx, account.CreateTeamInput{Name: "Family", OwnerUserID: owner.ID})
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if err := store.AddTeamMember(ctx, team.ID, member.ID, account.RoleMember, owner.ID); err != nil {
		t.Fatalf("AddTeamMember: %v", err)
	}

	remote := filepath.Join(tmp, "remote.git")
	gitRun(t, "", "init", "--bare", remote)
	seed := filepath.Join(tmp, "seed")
	gitRun(t, "", "clone", remote, seed)
	gitRun(t, seed, "checkout", "-B", "main")
	if err := os.WriteFile(filepath.Join(seed, "MEMORY.md"), []byte("# Memory\n"), 0o644); err != nil {
		t.Fatalf("seed write: %v", err)
	}
	gitRun(t, seed, "add", "-A")
	gitRun(t, seed, "-c", "user.name=seed", "-c", "user.email=seed@example.com", "commit", "-m", "seed")
	gitRun(t, seed, "push", "-u", "origin", "main")
	gitRun(t, "", "--git-dir", remote, "symbolic-ref", "HEAD", "refs/heads/main")

	r, err := NewAccountBacked(ctx, store)
	if err != nil {
		t.Fatalf("NewAccountBacked: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	dirID, err := r.AddDirectoryForUser(owner.ID, config.Directory{
		Name:    "Shared",
		TeamID:  team.ID,
		Backend: "git",
		Git:     &config.Git{RemoteURL: remote, Branch: "main"},
	})
	if err != nil {
		t.Fatalf("AddDirectoryForUser: %v", err)
	}
	conn, err := r.AddConnectorForUser(member.ID, config.Connector{
		Name:         "alice-agent",
		Kind:         config.ConnectorKindMCP,
		DirectoryIDs: []string{dirID},
		Write:        true,
	})
	if err != nil {
		t.Fatalf("AddConnectorForUser: %v", err)
	}

	views := r.DirectoriesForConnector(&conn)
	if len(views) != 1 || !views[0].CanWrite {
		t.Fatalf("connector views = %+v, want one writable dir", views)
	}
	if err := views[0].Backend.Write("memory/alice.md", []byte("# Alice\n"), "memd: alice note"); err != nil {
		t.Fatalf("branch write: %v", err)
	}
	if err := views[0].Backend.Flush(); err != nil {
		t.Fatalf("branch flush: %v", err)
	}

	branch := "memd/alice-" + conn.ID
	verify := filepath.Join(tmp, "verify")
	gitRun(t, "", "clone", "--branch", branch, remote, verify)
	if _, err := os.Stat(filepath.Join(verify, "memory", "alice.md")); err != nil {
		t.Fatalf("file missing from connector branch %s: %v", branch, err)
	}
	author := strings.TrimSpace(gitRun(t, verify, "log", "-1", "--format=%an"))
	if author != "alice" {
		t.Fatalf("commit author = %q, want alice", author)
	}

	// main only has the seed content: the member's write didn't touch it.
	verifyMain := filepath.Join(tmp, "verify-main")
	gitRun(t, "", "clone", "--branch", "main", remote, verifyMain)
	if _, err := os.Stat(filepath.Join(verifyMain, "memory", "alice.md")); err == nil {
		t.Fatal("member write leaked onto main")
	}

	// The owner's own view still serves the primary main-branch backend.
	ownerView := r.DirectoryViewForUser(owner.ID, dirID)
	if ownerView == nil || ownerView.Backend == nil {
		t.Fatal("owner view missing")
	}
	if ownerView.Backend == views[0].Backend {
		t.Fatal("owner and member should get distinct backends")
	}
}

func TestOwnerConnectorDesignationControlsMainAccess(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git executable not available")
	}
	ctx := context.Background()
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HOME", tmp)

	store := openRegistryTestStore(t)
	owner, err := store.CreateLocalUser(ctx, account.CreateUserInput{Username: "bob", Password: "bob-pass"})
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	team, err := store.CreateTeam(ctx, account.CreateTeamInput{Name: "Crew", OwnerUserID: owner.ID})
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}

	remote := filepath.Join(tmp, "remote.git")
	gitRun(t, "", "init", "--bare", remote)
	seed := filepath.Join(tmp, "seed")
	gitRun(t, "", "clone", remote, seed)
	gitRun(t, seed, "checkout", "-B", "main")
	if err := os.WriteFile(filepath.Join(seed, "MEMORY.md"), []byte("# Memory\n"), 0o644); err != nil {
		t.Fatalf("seed write: %v", err)
	}
	gitRun(t, seed, "add", "-A")
	gitRun(t, seed, "-c", "user.name=seed", "-c", "user.email=seed@example.com", "commit", "-m", "seed")
	gitRun(t, seed, "push", "-u", "origin", "main")
	gitRun(t, "", "--git-dir", remote, "symbolic-ref", "HEAD", "refs/heads/main")

	r, err := NewAccountBacked(ctx, store)
	if err != nil {
		t.Fatalf("NewAccountBacked: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	dirID, err := r.AddDirectoryForUser(owner.ID, config.Directory{
		Name:    "Shared",
		TeamID:  team.ID,
		Backend: "git",
		Git:     &config.Git{RemoteURL: remote, Branch: "main"},
	})
	if err != nil {
		t.Fatalf("AddDirectoryForUser: %v", err)
	}
	conn, err := r.AddConnectorForUser(owner.ID, config.Connector{
		Name:         "bob-agent",
		Kind:         config.ConnectorKindMCP,
		DirectoryIDs: []string{dirID},
		Write:        true,
	})
	if err != nil {
		t.Fatalf("AddConnectorForUser: %v", err)
	}

	// Without a designation, even the owner's connector works on a branch.
	views := r.DirectoriesForConnector(&conn)
	if len(views) != 1 {
		t.Fatalf("connector views = %+v", views)
	}
	if err := views[0].Backend.Write("memory/branched.md", []byte("# B\n"), "memd: branched"); err != nil {
		t.Fatalf("branch write: %v", err)
	}
	if err := views[0].Backend.Flush(); err != nil {
		t.Fatalf("branch flush: %v", err)
	}
	verifyMain := filepath.Join(tmp, "verify-main-1")
	gitRun(t, "", "clone", "--branch", "main", remote, verifyMain)
	if _, err := os.Stat(filepath.Join(verifyMain, "memory", "branched.md")); err == nil {
		t.Fatal("undesignated owner connector wrote main directly")
	}

	// Designating the connector routes it to the directory branch.
	if _, err := r.UpdateDirectoryOwnerConnectorForActor(owner.ID, dirID, conn.ID); err != nil {
		t.Fatalf("UpdateDirectoryOwnerConnectorForActor: %v", err)
	}
	views = r.DirectoriesForConnector(&conn)
	if len(views) != 1 {
		t.Fatalf("connector views after designation = %+v", views)
	}
	if err := views[0].Backend.Write("memory/mainline.md", []byte("# M\n"), "memd: mainline"); err != nil {
		t.Fatalf("main write: %v", err)
	}
	if err := views[0].Backend.Flush(); err != nil {
		t.Fatalf("main flush: %v", err)
	}
	verifyMain2 := filepath.Join(tmp, "verify-main-2")
	gitRun(t, "", "clone", "--branch", "main", remote, verifyMain2)
	if _, err := os.Stat(filepath.Join(verifyMain2, "memory", "mainline.md")); err != nil {
		t.Fatalf("designated connector write missing from main: %v", err)
	}

	// A stranger's connector can never be designated.
	stranger, err := store.CreateLocalUser(ctx, account.CreateUserInput{Username: "eve", Password: "eve-pass"})
	if err != nil {
		t.Fatalf("CreateLocalUser stranger: %v", err)
	}
	if err := store.AddTeamMember(ctx, team.ID, stranger.ID, account.RoleAdmin, owner.ID); err != nil {
		t.Fatalf("AddTeamMember: %v", err)
	}
	strangerConn, err := r.AddConnectorForUser(stranger.ID, config.Connector{
		Name:         "eve-agent",
		Kind:         config.ConnectorKindMCP,
		DirectoryIDs: []string{dirID},
		Write:        true,
	})
	if err != nil {
		t.Fatalf("stranger AddConnectorForUser: %v", err)
	}
	if _, err := r.UpdateDirectoryOwnerConnectorForActor(owner.ID, dirID, strangerConn.ID); err == nil {
		t.Fatal("designating another user's connector should fail")
	}
	if _, err := r.UpdateDirectoryOwnerConnectorForActor(stranger.ID, dirID, strangerConn.ID); err == nil {
		t.Fatal("a non-owner should not be able to designate on someone else's directory")
	}

	// Deleting the designated connector clears the designation.
	if err := r.DeleteConnectorForActor(owner.ID, conn.ID); err != nil {
		t.Fatalf("DeleteConnectorForActor: %v", err)
	}
	for _, d := range r.DirectoriesForUser(owner.ID) {
		if d.ID == dirID && d.OwnerConnectorID != "" {
			t.Fatalf("designation not cleared after connector delete: %q", d.OwnerConnectorID)
		}
	}
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	cmd := exec.Command("git", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out)
}

func openRegistryTestStore(t *testing.T) *account.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "memd.db")
	// Use the production DSN so pragmas (foreign_keys in particular) match the
	// real deployment — a bare file: DSN once masked an FK violation here.
	cfg, err := account.ParseDatabaseURL("sqlite://" + path)
	if err != nil {
		t.Fatalf("account.ParseDatabaseURL: %v", err)
	}
	store, err := account.Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("account.Open: %v", err)
	}
	if err := store.Init(context.Background()); err != nil {
		t.Fatalf("account.Init: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
