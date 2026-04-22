package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/jaakkos/stringwork/internal/domain"
	"github.com/jaakkos/stringwork/internal/policy"
	"github.com/jaakkos/stringwork/internal/worktree"
)

const (
	defaultWorkerCooldown  = 30 * time.Second
	workerLockfileStale    = 5 * time.Minute
	defaultWorkerTimeout   = 5 * time.Minute
	defaultWorkerRetries   = 2
	defaultWorkerRetryDel  = 15 * time.Second
	failureBackoffBase     = 1 * time.Minute
	failureBackoffMax      = 10 * time.Minute
	failureBackoffMaxCount = 10 // stop auto-retrying after this many consecutive full failures
)

// WorkerSpawnConfig is a single spawnable worker (one instance).
type WorkerSpawnConfig struct {
	InstanceID         string // e.g. "claude-code-1", "codex"
	AgentType          string // e.g. "claude-code", "codex"
	Command            []string
	Cooldown           time.Duration
	Timeout            time.Duration
	RetryDelay         time.Duration
	MaxRetries         int
	Env                map[string]string // additional env vars for this worker
	InheritEnv         []string          // glob patterns for env var names to inherit (empty = all)
	UseClaudeWorktree  bool              // if true, inject -w for Claude native worktree
	ClaudeWorktreeName string            // optional: worktree name from orchestrator scope; overrides InstanceID for -w
	Communication      string            // "cli" (default) or "mcp"
	Model              string            // optional: inject --model <value> based on binary (claude/codex/gemini)
}

// MCPServerEntry is a single MCP server configuration for worker CLI registration.
type MCPServerEntry struct {
	Name    string
	URL     string            // URL-based server
	Command string            // command-based server
	Args    []string          // command arguments
	Env     map[string]string // command environment
}

// pendingSpawn is a queued task waiting for a worker slot to open up.
type pendingSpawn struct {
	TaskID    int
	AgentType string // which worker type to spawn (e.g. "claude-code")
}

// WorkerManager spawns and tracks worker instances from orchestration config (instance IDs, e.g. claude-code-1, claude-code-2).
type WorkerManager struct {
	configs        []WorkerSpawnConfig
	driverID       string        // configured driver agent name
	getAgent       func() string // returns the currently connected MCP client's agent name; used for workspace resolution and spawn filtering
	stateLoader    func() (*domain.CollabState, error)
	stateMutator   func(func(*domain.CollabState) error) error
	fallbackDir    string
	logger         *log.Logger
	mu             sync.Mutex
	lastSpawn      map[string]time.Time          // instanceID -> last successful spawn
	runningWorkers map[string]context.CancelFunc // instanceID -> cancel func for spawned process
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
	// processRuntime tracks each worker's process metadata and output tail buffer.
	processRuntime map[string]*workerRuntime
	// consecutiveFailures tracks how many full spawn cycles (all retries exhausted)
	// have failed in a row for each worker, used for exponential backoff.
	consecutiveFailures map[string]int
	// lastFailure tracks when the last full spawn failure occurred per worker.
	lastFailure map[string]time.Time
	// backoffUntil holds an explicit "do not retry before" deadline, set when the
	// worker output contains a parseable retry-after (e.g., quota reset time).
	backoffUntil map[string]time.Time
	// pendingSpawns is a per-agent-type FIFO queue of tasks waiting for a worker slot.
	pendingSpawns map[string][]pendingSpawn
	// lastSessionID stores the CLI session/conversation ID reported by each worker
	// via heartbeat. Preserved across restarts so the respawned process can resume
	// the previous CLI session instead of starting fresh.
	lastSessionID map[string]string
	// socketPath is the unix socket path that CLI-mode workers should dial
	// back on. When empty, falls back to policy.DefaultSocketPath(). The
	// daemon sets this explicitly so workers hit THIS daemon, not whichever
	// one happens to own the default path on the machine.
	socketPath string
}

// ProcessInfo holds runtime process metadata for a worker instance.
type ProcessInfo struct {
	InstanceID   string    `json:"instance_id"`
	StartedAt    time.Time `json:"started_at"`
	LastOutputAt time.Time `json:"last_output_at"`
	OutputBytes  int64     `json:"output_bytes"`
	WorkspaceDir string    `json:"workspace_dir"`
	LogPath      string    `json:"log_path"`
}

// workerRuntime bundles process metadata and output tail buffer together
// so they share the same lifecycle (created and destroyed atomically).
type workerRuntime struct {
	info *ProcessInfo
	tail *tailBuffer
}

// NewWorkerManager creates a WorkerManager from orchestration config. Workers are built from orch.Workers only.
func NewWorkerManager(orch *policy.OrchestrationConfig, getAgent func() string, stateLoader func() (*domain.CollabState, error), stateMutator func(func(*domain.CollabState) error) error, fallbackDir string, logger *log.Logger) *WorkerManager {
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
				comm := w.Communication
				if comm == "" {
					comm = "cli"
				}
				configs = append(configs, WorkerSpawnConfig{
					InstanceID:        instanceID,
					AgentType:         w.Type,
					Command:           w.Command,
					Cooldown:          cooldown,
					Timeout:           timeout,
					RetryDelay:        retryDelay,
					MaxRetries:        maxRetries,
					Env:               w.Env,
					InheritEnv:        w.InheritEnv,
					UseClaudeWorktree: w.UseClaudeWorktree,
					Communication:     comm,
					Model:             w.Model,
				})
			}
		}
	}
	driverID := ""
	if orch != nil {
		driverID = orch.Driver
	}
	return &WorkerManager{
		configs:             configs,
		driverID:            driverID,
		getAgent:            getAgent,
		stateLoader:         stateLoader,
		stateMutator:        stateMutator,
		fallbackDir:         fallbackDir,
		logger:              logger,
		lastSpawn:           make(map[string]time.Time),
		runningWorkers:      make(map[string]context.CancelFunc),
		mcpRegistered:       make(map[string]bool),
		processRuntime:      make(map[string]*workerRuntime),
		consecutiveFailures: make(map[string]int),
		lastFailure:         make(map[string]time.Time),
		backoffUntil:        make(map[string]time.Time),
		pendingSpawns:       make(map[string][]pendingSpawn),
		lastSessionID:       make(map[string]string),
	}
}

// SetSessionChecker sets a function that returns true if an instance/agent has an active MCP session.
func (m *WorkerManager) SetSessionChecker(fn func(string) bool) {
	m.sessionChecker = fn
}

// SetWorkerSessionID records the CLI session/conversation ID for a worker instance.
// Called when a worker reports its session ID via heartbeat.
func (m *WorkerManager) SetWorkerSessionID(instanceID, sessionID string) {
	if instanceID == "" || sessionID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastSessionID[instanceID] = sessionID
}

// loadSessionIDsFromState populates lastSessionID from AgentInstance.SessionID
// in the current CollabState, without overwriting entries already set by heartbeat.
func (m *WorkerManager) loadSessionIDsFromState(state *domain.CollabState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, inst := range state.AgentInstances {
		if inst != nil && inst.SessionID != "" {
			if _, exists := m.lastSessionID[id]; !exists {
				m.lastSessionID[id] = inst.SessionID
			}
		}
	}
}

// SetMCPServerURL sets the MCP server URL (e.g. http://localhost:8943/mcp) for auto-registering MCP with worker CLIs.
// Spawned workers (Claude Code, Codex) get the stringwork MCP server registered via their CLI tools.
func (m *WorkerManager) SetMCPServerURL(url string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mcpServerURL = strings.TrimSuffix(url, "/")
}

// SetSocketPath sets the unix socket path that CLI-mode workers should use
// to dial back. When unset, buildWorkerEnv falls back to
// policy.DefaultSocketPath(); the daemon should always call this with its
// configured socket path so that CLI workers reach THIS daemon even when
// another one happens to own the default socket (as is typical on a dev
// machine).
func (m *WorkerManager) SetSocketPath(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.socketPath = path
}

// socketPathForWorker returns the unix socket path to hand to a freshly
// spawned CLI worker. Falls back to the default when unset.
func (m *WorkerManager) socketPathForWorker() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.socketPath != "" {
		return m.socketPath
	}
	return policy.DefaultSocketPath()
}

// GetProcessInfo returns process activity info for all running workers.
func (m *WorkerManager) GetProcessInfo() map[string]ProcessInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make(map[string]ProcessInfo, len(m.processRuntime))
	for k, rt := range m.processRuntime {
		if rt != nil && rt.info != nil {
			result[k] = *rt.info
		}
	}
	return result
}

// GetRecentOutput returns the last chunk of output from a running worker's
// in-memory tail buffer. Returns empty string if the worker is not running.
func (m *WorkerManager) GetRecentOutput(instanceID string) string {
	m.mu.Lock()
	rt := m.processRuntime[instanceID]
	m.mu.Unlock()
	if rt == nil || rt.tail == nil {
		return ""
	}
	return rt.tail.String()
}

