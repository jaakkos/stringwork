package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jaakkos/stringwork/internal/domain"
	"github.com/jaakkos/stringwork/internal/policy"
	"github.com/jaakkos/stringwork/internal/worktree"
)

const (
	defaultWorkerCooldown = 30 * time.Second
	workerLockfileStale   = 5 * time.Minute
	defaultWorkerTimeout  = 5 * time.Minute
	defaultWorkerRetries  = 2
	defaultWorkerRetryDel = 15 * time.Second
)

// WorkerSpawnConfig is a single spawnable worker (one instance).
type WorkerSpawnConfig struct {
	InstanceID string // e.g. "claude-code-1", "codex"
	AgentType  string // e.g. "claude-code", "codex"
	Command    []string
	Cooldown   time.Duration
	Timeout    time.Duration
	RetryDelay time.Duration
	MaxRetries int
	Env        map[string]string // additional env vars for this worker
	InheritEnv []string          // glob patterns for env var names to inherit (empty = all)
}

// MCPServerEntry is a single MCP server configuration for worker CLI registration.
type MCPServerEntry struct {
	Name    string
	URL     string            // URL-based server
	Command string            // command-based (stdio) server
	Args    []string          // command arguments
	Env     map[string]string // command environment
}

// WorkerManager spawns and tracks worker instances from orchestration config (instance IDs, e.g. claude-code-1, claude-code-2).
type WorkerManager struct {
	configs        []WorkerSpawnConfig
	getAgent       func() string
	repo           StateRepository
	stateMutator   func(func(*domain.CollabState) error) error
	fallbackDir    string
	logger         *log.Logger
	mu             sync.Mutex
	lastSpawn      map[string]time.Time           // instanceID -> last successful spawn
	runningWorkers map[string]context.CancelFunc   // instanceID -> cancel func for spawned process
	sessionChecker func(instanceOrType string) bool
	// mcpServerURL when set (HTTP mode): used to register MCP server with worker CLIs.
	mcpServerURL string
	// mcpServers are additional MCP servers to auto-register with worker CLIs.
	mcpServers []MCPServerEntry
	// mcpReady caches the MCP readiness result. Once the health endpoint responds, the server is in-process and stays ready.
	mcpReady bool
	// mcpRegistered caches which agent types have been verified/registered with their CLI tools.
	mcpRegistered map[string]bool
	// worktreeManager creates isolated git worktrees per worker instance.
	worktreeManager *worktree.Manager
	// processActivity tracks when each worker process last produced output.
	processActivity map[string]*ProcessInfo
}

// ProcessInfo holds runtime process metadata for a worker instance.
type ProcessInfo struct {
	InstanceID     string    `json:"instance_id"`
	StartedAt      time.Time `json:"started_at"`
	LastOutputAt   time.Time `json:"last_output_at"`
	OutputBytes    int64     `json:"output_bytes"`
	WorkspaceDir   string    `json:"workspace_dir"`
}

// NewWorkerManager creates a WorkerManager from orchestration config. Workers are built from orch.Workers only.
func NewWorkerManager(orch *policy.OrchestrationConfig, getAgent func() string, repo StateRepository, stateMutator func(func(*domain.CollabState) error) error, fallbackDir string, logger *log.Logger) *WorkerManager {
	var configs []WorkerSpawnConfig
	if orch != nil {
		for _, w := range orch.Workers {
			n := w.Instances
			if n <= 0 {
				n = 1
			}
			cooldown := defaultWorkerCooldown
			if w.CooldownSeconds > 0 {
				cooldown = time.Duration(w.CooldownSeconds) * time.Second
			}
			timeout := defaultWorkerTimeout
			if w.TimeoutSeconds > 0 {
				timeout = time.Duration(w.TimeoutSeconds) * time.Second
			}
			retryDelay := defaultWorkerRetryDel
			if w.RetryDelaySeconds > 0 {
				retryDelay = time.Duration(w.RetryDelaySeconds) * time.Second
			}
			maxRetries := defaultWorkerRetries
			if w.MaxRetries > 0 {
				maxRetries = w.MaxRetries
			}
			for i := 0; i < n; i++ {
				instanceID := w.Type
				if n > 1 {
					instanceID = fmt.Sprintf("%s-%d", w.Type, i+1)
				}
				configs = append(configs, WorkerSpawnConfig{
					InstanceID: instanceID,
					AgentType:  w.Type,
					Command:    w.Command,
					Cooldown:   cooldown,
					Timeout:    timeout,
					RetryDelay: retryDelay,
					MaxRetries: maxRetries,
					Env:        w.Env,
					InheritEnv: w.InheritEnv,
				})
			}
		}
	}
	return &WorkerManager{
		configs:         configs,
		getAgent:        getAgent,
		repo:            repo,
		stateMutator:    stateMutator,
		fallbackDir:     fallbackDir,
		logger:          logger,
		lastSpawn:       make(map[string]time.Time),
		runningWorkers:  make(map[string]context.CancelFunc),
		mcpRegistered:   make(map[string]bool),
		processActivity: make(map[string]*ProcessInfo),
	}
}

