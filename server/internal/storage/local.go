package storage

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sahilm/fuzzy"
)

// Local is a memory backend backed by a plain folder of Markdown files.
// All operations that touch disk serialize through mu: one write at a time
// per directory. Reads also take mu because the read path bumps the
// per-page stats (last_read_at, access_count) in-place.
type Local struct {
	mu sync.Mutex
	// root is the absolute path as supplied. rootEval is root after symlink
	// evaluation. All containment checks compare against rootEval so a root
	// reached via a symlink (e.g. /tmp → /private/tmp on macOS) still works.
	root     string
	rootEval string
}

func NewLocal(root string) (*Local, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("not a directory: %s", abs)
	}
	eval, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	return &Local{root: abs, rootEval: eval}, nil
}

// Root returns the absolute root path.
func (l *Local) Root() string { return l.root }

func (l *Local) List() ([]string, error) {
	var out []string
	err := filepath.WalkDir(l.rootEval, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != l.rootEval && strings.HasPrefix(name, ".") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}
		rel, err := filepath.Rel(l.rootEval, path)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	return out, err
}

// Read returns the page bytes after bumping its server-managed stats. For
// Markdown pages the server parses the `memd:` front matter subtree, updates
// last_read_at and access_count, writes the updated page back through to
// disk, and returns the rendered bytes (so the agent sees the current
// stats). Non-Markdown files pass through untouched.
func (l *Local) Read(path string) ([]byte, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	abs, err := l.resolve(path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	if !isMarkdownPath(path) {
		return data, nil
	}
	p := ParsePage(data)
	now := today()
	// For legacy pages (no memd: block yet), seed timestamps from the file's
	// mtime so the migration is roughly accurate. Today is the fallback if
	// stat fails.
	if p.Stats.CreatedAt.IsZero() || p.Stats.UpdatedAt.IsZero() {
		seed := now
		if info, err := os.Stat(abs); err == nil {
			t := info.ModTime().UTC()
			seed = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		}
		if p.Stats.CreatedAt.IsZero() {
			p.Stats.CreatedAt = seed
		}
		if p.Stats.UpdatedAt.IsZero() {
			p.Stats.UpdatedAt = seed
		}
	}
	p.Stats.LastReadAt = now
	p.Stats.AccessCount++
	out := Page{
		Stats:   p.Stats,
		AgentFM: p.AgentFM,
		HasFM:   true,
		Body:    p.Body,
	}.Render()
	_ = atomicWriteFile(abs, out, 0o644)
	return out, nil
}

// Write persists the page. The agent's `memd:` subtree (if any) in the
// payload is discarded; the server merges its own authoritative stats with
// whatever existed on disk. created_at is set on first write; updated_at is
// bumped every write; last_read_at and access_count are preserved.
func (l *Local) Write(path string, content []byte, _ string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	abs, err := l.resolve(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	if !isMarkdownPath(path) {
		return atomicWriteFile(abs, content, 0o644)
	}

	var existing MemdStats
	if old, err := os.ReadFile(abs); err == nil {
		existing = ParsePage(old).Stats
	}

	incoming := ParsePage(content)
	now := today()
	stats := MemdStats{
		CreatedAt:   existing.CreatedAt,
		UpdatedAt:   now,
		LastReadAt:  existing.LastReadAt,
		AccessCount: existing.AccessCount,
	}
	if stats.CreatedAt.IsZero() {
		stats.CreatedAt = now
	}
	out := Page{
		Stats:   stats,
		AgentFM: incoming.AgentFM,
		HasFM:   true,
		Body:    incoming.Body,
	}.Render()
	return atomicWriteFile(abs, out, 0o644)
}

func isMarkdownPath(p string) bool {
	return strings.HasSuffix(strings.ToLower(p), ".md")
}

// Move renames src to dst. Refuses to overwrite an existing dst.
// MEMORY.md at the root cannot be moved (it's the required index).
func (l *Local) Move(src, dst, _ string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if isRootMemoryIndex(src) {
		return errors.New("cannot move MEMORY.md at the directory root")
	}
	srcAbs, err := l.resolve(src)
	if err != nil {
		return err
	}
	if _, err := os.Stat(srcAbs); err != nil {
		return err
	}
	dstAbs, err := l.resolve(dst)
	if err != nil {
		return err
	}
	if _, err := os.Stat(dstAbs); err == nil {
		return fmt.Errorf("destination already exists: %s", dst)
	}
	if err := os.MkdirAll(filepath.Dir(dstAbs), 0o755); err != nil {
		return err
	}
	return os.Rename(srcAbs, dstAbs)
}

// Delete removes a single file. Refuses to delete MEMORY.md at the
// directory root.
func (l *Local) Delete(path, _ string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if isRootMemoryIndex(path) {
		return errors.New("cannot delete MEMORY.md at the directory root")
	}
	abs, err := l.resolve(path)
	if err != nil {
		return err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory; use DeleteFolder", path)
	}
	return os.Remove(abs)
}

// DeleteFolder removes a folder and everything inside it. Refuses to
// delete the directory root itself.
func (l *Local) DeleteFolder(path, _ string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if path == "" || path == "." || path == "/" {
		return errors.New("cannot delete the directory root")
	}
	abs, err := l.resolve(path)
	if err != nil {
		return err
	}
	if abs == l.rootEval {
		return errors.New("cannot delete the directory root")
	}
	info, err := os.Stat(abs)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is a file; use Delete", path)
	}
	return os.RemoveAll(abs)
}

// isRootMemoryIndex reports whether the path refers to MEMORY.md at
// the directory root (the one path Delete/Move refuse to touch).
func isRootMemoryIndex(p string) bool {
	clean := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(p)), "./")
	return clean == "MEMORY.md"
}

