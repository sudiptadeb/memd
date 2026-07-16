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

func TestPreferencesOverlay(t *testing.T) {
	tasks, _ := Builtins().Lookup("tasks")

	cases := []struct {
		name string
		body string
		want string
	}{
		{"empty file", "", ""},
		{"untouched current scaffold", tasks.PreferencesTemplate(), ""},
		{"untouched legacy scaffold", tasksLegacyPrefsTemplate, ""},
		{"heading only", "# Tasks — your preferences\n", ""},
		{"comment only", "<!-- guidance -->\n", ""},
		{"unterminated comment", "# Tasks\n\n<!-- half a comment\n- not a real pref", ""},
		{
			"real preference under scaffold",
			tasks.PreferencesTemplate() + "\n- Tag work with #work\n",
			"- Tag work with #work",
		},
		{
			"plain preference file",
			"- Always schedule 1 hour early\n- Tag with #work\n",
			"- Always schedule 1 hour early\n- Tag with #work",
		},
	}
	for _, tc := range cases {
		if got := tasks.PreferencesOverlay(tc.body); got != tc.want {
			t.Errorf("%s: overlay = %q, want %q", tc.name, got, tc.want)
		}
	}
}
