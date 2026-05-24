package storage

import (
	"strings"
	"testing"
)

func TestSearch_RanksFilenameAboveBody(t *testing.T) {
	l, _ := NewLocal(t.TempDir())
	if err := l.Write("alpha.md", []byte("# Alpha\nThis page mentions phoenix in the body.\n"), ""); err != nil {
		t.Fatal(err)
	}
	if err := l.Write("phoenix.md", []byte("# Heading\nUnrelated content here.\n"), ""); err != nil {
		t.Fatal(err)
	}

	hits, err := l.Search("phoenix", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) < 2 {
		t.Fatalf("expected hits in both pages, got %d: %+v", len(hits), hits)
	}
	if hits[0].Path != "phoenix.md" {
		t.Fatalf("filename match should rank above body match, got order: %v", paths(hits))
	}
}

func TestSearch_FuzzyMatchNonContiguous(t *testing.T) {
	l, _ := NewLocal(t.TempDir())
	if err := l.Write("phoenix-web-server.md", []byte("# Phx\nbody\n"), ""); err != nil {
		t.Fatal(err)
	}
	if err := l.Write("unrelated.md", []byte("# X\nplain content\n"), ""); err != nil {
		t.Fatal(err)
	}

	hits, _ := l.Search("phxweb", 10)
	if len(hits) == 0 || hits[0].Path != "phoenix-web-server.md" {
		t.Fatalf("expected fuzzy hit on phoenix-web-server.md, got: %+v", hits)
	}
}

func TestSearch_OneHitPerPage(t *testing.T) {
	l, _ := NewLocal(t.TempDir())
	body := "phoenix mention 1.\nirrelevant line.\nphoenix mention 2.\nphoenix mention 3.\n"
	if err := l.Write("multi.md", []byte("# Multi\n"+body), ""); err != nil {
		t.Fatal(err)
	}

	hits, _ := l.Search("phoenix", 10)
	count := 0
	for _, h := range hits {
		if h.Path == "multi.md" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected 1 hit per page, got %d: %+v", count, hits)
	}
}

func TestSearch_TitleMatch(t *testing.T) {
	l, _ := NewLocal(t.TempDir())
	if err := l.Write("page-xyz.md", []byte("# Concurrency Patterns\nbody text\n"), ""); err != nil {
		t.Fatal(err)
	}

	hits, _ := l.Search("concurrency", 10)
	if len(hits) == 0 || hits[0].Path != "page-xyz.md" {
		t.Fatalf("expected page-xyz.md via title match, got: %+v", hits)
	}
}

func TestSearch_TagMatch(t *testing.T) {
	l, _ := NewLocal(t.TempDir())
	content := []byte("---\ntags: [encryption, security]\n---\n# Untitled\nbody\n")
	if err := l.Write("tagged.md", content, ""); err != nil {
		t.Fatal(err)
	}

	hits, _ := l.Search("encryption", 10)
	if len(hits) == 0 || hits[0].Path != "tagged.md" {
		t.Fatalf("expected tagged.md via FM tag match, got: %+v", hits)
	}
}

func TestSearch_SnippetPrefersBodyMatch(t *testing.T) {
	l, _ := NewLocal(t.TempDir())
	if err := l.Write("p.md", []byte("# Heading\nFirst line.\nThe phoenix flies high.\nThird line.\n"), ""); err != nil {
		t.Fatal(err)
	}

	hits, _ := l.Search("phoenix", 10)
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	if !strings.Contains(strings.ToLower(hits[0].Snippet), "phoenix") {
		t.Fatalf("snippet should contain matched body line, got %q", hits[0].Snippet)
	}
	if hits[0].Line < 1 {
		t.Fatalf("Line should be set when snippet comes from body, got %d", hits[0].Line)
	}
}

func TestSearch_SnippetFallsBackToTitle(t *testing.T) {
	l, _ := NewLocal(t.TempDir())
	if err := l.Write("phoenix.md", []byte("# Bird Notes\nshort body\n"), ""); err != nil {
		t.Fatal(err)
	}

	// Query matches filename, not body. Snippet should fall back to the
	// title line ("Bird Notes") which is the first non-empty body line.
	hits, _ := l.Search("phoenix", 10)
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	if hits[0].Snippet == "" {
		t.Fatalf("snippet should not be empty: %+v", hits[0])
	}
}

func TestSearch_EmptyQueryErrors(t *testing.T) {
	l, _ := NewLocal(t.TempDir())
	if _, err := l.Search("   ", 10); err == nil {
		t.Fatalf("whitespace-only query should error")
	}
}

func TestSearch_ScoreIsSet(t *testing.T) {
	l, _ := NewLocal(t.TempDir())
	if err := l.Write("phoenix.md", []byte("# X\nbody\n"), ""); err != nil {
		t.Fatal(err)
	}
	hits, _ := l.Search("phoenix", 10)
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	if hits[0].Score <= 0 {
		t.Fatalf("expected positive score, got %d", hits[0].Score)
	}
}

func TestSearch_LimitRespected(t *testing.T) {
	l, _ := NewLocal(t.TempDir())
	for _, n := range []string{"a.md", "b.md", "c.md", "d.md", "e.md"} {
		if err := l.Write(n, []byte("# X\nphoenix in body\n"), ""); err != nil {
			t.Fatal(err)
		}
	}
	hits, _ := l.Search("phoenix", 3)
	if len(hits) != 3 {
		t.Fatalf("expected exactly 3 hits, got %d", len(hits))
	}
}

func paths(hs []Hit) []string {
	out := make([]string, len(hs))
	for i, h := range hs {
		out[i] = h.Path
	}
	return out
}
