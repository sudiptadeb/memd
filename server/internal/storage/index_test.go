package storage

import (
	"strings"
	"testing"
)

func TestIndexed_GeneratesIndexOnWrite(t *testing.T) {
	dir := t.TempDir()
	l, _ := NewLocal(dir)
	b := WrapIndexed(l)

	content := "---\ntype: runbook\ntitle: Deploy Gateway\ndescription: How to ship the gateway.\n---\n\n# Deploy\n"
	if err := b.Write("memory/architecture/deploy.md", []byte(content), ""); err != nil {
		t.Fatal(err)
	}

	// The file's own directory gets an index listing it...
	idx, err := b.ReadRaw("memory/architecture/index.md")
	if err != nil {
		t.Fatalf("expected memory/architecture/index.md: %v", err)
	}
	s := string(idx)
	if !strings.Contains(s, "type: index") || !strings.Contains(s, "okf: generated") {
		t.Errorf("index front matter missing OKF markers:\n%s", s)
	}
	if !strings.Contains(s, "[Deploy Gateway](deploy.md)") {
		t.Errorf("index should link the file by its title:\n%s", s)
	}
	if !strings.Contains(s, "`runbook`") || !strings.Contains(s, "How to ship the gateway.") {
		t.Errorf("index should show type and description:\n%s", s)
	}

	// ...and every ancestor up to the root lists the sub-folder.
	rootIdx, err := b.ReadRaw("index.md")
	if err != nil {
		t.Fatalf("expected root index.md: %v", err)
	}
	if !strings.Contains(string(rootIdx), "[memory/](memory/index.md)") {
		t.Errorf("root index should link the memory/ folder:\n%s", string(rootIdx))
	}
}

func TestIndexed_DoesNotTouchMemoryMD(t *testing.T) {
	dir := t.TempDir()
	l, _ := NewLocal(dir)
	b := WrapIndexed(l)

	curated := "---\nentries: 0\n---\n\n# Curated index\n\nHand written.\n"
	if err := b.Write("MEMORY.md", []byte(curated), ""); err != nil {
		t.Fatal(err)
	}
	if err := b.Write("note.md", []byte("# Note\n"), ""); err != nil {
		t.Fatal(err)
	}
	got, _ := b.ReadRaw("MEMORY.md")
	if !strings.Contains(string(got), "Hand written.") {
		t.Errorf("MEMORY.md must stay curated, got:\n%s", string(got))
	}
	// MEMORY.md is listed in the generated index but never regenerated.
	idx, _ := b.ReadRaw("index.md")
	if !strings.Contains(string(idx), "MEMORY.md") {
		t.Errorf("generated index should list MEMORY.md:\n%s", string(idx))
	}
}

func TestIndexed_DeleteRefreshesIndex(t *testing.T) {
	dir := t.TempDir()
	l, _ := NewLocal(dir)
	b := WrapIndexed(l)
	_ = b.Write("memory/a.md", []byte("# A\n"), "")
	_ = b.Write("memory/b.md", []byte("# B\n"), "")
	if err := b.Delete("memory/a.md", ""); err != nil {
		t.Fatal(err)
	}
	idx, _ := b.ReadRaw("memory/index.md")
	if strings.Contains(string(idx), "(a.md)") {
		t.Errorf("deleted file should be gone from index:\n%s", string(idx))
	}
	if !strings.Contains(string(idx), "(b.md)") {
		t.Errorf("remaining file should still be in index:\n%s", string(idx))
	}
}

func TestFieldsFromAgentFM(t *testing.T) {
	fm := "type: note\ntitle: Hello\ntags: [a, b]\nnested:\n  k: v\ndescription: \"quoted desc\"\n"
	f := FieldsFromAgentFM(fm)
	if f["type"] != "note" || f["title"] != "Hello" {
		t.Errorf("scalar parse wrong: %#v", f)
	}
	if f["description"] != "quoted desc" {
		t.Errorf("quotes should be trimmed: %q", f["description"])
	}
	if _, ok := f["nested"]; ok {
		t.Errorf("nested block key should be skipped: %#v", f)
	}
}

func TestExtractMarkdownLinks(t *testing.T) {
	body := []byte("See [x](a/b.md) and [y](../c.md#frag) and [ext](https://e.com) and [anch](#top).")
	got := ExtractMarkdownLinks(body)
	want := map[string]bool{"a/b.md": true, "../c.md": true}
	if len(got) != 2 {
		t.Fatalf("want 2 local links, got %v", got)
	}
	for _, g := range got {
		if !want[g] {
			t.Errorf("unexpected link %q", g)
		}
	}
}
