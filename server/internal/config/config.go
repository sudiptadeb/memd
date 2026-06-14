package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// Dir returns the platform-specific memd config directory.
//
//	macOS:   ~/Library/Application Support/memd
//	Linux:   ~/.config/memd  (or $XDG_CONFIG_HOME/memd)
//	Windows: %AppData%\memd
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "memd"), nil
}

// File returns the path to config.json.
func File() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// WorkdirsRoot returns the root path for git working copies.
func WorkdirsRoot() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "workdirs"), nil
}

// ManagedLocalRoot returns the root path under which memd creates sandboxed
// local directories for users who only supply a name (rather than choosing
// their own path). Each directory lives at <root>/<ownerUserID>/<dirID>.
func ManagedLocalRoot() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "local"), nil
}

// Config is the on-disk shape of memd state.
type Config struct {
	Directories []Directory `json:"directories"`
	Connectors  []Connector `json:"connectors"`
}

// Directory describes one memory directory.
type Directory struct {
	ID          string `json:"id"`
	OwnerUserID string `json:"owner_user_id,omitempty"`
	TeamID      string `json:"team_id,omitempty"`

	// OwnerConnectorID designates the one connector allowed to work directly on
	// a git directory's branch (main); every other connector works on its own
	// memd/<user>-<connector> branch. It must belong to the directory's owner.
	// Empty means no designation: the owner's own connectors write the
	// directory branch directly and everyone else branches. Applies to personal
	// and team directories alike.
	OwnerConnectorID string `json:"owner_connector_id,omitempty"`

	Name        string    `json:"name"`
	Description string    `json:"description"`
	Backend     string    `json:"backend"` // "local" or "git"
	LocalPath   string    `json:"local_path,omitempty"`
	Git         *Git      `json:"git,omitempty"`
	CreatedAt   time.Time `json:"created_at"`

	// Features lists the structured-memory features enabled on this directory
	// (tasks, calendar, …). Enablement is independent of folder presence: a
	// disabled feature keeps its folder and data, it is just not surfaced.
	Features []DirectoryFeature `json:"features,omitempty"`
}

// DirectoryFeature is the per-directory enable record for one feature. Settings
// is reserved for future per-feature configuration.
type DirectoryFeature struct {
	Key      string            `json:"key"`
	Enabled  bool              `json:"enabled"`
	Settings map[string]string `json:"settings,omitempty"`
}

// Git is the per-directory git backend config.
type Git struct {
	RemoteURL    string `json:"remote_url"`
	Branch       string `json:"branch"`
	BasePath     string `json:"base_path"`
	AuthorName   string `json:"author_name"`
	AuthorEmail  string `json:"author_email"`
	AuthUsername string `json:"auth_username,omitempty"`
	AuthToken    string `json:"auth_token,omitempty"`
	SSHKeyPath   string `json:"ssh_key_path,omitempty"`

	// WaitForWrites is the debounce window after a memory_write. Any further
	// write resets the timer; on expiry the working copy is committed and
	// pushed. Accepts Go duration strings ("5m", "30s"). Default: 5m.
	WaitForWrites string `json:"wait_for_writes,omitempty"`

	// SaveEvery is the periodic safety flush interval. If the working copy
	// has dirty files (e.g. read-only session that bumped FM stats) it's
	// committed regardless of write activity. Default: 10m.
	SaveEvery string `json:"save_every,omitempty"`
}

// RedactGitRemoteURL removes inline credentials from a git remote before it is
// shown in UI/status output. Tokens supplied through Git.AuthToken are never
// part of the URL, but users sometimes paste credentialed HTTPS remotes.
func RedactGitRemoteURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	u.User = url.User("redacted")
	return u.String()
}

// Connector grants an agent access to one or more directories.
type Connector struct {
	ID           string    `json:"id"`
	OwnerUserID  string    `json:"owner_user_id,omitempty"`
	TeamID       string    `json:"team_id,omitempty"`
	Name         string    `json:"name"`
	Kind         string    `json:"kind,omitempty"` // "mcp" or "http"; empty means "mcp" for older configs
	Token        string    `json:"token"`
	DirectoryIDs []string  `json:"directory_ids"`
	Write        bool      `json:"write"`
	CreatedAt    time.Time `json:"created_at"`
}

const (
	ConnectorKindMCP  = "mcp"
	ConnectorKindHTTP = "http"
)

func NormalizeConnectorKind(kind string) string {
	switch kind {
	case "", ConnectorKindMCP:
		return ConnectorKindMCP
	case ConnectorKindHTTP:
		return ConnectorKindHTTP
	default:
		return kind
	}
}

func (c Connector) EffectiveKind() string {
	return NormalizeConnectorKind(c.Kind)
}

// Load reads config.json. Returns an empty Config if the file does not exist.
func Load() (*Config, error) {
	path, err := File()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// Save atomically writes config.json with 0600 mode.
func Save(c *Config) error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path, err := File()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
