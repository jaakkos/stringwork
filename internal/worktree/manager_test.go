package worktree

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jaakkos/stringwork/internal/policy"
)

func testManager(t *testing.T, repo string) *Manager {
	t.Helper()
	logger := log.New(os.Stderr, "[test] ", log.LstdFlags)
	return NewManager(&policy.WorktreeConfig{
		Enabled:         true,
		BaseBranch:      "",
		CleanupStrategy: "on_cancel",
		Path:            ".stringwork/worktrees",
	}, logger)
}

func TestManager_IsEnabled(t *testing.T) {
	logger := log.New(os.Stderr, "[test] ", log.LstdFlags)

	// Disabled by default
	m := NewManager(&policy.WorktreeConfig{Enabled: false}, logger)
	if m.IsEnabled() {
		t.Error("expected IsEnabled=false")
	}

	// Enabled
	m = NewManager(&policy.WorktreeConfig{Enabled: true}, logger)
	if !m.IsEnabled() {
		t.Error("expected IsEnabled=true")
	}

	// Nil config
	m = NewManager(nil, logger)
	if m.IsEnabled() {
		t.Error("expected IsEnabled=false for nil config")
	}
}

func TestManager_EnsureWorktree(t *testing.T) {
	repo := initTestRepo(t)
	m := testManager(t, repo)

	wtPath, err := m.EnsureWorktree("claude-code-1", repo)
	if err != nil {
		t.Fatalf("EnsureWorktree: %v", err)
	}

	expected := filepath.Join(repo, ".stringwork", "worktrees", "claude-code-1")
	if wtPath != expected {
		t.Errorf("expected path %s, got %s", expected, wtPath)
	}

	// Verify directory exists
	if _, err := os.Stat(wtPath); os.IsNotExist(err) {
		t.Fatal("worktree directory should exist")
	}

	// Second call should return same path (idempotent)
	wtPath2, err := m.EnsureWorktree("claude-code-1", repo)
	if err != nil {
		t.Fatalf("second EnsureWorktree: %v", err)
	}
	if wtPath2 != wtPath {
		t.Errorf("expected same path on second call, got %s vs %s", wtPath, wtPath2)
	}

	// Verify it appears in list
	wts := m.ListWorktrees()
	if len(wts) != 1 {
		t.Errorf("expected 1 worktree, got %d", len(wts))
	}
	if info, ok := wts["claude-code-1"]; !ok {
		t.Error("expected claude-code-1 in list")
	} else {
		if info.Branch != "pair/claude-code-1" {
			t.Errorf("expected branch pair/claude-code-1, got %s", info.Branch)
		}
	}

	// Clean up
	m.CleanupWorktree("claude-code-1", repo)
}

func TestManager_EnsureWorktree_NotGitRepo(t *testing.T) {
	dir := t.TempDir()
	m := testManager(t, dir)

	_, err := m.EnsureWorktree("worker-1", dir)
	if err == nil {
		t.Fatal("expected error for non-git repo")
	}
}

func TestManager_CleanupWorktree(t *testing.T) {
	repo := initTestRepo(t)
	m := testManager(t, repo)

	wtPath, err := m.EnsureWorktree("worker-1", repo)
	if err != nil {
		t.Fatalf("EnsureWorktree: %v", err)
	}

	// Verify exists
	if _, err := os.Stat(wtPath); os.IsNotExist(err) {
		t.Fatal("worktree should exist before cleanup")
	}

	err = m.CleanupWorktree("worker-1", repo)
	if err != nil {
		t.Fatalf("CleanupWorktree: %v", err)
	}

	// Verify removed from list
	wts := m.ListWorktrees()
	if len(wts) != 0 {
		t.Errorf("expected 0 worktrees after cleanup, got %d", len(wts))
	}

	// Verify branch deleted
	if branchExists(repo, "pair/worker-1") {
		t.Error("expected branch pair/worker-1 to be deleted")
	}
}

func TestManager_CleanupAll(t *testing.T) {
	repo := initTestRepo(t)
	m := testManager(t, repo)

	m.EnsureWorktree("worker-1", repo)
	m.EnsureWorktree("worker-2", repo)

	if len(m.ListWorktrees()) != 2 {
		t.Fatalf("expected 2 worktrees, got %d", len(m.ListWorktrees()))
	}

	err := m.CleanupAll(repo)
	if err != nil {
		t.Fatalf("CleanupAll: %v", err)
	}

	if len(m.ListWorktrees()) != 0 {
		t.Error("expected 0 worktrees after CleanupAll")
	}
}

func TestManager_WorktreePath(t *testing.T) {
	repo := initTestRepo(t)
	m := testManager(t, repo)

	// No worktree yet
	if p := m.WorktreePath("nope"); p != "" {
		t.Errorf("expected empty path for nonexistent, got %s", p)
	}

	m.EnsureWorktree("w1", repo)
	defer m.CleanupAll(repo)

	p := m.WorktreePath("w1")
	if p == "" {
		t.Error("expected non-empty path for existing worktree")
	}
}

func TestManager_CleanupStrategy(t *testing.T) {
	logger := log.New(os.Stderr, "[test] ", log.LstdFlags)

	m := NewManager(&policy.WorktreeConfig{CleanupStrategy: "on_exit"}, logger)
	if m.CleanupStrategy() != "on_exit" {
		t.Errorf("expected on_exit, got %s", m.CleanupStrategy())
	}

	m = NewManager(&policy.WorktreeConfig{}, logger)
	if m.CleanupStrategy() != "on_cancel" {
		t.Errorf("expected default on_cancel, got %s", m.CleanupStrategy())
	}
}

func TestManager_EnsureWorktree_StaleBranch(t *testing.T) {
	repo := initTestRepo(t)
	m := testManager(t, repo)

	// Create a stale branch manually
	cmd := exec.Command("git", "branch", "pair/stale-worker")
	cmd.Dir = repo
	cmd.Run()

	// EnsureWorktree should handle the stale branch
	wtPath, err := m.EnsureWorktree("stale-worker", repo)
	if err != nil {
		t.Fatalf("EnsureWorktree with stale branch: %v", err)
	}

	if _, err := os.Stat(wtPath); os.IsNotExist(err) {
		t.Fatal("worktree should exist")
	}

	m.CleanupAll(repo)
}

func TestIsGitRepo_Public(t *testing.T) {
	repo := initTestRepo(t)
	if !IsGitRepo(repo) {
		t.Error("expected IsGitRepo=true")
	}

	nonRepo := t.TempDir()
	if IsGitRepo(nonRepo) {
		t.Error("expected IsGitRepo=false")
	}
}
