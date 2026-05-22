package storage

import "time"

// Backend is a memory directory backend.
type Backend interface {
	// List returns every Markdown file path inside the directory, recursive.
	// Used by search; not used for showing topology.
	List() ([]string, error)

	// ListPath returns the direct children at the given relative path. An empty
	// path or "." means the directory root. Used by the MCP server to render a
	// shallow topology of memory rather than a deep flat listing.
	ListPath(path string) ([]DirEntry, error)

	Read(path string) ([]byte, error)
	Write(path string, content []byte, message string) error
	Search(query string, limit int) ([]Hit, error)
	Status() Status
	Close() error
}

// DirEntry is a single child of a directory path.
type DirEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
}

// Hit is a single search result.
type Hit struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Snippet string `json:"snippet"`
}

// Status describes backend health and last sync state.
type Status struct {
	Backend   string    `json:"backend"`
	Path      string    `json:"path"`
	LastSync  time.Time `json:"last_sync"`
	LastError string    `json:"last_error,omitempty"`
}
