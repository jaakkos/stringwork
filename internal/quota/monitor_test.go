package quota

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestMonitor_BlockedUsesCacheOnly(t *testing.T) {
	ch := &staticChecker{agent: "claude-code", status: Blocked("x", "spend 100%", time.Time{})}
	m := NewMonitor([]Checker{ch}, MonitorConfig{CacheTTL: time.Minute, FailOpen: true}, nil)
	m.SetCachedStatus("claude-code", Blocked("x", "spend 100%", time.Time{}))

	blocked, _, reason := m.Blocked("claude-code")
	if !blocked || reason != "quota-preflight" {
		t.Fatalf("blocked=%v reason=%q", blocked, reason)
	}
}

func TestMonitor_TransitionDrainsCallback(t *testing.T) {
	var calls atomic.Int32
	ch := &toggleChecker{agent: "codex", first: Blocked("x", "cap", time.Time{}), second: Available("ok")}
	m := NewMonitor([]Checker{ch}, MonitorConfig{CacheTTL: time.Minute, FailOpen: true}, nil)
	m.SetOnTransition(func(agentType string, wasBlocked, nowBlocked bool) {
		if wasBlocked && !nowBlocked {
			calls.Add(1)
		}
	})

	m.RefreshType(context.Background(), "codex")
	if calls.Load() != 0 {
		t.Fatal("first refresh should not fire transition")
	}
	m.RefreshType(context.Background(), "codex")
	if calls.Load() != 1 {
		t.Fatalf("expected drain callback once, got %d", calls.Load())
	}
}

type toggleChecker struct {
	agent  string
	first  Status
	second Status
	n      int
}

func (t *toggleChecker) AgentType() string { return t.agent }
func (t *toggleChecker) Check(ctx context.Context) Status {
	t.n++
	if t.n == 1 {
		return t.first
	}
	return t.second
}
