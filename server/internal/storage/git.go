package storage

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Default debounce + safety-flush durations for the git backend. Overridable
// per directory via GitConfig.
const (
	defaultWaitForWrites = 5 * time.Minute
	defaultSaveEvery     = 10 * time.Minute
)

// Git is a memory backend backed by a git repository working copy. Writes
// persist to the working copy immediately; commit + push is debounced so a
// session of edits produces one commit instead of N. A periodic safety
// flush catches read-only sessions where only front-matter stats churn.
type Git struct {
	mu           sync.Mutex
	workdir      string
	basePath     string
	remoteURL    string
	branch       string
	authorName   string
	authorEmail  string
	authUsername string
	authToken    string
	sshKey       string
	askPassPath  string

	waitForWrites time.Duration
	saveEvery     time.Duration

	local     *Local
	lastSync  time.Time
	lastError string

	// State guarded by mu:
	debounce    *time.Timer
	pendingHave bool
	pendingMsg  string

	stopCh chan struct{}
	wg     sync.WaitGroup
}

type GitConfig struct {
	WorkDir       string
	RemoteURL     string
	Branch        string
	BasePath      string
	AuthorName    string
	AuthorEmail   string
	AuthUsername  string
	AuthToken     string
	SSHKeyPath    string
	WaitForWrites time.Duration
	SaveEvery     time.Duration
}

