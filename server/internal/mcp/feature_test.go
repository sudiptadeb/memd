package mcp

import (
	"strings"
	"testing"
	"time"
)

func TestActiveMemoryFeatureSection(t *testing.T) {
	s, conn, dirID := testBudgetServer(t)

	// Without the feature enabled, no structured-memory section appears.
	if got := s.activeMemorySection(&conn); strings.Contains(got, "## Structured memory") {
		t.Fatal("feature section present before enabling")
	}

	if _, err := s.reg.SetDirectoryFeatureForActor("", dirID, "tasks", true); err != nil {
		t.Fatalf("enable tasks: %v", err)
	}
	// Replace the scaffolded preferences with a recognisable marker.
	d := s.reg.DirectoryForConnector(&conn, dirID)
	if d == nil {
		t.Fatal("directory not accessible")
	}
	if err := d.Backend.Write("tasks/_feature.md", []byte("- MARKER-PREF always tag work with #work\n"), "test"); err != nil {
		t.Fatalf("write prefs: %v", err)
	}

	got := s.activeMemorySection(&conn)
	// Base doctrine appears once, in the shared structured-memory section.
	if !strings.Contains(got, "Tasks are a kind of memory") {
		t.Error("base tasks doctrine missing from memory_load output")
	}
	if n := strings.Count(got, "Tasks are a kind of memory"); n != 1 {
		t.Errorf("base doctrine emitted %d times, want 1", n)
	}
	// Per-directory preferences overlay appears in the directory's own section.
	if !strings.Contains(got, "MARKER-PREF") {
		t.Error("user preference overlay missing from memory_load output")
	}

	// Disabling stops surfacing the feature (folder/data untouched).
	if _, err := s.reg.SetDirectoryFeatureForActor("", dirID, "tasks", false); err != nil {
		t.Fatalf("disable tasks: %v", err)
	}
	if got := s.activeMemorySection(&conn); strings.Contains(got, "MARKER-PREF") {
		t.Error("disabled feature still surfaced")
	}
	if got := s.activeMemorySection(&conn); strings.Contains(got, "## Structured memory") {
		t.Error("structured-memory section present after disabling")
	}
}

func TestActiveMemoryTaskSummary(t *testing.T) {
	s, conn, dirID := testBudgetServer(t)
	s.clock = func() time.Time { return time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC) }

	if _, err := s.reg.SetDirectoryFeatureForActor("", dirID, "tasks", true); err != nil {
		t.Fatalf("enable tasks: %v", err)
	}
	d := s.reg.DirectoryForConnector(&conn, dirID)
	if d == nil {
		t.Fatal("directory not accessible")
	}
	inbox := strings.Join([]string{
		"# Inbox",
		"- [ ] Renew passport due:2026-06-10",      // overdue
		"- [ ] Paint bedroom due:2026-06-18 #home", // due soon (within 7 days)
		"- [ ] File taxes due:2026-09-01",          // future, not flagged
		"- [ ] Buy milk",                           // open, no due
		"  - [ ] a subtask of buy milk",            // indented: counts as open, not a board item
		"- [x] Old done thing",                     // done
		"",
	}, "\n")
	if err := d.Backend.Write("tasks/inbox.md", []byte(inbox), "seed"); err != nil {
		t.Fatalf("write inbox: %v", err)
	}
	// A derived board file must not double-count the tasks it summarises.
	if err := d.Backend.Write("tasks/_board.md", []byte("- [ ] Renew passport due:2026-06-10\n"), "seed"); err != nil {
		t.Fatalf("write board: %v", err)
	}

	got := s.activeMemorySection(&conn)
	// Counts come from the built-in tasks grammar, which includes subtasks: the
	// four top-level open tasks plus the one open subtask make 5 open, 1 done.
	if !strings.Contains(got, "5 open · 1 done · 1 overdue · 1 due soon") {
		t.Errorf("task summary line missing/wrong; output:\n%s", got)
	}
	if !strings.Contains(got, "overdue: Renew passport (due 2026-06-10) — tasks/inbox.md") {
		t.Errorf("overdue task line missing; output:\n%s", got)
	}
	if !strings.Contains(got, "due soon: Paint bedroom #home (due 2026-06-18) — tasks/inbox.md") {
		t.Errorf("due-soon task line missing; output:\n%s", got)
	}
}
