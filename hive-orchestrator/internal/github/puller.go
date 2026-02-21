package github

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Puller clones and updates GitHub repos using the git binary.
type Puller struct {
	stagingDir string
}

func NewPuller(stagingDir string) *Puller {
	_ = os.MkdirAll(stagingDir, 0o755)
	return &Puller{stagingDir: stagingDir}
}

// CloneOrFetch clones the repo on first deploy, pulls latest on subsequent deploys.
// Returns the local path to the repo.
func (p *Puller) CloneOrFetch(cloneURL, branch, agentID string) (string, error) {
	dest := filepath.Join(p.stagingDir, agentID)

	if _, err := os.Stat(filepath.Join(dest, ".git")); os.IsNotExist(err) {
		return p.clone(cloneURL, branch, dest)
	}
	return p.fetch(dest, branch)
}

func (p *Puller) clone(cloneURL, branch, dest string) (string, error) {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return "", fmt.Errorf("mkdir staging: %w", err)
	}
	if err := git(dest, "clone", "--depth", "1", "--branch", branch, cloneURL, "."); err != nil {
		return "", fmt.Errorf("git clone %s: %w", cloneURL, err)
	}
	return dest, nil
}

func (p *Puller) fetch(dest, branch string) (string, error) {
	if err := git(dest, "fetch", "--depth", "1", "origin", branch); err != nil {
		return "", fmt.Errorf("git fetch: %w", err)
	}
	if err := git(dest, "reset", "--hard", "origin/"+branch); err != nil {
		return "", fmt.Errorf("git reset: %w", err)
	}
	return dest, nil
}

// Cleanup removes the cloned repo for an agent after the image has been pushed.
func (p *Puller) Cleanup(agentID string) {
	_ = os.RemoveAll(filepath.Join(p.stagingDir, agentID))
}

// NormaliseURL converts git@github.com:owner/repo.git to https form.
func NormaliseURL(raw string) string {
	if strings.HasPrefix(raw, "git@") {
		raw = strings.TrimPrefix(raw, "git@")
		raw = "https://" + strings.Replace(raw, ":", "/", 1)
	}
	return raw
}

func git(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
