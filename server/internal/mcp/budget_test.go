package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/sudiptadeb/memd/server/internal/config"
	"github.com/sudiptadeb/memd/server/internal/registry"
)

// testBudgetServer builds an in-memory server with a single writable local
// directory and returns the server, a connector with access, and the directory
// id (for memory_write args).
func testBudgetServer(t *testing.T) (*Server, config.Connector, string) {
	t.Helper()
	dir := t.TempDir()
	reg := registry.NewEphemeral()
	dirID, err := reg.AddDirectory(config.Directory{Name: "test", Description: "test memory", Backend: "local", LocalPath: dir})
	if err != nil {
		t.Fatalf("AddDirectory: %v", err)
	}
	conn, err := reg.AddConnector(config.Connector{Name: "mcp", Kind: config.ConnectorKindMCP, DirectoryIDs: []string{dirID}, Write: true})
	if err != nil {
		t.Fatalf("AddConnector: %v", err)
	}
	t.Cleanup(func() { _ = reg.Close() })
	return newTestServer(reg), conn, dirID
}

// seedMemory writes MEMORY.md to the connector's directory via the backend. The
// local backend injects a managed `memd:` front-matter block on write (and bumps
// volatile stats such as access_count on every read), so tests read the stored
// body back themselves and normalise the volatile fields before comparing.
func seedMemory(t *testing.T, s *Server, conn config.Connector, dirID, body string) {
	t.Helper()
	d := s.reg.DirectoryForConnector(&conn, dirID)
	if d == nil {
		t.Fatalf("directory %s not accessible", dirID)
	}
	if err := d.Backend.Write("MEMORY.md", []byte(body), "seed"); err != nil {
		t.Fatalf("seed MEMORY.md: %v", err)
	}
}

// readMemory returns the stored MEMORY.md body via the backend. Note: reading
// bumps the managed access_count stat, so callers normalise before comparing.
func readMemory(t *testing.T, s *Server, conn config.Connector, dirID string) string {
	t.Helper()
	d := s.reg.DirectoryForConnector(&conn, dirID)
	if d == nil {
		t.Fatalf("directory %s not accessible", dirID)
	}
	b, err := d.Backend.Read("MEMORY.md")
	if err != nil {
		t.Fatalf("read MEMORY.md: %v", err)
	}
	return string(b)
}

// normalizeStats blanks the volatile server-managed access_count line so two
// reads of the same body compare equal regardless of how many reads occurred.
func normalizeStats(body string) string {
	lines := strings.Split(body, "\n")
	for i, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), "access_count:") {
			lines[i] = "  access_count: <n>"
		}
	}
	return strings.Join(lines, "\n")
}

// fencedIndex returns the content of the ```markdown fence that holds MEMORY.md
// inside the active-memory section.
func fencedIndex(t *testing.T, section string) string {
	t.Helper()
	const open = "```markdown\n"
	i := strings.Index(section, open)
	if i < 0 {
		t.Fatalf("no markdown fence in section:\n%s", section)
	}
	rest := section[i+len(open):]
	j := strings.Index(rest, "\n```")
	if j < 0 {
		t.Fatalf("unterminated markdown fence in section:\n%s", section)
	}
	return rest[:j]
}

func writeArgs(t *testing.T, dirID, path, content string) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(map[string]string{"directory_id": dirID, "path": path, "content": content})
	if err != nil {
		t.Fatalf("marshal write args: %v", err)
	}
	return b
}

// (a) Small MEMORY.md preloads untruncated with no marker, byte-identical to
// the raw body.
func TestActiveMemorySmallNoTruncation(t *testing.T) {
	s, conn, dirID := testBudgetServer(t)
	seedMemory(t, s, conn, dirID, "# MEMORY\n\n- [notes](memory/notes.md): short index\n")

	section := s.activeMemorySection(&conn)
	if strings.Contains(section, "[memd: MEMORY.md truncated") {
		t.Fatalf("unexpected truncation marker for small index:\n%s", section)
	}
	// The fence holds the stored body verbatim (fencedIndex drops the single
	// trailing newline that closes the fence), so an untruncated index renders
	// byte-for-byte identical to the stored body (modulo the server-managed
	// access_count, which both reads bump independently).
	stored := readMemory(t, s, conn, dirID)
	got := normalizeStats(fencedIndex(t, section))
	want := normalizeStats(strings.TrimRight(stored, "\n"))
	if got != want {
		t.Fatalf("fenced index not byte-identical (modulo stats):\n got=%q\nwant=%q", got, want)
	}
}

