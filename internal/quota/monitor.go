package quota

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// MonitorConfig controls cache TTL and refresh behaviour.
type MonitorConfig struct {
	CacheTTL time.Duration
	FailOpen bool
}

// SnapshotEntry is one agent type's cached quota state for MCP/CLI output.
type SnapshotEntry struct {
	AgentType string    `json:"agent_type"`
	State     string    `json:"state"` // OK, BLOCKED, unknown
	Summary   string    `json:"summary"`
	Reason    string    `json:"reason,omitempty"`
	Blocked   bool      `json:"blocked"`
	CheckedAt time.Time `json:"checked_at,omitempty"`
	Age       string    `json:"age,omitempty"`
}

// TransitionCallback fires when an agent type's blocked state changes after refresh.
type TransitionCallback func(agentType string, wasBlocked, nowBlocked bool)

// Monitor aggregates checkers and serves cache-read-only spawn decisions.
type Monitor struct {
	mu           sync.RWMutex
	checkers     map[string]Checker
	cache        *Cache
	cfg          MonitorConfig
	onTransition TransitionCallback
	logger       *log.Logger
	refreshing   map[string]bool
	wasBlocked   map[string]bool
}

// NewMonitor creates a monitor for the given checkers.
func NewMonitor(checkers []Checker, cfg MonitorConfig, logger *log.Logger) *Monitor {
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = 2 * time.Minute
	}
	if logger == nil {
		logger = log.Default()
	}
	m := &Monitor{
		checkers:   make(map[string]Checker, len(checkers)),
		cache:      NewCache(cfg.CacheTTL),
		cfg:        cfg,
		logger:     logger,
		refreshing: make(map[string]bool),
		wasBlocked: make(map[string]bool),
	}
	for _, c := range checkers {
		if c == nil {
			continue
		}
		m.checkers[c.AgentType()] = c
	}
	return m
}

// SetOnTransition registers a callback for blocked→available transitions.
func (m *Monitor) SetOnTransition(cb TransitionCallback) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onTransition = cb
}

// Blocked reports whether spawn should be deferred based on cached quota only.
// Stale or missing cache → fail open and kick async refresh.
func (m *Monitor) Blocked(agentType string) (blocked bool, until time.Time, reason string) {
	if len(m.checkers) == 0 {
		return false, time.Time{}, ""
	}
	status, fresh := m.cache.Get(agentType)
	if !fresh {
		m.kickRefresh(agentType)
		return false, time.Time{}, ""
	}
	if status.IsBlocked() {
		until = status.ResetAt
		if until.IsZero() {
			until = time.Now().Add(m.cfg.CacheTTL)
		}
		return true, until, "quota-preflight"
	}
	return false, time.Time{}, ""
}

// BlockedInfo returns backoff-style info for merging into WorkerManager.
func (m *Monitor) BlockedInfo(agentType string) (blocked bool, remaining time.Duration, reason string) {
	b, until, r := m.Blocked(agentType)
	if !b {
		return false, 0, ""
	}
	rem := time.Until(until)
	if rem < 0 {
		rem = 0
	}
	return true, rem, r
}

// BlockedTypes returns agent types with an explicit cached block.
func (m *Monitor) BlockedTypes() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []string
	for agentType := range m.checkers {
		if blocked, _, _ := m.blockedFromCache(agentType); blocked {
			out = append(out, agentType)
		}
	}
	return out
}

func (m *Monitor) blockedFromCache(agentType string) (bool, time.Time, string) {
	status, fresh := m.cache.Get(agentType)
	if !fresh || !status.IsBlocked() {
		return false, time.Time{}, ""
	}
	until := status.ResetAt
	if until.IsZero() {
		until = time.Now().Add(m.cfg.CacheTTL)
	}
	return true, until, status.Summary
}

