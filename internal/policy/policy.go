// Package policy implements security guards for file paths, commands, and operations.
package policy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// GlobalStateDir returns the default global state directory (~/.config/stringwork).
func GlobalStateDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	return filepath.Join(home, ".config", "stringwork")
}

// GlobalStateFile returns the default global state file path.
func GlobalStateFile() string {
	return filepath.Join(GlobalStateDir(), "state.sqlite")
}

// DefaultSocketPath returns the default unix socket path for daemon communication.
// Useful for CLI commands that don't load the full config.
func DefaultSocketPath() string {
	return filepath.Join(GlobalStateDir(), "server.sock")
}

// WorkerConfig configures a worker type in the driver/worker orchestration model.
type WorkerConfig struct {
	Type               string   `yaml:"type"`                 // e.g. "claude-code", "codex"
	Instances          int      `yaml:"instances"`            // max concurrent instances (default 1)
	Command            []string `yaml:"command"`              // spawn command
	Capabilities       []string `yaml:"capabilities"`         // e.g. ["code-edit", "code-review"]
	MaxConcurrentTasks int      `yaml:"max_concurrent_tasks"` // per instance (default 1)
	CooldownSeconds    int      `yaml:"cooldown_seconds"`
	TimeoutSeconds     int      `yaml:"timeout_seconds"`
	RetryDelaySeconds  int      `yaml:"retry_delay_seconds"`
	MaxRetries         int      `yaml:"max_retries"`
	// Env sets additional environment variables for the spawned worker process.
	// Values can reference parent env vars with ${VAR} syntax (e.g. "home_dir: ${HOME}").
	// These are merged on top of the inherited environment.
	Env map[string]string `yaml:"env"`
	// InheritEnv is a list of glob patterns for env var names to inherit from the parent
	// process. By default, ALL env vars are inherited. Use this when you want to explicitly
	// ensure specific vars are passed (e.g. ["GH_*", "GITHUB_*", "SSH_AUTH_SOCK",
	// "DOCKER_HOST"]). If set to ["none"], no env vars are inherited (clean environment).
	InheritEnv []string `yaml:"inherit_env"`
	// UseClaudeWorktree, when true for claude-code workers, passes -w <instance_id> to the
	// Claude CLI so each worker runs in its own Git worktree under .claude/worktrees/.
	// Prevents branch/file conflicts when running multiple Claude Code workers in parallel.
	// Codex and Gemini do not have a native -w flag; for them, use orchestration.worktrees
	// (server-managed git worktrees) so each worker's process cwd is an isolated checkout.
	UseClaudeWorktree bool `yaml:"use_claude_worktree"`
	// Communication selects how workers talk to the Stringwork server.
	//   "cli" (default) — workers use shell commands (mcp-stringwork heartbeat, etc.)
	//                     that hit the daemon's REST API. More reliable, no MCP registration.
	//   "mcp"           — workers connect via MCP over HTTP (legacy). Requires mcp add
	//                     registration during spawn.
	Communication string `yaml:"communication"`
	// Model optionally overrides the LLM model used by the worker CLI. When set,
	// the spawner injects the appropriate `--model <value>` flag based on the
	// binary (claude, codex, gemini). If the user has already hard-coded
	// `--model` in `command`, the hard-coded value wins and this field is ignored.
	// Example values: "opus", "sonnet", "haiku" (claude); "gpt-5-codex" (codex);
	// "gemini-2.5-pro", "gemini-2.5-flash" (gemini). Any string accepted by the
	// underlying CLI works — Stringwork does not validate the model name.
	Model string `yaml:"model"`
}

// OrchestrationConfig holds driver/worker orchestration settings.
type OrchestrationConfig struct {
	Driver                   string          `yaml:"driver"` // agent type that is the driver, e.g. "cursor"
	Workers                  []WorkerConfig  `yaml:"workers"`
	AssignmentStrategy       string          `yaml:"assignment_strategy"` // least_loaded (default), capability_match, round_robin
	HeartbeatIntervalSeconds int             `yaml:"heartbeat_interval_seconds"`
	WorkerTimeoutSeconds     int             `yaml:"worker_timeout_seconds"`
	MaxTaskFailures          int             `yaml:"max_task_failures"` // DLQ: auto-block task after N watchdog failures
	Worktrees                *WorktreeConfig `yaml:"worktrees"`         // optional git worktree isolation
}

