package storage

import (
	"strings"
	"testing"
	"time"
)

func TestParsePage_NoFrontMatter(t *testing.T) {
	raw := []byte("# Hello\n\nbody text\n")
	p := ParsePage(raw)
	if p.HasFM {
		t.Fatalf("HasFM = true, want false")
	}
	if !p.Stats.IsZero() {
		t.Fatalf("Stats = %+v, want zero", p.Stats)
	}
	if string(p.Body) != string(raw) {
		t.Fatalf("Body = %q, want %q", p.Body, raw)
	}
}

func TestParsePage_OnlyMemdBlock(t *testing.T) {
	raw := []byte(`---
memd:
  created_at: 2026-04-10
  updated_at: 2026-05-22
  last_read_at: 2026-05-23
  access_count: 17
---

# Hello
body
`)
	p := ParsePage(raw)
	if !p.HasFM {
		t.Fatalf("HasFM = false, want true")
	}
	if p.Stats.AccessCount != 17 {
		t.Fatalf("AccessCount = %d, want 17", p.Stats.AccessCount)
	}
	if p.Stats.CreatedAt.Format(dateFmt) != "2026-04-10" {
		t.Fatalf("CreatedAt = %v, want 2026-04-10", p.Stats.CreatedAt)
	}
	if p.AgentFM != "" {
		t.Fatalf("AgentFM = %q, want empty", p.AgentFM)
	}
	if !strings.HasPrefix(string(p.Body), "\n# Hello") {
		t.Fatalf("Body = %q, want to start with \\n# Hello", p.Body)
	}
}

func TestParsePage_AgentFMPreserved(t *testing.T) {
	raw := []byte(`---
memd:
  created_at: 2026-04-10
  updated_at: 2026-05-22
  last_read_at: 2026-05-23
  access_count: 3
topic: dlp
priority: load-bearing
tags:
  - scanner
  - performance
---
# Body
`)
	p := ParsePage(raw)
	if !strings.Contains(p.AgentFM, "topic: dlp") {
		t.Fatalf("AgentFM missing 'topic: dlp': %q", p.AgentFM)
	}
	if !strings.Contains(p.AgentFM, "tags:") {
		t.Fatalf("AgentFM missing tags block: %q", p.AgentFM)
	}
	if !strings.Contains(p.AgentFM, "  - scanner") {
		t.Fatalf("AgentFM missing indented tag: %q", p.AgentFM)
	}
}

func TestRender_Roundtrip(t *testing.T) {
	p := Page{
		Stats: MemdStats{
			CreatedAt:   mustDate(t, "2026-04-10"),
			UpdatedAt:   mustDate(t, "2026-05-22"),
			LastReadAt:  mustDate(t, "2026-05-23"),
			AccessCount: 17,
		},
		AgentFM: "topic: dlp\npriority: high\n",
		HasFM:   true,
		Body:    []byte("\n# Hello\nbody\n"),
	}
	out := p.Render()
	p2 := ParsePage(out)
	if p2.Stats != p.Stats {
		t.Fatalf("stats roundtrip lost: got %+v want %+v", p2.Stats, p.Stats)
	}
	if !strings.Contains(p2.AgentFM, "topic: dlp") {
		t.Fatalf("agent FM lost: %q", p2.AgentFM)
	}
	if string(p2.Body) != string(p.Body) {
		t.Fatalf("body roundtrip lost: got %q want %q", p2.Body, p.Body)
	}
}

func TestRenderHTML_Roundtrip(t *testing.T) {
	p := Page{
		Stats: MemdStats{
			CreatedAt:   mustDate(t, "2026-04-10"),
			UpdatedAt:   mustDate(t, "2026-05-22"),
			LastReadAt:  mustDate(t, "2026-05-23"),
			AccessCount: 17,
		},
		AgentFM: "topic: mock-ui\n",
		HasFM:   true,
		Body:    []byte("<!doctype html><title>Mock</title>\n"),
	}
	out := p.RenderHTML()
	if !strings.HasPrefix(string(out), "<!--\n---\nmemd:\n") {
		t.Fatalf("html render missing leading comment front matter:\n%s", out)
	}
	p2 := ParseHTMLPage(out)
	if p2.Stats != p.Stats {
		t.Fatalf("stats roundtrip lost: got %+v want %+v", p2.Stats, p.Stats)
	}
	if !strings.Contains(p2.AgentFM, "topic: mock-ui") {
		t.Fatalf("agent FM lost: %q", p2.AgentFM)
	}
	if string(p2.Body) != string(p.Body) {
		t.Fatalf("body roundtrip lost: got %q want %q", p2.Body, p.Body)
	}
}

func TestStripAgentMemdSubtree(t *testing.T) {
	raw := []byte(`---
memd:
  created_at: 2099-01-01
  access_count: 9999
topic: dlp
---
# Body
`)
	stripped := StripAgentMemdSubtree(raw)
	p := ParsePage(stripped)
	if !p.Stats.IsZero() {
		t.Fatalf("stripped Stats should be zero, got %+v", p.Stats)
	}
	if !strings.Contains(p.AgentFM, "topic: dlp") {
		t.Fatalf("agent FM lost: %q", p.AgentFM)
	}
}

func TestStripAgentMemdSubtree_NoFM(t *testing.T) {
	raw := []byte("# Hello\n")
	out := StripAgentMemdSubtree(raw)
	if string(out) != string(raw) {
		t.Fatalf("no-FM strip should be no-op, got %q", out)
	}
}

func TestRender_BodyWithoutLeadingNewline(t *testing.T) {
	p := Page{
		Stats: MemdStats{CreatedAt: mustDate(t, "2026-01-01")},
		Body:  []byte("# Body\n"),
	}
	out := string(p.Render())
	if !strings.Contains(out, "---\n\n# Body") {
		t.Fatalf("expected blank line between FM close and body, got:\n%s", out)
	}
}

func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(dateFmt, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return v
}
