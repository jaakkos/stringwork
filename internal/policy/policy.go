// Package policy implements security guards for file paths, commands, and operations.
package policy

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/jaakkos/stringwork/internal/constitution"
	"github.com/jaakkos/stringwork/internal/tasktemplates"
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

// GlobalConstitutionDir returns the conventional location of the
// per-user constitution directory (~/.config/stringwork/constitution).
// This is the built-in source: any *.md files placed here are read as
// guidance for every task on every claim. Users can extend this via
// the `constitution.sources` block in config.yaml (see R4).
func GlobalConstitutionDir() string {
	return filepath.Join(GlobalStateDir(), "constitution")
}

// DefaultConfigFile returns the conventional config-file path
// (~/.config/stringwork/config.yaml). Used as the auto-discovery fallback
// when MCP_CONFIG isn't set in the environment, so that bare invocations like
// `mcp-stringwork --daemon` honour the user's config file the same way
// MCP-launched stdio invocations do.
func DefaultConfigFile() string {
	return filepath.Join(GlobalStateDir(), "config.yaml")
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
	// RespawnGraceSeconds is how long after a worker spawn the watchdog
	// leaves the AgentInstance row alone (no offline-marking, no recovery).
	// Lets a freshly-spawned worker bootstrap and emit its first heartbeat
	// before liveness checks engage. Default: 60 seconds.
	RespawnGraceSeconds int `yaml:"respawn_grace_seconds"`
	// SpawnSweepGraceSeconds is how old a pending task must be before the
	// watchdog re-drives an assignment for it. The normal create_task →
	// SpawnForTask path handles the happy case; the sweep is the safety net
	// for tasks that were created when no live worker was available (e.g.
	// after a crash, or when the orchestrator's pool was empty). Default:
	// 30 seconds. Set to 0 to disable the sweep.
	SpawnSweepGraceSeconds int `yaml:"spawn_sweep_grace_seconds"`
	// ModelTiers maps tier names to concrete CLI model names per worker type
	// (claude-code, codex, gemini). The driver sets task.model_tier on
	// create_task; spawn resolves it here.
	// Example: fast: {claude-code: haiku, codex: o4-mini, gemini: gemini-2.5-flash}
	ModelTiers map[string]map[string]string `yaml:"model_tiers"`
	// QuotaPreflight enables zero-token HTTP quota checks before worker spawn.
	QuotaPreflight *QuotaPreflightConfig `yaml:"quota_preflight"`
}

// QuotaPreflightConfig controls background quota cache refresh and spawn gating.
type QuotaPreflightConfig struct {
	Enabled                  bool  `yaml:"enabled"`
	CacheTTLSeconds          int   `yaml:"cache_ttl_seconds"`
	BackgroundRefreshSeconds int   `yaml:"background_refresh_seconds"`
	FailOpen                 *bool `yaml:"fail_open"`
}

// ResolvedQuotaPreflight returns quota preflight settings with defaults applied.
func (o *OrchestrationConfig) ResolvedQuotaPreflight() QuotaPreflightConfig {
	def := QuotaPreflightConfig{
		CacheTTLSeconds:          120,
		BackgroundRefreshSeconds: 300,
	}
	failOpen := true
	if o == nil || o.QuotaPreflight == nil {
		def.FailOpen = &failOpen
		return def
	}
	q := *o.QuotaPreflight
	if q.CacheTTLSeconds <= 0 {
		q.CacheTTLSeconds = 120
	}
	if q.BackgroundRefreshSeconds == 0 && !o.QuotaPreflight.Enabled {
		q.BackgroundRefreshSeconds = 300
	}
	if o.QuotaPreflight.FailOpen != nil {
		failOpen = *o.QuotaPreflight.FailOpen
	}
	q.FailOpen = &failOpen
	return q
}

// QuotaPreflightEnabled reports whether spawn-time quota cache reads are active.
func (o *OrchestrationConfig) QuotaPreflightEnabled() bool {
	if o == nil || o.QuotaPreflight == nil {
		return false
	}
	return o.QuotaPreflight.Enabled
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
	Backup        *BackupConfig              `yaml:"backup"`
	// Constitution declares user- and team-level rule sources that
	// extend the built-in ~/.config/stringwork/constitution directory.
	// Sources are ordered: built-in global first, then profile (R4.c),
	// then user-declared `sources`. Earlier files win on conflict.
	Constitution *ConstitutionConfig `yaml:"constitution,omitempty"`

	// TaskTemplates declares user- and team-level task-template
	// overlay sources that extend the built-in defaults baked into
	// the binary (see internal/tasktemplates/defaults). Sources are
	// ordered: built-in defaults first, then profile, then
	// user-declared `sources`. Earlier sources win on replace-by-id
	// merge slots; append-style slots (routing, classifiers,
	// checklists) preserve declaration order across sources.
	TaskTemplates *TaskTemplateConfig `yaml:"task_templates,omitempty"`
}

