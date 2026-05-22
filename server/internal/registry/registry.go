package registry

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/sudiptadeb/memd/server/internal/config"
	"github.com/sudiptadeb/memd/server/internal/storage"
	"github.com/sudiptadeb/memd/server/internal/token"
)

// Connector aliases the on-disk type so callers don't need both imports.
type Connector = config.Connector

// DirectoryView pairs a directory's config with its open backend.
type DirectoryView struct {
	Directory config.Directory
	Backend   storage.Backend
}

// Registry holds directories + connectors and the open backends behind them.
type Registry struct {
	mu         sync.RWMutex
	cfg        *config.Config
	persistent bool
	backends   map[string]storage.Backend // directory ID → backend
}

// NewPersistent loads config.json from disk and opens every directory's backend.
// Backends that fail to open are skipped (logged to stderr); the registry is
// still usable for the remaining directories.
func NewPersistent() (*Registry, error) {
	c, err := config.Load()
	if err != nil {
		return nil, err
	}
	r := &Registry{cfg: c, persistent: true, backends: map[string]storage.Backend{}}
	for _, d := range c.Directories {
		b, err := r.openBackend(d)
		if err != nil {
			fmt.Fprintf(os.Stderr, "memd: directory %q failed to open: %v\n", d.Name, err)
			continue
		}
		r.backends[d.ID] = b
	}
	return r, nil
}

// NewEphemeral creates an in-memory registry that does not persist to disk.
// Used by quick mode.
func NewEphemeral() *Registry {
	return &Registry{
		cfg:      &config.Config{},
		backends: map[string]storage.Backend{},
	}
}

func (r *Registry) openBackend(d config.Directory) (storage.Backend, error) {
	switch d.Backend {
	case "local":
		l, err := storage.NewLocal(d.LocalPath)
		if err != nil {
			return nil, err
		}
		if err := l.EnsureIndex(d.Description); err != nil {
			return nil, err
		}
		return l, nil
	case "git":
		if d.Git == nil {
			return nil, errors.New("git directory missing config")
		}
		workdirs, err := config.WorkdirsRoot()
		if err != nil {
			return nil, err
		}
		g, err := storage.NewGit(storage.GitConfig{
			WorkDir:       filepath.Join(workdirs, d.ID),
			RemoteURL:     d.Git.RemoteURL,
			Branch:        d.Git.Branch,
			BasePath:      d.Git.BasePath,
			AuthorName:    d.Git.AuthorName,
			AuthorEmail:   d.Git.AuthorEmail,
			SSHKeyPath:    d.Git.SSHKeyPath,
			WaitForWrites: parseDurationOrZero(d.Git.WaitForWrites),
			SaveEvery:     parseDurationOrZero(d.Git.SaveEvery),
		})
		if err != nil {
			return nil, err
		}
		if err := g.EnsureIndex(d.Description); err != nil {
			return nil, err
		}
		return g, nil
	}
	return nil, fmt.Errorf("unknown backend %q", d.Backend)
}

func (r *Registry) save() error {
	if !r.persistent {
		return nil
	}
	return config.Save(r.cfg)
}

// --- Reads ---

func (r *Registry) Directories() []config.Directory {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]config.Directory, len(r.cfg.Directories))
	copy(out, r.cfg.Directories)
	return out
}

func (r *Registry) Connectors() []config.Connector {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]config.Connector, len(r.cfg.Connectors))
	copy(out, r.cfg.Connectors)
	return out
}

// ConnectorByToken returns the connector with this token, or nil.
func (r *Registry) ConnectorByToken(tok string) *Connector {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for i := range r.cfg.Connectors {
		if r.cfg.Connectors[i].Token == tok {
			c := r.cfg.Connectors[i]
			return &c
		}
	}
	return nil
}

// DirectoriesForConnector returns the directories this connector can access.
func (r *Registry) DirectoriesForConnector(c *Connector) []DirectoryView {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []DirectoryView
	for _, id := range c.DirectoryIDs {
		for _, d := range r.cfg.Directories {
			if d.ID != id {
				continue
			}
			b := r.backends[id]
			if b == nil {
				continue
			}
			out = append(out, DirectoryView{Directory: d, Backend: b})
		}
	}
	return out
}

// DirectoryForConnector returns one specific directory if the connector has access.
func (r *Registry) DirectoryForConnector(c *Connector, id string) *DirectoryView {
	for _, dv := range r.DirectoriesForConnector(c) {
		if dv.Directory.ID == id {
			d := dv
			return &d
		}
	}
	return nil
}

// --- Mutations ---

func (r *Registry) AddDirectory(d config.Directory) (string, error) {
	if d.ID == "" {
		d.ID = newID()
	}
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now()
	}
	b, err := r.openBackend(d)
	if err != nil {
		return "", err
	}
	r.mu.Lock()
	r.cfg.Directories = append(r.cfg.Directories, d)
	r.backends[d.ID] = b
	r.mu.Unlock()
	if err := r.save(); err != nil {
		return "", err
	}
	return d.ID, nil
}

func (r *Registry) DeleteDirectory(id string) error {
	r.mu.Lock()
	idx := -1
	for i, d := range r.cfg.Directories {
		if d.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		r.mu.Unlock()
		return errors.New("directory not found")
	}
	if b := r.backends[id]; b != nil {
		_ = b.Close()
	}
	delete(r.backends, id)
	r.cfg.Directories = append(r.cfg.Directories[:idx], r.cfg.Directories[idx+1:]...)
	for i := range r.cfg.Connectors {
		r.cfg.Connectors[i].DirectoryIDs = removeString(r.cfg.Connectors[i].DirectoryIDs, id)
	}
	r.mu.Unlock()
	return r.save()
}

func (r *Registry) AddConnector(c config.Connector) (config.Connector, error) {
	if c.ID == "" {
		c.ID = newID()
	}
	if c.Token == "" {
		tok, err := token.New()
		if err != nil {
			return config.Connector{}, err
		}
		c.Token = tok
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now()
	}
	r.mu.Lock()
	r.cfg.Connectors = append(r.cfg.Connectors, c)
	r.mu.Unlock()
	if err := r.save(); err != nil {
		return config.Connector{}, err
	}
	return c, nil
}

func (r *Registry) DeleteConnector(id string) error {
	r.mu.Lock()
	idx := -1
	for i, c := range r.cfg.Connectors {
		if c.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		r.mu.Unlock()
		return errors.New("connector not found")
	}
	r.cfg.Connectors = append(r.cfg.Connectors[:idx], r.cfg.Connectors[idx+1:]...)
	r.mu.Unlock()
	return r.save()
}

func removeString(list []string, target string) []string {
	out := list[:0]
	for _, v := range list {
		if v != target {
			out = append(out, v)
		}
	}
	return out
}

// Close flushes and closes every open backend. Safe to call once at server
// shutdown.
func (r *Registry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, b := range r.backends {
		_ = b.Close()
		delete(r.backends, id)
	}
	return nil
}

// parseDurationOrZero returns the parsed duration or zero (which downstream
// callers interpret as "use the default").
func parseDurationOrZero(s string) time.Duration {
	if s == "" {
		return 0
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0
	}
	return d
}

func newID() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 8)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return string(b)
}
