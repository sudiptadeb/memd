package storage

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestLocal_WriteReadLifecycle(t *testing.T) {
	dir := t.TempDir()
	l, err := NewLocal(dir)
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}

	// Write a fresh page.
	if err := l.Write("hello.md", []byte("# Hello\n\nbody\n"), ""); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Read it back — should have stats with access_count=1 after read.
	out, err := l.Read("hello.md")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	p := ParsePage(out)
	if !p.HasFM {
		t.Fatalf("page lost FM on first read: %s", out)
	}
	if p.Stats.AccessCount != 1 {
		t.Fatalf("AccessCount = %d, want 1", p.Stats.AccessCount)
	}
	if p.Stats.CreatedAt.IsZero() || p.Stats.UpdatedAt.IsZero() || p.Stats.LastReadAt.IsZero() {
		t.Fatalf("timestamps unset: %+v", p.Stats)
	}
	if !strings.Contains(string(p.Body), "# Hello") {
		t.Fatalf("body lost: %q", p.Body)
	}

	// Second read should bump access_count to 2.
	out2, _ := l.Read("hello.md")
	p2 := ParsePage(out2)
	if p2.Stats.AccessCount != 2 {
		t.Fatalf("AccessCount after 2nd read = %d, want 2", p2.Stats.AccessCount)
	}
}

func TestLocal_WriteStripsAgentMemdSubtree(t *testing.T) {
	dir := t.TempDir()
	l, _ := NewLocal(dir)
	// Agent tries to lie about its own stats.
	payload := []byte(`---
memd:
  access_count: 9999
  created_at: 1999-01-01
topic: dlp
---
# Body
`)
	if err := l.Write("page.md", payload, ""); err != nil {
		t.Fatalf("Write: %v", err)
	}
	on_disk, _ := os.ReadFile(filepath.Join(dir, "page.md"))
	p := ParsePage(on_disk)
	if p.Stats.AccessCount != 0 {
		t.Fatalf("server should have overwritten access_count, got %d", p.Stats.AccessCount)
	}
	if p.Stats.CreatedAt.Format("2006") == "1999" {
		t.Fatalf("server should have overwritten created_at, got %v", p.Stats.CreatedAt)
	}
	if !strings.Contains(p.AgentFM, "topic: dlp") {
		t.Fatalf("agent FM lost: %q", p.AgentFM)
	}
}

func TestLocal_WritePreservesStatsAcrossUpdates(t *testing.T) {
	dir := t.TempDir()
	l, _ := NewLocal(dir)

	_ = l.Write("page.md", []byte("# v1\n"), "")
	_, _ = l.Read("page.md") // access_count → 1
	_, _ = l.Read("page.md") // access_count → 2

	// Update body — access_count and last_read_at should survive.
	if err := l.Write("page.md", []byte("# v2\n"), ""); err != nil {
		t.Fatalf("Write update: %v", err)
	}
	on_disk, _ := os.ReadFile(filepath.Join(dir, "page.md"))
	p := ParsePage(on_disk)
	if p.Stats.AccessCount != 2 {
		t.Fatalf("AccessCount after Write should preserve, got %d want 2", p.Stats.AccessCount)
	}
	if !strings.Contains(string(p.Body), "v2") {
		t.Fatalf("body not updated: %q", p.Body)
	}
}

func TestLocal_ConcurrentReadsNoLostUpdates(t *testing.T) {
	dir := t.TempDir()
	l, _ := NewLocal(dir)
	if err := l.Write("page.md", []byte("# body\n"), ""); err != nil {
		t.Fatalf("seed write: %v", err)
	}

	const N = 50
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			if _, err := l.Read("page.md"); err != nil {
				t.Errorf("Read: %v", err)
			}
		}()
	}
	wg.Wait()

	out, _ := os.ReadFile(filepath.Join(dir, "page.md"))
	p := ParsePage(out)
	if p.Stats.AccessCount != N {
		t.Fatalf("AccessCount after %d concurrent reads = %d, want %d", N, p.Stats.AccessCount, N)
	}
}

func TestLocal_AtomicWriteLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	l, _ := NewLocal(dir)
	if err := l.Write("page.md", []byte("# body\n"), ""); err != nil {
		t.Fatalf("Write: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") && strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("leftover temp file: %s", e.Name())
		}
	}
}

func TestLocal_RejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	l, _ := NewLocal(dir)

	cases := []struct {
		name, path string
	}{
		{"single dotdot", "../escape.md"},
		{"deep dotdot to existing file", "../../../../etc/passwd"},
		{"mixed traversal", "memory/../../escape.md"},
		{"escape via non-existent target", "../../never-exists-xyz123.md"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := l.Read(c.path); err == nil {
				t.Fatalf("Read(%q) should fail", c.path)
			}
			if err := l.Write(c.path, []byte("x"), ""); err == nil {
				t.Fatalf("Write(%q) should fail", c.path)
			}
		})
	}
}