// MCPServerConfig describes an MCP server that should be auto-registered with
// worker CLIs (claude, codex) when they are spawned. Supports URL-based
// (HTTP/SSE) and command-based servers.
type MCPServerConfig struct {
	URL     string            `yaml:"url,omitempty"`     // For URL-based servers (HTTP/SSE)
	Command string            `yaml:"command,omitempty"` // For command-based servers
	Args    []string          `yaml:"args,omitempty"`    // Command arguments
	Env     map[string]string `yaml:"env,omitempty"`     // Environment variables for command
	Auth    string            `yaml:"auth,omitempty"`    // "oauth", "bearer", or empty for none
}

// WorktreeConfig controls git worktree isolation for workers.
type WorktreeConfig struct {
	Enabled         bool     `yaml:"enabled"`          // opt-in: create per-worker worktrees
	BaseBranch      string   `yaml:"base_branch"`      // base branch for worktrees (empty = current HEAD)
	CleanupStrategy string   `yaml:"cleanup_strategy"` // "on_cancel" (default), "on_exit", "manual"
	SetupCommands   []string `yaml:"setup_commands"`   // post-checkout setup commands (auto-detect if empty)
	Path            string   `yaml:"path"`             // worktree directory relative to workspace (default ".stringwork/worktrees")
}

// DaemonConfig controls the singleton daemon mode for multi-driver support.
// When enabled, the first invocation starts a background daemon process and
// subsequent invocations connect to it as thin stdio-to-HTTP proxies.
type DaemonConfig struct {
	Enabled         bool   `yaml:"enabled"`
	SocketPath      string `yaml:"socket_path"`
	PIDFile         string `yaml:"pid_file"`
	GracePeriodSecs int    `yaml:"grace_period_seconds"`
}

// Config holds policy configuration
type Config struct {
	WorkspaceRoot string   `yaml:"workspace_root"`
	EnabledTools  []string `yaml:"enabled_tools"`
	StateFile     string   `yaml:"state_file"`
	LogFile       string   `yaml:"log_file"`

	MessageRetentionMax  int `yaml:"message_retention_max"`
	MessageRetentionDays int `yaml:"message_retention_days"`
	PresenceTTLSeconds   int `yaml:"presence_ttl_seconds"`

	// PresenceRetentionDays controls how long stale Presence rows for
	// non-driver agents are kept before the watchdog GC removes them.
	// Default: 7 days. Set to 0 to disable presence GC.
	PresenceRetentionDays int `yaml:"presence_retention_days"`
	// InstanceRetentionDays controls how long offline static-pool
	// AgentInstance rows are kept before the watchdog GC removes them.
	// Default: 7 days. Set to 0 to disable static instance GC.
	InstanceRetentionDays int `yaml:"instance_retention_days"`
	// TaskBoundInstanceRetentionHours controls how long offline TASK-BOUND
	// AgentInstance rows (lifetime tied to a single task, e.g.
	// "claude-code-task-7") are kept. Shorter than InstanceRetentionDays
	// because task-bound rows are normally reaped on terminal task
	// transition — this knob is the safety net. Default: 24 hours. Set to 0
	// to disable task-bound GC.
	TaskBoundInstanceRetentionHours int `yaml:"task_bound_instance_retention_hours"`

	HTTPPort      int                        `yaml:"http_port"`
	Orchestration *OrchestrationConfig       `yaml:"orchestration"`
	MCPServers    map[string]MCPServerConfig `yaml:"mcp_servers"`
	Daemon        *DaemonConfig              `yaml:"daemon"`
	Audit         *AuditConfig               `yaml:"audit"`
}

// AuditConfig controls audit logging behavior.
// Uses `disabled: true` in config to turn off; omitting the section or field keeps audit ON.
type AuditConfig struct {
	Disabled      bool `yaml:"disabled"`
	ArgsMaxLen    int  `yaml:"args_max_len"`
	RetentionDays int  `yaml:"retention_days"`
}

