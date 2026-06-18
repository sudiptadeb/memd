package storage

import (
	"bufio"
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

	"github.com/sudiptadeb/memd/server/internal/logs"
)

// Local is a memory backend backed by a plain folder of files.
// All operations that touch disk serialize through mu: one write at a time
// per directory. Reads also take mu because the read path bumps the
// managed file stats (last_read_at, access_count) in-place.
type Local struct {
	mu sync.Mutex
	// root is the absolute path as supplied. rootEval is root after symlink
	// evaluation. All containment checks compare against rootEval so a root
	// reached via a symlink (e.g. /tmp → /private/tmp on macOS) still works.
	root     string
	rootEval string

	// skipReadStats makes Read behave like ReadRaw: no last_read_at /
	// access_count bump, no write-back. Per-connector git branch clones set
	// this so read-only sessions don't dirty the branch with stat churn that
	// would pollute the eventual review diff.
	skipReadStats bool
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
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
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

// Read returns the file bytes. For managed metadata formats the server
// parses the `memd:` front matter subtree, updates last_read_at and
// access_count, writes the updated file back through to disk, and returns
// the rendered bytes (so the agent sees the current stats). Other files
// pass through untouched.
// ReadRaw returns the file bytes exactly as stored, without updating the
// managed access stats or writing anything back.
func (l *Local) ReadRaw(path string) ([]byte, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	abs, err := l.resolve(path)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(abs)
}

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
	if l.skipReadStats {
		return data, nil
	}
	p, managed := parseManagedPage(path, data)
	if !managed {
		return data, nil
	}
	now := today()
	// For legacy files (no memd: block yet), seed timestamps from the file's
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
	}.renderForPath(path)
	_ = atomicWriteFile(abs, out, 0o644)
	return out, nil
}

// Write persists the file. For managed metadata formats, the agent's `memd:`
// subtree (if any) in the payload is discarded; the server merges its own
// authoritative stats with whatever existed on disk. created_at is set on
// first write; updated_at is bumped every write; last_read_at and
// access_count are preserved. Unmanaged files are stored verbatim.
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
	if !hasManagedMetadataPath(path) {
		return atomicWriteFile(abs, content, 0o644)
	}

	var existing Page
	if old, err := os.ReadFile(abs); err == nil {
		existing, _ = parseManagedPage(path, old)
	}

	incoming, _ := parseManagedPage(path, content)
	now := today()
	stats := MemdStats{
		CreatedAt:   existing.Stats.CreatedAt,
		UpdatedAt:   now,
		LastReadAt:  existing.Stats.LastReadAt,
		AccessCount: existing.Stats.AccessCount,
	}
	if stats.CreatedAt.IsZero() {
		stats.CreatedAt = now
	}
	out := Page{
		Stats:   stats,
		AgentFM: incoming.AgentFM,
		HasFM:   true,
		Body:    incoming.Body,
	}.renderForPath(path)
	return atomicWriteFile(abs, out, 0o644)
}

func isMarkdownPath(p string) bool {
	return strings.HasSuffix(strings.ToLower(p), ".md")
}

func isHTMLPath(p string) bool {
	lower := strings.ToLower(p)
	return strings.HasSuffix(lower, ".html") || strings.HasSuffix(lower, ".htm")
}

func hasManagedMetadataPath(p string) bool {
	return isMarkdownPath(p) || isHTMLPath(p)
}

func parseManagedPage(path string, data []byte) (Page, bool) {
	switch {
	case isMarkdownPath(path):
		return ParsePage(data), true
	case isHTMLPath(path):
		return ParseHTMLPage(data), true
	default:
		return Page{}, false
	}
}

func (p Page) renderForPath(path string) []byte {
	if isHTMLPath(path) {
		return p.RenderHTML()
	}
	return p.Render()
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
	q := strings.ToLower(query)
	var hits []Hit
	for _, p := range paths {
		abs := filepath.Join(l.rootEval, filepath.FromSlash(p))
		fileHits, done := l.searchFile(abs, p, q, limit-len(hits))
		hits = append(hits, fileHits...)
		if done {
			break
		}
	}
	return hits, nil
}

// searchFile scans one file for q, returning up to remaining hits. It opens the
// file once (sniffing for binary content from the same buffered reader) and
// returns done=true when the caller's overall limit has been reached.
func (l *Local) searchFile(abs, p, q string, remaining int) (hits []Hit, done bool) {
	if remaining <= 0 {
		return nil, true
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, false
	}
	defer f.Close()

	br := bufio.NewReaderSize(f, 64*1024)
	// Binary sniff: a NUL byte in the first 8 KiB means "not text".
	if head, _ := br.Peek(8192); bytes.IndexByte(head, 0) >= 0 {
		return nil, false
	}

	scanner := bufio.NewScanner(br)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if strings.Contains(strings.ToLower(line), q) {
			hits = append(hits, Hit{Path: p, Line: lineNo, Snippet: strings.TrimSpace(line)})
			if len(hits) >= remaining {
				return hits, true
			}
		}
	}
	if err := scanner.Err(); err != nil {
		// Don't silently drop the rest of the file (e.g. a line over the 1 MiB
		// token limit); surface it so the gap is visible.
		logs.Warn("search: scanning %s stopped early: %v", p, err)
	}
	return hits, false
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
// Markdown content at its root. The stub carries the front matter the
// doctrine expects (last_reorganised, entries, limit) so future agents
// start from a well-formed index.
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
// The file carries both the server-managed memd: subtree (created via the
// Page renderer for consistency) and the agent-managed last_reorganised /
// entries / limit fields the reorganise prompt expects.
//
// The stub is intentionally neutral about folder shape — the layout is
// decided by the agent during the first `harvest` pass once it sees what
// the content actually clusters into.
func starterMemoryMD(description string, now time.Time) string {
	date := now.Format("2006-01-02")
	agentFM := fmt.Sprintf("last_reorganised: %s\nentries: 0\nlimit: 50\n", date)
	body := fmt.Sprintf(`
# %s

This is the curated index. Detailed files live in top-level folders below — Markdown pages, standalone HTML mockups, CSV tables, JSON examples, and other text artifacts are all valid. The shape is up to the agent (single `+"`memory/`"+` for general directories; multiple folders like `+"`notes/`, `projects/`, `preferences/`, `mockups/`, `data/`"+` when content splits naturally).

Group entries under thematic H2 sections. Each entry is one line: a link to the file plus a concrete one-line description of what's in it. Curate, don't just list files.

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
