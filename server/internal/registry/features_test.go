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

func TestSetDirectoryFeature(t *testing.T) {
	reg := NewEphemeral()
	t.Cleanup(func() { _ = reg.Close() })
	dirID, err := reg.AddDirectory(config.Directory{Name: "personal", Backend: "local", LocalPath: t.TempDir()})
	if err != nil {
		t.Fatalf("AddDirectory: %v", err)
	}

	// Enable tasks: directory records it and the preference file is scaffolded.
	d, err := reg.SetDirectoryFeatureForActor("", dirID, "tasks", true)
	if err != nil {
		t.Fatalf("enable tasks: %v", err)
	}
	if !featureEnabled(d.Features, "tasks") {
		t.Fatalf("tasks not enabled: %+v", d.Features)
	}
	dv := reg.DirectoryViewForUser("", dirID)
	if dv == nil || dv.Backend == nil {
		t.Fatal("directory backend unavailable")
	}
	prefs, err := dv.Backend.Read("tasks/_feature.md")
	if err != nil {
		t.Fatalf("scaffolded _feature.md missing: %v", err)
	}
	if !strings.Contains(string(prefs), "your preferences") {
		t.Errorf("scaffold should be a preferences template, got: %q", prefs)
	}

	// Disable tasks: flag flips but the folder/file stays (disable != delete).
	d, err = reg.SetDirectoryFeatureForActor("", dirID, "tasks", false)
	if err != nil {
		t.Fatalf("disable tasks: %v", err)
	}
	if featureEnabled(d.Features, "tasks") {
		t.Error("tasks should be disabled")
	}
	if _, err := dv.Backend.Read("tasks/_feature.md"); err != nil {
		t.Errorf("disabling must not delete the folder/file: %v", err)
	}

	// Unknown feature and coming-soon feature are rejected on enable.
	if _, err := reg.SetDirectoryFeatureForActor("", dirID, "nope", true); err == nil {
		t.Error("unknown feature should be rejected")
	}
	if _, err := reg.SetDirectoryFeatureForActor("", dirID, "calendar", true); err == nil {
		t.Error("coming-soon feature should be rejected on enable")
	}
}

// TestSetDirectoryFeatureGitPropagates verifies that enabling a feature on a
// git directory scaffolds the folder on the directory branch (main) and that a
// cached per-connector branch backend pulls it in.
func TestSetDirectoryFeatureGitPropagates(t *testing.T) {
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
	// Open (and cache) the member's branch backend before the feature exists.
	if views := r.DirectoriesForConnector(&conn); len(views) != 1 {
		t.Fatalf("connector views = %d, want 1", len(views))
	}

	// Owner enables tasks: scaffolds on main and propagates to the branch.
	if _, err := r.SetDirectoryFeatureForActor(owner.ID, dirID, "tasks", true); err != nil {
		t.Fatalf("enable tasks: %v", err)
	}

	// main has the scaffolded feature folder.
	verifyMain := filepath.Join(tmp, "verify-main")
	gitRun(t, "", "clone", "--branch", "main", remote, verifyMain)
	if _, err := os.Stat(filepath.Join(verifyMain, "tasks", "_feature.md")); err != nil {
		t.Fatalf("tasks/_feature.md missing from main: %v", err)
	}

	// The member's connector branch pulled it in.
	branch := "memd/alice-" + conn.ID
	verifyBranch := filepath.Join(tmp, "verify-branch")
	gitRun(t, "", "clone", "--branch", branch, remote, verifyBranch)
	if _, err := os.Stat(filepath.Join(verifyBranch, "tasks", "_feature.md")); err != nil {
		t.Fatalf("tasks/_feature.md did not propagate to connector branch %s: %v", branch, err)
	}
}

func featureEnabled(features []config.DirectoryFeature, key string) bool {
	for _, f := range features {
		if f.Key == key {
			return f.Enabled
		}
	}
	return false
}