// (b) >200 lines truncates: marker present and at most 200 content lines kept
// from the index inside the fence.
func TestActiveMemoryTruncatesByLines(t *testing.T) {
	s, conn, dirID := testBudgetServer(t)
	var sb strings.Builder
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&sb, "line %d\n", i)
	}
	// 500 short lines (plus a few front-matter lines) is well under 25KB, so the
	// 200-line budget trips first.
	seedMemory(t, s, conn, dirID, sb.String())

	section := s.activeMemorySection(&conn)
	if !strings.Contains(section, "[memd: MEMORY.md truncated at ") {
		t.Fatalf("expected truncation marker, got:\n%s", section)
	}
	idx := fencedIndex(t, section)
	// Count every kept line from the index except the marker; clampPreload keeps
	// exactly memoryIndexPreloadMaxLines lines of the stored body here.
	content := 0
	for _, ln := range strings.Split(idx, "\n") {
		if strings.HasPrefix(ln, "[memd: MEMORY.md truncated") {
			continue
		}
		content++
	}
	if content != memoryIndexPreloadMaxLines {
		t.Fatalf("kept %d index lines, want exactly %d", content, memoryIndexPreloadMaxLines)
	}
	// Marker must report the kept line count.
	wantMarker := fmt.Sprintf("[memd: MEMORY.md truncated at %d lines / 25KB for preload", memoryIndexPreloadMaxLines)
	if !strings.Contains(section, wantMarker) {
		t.Fatalf("marker line count wrong; want substring %q in:\n%s", wantMarker, section)
	}
}

// (c) >25KB with long lines truncates on a line boundary: marker present, and
// the kept body never splits a line.
func TestActiveMemoryTruncatesByBytesOnLineBoundary(t *testing.T) {
	s, conn, dirID := testBudgetServer(t)
	// 50 lines of ~1KB each = ~50KB, well over the 25KB byte budget but far
	// under the 200-line budget, so the byte limit is what trips.
	longLine := strings.Repeat("x", 1024)
	var sb strings.Builder
	for i := 0; i < 50; i++ {
		sb.WriteString(longLine)
		sb.WriteString("\n")
	}
	seedMemory(t, s, conn, dirID, sb.String())

	section := s.activeMemorySection(&conn)
	if !strings.Contains(section, "[memd: MEMORY.md truncated at ") {
		t.Fatalf("expected truncation marker, got byte-truncation without marker")
	}
	idx := fencedIndex(t, section)
	marker := strings.Index(idx, "\n[memd: MEMORY.md truncated")
	if marker < 0 {
		t.Fatalf("marker not found in fenced index")
	}
	kept := idx[:marker+1] // body portion, including its trailing newline
	if !strings.HasSuffix(kept, "\n") {
		t.Fatalf("kept body does not end on a line boundary: ...%q", kept[len(kept)-min(len(kept), 40):])
	}
	if len(kept) > memoryIndexPreloadMaxBytes {
		t.Fatalf("kept %d bytes, want <= %d", len(kept), memoryIndexPreloadMaxBytes)
	}
	// Line-boundary cut: the kept body is a prefix of the stored body (modulo the
	// volatile access_count both reads bump).
	stored := normalizeStats(readMemory(t, s, conn, dirID))
	if !strings.HasPrefix(stored, normalizeStats(kept)) {
		t.Fatalf("kept body is not a prefix of the stored body — content was altered")
	}
	// Each full kept long line must survive intact (no mid-line split).
	for _, ln := range strings.Split(strings.TrimRight(kept, "\n"), "\n") {
		if strings.HasPrefix(ln, "x") && ln != longLine {
			t.Fatalf("long line split mid-line: got %d bytes, want %d", len(ln), len(longLine))
		}
	}
}

// (d) toolWrite warns when content exceeds 100KB.
func TestToolWriteWarnsOverSize(t *testing.T) {
	s, conn, dirID := testBudgetServer(t)
	content := strings.Repeat("a", fileSizeWarnBytes+1)
	out, isErr := s.toolWrite(&conn, writeArgs(t, dirID, "big/file.md", content))
	if isErr {
		t.Fatalf("toolWrite errored: %s", out)
	}
	if !strings.Contains(out, "warning: file exceeds 100KB") {
		t.Fatalf("missing 100KB warning: %s", out)
	}
	if !strings.HasPrefix(out, fmt.Sprintf("wrote %d bytes to big/file.md", len(content))) {
		t.Fatalf("unexpected success prefix: %s", out)
	}
}

// (e) Writing a MEMORY.md over the preload budget warns about the budget.
func TestToolWriteWarnsMemoryOverBudget(t *testing.T) {
	s, conn, dirID := testBudgetServer(t)
	var sb strings.Builder
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&sb, "line %d\n", i)
	}
	content := sb.String()
	out, isErr := s.toolWrite(&conn, writeArgs(t, dirID, "MEMORY.md", content))
	if isErr {
		t.Fatalf("toolWrite errored: %s", out)
	}
	if !strings.Contains(out, "MEMORY.md exceeds the preload budget") {
		t.Fatalf("missing preload-budget warning: %s", out)
	}
	// A 300-line body under 100KB must NOT also trip the size warning.
	if strings.Contains(out, "file exceeds 100KB") {
		t.Fatalf("unexpected 100KB warning for sub-100KB MEMORY.md: %s", out)
	}
}

// (f) Small writes produce no warnings.
func TestToolWriteNoWarningOnSmall(t *testing.T) {
	s, conn, dirID := testBudgetServer(t)
	out, isErr := s.toolWrite(&conn, writeArgs(t, dirID, "MEMORY.md", "# small\n\n- one entry\n"))
	if isErr {
		t.Fatalf("toolWrite errored: %s", out)
	}
	if strings.Contains(out, "warning") {
		t.Fatalf("unexpected warning on small write: %s", out)
	}
	if out != "wrote 21 bytes to MEMORY.md" {
		t.Fatalf("unexpected success message: %q", out)
	}
}