// SetSessionChecker sets a function that returns true if an instance/agent has an active MCP session.
func (m *WorkerManager) SetSessionChecker(fn func(string) bool) {
	m.sessionChecker = fn
}

// SetMCPServerURL sets the MCP server URL (e.g. http://localhost:8943/mcp) for auto-registering MCP with worker CLIs.
// When set, spawned workers (Claude Code, Codex) get the stringwork MCP server registered via their CLI tools.
// Only used when the server runs in HTTP/daemon mode. Leave unset for stdio-only.
func (m *WorkerManager) SetMCPServerURL(url string) {
	m.mcpServerURL = strings.TrimSuffix(url, "/")
}

// GetProcessInfo returns process activity info for all running workers.
func (m *WorkerManager) GetProcessInfo() map[string]ProcessInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make(map[string]ProcessInfo, len(m.processActivity))
	for k, v := range m.processActivity {
		if v != nil {
			result[k] = *v
		}
	}
	return result
}

// activityWriter wraps an io.Writer and records when writes happen for process monitoring.
type activityWriter struct {
	inner    *os.File
	mu       *sync.Mutex
	info     *ProcessInfo
}

func (w *activityWriter) Write(p []byte) (int, error) {
	n, err := w.inner.Write(p)
	if n > 0 {
		w.mu.Lock()
		w.info.LastOutputAt = time.Now()
		w.info.OutputBytes += int64(n)
		w.mu.Unlock()
	}
	return n, err
}

// SetWorktreeManager sets the worktree manager for per-worker git isolation.
func (m *WorkerManager) SetWorktreeManager(wm *worktree.Manager) {
	m.worktreeManager = wm
}

// WorktreeManager returns the worktree manager, if set.
func (m *WorkerManager) WorktreeManager() *worktree.Manager {
	return m.worktreeManager
}

// SetMCPServers sets additional MCP servers for auto-registration with worker CLIs.
func (m *WorkerManager) SetMCPServers(servers []MCPServerEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(servers) == 0 {
		m.mcpServers = nil
		return
	}
	m.mcpServers = append([]MCPServerEntry(nil), servers...)
}

// checkMCPReady verifies that the MCP HTTP endpoint is reachable (HTTP mode only).
// Returns true if no URL is set (stdio mode) or if the health endpoint responds.
// Once ready, the result is cached — the server is in-process and stays ready.
func (m *WorkerManager) checkMCPReady() bool {
	if m.mcpServerURL == "" {
		return true // stdio mode – no HTTP check needed
	}
	m.mu.Lock()
	if m.mcpReady {
		m.mu.Unlock()
		return true
	}
	m.mu.Unlock()

	// Derive health URL from the MCP server URL (e.g. http://localhost:8943/mcp -> http://localhost:8943/health)
	base := m.mcpServerURL
	if idx := strings.LastIndex(base, "/mcp"); idx >= 0 {
		base = base[:idx]
	}
	healthURL := strings.TrimSuffix(base, "/") + "/health"
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(healthURL)
	if err != nil {
		m.logger.Printf("WorkerManager: MCP not ready (%s): %v", healthURL, err)
		return false
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		m.logger.Printf("WorkerManager: MCP not ready (%s): status %d", healthURL, resp.StatusCode)
		return false
	}

	// Cache the result — server is in-process and won't go away
	m.mu.Lock()
	m.mcpReady = true
	m.mu.Unlock()
	return true
}

// StartupCheck runs a check after a short delay to pick up pending work after server start.
// In HTTP mode, it waits for the MCP endpoint to become reachable before spawning workers.
func (m *WorkerManager) StartupCheck() {
	if len(m.configs) == 0 {
		return
	}
	go func() {
		// Wait for MCP endpoint readiness (up to 15 seconds) before spawning workers.
		const maxWait = 15 * time.Second
		const pollInterval = 500 * time.Millisecond
		deadline := time.Now().Add(maxWait)
		for time.Now().Before(deadline) {
			if m.checkMCPReady() {
				break
			}
			time.Sleep(pollInterval)
		}
		if !m.checkMCPReady() {
			m.logger.Printf("WorkerManager: startup check skipped – MCP endpoint not ready after %s", maxWait)
			return
		}
		m.logger.Printf("WorkerManager: startup recovery check (MCP ready)")
		m.Check()
	}()
}