// bodyHaystackBytes is how much of each page body feeds into the fuzzy
// haystack. Path, title, and tags appear first so they outrank body-only
// matches; the body cap keeps very long pages from blowing up the cost
// of a single search call.
const bodyHaystackBytes = 4096

// Search returns one Hit per matched page, sorted by relevance (best
// first). Matching is fuzzy — characters of the query must appear in
// the page's searchable surface (path, title, tags, body prefix) in
// order, but not contiguously. Scoring rewards matches near word
// boundaries and earlier positions; since path/title/tags sit at the
// start of the haystack, filename matches outrank body-only matches
// for free.
func (l *Local) Search(query string, limit int) ([]Hit, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("empty query")
	}
	if limit <= 0 {
		limit = 50
	}
	paths, err := l.List()
	if err != nil {
		return nil, err
	}

	docs := make([]searchDoc, 0, len(paths))
	for _, p := range paths {
		abs := filepath.Join(l.rootEval, filepath.FromSlash(p))
		raw, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		page := ParsePage(raw)
		body := page.Body
		bodyPrefix := body
		if len(bodyPrefix) > bodyHaystackBytes {
			bodyPrefix = bodyPrefix[:bodyHaystackBytes]
		}
		title := firstHeading(body)
		haystack := p + "\n" + title + "\n" + page.AgentFM + "\n" + string(bodyPrefix)
		docs = append(docs, searchDoc{path: p, title: title, body: body, haystack: haystack})
	}

	matches := fuzzy.FindFrom(query, searchDocs(docs))
	hits := make([]Hit, 0, len(matches))
	for _, m := range matches {
		if len(hits) >= limit {
			break
		}
		d := docs[m.Index]
		snippet, lineNo := bestBodyLine(d.body, query)
		if snippet == "" {
			snippet = d.title
		}
		if snippet == "" {
			snippet = d.path
		}
		hits = append(hits, Hit{Path: d.path, Line: lineNo, Snippet: snippet, Score: m.Score})
	}
	return hits, nil
}

type searchDoc struct {
	path     string
	title    string
	body     []byte
	haystack string
}

// searchDocs adapts a []searchDoc to fuzzy.Source.
type searchDocs []searchDoc

func (s searchDocs) Len() int             { return len(s) }
func (s searchDocs) String(i int) string  { return s[i].haystack }

// firstHeading returns the first Markdown H1/H2 line in body (with the
// leading #s stripped), or "" if none.
func firstHeading(body []byte) string {
	for _, raw := range bytes.Split(body, []byte("\n")) {
		line := strings.TrimRight(string(raw), "\r")
		trim := strings.TrimLeft(line, "#")
		if len(trim) < len(line) && len(trim) > 0 && trim[0] == ' ' {
			return strings.TrimSpace(trim)
		}
	}
	return ""
}

// bestBodyLine picks a body line to display as the snippet for a hit.
// Prefers a line that contains the query as a fuzzy subsequence (so the
// snippet shows the user *why* it matched). Falls back to the first
// non-empty body line. Returns 0 for line when no match was found.
func bestBodyLine(body []byte, query string) (string, int) {
	q := strings.ToLower(strings.TrimSpace(query))
	firstNonEmpty := ""
	firstNonEmptyLine := 0
	lineNo := 0
	for _, raw := range bytes.Split(body, []byte("\n")) {
		lineNo++
		line := strings.TrimSpace(string(raw))
		if line == "" {
			continue
		}
		if firstNonEmpty == "" {
			firstNonEmpty = line
			firstNonEmptyLine = lineNo
		}
		if subsequenceFold(line, q) {
			return line, lineNo
		}
	}
	return firstNonEmpty, firstNonEmptyLine
}

// subsequenceFold reports whether every rune of needle appears in
// haystack in order (case-insensitive).
func subsequenceFold(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	hi := 0
	hs := strings.ToLower(haystack)
	for _, r := range needle {
		idx := strings.IndexRune(hs[hi:], r)
		if idx < 0 {
			return false
		}
		hi += idx + 1
	}
	return true
}

