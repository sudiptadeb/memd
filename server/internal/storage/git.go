package storage

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Git is a memory backend backed by a git repository working copy.
type Git struct {
	mu          sync.Mutex
	workdir     string
	basePath    string
	remoteURL   string
	branch      string
	authorName  string
	authorEmail string
	sshKey      string

	local     *Local
	lastSync  time.Time
	lastError string
}

type GitConfig struct {
	WorkDir     string
	RemoteURL   string
	Branch      string
	BasePath    string
	AuthorName  string
	AuthorEmail string
	SSHKeyPath  string
}

func NewGit(cfg GitConfig) (*Git, error) {
	if cfg.WorkDir == "" || cfg.RemoteURL == "" {
		return nil, errors.New("workdir and remote URL required")
	}
	g := &Git{
		workdir:     cfg.WorkDir,
		basePath:    strings.TrimPrefix(cfg.BasePath, "/"),
		remoteURL:   cfg.RemoteURL,
		branch:      cfg.Branch,
		authorName:  cfg.AuthorName,
		authorEmail: cfg.AuthorEmail,
		sshKey:      cfg.SSHKeyPath,
	}
	if g.branch == "" {
		g.branch = "main"
	}
	if g.authorName == "" {
		g.authorName = "memd"
	}
	if g.authorEmail == "" {
		g.authorEmail = "memd@localhost"
	}

	if _, err := os.Stat(filepath.Join(g.workdir, ".git")); errors.Is(err, fs.ErrNotExist) {
		if err := g.clone(); err != nil {
			return nil, err
		}
	} else if err == nil {
		if err := g.runQuiet("pull", "--ff-only", "origin", g.branch); err != nil {
			g.lastError = err.Error()
		}
	} else {
		return nil, err
	}

	root := filepath.Join(g.workdir, g.basePath)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	local, err := NewLocal(root)
	if err != nil {
		return nil, err
	}
	g.local = local
	g.lastSync = time.Now()
	return g, nil
}

func (g *Git) clone() error {
	if err := os.MkdirAll(filepath.Dir(g.workdir), 0o755); err != nil {
		return err
	}
	cmd := exec.Command("git", "clone", "--branch", g.branch, g.remoteURL, g.workdir)
	cmd.Env = g.cmdEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		// retry without branch flag (repo may have a different default branch)
		cmd2 := exec.Command("git", "clone", g.remoteURL, g.workdir)
		cmd2.Env = g.cmdEnv()
		out2, err2 := cmd2.CombinedOutput()
		if err2 != nil {
			return fmt.Errorf("clone: %s", strings.TrimSpace(string(out2)))
		}
		// switch to the requested branch if it exists
		_ = g.runQuiet("checkout", g.branch)
		_ = out
	}
	return nil
}

func (g *Git) cmdEnv() []string {
	env := os.Environ()
	if g.sshKey != "" {
		env = append(env, fmt.Sprintf("GIT_SSH_COMMAND=ssh -i %s -o StrictHostKeyChecking=accept-new", g.sshKey))
	}
	return env
}

func (g *Git) runQuiet(args ...string) error {
	full := append([]string{"-C", g.workdir}, args...)
	cmd := exec.Command("git", full...)
	cmd.Env = g.cmdEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func (g *Git) List() ([]string, error)               { return g.local.List() }
func (g *Git) Read(path string) ([]byte, error)      { return g.local.Read(path) }
func (g *Git) Search(q string, l int) ([]Hit, error) { return g.local.Search(q, l) }
func (g *Git) Close() error                          { return nil }

func (g *Git) Write(path string, content []byte, message string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if err := g.local.Write(path, content, message); err != nil {
		return err
	}

	relInRepo := filepath.ToSlash(filepath.Join(g.basePath, path))
	if err := g.runQuiet("add", relInRepo); err != nil {
		g.lastError = err.Error()
		return err
	}

	if message == "" {
		message = fmt.Sprintf("memd: update %s", path)
	}
	commitErr := g.runQuiet(
		"-c", "user.name="+g.authorName,
		"-c", "user.email="+g.authorEmail,
		"commit", "-m", message,
	)
	if commitErr != nil && !strings.Contains(commitErr.Error(), "nothing to commit") {
		g.lastError = commitErr.Error()
		return commitErr
	}

	if err := g.runQuiet("push", "origin", g.branch); err != nil {
		g.lastError = err.Error()
		return err
	}

	g.lastError = ""
	g.lastSync = time.Now()
	return nil
}

func (g *Git) Status() Status {
	return Status{
		Backend:   "git",
		Path:      fmt.Sprintf("%s @ %s:%s", g.remoteURL, g.branch, g.basePath),
		LastSync:  g.lastSync,
		LastError: g.lastError,
	}
}

// EnsureIndex creates index.md (committing and pushing) if missing.
func (g *Git) EnsureIndex(description string) error {
	idx := filepath.Join(g.local.Root(), "index.md")
	if _, err := os.Stat(idx); err == nil {
		return nil
	}
	if description == "" {
		description = "Memory"
	}
	body := fmt.Sprintf("# %s\n\n_(no memory yet — populate as durable knowledge accrues)_\n", description)
	return g.Write("index.md", []byte(body), "memd: initialize index.md")
}