// Check examines state and spawns workers for instances that have unread messages or pending tasks.
// In HTTP mode, skips spawning if the MCP endpoint is not reachable.
func (m *WorkerManager) Check() {
	if len(m.configs) == 0 {
		return
	}
	if !m.checkMCPReady() {
		return
	}
	connected := m.getAgent()
	state, err := m.repo.Load()
	if err != nil {
		return
	}
	EnsureStateMaps(state)
	EnsureAgentInstances(state, nil)

	unreadFor := make(map[string]int)
	pendingFor := make(map[string]int)
	agentTypes := make(map[string]struct{})
	for _, c := range m.configs {
		agentTypes[c.AgentType] = struct{}{}
	}
	for _, msg := range state.Messages {
		if msg.Read {
			continue
		}
		if msg.To == "all" {
			for typ := range agentTypes {
				unreadFor[typ]++
			}
			continue
		}
		unreadFor[msg.To]++
	}
	for _, t := range state.Tasks {
		if t.Status != "pending" {
			continue
		}
		if t.AssignedTo == "any" {
			for typ := range agentTypes {
				pendingFor[typ]++
			}
			continue
		}
		pendingFor[t.AssignedTo]++
	}

	workspace := m.resolveWorkspace(state)

	for _, c := range m.configs {
		if c.InstanceID == connected || c.AgentType == connected {
			continue
		}
		hasWork := (unreadFor[c.AgentType] > 0 || pendingFor[c.AgentType] > 0) || (unreadFor[c.InstanceID] > 0 || pendingFor[c.InstanceID] > 0)
		if !hasWork {
			continue
		}
		if m.sessionChecker != nil && (m.sessionChecker(c.InstanceID) || m.sessionChecker(c.AgentType)) {
			continue
		}
		if len(c.Command) == 0 {
			continue
		}
		if !m.cooldownElapsed(c.InstanceID, c.Cooldown) {
			continue
		}
		if !m.acquireLock(c.InstanceID) {
			continue
		}
		unread := unreadFor[c.AgentType] + unreadFor[c.InstanceID]
		pending := pendingFor[c.AgentType] + pendingFor[c.InstanceID]

		// Use worktree isolation if configured and workspace is a git repo
		spawnDir := workspace
		if m.worktreeManager != nil {
			wtPath, err := m.worktreeManager.EnsureWorktree(c.InstanceID, workspace)
			if err != nil {
				m.logger.Printf("WorkerManager: worktree failed for %s: %v (falling back to shared dir)", c.InstanceID, err)
			} else {
				spawnDir = wtPath
			}
		}

		m.logger.Printf("WorkerManager: spawning %s (%d unread, %d pending, workspace=%s)", c.InstanceID, unread, pending, spawnDir)
		m.sendAck(c.InstanceID, connected, unread, pending)
		go m.spawn(c, spawnDir)
	}
}

// CancelWorker kills a running worker process by cancelling its context.
// Returns true if the worker was running and has been signalled to stop.
func (m *WorkerManager) CancelWorker(instanceID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Try exact instance ID first
	if cancel, ok := m.runningWorkers[instanceID]; ok {
		cancel()
		m.logger.Printf("WorkerManager: cancelled worker %s", instanceID)
		// Cleanup worktree if strategy is "on_cancel"
		if m.worktreeManager != nil && m.worktreeManager.CleanupStrategy() == "on_cancel" {
			go func() {
				if err := m.worktreeManager.CleanupWorktree(instanceID, m.fallbackDir); err != nil {
					m.logger.Printf("WorkerManager: worktree cleanup for %s: %v", instanceID, err)
				}
			}()
		}
		return true
	}
	// Try matching by agent type (e.g. "claude-code" matches "claude-code-1")
	for id, cancel := range m.runningWorkers {
		for _, c := range m.configs {
			if c.InstanceID == id && c.AgentType == instanceID {
				cancel()
				m.logger.Printf("WorkerManager: cancelled worker %s (matched type %s)", id, instanceID)
				// Cleanup worktree if strategy is "on_cancel"
				if m.worktreeManager != nil && m.worktreeManager.CleanupStrategy() == "on_cancel" {
					cancelledID := id
					go func() {
						if err := m.worktreeManager.CleanupWorktree(cancelledID, m.fallbackDir); err != nil {
							m.logger.Printf("WorkerManager: worktree cleanup for %s: %v", cancelledID, err)
						}
					}()
				}
				return true
			}
		}
	}
	return false
}

