package logs

import "testing"

func TestSinceForViewerScopesByUser(t *testing.T) {
	// Reset package state for a deterministic test.
	mu.Lock()
	entries = nil
	nextID = 0
	mu.Unlock()

	Info("system boot")               // system entry
	InfoUser("usrA", "A did a thing") // user A
	InfoUser("usrB", "B did a thing") // user B

	// A regular user sees only their own entries.
	a := SinceForViewer(-1, "usrA", false)
	if len(a) != 1 || a[0].Message != "A did a thing" {
		t.Fatalf("user A viewer = %+v, want only A's entry", a)
	}

	// A super admin (all=true) sees everything, including system entries.
	all := SinceForViewer(-1, "usrA", true)
	if len(all) != 3 {
		t.Fatalf("super-admin viewer returned %d entries, want 3", len(all))
	}

	// An empty userID without super-admin sees nothing (no system leakage).
	none := SinceForViewer(-1, "", false)
	if len(none) != 0 {
		t.Fatalf("anonymous viewer returned %d entries, want 0", len(none))
	}
}