// AuditConfig controls audit logging behavior.
// Uses `disabled: true` in config to turn off; omitting the section or field keeps audit ON.
type AuditConfig struct {
	Disabled      bool `yaml:"disabled"`
	ArgsMaxLen    int  `yaml:"args_max_len"`
	RetentionDays int  `yaml:"retention_days"`
}

// BackupConfig controls the rotating SQLite backup taken on every server start.
// Uses `disabled: true` in config to turn off; omitting the section or field keeps backups ON.
// Files are written next to state.sqlite as state.sqlite.bak.<unix-seconds>.
type BackupConfig struct {
	Disabled bool `yaml:"disabled"`
	KeepN    int  `yaml:"keep_n"`
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
		Backup:                          &BackupConfig{Disabled: false, KeepN: 5},
	}
}

// DefaultOrchestration returns a minimal default (driver only, no workers) when config has none.
//
// WorkerTimeoutSeconds is the single knob that overrides the watchdog's
// heartbeat/session/task-stuck thresholds (see watchdog.go: when set, it
// becomes heartbeatStaleThresh and taskStuckThresh=2×). Bumped from 120s
// in Q2/2026 — at 120s, the override drove heartbeat staleness down to
// 2 min, well below the progress-warning threshold (now 4 min), which
// could mark a still-working worker offline before the progress alerts
// even fired. 180s keeps the override under the new heartbeat default
// (7 min) so the watchdog uses the override exactly as intended:
// admin-tunable but no longer punitively short.
func DefaultOrchestration() *OrchestrationConfig {
	return &OrchestrationConfig{
		Driver:                   "cursor",
		Workers:                  nil,
		AssignmentStrategy:       "least_loaded",
		HeartbeatIntervalSeconds: 30,
		WorkerTimeoutSeconds:     180,
	}
}

// LoadConfig loads configuration from a YAML file.
// If orchestration is not set, DefaultOrchestration() is used (driver cursor, no workers).
//
// The decoder runs in KnownFields(true) mode so that typos in any
// top-level key (e.g. `sourcs:` for `sources:` under `constitution`)
// or any nested block surface as a clear `parse config: <path>: ...`
// error instead of silently dropping the field on the floor. Every
// legitimately-supported field has an explicit `yaml:"..."` tag, so
// strict mode is safe for all known-good configs.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := DefaultConfig()
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
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

	// Cache for ConstitutionSources. Held under its own mutex so a
	// slow YAML re-parse during a rebuild does not block
	// SetWorkspaceRoot or other config-mutating callers. Invalidated
	// when the profile file mtime changes or the in-memory user
	// `sources` declaration differs from the cached snapshot.
	consMu        sync.Mutex
	consCache     []constitution.Source
	consCacheKey  constitutionCacheKey
	consCacheInit bool
}

// constitutionCacheKey is the snapshot used to decide whether the
// cached ConstitutionSources slice is still valid. Profile mtime
// catches edits to the team-shared profile file (cheap stat per
// call); the user sources snapshot catches in-process config swaps
// (e.g. a future hot-reload). Comparison is by value via
// constitutionCacheKey.equal.
type constitutionCacheKey struct {
	profilePath  string
	profileMTime time.Time
	profileMiss  bool // distinguishes "stat failed" from "absent profile"
	sources      []ConstitutionSourceConfig
}

func (a constitutionCacheKey) equal(b constitutionCacheKey) bool {
	if a.profilePath != b.profilePath ||
		a.profileMiss != b.profileMiss ||
		!a.profileMTime.Equal(b.profileMTime) ||
		len(a.sources) != len(b.sources) {
		return false
	}
	for i := range a.sources {
		if !sourceConfigEqual(a.sources[i], b.sources[i]) {
			return false
		}
	}
	return true
}

