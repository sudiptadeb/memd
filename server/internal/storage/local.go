package storage

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Local is a memory backend backed by a plain folder of Markdown files.
// All operations that touch disk serialize through mu: one write at a time
// per directory. Reads also take mu because the read path bumps the
// per-page stats (last_read_at, access_count) in-place.
type Local struct {
	mu   sync.Mutex
	root string
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
	return &Local{root: abs}, nil
}

// Root returns the absolute root path.
func (l *Local) Root() string { return l.root }

func (l *Local) List() ([]string, error) {
	var out []string
	err := filepath.WalkDir(l.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != l.root && strings.HasPrefix(name, ".") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}
		rel, err := filepath.Rel(l.root, path)
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
		abs := filepath.Join(l.root, filepath.FromSlash(p))
		f, err := os.Open(abs)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		lineNo := 0
		for scanner.Scan() {
			lineNo++
			line := scanner.Text()
			if strings.Contains(strings.ToLower(line), q) {
				hits = append(hits, Hit{Path: p, Line: lineNo, Snippet: strings.TrimSpace(line)})
				if len(hits) >= limit {
					f.Close()
					return hits, nil
				}
			}
		}
		f.Close()
	}
	return hits, nil
}

func (l *Local) Status() Status {
	return Status{Backend: "local", Path: l.root, LastSync: time.Now()}
}

func (l *Local) Flush() error { return nil }

func (l *Local) Close() error { return nil }

// resolve maps a directory-relative path to an absolute path and rejects traversal.
func (l *Local) resolve(rel string) (string, error) {
	if rel == "" {
		return "", errors.New("empty path")
	}
	clean := filepath.Clean(filepath.Join(l.root, filepath.FromSlash(rel)))
	rootClean := filepath.Clean(l.root)
	if clean != rootClean && !strings.HasPrefix(clean, rootClean+string(filepath.Separator)) {
		return "", errors.New("path escapes directory")
	}
	return clean, nil
}

// EnsureIndex creates a starter MEMORY.md only when the directory has no
// Markdown content at its root. The stub carries the front matter the doctrine
// expects (last_reorganised, entries, limit) so future agents start from a
// well-formed index.
func (l *Local) EnsureIndex(description string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	entries, err := os.ReadDir(l.root)
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
	return atomicWriteFile(filepath.Join(l.root, "MEMORY.md"), []byte(body), 0o644)
}

// starterMemoryMD returns the body for an empty-directory MEMORY.md stub.
// The page carries both the server-managed memd: subtree (created via the
// Page renderer for consistency) and the agent-managed last_reorganised /
// entries / limit fields the reorganise prompt expects.
func starterMemoryMD(description string, now time.Time) string {
	date := now.Format("2006-01-02")
	agentFM := fmt.Sprintf("last_reorganised: %s\nentries: 0\nlimit: 30\n", date)
	body := fmt.Sprintf(`
# %s

Curated index. Pages live under `+"`memory/`"+`; this file is the map.

Group entries under thematic H2 sections (e.g. `+"`## Rules & Conventions`, `## Architecture Notes`, `## Lessons / Feedback`"+`). Each entry is one line: a link to a page plus a concrete one-line description of what the page contains. Curate, don't just list files.

_(no memory yet — populate as durable knowledge accrues)_
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
	abs := l.root
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
