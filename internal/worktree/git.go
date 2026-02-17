// Package worktree provides git worktree isolation for worker agents.
// Each worker gets its own checkout (branch + directory), preventing file
// conflicts when multiple agents work on the same repository simultaneously.
package worktree

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// worktreeAdd creates a new git worktree at the specified path with a new branch.
// If baseBranch is empty, it uses the current HEAD.
func worktreeAdd(repoDir, worktreePath, branch, baseBranch string) error {
	args := []string{"worktree", "add"}
	if baseBranch != "" {
		args = append(args, "-b", branch, worktreePath, baseBranch)
	} else {
		args = append(args, "-b", branch, worktreePath)
	}

	cmd := exec.Command("git", args...)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree add: %w\noutput: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// worktreeRemove removes a git worktree. If force is true, uses --force.
func worktreeRemove(repoDir, worktreePath string, force bool) error {
	args := []string{"worktree", "remove", worktreePath}
	if force {
		args = append(args, "--force")
	}

	cmd := exec.Command("git", args...)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree remove: %w\noutput: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// worktreeList returns the paths of all worktrees in the repository.
func worktreeList(repoDir string) ([]string, error) {
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git worktree list: %w\noutput: %s", err, strings.TrimSpace(string(out)))
	}

	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "worktree ") {
			paths = append(paths, strings.TrimPrefix(line, "worktree "))
		}
	}
	return paths, nil
}

// branchExists checks if a branch exists in the repository.
func branchExists(repoDir, branch string) bool {
	cmd := exec.Command("git", "rev-parse", "--verify", "refs/heads/"+branch)
	cmd.Dir = repoDir
	return cmd.Run() == nil
}

// branchDelete deletes a local branch. Uses -D (force delete) to handle
// branches that haven't been merged.
func branchDelete(repoDir, branch string) error {
	cmd := exec.Command("git", "branch", "-D", branch)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git branch -D %s: %w\noutput: %s", branch, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// isGitRepo checks whether the given directory is inside a git repository.
func isGitRepo(dir string) bool {
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

// currentBranch returns the current branch name (or HEAD if detached).
func currentBranch(repoDir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git current branch: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// detectSetupCommands examines the directory for common project files and returns
// appropriate setup commands to run after worktree creation.
func detectSetupCommands(dir string) []string {
	var cmds []string

	if fileExists(filepath.Join(dir, "go.mod")) {
		cmds = append(cmds, "go mod download")
	}
	if fileExists(filepath.Join(dir, "package.json")) {
		if fileExists(filepath.Join(dir, "yarn.lock")) {
			cmds = append(cmds, "yarn install --frozen-lockfile")
		} else if fileExists(filepath.Join(dir, "pnpm-lock.yaml")) {
			cmds = append(cmds, "pnpm install --frozen-lockfile")
		} else {
			cmds = append(cmds, "npm ci")
		}
	}
	if fileExists(filepath.Join(dir, "requirements.txt")) {
		cmds = append(cmds, "pip install -r requirements.txt")
	}
	if fileExists(filepath.Join(dir, "Gemfile")) {
		cmds = append(cmds, "bundle install")
	}

	return cmds
}

// runSetupCommands executes setup commands in the given directory.
// Errors are non-fatal; they are collected and returned.
func runSetupCommands(dir string, cmds []string) []error {
	var errs []error
	for _, cmdStr := range cmds {
		parts := strings.Fields(cmdStr)
		if len(parts) == 0 {
			continue
		}
		cmd := exec.Command(parts[0], parts[1:]...)
		cmd.Dir = dir
		cmd.Stdout = nil
		cmd.Stderr = nil
		if err := cmd.Run(); err != nil {
			errs = append(errs, fmt.Errorf("setup command %q: %w", cmdStr, err))
		}
	}
	return errs
}

// worktreePrune removes stale worktree administrative data.
func worktreePrune(repoDir string) error {
	cmd := exec.Command("git", "worktree", "prune")
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree prune: %w\noutput: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
