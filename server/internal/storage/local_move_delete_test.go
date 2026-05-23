package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocal_Move_RenamesFile(t *testing.T) {
	dir := t.TempDir()
	l, _ := NewLocal(dir)
	if err := l.Write("memory/old.md", []byte("# Body\n"), ""); err != nil {
		t.Fatal(err)
	}
	if err := l.Move("memory/old.md", "memory/feedback/new.md", ""); err != nil {
		t.Fatalf("Move: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "memory", "old.md")); !os.IsNotExist(err) {
		t.Fatalf("old path should be gone, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "memory", "feedback", "new.md")); err != nil {
		t.Fatalf("new path should exist: %v", err)
	}
}

func TestLocal_Move_RefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	l, _ := NewLocal(dir)
	_ = l.Write("a.md", []byte("# a\n"), "")
	_ = l.Write("b.md", []byte("# b\n"), "")
	if err := l.Move("a.md", "b.md", ""); err == nil {
		t.Fatalf("Move should refuse to overwrite existing dst")
	}
}

func TestLocal_Move_RefusesMemoryIndex(t *testing.T) {
	dir := t.TempDir()
	l, _ := NewLocal(dir)
	_ = l.Write("MEMORY.md", []byte("# Index\n"), "")
	if err := l.Move("MEMORY.md", "elsewhere.md", ""); err == nil {
		t.Fatalf("Move should refuse MEMORY.md at root")
	}
}

func TestLocal_Move_RejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	l, _ := NewLocal(dir)
	_ = l.Write("page.md", []byte("# x\n"), "")
	if err := l.Move("page.md", "../escape.md", ""); err == nil {
		t.Fatalf("Move should reject ../ in dst")
	}
}

func TestLocal_Delete_RemovesFile(t *testing.T) {
	dir := t.TempDir()
	l, _ := NewLocal(dir)
	_ = l.Write("memory/stale.md", []byte("# stale\n"), "")
	if err := l.Delete("memory/stale.md", ""); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "memory", "stale.md")); !os.IsNotExist(err) {
		t.Fatalf("file should be gone, err=%v", err)
	}
}

func TestLocal_Delete_RefusesMemoryIndex(t *testing.T) {
	dir := t.TempDir()
	l, _ := NewLocal(dir)
	_ = l.Write("MEMORY.md", []byte("# Index\n"), "")
	if err := l.Delete("MEMORY.md", ""); err == nil {
		t.Fatalf("Delete should refuse MEMORY.md at root")
	}
}

func TestLocal_Delete_RefusesFolder(t *testing.T) {
	dir := t.TempDir()
	l, _ := NewLocal(dir)
	_ = l.Write("memory/sub/x.md", []byte("# x\n"), "")
	if err := l.Delete("memory/sub", ""); err == nil {
		t.Fatalf("Delete should refuse a folder; use DeleteFolder")
	}
}

func TestLocal_DeleteFolder_RemovesRecursively(t *testing.T) {
	dir := t.TempDir()
	l, _ := NewLocal(dir)
	_ = l.Write("memory/reference/a.md", []byte("# a\n"), "")
	_ = l.Write("memory/reference/b.md", []byte("# b\n"), "")
	if err := l.DeleteFolder("memory/reference", ""); err != nil {
		t.Fatalf("DeleteFolder: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "memory", "reference")); !os.IsNotExist(err) {
		t.Fatalf("folder should be gone, err=%v", err)
	}
	// Parent (memory) should still exist.
	if _, err := os.Stat(filepath.Join(dir, "memory")); err != nil {
		t.Fatalf("parent folder should survive: %v", err)
	}
}

func TestLocal_DeleteFolder_RefusesRoot(t *testing.T) {
	dir := t.TempDir()
	l, _ := NewLocal(dir)
	for _, p := range []string{"", ".", "/"} {
		if err := l.DeleteFolder(p, ""); err == nil {
			t.Fatalf("DeleteFolder(%q) should refuse the directory root", p)
		}
	}
}

func TestLocal_DeleteFolder_RefusesFile(t *testing.T) {
	dir := t.TempDir()
	l, _ := NewLocal(dir)
	_ = l.Write("memory/file.md", []byte("# x\n"), "")
	if err := l.DeleteFolder("memory/file.md", ""); err == nil {
		t.Fatalf("DeleteFolder should refuse a file; use Delete")
	}
}

func TestLocal_Move_FolderRenamed(t *testing.T) {
	dir := t.TempDir()
	l, _ := NewLocal(dir)
	_ = l.Write("memory/reference/a.md", []byte("# a\n"), "")
	_ = l.Write("memory/reference/b.md", []byte("# b\n"), "")
	if err := l.Move("memory/reference", "memory/archive", ""); err != nil {
		t.Fatalf("Move folder: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "memory", "archive", "a.md")); err != nil {
		t.Fatalf("file should be at new folder path: %v", err)
	}
	if !strings.Contains(readOrFail(t, filepath.Join(dir, "memory", "archive", "b.md")), "# b") {
		t.Fatalf("file content lost across folder move")
	}
}

func readOrFail(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
