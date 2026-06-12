package doctrine

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestEmbeddedDoctrineMatchesCanonical pins the embedded doctrine (doctrine.Text,
// from server/internal/doctrine/doctrine.md) to the canonical source at
// docs/doctrine.md. build/build.sh's sync_doctrine copies docs -> embedded at
// build time only, so drift between the two is otherwise invisible until a
// build. This test makes drift a CI failure instead.
func TestEmbeddedDoctrineMatchesCanonical(t *testing.T) {
	canonicalPath := findCanonicalDoctrine(t)

	canonical, err := os.ReadFile(canonicalPath)
	if err != nil {
		t.Fatalf("read canonical doctrine at %s: %v", canonicalPath, err)
	}

	if string(canonical) != Text {
		t.Fatalf(`embedded doctrine is out of sync with the canonical source.
  canonical: %s (%d bytes)
  embedded:  server/internal/doctrine/doctrine.md (%d bytes)

Re-sync before committing, either:
  build/build.sh            # runs sync_doctrine
  cp docs/doctrine.md server/internal/doctrine/doctrine.md`,
			canonicalPath, len(canonical), len(Text))
	}
}

// findCanonicalDoctrine locates docs/doctrine.md by walking upward from this
// test file's own directory (resolved via runtime.Caller, so it is independent
// of the working directory the test is invoked from) until it finds a directory
// that contains both a go.mod and docs/doctrine.md — i.e. the repo root.
func findCanonicalDoctrine(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed; cannot locate test file path")
	}
	dir := filepath.Dir(thisFile)
	for {
		doctrineMD := filepath.Join(dir, "docs", "doctrine.md")
		_, goModErr := os.Stat(filepath.Join(dir, "go.mod"))
		_, doctrineErr := os.Stat(doctrineMD)
		if goModErr == nil && doctrineErr == nil {
			return doctrineMD
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("walked to filesystem root from %s without finding go.mod + docs/doctrine.md", filepath.Dir(thisFile))
		}
		dir = parent
	}
}