// DefaultConfig returns sensible defaults. Orchestration is always set (driver cursor, no workers).
func DefaultConfig() *Config {
	return &Config{
		WorkspaceRoot:                   "",
		EnabledTools:                    []string{"*"},
		MessageRetentionMax:             1000,
		MessageRetentionDays:            30,
		PresenceTTLSeconds:              300,
		PresenceRetentionDays:           7,
		InstanceRetentionDays:           7,
		TaskBoundInstanceRetentionHours: 24,
		StateFile:                       "",
		Orchestration:                   DefaultOrchestration(),
	}
}

// DefaultOrchestration returns a minimal default (driver only, no workers) when config has none.
func DefaultOrchestration() *OrchestrationConfig {
	return &OrchestrationConfig{
		Driver:                   "cursor",
		Workers:                  nil,
		AssignmentStrategy:       "least_loaded",
		HeartbeatIntervalSeconds: 30,
		WorkerTimeoutSeconds:     120,
	}
}

// LoadConfig loads configuration from a YAML file.
// If orchestration is not set, DefaultOrchestration() is used (driver cursor, no workers).
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := DefaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if cfg.Orchestration == nil {
		cfg.Orchestration = DefaultOrchestration()
	}

	return cfg, nil
}

// Policy enforces security rules
type Policy struct {
	config *Config
	mu     sync.RWMutex // protects workspaceRoot for dynamic updates
}

// New creates a new policy enforcer
func New(cfg *Config) *Policy {
	return &Policy{config: cfg}
}

// WorkspaceRoot returns the current workspace root. This may differ from the
// config-file value if a client has called SetWorkspaceRoot at runtime.
func (p *Policy) WorkspaceRoot() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.config.WorkspaceRoot
}

// SetWorkspaceRoot dynamically changes the workspace root at runtime.
// This is called when a connected client sets its workspace via set_presence,
// allowing the server to follow the client into a different project directory.
func (p *Policy) SetWorkspaceRoot(root string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.config.WorkspaceRoot = root
}

// StateFile returns the configured state file path.
// If unset, defaults to the global state file (~/.config/stringwork/state.sqlite)
// so that all agents on the machine share the same state regardless of working directory.
func (p *Policy) StateFile() string {
	p.mu.RLock()
	sf := p.config.StateFile
	wsRoot := p.config.WorkspaceRoot
	p.mu.RUnlock()

	if sf == "" {
		return GlobalStateFile()
	}
	if filepath.IsAbs(sf) {
		return sf
	}
	return filepath.Join(wsRoot, sf)
}

// SignalFilePath returns the path to the notify signal file (same directory as state file).
// Watchers use this to detect state changes without relying on SQLite WAL file events.
func (p *Policy) SignalFilePath() string {
	return filepath.Join(filepath.Dir(p.StateFile()), ".stringwork-notify")
}

// LogFile returns the configured log file path.
// If unset, defaults to ~/.config/stringwork/mcp-stringwork.log.
// Set to "none" or "off" to disable file logging entirely.
func (p *Policy) LogFile() string {
	p.mu.RLock()
	lf := p.config.LogFile
	p.mu.RUnlock()

	if lf == "" {
		return filepath.Join(GlobalStateDir(), "mcp-stringwork.log")
	}
	return lf
}

// ValidatePath checks if a path is within the workspace
func (p *Policy) ValidatePath(path string) (string, error) {
	p.mu.RLock()
	wsRoot := p.config.WorkspaceRoot
	p.mu.RUnlock()

	// Resolve to absolute path
	if !filepath.IsAbs(path) {
		path = filepath.Join(wsRoot, path)
	}

	// Clean and resolve symlinks
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}

	// Check if path is within workspace
	relPath, err := filepath.Rel(wsRoot, absPath)
	if err != nil {
		return "", fmt.Errorf("relative path: %w", err)
	}

	if strings.HasPrefix(relPath, "..") {
		return "", fmt.Errorf("path %s is outside workspace", path)
	}

	return absPath, nil
}

// IsToolEnabled checks if a tool is enabled
func (p *Policy) IsToolEnabled(name string) bool {
	for _, t := range p.config.EnabledTools {
		if t == "*" || t == name {
			return true
		}
	}
	return false
}

// MessageRetentionMax returns the max messages to keep
func (p *Policy) MessageRetentionMax() int {
	return p.config.MessageRetentionMax
}

