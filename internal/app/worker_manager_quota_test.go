package app

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jaakkos/stringwork/internal/domain"
	"github.com/jaakkos/stringwork/internal/quota"
)

func quotaTestWM(t *testing.T, mon *quota.Monitor) *WorkerManager {
	t.Helper()
	state := &domain.CollabState{DriverID: "cursor"}
	EnsureStateMaps(state)
	state.NextMsgID = 1
	var mu sync.Mutex
	mutator := func(fn func(*domain.CollabState) error) error {
		mu.Lock()
		defer mu.Unlock()
		return fn(state)
	}
	wm := drainTestWM(t, state, WorkerSpawnConfig{
		InstanceID: "claude-code",
		AgentType:  "claude-code",
		MaxRetries: 0,
	})
	wm.quotaMonitor = mon
	wm.mcpReady = true
	wm.stateMutator = mutator
	return wm
}

func TestSpawnForTask_QuotaBlockedQueues(t *testing.T) {
	mon := quota.NewMonitor([]quota.Checker{noopQuotaChecker{"claude-code"}},
		quota.MonitorConfig{CacheTTL: time.Minute, FailOpen: true}, testLogger(t))
	mon.SetCachedStatus("claude-code", quota.Blocked("spend", "org spend 100%", time.Now().Add(time.Hour)))

	wm := quotaTestWM(t, mon)
	before := wm.PendingSpawnCount("claude-code")
	wm.SpawnForTask(42, "claude-code")
	after := wm.PendingSpawnCount("claude-code")
	if after != before+1 {
		t.Fatalf("expected queued spawn, pending %d -> %d", before, after)
	}
}

func TestSpawnForTask_QuotaNoCredentialsStillSpawns(t *testing.T) {
	mon := quota.NewMonitor([]quota.Checker{noopQuotaChecker{"claude-code"}},
		quota.MonitorConfig{CacheTTL: time.Minute, FailOpen: true}, testLogger(t))
	mon.SetCachedStatus("claude-code", quota.NoCredentials())

	wm := quotaTestWM(t, mon)
	wm.SpawnForTask(7, "claude-code")
	if wm.PendingSpawnCount("claude-code") > 0 {
		t.Fatal("NoCredentials should not queue — fail open")
	}
}

func TestSpawnForTask_QuotaCheckFailedStillSpawns(t *testing.T) {
	mon := quota.NewMonitor([]quota.Checker{noopQuotaChecker{"claude-code"}},
		quota.MonitorConfig{CacheTTL: time.Minute, FailOpen: true}, testLogger(t))
	mon.SetCachedStatus("claude-code", quota.CheckFailed(errors.New("network")))

	wm := quotaTestWM(t, mon)
	wm.SpawnForTask(8, "claude-code")
	if wm.PendingSpawnCount("claude-code") > 0 {
		t.Fatal("CheckFailed should not queue — fail open")
	}
}

func TestBackedOffAgentTypes_MergesQuotaBlock(t *testing.T) {
	mon := quota.NewMonitor([]quota.Checker{noopQuotaChecker{"claude-code"}},
		quota.MonitorConfig{CacheTTL: time.Minute, FailOpen: true}, testLogger(t))
	mon.SetCachedStatus("claude-code", quota.Blocked("spend", "org spend 100%", time.Now().Add(time.Hour)))

	wm := quotaTestWM(t, mon)
	backed := wm.BackedOffAgentTypes()
	if len(backed) != 1 || backed[0] != "claude-code" {
		t.Fatalf("expected claude-code backed off, got %v", backed)
	}
	blocked, _, reason := wm.BackoffInfoForType("claude-code")
	if !blocked || reason != "quota-preflight" {
		t.Fatalf("blocked=%v reason=%q", blocked, reason)
	}
}

func TestQuotaTransition_DrainsQueue(t *testing.T) {
	var drained atomic.Int32
	ch := &toggleCheckerQuota{}
	mon := quota.NewMonitor([]quota.Checker{ch}, quota.MonitorConfig{CacheTTL: time.Minute, FailOpen: true}, testLogger(t))
	wm := quotaTestWM(t, mon)
	wm.enqueueSpawn("claude-code", 99)
	mon.SetOnTransition(func(agentType string, wasBlocked, nowBlocked bool) {
		if wasBlocked && !nowBlocked {
			wm.DrainQueueForType(agentType)
			drained.Add(1)
		}
	})
	mon.RefreshType(context.Background(), "claude-code")
	mon.RefreshType(context.Background(), "claude-code")
	if drained.Load() != 1 {
		t.Fatalf("expected drain on blocked→available, got %d", drained.Load())
	}
}

type noopQuotaChecker struct{ agent string }

func (n noopQuotaChecker) AgentType() string { return n.agent }
func (n noopQuotaChecker) Check(ctx context.Context) quota.Status {
	return quota.Available("ok")
}

type toggleCheckerQuota struct{ n int }

func (t *toggleCheckerQuota) AgentType() string { return "claude-code" }
func (t *toggleCheckerQuota) Check(ctx context.Context) quota.Status {
	t.n++
	if t.n == 1 {
		return quota.Blocked("x", "blocked", time.Time{})
	}
	return quota.Available("ok")
}
