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
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/sudiptadeb/memd/server/internal/logs"
)

// allowedGitProtocols is the colon-separated allowlist handed to git via
// GIT_ALLOW_PROTOCOL. It intentionally omits transport helpers such as ext and
// fd, which let a remote URL run arbitrary shell commands.
const allowedGitProtocols = "http:https:ssh:git:file"

// transportHelperRe matches git's "helper::address" smart-transport syntax
// (e.g. ext::, fd::, transport::). The segment before "::" is a bare token
// with no slash, which is what distinguishes it from a local path like
// "/tmp/a::b".
var transportHelperRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*::`)

// urlSchemeRe matches an explicit "scheme://" prefix.
var urlSchemeRe = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9+.-]*)://`)

// validateRemoteURL rejects git remote URLs that could lead to command
// execution or option injection. It allows normal URL schemes (http(s), ssh,
// git, file), scp-style host:path remotes, and local filesystem paths, while
// rejecting transport-helper URLs (ext::, fd::, ...) and anything that would be
// parsed as a command-line flag.
func validateRemoteURL(raw string) error {
	s := strings.TrimSpace(raw)
	if s == "" {
		return errors.New("remote URL required")
	}
	if strings.HasPrefix(s, "-") {
		return fmt.Errorf("invalid remote URL %q: must not start with '-'", s)
	}
	if transportHelperRe.MatchString(s) {
		helper := s[:strings.Index(s, "::")]
		return fmt.Errorf("unsupported git remote transport %q::", helper)
	}
	if m := urlSchemeRe.FindStringSubmatch(s); m != nil {
		switch strings.ToLower(m[1]) {
		case "http", "https", "ssh", "git", "file":
		default:
			return fmt.Errorf("unsupported git remote scheme %q", m[1])
		}
	}
	return nil
}

// validateBranch rejects branch names that could be interpreted as a
// command-line flag or that contain characters git forbids in refs.
func validateBranch(branch string) error {
	if branch == "" {
		return nil
	}
	if strings.HasPrefix(branch, "-") {
		return fmt.Errorf("invalid branch %q: must not start with '-'", branch)
	}
	if strings.ContainsAny(branch, " \t\n\r~^:?*[\\") || strings.Contains(branch, "..") {
		return fmt.Errorf("invalid branch %q", branch)
	}
	return nil
}

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
	baseBranch   string
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

	// BaseBranch turns Branch into a work branch: the clone forks Branch from
	// BaseBranch when it doesn't exist yet, and every flush merges fresh
	// BaseBranch commits into Branch (preferring the work branch's side on
	// conflicting hunks). Used for per-connector branches on team directories,
	// where Branch is the connector's branch and BaseBranch is the directory's
	// configured branch. Empty means Branch is the directory branch itself.
	BaseBranch string

	// DisableReadStats stops Read from bumping managed file stats. Set on
	// per-connector branch clones so reads never dirty the branch.
	DisableReadStats bool
}

