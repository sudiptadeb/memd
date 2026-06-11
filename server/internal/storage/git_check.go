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

	if !add("read", "Read remote repository", g.runNoWorkdir("ls-remote", g.remoteURL), "Verified HTTPS credentials can read refs.") {
		return report
	}
	if !add("clone", "Clone repository", g.clone(), "Cloned into a temporary workspace.") {
		return report
	}

	checkBranch := fmt.Sprintf("memd-connection-check/%d", time.Now().UnixNano())
	if !add("write", "Create local test commit", g.createConnectionCheckCommit(checkBranch), "Created a commit only in the temporary workspace.") {
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

func (g *Git) runNoWorkdir(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Env = g.cmdEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func (g *Git) createConnectionCheckCommit(branch string) error {
	if err := g.runQuiet("checkout", "-B", branch); err != nil {
		return err
	}
	path := filepath.Join(g.workdir, ".memd-connection-check")
	body := []byte("temporary memd connection check\n")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return err
	}
	if err := g.runQuiet("add", ".memd-connection-check"); err != nil {
		return err
	}
	return g.runQuiet(
		"-c", "user.name="+g.authorName,
		"-c", "user.email="+g.authorEmail,
		"commit", "-m", "memd: connection check",
	)
}
