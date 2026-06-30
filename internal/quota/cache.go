package quota

import (
	"sync"
	"time"
)

// Cache stores per-agent-type quota results with a TTL.
type Cache struct {
	mu      sync.RWMutex
	ttl     time.Duration
	entries map[string]cacheEntry
}

type cacheEntry struct {
	status    Status
	checkedAt time.Time
}

// NewCache creates a TTL cache. ttl <= 0 defaults to 2 minutes.
func NewCache(ttl time.Duration) *Cache {
	if ttl <= 0 {
		ttl = 2 * time.Minute
	}
	return &Cache{
		ttl:     ttl,
		entries: make(map[string]cacheEntry),
	}
}

// Get returns the cached status and whether it is still fresh.
func (c *Cache) Get(agentType string) (Status, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[agentType]
	if !ok {
		return Status{}, false
	}
	if c.ttl > 0 && time.Since(e.checkedAt) > c.ttl {
		return e.status, false
	}
	return e.status, true
}

// GetStale returns the cached status even when TTL has expired.
func (c *Cache) GetStale(agentType string) (Status, time.Time, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[agentType]
	if !ok {
		return Status{}, time.Time{}, false
	}
	return e.status, e.checkedAt, true
}

// Set stores a fresh quota result.
func (c *Cache) Set(agentType string, status Status) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[agentType] = cacheEntry{status: status, checkedAt: time.Now()}
}

// CheckedAt returns when the agent type was last checked, or zero.
func (c *Cache) CheckedAt(agentType string) time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if e, ok := c.entries[agentType]; ok {
		return e.checkedAt
	}
	return time.Time{}
}
