package doctrine

import "testing"

func TestLiveOverrideAndReset(t *testing.T) {
	l := NewLive()
	l.Register(GlobalID, "Global", "base global")
	l.Register(FeatureID("tasks"), "Tasks", "base tasks")

	if got := l.Get(GlobalID); got != "base global" {
		t.Fatalf("default global = %q", got)
	}

	if !l.Set(GlobalID, "override global") {
		t.Fatal("Set on a known id should succeed")
	}
	if got := l.Get(GlobalID); got != "override global" {
		t.Fatalf("after override global = %q", got)
	}

	// Unaffected sibling keeps its default.
	if got := l.Get(FeatureID("tasks")); got != "base tasks" {
		t.Fatalf("tasks default = %q", got)
	}

	if l.Set("unknown", "x") {
		t.Error("Set on an unknown id should fail")
	}

	var globalView EntryView
	for _, e := range l.List() {
		if e.ID == GlobalID {
			globalView = e
		}
	}
	if !globalView.Overridden || globalView.Text != "override global" {
		t.Errorf("List global = %+v, want overridden text", globalView)
	}

	if !l.Reset(GlobalID) {
		t.Fatal("Reset on a known id should succeed")
	}
	if got := l.Get(GlobalID); got != "base global" {
		t.Fatalf("after reset global = %q, want default", got)
	}
}
