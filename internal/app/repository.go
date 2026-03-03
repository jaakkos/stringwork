// Package app implements application use cases and defines ports (repository interfaces).
package app

import (
	"github.com/jaakkos/stringwork/internal/domain"
	"time"
)

// StateRepository loads and saves the full collaboration state.
// Implementation: internal/repository/sqlite.
type StateRepository interface {
	Load() (*domain.CollabState, error)
	Save(*domain.CollabState) error
}

// AuditFilter defines criteria for querying audit logs.
type AuditFilter struct {
	Agent     string
	ToolName  string
	SessionID string
	From      time.Time
	To        time.Time
	Limit     int
}

// AuditWriter records tool calls and significant events.
type AuditWriter interface {
	WriteAudit(entry domain.AuditEntry) error
	PruneAudit(olderThan time.Time) (int64, error)
}

// AuditReader queries recorded audit logs.
type AuditReader interface {
	ReadAudit(filter AuditFilter) ([]domain.AuditEntry, error)
}
