package worktree

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jaakkos/stringwork/internal/policy"
)

// WorktreeInfo holds information about a managed worktree.
type WorktreeInfo struct {
	InstanceID string    `json:"instance_id"`
	Path       string    `json:"path"`
	Branch     string    `json:"branch"`
	BaseBranch string    `json:"base_branch"`
	CreatedAt  time.Time `json:"created_at"`
}

// Manager manages git worktrees for worker instances.
// It creates isolated checkouts per worker and cleans them up based on
// the configured cleanup strategy.
type Manager struct {
	config *policy.WorktreeConfig
	logger *log.Logger
	mu     sync.Mutex
	active map[string]*WorktreeInfo // instanceID -> info
}

// NewManager creates a new worktree Manager.
func NewManager(config *policy.WorktreeConfig, logger *log.Logger) *Manager {
	return &Manager{
		config: config,
		logger: logger,
		active: make(map[string]*WorktreeInfo),
	}
}

// IsEnabled returns true if worktree isolation is configured and enabled.
func (m *Manager) IsEnabled() bool {
	return m.config != nil && m.config.Enabled
}

// EnsureWorktree creates a worktree for the given instance if it doesn't already exist.
// Returns the worktree directory path. If the workspace is not a git repo, returns
// an error (caller should fall back to the shared workspace).
func (m *Manager) EnsureWorktree(instanceID, workspaceDir string) (string, error) {
	if !m.IsEnabled() {
		return "", fmt.Errorf("worktrees not enabled")
	}

	if !isGitRepo(workspaceDir) {
		return "", fmt.Errorf("workspace %s is not a git repository", workspaceDir)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Return existing worktree if already active
	if info, ok := m.active[instanceID]; ok {
		if fileExists(info.Path) {
			return info.Path, nil
		}
		// Path was removed externally; recreate
		delete(m.active, instanceID)
	}

	// Determine paths and branches
	worktreeRoot := m.config.Path
	if worktreeRoot == "" {
		worktreeRoot = ".stringwork/worktrees"
	}
	wtPath := filepath.Join(workspaceDir, worktreeRoot, instanceID)
	branch := "pair/" + instanceID

	// Determine base branch
	baseBranch := m.config.BaseBranch
	if baseBranch == "" {
		var err error
		baseBranch, err = currentBranch(workspaceDir)
		if err != nil {
			return "", fmt.Errorf("detect current branch: %w", err)
		}
		// If detached HEAD, can't use as base
		if baseBranch == "HEAD" {
			return "", fmt.Errorf("repository is in detached HEAD state; set base_branch in config")
		}
	}

	// If the branch already exists (stale from a previous run), delete it
	if branchExists(workspaceDir, branch) {
		// Try to prune stale worktree references first
		_ = worktreePrune(workspaceDir)
		if err := branchDelete(workspaceDir, branch); err != nil {
			m.logger.Printf("WorktreeManager: warning: could not delete stale branch %s: %v", branch, err)
		}
	}

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(wtPath), 0o755); err != nil {
		return "", fmt.Errorf("create worktree parent dir: %w", err)
	}

	// Create the worktree
	if err := worktreeAdd(workspaceDir, wtPath, branch, baseBranch); err != nil {
		return "", fmt.Errorf("create worktree: %w", err)
	}

	// Run setup commands
	setupCmds := m.config.SetupCommands
	if len(setupCmds) == 0 {
		setupCmds = detectSetupCommands(wtPath)
	}
	if len(setupCmds) > 0 {
		m.logger.Printf("WorktreeManager: running setup commands in %s: %v", wtPath, setupCmds)
		if errs := runSetupCommands(wtPath, setupCmds); len(errs) > 0 {
			for _, err := range errs {
				m.logger.Printf("WorktreeManager: setup warning: %v", err)
			}
		}
	}

	info := &WorktreeInfo{
		InstanceID: instanceID,
		Path:       wtPath,
		Branch:     branch,
		BaseBranch: baseBranch,
		CreatedAt:  time.Now(),
	}
	m.active[instanceID] = info

	m.logger.Printf("WorktreeManager: created worktree for %s at %s (branch: %s, base: %s)", instanceID, wtPath, branch, baseBranch)
	return wtPath, nil
}

// CleanupWorktree removes the worktree for a specific instance.
func (m *Manager) CleanupWorktree(instanceID, workspaceDir string) error {
	m.mu.Lock()
	info, ok := m.active[instanceID]
	if ok {
		delete(m.active, instanceID)
	}
	m.mu.Unlock()

	if !ok {
		return nil
	}

	return m.removeWorktree(info, workspaceDir)
}

// CleanupAll removes all managed worktrees. Used during server shutdown.
func (m *Manager) CleanupAll(workspaceDir string) error {
	m.mu.Lock()
	active := make(map[string]*WorktreeInfo, len(m.active))
	for k, v := range m.active {
		active[k] = v
	}
	m.active = make(map[string]*WorktreeInfo)
	m.mu.Unlock()

	var firstErr error
	for _, info := range active {
		if err := m.removeWorktree(info, workspaceDir); err != nil {
			m.logger.Printf("WorktreeManager: cleanup error for %s: %v", info.InstanceID, err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	return firstErr
}

// WorktreePath returns the worktree path for an instance, or empty string if none.
func (m *Manager) WorktreePath(instanceID string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if info, ok := m.active[instanceID]; ok {
		return info.Path
	}
	return ""
}

// ListWorktrees returns information about all active worktrees.
func (m *Manager) ListWorktrees() map[string]WorktreeInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make(map[string]WorktreeInfo, len(m.active))
	for k, v := range m.active {
		result[k] = *v
	}
	return result
}

// CleanupStrategy returns the configured cleanup strategy.
func (m *Manager) CleanupStrategy() string {
	if m.config == nil || m.config.CleanupStrategy == "" {
		return "on_cancel"
	}
	return m.config.CleanupStrategy
}

// IsGitRepo checks whether the given directory is inside a git repository.
// This is a public wrapper for the low-level function.
func IsGitRepo(dir string) bool {
	return isGitRepo(dir)
}

// removeWorktree handles the actual removal of a worktree and its branch.
func (m *Manager) removeWorktree(info *WorktreeInfo, workspaceDir string) error {
	m.logger.Printf("WorktreeManager: removing worktree for %s at %s", info.InstanceID, info.Path)

	// Remove the worktree directory via git
	if err := worktreeRemove(workspaceDir, info.Path, true); err != nil {
		// If git remove fails, try manual cleanup
		m.logger.Printf("WorktreeManager: git worktree remove failed, trying manual: %v", err)
		if err2 := os.RemoveAll(info.Path); err2 != nil {
			return fmt.Errorf("remove worktree dir: %w (git: %v)", err2, err)
		}
	}

	// Prune stale worktree references
	_ = worktreePrune(workspaceDir)

	// Delete the branch
	if branchExists(workspaceDir, info.Branch) {
		if err := branchDelete(workspaceDir, info.Branch); err != nil {
			m.logger.Printf("WorktreeManager: warning: could not delete branch %s: %v", info.Branch, err)
		}
	}

	m.logger.Printf("WorktreeManager: removed worktree for %s", info.InstanceID)
	return nil
}