// sourceConfigEqual compares two ConstitutionSourceConfig values for
// cache-key equality. Slices are compared element-wise; the *Scope
// pointer is handled structurally so two identical declarations from
// different decode passes still match.
func sourceConfigEqual(a, b ConstitutionSourceConfig) bool {
	if a.Name != b.Name ||
		a.Type != b.Type ||
		a.Path != b.Path ||
		a.Repo != b.Repo ||
		a.Ref != b.Ref ||
		a.CacheDir != b.CacheDir {
		return false
	}
	if !stringSliceEqual(a.Include, b.Include) ||
		!stringSliceEqual(a.Paths, b.Paths) {
		return false
	}
	switch {
	case a.Scope == nil && b.Scope == nil:
		return true
	case a.Scope == nil || b.Scope == nil:
		return false
	}
	return stringSliceEqual(a.Scope.TaskKind, b.Scope.TaskKind) &&
		stringSliceEqual(a.Scope.AgentRoles, b.Scope.AgentRoles)
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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

// RespawnGrace returns how long after a worker spawn the watchdog leaves the
// AgentInstance row alone. Defaults to 60 seconds when not configured.
// Set to a negative value (or 0 with explicit intent in code) to disable
// — but the default avoids tight respawn ↔ watchdog flap loops, so consumers
// of the negative path should know what they're giving up.
func (p *Policy) RespawnGrace() time.Duration {
	if p.config.Orchestration != nil && p.config.Orchestration.RespawnGraceSeconds > 0 {
		return time.Duration(p.config.Orchestration.RespawnGraceSeconds) * time.Second
	}
	return 60 * time.Second
}

// SpawnSweepGrace returns how old a pending task must be before the watchdog
// re-drives an assignment for it. Defaults to 30 seconds when not configured.
// Returning 0 explicitly via config disables the sweep.
func (p *Policy) SpawnSweepGrace() time.Duration {
	if p.config.Orchestration == nil {
		return 30 * time.Second
	}
	if p.config.Orchestration.SpawnSweepGraceSeconds < 0 {
		return 0
	}
	if p.config.Orchestration.SpawnSweepGraceSeconds > 0 {
		return time.Duration(p.config.Orchestration.SpawnSweepGraceSeconds) * time.Second
	}
	return 30 * time.Second
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

// BackupEnabled reports whether a rotating backup of state.sqlite should be
// taken on every server start. Default is true; the user can opt out by
// setting `backup.disabled: true` in config.
func (p *Policy) BackupEnabled() bool {
	if p.config.Backup == nil {
		return true
	}
	return !p.config.Backup.Disabled
}

// BackupKeepN returns how many auto-generated backups to retain after the
// rotation step. Defaults to 5. Values are clamped to [1, 50] so a typo can't
// silently disable retention or fill the user's disk.
func (p *Policy) BackupKeepN() int {
	const (
		def    = 5
		minKey = 1
		maxKey = 50
	)
	if p.config.Backup == nil || p.config.Backup.KeepN <= 0 {
		return def
	}
	if p.config.Backup.KeepN < minKey {
		return minKey
	}
	if p.config.Backup.KeepN > maxKey {
		return maxKey
	}
	return p.config.Backup.KeepN
}

// ConstitutionDir returns the on-disk location of the built-in
// per-user constitution directory. It is always
// ~/.config/stringwork/constitution; the path is stable across
// invocations so the `constitution init` and `constitution show` CLI
// subcommands write/read the same place.
func (p *Policy) ConstitutionDir() string {
	return GlobalConstitutionDir()
}

// ConstitutionSources returns the ordered list of constitution sources
// to feed to constitution.Resolve. The order is:
//
//  1. The built-in `global` DirSource pointing at ConstitutionDir().
//     Always included so a user with no config file still gets their
//     personal rules attached.
//  2. Sources loaded from the team profile file referenced by
//     `constitution.profile`, in declaration order. Profile sources
//     win over user sources for conflicts (earlier source wins on
//     conflict). `$PROFILE_DIR` is expanded to the profile file's
//     directory so a team can ship a single shared file.
//  3. Sources declared via config.yaml's `constitution.sources` block,
//     preserved in declaration order.
//
// The result is cached on the Policy and reused while the in-process
// user `sources` declaration is unchanged AND the profile file's
// mtime has not advanced. claim_next / get_work_context call this on
// every invocation; without the cache the YAML profile parse plus
// per-decl validation re-runs on every claim and adds measurable I/O
// when the profile lives on a network filesystem (ZFS sends, NFS,
// etc.). The cache hit path returns a fresh slice header so callers
// remain free to mutate it without affecting subsequent invocations;
// Source implementations themselves are still stateless wrt the
// resolver (they re-read the filesystem on every List()).
//
// Bad source declarations are logged to stderr and skipped — a typo
// in one team rule entry must not nuke the worker's view of the rest
// of the constitution.
func (p *Policy) ConstitutionSources() []constitution.Source {
	p.mu.RLock()
	var profile string
	var sources []ConstitutionSourceConfig
	if p.config.Constitution != nil {
		profile = p.config.Constitution.Profile
		if len(p.config.Constitution.Sources) > 0 {
			sources = append([]ConstitutionSourceConfig(nil), p.config.Constitution.Sources...)
		}
	}
	p.mu.RUnlock()

	mtime, miss := constitutionProfileMTime(profile)
	key := constitutionCacheKey{
		profilePath:  profile,
		profileMTime: mtime,
		profileMiss:  miss,
		sources:      sources,
	}

	p.consMu.Lock()
	defer p.consMu.Unlock()
	if p.consCacheInit && p.consCacheKey.equal(key) {
		return append([]constitution.Source(nil), p.consCache...)
	}

	out := []constitution.Source{
		&constitution.DirSource{
			SourceName: "global",
			Path:       p.ConstitutionDir(),
			Include:    []string{"*.md"},
		},
	}
	if profile != "" {
		out = append(out, constitutionProfileSources(profile)...)
	}
	for _, decl := range sources {
		src, err := decl.toSource("")
		if err != nil {
			log.Printf("constitution: skipping source %q: %v", decl.Name, err)
			continue
		}
		if src != nil {
			out = append(out, src)
		}
	}

	p.consCache = out
	p.consCacheKey = key
	p.consCacheInit = true
	return append([]constitution.Source(nil), out...)
}

// TaskTemplateSources returns the ordered list of task-template
// sources to feed to tasktemplates.Resolve. The order is:
//
//  1. The built-in `stringwork-defaults` EmbedSource baked into the
//     binary. Always included so a user with no config still gets
//     the default code-review template.
//  2. Sources loaded from the team profile file referenced by
//     `task_templates.profile`, in declaration order. Profile sources
//     win over user sources for replace-by-id merge slots.
//  3. Sources declared via config.yaml's `task_templates.sources`
//     block, preserved in declaration order.
//
// Bad source declarations are logged and skipped — a typo in one team
// overlay must not nuke the worker's view of the other templates.
//
// No mtime cache here (yet): unlike the constitution path, task-template
// resolution only fires inside `task_plan` calls (interactive driver
// path), not on every claim. The YAML parse cost is once-per-call,
// which is acceptable. If the planner moves to a hot path later, mirror
// the consCache pattern from ConstitutionSources.
func (p *Policy) TaskTemplateSources() []tasktemplates.Source {
	p.mu.RLock()
	var profile string
	var sources []TaskTemplateSourceConfig
	if p.config.TaskTemplates != nil {
		profile = p.config.TaskTemplates.Profile
		if len(p.config.TaskTemplates.Sources) > 0 {
			sources = append([]TaskTemplateSourceConfig(nil), p.config.TaskTemplates.Sources...)
		}
	}
	p.mu.RUnlock()

	out := []tasktemplates.Source{tasktemplates.DefaultEmbeddedSource()}
	if profile != "" {
		out = append(out, taskTemplateProfileSources(profile)...)
	}
	for _, decl := range sources {
		src, err := decl.toSource("")
		if err != nil {
			log.Printf("task templates: skipping source %q: %v", decl.Name, err)
			continue
		}
		if src != nil {
			out = append(out, src)
		}
	}
	return out
}

// constitutionProfileMTime stat-probes the profile file and returns
// its modification time. Returns (zero, false) when path is empty
// (no profile configured — cache is keyed on the empty path) and
// (zero, true) when the stat fails (file missing, perms, etc) so a
// transient stat error invalidates the cache exactly once and the
// rebuild path surfaces the underlying problem via constitutionProfileSources.
func constitutionProfileMTime(path string) (time.Time, bool) {
	if path == "" {
		return time.Time{}, false
	}
	expanded, err := expandPath(path, "")
	if err != nil {
		return time.Time{}, true
	}
	info, err := os.Stat(expanded)
	if err != nil {
		return time.Time{}, true
	}
	return info.ModTime(), false
}
