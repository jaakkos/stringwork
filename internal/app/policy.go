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
}
