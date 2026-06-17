package graph

import (
	"testing"

	"github.com/sudiptadeb/memd/server/internal/storage"
)

func TestBuild_EdgesOrphansBroken(t *testing.T) {
	dir := t.TempDir()
	l, _ := storage.NewLocal(dir)

	// hub links to a.md (exists) and missing.md (broken).
	_ = l.Write("hub.md", []byte("---\ntype: index\ntitle: Hub\n---\n\n# Hub\n[a](memory/a.md) and [gone](memory/missing.md)\n"), "")
	_ = l.Write("memory/a.md", []byte("---\ntitle: Alpha\n---\n\n# Alpha\nback to [hub](../hub.md)\n"), "")
	_ = l.Write("memory/lonely.md", []byte("# Lonely\nno links here\n"), "")

	g, err := Build(l)
	if err != nil {
		t.Fatal(err)
	}

	// node titles come from front matter / H1
	titles := map[string]string{}
	for _, n := range g.Nodes {
		titles[n.Path] = n.Title
	}
	if titles["memory/a.md"] != "Alpha" {
		t.Errorf("title from front matter wrong: %q", titles["memory/a.md"])
	}
	if titles["memory/lonely.md"] != "Lonely" {
		t.Errorf("title should fall back to H1: %q", titles["memory/lonely.md"])
	}

	// lonely.md is an orphan
	foundOrphan := false
	for _, o := range g.Orphans {
		if o == "memory/lonely.md" {
			foundOrphan = true
		}
	}
	if !foundOrphan {
		t.Errorf("memory/lonely.md should be an orphan, orphans=%v", g.Orphans)
	}

	// one broken edge to memory/missing.md
	if len(g.Broken) != 1 || g.Broken[0].To != "memory/missing.md" {
		t.Errorf("expected one broken edge to memory/missing.md, got %v", g.Broken)
	}

	// neighbours of hub
	out, _ := g.Neighbors("hub.md")
	if len(out) != 2 {
		t.Errorf("hub should have 2 outbound links, got %v", out)
	}
}

func TestBuild_SkipsGeneratedIndex(t *testing.T) {
	dir := t.TempDir()
	l, _ := storage.NewLocal(dir)
	b := storage.WrapIndexed(l)
	_ = b.Write("memory/a.md", []byte("# A\n"), "")

	g, err := Build(b)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range g.Nodes {
		if n.Path == "memory/index.md" || n.Path == "index.md" {
			t.Errorf("generated index.md should not be a graph node: %s", n.Path)
		}
	}
}
