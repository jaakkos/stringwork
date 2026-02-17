package repository

import (
	"github.com/jaakkos/stringwork/internal/app"
	"github.com/jaakkos/stringwork/internal/repository/sqlite"
)

// NewStateRepository returns a StateRepository backed by SQLite at the given path.
// The path is typically from policy.StateFile() (default ~/.config/stringwork/state.sqlite).
func NewStateRepository(path string) (app.StateRepository, error) {
	return sqlite.New(path)
}
