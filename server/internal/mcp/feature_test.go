package mcp

import (
	"encoding/json"
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

	got := s.activeMemorySection(&conn)
	// The preload carries only the one-line trigger: summary bullet plus the
	// pointer to memory_feature_guide — never the full base doctrine.
	if !strings.Contains(got, "## Structured memory") {
		t.Error("structured-memory summary missing from memory_load output")
	}
	if !strings.Contains(got, "`tasks` — things the user needs to do") {
		t.Error("tasks summary bullet missing from memory_load output")
	}
	if !strings.Contains(got, "memory_feature_guide") {
		t.Error("memory_feature_guide trigger missing from memory_load output")
	}
	if strings.Contains(got, "Tasks are a kind of memory") {
		t.Error("full tasks doctrine leaked into memory_load output; it belongs behind memory_feature_guide")
	}
	// The freshly scaffolded _feature.md holds no user preferences, so the
	// preload must not echo the template.
	if strings.Contains(got, "Preferences (tasks/_feature.md") {
		t.Errorf("untouched preferences scaffold echoed in memory_load output:\n%s", got)
	}

	// A real user preference is appended quietly: present, hedged label, and
	// no managed front matter alongside it.
	d := s.reg.DirectoryForConnector(&conn, dirID)
	if d == nil {
		t.Fatal("directory not accessible")
	}
	if err := d.Backend.Write("tasks/_feature.md", []byte("- MARKER-PREF always tag work with #work\n"), "test"); err != nil {
		t.Fatalf("write prefs: %v", err)
	}
	got = s.activeMemorySection(&conn)
	if !strings.Contains(got, "MARKER-PREF") {
		t.Error("user preference overlay missing from memory_load output")
	}
	if !strings.Contains(got, "apply where relevant") {
		t.Error("preference overlay lost its hedged label")
	}
	if strings.Contains(got, "access_count") {
		t.Error("managed memd: front matter leaked into memory_load output")
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

// The pre-comment-era scaffold carries no user preferences either; a directory
// enabled before the template changed must not echo it.
func TestActiveMemoryLegacyPrefsTemplateSuppressed(t *testing.T) {
	s, conn, dirID := testBudgetServer(t)
	if _, err := s.reg.SetDirectoryFeatureForActor("", dirID, "tasks", true); err != nil {
		t.Fatalf("enable tasks: %v", err)
	}
	d := s.reg.DirectoryForConnector(&conn, dirID)
	if d == nil {
		t.Fatal("directory not accessible")
	}
	legacy := "# Tasks — your preferences\n\n" +
		"These rules are layered on top of memd's built-in task behavior. Add your own;\n" +
		"you or the agent may edit this file freely. Examples:\n\n" +
		"- Always schedule tasks to be done 1 hour earlier than the real deadline.\n" +
		"- Tag anything work-related with #work.\n"
	if err := d.Backend.Write("tasks/_feature.md", []byte(legacy), "test"); err != nil {
		t.Fatalf("write legacy prefs: %v", err)
	}
	if got := s.activeMemorySection(&conn); strings.Contains(got, "Preferences (tasks/_feature.md") {
		t.Errorf("legacy preferences scaffold echoed in memory_load output:\n%s", got)
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

func guideArgs(t *testing.T, kv map[string]string) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(kv)
	if err != nil {
		t.Fatalf("marshal guide args: %v", err)
	}
	return b
}

func TestToolFeatureGuide(t *testing.T) {
	s, conn, dirID := testBudgetServer(t)

	// Unknown / coming-soon / missing feature keys error, listing what exists.
	if text, isErr := s.toolFeatureGuide(&conn, guideArgs(t, map[string]string{"feature": "nope"})); !isErr || !strings.Contains(text, "tasks") {
		t.Errorf("unknown feature: isErr=%v text=%q, want error listing 'tasks'", isErr, text)
	}
	if _, isErr := s.toolFeatureGuide(&conn, guideArgs(t, map[string]string{"feature": "calendar"})); !isErr {
		t.Error("coming-soon feature should not be served")
	}
	if _, isErr := s.toolFeatureGuide(&conn, guideArgs(t, map[string]string{})); !isErr {
		t.Error("missing feature key should error")
	}

	// Not enabled anywhere: the doctrine is still served, with a note.
	text, isErr := s.toolFeatureGuide(&conn, guideArgs(t, map[string]string{"feature": "tasks"}))
	if isErr {
		t.Fatalf("guide errored: %s", text)
	}
	if !strings.Contains(text, "Tasks are a kind of memory") {
		t.Error("base doctrine missing from guide output")
	}
	if !strings.Contains(text, "Not enabled in any accessible directory") {
		t.Errorf("expected not-enabled note, got:\n%s", text)
	}

	if _, err := s.reg.SetDirectoryFeatureForActor("", dirID, "tasks", true); err != nil {
		t.Fatalf("enable tasks: %v", err)
	}

	// Enabled with an untouched scaffold: doctrine + state + "(no user preferences set".
	text, isErr = s.toolFeatureGuide(&conn, guideArgs(t, map[string]string{"feature": "tasks"}))
	if isErr {
		t.Fatalf("guide errored: %s", text)
	}
	for _, want := range []string{"Tasks are a kind of memory", "no tasks yet", "(no user preferences set"} {
		if !strings.Contains(text, want) {
			t.Errorf("guide output missing %q:\n%s", want, text)
		}
	}

	// Customised preferences are layered on top, without managed front matter.
	d := s.reg.DirectoryForConnector(&conn, dirID)
	if d == nil {
		t.Fatal("directory not accessible")
	}
	if err := d.Backend.Write("tasks/_feature.md", []byte("- MARKER-PREF always tag work with #work\n"), "test"); err != nil {
		t.Fatalf("write prefs: %v", err)
	}
	text, isErr = s.toolFeatureGuide(&conn, guideArgs(t, map[string]string{"feature": "tasks"}))
	if isErr {
		t.Fatalf("guide errored: %s", text)
	}
	if !strings.Contains(text, "MARKER-PREF") {
		t.Errorf("user preferences missing from guide output:\n%s", text)
	}
	if strings.Contains(text, "access_count") {
		t.Error("managed memd: front matter leaked into guide output")
	}

	// directory_id filter: an inaccessible id errors.
	if _, isErr := s.toolFeatureGuide(&conn, guideArgs(t, map[string]string{"feature": "tasks", "directory_id": "bogus"})); !isErr {
		t.Error("bogus directory_id should error")
	}
	// The guide is callable through tools/call under its catalog name.
	found := false
	for _, tool := range toolsCatalog {
		if tool["name"] == "memory_feature_guide" {
			found = true
		}
	}
	if !found {
		t.Error("memory_feature_guide missing from the tool catalog")
	}
}
