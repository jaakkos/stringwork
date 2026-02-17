// Package app implements application use cases and defines ports (repository interfaces).
package app

import (
	"github.com/jaakkos/stringwork/internal/domain"
)

// StateRepository loads and saves the full collaboration state.
// Implementation: internal/repository/sqlite.
type StateRepository interface {
	Load() (*domain.CollabState, error)
	Save(*domain.CollabState) error
}
