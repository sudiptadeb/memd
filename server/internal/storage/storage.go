package storage

import "time"

// Backend is a memory directory backend.
type Backend interface {
	List() ([]string, error)
	Read(path string) ([]byte, error)
	Write(path string, content []byte, message string) error
	Search(query string, limit int) ([]Hit, error)
	Status() Status
	Close() error
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
