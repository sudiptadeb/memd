package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestApplyEdits(t *testing.T) {
	const doc = "# Index\n\n## Current state\n\n- a\n- b\n\n## Later\n\n- z\n"
	cases := []struct {
		name  string
		edits []fileEdit
		want  string
	}{
		{"replace", []fileEdit{{Op: "replace", Old: "- b", New: "- B"}},
			"# Index\n\n## Current state\n\n- a\n- B\n\n## Later\n\n- z\n"},
		{"replace with empty deletes", []fileEdit{{Op: "replace", Old: "- a\n", New: ""}},
			"# Index\n\n## Current state\n\n- b\n\n## Later\n\n- z\n"},
		{"insert_after anchors mid-line", []fileEdit{{Op: "insert_after", Anchor: "- a", Text: "- a2"}},
			"# Index\n\n## Current state\n\n- a\n- a2\n- b\n\n## Later\n\n- z\n"},
		{"insert_after anchor ending in newline", []fileEdit{{Op: "insert_after", Anchor: "- a\n", Text: "- a2\n"}},
			"# Index\n\n## Current state\n\n- a\n- a2\n- b\n\n## Later\n\n- z\n"},
		{"insert_after last line without newline", nil, ""},
		{"insert_before", []fileEdit{{Op: "insert_before", Anchor: "Later", Text: "## Mid\n\n- m\n"}},
			"# Index\n\n## Current state\n\n- a\n- b\n\n## Mid\n\n- m\n## Later\n\n- z\n"},
		{"append", []fileEdit{{Op: "append", Text: "- y"}},
			"# Index\n\n## Current state\n\n- a\n- b\n\n## Later\n\n- z\n- y\n"},
		{"delete_lines removes every match", []fileEdit{{Op: "delete_lines", Match: "- "}},
			"# Index\n\n## Current state\n\n\n## Later\n\n"},
		{"edits apply in order", []fileEdit{
			{Op: "insert_after", Anchor: "- b", Text: "- c"},
			{Op: "replace", Old: "- c", New: "- C"},
		}, "# Index\n\n## Current state\n\n- a\n- b\n- C\n\n## Later\n\n- z\n"},
	}
	for _, c := range cases {
		if c.edits == nil {
			got, err := applyEdits("x\n- last", []fileEdit{{Op: "insert_after", Anchor: "last", Text: "- new"}})
			if err != nil || got != "x\n- last\n- new\n" {
				t.Fatalf("%s: got %q, %v", c.name, got, err)
			}
			continue
		}
		got, err := applyEdits(doc, c.edits)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got != c.want {
			t.Fatalf("%s:\ngot  %q\nwant %q", c.name, got, c.want)
		}
	}
}

func TestApplyEditsRefuses(t *testing.T) {
	const doc = "- a\n- b\n- a b\n"
	cases := []struct {
		name  string
		edits []fileEdit
		want  string
	}{
		{"old not found", []fileEdit{{Op: "replace", Old: "zzz", New: "y"}}, "old not found"},
		{"old not unique", []fileEdit{{Op: "replace", Old: "- a", New: "y"}}, "occurs 2 times"},
		{"anchor not unique", []fileEdit{{Op: "insert_after", Anchor: "b", Text: "y"}}, "occurs 2 times"},
		{"delete_lines matches nothing", []fileEdit{{Op: "delete_lines", Match: "zzz"}}, "no line contains"},
		{"unknown op", []fileEdit{{Op: "rewrite"}}, "unknown op"},
		{"no edits", []fileEdit{}, "edits is empty"},
		{"a later failure names its edit", []fileEdit{{Op: "append", Text: "y"}, {Op: "replace", Old: "zzz"}}, "edit 2 (replace)"},
	}
	for _, c := range cases {
		_, err := applyEdits(doc, c.edits)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: err = %v, want it to contain %q", c.name, err, c.want)
		}
	}
}

// TestMemoryEditKeepsFrontMatterAndIndex: an edit through the tool keeps the
// server-owned memd: block (created_at survives), keeps agent front matter,
// regenerates the folder's index.md, and writes nothing when an edit fails.
func TestMemoryEditKeepsFrontMatterAndIndex(t *testing.T) {
	s, conn, dirID := testBudgetServer(t)
	d := s.reg.DirectoryForConnector(&conn, dirID)
	const page = "---\ntype: note\ndescription: old summary\n---\n# Page\n\n- one\n"
	if err := d.Backend.Write("memory/page.md", []byte(page), ""); err != nil {
		t.Fatal(err)
	}
	root := d.Directory.LocalPath
	abs := filepath.Join(root, "memory", "page.md")
	seeded, _ := os.ReadFile(abs)
	// Age created_at so a rewrite that lost it would show.
	seeded = regexp.MustCompile(`created_at: \S+`).ReplaceAll(seeded, []byte("created_at: 2020-01-01"))
	if err := os.WriteFile(abs, seeded, 0o644); err != nil {
		t.Fatal(err)
	}

	call := func(edits []map[string]any) (string, bool) {
		args, _ := json.Marshal(map[string]any{"directory_id": dirID, "path": "memory/page.md", "edits": edits})
		return s.toolEdit(&conn, args)
	}
	text, isErr := call([]map[string]any{
		{"op": "replace", "old": "old summary", "new": "new summary"},
		{"op": "insert_after", "anchor": "- one", "text": "- two"},
	})
	if isErr {
		t.Fatalf("memory_edit: %s", text)
	}
	got, _ := os.ReadFile(abs)
	for _, want := range []string{"created_at: 2020-01-01", "type: note", "description: new summary", "- one\n- two\n"} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("edited file lacks %q:\n%s", want, got)
		}
	}
	index, _ := os.ReadFile(filepath.Join(root, "memory", "index.md"))
	if !strings.Contains(string(index), "new summary") {
		t.Fatalf("index.md not regenerated:\n%s", index)
	}

	before, _ := os.ReadFile(abs)
	if text, isErr := call([]map[string]any{
		{"op": "append", "text": "- three"},
		{"op": "replace", "old": "not there", "new": "x"},
	}); !isErr || !strings.Contains(text, "nothing was written") {
		t.Fatalf("failing edit: isErr=%v text=%q", isErr, text)
	}
	after, _ := os.ReadFile(abs)
	if string(before) != string(after) {
		t.Fatalf("a failed edit changed the file")
	}
}