// RestartWorkers cancels all running worker processes, clears cooldown timers,
// and triggers a check to respawn them. Returns the list of instance IDs that were killed.
func (m *WorkerManager) RestartWorkers() []string {
	m.mu.Lock()
	// Collect and cancel all running workers
	var killed []string
	for id, cancel := range m.runningWorkers {
		cancel()
		killed = append(killed, id)
		m.logger.Printf("WorkerManager: restart — killed %s", id)
	}
	// Clear cooldown timers so workers can respawn immediately
	m.lastSpawn = make(map[string]time.Time)
	m.mu.Unlock()

	// Brief pause for processes to exit before respawning
	time.Sleep(500 * time.Millisecond)

	// Trigger a check to respawn workers
	m.Check()

	return killed
}

// RunningWorkers returns the instance IDs of currently running worker processes.
func (m *WorkerManager) RunningWorkers() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.runningWorkers))
	for id := range m.runningWorkers {
		ids = append(ids, id)
	}
	return ids
}

// IsWorkerRunning returns true if a spawned worker process is currently running.
func (m *WorkerManager) IsWorkerRunning(instanceID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.runningWorkers[instanceID]; ok {
		return true
	}
	for id := range m.runningWorkers {
		for _, c := range m.configs {
			if c.InstanceID == id && c.AgentType == instanceID {
				return true
			}
		}
	}
	return false
}

func (m *WorkerManager) cooldownElapsed(instanceID string, cooldown time.Duration) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	last, ok := m.lastSpawn[instanceID]
	return !ok || time.Since(last) >= cooldown
}

func (m *WorkerManager) resolveWorkspace(state *domain.CollabState) string {
	connected := m.getAgent()
	if connected != "" {
		if p, ok := state.Presence[connected]; ok && p != nil && p.Workspace != "" {
			return p.Workspace
		}
	}
	for _, p := range state.Presence {
		if p != nil && p.Workspace != "" {
			return p.Workspace
		}
	}
	for _, ra := range state.RegisteredAgents {
		if ra != nil && ra.Workspace != "" {
			return ra.Workspace
		}
	}
	return m.fallbackDir
}

func (m *WorkerManager) spawn(c WorkerSpawnConfig, workspaceDir string) {
	defer m.releaseLock(c.InstanceID)
	retryDelay := c.RetryDelay
	var lastErr error
	for attempt := 0; attempt <= c.MaxRetries; attempt++ {
		if attempt > 0 {
			m.logger.Printf("WorkerManager: %s retry %d/%d after %s", c.InstanceID, attempt, c.MaxRetries, retryDelay)
			time.Sleep(retryDelay)
			retryDelay *= 2
			if retryDelay > 2*time.Minute {
				retryDelay = 2 * time.Minute
			}
		}
		lastErr = m.runOnce(c, workspaceDir, attempt)
		if lastErr == nil {
			m.mu.Lock()
			m.lastSpawn[c.InstanceID] = time.Now()
			m.mu.Unlock()
			return
		}
		m.logger.Printf("WorkerManager: %s attempt %d failed: %v", c.InstanceID, attempt+1, lastErr)
	}
	m.logger.Printf("WorkerManager: %s failed after %d attempts", c.InstanceID, c.MaxRetries+1)
	m.sendFailureAck(c.InstanceID, lastErr, c.MaxRetries+1)
}

