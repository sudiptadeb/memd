package storage

import "time"

// Backend is a memory directory backend.
type Backend interface {
	// List returns every non-hidden regular file path inside the directory,
	// recursive. Used by search; not used for showing topology.
	List() ([]string, error)

	// ListPath returns the direct children at the given relative path. An empty
	// path or "." means the directory root. Used by the MCP server to render a
	// shallow topology of memory rather than a deep flat listing.
	ListPath(path string) ([]DirEntry, error)

	Read(path string) ([]byte, error)

	// ReadRaw returns the file bytes verbatim without touching the managed
	// `memd:` access stats. Used by the UI file viewer: a human peeking at a
	// file must not skew agent access counts or trigger backend writes.
	ReadRaw(path string) ([]byte, error)

	Write(path string, content []byte, message string) error

	// Move renames src to dst inside the directory. Both paths are
	// directory-relative; both are subject to traversal checks. Used by
	// reorganise so it can move a file (preserving git rename detection)
	// without leaving a duplicate behind. Returns an error if src does
	// not exist, dst already exists, or either path escapes the directory.
	Move(src, dst, message string) error

	// Delete removes a single file. Refuses to delete MEMORY.md at the
	// directory root. Path-traversal-safe.
	Delete(path, message string) error

	// DeleteFolder removes a folder and everything inside it (recursive).
	// Refuses to delete the directory root itself. Path-traversal-safe.
	DeleteFolder(path, message string) error

	Search(query string, limit int) ([]Hit, error)
	Status() Status

	// Flush forces any deferred writes (e.g. debounced git commits) to
	// complete. Local backends flush instantly on each write and so return
	// nil. Idempotent.
	Flush() error

	// Close releases backend resources. Implementations should flush before
	// closing.
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
