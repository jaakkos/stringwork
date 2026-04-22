package app

import "github.com/jaakkos/stringwork/internal/policy"

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
}
