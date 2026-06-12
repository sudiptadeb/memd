package registry

import (
	"context"
	crand "crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sudiptadeb/memd/server/internal/account"
	"github.com/sudiptadeb/memd/server/internal/config"
	"github.com/sudiptadeb/memd/server/internal/logs"
	"github.com/sudiptadeb/memd/server/internal/storage"
	"github.com/sudiptadeb/memd/server/internal/token"
)

// Connector aliases the on-disk type so callers don't need both imports.
type Connector = config.Connector

// DirectoryView pairs a directory's config with its open backend.
//
// CanWrite reports whether the resolving connector may mutate this directory.
// It is true when the connector's owner owns the directory, or when the
// directory is team-shared and the owner holds a write-capable team role
// (owner/admin/member). Viewers get read-only team access.
type DirectoryView struct {
	Directory config.Directory
	Backend   storage.Backend
	CanWrite  bool
}

// Registry holds directories + connectors and the open backends behind them.
type Registry struct {
	mu         sync.RWMutex
	cfg        *config.Config
	persistent bool
	accounts   *account.Store
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
			logs.Error("directory %q failed to open: %v", d.Name, err)
			continue
		}
		r.backends[backendKey(d.OwnerUserID, d.ID)] = b
	}
	return r, nil
}

// NewAccountBacked loads user-owned directories/connectors from the SQL
// account store. Configured mode uses this; config.json is a legacy import
// source only.
func NewAccountBacked(ctx context.Context, accounts *account.Store) (*Registry, error) {
	r := &Registry{cfg: &config.Config{}, accounts: accounts, backends: map[string]storage.Backend{}}
	users, err := accounts.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	for _, user := range users {
		dirs, err := accounts.ListUserDirectories(ctx, user.ID)
		if err != nil {
			return nil, err
		}
		for _, d := range dirs {
			r.cfg.Directories = append(r.cfg.Directories, d)
			b, err := r.openBackend(d)
			if err != nil {
				logs.Error("directory %q failed to open: %v", d.Name, err)
				continue
			}
			r.backends[backendKey(d.OwnerUserID, d.ID)] = b
		}
		connectors, err := accounts.ListUserConnectors(ctx, user.ID)
		if err != nil {
			return nil, err
		}
		r.cfg.Connectors = append(r.cfg.Connectors, connectors...)
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

// resolveLocalPath applies the local-directory policy. A caller-supplied path
// is honoured only when allowCustom is true; otherwise (and whenever no path is
// given) memd creates and uses a sandboxed directory at
// <ManagedLocalRoot>/<ownerUserID>/<dirID>.
func resolveLocalPath(d *config.Directory, allowCustom bool) error {
	custom := strings.TrimSpace(d.LocalPath)
	if custom != "" {
		if !allowCustom {
			return errors.New("choosing a local directory path is only available for local accounts; create a name-only directory or use a git backend instead")
		}
		d.LocalPath = custom
		return nil
	}
	root, err := config.ManagedLocalRoot()
	if err != nil {
		return err
	}
	if !safePathSegment(d.OwnerUserID) || !safePathSegment(d.ID) {
		return fmt.Errorf("cannot derive a managed directory path")
	}
	dir := filepath.Join(root, d.OwnerUserID, d.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	d.LocalPath = dir
	return nil
}

// safePathSegment reports whether s is a single, traversal-free path segment.
// Owner and directory IDs are server-generated and always satisfy this; the
// check is a guard against any future ID format change.
func safePathSegment(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	return !strings.ContainsAny(s, `/\`) && !strings.Contains(s, "..")
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
			AuthUsername:  d.Git.AuthUsername,
			AuthToken:     d.Git.AuthToken,
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
	for i := range r.cfg.Connectors {
		out[i] = cloneConnector(r.cfg.Connectors[i])
	}
	return out
}

// cloneConnector returns a copy whose DirectoryIDs has its own backing array,
// so callers can't observe (or race with) later in-place registry mutations.
func cloneConnector(c config.Connector) config.Connector {
	c.DirectoryIDs = append([]string(nil), c.DirectoryIDs...)
	return c
}

// ConnectorByToken returns the connector with this token, or nil.
func (r *Registry) ConnectorByToken(tok string) *Connector {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for i := range r.cfg.Connectors {
		if subtle.ConstantTimeCompare([]byte(r.cfg.Connectors[i].Token), []byte(tok)) == 1 {
			c := cloneConnector(r.cfg.Connectors[i])
			return &c
		}
	}
	return nil
}

// DirectoriesForConnector returns the directories this connector can access.
//
// A connector reaches a directory when its owner owns the directory, or when
// the directory is team-shared with a team the owner can view. This lets a team
// member point their own connector at a team directory owned by a teammate;
// each member keeps a distinct connector (and token), so the activity log
// attributes every operation to whoever acted. Write access is reported per
// directory via DirectoryView.CanWrite.
func (r *Registry) DirectoriesForConnector(c *Connector) []DirectoryView {
	viewTeams, writeTeams, _ := r.teamAccessForUser(c.OwnerUserID)
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []DirectoryView
	for _, id := range c.DirectoryIDs {
		for _, d := range r.cfg.Directories {
			if d.ID != id {
				continue
			}
			owned := d.OwnerUserID == c.OwnerUserID
			if !owned && (d.TeamID == "" || !viewTeams[d.TeamID]) {
				continue
			}
			b := r.backends[backendKey(d.OwnerUserID, id)]
			if b == nil {
				continue
			}
			out = append(out, DirectoryView{
				Directory: d,
				Backend:   b,
				CanWrite:  owned || writeTeams[d.TeamID],
			})
			break
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
	// Trusted/internal caller (e.g. quick mode): a custom local path is allowed.
	return r.addDirectory(d.OwnerUserID, d, true)
}

// AddDirectoryForUser adds a directory on behalf of a trusted caller, allowing
// a custom local path. Hosted UI flows should use AddDirectoryForUserManaged.
func (r *Registry) AddDirectoryForUser(ownerUserID string, d config.Directory) (string, error) {
	return r.addDirectory(ownerUserID, d, true)
}

// AddDirectoryForUserManaged adds a directory subject to the hosted policy.
// When allowCustomLocalPath is false, a caller-supplied local path is rejected
// and a name-only local directory is sandboxed under a per-user managed root.
func (r *Registry) AddDirectoryForUserManaged(ownerUserID string, d config.Directory, allowCustomLocalPath bool) (string, error) {
	return r.addDirectory(ownerUserID, d, allowCustomLocalPath)
}

func (r *Registry) addDirectory(ownerUserID string, d config.Directory, allowCustomLocalPath bool) (string, error) {
	if r.accounts != nil {
		if err := r.accounts.EnsureUserDataOwner(context.Background(), ownerUserID); err != nil {
			return "", err
		}
		if d.TeamID != "" {
			ok, err := r.accounts.CanManageTeamData(context.Background(), d.TeamID, ownerUserID)
			if err != nil {
				return "", err
			}
			if !ok {
				return "", account.ErrForbidden
			}
		}
	}
	d.OwnerUserID = ownerUserID
	if d.ID == "" {
		d.ID = newID()
	}
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now()
	}
	if d.Backend == "local" {
		if err := resolveLocalPath(&d, allowCustomLocalPath); err != nil {
			return "", err
		}
	}
	b, err := r.openBackend(d)
	if err != nil {
		return "", err
	}
	r.mu.Lock()
	r.cfg.Directories = append(r.cfg.Directories, d)
	r.backends[backendKey(d.OwnerUserID, d.ID)] = b
	r.mu.Unlock()
	if r.accounts != nil {
		if err := r.accounts.UpsertUserDirectory(context.Background(), ownerUserID, d); err != nil {
			return "", err
		}
	} else if err := r.save(); err != nil {
		return "", err
	}
	return d.ID, nil
}

func (r *Registry) DeleteDirectory(id string) error {
	return r.DeleteDirectoryForUser("", id)
}

func (r *Registry) DeleteDirectoryForUser(ownerUserID, id string) error {
	r.mu.Lock()
	idx := -1
	for i, d := range r.cfg.Directories {
		if d.ID == id && d.OwnerUserID == ownerUserID {
			idx = i
			break
		}
	}
	if idx < 0 {
		r.mu.Unlock()
		return errors.New("directory not found")
	}
	if b := r.backends[backendKey(ownerUserID, id)]; b != nil {
		_ = b.Close()
	}
	delete(r.backends, backendKey(ownerUserID, id))
	r.cfg.Directories = append(r.cfg.Directories[:idx], r.cfg.Directories[idx+1:]...)
	for i := range r.cfg.Connectors {
		if r.cfg.Connectors[i].OwnerUserID == ownerUserID {
			r.cfg.Connectors[i].DirectoryIDs = removeString(r.cfg.Connectors[i].DirectoryIDs, id)
		}
	}
	r.mu.Unlock()
	if r.accounts != nil {
		return r.accounts.DeleteUserDirectory(context.Background(), ownerUserID, id)
	}
	return r.save()
}

func (r *Registry) UpdateDirectoryTeamForActor(actorUserID, id, teamID string) (config.Directory, error) {
	if r.accounts != nil {
		if err := r.accounts.EnsureUserDataOwner(context.Background(), actorUserID); err != nil {
			return config.Directory{}, err
		}
		if teamID != "" {
			ok, err := r.accounts.CanManageTeamData(context.Background(), teamID, actorUserID)
			if err != nil {
				return config.Directory{}, err
			}
			if !ok {
				return config.Directory{}, account.ErrForbidden
			}
		}
	}
	_, _, manageTeams := r.teamAccessForUser(actorUserID)
	r.mu.Lock()
	idx := -1
	for i, d := range r.cfg.Directories {
		if d.ID != id {
			continue
		}
		if d.OwnerUserID == actorUserID || (d.TeamID != "" && manageTeams[d.TeamID]) {
			idx = i
			break
		}
	}
	if idx < 0 {
		r.mu.Unlock()
		return config.Directory{}, errors.New("directory not found")
	}
	current := r.cfg.Directories[idx]
	if current.OwnerUserID != actorUserID && (current.TeamID == "" || !manageTeams[current.TeamID]) {
		r.mu.Unlock()
		return config.Directory{}, account.ErrForbidden
	}
	if current.TeamID != "" && current.TeamID != teamID && !manageTeams[current.TeamID] {
		r.mu.Unlock()
		return config.Directory{}, account.ErrForbidden
	}
	current.TeamID = teamID
	r.cfg.Directories[idx] = current
	r.mu.Unlock()
	if r.accounts != nil {
		if err := r.accounts.UpsertUserDirectory(context.Background(), current.OwnerUserID, current); err != nil {
			return config.Directory{}, err
		}
	} else if err := r.save(); err != nil {
		return config.Directory{}, err
	}
	return current, nil
}

func (r *Registry) DeleteDirectoryForActor(actorUserID, id string) error {
	if r.accounts != nil {
		if err := r.accounts.EnsureUserDataOwner(context.Background(), actorUserID); err != nil {
			return err
		}
	}
	_, _, manageTeams := r.teamAccessForUser(actorUserID)
	r.mu.Lock()
	idx := -1
	var ownerUserID string
	for i, d := range r.cfg.Directories {
		if d.ID != id {
			continue
		}
		if d.OwnerUserID == actorUserID || (d.TeamID != "" && manageTeams[d.TeamID]) {
			idx = i
			ownerUserID = d.OwnerUserID
			break
		}
	}
	if idx < 0 {
		r.mu.Unlock()
		return errors.New("directory not found")
	}
	if b := r.backends[backendKey(ownerUserID, id)]; b != nil {
		_ = b.Close()
	}
	delete(r.backends, backendKey(ownerUserID, id))
	r.cfg.Directories = append(r.cfg.Directories[:idx], r.cfg.Directories[idx+1:]...)
	for i := range r.cfg.Connectors {
		if r.cfg.Connectors[i].OwnerUserID == ownerUserID {
			r.cfg.Connectors[i].DirectoryIDs = removeString(r.cfg.Connectors[i].DirectoryIDs, id)
		}
	}
	r.mu.Unlock()
	if r.accounts != nil {
		return r.accounts.DeleteUserDirectory(context.Background(), ownerUserID, id)
	}
	return r.save()
}

func (r *Registry) AddConnector(c config.Connector) (config.Connector, error) {
	return r.AddConnectorForUser(c.OwnerUserID, c)
}

func (r *Registry) AddConnectorForUser(ownerUserID string, c config.Connector) (config.Connector, error) {
	if r.accounts != nil {
		if err := r.accounts.EnsureUserDataOwner(context.Background(), ownerUserID); err != nil {
			return config.Connector{}, err
		}
		if c.TeamID != "" {
			ok, err := r.accounts.CanManageTeamData(context.Background(), c.TeamID, ownerUserID)
			if err != nil {
				return config.Connector{}, err
			}
			if !ok {
				return config.Connector{}, account.ErrForbidden
			}
		}
	}
	c.OwnerUserID = ownerUserID
	c.Kind = config.NormalizeConnectorKind(c.Kind)
	if c.Kind != config.ConnectorKindMCP && c.Kind != config.ConnectorKindHTTP {
		return config.Connector{}, fmt.Errorf("unknown connector kind: %s", c.Kind)
	}
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
	viewTeams, writeTeams, _ := r.teamAccessForUser(ownerUserID)
	r.mu.Lock()
	if r.accounts != nil || len(r.cfg.Directories) > 0 {
		if err := r.validateConnectorDirectoriesLocked(ownerUserID, c.TeamID, c.DirectoryIDs, c.Write, viewTeams, writeTeams); err != nil {
			r.mu.Unlock()
			return config.Connector{}, err
		}
	}
	r.cfg.Connectors = append(r.cfg.Connectors, c)
	r.mu.Unlock()
	if r.accounts != nil {
		if err := r.accounts.UpsertUserConnector(context.Background(), ownerUserID, c); err != nil {
			return config.Connector{}, err
		}
	} else if err := r.save(); err != nil {
		return config.Connector{}, err
	}
	return c, nil
}

// UpdateConnector edits a connector's name, kind, directory access, and
// write flag. The token, ID, and creation time are preserved.
// Returns the updated connector.
func (r *Registry) UpdateConnector(id, name, kind string, directoryIDs []string, write bool) (config.Connector, error) {
	return r.UpdateConnectorForUser("", id, name, kind, directoryIDs, write)
}

func (r *Registry) UpdateConnectorForUser(ownerUserID, id, name, kind string, directoryIDs []string, write bool) (config.Connector, error) {
	if r.accounts != nil {
		if err := r.accounts.EnsureUserDataOwner(context.Background(), ownerUserID); err != nil {
			return config.Connector{}, err
		}
	}
	if name == "" {
		return config.Connector{}, errors.New("name is required")
	}
	if len(directoryIDs) == 0 {
		return config.Connector{}, errors.New("at least one directory is required")
	}
	kind = config.NormalizeConnectorKind(kind)
	if kind != config.ConnectorKindMCP && kind != config.ConnectorKindHTTP {
		return config.Connector{}, fmt.Errorf("unknown connector kind: %s", kind)
	}
	viewTeams, writeTeams, _ := r.teamAccessForUser(ownerUserID)
	r.mu.Lock()
	idx := -1
	for i, c := range r.cfg.Connectors {
		if c.ID == id && c.OwnerUserID == ownerUserID {
			idx = i
			break
		}
	}
	if idx < 0 {
		r.mu.Unlock()
		return config.Connector{}, errors.New("connector not found")
	}
	if err := r.validateConnectorDirectoriesLocked(ownerUserID, r.cfg.Connectors[idx].TeamID, directoryIDs, write, viewTeams, writeTeams); err != nil {
		r.mu.Unlock()
		return config.Connector{}, err
	}
	r.cfg.Connectors[idx].Name = name
	r.cfg.Connectors[idx].Kind = kind
	r.cfg.Connectors[idx].DirectoryIDs = append([]string(nil), directoryIDs...)
	r.cfg.Connectors[idx].Write = write
	updated := r.cfg.Connectors[idx]
	r.mu.Unlock()
	if r.accounts != nil {
		if err := r.accounts.UpsertUserConnector(context.Background(), ownerUserID, updated); err != nil {
			return config.Connector{}, err
		}
	} else if err := r.save(); err != nil {
		return config.Connector{}, err
	}
	return updated, nil
}

func (r *Registry) UpdateConnectorForActor(actorUserID, id, name, kind string, directoryIDs []string, write bool, teamID string) (config.Connector, error) {
	if r.accounts != nil {
		if err := r.accounts.EnsureUserDataOwner(context.Background(), actorUserID); err != nil {
			return config.Connector{}, err
		}
		if teamID != "" {
			ok, err := r.accounts.CanManageTeamData(context.Background(), teamID, actorUserID)
			if err != nil {
				return config.Connector{}, err
			}
			if !ok {
				return config.Connector{}, account.ErrForbidden
			}
		}
	}
	if name == "" {
		return config.Connector{}, errors.New("name is required")
	}
	if len(directoryIDs) == 0 {
		return config.Connector{}, errors.New("at least one directory is required")
	}
	kind = config.NormalizeConnectorKind(kind)
	if kind != config.ConnectorKindMCP && kind != config.ConnectorKindHTTP {
		return config.Connector{}, fmt.Errorf("unknown connector kind: %s", kind)
	}
	viewTeams, writeTeams, manageTeams := r.teamAccessForUser(actorUserID)
	r.mu.Lock()
	idx := -1
	for i, c := range r.cfg.Connectors {
		if c.ID != id {
			continue
		}
		if c.OwnerUserID == actorUserID || (c.TeamID != "" && manageTeams[c.TeamID]) {
			idx = i
			break
		}
	}
	if idx < 0 {
		r.mu.Unlock()
		return config.Connector{}, errors.New("connector not found")
	}
	current := r.cfg.Connectors[idx]
	if current.OwnerUserID != actorUserID && (current.TeamID == "" || !manageTeams[current.TeamID]) {
		r.mu.Unlock()
		return config.Connector{}, account.ErrForbidden
	}
	if current.TeamID != "" && current.TeamID != teamID && !manageTeams[current.TeamID] {
		r.mu.Unlock()
		return config.Connector{}, account.ErrForbidden
	}
	if err := r.validateConnectorDirectoriesLocked(current.OwnerUserID, teamID, directoryIDs, write, viewTeams, writeTeams); err != nil {
		r.mu.Unlock()
		return config.Connector{}, err
	}
	current.Name = name
	current.Kind = kind
	current.TeamID = teamID
	current.DirectoryIDs = append([]string(nil), directoryIDs...)
	current.Write = write
	r.cfg.Connectors[idx] = current
	r.mu.Unlock()
	if r.accounts != nil {
		if err := r.accounts.UpsertUserConnector(context.Background(), current.OwnerUserID, current); err != nil {
			return config.Connector{}, err
		}
	} else if err := r.save(); err != nil {
		return config.Connector{}, err
	}
	return current, nil
}

// RotateConnector replaces the connector's token with a fresh one. The
// previous token stops authenticating immediately (any agent using it
// will need the new URL). Returns the updated connector.
func (r *Registry) RotateConnector(id string) (config.Connector, error) {
	return r.RotateConnectorForUser("", id)
}

func (r *Registry) RotateConnectorForUser(ownerUserID, id string) (config.Connector, error) {
	if r.accounts != nil {
		if err := r.accounts.EnsureUserDataOwner(context.Background(), ownerUserID); err != nil {
			return config.Connector{}, err
		}
	}
	tok, err := token.New()
	if err != nil {
		return config.Connector{}, err
	}
	r.mu.Lock()
	idx := -1
	for i, c := range r.cfg.Connectors {
		if c.ID == id && c.OwnerUserID == ownerUserID {
			idx = i
			break
		}
	}
	if idx < 0 {
		r.mu.Unlock()
		return config.Connector{}, errors.New("connector not found")
	}
	r.cfg.Connectors[idx].Token = tok
	updated := r.cfg.Connectors[idx]
	r.mu.Unlock()
	if r.accounts != nil {
		if err := r.accounts.UpsertUserConnector(context.Background(), ownerUserID, updated); err != nil {
			return config.Connector{}, err
		}
	} else if err := r.save(); err != nil {
		return config.Connector{}, err
	}
	return updated, nil
}

func (r *Registry) RotateConnectorForActor(actorUserID, id string) (config.Connector, error) {
	if r.accounts != nil {
		if err := r.accounts.EnsureUserDataOwner(context.Background(), actorUserID); err != nil {
			return config.Connector{}, err
		}
	}
	tok, err := token.New()
	if err != nil {
		return config.Connector{}, err
	}
	_, _, manageTeams := r.teamAccessForUser(actorUserID)
	r.mu.Lock()
	idx := -1
	for i, c := range r.cfg.Connectors {
		if c.ID != id {
			continue
		}
		if c.OwnerUserID == actorUserID || (c.TeamID != "" && manageTeams[c.TeamID]) {
			idx = i
			break
		}
	}
	if idx < 0 {
		r.mu.Unlock()
		return config.Connector{}, errors.New("connector not found")
	}
	r.cfg.Connectors[idx].Token = tok
	updated := r.cfg.Connectors[idx]
	r.mu.Unlock()
	if r.accounts != nil {
		if err := r.accounts.UpsertUserConnector(context.Background(), updated.OwnerUserID, updated); err != nil {
			return config.Connector{}, err
		}
	} else if err := r.save(); err != nil {
		return config.Connector{}, err
	}
	return updated, nil
}

func (r *Registry) DeleteConnector(id string) error {
	return r.DeleteConnectorForUser("", id)
}

func (r *Registry) DeleteConnectorForUser(ownerUserID, id string) error {
	r.mu.Lock()
	idx := -1
	for i, c := range r.cfg.Connectors {
		if c.ID == id && c.OwnerUserID == ownerUserID {
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
	if r.accounts != nil {
		return r.accounts.DeleteUserConnector(context.Background(), ownerUserID, id)
	}
	return r.save()
}

func (r *Registry) DeleteConnectorForActor(actorUserID, id string) error {
	if r.accounts != nil {
		if err := r.accounts.EnsureUserDataOwner(context.Background(), actorUserID); err != nil {
			return err
		}
	}
	_, _, manageTeams := r.teamAccessForUser(actorUserID)
	r.mu.Lock()
	idx := -1
	var ownerUserID string
	for i, c := range r.cfg.Connectors {
		if c.ID != id {
			continue
		}
		if c.OwnerUserID == actorUserID || (c.TeamID != "" && manageTeams[c.TeamID]) {
			idx = i
			ownerUserID = c.OwnerUserID
			break
		}
	}
	if idx < 0 {
		r.mu.Unlock()
		return errors.New("connector not found")
	}
	r.cfg.Connectors = append(r.cfg.Connectors[:idx], r.cfg.Connectors[idx+1:]...)
	r.mu.Unlock()
	if r.accounts != nil {
		return r.accounts.DeleteUserConnector(context.Background(), ownerUserID, id)
	}
	return r.save()
}

func (r *Registry) ImportUserData(ownerUserID string, bundle account.UserDataBundle, replace bool) error {
	if r.accounts == nil {
		return errors.New("registry is not account-backed")
	}
	if err := r.accounts.ImportUserData(context.Background(), ownerUserID, bundle, replace); err != nil {
		return err
	}
	newBackends := make(map[string]storage.Backend, len(bundle.Directories))
	for _, d := range bundle.Directories {
		d.OwnerUserID = ownerUserID
		d.TeamID = ""
		b, err := r.openBackend(d)
		if err != nil {
			logs.Error("imported directory %q failed to open: %v", d.Name, err)
			continue
		}
		newBackends[backendKey(ownerUserID, d.ID)] = b
	}
	connectors := make([]config.Connector, len(bundle.Connectors))
	for i, c := range bundle.Connectors {
		c.OwnerUserID = ownerUserID
		c.TeamID = ""
		connectors[i] = c
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if replace {
		r.replaceUserDataLocked(ownerUserID, bundle.Directories, connectors, newBackends)
		return nil
	}

	for _, d := range bundle.Directories {
		d.OwnerUserID = ownerUserID
		d.TeamID = ""
		key := backendKey(ownerUserID, d.ID)
		found := false
		for i, existing := range r.cfg.Directories {
			if existing.OwnerUserID == ownerUserID && existing.ID == d.ID {
				if old := r.backends[key]; old != nil {
					_ = old.Close()
				}
				r.cfg.Directories[i] = d
				if b := newBackends[key]; b != nil {
					r.backends[key] = b
				} else {
					delete(r.backends, key)
				}
				found = true
				break
			}
		}
		if !found {
			r.cfg.Directories = append(r.cfg.Directories, d)
			if b := newBackends[key]; b != nil {
				r.backends[key] = b
			}
		}
	}
	for _, c := range connectors {
		found := false
		for i, existing := range r.cfg.Connectors {
			if existing.OwnerUserID == ownerUserID && existing.ID == c.ID {
				r.cfg.Connectors[i] = c
				found = true
				break
			}
		}
		if !found {
			r.cfg.Connectors = append(r.cfg.Connectors, c)
		}
	}
	return nil
}

func (r *Registry) replaceUserDataLocked(ownerUserID string, dirs []config.Directory, connectors []config.Connector, newBackends map[string]storage.Backend) {
	filteredDirs := r.cfg.Directories[:0]
	for _, d := range r.cfg.Directories {
		if d.OwnerUserID == ownerUserID {
			if old := r.backends[backendKey(ownerUserID, d.ID)]; old != nil {
				_ = old.Close()
			}
			delete(r.backends, backendKey(ownerUserID, d.ID))
			continue
		}
		filteredDirs = append(filteredDirs, d)
	}
	r.cfg.Directories = filteredDirs
	for _, d := range dirs {
		d.OwnerUserID = ownerUserID
		d.TeamID = ""
		r.cfg.Directories = append(r.cfg.Directories, d)
		if b := newBackends[backendKey(ownerUserID, d.ID)]; b != nil {
			r.backends[backendKey(ownerUserID, d.ID)] = b
		}
	}
	filteredConnectors := r.cfg.Connectors[:0]
	for _, c := range r.cfg.Connectors {
		if c.OwnerUserID != ownerUserID {
			filteredConnectors = append(filteredConnectors, c)
		}
	}
	r.cfg.Connectors = filteredConnectors
	r.cfg.Connectors = append(r.cfg.Connectors, connectors...)
}

func (r *Registry) DirectoriesForUser(ownerUserID string) []config.Directory {
	viewTeams, _, _ := r.teamAccessForUser(ownerUserID)
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []config.Directory
	for _, d := range r.cfg.Directories {
		if d.OwnerUserID == ownerUserID || (d.TeamID != "" && viewTeams[d.TeamID]) {
			out = append(out, d)
		}
	}
	return out
}

// DirectoryViewForUser returns one directory plus its open backend when the
// user owns it or has team view access. Returns nil when the directory is
// unknown or not visible to this user. The Backend field is nil when the
// directory exists but its backend failed to open.
func (r *Registry) DirectoryViewForUser(userID, id string) *DirectoryView {
	viewTeams, _, _ := r.teamAccessForUser(userID)
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, d := range r.cfg.Directories {
		if d.ID != id {
			continue
		}
		if d.OwnerUserID != userID && (d.TeamID == "" || !viewTeams[d.TeamID]) {
			continue
		}
		return &DirectoryView{Directory: d, Backend: r.backends[backendKey(d.OwnerUserID, d.ID)]}
	}
	return nil
}

func (r *Registry) ConnectorsForUser(ownerUserID string) []config.Connector {
	viewTeams, _, _ := r.teamAccessForUser(ownerUserID)
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []config.Connector
	for _, c := range r.cfg.Connectors {
		if c.OwnerUserID == ownerUserID || (c.TeamID != "" && viewTeams[c.TeamID]) {
			out = append(out, cloneConnector(c))
		}
	}
	return out
}

// teamAccessForUser returns three team-ID sets for a user:
//
//   - view:   every team the user belongs to (any role) — read access.
//   - write:  teams where the user may mutate shared data (owner/admin/member).
//   - manage: teams the user administers (owner/admin) — settings, membership,
//     and (re)scoping directories/connectors.
//
// Viewers appear in view but not write or manage.
func (r *Registry) teamAccessForUser(userID string) (view, write, manage map[string]bool) {
	view = map[string]bool{}
	write = map[string]bool{}
	manage = map[string]bool{}
	if r.accounts == nil || userID == "" {
		return view, write, manage
	}
	teams, err := r.accounts.ListTeamsForUser(context.Background(), userID)
	if err != nil {
		return view, write, manage
	}
	for _, team := range teams {
		view[team.ID] = true
		switch team.Role {
		case account.RoleOwner, account.RoleAdmin:
			write[team.ID] = true
			manage[team.ID] = true
		case account.RoleMember:
			write[team.ID] = true
		}
	}
	return view, write, manage
}

// validateConnectorDirectoriesLocked checks that every directory a connector
// references is reachable by ownerUserID — either owned outright, or team-shared
// with a team the owner can view. When the connector itself is team-scoped
// (teamID != ""), each directory must belong to that same team. A writable
// connector may only reference directories the owner can write (owned, or a
// write-capable team role); referencing a read-only team directory from a
// write connector is rejected up front rather than silently downgraded.
//
// viewTeams/writeTeams are the owner's team-access sets, resolved by the caller
// before acquiring the lock so this method performs no I/O.
func (r *Registry) validateConnectorDirectoriesLocked(ownerUserID, teamID string, directoryIDs []string, write bool, viewTeams, writeTeams map[string]bool) error {
	for _, did := range directoryIDs {
		found := false
		for _, d := range r.cfg.Directories {
			if d.ID != did {
				continue
			}
			owned := d.OwnerUserID == ownerUserID
			if !owned && (d.TeamID == "" || !viewTeams[d.TeamID]) {
				continue
			}
			if teamID != "" && d.TeamID != teamID {
				return fmt.Errorf("directory %s is not in connector team scope", did)
			}
			if write && !owned && !writeTeams[d.TeamID] {
				return fmt.Errorf("you have read-only access to directory %s", did)
			}
			found = true
			break
		}
		if !found {
			return fmt.Errorf("unknown directory: %s", did)
		}
	}
	return nil
}

// removeString returns a new slice with target removed. It must not filter in
// place (list[:0]): connector copies returned by the accessors share the same
// backing array, so mutating it would race with concurrent readers.
func removeString(list []string, target string) []string {
	out := make([]string, 0, len(list))
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
	b := make([]byte, 12)
	if _, err := crand.Read(b); err != nil {
		// crypto/rand should never fail; fall back to math/rand rather than
		// returning a predictable constant.
		for i := range b {
			b[i] = chars[rand.Intn(len(chars))]
		}
		return string(b)
	}
	for i := range b {
		b[i] = chars[int(b[i])%len(chars)]
	}
	return string(b)
}

func backendKey(ownerUserID, id string) string {
	return ownerUserID + "\x00" + id
}