func (l *Local) Status() Status {
	return Status{Backend: "local", Path: l.root, LastSync: time.Now()}
}

func (l *Local) Flush() error { return nil }

func (l *Local) Close() error { return nil }

// resolve maps a directory-relative path to an absolute path that's safe
// to open. Each Local is the reader for one directory; its sole
// invariant is that every path it returns lives under rootEval after
// full resolution.
//
// One algorithm covers every form of escape (`../..`, an absolute
// `/etc/passwd`, a symlink to outside, a chain of symlinks):
//
//  1. Build the candidate absolute path. filepath.Clean folds `.` and
//     `..` lexically and Join discards a leading slash on rel.
//  2. Resolve symlinks. For non-existent leaves (new pages) we recurse
//     to the deepest existing ancestor and re-append the missing tail.
//  3. Check the fully-resolved path is under rootEval.
//
// All I/O happens against the resolved path, so we never re-traverse
// the symlinks we already checked.
func (l *Local) resolve(rel string) (string, error) {
	if rel == "" {
		return "", errors.New("empty path")
	}
	target := filepath.Clean(filepath.Join(l.rootEval, filepath.FromSlash(rel)))
	real, err := evalSymlinksAllowMissing(target)
	if err != nil {
		return "", err
	}
	if !pathInside(l.rootEval, real) {
		return "", errors.New("path escapes directory")
	}
	return real, nil
}

// pathInside reports whether p is root or lives beneath it.
func pathInside(root, p string) bool {
	return p == root || strings.HasPrefix(p, root+string(filepath.Separator))
}

// evalSymlinksAllowMissing is filepath.EvalSymlinks that tolerates a
// non-existent leaf (or chain of non-existent ancestors), so callers can
// resolve "the path a new file will live at". The deepest existing
// ancestor is resolved with EvalSymlinks; the missing tail is appended.
func evalSymlinksAllowMissing(p string) (string, error) {
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	parent := filepath.Dir(p)
	if parent == p {
		return "", fmt.Errorf("cannot resolve %s", p)
	}
	parentReal, err := evalSymlinksAllowMissing(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(parentReal, filepath.Base(p)), nil
}

// EnsureIndex creates a starter MEMORY.md only when the directory has no
// Markdown content at its root. The stub carries the front matter the doctrine
// expects (last_reorganised, entries, limit) so future agents start from a
// well-formed index.
func (l *Local) EnsureIndex(description string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	entries, err := os.ReadDir(l.rootEval)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			return nil
		}
	}
	if description == "" {
		description = "Memory"
	}
	body := starterMemoryMD(description, time.Now())
	return atomicWriteFile(filepath.Join(l.rootEval, "MEMORY.md"), []byte(body), 0o644)
}

// starterMemoryMD returns the body for an empty-directory MEMORY.md stub.
// The page carries both the server-managed memd: subtree (created via the
// Page renderer for consistency) and the agent-managed last_reorganised /
// entries / limit fields the reorganise prompt expects.
//
// The stub is intentionally neutral about folder shape — the layout is
// decided by the agent during the first `harvest` pass once it sees what
// the content actually clusters into.
func starterMemoryMD(description string, now time.Time) string {
	date := now.Format("2006-01-02")
	agentFM := fmt.Sprintf("last_reorganised: %s\nentries: 0\nlimit: 30\n", date)
	body := fmt.Sprintf(`
# %s

This is the curated index. Detailed pages live in top-level folders below — the shape is up to the agent (single `+"`memory/`"+` for general directories; multiple folders like `+"`notes/`, `projects/`, `preferences/`"+` when content splits naturally).

Group entries under thematic H2 sections. Each entry is one line: a link to the page plus a concrete one-line description of what's in it. Curate, don't just list files.

_(no memory yet — run a `+"`harvest`"+` pass to seed this from your existing sources, or write directly as durable knowledge accrues)_
`, description)
	stats := MemdStats{
		CreatedAt:  now,
		UpdatedAt:  now,
		LastReadAt: now,
	}
	return string(Page{Stats: stats, AgentFM: agentFM, HasFM: true, Body: []byte(body)}.Render())
}

// ListPath returns the direct children at the given path inside the directory.
// Hidden entries (dotfiles, .git, etc.) are skipped. An empty path or "." means
// the directory root. Folders sort before files; both sort case-insensitively.
func (l *Local) ListPath(rel string) ([]DirEntry, error) {
	abs := l.rootEval
	if rel != "" && rel != "." {
		clean, err := l.resolve(rel)
		if err != nil {
			return nil, err
		}
		abs = clean
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("not a directory: %s", rel)
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	out := make([]DirEntry, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		p := name
		if rel != "" && rel != "." {
			p = strings.TrimSuffix(filepath.ToSlash(rel), "/") + "/" + name
		}
		out = append(out, DirEntry{Name: name, Path: p, IsDir: e.IsDir()})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir // dirs first
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}
