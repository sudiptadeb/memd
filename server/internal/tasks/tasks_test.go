package tasks

import (
	"testing"
	"time"
)

func TestParseFileBasic(t *testing.T) {
	content := `# Home

- [ ] Paint the bedroom  due:2026-06-20 prio:high #home
    - [ ] buy paint
    - [x] tape edges
    note: Asha wants matte, not gloss
- [x] Hang the mirror
`
	tasks := ParseFile("tasks/home.md", []byte(content))
	if len(tasks) != 2 {
		t.Fatalf("want 2 top-level tasks, got %d", len(tasks))
	}
	paint := tasks[0]
	if paint.Title != "Paint the bedroom" {
		t.Errorf("title = %q", paint.Title)
	}
	if paint.Due != "2026-06-20" || paint.Prio != "high" {
		t.Errorf("due=%q prio=%q", paint.Due, paint.Prio)
	}
	if len(paint.Tags) != 1 || paint.Tags[0] != "home" {
		t.Errorf("tags = %v", paint.Tags)
	}
	if paint.Done {
		t.Error("paint should be open")
	}
	if len(paint.Subtasks) != 2 {
		t.Fatalf("want 2 subtasks, got %d", len(paint.Subtasks))
	}
	if paint.Subtasks[0].Title != "buy paint" || paint.Subtasks[0].Done {
		t.Errorf("subtask0 = %+v", paint.Subtasks[0])
	}
	if !paint.Subtasks[1].Done {
		t.Error("tape edges should be done")
	}
	if len(paint.Notes) != 1 || paint.Notes[0] != "Asha wants matte, not gloss" {
		t.Errorf("notes = %v", paint.Notes)
	}
	if paint.Line != 3 {
		t.Errorf("paint line = %d, want 3", paint.Line)
	}
	if !tasks[1].Done {
		t.Error("hang mirror should be done")
	}
}

func TestParseFileLink(t *testing.T) {
	content := "- [ ] [Paint the bedroom](paint-bedroom.md) due:2026-06-20\n"
	tasks := ParseFile("tasks/inbox.md", []byte(content))
	if len(tasks) != 1 {
		t.Fatalf("want 1 task, got %d", len(tasks))
	}
	if tasks[0].Link != "paint-bedroom.md" {
		t.Errorf("link = %q", tasks[0].Link)
	}
	if tasks[0].Title != "Paint the bedroom" {
		t.Errorf("title = %q", tasks[0].Title)
	}
	if tasks[0].Due != "2026-06-20" {
		t.Errorf("due = %q", tasks[0].Due)
	}
}

func TestToggleLine(t *testing.T) {
	content := "- [ ] one\n- [ ] two\n- [x] three\n"
	out, err := ToggleLine([]byte(content), 2, "- [ ] two")
	if err != nil {
		t.Fatal(err)
	}
	want := "- [ ] one\n- [x] two\n- [x] three\n"
	if string(out) != want {
		t.Errorf("got %q want %q", out, want)
	}
	// Toggle back off.
	out, err = ToggleLine([]byte(want), 3, "- [x] three")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "- [ ] one\n- [x] two\n- [ ] three\n" {
		t.Errorf("toggle off got %q", out)
	}
}

func TestToggleLinePreservesTokens(t *testing.T) {
	content := "- [ ] Paint  due:2026-06-20 prio:high #home\n"
	out, err := ToggleLine([]byte(content), 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "- [x] Paint  due:2026-06-20 prio:high #home\n" {
		t.Errorf("tokens not preserved: %q", out)
	}
}

func TestToggleLineStaleGuard(t *testing.T) {
	content := "- [ ] one\n- [ ] two\n"
	if _, err := ToggleLine([]byte(content), 2, "- [ ] something else"); err == nil {
		t.Error("expected stale guard error")
	}
}

func TestToggleLineNotATask(t *testing.T) {
	content := "# heading\n- [ ] one\n"
	if _, err := ToggleLine([]byte(content), 1, ""); err == nil {
		t.Error("expected not-a-task error")
	}
}

func TestToggleLineCRLF(t *testing.T) {
	content := "- [ ] one\r\n- [ ] two\r\n"
	out, err := ToggleLine([]byte(content), 1, "- [ ] one")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "- [x] one\r\n- [ ] two\r\n" {
		t.Errorf("crlf got %q", out)
	}
}

func TestAppendTask(t *testing.T) {
	if got := string(AppendTask(nil, "first")); got != "- [ ] first\n" {
		t.Errorf("empty append = %q", got)
	}
	if got := string(AppendTask([]byte("- [ ] one"), "two")); got != "- [ ] one\n- [ ] two\n" {
		t.Errorf("append no trailing nl = %q", got)
	}
	if got := string(AppendTask([]byte("- [ ] one\n"), "two\nwith newline")); got != "- [ ] one\n- [ ] two with newline\n" {
		t.Errorf("append sanitised = %q", got)
	}
}

func TestBuildBoard(t *testing.T) {
	now := time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)
	lists := []List{
		BuildList("tasks/inbox.md", "inbox", []byte(
			"- [ ] overdue thing due:2026-06-10\n"+
				"- [ ] soon thing due:2026-06-18\n"+
				"- [ ] later thing due:2026-08-01\n"+
				"- [ ] no date thing\n"+
				"- [x] done thing due:2026-06-09\n")),
	}
	b := BuildBoard(lists, now)
	if len(b.Overdue) != 1 || b.Overdue[0].Title != "overdue thing" {
		t.Errorf("overdue = %+v", b.Overdue)
	}
	if len(b.DueSoon) != 1 || b.DueSoon[0].Title != "soon thing" {
		t.Errorf("due soon = %+v", b.DueSoon)
	}
	if len(b.Later) != 1 {
		t.Errorf("later = %+v", b.Later)
	}
	if len(b.NoDate) != 1 {
		t.Errorf("no date = %+v", b.NoDate)
	}
	if len(b.Lists) != 1 || b.Lists[0].Open != 4 || b.Lists[0].Total != 5 {
		t.Errorf("list summary = %+v", b.Lists)
	}
}

func TestDisplayName(t *testing.T) {
	cases := map[string]string{
		"inbox.md":           "inbox",
		"home-renovation.md": "home renovation",
		"next_trip.md":       "next trip",
	}
	for in, want := range cases {
		if got := DisplayName(in); got != want {
			t.Errorf("DisplayName(%q) = %q want %q", in, got, want)
		}
	}
}

func TestIsListFile(t *testing.T) {
	for name, want := range map[string]bool{
		"inbox.md":    true,
		"_feature.md": false,
		"_board.md":   false,
		"notes.txt":   false,
		"trip.md":     true,
	} {
		if got := IsListFile(name); got != want {
			t.Errorf("IsListFile(%q) = %v want %v", name, got, want)
		}
	}
}
