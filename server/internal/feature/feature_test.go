package feature

import "testing"

func TestBuiltins(t *testing.T) {
	r := Builtins()

	tasks, ok := r.Lookup("tasks")
	if !ok {
		t.Fatal("tasks feature missing")
	}
	if tasks.Folder != "tasks" {
		t.Errorf("tasks folder = %q, want tasks", tasks.Folder)
	}
	if tasks.ComingSoon {
		t.Error("tasks should be available, not coming-soon")
	}
	if tasks.BaseDoctrine() == "" || tasks.PreferencesTemplate() == "" {
		t.Error("tasks should ship a base doctrine and a preferences template")
	}

	cal, ok := r.Lookup("calendar")
	if !ok {
		t.Fatal("calendar feature missing")
	}
	if !cal.ComingSoon {
		t.Error("calendar should be coming-soon")
	}

	if r.Has("nope") {
		t.Error("unknown key should not be reported as present")
	}
	if len(r.List()) != 2 {
		t.Errorf("List len = %d, want 2", len(r.List()))
	}
}
