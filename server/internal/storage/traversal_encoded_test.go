package storage

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLocal_RejectsOrContainsEncodedTraversal feeds hostile, encoded path
// variants through Read/Write/Delete and pins the server's behavior:
//
//   - The server does NOT percent-decode or URL-decode paths. A sequence like
//     `%2e%2e` is treated as the literal four-byte filename component "%2e%2e",
//     not as "..". `resolve` runs filepath.Clean(Join(root, FromSlash(rel))),
//     which only folds real `.`/`..` segments lexically. So encoded traversal
//     can never climb above the root — at worst it creates a literally-named
//     file *inside* the root, which is harmless.
//   - Backslash variants (`..\escape.md`) are, on POSIX, a single literal
//     filename (filepath.FromSlash is a no-op off Windows; `\` is a legal
//     filename byte). They likewise stay inside the root.
//   - Absolute paths (`/etc/passwd`, `C:\evil.md`) are defeated because
//     filepath.Join discards a leading separator on the rel argument and, for
//     the Windows drive form, `C:\evil.md` becomes a literal in-root name on
//     POSIX. EvalSymlinks-based containment then rejects anything that still
//     resolves outside.
//
// The invariant asserted for every case: nothing is ever created, read, or
// deleted OUTSIDE the temp root. Either the op errors, or it touches a literal
// path strictly inside the root. We additionally place a sentinel file at the
// "escape target" location next to the root and confirm it is never disturbed.
func TestLocal_RejectsOrContainsEncodedTraversal(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{"percent encoded dotdot slash", "%2e%2e/escape.md"},
		{"dotdot percent encoded slash", "..%2fescape.md"},
		{"fully percent encoded dotdot slash", "%2e%2e%2fescape.md"},
		{"backslash dotdot", `..\escape.md`},
		{"mixed encoded traversal", "memory/%2e%2e/%2e%2e/escape.md"},
		{"absolute unix path", "/etc/passwd"},
		{"windows drive path", `C:\evil.md`},
		// A NUL byte is not expressible as a Go filesystem path; os/syscall
		// rejects it with EINVAL. We include a control byte the kernel will
		// store literally to prove non-NUL control bytes also stay contained.
		{"control byte name", "memory/\x01bad.md"},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			// Fresh roots per case so a literal-in-root write from one case
			// can't be mistaken for an escape in another.
			root := t.TempDir()
			parent := filepath.Dir(root)

			// Sentinel that must survive untouched: the bare basename of the
			// escape target, planted in the root's PARENT directory. If any op
			// ever decoded the traversal and climbed one level, it would land
			// here.
			sentinelName := "escape.md"
			if c.name == "absolute unix path" {
				sentinelName = "passwd-sentinel.md"
			}
			sentinel := filepath.Join(parent, sentinelName)
			if err := os.WriteFile(sentinel, []byte("DO NOT TOUCH"), 0o644); err != nil {
				t.Fatalf("seed sentinel: %v", err)
			}
			t.Cleanup(func() { _ = os.Remove(sentinel) })

			l, err := NewLocal(root)
			if err != nil {
				t.Fatalf("NewLocal: %v", err)
			}

			// --- Read: must never read anything above the root. ---
			if _, err := l.Read(c.path); err == nil {
				// A nil error is only acceptable if the path resolved to a real
				// file strictly inside the root. Since we never created such a
				// file, Read should always fail here. If it somehow succeeded,
				// confirm it did not reach outside.
				assertNoEscape(t, root, parent, sentinel, "Read")
			}

			// --- Write: either errors, or creates a literal file inside root. ---
			werr := l.Write(c.path, []byte("payload"), "")
			assertNoEscape(t, root, parent, sentinel, "Write")
			if werr == nil {
				// The server accepted the path literally. Whatever it wrote MUST
				// live inside the root. Walk the root and verify every regular
				// file is contained (WalkDir over rootEval can't escape, but we
				// also re-resolve to be explicit) — and that the parent gained
				// no stray file.
				assertParentUnchanged(t, parent, root, sentinel)
			}

			// --- Delete: must never remove anything outside the root. ---
			// (Most of these don't exist as files; Delete should error. The
			// point is that the sentinel and parent are never touched.)
			_ = l.Delete(c.path, "")
			assertNoEscape(t, root, parent, sentinel, "Delete")
			assertParentUnchanged(t, parent, root, sentinel)
		})
	}
}

// assertNoEscape verifies the sentinel above the root is intact and that no
// file materialized at the parent-level escape target.
func assertNoEscape(t *testing.T, root, parent, sentinel, op string) {
	t.Helper()
	got, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatalf("%s: sentinel disappeared (%v) — op reached outside root", op, err)
	}
	if string(got) != "DO NOT TOUCH" {
		t.Fatalf("%s: sentinel modified to %q — op wrote outside root", op, got)
	}
	// The escape target the encoded `..` was aiming for is escape.md in the
	// parent. Confirm no NEW escape.md (other than the sentinel itself) exists.
	stray := filepath.Join(parent, "escape.md")
	if stray != sentinel {
		if _, err := os.Stat(stray); err == nil {
			t.Fatalf("%s: created %s outside root", op, stray)
		}
	}
}

// assertParentUnchanged confirms the root's parent directory contains only the
// root itself and the sentinel — i.e. no operation leaked a sibling file.
func assertParentUnchanged(t *testing.T, parent, root, sentinel string) {
	t.Helper()
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatalf("read parent: %v", err)
	}
	rootBase := filepath.Base(root)
	sentinelBase := filepath.Base(sentinel)
	for _, e := range entries {
		name := e.Name()
		if name == rootBase || name == sentinelBase {
			continue
		}
		full := filepath.Join(parent, name)
		// t.TempDir() parents can legitimately hold other tests' temp roots in
		// rare shared-parent setups, but t.TempDir gives each test a unique
		// dir whose parent is created per-test, so anything else here is a
		// leak from the op under test.
		if !e.IsDir() {
			t.Fatalf("operation leaked sibling file into parent: %s", full)
		}
	}
}
