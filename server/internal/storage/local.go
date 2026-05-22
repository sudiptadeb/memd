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
	"time"
)

// Local is a memory backend backed by a plain folder of Markdown files.
type Local struct {
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

func (l *Local) Read(path string) ([]byte, error) {
	abs, err := l.resolve(path)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(abs)
}

func (l *Local) Write(path string, content []byte, _ string) error {
	abs, err := l.resolve(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	return os.WriteFile(abs, content, 0o644)
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
	return os.WriteFile(filepath.Join(l.root, "MEMORY.md"), []byte(body), 0o644)
}

// starterMemoryMD returns the body for an empty-directory MEMORY.md stub.
func starterMemoryMD(description string, now time.Time) string {
	return fmt.Sprintf(`---
last_reorganised: %s
entries: 0
limit: 30
---

# %s

Short index. Each line below should be one link to a page under `+"`memory/`"+` plus a one-line summary.

_(no memory yet — populate as durable knowledge accrues)_
`, now.Format("2006-01-02"), description)
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