// Snapshot returns cached quota state for all configured checkers.
func (m *Monitor) Snapshot() []SnapshotEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]SnapshotEntry, 0, len(m.checkers))
	now := time.Now()
	for agentType := range m.checkers {
		status, checkedAt, ok := m.cache.GetStale(agentType)
		entry := SnapshotEntry{AgentType: agentType}
		if !ok {
			entry.State = "unknown"
			entry.Summary = "not checked yet"
			out = append(out, entry)
			continue
		}
		entry.CheckedAt = checkedAt
		entry.Age = now.Sub(checkedAt).Round(time.Second).String() + " ago"
		if status.IsBlocked() {
			entry.State = "BLOCKED"
			entry.Blocked = true
			entry.Summary = status.Summary
			entry.Reason = status.Reason
		} else {
			entry.State = "OK"
			entry.Summary = status.Summary
			if entry.Summary == "" {
				entry.Summary = "OK"
			}
		}
		out = append(out, entry)
	}
	return out
}

// SetCachedStatus seeds the cache without an HTTP round trip (tests / warm-start).
func (m *Monitor) SetCachedStatus(agentType string, status Status) {
	m.applyRefresh(agentType, status)
}

// Refresh runs all checkers and updates the cache.
func (m *Monitor) Refresh(ctx context.Context) {
	m.mu.RLock()
	types := make([]string, 0, len(m.checkers))
	for t := range m.checkers {
		types = append(types, t)
	}
	m.mu.RUnlock()
	for _, agentType := range types {
		m.RefreshType(ctx, agentType)
	}
}

// RefreshType runs one checker and updates the cache.
func (m *Monitor) RefreshType(ctx context.Context, agentType string) Status {
	m.mu.RLock()
	checker := m.checkers[agentType]
	m.mu.RUnlock()
	if checker == nil {
		return Status{}
	}
	status := checker.Check(ctx)
	m.applyRefresh(agentType, status)
	return status
}

// CheckDirect runs a live HTTP check without reading the cache (for CLI).
func (m *Monitor) CheckDirect(ctx context.Context, agentType string) Status {
	m.mu.RLock()
	checker := m.checkers[agentType]
	m.mu.RUnlock()
	if checker == nil {
		return CheckFailed(fmt.Errorf("unknown agent type %q", agentType))
	}
	return checker.Check(ctx)
}

func (m *Monitor) applyRefresh(agentType string, status Status) {
	m.mu.Lock()
	prevBlocked := m.wasBlocked[agentType]
	nowBlocked := status.IsBlocked()
	m.wasBlocked[agentType] = nowBlocked
	cb := m.onTransition
	m.mu.Unlock()

	m.cache.Set(agentType, status)

	if prevBlocked && !nowBlocked && cb != nil {
		cb(agentType, true, false)
	}
	if nowBlocked && !prevBlocked {
		m.logger.Printf("QuotaMonitor: %s blocked — %s", agentType, status.Summary)
	} else if prevBlocked && !nowBlocked {
		m.logger.Printf("QuotaMonitor: %s available again — %s", agentType, status.Summary)
	}
}

func (m *Monitor) kickRefresh(agentType string) {
	m.mu.Lock()
	if m.refreshing[agentType] {
		m.mu.Unlock()
		return
	}
	m.refreshing[agentType] = true
	m.mu.Unlock()

	go func() {
		defer func() {
			m.mu.Lock()
			delete(m.refreshing, agentType)
			m.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		m.RefreshType(ctx, agentType)
	}()
}

// BuildCheckers returns quota checkers for the configured worker types.
func BuildCheckers(workerTypes []string) []Checker {
	var checkers []Checker
	seen := make(map[string]bool)
	for _, wt := range workerTypes {
		if seen[wt] {
			continue
		}
		seen[wt] = true
		switch wt {
		case "claude-code":
			checkers = append(checkers, NewClaudeChecker("", nil))
		case "codex":
			checkers = append(checkers, NewCodexChecker("", nil))
		case "gemini":
			checkers = append(checkers, NewGeminiChecker("", nil))
		}
	}
	return checkers
}