// buildWorkerEnv constructs the environment for a spawned worker process.
// It handles three layers:
//  1. Base: inherited from parent process (filtered by InheritEnv patterns if set)
//  2. STRINGWORK_AGENT and STRINGWORK_WORKSPACE always injected
//  3. Config env vars merged on top (with ${VAR} expansion from parent env)
func buildWorkerEnv(c WorkerSpawnConfig, workspaceDir string) []string {
	parentEnv := os.Environ()
	parentMap := make(map[string]string, len(parentEnv))
	for _, e := range parentEnv {
		if k, v, ok := strings.Cut(e, "="); ok {
			parentMap[k] = v
		}
	}

	var base []string
	if len(c.InheritEnv) == 1 && strings.ToLower(c.InheritEnv[0]) == "none" {
		// Clean environment: inherit nothing
		base = nil
	} else if len(c.InheritEnv) > 0 {
		// Selective inheritance: only pass vars matching patterns
		for _, e := range parentEnv {
			k, _, ok := strings.Cut(e, "=")
			if !ok {
				continue
			}
			for _, pattern := range c.InheritEnv {
				if matchEnvGlob(pattern, k) {
					base = append(base, e)
					break
				}
			}
		}
	} else {
		// Default: inherit everything
		base = append([]string(nil), parentEnv...)
	}

	// Always inject our own vars
	base = append(base, "STRINGWORK_AGENT="+c.InstanceID, "STRINGWORK_WORKSPACE="+workspaceDir)

	// Merge config env vars (with ${VAR} expansion)
	for k, v := range c.Env {
		expanded := os.Expand(v, func(key string) string {
			if val, ok := parentMap[key]; ok {
				return val
			}
			return ""
		})
		base = setEnvVar(base, k, expanded)
	}

	return base
}

// setEnvVar sets or replaces an env var in a []string env slice.
func setEnvVar(env []string, key, value string) []string {
	prefix := key + "="
	for i, e := range env {
		if strings.HasPrefix(e, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

// matchEnvGlob matches an env var name against a glob pattern.
// Supports * (match any chars) and ? (match single char).
func matchEnvGlob(pattern, name string) bool {
	matched, _ := filepath.Match(pattern, name)
	return matched
}

func expandWorkerTemplates(args []string, agent, workspace string) []string {
	replacer := strings.NewReplacer("{workspace}", workspace, "{agent}", agent)
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = replacer.Replace(a)
	}
	return out
}

func isClaudeCommand(exe string) bool {
	base := filepath.Base(exe)
	return base == "claude" || strings.Contains(strings.ToLower(exe), "claude")
}

func isCodexCommand(exe string) bool {
	base := filepath.Base(exe)
	return base == "codex" || strings.Contains(strings.ToLower(exe), "codex")
}

// mcpBaseURL extracts the scheme+host+port from a URL (e.g. "http://localhost:8943/mcp" -> "http://localhost:8943").
func mcpBaseURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return rawURL
	}
	return u.Scheme + "://" + u.Host
}

// ensureMCPRegistered checks if configured MCP servers are registered with the worker's CLI
// tool (claude or codex), and adds them via CLI if missing or pointing to a different server.
// The result is cached per agent type — each type is checked only once per server lifetime.
func (m *WorkerManager) ensureMCPRegistered(agentType, exe string) error {
	servers := m.mcpServerEntries()
	if len(servers) == 0 {
		return nil // stdio mode, no registration needed
	}

	m.mu.Lock()
	if m.mcpRegistered[agentType] {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()

	switch {
	case isClaudeCommand(exe):
		for _, server := range servers {
			if isClaudeMCPConfigured(server.Name, server) {
				m.logger.Printf("WorkerManager: MCP %q already registered with %s CLI", server.Name, agentType)
				continue
			}
			m.logger.Printf("WorkerManager: registering MCP %q with %s CLI...", server.Name, agentType)
			if err := registerMCPViaClaude(exe, server, m.logger); err != nil {
				return fmt.Errorf("failed to register MCP %q with %s CLI: %w", server.Name, agentType, err)
			}
			m.logger.Printf("WorkerManager: MCP %q registered with %s CLI", server.Name, agentType)
		}
	case isCodexCommand(exe):
		for _, server := range servers {
			if isCodexMCPConfigured(server.Name, server) {
				m.logger.Printf("WorkerManager: MCP %q already registered with %s CLI", server.Name, agentType)
				continue
			}
			m.logger.Printf("WorkerManager: registering MCP %q with %s CLI...", server.Name, agentType)
			if err := registerMCPViaCodex(exe, server, m.logger); err != nil {
				return fmt.Errorf("failed to register MCP %q with %s CLI: %w", server.Name, agentType, err)
			}
			m.logger.Printf("WorkerManager: MCP %q registered with %s CLI", server.Name, agentType)
		}
	default:
		return nil // unknown CLI, skip
	}

	m.mu.Lock()
	m.mcpRegistered[agentType] = true
	m.mu.Unlock()
	return nil
}

func (m *WorkerManager) mcpServerEntries() []MCPServerEntry {
	m.mu.Lock()
	defer m.mu.Unlock()

	var entries []MCPServerEntry
	seen := make(map[string]struct{})

	if m.mcpServerURL != "" {
		entries = append(entries, MCPServerEntry{
			Name: "stringwork",
			URL:  m.mcpServerURL,
		})
		seen["stringwork"] = struct{}{}
	}
	for _, s := range m.mcpServers {
		if s.Name == "" {
			continue
		}
		if _, ok := seen[s.Name]; ok {
			continue
		}
		entries = append(entries, s)
		seen[s.Name] = struct{}{}
	}
	return entries
}

// isClaudeMCPConfigured checks ~/.claude.json for a named entry matching the target config.
func isClaudeMCPConfigured(name string, entry MCPServerEntry) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	data, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		return false
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return false
	}
	servers, _ := cfg["mcpServers"].(map[string]interface{})
	serverCfg, _ := servers[name].(map[string]interface{})
	if len(serverCfg) == 0 {
		return false
	}

	if entry.URL != "" {
		existingURL, _ := serverCfg["url"].(string)
		if existingURL == "" {
			return false
		}
		// Exact URL match required. Different paths (e.g. /mcp vs /sse) use different
		// protocols — Codex's rmcp only supports streamable HTTP (/mcp), not SSE.
		return strings.TrimSuffix(existingURL, "/") == strings.TrimSuffix(entry.URL, "/")
	}

	if entry.Command == "" {
		return false
	}
	cmd, _ := serverCfg["command"].(string)
	if cmd != entry.Command {
		return false
	}
	if len(entry.Args) > 0 {
		rawArgs, _ := serverCfg["args"].([]interface{})
		if len(rawArgs) != len(entry.Args) {
			return false
		}
		for i, want := range entry.Args {
			got, _ := rawArgs[i].(string)
			if got != want {
				return false
			}
		}
	}
	if len(entry.Env) > 0 {
		rawEnv, _ := serverCfg["env"].(map[string]interface{})
		if len(rawEnv) == 0 {
			return false
		}
		for k, want := range entry.Env {
			got, _ := rawEnv[k].(string)
			if got != want {
				return false
			}
		}
	}
	return true
}

