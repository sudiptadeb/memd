package storage

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// GitConnectionReport is a non-destructive confidence check for a git
// directory configuration. The push check creates and deletes a temporary
// branch; it does not modify the configured memory branch.
type GitConnectionReport struct {
	OK     bool             `json:"ok"`
	Checks []GitCheckResult `json:"checks"`
}

type GitCheckResult struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
	Error  string `json:"error,omitempty"`
}

func CheckGitConnection(cfg GitConfig) GitConnectionReport {
	var report GitConnectionReport
	add := func(id, label string, err error, detail string) bool {
		check := GitCheckResult{ID: id, Label: label, Detail: detail}
		if err != nil {
			check.Error = err.Error()
		} else {
			check.OK = true
		}
		report.Checks = append(report.Checks, check)
		return check.OK
	}

	tmp, err := os.MkdirTemp("", "memd-git-check-*")
	if !add("prepare", "Prepare temporary workspace", err, "") {
		return report
	}
	defer os.RemoveAll(tmp)
	cfg.WorkDir = filepath.Join(tmp, "work")

	g, err := newGitFromConfig(cfg)
	if !add("config", "Validate Git configuration", err, "") {
		return report
	}

	heads, err := g.outputNoWorkdir("ls-remote", "--heads", g.remoteURL)
	if !add("read", "Read remote repository", err, "Verified HTTPS credentials can read refs.") {
		return report
	}
	// A repository with no branches yet needs a different push check: the
	// first branch pushed to an empty GitHub/GitLab repository becomes its
	// default branch, which the cleanup step is then refused to delete, and
	// the leftover branch breaks every later check.
	emptyRemote := strings.TrimSpace(heads) == ""
	if !add("clone", "Clone repository", g.clone(), "Cloned into a temporary workspace.") {
		return report
	}

	checkBranch := fmt.Sprintf("memd-connection-check/%d", time.Now().UnixNano())
	if !add("write", "Create local test commit", g.createConnectionCheckCommit(checkBranch), "Created a commit only in the temporary workspace.") {
		return report
	}

	if emptyRemote {
		if !add("push_dry_run", "Verify push access (dry run)", g.runQuiet("push", "--dry-run", "origin", "HEAD:refs/heads/"+checkBranch), "The repository is empty, so a real test branch would become its default branch; write access was verified with a dry-run push instead.") {
			return report
		}
		report.OK = true
		return report
	}

	pushed := add("push_pr_branch", "Push PR/MR test branch", g.runQuiet("push", "origin", "HEAD:refs/heads/"+checkBranch), "Pushed a temporary branch without touching the configured branch.")
	if !pushed {
		return report
	}
	if !add("cleanup", "Delete temporary test branch", g.runQuiet("push", "origin", "--delete", checkBranch), "Removed the temporary branch from the remote.") {
		return report
	}
	report.OK = true
	return report
}

func (g *Git) outputNoWorkdir(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Env = g.cmdEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func (g *Git) createConnectionCheckCommit(branch string) error {
	if err := g.runQuiet("checkout", "-B", branch); err != nil {
		return err
	}
	path := filepath.Join(g.workdir, ".memd-connection-check")
	// The body carries the branch name so the file always differs from any
	// earlier check that was left behind on the remote; an unchanged file
	// would make the commit fail with "nothing to commit".
	body := []byte("temporary memd connection check " + branch + "\n")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return err
	}
	if err := g.runQuiet("add", ".memd-connection-check"); err != nil {
		return err
	}
	// git commit reports "nothing to commit" on stdout, so capture both
	// streams here rather than the stderr-only runQuiet.
	cmd := exec.Command("git", "-C", g.workdir,
		"-c", "user.name="+g.authorName,
		"-c", "user.email="+g.authorEmail,
		"commit", "-m", "memd: connection check",
	)
	cmd.Env = g.cmdEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
