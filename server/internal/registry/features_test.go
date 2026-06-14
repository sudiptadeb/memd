package registry

import (
	"strings"
	"testing"

	"github.com/sudiptadeb/memd/server/internal/config"
)

func TestSetDirectoryFeature(t *testing.T) {
	reg := NewEphemeral()
	t.Cleanup(func() { _ = reg.Close() })
	dirID, err := reg.AddDirectory(config.Directory{Name: "personal", Backend: "local", LocalPath: t.TempDir()})
	if err != nil {
		t.Fatalf("AddDirectory: %v", err)
	}

	// Enable tasks: directory records it and the preference file is scaffolded.
	d, err := reg.SetDirectoryFeatureForActor("", dirID, "tasks", true)
	if err != nil {
		t.Fatalf("enable tasks: %v", err)
	}
	if !featureEnabled(d.Features, "tasks") {
		t.Fatalf("tasks not enabled: %+v", d.Features)
	}
	dv := reg.DirectoryViewForUser("", dirID)
	if dv == nil || dv.Backend == nil {
		t.Fatal("directory backend unavailable")
	}
	prefs, err := dv.Backend.Read("tasks/_feature.md")
	if err != nil {
		t.Fatalf("scaffolded _feature.md missing: %v", err)
	}
	if !strings.Contains(string(prefs), "your preferences") {
		t.Errorf("scaffold should be a preferences template, got: %q", prefs)
	}

	// Disable tasks: flag flips but the folder/file stays (disable != delete).
	d, err = reg.SetDirectoryFeatureForActor("", dirID, "tasks", false)
	if err != nil {
		t.Fatalf("disable tasks: %v", err)
	}
	if featureEnabled(d.Features, "tasks") {
		t.Error("tasks should be disabled")
	}
	if _, err := dv.Backend.Read("tasks/_feature.md"); err != nil {
		t.Errorf("disabling must not delete the folder/file: %v", err)
	}

	// Unknown feature and coming-soon feature are rejected on enable.
	if _, err := reg.SetDirectoryFeatureForActor("", dirID, "nope", true); err == nil {
		t.Error("unknown feature should be rejected")
	}
	if _, err := reg.SetDirectoryFeatureForActor("", dirID, "calendar", true); err == nil {
		t.Error("coming-soon feature should be rejected on enable")
	}
}

func featureEnabled(features []config.DirectoryFeature, key string) bool {
	for _, f := range features {
		if f.Key == key {
			return f.Enabled
		}
	}
	return false
}
