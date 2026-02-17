package app

import (
	"fmt"
	"log"
	"sync"

	"github.com/jaakkos/stringwork/internal/domain"
)

// Triggerable is something that can be triggered after a state write (e.g. Notifier).
type Triggerable interface {
	Trigger()
}

// CollabService runs collaboration use cases over persisted state.
type CollabService struct {
	repo     StateRepository
	policy   Policy
	logger   *log.Logger
	mu       sync.Mutex
	notifier Triggerable // optional; set via SetNotifier after construction
}

// NewCollabService returns a new CollabService.
func NewCollabService(repo StateRepository, policy Policy, logger *log.Logger) *CollabService {
	return &CollabService{repo: repo, policy: policy, logger: logger}
}

// SetNotifier attaches a Triggerable (e.g. *Notifier) that is poked after every state write.
// This ensures the auto-responder fires even when fsnotify misses same-process writes.
func (s *CollabService) SetNotifier(n Triggerable) {
	s.notifier = n
}

// Run loads state, runs fn, then saves. Caller must not retain state after fn returns.
// On successful save, touches the notify signal file so other agent processes can push updates.
// If the database cannot be loaded, the error is returned immediately — we never fall back to
// an empty state for writes, because Save() would overwrite the database with nothing.
func (s *CollabService) Run(fn func(*domain.CollabState) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.repo.Load()
	if err != nil {
		return fmt.Errorf("state load: %w", err)
	}
	EnsureStateMaps(state)
	EnsureAgentInstances(state, s.policy.Orchestration())
	if err := fn(state); err != nil {
		return err
	}
	if err := s.repo.Save(state); err != nil {
		return err
	}
	_ = TouchNotifySignal(s.policy.SignalFilePath())
	if s.notifier != nil {
		s.notifier.Trigger()
	}
	return nil
}

// Query loads state and runs fn without saving. Use for read-only checks (e.g. unread counts).
// If the database cannot be loaded, falls back to an empty state since no save will occur.
func (s *CollabService) Query(fn func(*domain.CollabState) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.repo.Load()
	if err != nil {
		s.logger.Printf("Warning: state load failed in Query: %v (using empty state)", err)
		state = domain.NewCollabState()
	}
	EnsureStateMaps(state)
	EnsureAgentInstances(state, s.policy.Orchestration())
	return fn(state)
}

// Policy returns the policy for use in handlers that need retention etc.
func (s *CollabService) Policy() Policy { return s.policy }
