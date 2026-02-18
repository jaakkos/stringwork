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
}

// OrchestrationConfig holds driver/worker orchestration settings.
type OrchestrationConfig struct {
	Driver                   string          `yaml:"driver"` // agent type that is the driver, e.g. "cursor"
	Workers                  []WorkerConfig  `yaml:"workers"`
	AssignmentStrategy       string          `yaml:"assignment_strategy"` // least_loaded (default), capability_match, round_robin
	HeartbeatIntervalSeconds int             `yaml:"heartbeat_interval_seconds"`
	WorkerTimeoutSeconds     int             `yaml:"worker_timeout_seconds"`
	Worktrees                *WorktreeConfig `yaml:"worktrees"` // optional git worktree isolation
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

// KnowledgeConfig controls the FTS5-based project knowledge indexer.
type KnowledgeConfig struct {
	Enabled              bool `yaml:"enabled"`                // enable the knowledge indexer and query_knowledge tool
	IndexGoSource        bool `yaml:"index_go_source"`        // index .go source files (signatures, comments)
	WatchIntervalSeconds int  `yaml:"watch_interval_seconds"` // state sync interval (default 60)
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

// FeaturesConfig groups optional feature flags.
type FeaturesConfig struct {
	Knowledge *KnowledgeConfig `yaml:"knowledge"`
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

	HTTPPort      int                        `yaml:"http_port"`
	Orchestration *OrchestrationConfig       `yaml:"orchestration"`
	MCPServers    map[string]MCPServerConfig `yaml:"mcp_servers"`
	Features      *FeaturesConfig            `yaml:"features"`
	Daemon        *DaemonConfig              `yaml:"daemon"`
}

// DefaultConfig returns sensible defaults. Orchestration is always set (driver cursor, no workers).
func DefaultConfig() *Config {
	return &Config{
		WorkspaceRoot:        "",
		EnabledTools:         []string{"*"},
		MessageRetentionMax:  1000,
		MessageRetentionDays: 30,
		PresenceTTLSeconds:   300,
		StateFile:            "",
		Orchestration:        DefaultOrchestration(),
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

// Orchestration returns the orchestration config (driver/workers). Never nil (default applied in LoadConfig).
func (p *Policy) Orchestration() *OrchestrationConfig {
	return p.config.Orchestration
}

// MCPServers returns the configured MCP servers that should be auto-registered
// with worker CLIs. Returns nil if no servers are configured.
func (p *Policy) MCPServers() map[string]MCPServerConfig {
	return p.config.MCPServers
}

// KnowledgeConfig returns the knowledge indexer configuration.
// Returns nil if not configured (feature disabled).
func (p *Policy) KnowledgeConfig() *KnowledgeConfig {
	if p.config.Features == nil {
		return nil
	}
	return p.config.Features.Knowledge
}

// KnowledgeDBPath returns the path for the knowledge FTS5 database.
// It lives alongside the state file.
func (p *Policy) KnowledgeDBPath() string {
	return filepath.Join(filepath.Dir(p.StateFile()), "knowledge.db")
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