func TestLocal_RejectsSymlinkToOutsideFile(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.md"), []byte("top secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Symlink(filepath.Join(outside, "secret.md"), filepath.Join(dir, "leak.md")); err != nil {
		t.Skipf("symlink not supported on this fs: %v", err)
	}
	l, _ := NewLocal(dir)

	if _, err := l.Read("leak.md"); err == nil {
		t.Fatalf("Read through a symlink-to-outside should fail")
	}
	if err := l.Write("leak.md", []byte("pwned"), ""); err == nil {
		t.Fatalf("Write through a symlink-to-outside should fail")
	}
	// And the outside file must be untouched.
	got, _ := os.ReadFile(filepath.Join(outside, "secret.md"))
	if string(got) != "top secret" {
		t.Fatalf("outside file was modified: %q", got)
	}
}

func TestLocal_RejectsSymlinkedParentDirToOutside(t *testing.T) {
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "victim"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Symlink(filepath.Join(outside, "victim"), filepath.Join(dir, "sub")); err != nil {
		t.Skipf("symlink not supported on this fs: %v", err)
	}
	l, _ := NewLocal(dir)

	if err := l.Write("sub/page.md", []byte("x"), ""); err == nil {
		t.Fatalf("Write under a symlinked parent pointing outside should fail")
	}
}

func TestLocal_AllowsSymlinkToInsideFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "memory", "topic-a.md")
	if err := os.WriteFile(target, []byte("---\nmemd:\n  access_count: 0\n---\n# A\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "alias.md")); err != nil {
		t.Skipf("symlink not supported on this fs: %v", err)
	}
	l, _ := NewLocal(dir)

	if _, err := l.Read("alias.md"); err != nil {
		t.Fatalf("Read through symlink-to-inside should succeed: %v", err)
	}
}

func TestLocal_AllowsSymlinkedParentDirToInside(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "memory"), filepath.Join(dir, "shortcut")); err != nil {
		t.Skipf("symlink not supported on this fs: %v", err)
	}
	l, _ := NewLocal(dir)

	if err := l.Write("shortcut/page.md", []byte("# x\n"), ""); err != nil {
		t.Fatalf("Write under a symlinked parent pointing inside should succeed: %v", err)
	}
	// And it should be reachable at the canonical path.
	if _, err := os.Stat(filepath.Join(dir, "memory", "page.md")); err != nil {
		t.Fatalf("expected file at canonical path: %v", err)
	}
}

func TestLocal_RejectsChainedSymlinkEscape(t *testing.T) {
	// inside-symlink -> inside-dir -> ... but the inside-dir itself is a
	// symlink to outside. EvalSymlinks must follow the full chain.
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.md"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "hop")); err != nil {
		t.Skipf("symlink not supported on this fs: %v", err)
	}
	if err := os.Symlink(filepath.Join(dir, "hop", "secret.md"), filepath.Join(dir, "leak.md")); err != nil {
		t.Skipf("symlink not supported on this fs: %v", err)
	}
	l, _ := NewLocal(dir)

	if _, err := l.Read("leak.md"); err == nil {
		t.Fatalf("Read through a chained symlink that ends outside should fail")
	}
}

func TestLocal_SymlinkedRootStillWorks(t *testing.T) {
	// macOS's /tmp is a symlink to /private/tmp. Simulate by creating a
	// symlinked root and verifying that ordinary reads/writes succeed.
	real := t.TempDir()
	parent := t.TempDir()
	link := filepath.Join(parent, "root-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink not supported on this fs: %v", err)
	}
	l, err := NewLocal(link)
	if err != nil {
		t.Fatalf("NewLocal with symlinked root: %v", err)
	}
	if err := l.Write("page.md", []byte("# hi\n"), ""); err != nil {
		t.Fatalf("Write under symlinked root: %v", err)
	}
	if _, err := l.Read("page.md"); err != nil {
		t.Fatalf("Read under symlinked root: %v", err)
	}
}

func TestLocal_NonMarkdownPassthrough(t *testing.T) {
	dir := t.TempDir()
	l, _ := NewLocal(dir)
	if err := os.WriteFile(filepath.Join(dir, "blob.bin"), []byte("\x00\x01\x02"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := l.Read("blob.bin")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(out) != "\x00\x01\x02" {
		t.Fatalf("non-markdown should pass through, got %q", out)
	}
}