func NewGit(cfg GitConfig) (*Git, error) {
	if cfg.WorkDir == "" || cfg.RemoteURL == "" {
		return nil, errors.New("workdir and remote URL required")
	}
	remoteURL, urlUser, urlToken := splitRemoteAuth(cfg.RemoteURL)
	authUsername := strings.TrimSpace(cfg.AuthUsername)
	if authUsername == "" {
		authUsername = urlUser
	}
	authToken := strings.TrimSpace(cfg.AuthToken)
	if authToken == "" {
		authToken = urlToken
	}
	if authToken != "" && authUsername == "" {
		authUsername = "x-access-token"
	}

	g := &Git{
		workdir:       cfg.WorkDir,
		basePath:      strings.TrimPrefix(cfg.BasePath, "/"),
		remoteURL:     remoteURL,
		branch:        cfg.Branch,
		authorName:    cfg.AuthorName,
		authorEmail:   cfg.AuthorEmail,
		authUsername:  authUsername,
		authToken:     authToken,
		sshKey:        cfg.SSHKeyPath,
		waitForWrites: cfg.WaitForWrites,
		saveEvery:     cfg.SaveEvery,
		stopCh:        make(chan struct{}),
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
	if g.waitForWrites <= 0 {
		g.waitForWrites = defaultWaitForWrites
	}
	if g.saveEvery <= 0 {
		g.saveEvery = defaultSaveEvery
	}
	if err := g.prepareAskPass(); err != nil {
		return nil, err
	}

	if _, err := os.Stat(filepath.Join(g.workdir, ".git")); errors.Is(err, fs.ErrNotExist) {
		if err := g.clone(); err != nil {
			return nil, err
		}
	} else if err == nil {
		if err := g.ensureOrigin(); err != nil {
			g.lastError = err.Error()
		} else if err := g.checkoutConfiguredBranch(); err != nil {
			g.lastError = err.Error()
		} else if err := g.syncRemote(); err != nil {
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

	// Safety ticker: periodically commit anything dirty so read-only
	// sessions (which only mutate FM stats) eventually sync.
	g.wg.Add(1)
	go g.safetyTick()

	return g, nil
}

func (g *Git) clone() error {
	if err := os.MkdirAll(filepath.Dir(g.workdir), 0o755); err != nil {
		return err
	}
	cmd := exec.Command("git", "clone", "--branch", g.branch, g.remoteURL, g.workdir)
	cmd.Env = g.cmdEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		cmd2 := exec.Command("git", "clone", g.remoteURL, g.workdir)
		cmd2.Env = g.cmdEnv()
		out2, err2 := cmd2.CombinedOutput()
		if err2 != nil {
			return fmt.Errorf("clone: %s", strings.TrimSpace(string(out2)))
		}
		if err := g.runQuiet("checkout", "-B", g.branch); err != nil {
			return err
		}
		_ = out
	}
	if err := g.ensureOrigin(); err != nil {
		return err
	}
	return nil
}

func (g *Git) cmdEnv() []string {
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if g.authToken != "" {
		env = append(env,
			"GIT_ASKPASS="+g.askPassPath,
			"MEMD_GIT_USERNAME="+g.authUsername,
			"MEMD_GIT_TOKEN="+g.authToken,
		)
	}
	if g.sshKey != "" {
		env = append(env, fmt.Sprintf("GIT_SSH_COMMAND=ssh -i %s -o StrictHostKeyChecking=accept-new -o BatchMode=yes", shellQuote(g.sshKey)))
	}
	return env
}

func (g *Git) prepareAskPass() error {
	if g.authToken == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(g.workdir), 0o700); err != nil {
		return err
	}
	path := filepath.Join(filepath.Dir(g.workdir), ".git-askpass")
	const script = `#!/bin/sh
case "$1" in
*Username*|*username*) printf '%s\n' "$MEMD_GIT_USERNAME" ;;
*Password*|*password*) printf '%s\n' "$MEMD_GIT_TOKEN" ;;
*) printf '\n' ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		return err
	}
	g.askPassPath = path
	return nil
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

func (g *Git) ensureOrigin() error {
	if err := g.runQuiet("remote", "get-url", "origin"); err != nil {
		return g.runQuiet("remote", "add", "origin", g.remoteURL)
	}
	return g.runQuiet("remote", "set-url", "origin", g.remoteURL)
}

func (g *Git) checkoutConfiguredBranch() error {
	if err := g.runQuiet("checkout", g.branch); err == nil {
		return nil
	}
	exists, err := g.remoteBranchExists()
	if err != nil {
		return err
	}
	if exists {
		if err := g.runQuiet("fetch", "origin", g.branch); err != nil {
			return err
		}
		return g.runQuiet("checkout", "-B", g.branch, "FETCH_HEAD")
	}
	return g.runQuiet("checkout", "-B", g.branch)
}

func (g *Git) syncRemote() error {
	exists, err := g.remoteBranchExists()
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	return g.runQuiet("pull", "--rebase", "--autostash", "origin", g.branch)
}

func (g *Git) remoteBranchExists() (bool, error) {
	full := []string{"-C", g.workdir, "ls-remote", "--exit-code", "--heads", "origin", g.branch}
	cmd := exec.Command("git", full...)
	cmd.Env = g.cmdEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 2 {
			return false, nil
		}
		return false, fmt.Errorf("git ls-remote --heads origin %s: %v: %s", g.branch, err, strings.TrimSpace(stderr.String()))
	}
	return true, nil
}

func (g *Git) List() ([]string, error)                  { return g.local.List() }
func (g *Git) ListPath(path string) ([]DirEntry, error) { return g.local.ListPath(path) }
func (g *Git) Read(path string) ([]byte, error)         { return g.local.Read(path) }
func (g *Git) Search(q string, l int) ([]Hit, error)    { return g.local.Search(q, l) }

// Move renames src to dst in the working copy and arms the debounce
// timer. The eventual `git add -A` will track it as a rename via
// similarity detection.
func (g *Git) Move(src, dst, message string) error {
	if err := g.local.Move(src, dst, message); err != nil {
		return err
	}
	if message == "" {
		message = fmt.Sprintf("memd: move %s -> %s", src, dst)
	}
	g.armDebounce(message)
	return nil
}

// Delete removes a single file from the working copy and arms the
// debounce timer.
func (g *Git) Delete(path, message string) error {
	if err := g.local.Delete(path, message); err != nil {
		return err
	}
	if message == "" {
		message = fmt.Sprintf("memd: delete %s", path)
	}
	g.armDebounce(message)
	return nil
}

// DeleteFolder recursively removes a folder and arms the debounce timer.
func (g *Git) DeleteFolder(path, message string) error {
	if err := g.local.DeleteFolder(path, message); err != nil {
		return err
	}
	if message == "" {
		message = fmt.Sprintf("memd: delete folder %s", path)
	}
	g.armDebounce(message)
	return nil
}

// armDebounce records a pending commit message and (re)arms the
// wait_for_writes timer. Safe to call from any goroutine.
func (g *Git) armDebounce(message string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pendingHave = true
	g.pendingMsg = message
	if g.debounce == nil {
		g.debounce = time.AfterFunc(g.waitForWrites, func() {
			_ = g.flushDirty("memd: session checkpoint")
		})
	} else {
		g.debounce.Reset(g.waitForWrites)
	}
}

// Write persists the file to the working copy and arms the debounce timer.
// Returns as soon as the file is on disk; the commit+push happens after
// `wait_for_writes` of write silence (or on the periodic safety flush, or
// on Close).
func (g *Git) Write(path string, content []byte, message string) error {
	if err := g.local.Write(path, content, ""); err != nil {
		return err
	}
	if message == "" {
		message = fmt.Sprintf("memd: update %s", path)
	}
	g.armDebounce(message)
	return nil
}

// Flush forces any deferred writes to commit + push. Safe to call multiple
// times; if there's nothing dirty it's a near no-op (just runs `git add` and
// gets "nothing to commit").
func (g *Git) Flush() error {
	return g.flushDirty("memd: manual checkpoint")
}

// Close stops timers and flushes any pending commits.
func (g *Git) Close() error {
	g.mu.Lock()
	select {
	case <-g.stopCh:
		// already closed
	default:
		close(g.stopCh)
	}
	if g.debounce != nil {
		g.debounce.Stop()
		g.debounce = nil
	}
	g.mu.Unlock()
	err := g.flushDirty("memd: session checkpoint")
	g.wg.Wait()
	return err
}

// safetyTick fires every `save_every` and commits anything dirty.
func (g *Git) safetyTick() {
	defer g.wg.Done()
	t := time.NewTicker(g.saveEvery)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			_ = g.flushDirty("memd: periodic checkpoint")
		case <-g.stopCh:
			return
		}
	}
}

// flushDirty syncs with the remote, then runs `git add -A` + `git commit` +
// `git push`. If there's nothing new to commit it still pushes, which retries
// any local commits left behind by an earlier network/auth failure. The caller
// should not hold g.mu; flushDirty takes it.
func (g *Git) flushDirty(defaultMsg string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.debounce != nil {
		g.debounce.Stop()
		g.debounce = nil
	}
	msg := defaultMsg
	if g.pendingHave && g.pendingMsg != "" {
		msg = g.pendingMsg
	}
	g.pendingHave = false
	g.pendingMsg = ""

	addPath := g.basePath
	if addPath == "" {
		addPath = "."
	}
	if err := g.runQuiet("add", "-A", addPath); err != nil {
		g.lastError = err.Error()
		return err
	}
	if err := g.ensureOrigin(); err != nil {
		g.lastError = err.Error()
		return err
	}
	if err := g.checkoutConfiguredBranch(); err != nil {
		g.lastError = err.Error()
		return err
	}
	if err := g.syncRemote(); err != nil {
		g.lastError = err.Error()
		return err
	}
	if err := g.runQuiet("add", "-A", addPath); err != nil {
		g.lastError = err.Error()
		return err
	}
	commitErr := g.runQuiet(
		"-c", "user.name="+g.authorName,
		"-c", "user.email="+g.authorEmail,
		"commit", "-m", msg,
	)
	if commitErr != nil {
		if strings.Contains(commitErr.Error(), "nothing to commit") ||
			strings.Contains(commitErr.Error(), "no changes added to commit") {
			commitErr = nil
		} else {
			g.lastError = commitErr.Error()
			return commitErr
		}
	}
	if err := g.runQuiet("push", "-u", "origin", g.branch); err != nil {
		firstPushErr := err
		if syncErr := g.syncRemote(); syncErr != nil {
			err := fmt.Errorf("%v; after failed push, pull --rebase also failed: %v", firstPushErr, syncErr)
			g.lastError = err.Error()
			return err
		}
		if err := g.runQuiet("push", "-u", "origin", g.branch); err != nil {
			g.lastError = err.Error()
			return err
		}
	}
	g.lastError = ""
	g.lastSync = time.Now()
	return nil
}

func (g *Git) Status() Status {
	return Status{
		Backend:   "git",
		Path:      fmt.Sprintf("%s @ %s:%s", redactRemoteURL(g.remoteURL), g.branch, g.basePath),
		LastSync:  g.lastSync,
		LastError: g.lastError,
	}
}

// EnsureIndex commits a starter MEMORY.md only when the directory has no
// Markdown content at its root. Existing Markdown is left untouched.
func (g *Git) EnsureIndex(description string) error {
	entries, err := os.ReadDir(g.local.Root())
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
	return g.Write("MEMORY.md", []byte(body), "memd: initialize MEMORY.md")
}

func splitRemoteAuth(raw string) (clean, username, token string) {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw, "", ""
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return raw, "", ""
	}
	username = u.User.Username()
	token, _ = u.User.Password()
	u.User = nil
	return u.String(), username, token
}

func redactRemoteURL(raw string) string {
	clean, _, _ := splitRemoteAuth(raw)
	return clean
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
