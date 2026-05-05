package app

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jaakkos/stringwork/internal/domain"
)

// drainTestWM builds a minimal WorkerManager wired with just enough state
// for DrainAllQueues / drainQueue / spawn to run end-to-end against an
// in-memory CollabState. Mirrors NewWorkerManager's map initialisation so
// any maps spawn() touches are non-nil.
func drainTestWM(t *testing.T, state *domain.CollabState, configs ...WorkerSpawnConfig) *WorkerManager {
	t.Helper()
	if state == nil {
		state = &domain.CollabState{}
	}
	EnsureStateMaps(state)
	if state.NextMsgID == 0 {
		state.NextMsgID = 1
	}
	var mu sync.Mutex
	mutator := func(fn func(*domain.CollabState) error) error {
		mu.Lock()
		defer mu.Unlock()
		return fn(state)
	}
	return &WorkerManager{
		configs:             configs,
		stateMutator:        mutator,
		stateLoader:         func() (*domain.CollabState, error) { return state, nil },
		logger:              testLogger(t),
		lastSpawn:           make(map[string]time.Time),
		runningWorkers:      make(map[string]context.CancelFunc),
		mcpRegistered:       make(map[string]bool),
		processRuntime:      make(map[string]*workerRuntime),
		consecutiveFailures: make(map[string]int),
		lastFailure:         make(map[string]time.Time),
		backoffUntil:        make(map[string]time.Time),
		pendingSpawns:       make(map[string][]pendingSpawn),
		lastSessionID:       make(map[string]string),
		spawnSkipLogged:     make(map[string]time.Time),
		failureAcks:         make(map[string]*failureAckState),
	}
}

// TestWorkerManager_DrainAllQueues_RetursTypeCount — DrainAllQueues iterates
// every per-type pending spawn queue and returns the number of types it
// touched. Verifies the new SpawnDriver.DrainAllQueues contract that the
// watchdog relies on for one-tick drain coverage.
func TestWorkerManager_DrainAllQueues_RetursTypeCount(t *testing.T) {
	wm := drainTestWM(t, nil)

	// Two distinct types with queued tasks. No matching configs — drainQueue
	// pops then exits cleanly via the findConfigForAgent==nil branch, so we
	// avoid spawning real subprocesses while still exercising the per-type
	// iteration the watchdog cares about.
	wm.enqueueSpawn("claude-code", 101)
	wm.enqueueSpawn("claude-code", 102)
	wm.enqueueSpawn("codex", 201)

	got := wm.DrainAllQueues()
	if got != 2 {
		t.Errorf("DrainAllQueues returned %d, want 2 (one per non-empty type)", got)
	}
	// Each drainQueue call pops exactly one entry; the rest stay queued
	// for the next drain. Verifies we don't try to drain the entire queue
	// in one tick (which would burst-spawn N parallel processes).
	if got := wm.PendingSpawnCount("claude-code"); got != 1 {
		t.Errorf("PendingSpawnCount(claude-code) = %d, want 1 (only one popped per drain)", got)
	}
	if got := wm.PendingSpawnCount("codex"); got != 0 {
		t.Errorf("PendingSpawnCount(codex) = %d, want 0", got)
	}
}

// TestWorkerManager_DrainAllQueues_EmptyQueuesReturnsZero — when nothing is
// queued, DrainAllQueues is a cheap no-op. The watchdog calls it every
// cycle, so this path matters.
func TestWorkerManager_DrainAllQueues_EmptyQueuesReturnsZero(t *testing.T) {
	wm := drainTestWM(t, nil)
	if got := wm.DrainAllQueues(); got != 0 {
		t.Errorf("DrainAllQueues with no pending spawns returned %d, want 0", got)
	}
}

// TestSpawn_RemovesSyntheticSessionOnExit — spawn() must invoke the
// configured sessionRemover on every exit path. Without this, the
// synthetic "cli-<instance>" session created by touchCLISession lingers
// for ~5 minutes after the worker exits and falsely closes the
// active-session gate in Check() against a respawn.
func TestSpawn_RemovesSyntheticSessionOnExit(t *testing.T) {
	state := &domain.CollabState{DriverID: "cursor"}
	cfg := WorkerSpawnConfig{
		InstanceID: "claude-code-1",
		AgentType:  "claude-code",
		// Empty Command → runOnce returns immediately with "empty command",
		// so spawn() falls through with no real subprocess work.
		Command:    nil,
		MaxRetries: 0,
	}
	wm := drainTestWM(t, state, cfg)

	var removed atomic.Int32
	var removedID atomic.Value // string
	wm.SetSessionRemover(func(instanceID string) {
		removed.Add(1)
		removedID.Store(instanceID)
	})

	wm.spawn(cfg, t.TempDir())

	if got := removed.Load(); got != 1 {
		t.Fatalf("sessionRemover called %d times, want 1", got)
	}
	if got, _ := removedID.Load().(string); got != "claude-code-1" {
		t.Errorf("sessionRemover called with %q, want %q", got, "claude-code-1")
	}
}

// TestSpawn_NoSessionRemoverIsSafe — a WorkerManager wired without a
// sessionRemover (e.g. unit tests, MCP-only deployments) must not panic
// when spawn() exits.
func TestSpawn_NoSessionRemoverIsSafe(t *testing.T) {
	state := &domain.CollabState{DriverID: "cursor"}
	cfg := WorkerSpawnConfig{
		InstanceID: "claude-code-1",
		AgentType:  "claude-code",
		MaxRetries: 0,
	}
	wm := drainTestWM(t, state, cfg)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("spawn() with nil sessionRemover panicked: %v", r)
		}
	}()
	wm.spawn(cfg, t.TempDir())
}