func NewGit(cfg GitConfig) (*Git, error) {
	g, err := newGitFromConfig(cfg)
	if err != nil {
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

	if err := g.syncBase(); err != nil {
		g.lastError = err.Error()
	}

	root := filepath.Join(g.workdir, g.basePath)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	local, err := NewLocal(root)
	if err != nil {
		return nil, err
	}
	local.skipReadStats = cfg.DisableReadStats
	g.local = local
	g.lastSync = time.Now()

	// Safety ticker: periodically commit anything dirty so read-only
	// sessions (which only mutate FM stats) eventually sync.
	g.wg.Add(1)
	go g.safetyTick()

	return g, nil
}

func newGitFromConfig(cfg GitConfig) (*Git, error) {
	if cfg.WorkDir == "" || cfg.RemoteURL == "" {
		return nil, errors.New("workdir and remote URL required")
	}
	remoteURL, urlUser, urlToken := splitRemoteAuth(cfg.RemoteURL)
	if err := validateRemoteURL(remoteURL); err != nil {
		return nil, err
	}
	if err := validateBranch(strings.TrimSpace(cfg.Branch)); err != nil {
		return nil, err
	}
	if err := validateBranch(strings.TrimSpace(cfg.BaseBranch)); err != nil {
		return nil, err
	}
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
		baseBranch:    strings.TrimSpace(cfg.BaseBranch),
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
	if g.baseBranch == g.branch {
		g.baseBranch = ""
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
		if err := g.checkoutBranchFromBase(); err != nil {
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
	env := append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ALLOW_PROTOCOL="+allowedGitProtocols,
	)
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
	return g.checkoutBranchFromBase()
}

// checkoutBranchFromBase creates the configured branch. When a base branch is
// configured (per-connector work branches), the new branch forks from the
// remote base so it starts at the directory's current content; otherwise it
// forks from whatever HEAD the clone left behind.
func (g *Git) checkoutBranchFromBase() error {
	if g.baseBranch != "" {
		if err := g.runQuiet("fetch", "origin", g.baseBranch); err == nil {
			return g.runQuiet("checkout", "-B", g.branch, "FETCH_HEAD")
		}
	}
	return g.runQuiet("checkout", "-B", g.branch)
}

// syncBase folds fresh base-branch commits into the work branch so long-lived
// connector branches keep seeing the directory's merged truth. Conflicting
// hunks keep the work branch's side (-X ours): a member's pending edits are
// never silently overwritten — review happens when the branch is merged back.
// No-op without a base branch. Failures are soft: the merge is aborted and the
// branch continues on its current base.
func (g *Git) syncBase() error {
	if g.baseBranch == "" {
		return nil
	}
	if err := g.runQuiet("fetch", "origin", g.baseBranch); err != nil {
		return err
	}
	if err := g.runQuiet(
		"-c", "user.name="+g.authorName,
		"-c", "user.email="+g.authorEmail,
		"merge", "--no-edit", "-X", "ours", "FETCH_HEAD",
	); err != nil {
		_ = g.runQuiet("merge", "--abort")
		return fmt.Errorf("merge %s into %s: %w", g.baseBranch, g.branch, err)
	}
	return nil
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
func (g *Git) ReadRaw(path string) ([]byte, error)      { return g.local.ReadRaw(path) }
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
			if err := g.flushDirty("memd: session checkpoint"); err != nil {
				logs.Error("git sync for %s failed: %v", redactRemoteURL(g.remoteURL), err)
			}
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
			if err := g.flushDirty("memd: periodic checkpoint"); err != nil {
				logs.Error("git sync for %s failed: %v", redactRemoteURL(g.remoteURL), err)
			}
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
	// `git commit` with a clean index exits 1 with its explanation on stdout,
	// which runQuiet doesn't capture — so check for staged changes explicitly
	// instead of pattern-matching the commit error. diff --cached --quiet
	// exits non-zero exactly when something is staged.
	if g.runQuiet("diff", "--cached", "--quiet") != nil {
		if err := g.runQuiet(
			"-c", "user.name="+g.authorName,
			"-c", "user.email="+g.authorEmail,
			"commit", "-m", msg,
		); err != nil {
			g.lastError = err.Error()
			return err
		}
	}
	// Refresh the work branch from its base now that the tree is clean. Soft
	// failure: a conflicted or unreachable base must not block pushing the
	// member's own commits.
	if err := g.syncBase(); err != nil {
		logs.Error("git base sync for %s failed: %v", redactRemoteURL(g.remoteURL), err)
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
	g.mu.Lock()
	lastSync, lastError := g.lastSync, g.lastError
	g.mu.Unlock()
	return Status{
		Backend:   "git",
		Path:      fmt.Sprintf("%s @ %s:%s", redactRemoteURL(g.remoteURL), g.branch, g.basePath),
		LastSync:  lastSync,
		LastError: lastError,
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
