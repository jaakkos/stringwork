package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initTestRepo creates a temporary git repository for testing.
// Returns the repo directory path.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git init: %s: %v", string(out), err)
		}
	}

	// Create initial commit so HEAD exists
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test\n"), 0644)
	cmd := exec.Command("git", "add", ".")
	cmd.Dir = dir
	cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "initial")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %s: %v", string(out), err)
	}

	return dir
}

func TestIsGitRepo(t *testing.T) {
	repo := initTestRepo(t)
	if !isGitRepo(repo) {
		t.Error("expected isGitRepo=true for initialized repo")
	}

	nonRepo := t.TempDir()
	if isGitRepo(nonRepo) {
		t.Error("expected isGitRepo=false for non-repo dir")
	}
}

func TestCurrentBranch(t *testing.T) {
	repo := initTestRepo(t)

	branch, err := currentBranch(repo)
	if err != nil {
		t.Fatalf("currentBranch: %v", err)
	}
	// Could be "main" or "master" depending on git defaults
	if branch != "main" && branch != "master" {
		t.Errorf("expected main or master, got %s", branch)
	}
}

func TestWorktreeAdd_Remove(t *testing.T) {
	repo := initTestRepo(t)
	wtPath := filepath.Join(repo, ".worktrees", "test-worker")

	err := worktreeAdd(repo, wtPath, "pair/test-worker", "")
	if err != nil {
		t.Fatalf("worktreeAdd: %v", err)
	}

	// Verify worktree directory exists
	if _, err := os.Stat(wtPath); os.IsNotExist(err) {
		t.Fatal("worktree directory should exist")
	}

	// Verify branch was created
	if !branchExists(repo, "pair/test-worker") {
		t.Error("expected branch pair/test-worker to exist")
	}

	// Verify worktree is listed
	paths, err := worktreeList(repo)
	if err != nil {
		t.Fatalf("worktreeList: %v", err)
	}
	found := false
	for _, p := range paths {
		if strings.HasSuffix(p, "test-worker") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("worktree not found in list: %v", paths)
	}

	// Remove worktree
	err = worktreeRemove(repo, wtPath, false)
	if err != nil {
		t.Fatalf("worktreeRemove: %v", err)
	}
}

func TestWorktreeAdd_WithBaseBranch(t *testing.T) {
	repo := initTestRepo(t)

	// Create a new branch to use as base
	cmd := exec.Command("git", "branch", "develop")
	cmd.Dir = repo
	cmd.Run()

	wtPath := filepath.Join(repo, ".worktrees", "feature")
	err := worktreeAdd(repo, wtPath, "pair/feature", "develop")
	if err != nil {
		t.Fatalf("worktreeAdd with base: %v", err)
	}
	defer worktreeRemove(repo, wtPath, true)

	if !branchExists(repo, "pair/feature") {
		t.Error("expected branch pair/feature to exist")
	}
}

func TestBranchExists_Delete(t *testing.T) {
	repo := initTestRepo(t)

	if branchExists(repo, "nonexistent") {
		t.Error("expected nonexistent branch to not exist")
	}

	// Create a branch
	cmd := exec.Command("git", "branch", "test-branch")
	cmd.Dir = repo
	cmd.Run()

	if !branchExists(repo, "test-branch") {
		t.Error("expected test-branch to exist")
	}

	err := branchDelete(repo, "test-branch")
	if err != nil {
		t.Fatalf("branchDelete: %v", err)
	}

	if branchExists(repo, "test-branch") {
		t.Error("expected test-branch to be deleted")
	}
}

func TestDetectSetupCommands(t *testing.T) {
	dir := t.TempDir()

	// No setup files
	cmds := detectSetupCommands(dir)
	if len(cmds) != 0 {
		t.Errorf("expected no setup commands for empty dir, got %v", cmds)
	}

	// Go project
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n"), 0644)
	cmds = detectSetupCommands(dir)
	if len(cmds) != 1 || cmds[0] != "go mod download" {
		t.Errorf("expected ['go mod download'], got %v", cmds)
	}

	// Node project with yarn
	os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0644)
	os.WriteFile(filepath.Join(dir, "yarn.lock"), []byte(""), 0644)
	cmds = detectSetupCommands(dir)
	if len(cmds) != 2 {
		t.Errorf("expected 2 setup commands (go + yarn), got %v", cmds)
	}
}

func TestWorktreePrune(t *testing.T) {
	repo := initTestRepo(t)

	// Prune should succeed even with no stale worktrees
	if err := worktreePrune(repo); err != nil {
		t.Fatalf("worktreePrune: %v", err)
	}
}

func TestFileExists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "exists.txt")
	os.WriteFile(path, []byte("hello"), 0644)

	if !fileExists(path) {
		t.Error("expected fileExists=true for existing file")
	}
	if fileExists(filepath.Join(dir, "nope.txt")) {
		t.Error("expected fileExists=false for non-existing file")
	}
}