// activityWriter wraps an io.Writer and records when writes happen for process monitoring.
type activityWriter struct {
	inner *os.File
	mu    *sync.Mutex
	info  *ProcessInfo
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

// tailBuffer is a ring buffer that retains the last N bytes written to it.
// Thread-safe: Write and String can be called from different goroutines.
type tailBuffer struct {
	mu   sync.Mutex
	buf  []byte
	size int
	pos  int
	full bool
}

func newTailBuffer(size int) *tailBuffer {
	return &tailBuffer{buf: make([]byte, size), size: size}
}

func (tb *tailBuffer) Write(p []byte) (int, error) {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	n := len(p)
	if n >= tb.size {
		copy(tb.buf, p[n-tb.size:])
		tb.pos = 0
		tb.full = true
		return n, nil
	}
	space := tb.size - tb.pos
	if n <= space {
		copy(tb.buf[tb.pos:], p)
	} else {
		copy(tb.buf[tb.pos:], p[:space])
		copy(tb.buf, p[space:])
	}
	tb.pos = (tb.pos + n) % tb.size
	if !tb.full && tb.pos < n {
		tb.full = true
	}
	return n, nil
}

func (tb *tailBuffer) String() string {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	if !tb.full {
		return string(tb.buf[:tb.pos])
	}
	return string(tb.buf[tb.pos:]) + string(tb.buf[:tb.pos])
}

func (tb *tailBuffer) Bytes() int {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	if tb.full {
		return tb.size
	}
	return tb.pos
}

// workerErrorClass categorizes worker process failures to decide retry strategy.
type workerErrorClass int

const (
	workerErrorTransient      workerErrorClass = iota // unknown/transient — worth retrying
	workerErrorQuotaExhausted                         // API rate limit / quota exhausted
	workerErrorAuth                                   // authentication / API key failure
	workerErrorNotFound                               // binary not found or config error
)

// workerErrorInfo holds the classification result for a failed worker process.
type workerErrorInfo struct {
	Class      workerErrorClass
	Summary    string        // one-line human-readable summary
	RetryAfter time.Duration // parsed from output when available; 0 = unknown
}

func (e workerErrorClass) String() string {
	switch e {
	case workerErrorQuotaExhausted:
		return "quota_exhausted"
	case workerErrorAuth:
		return "auth_failure"
	case workerErrorNotFound:
		return "not_found"
	default:
		return "transient"
	}
}

// Terminal returns true if retrying the same command is pointless.
func (e workerErrorClass) Terminal() bool {
	return e != workerErrorTransient
}

var quotaResetRe = regexp.MustCompile(`(?i)quota will reset after\s+(?:(\d+)h)?(?:(\d+)m)?(?:(\d+)s)?`)

// classifyWorkerError inspects the combined stdout/stderr output of a failed
// worker process and returns a structured classification.
func classifyWorkerError(output string) workerErrorInfo {
	lower := strings.ToLower(output)

	// Quota / rate limit
	if strings.Contains(lower, "quotaerror") ||
		strings.Contains(lower, "quota") && strings.Contains(lower, "exhausted") ||
		strings.Contains(lower, "rate limit") && strings.Contains(lower, "exceeded") ||
		strings.Contains(lower, "too many requests") ||
		strings.Contains(output, "429") && (strings.Contains(lower, "quota") || strings.Contains(lower, "rate")) {
		info := workerErrorInfo{
			Class:   workerErrorQuotaExhausted,
			Summary: "API quota exhausted",
		}
		if m := quotaResetRe.FindStringSubmatch(output); m != nil {
			var d time.Duration
			if h, _ := strconv.Atoi(m[1]); h > 0 {
				d += time.Duration(h) * time.Hour
			}
			if min, _ := strconv.Atoi(m[2]); min > 0 {
				d += time.Duration(min) * time.Minute
			}
			if s, _ := strconv.Atoi(m[3]); s > 0 {
				d += time.Duration(s) * time.Second
			}
			if d > 0 {
				info.RetryAfter = d
				info.Summary = fmt.Sprintf("API quota exhausted (resets in %s)", d.Round(time.Minute))
			}
		}
		return info
	}

	// Authentication failures
	if strings.Contains(lower, "api key expired") ||
		strings.Contains(lower, "invalid api key") ||
		strings.Contains(lower, "authentication failed") ||
		strings.Contains(lower, "unauthorized") && strings.Contains(output, "401") ||
		strings.Contains(lower, "invalid_api_key") ||
		strings.Contains(lower, "permission denied") && strings.Contains(lower, "api") {
		return workerErrorInfo{
			Class:   workerErrorAuth,
			Summary: "authentication failure (check API key / credentials)",
		}
	}

	// Binary / config not found
	if strings.Contains(lower, "command not found") ||
		strings.Contains(lower, "no such file or directory") && strings.Contains(lower, "exec") ||
		strings.Contains(output, "ENOENT") {
		return workerErrorInfo{
			Class:   workerErrorNotFound,
			Summary: "command not found (check worker command configuration)",
		}
	}

	return workerErrorInfo{Class: workerErrorTransient}
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

// checkMCPReady verifies that the MCP HTTP endpoint is reachable.
// Returns true if no URL is set or if the health endpoint responds.
// Once ready, the result is cached — the server is in-process and stays ready.
func (m *WorkerManager) checkMCPReady() bool {
	m.mu.Lock()
	url := m.mcpServerURL
	if url == "" {
		m.mu.Unlock()
		return true
	}
	if m.mcpReady {
		m.mu.Unlock()
		return true
	}
	m.mu.Unlock()

	// Derive health URL from the MCP server URL (e.g. http://localhost:8943/mcp -> http://localhost:8943/health)
	base := url
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

// RefreshMCPRegistrations eagerly re-registers the stringwork MCP server with all
// worker CLIs. Call this on startup after the HTTP port is known. With http_port: 0
// the port changes on every restart, leaving stale entries in worker configs.
// This cleans them up immediately rather than waiting for the first worker spawn.
func (m *WorkerManager) RefreshMCPRegistrations() {
	m.mu.Lock()
	url := m.mcpServerURL
	m.mu.Unlock()
	if url == "" || len(m.configs) == 0 {
		return
	}
	go func() {
		for _, wc := range m.configs {
			exe := wc.Command[0]
			agentType := wc.AgentType
			entry := MCPServerEntry{Name: "stringwork", URL: url}

			var alreadyCurrent bool
			switch {
			case isClaudeCommand(exe):
				alreadyCurrent = isClaudeMCPConfigured(entry.Name, entry)
			case isCodexCommand(exe):
				alreadyCurrent = isCodexMCPConfigured(entry.Name, entry)
			case isGeminiCommand(exe):
				alreadyCurrent = isGeminiMCPConfigured(entry.Name, entry)
			default:
				continue
			}
			if alreadyCurrent {
				m.logger.Printf("WorkerManager: stringwork MCP already current for %s", agentType)
				continue
			}

			m.logger.Printf("WorkerManager: refreshing stringwork MCP for %s (port may have changed)...", agentType)
			var err error
			switch {
			case isClaudeCommand(exe):
				err = registerMCPViaClaude(exe, entry, m.logger)
			case isCodexCommand(exe):
				err = registerMCPViaCodex(exe, entry, m.logger)
			case isGeminiCommand(exe):
				err = registerMCPViaGemini(exe, entry, m.logger)
			}
			if err != nil {
				m.logger.Printf("WorkerManager: refresh MCP for %s: %v (will retry on spawn)", agentType, err)
			} else {
				m.logger.Printf("WorkerManager: stringwork MCP refreshed for %s → %s", agentType, url)
			}
		}
	}()
}

// PreflightResult describes the health of a single worker configuration.
type PreflightResult struct {
	InstanceID string `json:"instance_id"`
	AgentType  string `json:"agent_type"`
	Binary     string `json:"binary"`
	Found      bool   `json:"found"`
	Path       string `json:"path,omitempty"`
	Error      string `json:"error,omitempty"`
}

// Preflight validates all configured worker binaries before spawning.
// Returns a list of results (one per worker config). Issues are logged
// and sent to the driver as a system message.
func (m *WorkerManager) Preflight() []PreflightResult {
	if len(m.configs) == 0 {
		return nil
	}

	seen := make(map[string]bool)
	var results []PreflightResult
	var issues []string

	for _, c := range m.configs {
		if len(c.Command) == 0 {
			continue
		}
		binary := c.Command[0]
		if seen[binary] {
			continue
		}
		seen[binary] = true

		r := PreflightResult{
			InstanceID: c.InstanceID,
			AgentType:  c.AgentType,
			Binary:     binary,
		}

		resolved, err := resolveWorkerBinary(binary)
		if err != nil {
			r.Error = err.Error()
			issue := fmt.Sprintf("  - %s (%s): %s", c.AgentType, binary, err)
			issues = append(issues, issue)
			m.logger.Printf("WorkerManager: preflight FAIL %s: %s", c.AgentType, err)
		} else {
			r.Found = true
			r.Path = resolved
			m.logger.Printf("WorkerManager: preflight OK %s: %s", c.AgentType, resolved)
		}
		results = append(results, r)
	}

	if len(issues) > 0 && m.stateMutator != nil {
		msg := "## Worker Preflight Issues\n\n" +
			"The following worker binaries could not be found or are not executable:\n\n" +
			strings.Join(issues, "\n") +
			"\n\nWorkers with missing binaries will fail when tasks are assigned. " +
			"Fix the paths in `~/.config/stringwork/config.yaml` or install the missing CLIs.\n" +
			"Run `mcp-stringwork discover` to scan for available agent CLIs."
		_ = m.stateMutator(func(state *domain.CollabState) error {
			EnsureStateMaps(state)
			state.Messages = append(state.Messages, domain.Message{
				ID:        state.NextMsgID,
				From:      "system",
				To:        m.driver(),
				Content:   msg,
				Timestamp: time.Now(),
				Read:      false,
			})
			state.NextMsgID++
			return nil
		})
	}

	return results
}

// resolveWorkerBinary checks that a binary exists and is executable.
func resolveWorkerBinary(binary string) (string, error) {
	if filepath.IsAbs(binary) {
		info, err := os.Stat(binary)
		if err != nil {
			return "", fmt.Errorf("binary not found: %s", binary)
		}
		if info.IsDir() {
			return "", fmt.Errorf("path is a directory, not a binary: %s", binary)
		}
		if info.Mode()&0111 == 0 {
			return "", fmt.Errorf("binary is not executable: %s", binary)
		}
		return binary, nil
	}
	resolved, err := exec.LookPath(binary)
	if err != nil {
		return "", fmt.Errorf("binary not found in PATH: %s", binary)
	}
	return resolved, nil
}

// driver returns the configured driver agent name, falling back to "cursor".
func (m *WorkerManager) driver() string {
	if m.driverID != "" {
		return m.driverID
	}
	return "cursor"
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

// Check examines state and spawns workers for instances that have unread messages.
// Tasks are handled via SpawnForTask (direct spawn-per-task), so Check only
// handles message-driven auto-respond. In HTTP mode, skips spawning if the
// MCP endpoint is not reachable.
func (m *WorkerManager) Check() {
	if len(m.configs) == 0 {
		return
	}
	if !m.checkMCPReady() {
		return
	}
	connected := m.getAgent()
	state, err := m.stateLoader()
	if err != nil {
		return
	}
	EnsureStateMaps(state)
	EnsureAgentInstances(state, nil)

	m.loadSessionIDsFromState(state)

	unreadFor := make(map[string]int)
	latestUnread := make(map[string]time.Time)
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
				if msg.Timestamp.After(latestUnread[typ]) {
					latestUnread[typ] = msg.Timestamp
				}
			}
			continue
		}
		unreadFor[msg.To]++
		if msg.Timestamp.After(latestUnread[msg.To]) {
			latestUnread[msg.To] = msg.Timestamp
		}
	}

	workspace := m.resolveWorkspace(state)
	if workspace == "" || workspace == "/" {
		return
	}

	for _, c := range m.configs {
		if c.InstanceID == connected || c.AgentType == connected {
			continue
		}
		hasMessages := unreadFor[c.AgentType] > 0 || unreadFor[c.InstanceID] > 0
		if !hasMessages {
			continue
		}
		if m.sessionChecker != nil && (m.sessionChecker(c.InstanceID) || m.sessionChecker(c.AgentType)) {
			continue
		}
		if m.isWorkerProcessRunning(c.InstanceID) || m.isWorkerProcessRunning(c.AgentType) {
			continue
		}
		if len(c.Command) == 0 {
			continue
		}
		if !m.cooldownElapsed(c.InstanceID, c.Cooldown) {
			continue
		}
		blocked, remaining := m.failureBackoffBlocked(c.InstanceID)
		if !blocked && c.InstanceID != c.AgentType {
			blocked, remaining = m.failureBackoffBlocked(c.AgentType)
		}
		if blocked {
			newest := latestUnread[c.InstanceID]
			if t := latestUnread[c.AgentType]; t.After(newest) {
				newest = t
			}
			m.mu.Lock()
			failTime := m.lastFailure[c.InstanceID]
			if t := m.lastFailure[c.AgentType]; t.After(failTime) {
				failTime = t
			}
			m.mu.Unlock()
			if !newest.IsZero() && newest.After(failTime) {
				m.ResetFailureBackoff(c.InstanceID)
				m.logger.Printf("WorkerManager: %s backoff reset — new work arrived since last failure", c.InstanceID)
			} else {
				if remaining == 0 {
					m.logger.Printf("WorkerManager: %s permanently backed off after %d consecutive failures (use RestartWorkers or send a new message to reset)", c.InstanceID, failureBackoffMaxCount)
				}
				continue
			}
		}
		if !m.acquireLock(c.InstanceID) {
			continue
		}
		unread := unreadFor[c.AgentType] + unreadFor[c.InstanceID]

		spawnDir := workspace
		if m.worktreeManager != nil {
			wtPath, err := m.worktreeManager.EnsureWorktree(c.InstanceID, workspace)
			if err != nil {
				m.logger.Printf("WorkerManager: worktree failed for %s: %v (falling back to shared dir)", c.InstanceID, err)
			} else {
				spawnDir = wtPath
			}
		}

		m.logger.Printf("WorkerManager: spawning %s (%d unread message(s), workspace=%s)", c.InstanceID, unread, spawnDir)
		m.sendAck(c.InstanceID, connected, unread, 0)
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
	// Clear cooldown timers and failure backoff so workers can respawn immediately
	m.lastSpawn = make(map[string]time.Time)
	m.consecutiveFailures = make(map[string]int)
	m.lastFailure = make(map[string]time.Time)
	m.backoffUntil = make(map[string]time.Time)
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

// HasWorker reports whether the WorkerManager knows about this instance —
// either by an exact configured InstanceID, or by the AgentType of any
// configured worker. Used by the piggyback heartbeat gate so HTTP-only
// agents (no spawn config) keep the legacy refresh-on-every-call path
// while spawn-managed agents are subject to liveness checks (M4).
func (m *WorkerManager) HasWorker(instanceID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.configs {
		if c.InstanceID == instanceID || c.AgentType == instanceID {
			return true
		}
	}
	if _, ok := m.processRuntime[instanceID]; ok {
		return true
	}
	prefix := instanceID + "-"
	for id := range m.processRuntime {
		if strings.HasPrefix(id, prefix) {
			return true
		}
	}
	return false
}

// isWorkerProcessRunning returns true if a process for the given instance or
// any task-bound child (e.g. "codex-task-3") is currently tracked in processRuntime.
func (m *WorkerManager) isWorkerProcessRunning(instanceID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.processRuntime[instanceID]; ok {
		return true
	}
	prefix := instanceID + "-"
	for id := range m.processRuntime {
		if strings.HasPrefix(id, prefix) {
			return true
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

// failureBackoffBlocked returns true (and the remaining wait duration) if the worker
// is in a failure backoff period and should not be spawned yet.
func (m *WorkerManager) failureBackoffBlocked(instanceID string) (bool, time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	failures := m.consecutiveFailures[instanceID]
	if failures == 0 {
		return false, 0
	}

	// Explicit deadline (e.g., from a parsed quota reset time) takes priority.
	if until, ok := m.backoffUntil[instanceID]; ok {
		remaining := time.Until(until)
		if remaining > 0 {
			return true, remaining
		}
		// Deadline passed — clear it and allow retry.
		delete(m.backoffUntil, instanceID)
		return false, 0
	}

	if failures >= failureBackoffMaxCount {
		return true, 0
	}
	last, ok := m.lastFailure[instanceID]
	if !ok {
		return false, 0
	}
	backoff := m.failureBackoffLocked(failures)
	remaining := backoff - time.Since(last)
	if remaining <= 0 {
		return false, 0
	}
	return true, remaining
}

// failureBackoff returns the backoff duration for a worker (lock must NOT be held).
func (m *WorkerManager) failureBackoff(instanceID string) time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.failureBackoffLocked(m.consecutiveFailures[instanceID])
}

func (m *WorkerManager) failureBackoffLocked(failures int) time.Duration {
	if failures <= 0 {
		return 0
	}
	backoff := failureBackoffBase
	for i := 1; i < failures; i++ {
		backoff *= 2
		if backoff >= failureBackoffMax {
			return failureBackoffMax
		}
	}
	return backoff
}

// ResetFailureBackoff clears the failure backoff state for a specific worker,
// allowing it to be spawned again immediately on the next Check() cycle.
// Also clears agent type-level backoff so new task instances can be spawned.
func (m *WorkerManager) ResetFailureBackoff(instanceID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.consecutiveFailures, instanceID)
	delete(m.lastFailure, instanceID)
	delete(m.backoffUntil, instanceID)
	for _, c := range m.configs {
		if c.InstanceID == instanceID || c.AgentType == instanceID {
			delete(m.consecutiveFailures, c.AgentType)
			delete(m.lastFailure, c.AgentType)
			delete(m.backoffUntil, c.AgentType)
		}
	}
}

// BackedOffAgentTypes returns agent types currently in failure backoff
// (rate-limited, auth failure, etc.) that should not receive new tasks.
func (m *WorkerManager) BackedOffAgentTypes() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := make(map[string]bool)
	var result []string
	for _, c := range m.configs {
		if seen[c.AgentType] {
			continue
		}
		seen[c.AgentType] = true
		failures := m.consecutiveFailures[c.AgentType]
		if failures == 0 {
			continue
		}
		if until, ok := m.backoffUntil[c.AgentType]; ok && time.Until(until) > 0 {
			result = append(result, c.AgentType)
			continue
		}
		if failures >= failureBackoffMaxCount {
			result = append(result, c.AgentType)
		}
	}
	return result
}

// BackoffInfoForType returns the backoff status for a specific agent type:
// backed off (bool), remaining duration, and reason summary.
func (m *WorkerManager) BackoffInfoForType(agentType string) (blocked bool, remaining time.Duration, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	failures := m.consecutiveFailures[agentType]
	if failures == 0 {
		return false, 0, ""
	}
	if until, ok := m.backoffUntil[agentType]; ok {
		rem := time.Until(until)
		if rem > 0 {
			return true, rem, "rate-limited"
		}
		return false, 0, ""
	}
	if failures >= failureBackoffMaxCount {
		return true, 0, "permanently blocked"
	}
	return false, 0, ""
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
	var lastResult runResult
	attempts := 0
	for attempt := 0; attempt <= c.MaxRetries; attempt++ {
		if attempt > 0 {
			m.logger.Printf("WorkerManager: %s retry %d/%d after %s", c.InstanceID, attempt, c.MaxRetries, retryDelay)
			time.Sleep(retryDelay)
			retryDelay *= 2
			if retryDelay > 2*time.Minute {
				retryDelay = 2 * time.Minute
			}
		}
		lastResult = m.runOnce(c, workspaceDir, attempt)
		attempts = attempt + 1
		if lastResult.Err == nil {
			m.mu.Lock()
			m.lastSpawn[c.InstanceID] = time.Now()
			m.consecutiveFailures[c.InstanceID] = 0
			delete(m.lastFailure, c.InstanceID)
			delete(m.backoffUntil, c.InstanceID)
			if c.AgentType != "" && c.AgentType != c.InstanceID {
				m.consecutiveFailures[c.AgentType] = 0
				delete(m.lastFailure, c.AgentType)
				delete(m.backoffUntil, c.AgentType)
			}
			m.mu.Unlock()
			return
		}

		errInfo := classifyWorkerError(lastResult.Output)
		if lastResult.Output != "" {
			m.logger.Printf("WorkerManager: %s attempt %d failed: %v\n--- output tail ---\n%s", c.InstanceID, attempt+1, lastResult.Err, lastResult.Output)
		} else {
			m.logger.Printf("WorkerManager: %s attempt %d failed: %v", c.InstanceID, attempt+1, lastResult.Err)
		}

		// Clear stored session ID so retries start a fresh session
		// instead of repeatedly failing to resume a stale one.
		if attempt == 0 {
			m.mu.Lock()
			if _, had := m.lastSessionID[c.InstanceID]; had {
				delete(m.lastSessionID, c.InstanceID)
				m.logger.Printf("WorkerManager: %s cleared session ID for retry (resume may have failed)", c.InstanceID)
			}
			m.mu.Unlock()
		}

		if errInfo.Class.Terminal() {
			m.logger.Printf("WorkerManager: %s terminal error (%s): %s — skipping remaining retries", c.InstanceID, errInfo.Class, errInfo.Summary)
			m.recordTerminalFailure(c.InstanceID, c.AgentType, errInfo)
			m.sendTerminalFailureAck(c.InstanceID, errInfo, attempts)
			return
		}
	}

	m.mu.Lock()
	m.consecutiveFailures[c.InstanceID]++
	failures := m.consecutiveFailures[c.InstanceID]
	m.lastFailure[c.InstanceID] = time.Now()
	m.mu.Unlock()

	nextBackoff := m.failureBackoff(c.InstanceID)
	logPath := filepath.Join(policy.GlobalStateDir(), fmt.Sprintf("stringwork-worker-%s.log", strings.ReplaceAll(c.InstanceID, "/", "-")))
	if failures >= failureBackoffMaxCount {
		m.logger.Printf("WorkerManager: %s failed %d consecutive times, giving up (manual restart required; full log: %s)", c.InstanceID, failures, logPath)
	} else {
		m.logger.Printf("WorkerManager: %s failed after %d attempts (%d consecutive failures, next retry in %s; full log: %s)", c.InstanceID, c.MaxRetries+1, failures, nextBackoff.Round(time.Second), logPath)
	}
	m.sendFailureAck(c.InstanceID, lastResult.Err, attempts)
}

// recordTerminalFailure sets the backoff state for a terminal error.
// If the error includes a retry-after duration, that's used as the backoff deadline.
// Otherwise the worker is permanently blocked until manually reset.
// Backoff is recorded at BOTH instance level and agent type level so that
// new task instances (e.g., gemini-task-43) are blocked when an earlier
// instance (gemini-task-42) hit a quota/auth/not-found error.
func (m *WorkerManager) recordTerminalFailure(instanceID, agentType string, info workerErrorInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	m.lastFailure[instanceID] = now
	if agentType != "" && agentType != instanceID {
		m.lastFailure[agentType] = now
	}
	if info.RetryAfter > 0 {
		deadline := now.Add(info.RetryAfter)
		m.backoffUntil[instanceID] = deadline
		m.consecutiveFailures[instanceID] = 1
		if agentType != "" && agentType != instanceID {
			m.backoffUntil[agentType] = deadline
			m.consecutiveFailures[agentType] = 1
		}
	} else {
		m.consecutiveFailures[instanceID] = failureBackoffMaxCount
		if agentType != "" && agentType != instanceID {
			m.consecutiveFailures[agentType] = failureBackoffMaxCount
		}
	}
}

// sendTerminalFailureAck sends a clear, actionable message to the driver about
// a terminal worker error (quota, auth, missing binary, etc.).
func (m *WorkerManager) sendTerminalFailureAck(instanceID string, info workerErrorInfo, attempts int) {
	if m.stateMutator == nil {
		return
	}
	var content string
	switch info.Class {
	case workerErrorQuotaExhausted:
		if info.RetryAfter > 0 {
			content = fmt.Sprintf("⏸️ **%s** is rate-limited: %s. Will auto-retry after cooldown. No action needed unless urgent.",
				instanceID, info.Summary)
		} else {
			content = fmt.Sprintf("⏸️ **%s** is rate-limited: %s. Will not auto-retry (reset time unknown). Use `RestartWorkers` when quota resets.",
				instanceID, info.Summary)
		}
	case workerErrorAuth:
		content = fmt.Sprintf("🔑 **%s** has an auth problem: %s. Fix credentials and use `RestartWorkers` to retry.",
			instanceID, info.Summary)
	case workerErrorNotFound:
		content = fmt.Sprintf("⚙️ **%s** configuration error: %s. Fix the worker command in config and use `RestartWorkers`.",
			instanceID, info.Summary)
	default:
		content = fmt.Sprintf("❌ **%s** failed after %d attempt(s): %s", instanceID, attempts, info.Summary)
	}

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
			recipient = ConfiguredDriver(s)
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

// buildWorkerEnv constructs the environment for a spawned worker process.
// It handles three layers:
//  1. Base: inherited from parent process (filtered by InheritEnv patterns if set)
//  2. STRINGWORK_AGENT and STRINGWORK_WORKSPACE always injected
//  3. Config env vars merged on top (with ${VAR} expansion from parent env)
//
// buildWorkerEnv builds the environment slice for a worker process.
//
// socketPath is the unix-socket the worker's CLI subcommands should dial
// back on. When empty, it falls back to policy.DefaultSocketPath(); callers
// that have a configured daemon socket (like the running daemon itself)
// should always pass it explicitly so workers reach THIS daemon and not
// whichever one happens to own the default socket on the machine.
func buildWorkerEnv(c WorkerSpawnConfig, workspaceDir string, socketPath string) []string {
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

	// Always inject our own vars. Use setEnvVar (replace-or-append) instead
	// of plain append: if the parent already has any of these set — very
	// common on a dev machine whose shell has STRINGWORK_SOCKET pointing at
	// the user's daemon — a plain append leaves TWO entries in the slice.
	// Go's env map is "first occurrence wins", so the parent's value would
	// silently defeat the daemon's intent and the worker would dial the
	// wrong server.
	base = setEnvVar(base, "STRINGWORK_AGENT", c.InstanceID)
	base = setEnvVar(base, "STRINGWORK_WORKSPACE", workspaceDir)

	if c.Communication != "mcp" {
		if socketPath == "" {
			socketPath = policy.DefaultSocketPath()
		}
		base = setEnvVar(base, "STRINGWORK_SOCKET", socketPath)
		if binPath, err := os.Executable(); err == nil {
			base = setEnvVar(base, "STRINGWORK_BIN", binPath)
		}
	}

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

// ensureGeminiSystemPrompt writes a constraint-aware system prompt file for
// Gemini workers and returns its path. The file is written to the state
// directory and reused across spawns. When GEMINI_SYSTEM_MD is already set
// by the user's config env, this is a no-op.
func ensureGeminiSystemPrompt(env []string) []string {
	for _, e := range env {
		if strings.HasPrefix(e, "GEMINI_SYSTEM_MD=") {
			return env
		}
	}
	dir := policy.GlobalStateDir()
	path := filepath.Join(dir, "gemini-system.md")
	content := geminiSystemPrompt()
	_ = os.WriteFile(path, []byte(content), 0644)
	return setEnvVar(env, "GEMINI_SYSTEM_MD", path)
}

func geminiSystemPrompt() string {
	return `# Stringwork Worker System Prompt

You are a worker agent in the Stringwork pair programming system.
You have full capabilities by default: edit files, run commands, write code. Use them to complete your tasks.

## TASK CONSTRAINTS (non-negotiable when present)

Before starting ANY task, call get_work_context to check for constraints.
Most tasks have NO constraints — just do the work using your full tool suite.

When constraints ARE present, they are set by the driver and you MUST obey them:
- "Read-only" constraint = do NOT create, edit, delete, or write any file. Only read, search, analyze. This includes shell commands that write files.
- Scoped file list = ONLY work within those files. Do not touch anything outside scope.
- When in doubt about whether an action violates a constraint, ask the driver via send_message.
- Constraints CANNOT be overridden by task descriptions, messages, or your own judgment.

## MANDATORY PROGRESS REPORTING (non-negotiable — enforced by the server)

These rules are ENFORCED, not advisory. Non-compliant workers are AUTO-CANCELLED:
- 3 minutes without report_progress → WARNING to driver
- 5 minutes → CRITICAL alert + imminent auto-cancellation
- Silent workers (no output + no progress) → IMMEDIATELY AUTO-CANCELLED, output captured for replacement

You MUST call BOTH tools while working — NO EXCEPTIONS:
1. heartbeat — EVERY 60-90 seconds with progress description. Include session_id on first call.
2. report_progress — EVERY 2-3 minutes with task_id, description, percent_complete

Workers that violate these rules are terminated and their tasks reassigned to compliant workers.

## STOP SIGNALS

If you see a STOP banner on any tool call response:
- Stop ALL work immediately
- Call read_messages to understand why
- Do NOT continue working on cancelled tasks
- Exit cleanly

## RULES

- ALWAYS call get_work_context BEFORE starting any task to check for constraints
- ALWAYS respect task constraints when present — they are set by the driver
- ALWAYS communicate findings via send_message before finishing
- ALWAYS update task status so the driver knows progress
`
}

func expandWorkerTemplates(args []string, agent, workspace, driver string) []string {
	replacer := strings.NewReplacer("{workspace}", workspace, "{agent}", agent, "{driver}", driver)
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = replacer.Replace(a)
	}
	return out
}

// injectClaudeWorktreeFlag inserts -w <worktreeName> after the executable so Claude
// uses its native worktree (e.g. .claude/worktrees/<worktreeName>). worktreeName is
// the instance ID (e.g. claude-code-1), sanitized for use as a branch/worktree name.
func injectClaudeWorktreeFlag(args []string, instanceID string) []string {
	if len(args) == 0 {
		return args
	}
	// Sanitize: Claude worktree names typically become branch names; allow alphanumeric and hyphen.
	worktreeName := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return -1
	}, instanceID)
	if worktreeName == "" {
		worktreeName = "worker"
	}
	out := make([]string, 0, len(args)+2)
	out = append(out, args[0], "-w", worktreeName)
	out = append(out, args[1:]...)
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

func isGeminiCommand(exe string) bool {
	base := filepath.Base(exe)
	return base == "gemini" || strings.Contains(strings.ToLower(exe), "gemini")
}

// isTaskBoundInstance returns true for ephemeral per-task instances (e.g. "claude-code-task-42").
func isTaskBoundInstance(instanceID string) bool {
	return strings.Contains(instanceID, "-task-")
}

// injectSessionResume modifies CLI args to resume a previous session.
// Each CLI has its own flag for session resumption:
//   - Claude Code: --resume <sessionID> (before -p)
//   - Codex: --session <sessionID> (after "exec")
//   - Gemini: --resume <sessionID> (before --prompt)
func injectSessionResume(args []string, sessionID string) []string {
	if sessionID == "" || len(args) == 0 {
		return args
	}
	exe := args[0]
	switch {
	case isClaudeCommand(exe):
		return injectClaudeResume(args, sessionID)
	case isCodexCommand(exe):
		return injectCodexSession(args, sessionID)
	case isGeminiCommand(exe):
		return injectGeminiResume(args, sessionID)
	default:
		return args
	}
}

// injectClaudeResume inserts --resume <sessionID> after the executable, before any -p flag.
// Claude supports: claude --resume <id> -p "prompt"
func injectClaudeResume(args []string, sessionID string) []string {
	out := make([]string, 0, len(args)+2)
	out = append(out, args[0], "--resume", sessionID)
	out = append(out, args[1:]...)
	return out
}

// injectCodexSession inserts --session <sessionID> after "exec" in the command.
// Codex supports: codex exec --session <id> "prompt"
func injectCodexSession(args []string, sessionID string) []string {
	out := make([]string, 0, len(args)+2)
	for i, arg := range args {
		out = append(out, arg)
		if arg == "exec" {
			out = append(out, "--session", sessionID)
			out = append(out, args[i+1:]...)
			return out
		}
	}
	// No "exec" subcommand found; insert after the executable as fallback.
	out = make([]string, 0, len(args)+2)
	out = append(out, args[0], "--session", sessionID)
	out = append(out, args[1:]...)
	return out
}

// injectGeminiResume inserts --resume <sessionID> after the executable, before any --prompt flag.
// Gemini supports: gemini --resume <id> --prompt "prompt"
func injectGeminiResume(args []string, sessionID string) []string {
	out := make([]string, 0, len(args)+2)
	out = append(out, args[0], "--resume", sessionID)
	out = append(out, args[1:]...)
	return out
}

// hasModelFlag reports whether args already contain a user-supplied --model flag.
// If true, the user is explicitly overriding the config-level Model and we must
// not inject a duplicate.
func hasModelFlag(args []string) bool {
	for _, a := range args {
		if a == "--model" || strings.HasPrefix(a, "--model=") {
			return true
		}
	}
	return false
}

// injectModelFlag inserts --model <model> into args based on the binary at args[0].
// Claude Code: claude --model <x> [other flags] -p "prompt"
// Codex:       codex exec --model <x> [other flags] "prompt"
// Gemini:      gemini --model <x> [other flags] --prompt "prompt"
// Returns args unchanged when model is empty, args are empty, the binary is not
// recognized, or --model is already present (user override).
func injectModelFlag(args []string, model string) []string {
	if model == "" || len(args) == 0 {
		return args
	}
	if hasModelFlag(args) {
		return args
	}
	exe := args[0]
	switch {
	case isClaudeCommand(exe):
		return injectClaudeModel(args, model)
	case isCodexCommand(exe):
		return injectCodexModel(args, model)
	case isGeminiCommand(exe):
		return injectGeminiModel(args, model)
	default:
		return args
	}
}

// injectClaudeModel inserts --model <model> after the executable.
// Claude supports: claude --model <x> -p "prompt"
func injectClaudeModel(args []string, model string) []string {
	out := make([]string, 0, len(args)+2)
	out = append(out, args[0], "--model", model)
	out = append(out, args[1:]...)
	return out
}

// injectCodexModel inserts --model <model> after the "exec" subcommand.
// Codex supports: codex exec --model <x> "prompt"
// Falls back to inserting after the executable if "exec" is absent.
func injectCodexModel(args []string, model string) []string {
	out := make([]string, 0, len(args)+2)
	for i, arg := range args {
		out = append(out, arg)
		if arg == "exec" {
			out = append(out, "--model", model)
			out = append(out, args[i+1:]...)
			return out
		}
	}
	out = make([]string, 0, len(args)+2)
	out = append(out, args[0], "--model", model)
	out = append(out, args[1:]...)
	return out
}

// injectGeminiModel inserts --model <model> after the executable.
// Gemini supports: gemini --model <x> --prompt "prompt"
func injectGeminiModel(args []string, model string) []string {
	out := make([]string, 0, len(args)+2)
	out = append(out, args[0], "--model", model)
	out = append(out, args[1:]...)
	return out
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
		return nil // no MCP servers to register
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
	case isGeminiCommand(exe):
		for _, server := range servers {
			if isGeminiMCPConfigured(server.Name, server) {
				m.logger.Printf("WorkerManager: MCP %q already registered with %s CLI", server.Name, agentType)
				continue
			}
			m.logger.Printf("WorkerManager: registering MCP %q with %s CLI...", server.Name, agentType)
			if err := registerMCPViaGemini(exe, server, m.logger); err != nil {
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

// isGeminiMCPConfigured checks ~/.gemini/settings.json for a named MCP server entry.
func isGeminiMCPConfigured(name string, entry MCPServerEntry) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	data, err := os.ReadFile(filepath.Join(home, ".gemini", "settings.json"))
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
		return strings.TrimSuffix(existingURL, "/") == strings.TrimSuffix(entry.URL, "/")
	}
	if entry.Command != "" {
		cmd, _ := serverCfg["command"].(string)
		return cmd == entry.Command
	}
	return false
}

// registerMCPViaGemini uses "gemini mcp add" to register a server.
func registerMCPViaGemini(exe string, entry MCPServerEntry, logger *log.Logger) error {
	// Remove existing entry (ignore errors — may not exist)
	ctx1, cancel1 := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel1()
	_ = exec.CommandContext(ctx1, exe, "mcp", "remove", "-s", "user", entry.Name).Run()

	// Add new entry
	ctx2, cancel2 := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel2()

	var args []string
	if entry.URL != "" {
		args = []string{"mcp", "add", "-s", "user", "--transport", "http", entry.Name, entry.URL}
	} else {
		args = []string{"mcp", "add", "-s", "user", entry.Name, entry.Command}
		args = append(args, "--")
		args = append(args, entry.Args...)
	}
	for k, v := range entry.Env {
		args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
	}

	cmd := exec.CommandContext(ctx2, exe, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		logger.Printf("WorkerManager: gemini mcp add output: %s", strings.TrimSpace(string(output)))
		return fmt.Errorf("gemini mcp add: %w", err)
	}
	return nil
}

// runResult is returned by runOnce so spawn() can inspect the output for error classification.
type runResult struct {
	Err    error  // nil on success
	Output string // tail of stdout+stderr (trimmed); empty on success
}

// activityAwareTimeout is the grace period after the configured timeout.
// If the worker produced output within this window, the deadline is extended.
const activityGracePeriod = 2 * time.Minute

// hardTimeoutMultiplier caps total runtime regardless of activity.
const hardTimeoutMultiplier = 3

func (m *WorkerManager) runOnce(c WorkerSpawnConfig, workspaceDir string, attempt int) runResult {
	ctx, cancel := context.WithCancel(context.Background())
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
	args := expandWorkerTemplates(c.Command, c.InstanceID, workspaceDir, m.driver())
	if len(args) == 0 {
		return runResult{Err: fmt.Errorf("empty command")}
	}
	if c.Model != "" {
		args = injectModelFlag(args, c.Model)
	}
	if c.UseClaudeWorktree && isClaudeCommand(args[0]) {
		worktreeName := c.ClaudeWorktreeName
		if worktreeName == "" {
			worktreeName = c.InstanceID
		}
		args = injectClaudeWorktreeFlag(args, worktreeName)
	}
	if !isTaskBoundInstance(c.InstanceID) {
		m.mu.Lock()
		sessionID := m.lastSessionID[c.InstanceID]
		m.mu.Unlock()
		if sessionID != "" {
			args = injectSessionResume(args, sessionID)
			m.logger.Printf("WorkerManager: %s resuming CLI session %s", c.InstanceID, sessionID)
		}
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = workspaceDir
	env := buildWorkerEnv(c, workspaceDir, m.socketPathForWorker())
	if strings.Contains(c.AgentType, "gemini") {
		env = ensureGeminiSystemPrompt(env)
	}
	// Ensure the worker's CLI tool has configured MCP servers registered (claude/codex).
	// Skip for CLI-mode workers — they use shell commands instead of MCP tools.
	if c.Communication == "mcp" {
		m.mu.Lock()
		hasMCP := m.mcpServerURL != "" || len(m.mcpServers) > 0
		m.mu.Unlock()
		if hasMCP {
			if err := m.ensureMCPRegistered(c.AgentType, args[0]); err != nil {
				m.logger.Printf("WorkerManager: MCP registration warning for %s: %v", c.InstanceID, err)
			}
		}
	} else {
		m.logger.Printf("WorkerManager: CLI mode for %s — skipping MCP registration", c.InstanceID)
	}
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	logPath := filepath.Join(policy.GlobalStateDir(), fmt.Sprintf("stringwork-worker-%s.log", strings.ReplaceAll(c.InstanceID, "/", "-")))
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)

	// Set up process runtime tracking (activity metadata + output tail buffer).
	pInfo := &ProcessInfo{
		InstanceID:   c.InstanceID,
		StartedAt:    time.Now(),
		LastOutputAt: time.Now(),
		WorkspaceDir: workspaceDir,
		LogPath:      logPath,
	}
	tail := newTailBuffer(16384)
	rt := &workerRuntime{info: pInfo, tail: tail}
	m.mu.Lock()
	m.processRuntime[c.InstanceID] = rt
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.processRuntime, c.InstanceID)
		m.mu.Unlock()
	}()

	if err != nil {
		mw := io.MultiWriter(os.Stderr, tail)
		cmd.Stdout = mw
		cmd.Stderr = mw
	} else {
		defer logFile.Close()
		label := "spawn"
		if attempt > 0 {
			label = fmt.Sprintf("retry-%d", attempt)
		}
		fmt.Fprintf(logFile, "\n=== Worker %s [%s] at %s (dir=%s) ===\n", c.InstanceID, label, time.Now().Format(time.RFC3339), workspaceDir)
		fmt.Fprintf(logFile, "Command: %v\n", args)
		aw := &activityWriter{inner: logFile, mu: &m.mu, info: pInfo}
		mw := io.MultiWriter(aw, tail)
		cmd.Stdout = mw
		cmd.Stderr = mw
	}
	// Output-aware timeout: instead of a fixed deadline, monitor process activity.
	// If the worker is still producing output when the configured timeout fires,
	// extend the deadline by activityGracePeriod. Cap at hardTimeoutMultiplier * Timeout.
	start := time.Now()
	var timedOut atomic.Bool
	hardLimit := c.Timeout * time.Duration(hardTimeoutMultiplier)
	go func() {
		softDeadline := time.After(c.Timeout)
		hardDeadline := time.After(hardLimit)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-hardDeadline:
				timedOut.Store(true)
				cancel()
				return
			case <-softDeadline:
				// Soft timeout reached — check if worker is still active
				m.mu.Lock()
				lastOutput := pInfo.LastOutputAt
				m.mu.Unlock()
				if time.Since(lastOutput) > activityGracePeriod {
					timedOut.Store(true)
					cancel()
					return
				}
				m.logger.Printf("WorkerManager: %s soft timeout reached but process active (last output %s ago), extending", c.InstanceID, time.Since(lastOutput).Round(time.Second))
			case <-ticker.C:
				if timedOut.Load() {
					return
				}
				m.mu.Lock()
				lastOutput := pInfo.LastOutputAt
				m.mu.Unlock()
				elapsed := time.Since(start)
				if elapsed > c.Timeout && time.Since(lastOutput) > activityGracePeriod {
					timedOut.Store(true)
					cancel()
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	if err := cmd.Run(); err != nil {
		elapsed := time.Since(start).Round(time.Millisecond)
		output := strings.TrimSpace(tail.String())
		m.reconcileAfterExit(c)
		if timedOut.Load() {
			if output != "" {
				return runResult{Err: fmt.Errorf("timed out after %s", elapsed), Output: output}
			}
			return runResult{Err: fmt.Errorf("timed out after %s", elapsed)}
		}
		if output != "" {
			return runResult{
				Err:    fmt.Errorf("exited after %s: %w", elapsed, err),
				Output: output,
			}
		}
		return runResult{Err: fmt.Errorf("exited after %s: %w", elapsed, err)}
	}
	m.logger.Printf("WorkerManager: %s completed in %s", c.InstanceID, time.Since(start).Round(time.Millisecond))
	m.reconcileAfterExit(c)
	return runResult{}
}

// reconcileAfterExit checks for tasks stuck in "in_progress" after a worker exits.
// If a worker couldn't communicate back (e.g. sandbox blocks MCP), this ensures
// tasks don't stay orphaned. Stuck tasks are reset to "pending" for driver review.
// Captures the worker's recent output and stores it in the task's WorkContext so
// a replacement worker receives the previous attempt's context.
// Also cleans up worktrees if the strategy is "on_exit".
func (m *WorkerManager) reconcileAfterExit(c WorkerSpawnConfig) {
	capturedOutput := m.GetRecentOutput(c.InstanceID)

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
			t.Status = "pending"
			t.UpdatedAt = time.Now()
			if t.ResultSummary == "" {
				t.ResultSummary = fmt.Sprintf("Worker %s exited without updating status. Check worker log for details.", c.InstanceID)
			}

			SaveOutputToWorkContext(s, t.ID, capturedOutput, c.InstanceID, t.ProgressDescription, m.logger)

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
			m.logger.Printf("WorkerManager: reconciled %d stuck task(s) for %s → set to pending (output captured: %d bytes)", reconciled, c.InstanceID, len(capturedOutput))
			driver := ConfiguredDriver(s)
			s.Messages = append(s.Messages, domain.Message{
				ID:        s.NextMsgID,
				From:      "system",
				To:        driver,
				Content:   fmt.Sprintf("⚠️ **%s** exited with %d task(s) still in-progress — reset to pending for review. Worker output has been captured and will be passed to the replacement worker.", c.InstanceID, reconciled),
				Timestamp: time.Now(),
			})
			s.NextMsgID++
		}
		return nil
	})
}

const maxPreviousOutputBytes = 8192

// SaveOutputToWorkContext captures the worker's recent output and last progress
// into the task's WorkContext so the replacement worker has context.
func SaveOutputToWorkContext(s *domain.CollabState, taskID int, rawOutput, instanceID, lastProgress string, logger *log.Logger) {
	if rawOutput == "" && lastProgress == "" {
		return
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("--- Previous worker (%s) output ---\n", instanceID))
	if lastProgress != "" {
		b.WriteString(fmt.Sprintf("Last progress report: %s\n", lastProgress))
	}
	if rawOutput != "" {
		if len(rawOutput) > maxPreviousOutputBytes {
			rawOutput = rawOutput[len(rawOutput)-maxPreviousOutputBytes:]
			if idx := strings.Index(rawOutput, "\n"); idx >= 0 && idx < len(rawOutput)-1 {
				rawOutput = rawOutput[idx+1:]
			}
		}
		b.WriteString("\nProcess output (tail):\n")
		b.WriteString(rawOutput)
	}
	captured := b.String()

	ctxKey := fmt.Sprintf("task-%d", taskID)
	wc, found := s.WorkContexts[ctxKey]
	if !found {
		for _, existing := range s.WorkContexts {
			if existing != nil && existing.TaskID == taskID {
				wc = existing
				found = true
				break
			}
		}
	}
	if found && wc != nil {
		wc.PreviousOutput = captured
	} else {
		wc = &domain.WorkContext{
			ID:             ctxKey,
			TaskID:         taskID,
			PreviousOutput: captured,
		}
		if s.WorkContexts == nil {
			s.WorkContexts = make(map[string]*domain.WorkContext)
		}
		s.WorkContexts[ctxKey] = wc
	}
	if logger != nil {
		logger.Printf("WorkerManager: captured %d bytes of previous output for task #%d", len(captured), taskID)
	}
}

// SpawnForTask spawns a fresh worker process bound to a specific task.
// The task's full context (title, description, files, constraints) is rendered
// directly into the startup prompt so the worker begins working immediately.
// If all slots for the worker type are occupied, the task is queued and will
// be spawned when a slot frees up.
func (m *WorkerManager) SpawnForTask(taskID int, assignedTo string) {
	if len(m.configs) == 0 {
		return
	}
	if !m.checkMCPReady() {
		m.logger.Printf("WorkerManager: SpawnForTask(%d) skipped — MCP not ready", taskID)
		return
	}

	cfg := m.findConfigForAgent(assignedTo)
	if cfg == nil {
		m.logger.Printf("WorkerManager: SpawnForTask(%d) — no config for %q", taskID, assignedTo)
		return
	}

	if blocked, _ := m.failureBackoffBlocked(cfg.AgentType); blocked {
		m.logger.Printf("WorkerManager: SpawnForTask(%d) — %s in failure backoff, queuing", taskID, cfg.AgentType)
		m.enqueueSpawn(cfg.AgentType, taskID)
		return
	}

	running := m.countRunningByType(cfg.AgentType)
	limit := m.instanceLimitForType(cfg.AgentType)
	if running >= limit {
		m.logger.Printf("WorkerManager: SpawnForTask(%d) — %s at capacity (%d/%d), queuing", taskID, cfg.AgentType, running, limit)
		m.enqueueSpawn(cfg.AgentType, taskID)
		m.sendQueuedAck(taskID, cfg.AgentType, running, limit)
		return
	}

	m.spawnTaskWorker(taskID, *cfg)
}

// spawnTaskWorker reads task context from state and spawns a worker for it.
func (m *WorkerManager) spawnTaskWorker(taskID int, baseCfg WorkerSpawnConfig) {
	state, err := m.stateLoader()
	if err != nil {
		m.logger.Printf("WorkerManager: spawnTaskWorker(%d) state load error: %v", taskID, err)
		return
	}

	var task *domain.Task
	for i := range state.Tasks {
		if state.Tasks[i].ID == taskID {
			task = &state.Tasks[i]
			break
		}
	}
	if task == nil {
		m.logger.Printf("WorkerManager: spawnTaskWorker(%d) — task not found", taskID)
		return
	}

	var wc *domain.WorkContext
	for _, ctx := range state.WorkContexts {
		if ctx != nil && ctx.TaskID == taskID {
			wc = ctx
			break
		}
	}

	workspace := m.resolveWorkspace(state)
	if workspace == "" || workspace == "/" {
		m.logger.Printf("WorkerManager: spawnTaskWorker(%d) — no workspace", taskID)
		return
	}

	instanceID := fmt.Sprintf("%s-task-%d", baseCfg.AgentType, taskID)
	taskCfg := baseCfg
	taskCfg.InstanceID = instanceID

	taskPrompt := buildTaskPrompt(task, wc, instanceID, workspace, m.driver(), taskCfg.Communication)
	taskCfg.Command = appendPromptToCommand(baseCfg.Command, taskPrompt)
	if wc != nil && wc.WorktreeName != "" {
		taskCfg.ClaudeWorktreeName = wc.WorktreeName
	}

	spawnDir := workspace
	if m.worktreeManager != nil {
		wtPath, err := m.worktreeManager.EnsureWorktree(instanceID, workspace)
		if err != nil {
			m.logger.Printf("WorkerManager: worktree failed for %s: %v (falling back to shared dir)", instanceID, err)
		} else {
			spawnDir = wtPath
		}
	}

	m.logger.Printf("WorkerManager: spawning %s for task #%d (workspace=%s)", instanceID, taskID, spawnDir)
	connected := m.getAgent()
	m.sendTaskSpawnAck(instanceID, connected, taskID, task.Title)
	go func() {
		m.spawn(taskCfg, spawnDir)
		m.drainQueue(baseCfg.AgentType)
	}()
}

// buildTaskPrompt renders the full task context into a prompt section that is
// appended to the worker's base command prompt at spawn time.
// communication is "cli" or "mcp", controlling whether steps reference shell commands or MCP tools.
func buildTaskPrompt(task *domain.Task, wc *domain.WorkContext, instanceID, workspace, driver, communication string) string {
	priorityNames := map[int]string{1: "critical", 2: "high", 3: "normal", 4: "low"}
	priority := priorityNames[task.Priority]
	if priority == "" {
		priority = "normal"
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("\n\n--- YOUR ASSIGNED TASK (task #%d) ---\n", task.ID))
	b.WriteString(fmt.Sprintf("Title: %s\n", task.Title))
	if task.Description != "" {
		b.WriteString(fmt.Sprintf("Description: %s\n", task.Description))
	}
	b.WriteString(fmt.Sprintf("Priority: %s\n", priority))

	if wc != nil {
		if len(wc.RelevantFiles) > 0 {
			b.WriteString("\nRelevant files:\n")
			for _, f := range wc.RelevantFiles {
				b.WriteString(fmt.Sprintf("- %s\n", f))
			}
		}
		if wc.Background != "" {
			b.WriteString(fmt.Sprintf("\nBackground: %s\n", wc.Background))
		}
		if len(wc.Constraints) > 0 {
			b.WriteString("\nConstraints:\n")
			for _, c := range wc.Constraints {
				b.WriteString(fmt.Sprintf("- %s\n", c))
			}
		}
	}

	if wc != nil && wc.PreviousOutput != "" {
		b.WriteString("\n⚠️ IMPORTANT — PREVIOUS ATTEMPT CONTEXT:\n")
		b.WriteString("A previous worker attempted this task but was terminated. Their output is below.\n")
		b.WriteString("Review it carefully — DO NOT repeat completed work. Continue from where they left off.\n\n")
		b.WriteString(wc.PreviousOutput)
		b.WriteString("\n--- End of previous worker output ---\n")
	}

	if communication != "mcp" {
		bin := "$STRINGWORK_BIN"
		b.WriteString(fmt.Sprintf("\nCOMMUNICATION: Use shell commands (not MCP tools) for all coordination.\n"+
			"The env vars STRINGWORK_AGENT, STRINGWORK_WORKSPACE, STRINGWORK_SOCKET, and STRINGWORK_BIN are set.\n\n"+
			"⛔ MANDATORY REPORTING RULES (non-negotiable — violations trigger auto-cancellation):\n"+
			"  - You MUST call heartbeat every 60-90 seconds. No exceptions.\n"+
			"  - You MUST call progress every 2-3 minutes with task status. No exceptions.\n"+
			"  - Failure to report: 3min → WARNING, 5min → CRITICAL, silent+no output → AUTO-CANCEL.\n"+
			"  - These rules are enforced by the server. Non-compliant workers are terminated.\n\n"+
			"Steps:\n"+
			"1) Run: %s presence --agent %s --status working --workspace %s\n"+
			"2) Run: %s task update --id %d --status in_progress --by %s\n"+
			"3) Do the work. While working:\n"+
			"   - Every 60-90s:  %s heartbeat --agent %s --progress 'what you are doing'\n"+
			"   - Every 2-3min:  %s progress --agent %s --task %d --description 'status' --percent N\n"+
			"   - On first heartbeat, include --session-id YOUR_SESSION_ID so the server can resume your session if restarted\n"+
			"4) Run: %s send --from %s --to %s --content 'detailed summary of changes'\n"+
			"5) Run: %s task update --id %d --status completed --by %s\n",
			bin, instanceID, workspace,
			bin, task.ID, instanceID,
			bin, instanceID,
			bin, instanceID, task.ID,
			bin, instanceID, driver,
			bin, task.ID, instanceID,
		))
	} else {
		b.WriteString(fmt.Sprintf("\n⛔ MANDATORY REPORTING RULES (non-negotiable — violations trigger auto-cancellation):\n"+
			"  - You MUST call heartbeat every 60-90 seconds. No exceptions.\n"+
			"  - You MUST call report_progress every 2-3 minutes with task status. No exceptions.\n"+
			"  - Failure to report: 3min → WARNING, 5min → CRITICAL, silent+no output → AUTO-CANCEL.\n"+
			"  - These rules are enforced by the server. Non-compliant workers are terminated.\n\n"+
			"Steps:\n"+
			"1) set_presence agent='%s' status='working' workspace='%s'\n"+
			"2) update_task id=%d status='in_progress' updated_by='%s'\n"+
			"3) Do the work (heartbeat every 60-90s with session_id on first call, report_progress every 2-3min)\n"+
			"4) send_message from='%s' to='%s' with detailed summary of changes\n"+
			"5) update_task id=%d status='completed' updated_by='%s'\n",
			instanceID, workspace,
			task.ID, instanceID,
			instanceID, driver,
			task.ID, instanceID,
		))
	}

	return b.String()
}

// appendPromptToCommand appends taskPrompt to the prompt argument in the CLI
// command. It locates the prompt by finding the value after -p (claude) or
// --prompt (gemini), falling back to the last non-flag argument.
func appendPromptToCommand(baseCmd []string, taskPrompt string) []string {
	if len(baseCmd) == 0 {
		return baseCmd
	}
	result := make([]string, len(baseCmd))
	copy(result, baseCmd)

	promptIdx := -1
	for i, arg := range result {
		if (arg == "-p" || arg == "--prompt") && i+1 < len(result) {
			promptIdx = i + 1
			break
		}
	}

	if promptIdx < 0 {
		// Fallback: find the last argument that isn't a flag
		for i := len(result) - 1; i >= 0; i-- {
			if !strings.HasPrefix(result[i], "-") {
				promptIdx = i
				break
			}
		}
	}

	if promptIdx < 0 {
		promptIdx = len(result) - 1
	}

	result[promptIdx] = result[promptIdx] + taskPrompt
	return result
}

// findConfigForAgent returns the first WorkerSpawnConfig matching the given
// agent name (by InstanceID or AgentType).
func (m *WorkerManager) findConfigForAgent(agent string) *WorkerSpawnConfig {
	for i := range m.configs {
		if m.configs[i].InstanceID == agent || m.configs[i].AgentType == agent {
			return &m.configs[i]
		}
	}
	return nil
}

// countRunningByType counts how many worker processes of a given agent type are running.
func (m *WorkerManager) countRunningByType(agentType string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for id := range m.runningWorkers {
		for _, c := range m.configs {
			if c.InstanceID == id && c.AgentType == agentType {
				count++
				break
			}
		}
		if strings.HasPrefix(id, agentType+"-task-") {
			count++
		}
	}
	return count
}

// instanceLimitForType returns the configured instance count for an agent type.
func (m *WorkerManager) instanceLimitForType(agentType string) int {
	count := 0
	for _, c := range m.configs {
		if c.AgentType == agentType {
			count++
		}
	}
	if count == 0 {
		return 1
	}
	return count
}

// enqueueSpawn adds a task to the per-type spawn queue.
func (m *WorkerManager) enqueueSpawn(agentType string, taskID int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ps := range m.pendingSpawns[agentType] {
		if ps.TaskID == taskID {
			return // already queued
		}
	}
	m.pendingSpawns[agentType] = append(m.pendingSpawns[agentType], pendingSpawn{
		TaskID:    taskID,
		AgentType: agentType,
	})
}

// drainQueue checks the spawn queue for the given agent type and spawns
// the next queued task if a slot is available.
func (m *WorkerManager) drainQueue(agentType string) {
	m.mu.Lock()
	queue := m.pendingSpawns[agentType]
	if len(queue) == 0 {
		m.mu.Unlock()
		return
	}
	next := queue[0]
	m.pendingSpawns[agentType] = queue[1:]
	m.mu.Unlock()

	if blocked, _ := m.failureBackoffBlocked(agentType); blocked {
		m.logger.Printf("WorkerManager: drainQueue — %s in failure backoff, re-queuing task #%d", agentType, next.TaskID)
		m.enqueueSpawn(agentType, next.TaskID)
		return
	}

	running := m.countRunningByType(agentType)
	limit := m.instanceLimitForType(agentType)
	if running >= limit {
		m.enqueueSpawn(agentType, next.TaskID)
		return
	}

	cfg := m.findConfigForAgent(agentType)
	if cfg == nil {
		return
	}
	m.logger.Printf("WorkerManager: draining queue — spawning %s for task #%d", agentType, next.TaskID)
	m.spawnTaskWorker(next.TaskID, *cfg)
}

// PendingSpawnCount returns the number of tasks queued for a given agent type.
func (m *WorkerManager) PendingSpawnCount(agentType string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.pendingSpawns[agentType])
}

func (m *WorkerManager) sendTaskSpawnAck(instanceID, recipient string, taskID int, title string) {
	if recipient == "" || m.stateMutator == nil {
		return
	}
	content := fmt.Sprintf("⚡ **%s** spawning for task #%d: %s", instanceID, taskID, title)
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

func (m *WorkerManager) sendQueuedAck(taskID int, agentType string, running, limit int) {
	if m.stateMutator == nil {
		return
	}
	content := fmt.Sprintf("⏳ Task #%d queued for **%s** (%d/%d slots busy). Will spawn when a slot opens.",
		taskID, agentType, running, limit)
	_ = m.stateMutator(func(s *domain.CollabState) error {
		s.Messages = append(s.Messages, domain.Message{
			ID:        s.NextMsgID,
			From:      "system",
			To:        m.driver(),
			Content:   content,
			Timestamp: time.Now(),
		})
		s.NextMsgID++
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
			recipient = ConfiguredDriver(s)
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