// isCodexMCPConfigured checks ~/.codex/config.toml for a named entry matching the target config.
func isCodexMCPConfigured(name string, entry MCPServerEntry) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	data, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		return false
	}
	content := string(data)
	section := fmt.Sprintf("[mcp_servers.%s]", name)
	idx := strings.Index(content, section)
	if idx < 0 {
		return false
	}
	// Extract just this section (up to next "[" header or EOF) to avoid false positives
	// from other sections containing the same URL/command.
	sectionBody := content[idx+len(section):]
	if nextSect := strings.Index(sectionBody, "\n["); nextSect >= 0 {
		sectionBody = sectionBody[:nextSect]
	}
	if entry.URL != "" {
		// Exact URL match required. Different paths (e.g. /mcp vs /sse) use different
		// protocols — Codex's rmcp only supports streamable HTTP (/mcp), not SSE.
		return strings.Contains(sectionBody, fmt.Sprintf(`url = "%s"`, entry.URL))
	}
	if entry.Command == "" {
		return false
	}
	if !strings.Contains(sectionBody, fmt.Sprintf(`command = "%s"`, entry.Command)) {
		return false
	}
	for _, arg := range entry.Args {
		if !strings.Contains(sectionBody, fmt.Sprintf(`"%s"`, arg)) {
			return false
		}
	}
	return true
}

// registerMCPViaClaude uses "claude mcp add-json --scope user" to register a server.
func registerMCPViaClaude(exe string, entry MCPServerEntry, logger *log.Logger) error {
	cfg := map[string]interface{}{}
	if entry.URL != "" {
		cfg["type"] = "http"
		cfg["url"] = entry.URL
	} else {
		cfg["type"] = "stdio"
		cfg["command"] = entry.Command
		if len(entry.Args) > 0 {
			cfg["args"] = entry.Args
		}
		if len(entry.Env) > 0 {
			cfg["env"] = entry.Env
		}
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal claude mcp config: %w", err)
	}
	cfgJSON := string(data)

	// Remove existing entry (ignore errors — may not exist)
	ctx1, cancel1 := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel1()
	_ = exec.CommandContext(ctx1, exe, "mcp", "remove", "--scope", "user", entry.Name).Run()

	// Add new entry
	ctx2, cancel2 := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel2()
	cmd := exec.CommandContext(ctx2, exe, "mcp", "add-json", "--scope", "user", entry.Name, cfgJSON)
	output, err := cmd.CombinedOutput()
	if err != nil {
		logger.Printf("WorkerManager: claude mcp add-json output: %s", strings.TrimSpace(string(output)))
		return fmt.Errorf("claude mcp add-json: %w", err)
	}
	return nil
}

