package app

import (
	"time"

	"github.com/jaakkos/stringwork/internal/constitution"
	"github.com/jaakkos/stringwork/internal/policy"
	"github.com/jaakkos/stringwork/internal/tasktemplates"
)

// Policy is the configuration port used by the application.
// Implemented by internal/policy.Policy.
type Policy interface {
	MessageRetentionMax() int
	MessageRetentionDays() int
	PresenceTTLSeconds() int
	StateFile() string
	SignalFilePath() string
	WorkspaceRoot() string
	SetWorkspaceRoot(root string)
	IsToolEnabled(name string) bool
	ValidatePath(path string) (string, error)
	Orchestration() *policy.OrchestrationConfig
	MaxTaskFailures() int
	AuditEnabled() bool
	AuditArgsMaxLen() int
	AuditRetentionDays() int
	// PresenceRetentionDays returns the number of days a non-driver Presence
	// row is retained after its last_seen before the watchdog GCs it.
	PresenceRetentionDays() int
	// InstanceRetentionDays returns the number of days a static-pool
	// AgentInstance row is retained after going offline before the watchdog
	// GCs it.
	InstanceRetentionDays() int
	// TaskBoundInstanceRetentionHours returns the number of HOURS a
	// task-bound AgentInstance row is retained after going offline. Shorter
	// than InstanceRetentionDays because task-bound rows should normally be
	// reaped on terminal task transition; this is the GC safety net.
	TaskBoundInstanceRetentionHours() int
	// RespawnGrace returns how long after WorkerManager records a spawn
	// (AgentInstance.LastSpawnedAt) the watchdog leaves the row alone, so
	// freshly-spawned workers aren't flipped back to offline before their
	// first heartbeat lands. Default 60s. See worker_manager.MarkInstanceSpawning.
	RespawnGrace() time.Duration
	// SpawnSweepGrace returns how old a pending task must be before the
	// watchdog's spawn sweep re-drives an assignment for it. Default 30s.
	// Returning 0 disables the sweep entirely.
	SpawnSweepGrace() time.Duration

	// ConstitutionSources returns the ordered list of layered guidance
	// sources surfaced to workers via claim_next, get_work_context, and
	// the inline preamble at spawn time. Returns an empty slice when no
	// constitution is configured (a no-op for users who haven't opted
	// in).
	ConstitutionSources() []constitution.Source

	// TaskTemplateSources returns the ordered list of task-template
	// overlay sources used by the task_plan MCP tool. Always includes
	// the built-in defaults (one Source returning the embedded
	// code-review template); team / user sources extend that.
	TaskTemplateSources() []tasktemplates.Source
}
