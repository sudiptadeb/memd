package storage

import (
	"os"
	"path/filepath"
	"strings"
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
