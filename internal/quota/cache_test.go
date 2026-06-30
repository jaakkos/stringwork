package quota

import (
	"context"
	"testing"
	"time"
)

func TestCache_TTLHitMiss(t *testing.T) {
	c := NewCache(50 * time.Millisecond)
	c.Set("claude-code", Available("ok"))

	if _, fresh := c.Get("claude-code"); !fresh {
		t.Fatal("expected fresh entry")
	}
	time.Sleep(60 * time.Millisecond)
	if _, fresh := c.Get("claude-code"); fresh {
		t.Fatal("expected stale after TTL")
	}
}

func TestCache_StaleReturnsFailOpenSignal(t *testing.T) {
	c := NewCache(10 * time.Millisecond)
	c.Set("codex", Blocked("x", "blocked", time.Time{}))
	time.Sleep(20 * time.Millisecond)

	m := NewMonitor([]Checker{&staticChecker{agent: "codex", status: Available("ok")}},
		MonitorConfig{CacheTTL: 10 * time.Millisecond, FailOpen: true}, nil)

	m.SetCachedStatus("codex", Blocked("x", "blocked", time.Time{}))
	time.Sleep(20 * time.Millisecond)

	blocked, _, _ := m.Blocked("codex")
	if blocked {
		t.Fatal("stale cache should fail open on spawn path")
	}
}

type staticChecker struct {
	agent  string
	status Status
}

func (s *staticChecker) AgentType() string { return s.agent }
func (s *staticChecker) Check(ctx context.Context) Status {
	return s.status
}