// MessageRetentionDays returns the message TTL in days
func (p *Policy) MessageRetentionDays() int {
	return p.config.MessageRetentionDays
}

// PresenceTTLSeconds returns the presence TTL in seconds
func (p *Policy) PresenceTTLSeconds() int {
	return p.config.PresenceTTLSeconds
}

// PresenceRetentionDays returns the GC retention window for stale Presence
// rows. Default 7 if unset.
func (p *Policy) PresenceRetentionDays() int {
	if p.config.PresenceRetentionDays > 0 {
		return p.config.PresenceRetentionDays
	}
	return 7
}

// InstanceRetentionDays returns the GC retention window for offline static
// pool AgentInstance rows. Default 7 if unset.
func (p *Policy) InstanceRetentionDays() int {
	if p.config.InstanceRetentionDays > 0 {
		return p.config.InstanceRetentionDays
	}
	return 7
}

// TaskBoundInstanceRetentionHours returns the GC retention window for
// offline task-bound AgentInstance rows. Default 24 if unset.
func (p *Policy) TaskBoundInstanceRetentionHours() int {
	if p.config.TaskBoundInstanceRetentionHours > 0 {
		return p.config.TaskBoundInstanceRetentionHours
	}
	return 24
}

// Orchestration returns the orchestration config (driver/workers). Never nil (default applied in LoadConfig).
func (p *Policy) Orchestration() *OrchestrationConfig {
	return p.config.Orchestration
}

// MaxTaskFailures returns the failure threshold before a task is auto-blocked (default: 3).
func (p *Policy) MaxTaskFailures() int {
	if p.config.Orchestration != nil && p.config.Orchestration.MaxTaskFailures > 0 {
		return p.config.Orchestration.MaxTaskFailures
	}
	return 3
}

// MCPServers returns the configured MCP servers that should be auto-registered
// with worker CLIs. Returns nil if no servers are configured.
func (p *Policy) MCPServers() map[string]MCPServerConfig {
	return p.config.MCPServers
}

// WorktreeConfig returns the worktree isolation configuration from orchestration.
// Returns nil if not configured.
func (p *Policy) WorktreeConfig() *WorktreeConfig {
	if p.config.Orchestration == nil {
		return nil
	}
	return p.config.Orchestration.Worktrees
}

// DaemonEnabled returns true if daemon mode is configured.
func (p *Policy) DaemonEnabled() bool {
	return p.config.Daemon != nil && p.config.Daemon.Enabled
}

// SocketPath returns the unix socket path for daemon communication.
func (p *Policy) SocketPath() string {
	if p.config.Daemon != nil && p.config.Daemon.SocketPath != "" {
		return p.config.Daemon.SocketPath
	}
	return filepath.Join(GlobalStateDir(), "server.sock")
}

// PIDFile returns the daemon PID file path.
func (p *Policy) PIDFile() string {
	if p.config.Daemon != nil && p.config.Daemon.PIDFile != "" {
		return p.config.Daemon.PIDFile
	}
	return filepath.Join(GlobalStateDir(), "daemon.pid")
}

// DaemonGracePeriodSeconds returns how long the daemon waits after the last
// driver disconnects before shutting down.
func (p *Policy) DaemonGracePeriodSeconds() int {
	if p.config.Daemon != nil && p.config.Daemon.GracePeriodSecs > 0 {
		return p.config.Daemon.GracePeriodSecs
	}
	return 10
}

// AuditEnabled returns whether audit logging is active (default: true).
// Uses a `disabled` field so the Go zero value (false) means "not disabled" = enabled.
func (p *Policy) AuditEnabled() bool {
	if p.config.Audit == nil {
		return true
	}
	return !p.config.Audit.Disabled
}

// AuditArgsMaxLen returns the max length for audit arg summaries (default: 1000).
func (p *Policy) AuditArgsMaxLen() int {
	if p.config.Audit != nil && p.config.Audit.ArgsMaxLen > 0 {
		return p.config.Audit.ArgsMaxLen
	}
	return 1000
}

// AuditRetentionDays returns how many days of audit logs to keep (default: 7).
func (p *Policy) AuditRetentionDays() int {
	if p.config.Audit != nil && p.config.Audit.RetentionDays > 0 {
		return p.config.Audit.RetentionDays
	}
	return 7
}