// registerMCPViaCodex uses "codex mcp add" to register a server.
func registerMCPViaCodex(exe string, entry MCPServerEntry, logger *log.Logger) error {
	// Remove existing entry (ignore errors — may not exist)
	ctx1, cancel1 := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel1()
	_ = exec.CommandContext(ctx1, exe, "mcp", "remove", entry.Name).Run()

	// Add new entry
	ctx2, cancel2 := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel2()
	args := []string{"mcp", "add", entry.Name}
	if entry.URL != "" {
		args = append(args, "--url", entry.URL)
	} else {
		args = append(args, "--", entry.Command)
		args = append(args, entry.Args...)
	}
	cmd := exec.CommandContext(ctx2, exe, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		logger.Printf("WorkerManager: codex mcp add output: %s", strings.TrimSpace(string(output)))
		return fmt.Errorf("codex mcp add: %w", err)
	}
	return nil
}

func (m *WorkerManager) runOnce(c WorkerSpawnConfig, workspaceDir string, attempt int) error {
	ctx, cancel := context.WithTimeout(context.Background(), c.Timeout)
	defer cancel()

	// Track the cancel func so CancelWorker can kill this process.
	m.mu.Lock()
	m.runningWorkers[c.InstanceID] = cancel
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.runningWorkers, c.InstanceID)
		m.mu.Unlock()
	}()
	args := expandWorkerTemplates(c.Command, c.InstanceID, workspaceDir)
	if len(args) == 0 {
		return fmt.Errorf("empty command")
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = workspaceDir
	env := buildWorkerEnv(c, workspaceDir)
	// Ensure the worker's CLI tool has configured MCP servers registered (claude/codex).
	if m.mcpServerURL != "" || len(m.mcpServers) > 0 {
		if err := m.ensureMCPRegistered(c.AgentType, args[0]); err != nil {
			m.logger.Printf("WorkerManager: MCP registration warning for %s: %v", c.InstanceID, err)
		}
	}
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	logPath := filepath.Join(policy.GlobalStateDir(), fmt.Sprintf("stringwork-worker-%s.log", strings.ReplaceAll(c.InstanceID, "/", "-")))
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)

	// Set up process activity tracking
	pInfo := &ProcessInfo{
		InstanceID:   c.InstanceID,
		StartedAt:    time.Now(),
		LastOutputAt: time.Now(),
		WorkspaceDir: workspaceDir,
	}
	m.mu.Lock()
	m.processActivity[c.InstanceID] = pInfo
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.processActivity, c.InstanceID)
		m.mu.Unlock()
	}()

	if err != nil {
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
	} else {
		defer logFile.Close()
		label := "spawn"
		if attempt > 0 {
			label = fmt.Sprintf("retry-%d", attempt)
		}
		fmt.Fprintf(logFile, "\n=== Worker %s [%s] at %s (dir=%s) ===\n", c.InstanceID, label, time.Now().Format(time.RFC3339), workspaceDir)
		fmt.Fprintf(logFile, "Command: %v\n", args)
		aw := &activityWriter{inner: logFile, mu: &m.mu, info: pInfo}
		cmd.Stdout = aw
		cmd.Stderr = aw
	}
	start := time.Now()
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("timed out after %s", c.Timeout)
		}
		return fmt.Errorf("exited after %s: %w", time.Since(start).Round(time.Millisecond), err)
	}
	m.logger.Printf("WorkerManager: %s completed in %s", c.InstanceID, time.Since(start).Round(time.Millisecond))
	m.reconcileAfterExit(c)
	return nil
}

