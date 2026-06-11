package storage

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGitFlushPushesToEmptyRemote(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	runGitRaw(t, "", "init", "--bare", remote)

	g, err := NewGit(GitConfig{
		WorkDir:       filepath.Join(root, "work"),
		RemoteURL:     remote,
		Branch:        "main",
		AuthorName:    "memd test",
		AuthorEmail:   "memd@example.com",
		WaitForWrites: time.Hour,
		SaveEvery:     time.Hour,
	})
	if err != nil {
		t.Fatalf("NewGit: %v", err)
	}
	t.Cleanup(func() { _ = g.Close() })

	if err := g.Write("memory/topic.md", []byte("# Topic\n"), "memd: add topic"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := g.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if status := g.Status(); status.LastError != "" {
		t.Fatalf("git backend reported error: %s", status.LastError)
	}

	verify := filepath.Join(root, "verify")
	runGitRaw(t, "", "clone", "--branch", "main", remote, verify)
	if _, err := os.Stat(filepath.Join(verify, "memory", "topic.md")); err != nil {
		t.Fatalf("pushed file missing from remote clone: %v", err)
	}
}

func TestGitFlushRebasesRemoteChangesBeforePush(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	runGitRaw(t, "", "init", "--bare", remote)
	seedRepo(t, remote, filepath.Join(root, "seed"), map[string]string{
		"MEMORY.md": "# Memory\n",
	})

	g, err := NewGit(GitConfig{
		WorkDir:       filepath.Join(root, "work"),
		RemoteURL:     remote,
		Branch:        "main",
		AuthorName:    "memd test",
		AuthorEmail:   "memd@example.com",
		WaitForWrites: time.Hour,
		SaveEvery:     time.Hour,
	})
	if err != nil {
		t.Fatalf("NewGit: %v", err)
	}
	t.Cleanup(func() { _ = g.Close() })

	if err := g.Write("memory/local.md", []byte("# Local\n"), "memd: local change"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	seedRepo(t, remote, filepath.Join(root, "other"), map[string]string{
		"memory/remote.md": "# Remote\n",
	})

	if err := g.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if status := g.Status(); status.LastError != "" {
		t.Fatalf("git backend reported error: %s", status.LastError)
	}

	verify := filepath.Join(root, "verify")
	runGitRaw(t, "", "clone", "--branch", "main", remote, verify)
	for _, path := range []string{"memory/local.md", "memory/remote.md"} {
		if _, err := os.Stat(filepath.Join(verify, path)); err != nil {
			t.Fatalf("%s missing from remote clone: %v", path, err)
		}
	}
}

func TestSplitRemoteAuthRedactsHTTPToken(t *testing.T) {
	clean, username, token := splitRemoteAuth("https://ada:secret-token@example.com/acme/memory.git")
	if clean != "https://example.com/acme/memory.git" {
		t.Fatalf("clean remote = %q", clean)
	}
	if username != "ada" || token != "secret-token" {
		t.Fatalf("auth = %q/%q", username, token)
	}
}

func TestValidateRemoteURLRejectsTransportHelpers(t *testing.T) {
	bad := []string{
		`ext::sh -c "id > /tmp/pwned"`,
		"fd::17/foo",
		"transport::address",
		"-oProxyCommand=evil",
		"--upload-pack=evil",
		"sneaky://example.com/repo.git",
	}
	for _, raw := range bad {
		if err := validateRemoteURL(raw); err == nil {
			t.Errorf("validateRemoteURL(%q) = nil, want error", raw)
		}
	}

	ok := []string{
		"https://example.com/acme/memory.git",
		"http://example.com/acme/memory.git",
		"ssh://git@example.com/acme/memory.git",
		"git@example.com:acme/memory.git",
		"/tmp/local/remote.git",
		"file:///tmp/local/remote.git",
	}
	for _, raw := range ok {
		if err := validateRemoteURL(raw); err != nil {
			t.Errorf("validateRemoteURL(%q) = %v, want nil", raw, err)
		}
	}
}

func TestNewGitRejectsTransportHelperRemote(t *testing.T) {
	_, err := newGitFromConfig(GitConfig{
		WorkDir:   filepath.Join(t.TempDir(), "work"),
		RemoteURL: `ext::sh -c "id"`,
	})
	if err == nil {
		t.Fatal("newGitFromConfig accepted an ext:: transport remote")
	}
}

func TestCheckGitConnectionPushesAndCleansTemporaryBranch(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	runGitRaw(t, "", "init", "--bare", remote)
	seedRepo(t, remote, filepath.Join(root, "seed"), map[string]string{
		"MEMORY.md": "# Memory\n",
	})

	report := CheckGitConnection(GitConfig{
		RemoteURL:   remote,
		Branch:      "main",
		AuthorName:  "memd test",
		AuthorEmail: "memd@example.com",
	})
	if !report.OK {
		t.Fatalf("report not ok: %+v", report)
	}
	for _, check := range report.Checks {
		if !check.OK {
			t.Fatalf("check failed: %+v", check)
		}
	}
	out := runGitRaw(t, "", "--git-dir", remote, "for-each-ref", "--format=%(refname)", "refs/heads")
	if strings.Contains(out, "refs/heads/memd-connection-check/") {
		t.Fatalf("temporary check branch was not cleaned up:\n%s", out)
	}
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git executable not available")
	}
}

func seedRepo(t *testing.T, remote, dir string, files map[string]string) {
	t.Helper()
	runGitRaw(t, "", "clone", remote, dir)
	runGit(t, dir, "checkout", "-B", "main")
	for path, content := range files {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "-c", "user.name=memd test", "-c", "user.email=memd@example.com", "commit", "-m", "seed")
	runGit(t, dir, "push", "-u", "origin", "main")
	runGitRaw(t, "", "--git-dir", remote, "symbolic-ref", "HEAD", "refs/heads/main")
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return runGitRaw(t, dir, append([]string{"-C", dir}, args...)...)
}

func runGitRaw(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out)
}
