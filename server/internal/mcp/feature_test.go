package mcp

import (
	"strings"
	"testing"
)

func TestActiveMemoryFeatureSection(t *testing.T) {
	s, conn, dirID := testBudgetServer(t)

	// Without the feature enabled, no feature memory section appears.
	if got := s.activeMemorySection(&conn); strings.Contains(got, "Structured memory enabled here") {
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
	if !strings.Contains(got, "Tasks are a kind of memory") {
		t.Error("base tasks doctrine missing from memory_load output")
	}
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
}