// reconcileAfterExit checks for tasks stuck in "in_progress" after a worker exits.
// If a worker couldn't communicate back (e.g. sandbox blocks MCP), this ensures
// tasks don't stay orphaned. Stuck tasks are reset to "pending" for driver review.
// Also cleans up worktrees if the strategy is "on_exit".
func (m *WorkerManager) reconcileAfterExit(c WorkerSpawnConfig) {
	// Cleanup worktree if strategy is "on_exit"
	if m.worktreeManager != nil && m.worktreeManager.CleanupStrategy() == "on_exit" {
		if err := m.worktreeManager.CleanupWorktree(c.InstanceID, m.fallbackDir); err != nil {
			m.logger.Printf("WorkerManager: worktree cleanup on exit for %s: %v", c.InstanceID, err)
		}
	}

	if m.stateMutator == nil {
		return
	}
	_ = m.stateMutator(func(s *domain.CollabState) error {
		reconciled := 0
		for i := range s.Tasks {
			t := &s.Tasks[i]
			if t.Status != "in_progress" {
				continue
			}
			if t.AssignedTo != c.InstanceID && t.AssignedTo != c.AgentType {
				continue
			}
			// Mark as "pending" (not "completed") so the driver can re-assign or verify.
			// We don't know if the worker actually finished the work.
			t.Status = "pending"
			t.UpdatedAt = time.Now()
			if t.ResultSummary == "" {
				t.ResultSummary = fmt.Sprintf("Worker %s exited without updating status. Check worker log for details.", c.InstanceID)
			}
			// Clean up the worker instance's task list
			if inst, ok := s.AgentInstances[c.InstanceID]; ok && inst != nil {
				newTasks := make([]int, 0, len(inst.CurrentTasks))
				for _, tid := range inst.CurrentTasks {
					if tid != t.ID {
						newTasks = append(newTasks, tid)
					}
				}
				inst.CurrentTasks = newTasks
				if len(inst.CurrentTasks) == 0 {
					inst.Status = "idle"
				}
			}
			reconciled++
		}
		if reconciled > 0 {
			m.logger.Printf("WorkerManager: reconciled %d stuck task(s) for %s → set to pending", reconciled, c.InstanceID)
			// Notify the driver
			driver := "cursor"
			s.Messages = append(s.Messages, domain.Message{
				ID:        s.NextMsgID,
				From:      "system",
				To:        driver,
				Content:   fmt.Sprintf("⚠️ **%s** exited with %d task(s) still in-progress — reset to pending for review. Check worker log for details.", c.InstanceID, reconciled),
				Timestamp: time.Now(),
			})
			s.NextMsgID++
		}
		return nil
	})
}

func (m *WorkerManager) sendAck(instanceID, recipient string, unread, pending int) {
	if recipient == "" || m.stateMutator == nil {
		return
	}
	detail := ""
	if unread > 0 && pending > 0 {
		detail = fmt.Sprintf("%d unread message(s), %d pending task(s)", unread, pending)
	} else if unread > 0 {
		detail = fmt.Sprintf("%d unread message(s)", unread)
	} else {
		detail = fmt.Sprintf("%d pending task(s)", pending)
	}
	content := fmt.Sprintf("⚡ **%s** is coming online (%s)...", instanceID, detail)
	_ = m.stateMutator(func(s *domain.CollabState) error {
		s.Messages = append(s.Messages, domain.Message{
			ID:        s.NextMsgID,
			From:      "system",
			To:        recipient,
			Content:   content,
			Timestamp: time.Now(),
		})
		s.NextMsgID++
		return nil
	})
}

func (m *WorkerManager) sendFailureAck(instanceID string, lastErr error, attempts int) {
	if m.stateMutator == nil {
		return
	}
	content := fmt.Sprintf("❌ **%s** failed to respond after %d attempt(s): %v", instanceID, attempts, lastErr)
	_ = m.stateMutator(func(s *domain.CollabState) error {
		recipient := ""
		for i := len(s.Messages) - 1; i >= 0; i-- {
			msg := s.Messages[i]
			if (msg.To == instanceID || msg.To == "all") && !msg.Read && msg.From != "system" {
				recipient = msg.From
				break
			}
		}
		if recipient == "" {
			recipient = "cursor"
		}
		s.Messages = append(s.Messages, domain.Message{
			ID:        s.NextMsgID,
			From:      "system",
			To:        recipient,
			Content:   content,
			Timestamp: time.Now(),
		})
		s.NextMsgID++
		return nil
	})
}

func (m *WorkerManager) lockfilePath(instanceID string) string {
	safe := strings.ReplaceAll(instanceID, "/", "-")
	return filepath.Join(os.TempDir(), fmt.Sprintf("stringwork-worker-%s.lock", safe))
}

func (m *WorkerManager) acquireLock(instanceID string) bool {
	path := m.lockfilePath(instanceID)
	if info, err := os.Stat(path); err == nil {
		if time.Since(info.ModTime()) > workerLockfileStale {
			m.logger.Printf("WorkerManager: removing stale lock for %s", instanceID)
			_ = os.Remove(path)
		} else {
			return false
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return false
	}
	_, _ = fmt.Fprintf(f, "%d", os.Getpid())
	_ = f.Close()
	return true
}

func (m *WorkerManager) releaseLock(instanceID string) {
	_ = os.Remove(m.lockfilePath(instanceID))
}
