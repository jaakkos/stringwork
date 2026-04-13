package app

import (
	"context"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jaakkos/stringwork/internal/domain"
	"github.com/jaakkos/stringwork/internal/policy"
)

// testPolicy returns a minimal policy for testing.
func testPolicy() Policy {
	return policy.New(&policy.Config{
		WorkspaceRoot:      "/tmp",
		PresenceTTLSeconds: 300,
		Orchestration:      policy.DefaultOrchestration(),
	})
}

// inMemoryRepo implements StateRepository for tests.
type inMemoryRepo struct {
	state *domain.CollabState
}

func (r *inMemoryRepo) Load() (*domain.CollabState, error) {
	if r.state == nil {
		return domain.NewCollabState(), nil
	}
	return r.state, nil
}

func (r *inMemoryRepo) Save(state *domain.CollabState) error {
	r.state = state
	return nil
}

// testService returns a CollabService backed by an in-memory repo.
func testService(state *domain.CollabState) *CollabService {
	repo := &inMemoryRepo{state: state}
	logger := log.New(os.Stderr, "[test] ", 0)
	return NewCollabService(repo, testPolicy(), logger)
}

func TestWatchdog_RecoverStuckTasks(t *testing.T) {
	state := domain.NewCollabState()
	staleTime := time.Now().Add(-15 * time.Minute)

	// Create agent instances
	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID:    "cursor",
		AgentType:     "cursor",
		Role:          domain.RoleDriver,
		Status:        "idle",
		LastHeartbeat: time.Now(),
	}
	state.AgentInstances["claude-code"] = &domain.AgentInstance{
		InstanceID:    "claude-code",
		AgentType:     "claude-code",
		Role:          domain.RoleWorker,
		Status:        "busy",
		CurrentTasks:  []int{1},
		LastHeartbeat: staleTime, // stale heartbeat
	}
	state.DriverID = "cursor"

	// Create a stuck task
	state.Tasks = append(state.Tasks, domain.Task{
		ID:         1,
		Title:      "Stuck task",
		Status:     "in_progress",
		AssignedTo: "claude-code",
		UpdatedAt:  staleTime,
	})
	state.NextTaskID = 2
	state.NextMsgID = 1

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	wd := NewWatchdog(svc, registry, logger,
		WithHeartbeatThreshold(1*time.Minute),
		WithTaskStuckThreshold(5*time.Minute),
	)

	wd.CheckOnce()

	// Verify task was reset to pending
	_ = svc.Query(func(s *domain.CollabState) error {
		if len(s.Tasks) == 0 {
			t.Fatal("expected task to exist")
		}
		if s.Tasks[0].Status != "pending" {
			t.Errorf("task status = %q, want pending", s.Tasks[0].Status)
		}
		if s.Tasks[0].ResultSummary == "" {
			t.Error("expected ResultSummary to be set")
		}
		return nil
	})

	// Verify agent was marked offline
	_ = svc.Query(func(s *domain.CollabState) error {
		inst := s.AgentInstances["claude-code"]
		if inst == nil {
			t.Fatal("expected claude-code instance")
		}
		if inst.Status != "offline" {
			t.Errorf("agent status = %q, want offline", inst.Status)
		}
		if len(inst.CurrentTasks) != 0 {
			t.Errorf("expected empty CurrentTasks, got %v", inst.CurrentTasks)
		}
		return nil
	})

	// Verify system notification was sent
	_ = svc.Query(func(s *domain.CollabState) error {
		found := false
		for _, msg := range s.Messages {
			if msg.From == "system" && msg.To == "cursor" {
				found = true
			}
		}
		if !found {
			t.Error("expected system notification message to cursor")
		}
		return nil
	})
}

func TestWatchdog_DoesNotRecoverDriverTasks(t *testing.T) {
	state := domain.NewCollabState()
	staleTime := time.Now().Add(-15 * time.Minute)

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID:    "cursor",
		AgentType:     "cursor",
		Role:          domain.RoleDriver,
		Status:        "busy",
		CurrentTasks:  []int{1},
		LastHeartbeat: staleTime,
	}
	state.DriverID = "cursor"

	state.Tasks = append(state.Tasks, domain.Task{
		ID:         1,
		Title:      "Driver task",
		Status:     "in_progress",
		AssignedTo: "cursor",
		UpdatedAt:  staleTime,
	})
	state.NextTaskID = 2
	state.NextMsgID = 1

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	wd := NewWatchdog(svc, registry, logger,
		WithHeartbeatThreshold(1*time.Minute),
		WithTaskStuckThreshold(30*time.Minute), // high threshold so only heartbeat triggers
	)

	wd.CheckOnce()

	// Driver tasks should NOT be recovered by heartbeat check
	// (driver doesn't heartbeat via tool)
	_ = svc.Query(func(s *domain.CollabState) error {
		if s.Tasks[0].Status != "in_progress" {
			t.Errorf("driver task should remain in_progress, got %q", s.Tasks[0].Status)
		}
		return nil
	})
}

func TestWatchdog_StuckTaskNotRecoveredWhenAgentAlive(t *testing.T) {
	state := domain.NewCollabState()
	now := time.Now()

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID:    "cursor",
		AgentType:     "cursor",
		Role:          domain.RoleDriver,
		Status:        "idle",
		LastHeartbeat: now,
	}
	state.AgentInstances["claude-code"] = &domain.AgentInstance{
		InstanceID:    "claude-code",
		AgentType:     "claude-code",
		Role:          domain.RoleWorker,
		Status:        "busy",
		CurrentTasks:  []int{1},
		LastHeartbeat: now, // heartbeat is fresh — agent is alive
	}
	state.DriverID = "cursor"

	// Task is old but agent is alive — should NOT be recovered
	state.Tasks = append(state.Tasks, domain.Task{
		ID:         1,
		Title:      "Long running task",
		Status:     "in_progress",
		AssignedTo: "claude-code",
		UpdatedAt:  now.Add(-20 * time.Minute),
	})
	state.NextTaskID = 2
	state.NextMsgID = 1

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	wd := NewWatchdog(svc, registry, logger,
		WithHeartbeatThreshold(30*time.Minute),
		WithTaskStuckThreshold(10*time.Minute),
	)

	wd.CheckOnce()

	_ = svc.Query(func(s *domain.CollabState) error {
		if s.Tasks[0].Status != "in_progress" {
			t.Errorf("task should remain in_progress when agent is alive, got %q", s.Tasks[0].Status)
		}
		return nil
	})
}

func TestWatchdog_StuckTaskRecoveredWhenAgentDead(t *testing.T) {
	state := domain.NewCollabState()
	staleTime := time.Now().Add(-15 * time.Minute)

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID:    "cursor",
		AgentType:     "cursor",
		Role:          domain.RoleDriver,
		Status:        "idle",
		LastHeartbeat: time.Now(),
	}
	state.AgentInstances["claude-code"] = &domain.AgentInstance{
		InstanceID:    "claude-code",
		AgentType:     "claude-code",
		Role:          domain.RoleWorker,
		Status:        "busy",
		CurrentTasks:  []int{1},
		LastHeartbeat: staleTime, // agent is dead
	}
	state.DriverID = "cursor"

	// Task is stuck AND agent is dead — should be recovered
	state.Tasks = append(state.Tasks, domain.Task{
		ID:         1,
		Title:      "Stuck task with dead agent",
		Status:     "in_progress",
		AssignedTo: "claude-code",
		UpdatedAt:  staleTime,
	})
	state.NextTaskID = 2
	state.NextMsgID = 1

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	wd := NewWatchdog(svc, registry, logger,
		WithHeartbeatThreshold(5*time.Minute),
		WithTaskStuckThreshold(10*time.Minute),
	)

	wd.CheckOnce()

	_ = svc.Query(func(s *domain.CollabState) error {
		if s.Tasks[0].Status != "pending" {
			t.Errorf("stuck task should be reset to pending, got %q", s.Tasks[0].Status)
		}
		return nil
	})
}

func TestWatchdog_NoRecoveryWhenHealthy(t *testing.T) {
	state := domain.NewCollabState()
	now := time.Now()

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID:    "cursor",
		AgentType:     "cursor",
		Role:          domain.RoleDriver,
		Status:        "idle",
		LastHeartbeat: now,
	}
	state.AgentInstances["claude-code"] = &domain.AgentInstance{
		InstanceID:    "claude-code",
		AgentType:     "claude-code",
		Role:          domain.RoleWorker,
		Status:        "busy",
		CurrentTasks:  []int{1},
		LastHeartbeat: now,
	}
	state.DriverID = "cursor"

	state.Tasks = append(state.Tasks, domain.Task{
		ID:         1,
		Title:      "Active task",
		Status:     "in_progress",
		AssignedTo: "claude-code",
		UpdatedAt:  now,
	})
	state.NextTaskID = 2
	state.NextMsgID = 1

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	wd := NewWatchdog(svc, registry, logger,
		WithHeartbeatThreshold(5*time.Minute),
		WithTaskStuckThreshold(10*time.Minute),
	)

	wd.CheckOnce()

	// Everything should remain unchanged
	_ = svc.Query(func(s *domain.CollabState) error {
		if s.Tasks[0].Status != "in_progress" {
			t.Errorf("healthy task should remain in_progress, got %q", s.Tasks[0].Status)
		}
		inst := s.AgentInstances["claude-code"]
		if inst.Status != "busy" {
			t.Errorf("healthy agent should remain busy, got %q", inst.Status)
		}
		if len(s.Messages) != 0 {
			t.Errorf("no system messages expected, got %d", len(s.Messages))
		}
		return nil
	})
}

func TestWatchdog_PrunesStaleSessions(t *testing.T) {
	state := domain.NewCollabState()
	staleTime := time.Now().Add(-10 * time.Minute)

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID:    "cursor",
		AgentType:     "cursor",
		Role:          domain.RoleDriver,
		Status:        "idle",
		LastHeartbeat: time.Now(),
	}
	state.AgentInstances["claude-code"] = &domain.AgentInstance{
		InstanceID:    "claude-code",
		AgentType:     "claude-code",
		Role:          domain.RoleWorker,
		Status:        "offline",
		LastHeartbeat: staleTime,
	}
	state.DriverID = "cursor"
	state.NextMsgID = 1

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	// Simulate a stale session: registered long ago with no recent tool calls.
	// We set the session, then manually backdate the lastActivity to simulate staleness.
	registry.SetAgent("session-1", "claude-code")
	registry.BackdateActivity("session-1", staleTime)

	if !registry.HasActiveSession("claude-code") {
		t.Fatal("expected claude-code to have active session before watchdog")
	}

	wd := NewWatchdog(svc, registry, logger,
		WithSessionStaleThreshold(1*time.Minute),
	)

	wd.CheckOnce()

	// Session should be pruned
	if registry.HasActiveSession("claude-code") {
		t.Error("expected claude-code session to be pruned")
	}
}

func TestWatchdog_DoesNotPruneDriverSessions(t *testing.T) {
	state := domain.NewCollabState()
	staleTime := time.Now().Add(-10 * time.Minute)

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID:    "cursor",
		AgentType:     "cursor",
		Role:          domain.RoleDriver,
		Status:        "idle",
		LastHeartbeat: staleTime, // stale but is driver
	}
	state.DriverID = "cursor"
	state.NextMsgID = 1

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	registry.SetAgent("session-driver", "cursor")

	wd := NewWatchdog(svc, registry, logger,
		WithSessionStaleThreshold(1*time.Minute),
	)

	wd.CheckOnce()

	// Driver session should NOT be pruned
	if !registry.HasActiveSession("cursor") {
		t.Error("driver session should not be pruned")
	}
}

func TestWatchdog_NotifierTriggeredAfterRecovery(t *testing.T) {
	state := domain.NewCollabState()
	staleTime := time.Now().Add(-15 * time.Minute)

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID:    "cursor",
		AgentType:     "cursor",
		Role:          domain.RoleDriver,
		Status:        "idle",
		LastHeartbeat: time.Now(),
	}
	state.AgentInstances["claude-code"] = &domain.AgentInstance{
		InstanceID:    "claude-code",
		AgentType:     "claude-code",
		Role:          domain.RoleWorker,
		Status:        "busy",
		CurrentTasks:  []int{1},
		LastHeartbeat: staleTime,
	}
	state.DriverID = "cursor"

	state.Tasks = append(state.Tasks, domain.Task{
		ID:         1,
		Title:      "Stuck task",
		Status:     "in_progress",
		AssignedTo: "claude-code",
		UpdatedAt:  staleTime,
	})
	state.NextTaskID = 2
	state.NextMsgID = 1

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	triggered := false
	mockNotifier := &mockTriggerable{fn: func() { triggered = true }}

	wd := NewWatchdog(svc, registry, logger,
		WithHeartbeatThreshold(1*time.Minute),
		WithWatchdogNotifier(mockNotifier),
	)

	wd.CheckOnce()

	if !triggered {
		t.Error("expected notifier to be triggered after recovery")
	}
}

func TestWatchdog_StartStop_Graceful(t *testing.T) {
	state := domain.NewCollabState()
	state.NextMsgID = 1
	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	wd := NewWatchdog(svc, registry, logger,
		WithWatchdogInterval(10*time.Millisecond),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		wd.Start(ctx)
		close(done)
	}()

	time.Sleep(25 * time.Millisecond)
	cancel()
	wd.Stop()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Start did not return after Stop")
	}
}

func TestWatchdog_MultipleStuckTasks(t *testing.T) {
	state := domain.NewCollabState()
	staleTime := time.Now().Add(-15 * time.Minute)

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID:    "cursor",
		AgentType:     "cursor",
		Role:          domain.RoleDriver,
		Status:        "idle",
		LastHeartbeat: time.Now(),
	}
	state.AgentInstances["claude-code"] = &domain.AgentInstance{
		InstanceID:    "claude-code",
		AgentType:     "claude-code",
		Role:          domain.RoleWorker,
		Status:        "busy",
		CurrentTasks:  []int{1, 2, 3},
		LastHeartbeat: staleTime,
	}
	state.DriverID = "cursor"

	state.Tasks = append(state.Tasks,
		domain.Task{ID: 1, Title: "Task 1", Status: "in_progress", AssignedTo: "claude-code", UpdatedAt: staleTime},
		domain.Task{ID: 2, Title: "Task 2", Status: "in_progress", AssignedTo: "claude-code", UpdatedAt: staleTime},
		domain.Task{ID: 3, Title: "Task 3", Status: "in_progress", AssignedTo: "claude-code", UpdatedAt: staleTime},
		domain.Task{ID: 4, Title: "Task 4", Status: "completed", AssignedTo: "claude-code", UpdatedAt: staleTime}, // should not be touched
	)
	state.NextTaskID = 5
	state.NextMsgID = 1

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	wd := NewWatchdog(svc, registry, logger,
		WithHeartbeatThreshold(1*time.Minute),
	)

	wd.CheckOnce()

	_ = svc.Query(func(s *domain.CollabState) error {
		for _, task := range s.Tasks {
			if task.ID <= 3 && task.Status != "pending" {
				t.Errorf("task #%d should be pending, got %q", task.ID, task.Status)
			}
			if task.ID == 4 && task.Status != "completed" {
				t.Errorf("completed task #4 should not be modified, got %q", task.Status)
			}
		}
		return nil
	})
}

func TestWatchdog_DoesNotPruneActiveSession(t *testing.T) {
	// This is the key bug fix test: an agent that just connected and is actively
	// making tool calls should NOT be pruned, even if its state heartbeat is old.
	state := domain.NewCollabState()
	staleTime := time.Now().Add(-10 * time.Minute)

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID:    "cursor",
		AgentType:     "cursor",
		Role:          domain.RoleDriver,
		Status:        "idle",
		LastHeartbeat: time.Now(),
	}
	state.AgentInstances["claude-code"] = &domain.AgentInstance{
		InstanceID:    "claude-code",
		AgentType:     "claude-code",
		Role:          domain.RoleWorker,
		Status:        "busy",
		CurrentTasks:  []int{1},
		LastHeartbeat: staleTime, // old heartbeat in state (from previous server run)
	}
	state.DriverID = "cursor"

	state.Tasks = append(state.Tasks, domain.Task{
		ID:         1,
		Title:      "Active task",
		Status:     "in_progress",
		AssignedTo: "claude-code",
		UpdatedAt:  time.Now().Add(-30 * time.Second), // recently updated
	})
	state.NextTaskID = 2
	state.NextMsgID = 1

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	// Agent has an active session with recent tool activity
	registry.SetAgent("session-active", "claude-code")
	// Simulate recent tool call activity (as PiggybackMiddleware.TouchSession does)
	registry.TouchSession("session-active")

	wd := NewWatchdog(svc, registry, logger,
		WithHeartbeatThreshold(1*time.Minute),
		WithSessionStaleThreshold(1*time.Minute),
	)

	wd.CheckOnce()

	// Session should NOT be pruned (recent activity)
	if !registry.HasActiveSession("claude-code") {
		t.Error("active session should not be pruned")
	}

	// Task should NOT be recovered (agent is alive via session activity)
	_ = svc.Query(func(s *domain.CollabState) error {
		if s.Tasks[0].Status != "in_progress" {
			t.Errorf("task should remain in_progress, got %q", s.Tasks[0].Status)
		}
		return nil
	})

	// Agent should NOT be marked offline
	_ = svc.Query(func(s *domain.CollabState) error {
		inst := s.AgentInstances["claude-code"]
		if inst.Status == "offline" {
			t.Error("agent with active session should not be marked offline")
		}
		return nil
	})
}

func TestWatchdog_DoesNotPruneNewlyConnectedSession(t *testing.T) {
	// Agent just connected (SetAgent called) but hasn't made any tool calls yet.
	// The session exists but lastActivity was set by SetAgent.
	state := domain.NewCollabState()
	staleTime := time.Now().Add(-10 * time.Minute)

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID:    "cursor",
		AgentType:     "cursor",
		Role:          domain.RoleDriver,
		Status:        "idle",
		LastHeartbeat: time.Now(),
	}
	state.AgentInstances["claude-code"] = &domain.AgentInstance{
		InstanceID:    "claude-code",
		AgentType:     "claude-code",
		Role:          domain.RoleWorker,
		Status:        "offline",
		LastHeartbeat: staleTime,
	}
	state.DriverID = "cursor"
	state.NextMsgID = 1

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	// Agent just connected — SetAgent records current time as lastActivity
	registry.SetAgent("session-new", "claude-code")

	wd := NewWatchdog(svc, registry, logger,
		WithSessionStaleThreshold(1*time.Minute),
		WithHeartbeatThreshold(1*time.Minute),
	)

	wd.CheckOnce()

	// Newly connected session should NOT be pruned
	if !registry.HasActiveSession("claude-code") {
		t.Error("newly connected session should not be pruned")
	}
}

func TestWatchdog_RefreshHeartbeatsOnStartup(t *testing.T) {
	state := domain.NewCollabState()
	staleTime := time.Now().Add(-1 * time.Hour)

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID:    "cursor",
		AgentType:     "cursor",
		Role:          domain.RoleDriver,
		Status:        "idle",
		LastHeartbeat: staleTime,
	}
	state.AgentInstances["claude-code"] = &domain.AgentInstance{
		InstanceID:    "claude-code",
		AgentType:     "claude-code",
		Role:          domain.RoleWorker,
		Status:        "busy",
		CurrentTasks:  []int{1},
		LastHeartbeat: staleTime,
	}

	RefreshHeartbeatsOnStartup(state)

	// Heartbeats should be refreshed to recent
	for id, inst := range state.AgentInstances {
		if time.Since(inst.LastHeartbeat) > 1*time.Second {
			t.Errorf("instance %s heartbeat should be refreshed, got %s ago", id, time.Since(inst.LastHeartbeat))
		}
	}

	// Worker should be set to offline
	if state.AgentInstances["claude-code"].Status != "offline" {
		t.Errorf("worker should be offline after startup refresh, got %q", state.AgentInstances["claude-code"].Status)
	}
	if len(state.AgentInstances["claude-code"].CurrentTasks) != 0 {
		t.Error("worker tasks should be cleared after startup refresh")
	}

	// Driver should keep its status
	if state.AgentInstances["cursor"].Status != "idle" {
		t.Errorf("driver status should be preserved, got %q", state.AgentInstances["cursor"].Status)
	}
}

// ========== DLQ auto-block tests ==========

func TestWatchdog_DLQ_FailureCountIncrements(t *testing.T) {
	state := domain.NewCollabState()
	staleTime := time.Now().Add(-15 * time.Minute)

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver,
		Status: "idle", LastHeartbeat: time.Now(),
	}
	state.AgentInstances["claude-code"] = &domain.AgentInstance{
		InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker,
		Status: "busy", CurrentTasks: []int{1}, LastHeartbeat: staleTime,
	}
	state.DriverID = "cursor"
	state.Tasks = append(state.Tasks, domain.Task{
		ID: 1, Title: "Flaky task", Status: "in_progress",
		AssignedTo: "claude-code", UpdatedAt: staleTime,
		FailureCount: 0,
	})
	state.NextTaskID = 2
	state.NextMsgID = 1

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	wd := NewWatchdog(svc, registry, logger,
		WithHeartbeatThreshold(1*time.Minute),
		WithMaxTaskFailures(3),
	)
	wd.CheckOnce()

	_ = svc.Query(func(s *domain.CollabState) error {
		task := s.Tasks[0]
		if task.FailureCount != 1 {
			t.Errorf("FailureCount = %d, want 1", task.FailureCount)
		}
		if task.Status != "pending" {
			t.Errorf("Status = %q, want pending (below max failures)", task.Status)
		}
		if task.FailureReason == "" {
			t.Error("FailureReason should be set")
		}
		if task.LastFailure.IsZero() {
			t.Error("LastFailure should be set")
		}
		return nil
	})
}

func TestWatchdog_DLQ_AutoBlockAfterMaxFailures(t *testing.T) {
	state := domain.NewCollabState()
	staleTime := time.Now().Add(-15 * time.Minute)

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver,
		Status: "idle", LastHeartbeat: time.Now(),
	}
	state.AgentInstances["claude-code"] = &domain.AgentInstance{
		InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker,
		Status: "busy", CurrentTasks: []int{1}, LastHeartbeat: staleTime,
	}
	state.DriverID = "cursor"

	// Task already at threshold - 1
	state.Tasks = append(state.Tasks, domain.Task{
		ID: 1, Title: "Repeatedly failing", Status: "in_progress",
		AssignedTo: "claude-code", UpdatedAt: staleTime,
		FailureCount:  2, // one more will hit max of 3
		FailureReason: "previous failure",
	})
	state.NextTaskID = 2
	state.NextMsgID = 1

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	wd := NewWatchdog(svc, registry, logger,
		WithHeartbeatThreshold(1*time.Minute),
		WithMaxTaskFailures(3),
	)
	wd.CheckOnce()

	_ = svc.Query(func(s *domain.CollabState) error {
		task := s.Tasks[0]
		if task.FailureCount != 3 {
			t.Errorf("FailureCount = %d, want 3", task.FailureCount)
		}
		if task.Status != "blocked" {
			t.Errorf("Status = %q, want blocked (at max failures)", task.Status)
		}
		if task.BlockedBy == "" {
			t.Error("BlockedBy should explain the auto-block")
		}
		if !strings.Contains(task.BlockedBy, "3 failures") {
			t.Errorf("BlockedBy should mention failure count: %q", task.BlockedBy)
		}
		return nil
	})
}

func TestWatchdog_DLQ_CustomMaxFailures(t *testing.T) {
	state := domain.NewCollabState()
	staleTime := time.Now().Add(-15 * time.Minute)

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver,
		Status: "idle", LastHeartbeat: time.Now(),
	}
	state.AgentInstances["claude-code"] = &domain.AgentInstance{
		InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker,
		Status: "busy", CurrentTasks: []int{1}, LastHeartbeat: staleTime,
	}
	state.DriverID = "cursor"

	state.Tasks = append(state.Tasks, domain.Task{
		ID: 1, Title: "Custom threshold", Status: "in_progress",
		AssignedTo: "claude-code", UpdatedAt: staleTime,
		FailureCount: 0,
	})
	state.NextTaskID = 2
	state.NextMsgID = 1

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	wd := NewWatchdog(svc, registry, logger,
		WithHeartbeatThreshold(1*time.Minute),
		WithMaxTaskFailures(1), // block on first failure
	)
	wd.CheckOnce()

	_ = svc.Query(func(s *domain.CollabState) error {
		task := s.Tasks[0]
		if task.Status != "blocked" {
			t.Errorf("Status = %q, want blocked (maxTaskFailures=1)", task.Status)
		}
		if task.FailureCount != 1 {
			t.Errorf("FailureCount = %d, want 1", task.FailureCount)
		}
		return nil
	})
}

func TestWatchdog_DLQ_BelowMaxNotBlocked(t *testing.T) {
	state := domain.NewCollabState()
	staleTime := time.Now().Add(-15 * time.Minute)

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver,
		Status: "idle", LastHeartbeat: time.Now(),
	}
	state.AgentInstances["claude-code"] = &domain.AgentInstance{
		InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker,
		Status: "busy", CurrentTasks: []int{1}, LastHeartbeat: staleTime,
	}
	state.DriverID = "cursor"

	state.Tasks = append(state.Tasks, domain.Task{
		ID: 1, Title: "First failure", Status: "in_progress",
		AssignedTo: "claude-code", UpdatedAt: staleTime,
		FailureCount: 0,
	})
	state.NextTaskID = 2
	state.NextMsgID = 1

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	wd := NewWatchdog(svc, registry, logger,
		WithHeartbeatThreshold(1*time.Minute),
		WithMaxTaskFailures(5),
	)
	wd.CheckOnce()

	_ = svc.Query(func(s *domain.CollabState) error {
		task := s.Tasks[0]
		if task.Status != "pending" {
			t.Errorf("Status = %q, want pending (failure 1 of 5)", task.Status)
		}
		return nil
	})
}

func TestWatchdog_AlertsGoToClaudeCodeDriver(t *testing.T) {
	// When DriverID is "claude-code", progress alerts and recovery notifications
	// should be sent to "claude-code" instead of "cursor".
	state := domain.NewCollabState()
	staleTime := time.Now().Add(-15 * time.Minute)

	state.AgentInstances["claude-code"] = &domain.AgentInstance{
		InstanceID:    "claude-code",
		AgentType:     "claude-code",
		Role:          domain.RoleDriver,
		Status:        "idle",
		LastHeartbeat: time.Now(),
	}
	state.AgentInstances["codex"] = &domain.AgentInstance{
		InstanceID:    "codex",
		AgentType:     "codex",
		Role:          domain.RoleWorker,
		Status:        "busy",
		CurrentTasks:  []int{1},
		LastHeartbeat: staleTime,
	}
	state.DriverID = "claude-code"

	state.Tasks = append(state.Tasks, domain.Task{
		ID:         1,
		Title:      "Stuck task",
		Status:     "in_progress",
		AssignedTo: "codex",
		UpdatedAt:  staleTime,
	})
	state.NextTaskID = 2
	state.NextMsgID = 1

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	wd := NewWatchdog(svc, registry, logger,
		WithHeartbeatThreshold(1*time.Minute),
	)

	wd.CheckOnce()

	// Verify recovery notification was sent to claude-code (the driver), not cursor
	_ = svc.Query(func(s *domain.CollabState) error {
		foundForClaudeCode := false
		foundForCursor := false
		for _, msg := range s.Messages {
			if msg.From == "system" && msg.To == "claude-code" {
				foundForClaudeCode = true
			}
			if msg.From == "system" && msg.To == "cursor" {
				foundForCursor = true
			}
		}
		if !foundForClaudeCode {
			t.Error("expected system notification to claude-code (the driver)")
		}
		if foundForCursor {
			t.Error("system notification should go to claude-code, not cursor")
		}
		return nil
	})
}

func TestWatchdog_ProgressAlertToClaudeCodeDriver(t *testing.T) {
	// Progress warning/critical alerts should go to the configured driver.
	state := domain.NewCollabState()
	now := time.Now()

	state.AgentInstances["claude-code"] = &domain.AgentInstance{
		InstanceID:    "claude-code",
		AgentType:     "claude-code",
		Role:          domain.RoleDriver,
		Status:        "idle",
		LastHeartbeat: now,
	}
	state.AgentInstances["codex"] = &domain.AgentInstance{
		InstanceID:    "codex",
		AgentType:     "codex",
		Role:          domain.RoleWorker,
		Status:        "busy",
		CurrentTasks:  []int{1},
		LastHeartbeat: now,
	}
	state.DriverID = "claude-code"

	// Task with no recent progress
	state.Tasks = append(state.Tasks, domain.Task{
		ID:             1,
		Title:          "Slow task",
		Status:         "in_progress",
		AssignedTo:     "codex",
		UpdatedAt:      now.Add(-6 * time.Minute),
		LastProgressAt: now.Add(-6 * time.Minute),
	})
	state.NextTaskID = 2
	state.NextMsgID = 1

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	wd := NewWatchdog(svc, registry, logger,
		WithHeartbeatThreshold(30*time.Minute),
		WithProgressWarningThreshold(3*time.Minute),
		WithProgressCriticalThreshold(5*time.Minute),
	)

	wd.CheckOnce()

	_ = svc.Query(func(s *domain.CollabState) error {
		for _, msg := range s.Messages {
			if msg.From == "system" {
				if msg.To != "claude-code" {
					t.Errorf("progress alert sent to %q, want \"claude-code\"", msg.To)
				}
				return nil
			}
		}
		t.Error("expected a progress alert message")
		return nil
	})
}

// ========== Session-aware progress alert tests ==========

func TestWatchdog_ProgressWarning_SuppressedWhenSessionActive(t *testing.T) {
	// Worker has no process but has recent session activity (HTTP-connected).
	// Warning should be suppressed.
	state := domain.NewCollabState()
	now := time.Now()

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID:    "cursor",
		AgentType:     "cursor",
		Role:          domain.RoleDriver,
		Status:        "idle",
		LastHeartbeat: now,
	}
	state.AgentInstances["claude-code"] = &domain.AgentInstance{
		InstanceID:    "claude-code",
		AgentType:     "claude-code",
		Role:          domain.RoleWorker,
		Status:        "busy",
		CurrentTasks:  []int{1},
		LastHeartbeat: now,
	}
	state.DriverID = "cursor"

	// Task with stale progress (4 min), exceeding warning threshold (3 min).
	state.Tasks = append(state.Tasks, domain.Task{
		ID:             1,
		Title:          "HTTP worker task",
		Status:         "in_progress",
		AssignedTo:     "claude-code",
		UpdatedAt:      now.Add(-4 * time.Minute),
		LastProgressAt: now.Add(-4 * time.Minute),
	})
	state.NextTaskID = 2
	state.NextMsgID = 1

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	// Simulate recent session activity (tool calls).
	registry.SetAgent("session-http", "claude-code")
	registry.TouchSession("session-http")

	wd := NewWatchdog(svc, registry, logger,
		WithHeartbeatThreshold(30*time.Minute),
		WithProgressWarningThreshold(3*time.Minute),
		WithProgressCriticalThreshold(5*time.Minute),
	)

	wd.CheckOnce()

	// No warning message should be sent (suppressed by session activity).
	_ = svc.Query(func(s *domain.CollabState) error {
		for _, msg := range s.Messages {
			if msg.From == "system" && strings.Contains(msg.Content, "Warning") {
				t.Errorf("warning should be suppressed when session is active, got: %s", msg.Content)
			}
		}
		return nil
	})
}

func TestWatchdog_ProgressCritical_SoftWhenSessionActive(t *testing.T) {
	// Worker has no process but has recent session activity (HTTP-connected).
	// Critical alert should be softened to a "Note" instead of "No process".
	state := domain.NewCollabState()
	now := time.Now()

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID:    "cursor",
		AgentType:     "cursor",
		Role:          domain.RoleDriver,
		Status:        "idle",
		LastHeartbeat: now,
	}
	state.AgentInstances["claude-code"] = &domain.AgentInstance{
		InstanceID:    "claude-code",
		AgentType:     "claude-code",
		Role:          domain.RoleWorker,
		Status:        "busy",
		CurrentTasks:  []int{1},
		LastHeartbeat: now,
	}
	state.DriverID = "cursor"

	// Task with very stale progress (6 min), exceeding critical threshold (5 min).
	state.Tasks = append(state.Tasks, domain.Task{
		ID:             1,
		Title:          "HTTP worker task",
		Status:         "in_progress",
		AssignedTo:     "claude-code",
		UpdatedAt:      now.Add(-6 * time.Minute),
		LastProgressAt: now.Add(-6 * time.Minute),
	})
	state.NextTaskID = 2
	state.NextMsgID = 1

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	// Simulate recent session activity.
	registry.SetAgent("session-http", "claude-code")
	registry.TouchSession("session-http")

	wd := NewWatchdog(svc, registry, logger,
		WithHeartbeatThreshold(30*time.Minute),
		WithProgressWarningThreshold(3*time.Minute),
		WithProgressCriticalThreshold(5*time.Minute),
	)

	wd.CheckOnce()

	// Should get a VIOLATION message (session active), not no-process / silent-worker templates.
	_ = svc.Query(func(s *domain.CollabState) error {
		found := false
		for _, msg := range s.Messages {
			if msg.From == "system" && msg.To == "cursor" {
				found = true
				if !strings.Contains(msg.Content, "VIOLATION") {
					t.Errorf("expected 'VIOLATION' alert, got: %s", msg.Content)
				}
				if strings.Contains(msg.Content, "AUTO-RECOVERING") {
					t.Errorf("should not use no-process template when session is active, got: %s", msg.Content)
				}
				if strings.Contains(msg.Content, "AUTO-CANCELLING") {
					t.Errorf("should not use silent-worker template when session is active, got: %s", msg.Content)
				}
				if !strings.Contains(msg.Content, "Session is active") {
					t.Errorf("expected session-active wording in message, got: %s", msg.Content)
				}
			}
		}
		if !found {
			t.Error("expected a system message at critical threshold")
		}
		return nil
	})
}

func TestWatchdog_ProgressWarning_NotSuppressedWhenSessionStale(t *testing.T) {
	// Worker has no process AND stale session activity.
	// Warning should NOT be suppressed.
	state := domain.NewCollabState()
	now := time.Now()

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID:    "cursor",
		AgentType:     "cursor",
		Role:          domain.RoleDriver,
		Status:        "idle",
		LastHeartbeat: now,
	}
	state.AgentInstances["claude-code"] = &domain.AgentInstance{
		InstanceID:    "claude-code",
		AgentType:     "claude-code",
		Role:          domain.RoleWorker,
		Status:        "busy",
		CurrentTasks:  []int{1},
		LastHeartbeat: now,
	}
	state.DriverID = "cursor"

	state.Tasks = append(state.Tasks, domain.Task{
		ID:             1,
		Title:          "Dead worker task",
		Status:         "in_progress",
		AssignedTo:     "claude-code",
		UpdatedAt:      now.Add(-4 * time.Minute),
		LastProgressAt: now.Add(-4 * time.Minute),
	})
	state.NextTaskID = 2
	state.NextMsgID = 1

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	// Session exists but activity is old (stale).
	registry.SetAgent("session-stale", "claude-code")
	registry.BackdateActivity("session-stale", now.Add(-10*time.Minute))

	wd := NewWatchdog(svc, registry, logger,
		WithHeartbeatThreshold(30*time.Minute),
		WithProgressWarningThreshold(3*time.Minute),
		WithProgressCriticalThreshold(5*time.Minute),
	)

	wd.CheckOnce()

	// Warning should be sent (no active session to suppress it).
	_ = svc.Query(func(s *domain.CollabState) error {
		found := false
		for _, msg := range s.Messages {
			if msg.From == "system" && strings.Contains(msg.Content, "Warning") {
				found = true
			}
		}
		if !found {
			t.Error("expected warning when session is stale and no process")
		}
		return nil
	})
}

// ========== Task-bound worker tests ==========

func TestWatchdog_TaskBoundWorkerAlive_PreventsRecovery(t *testing.T) {
	// Reproduces the production bug: task assigned to "claude-code" type,
	// stale idle instances (claude-code-1, claude-code-2), but an alive
	// task-bound instance (claude-code-task-1) is actively heartbeating.
	// The watchdog must NOT recover the task.
	state := domain.NewCollabState()
	now := time.Now()
	staleTime := now.Add(-3 * time.Hour)

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID:    "cursor",
		AgentType:     "cursor",
		Role:          domain.RoleDriver,
		Status:        "idle",
		LastHeartbeat: now,
	}
	// Stale idle instances from a previous session.
	state.AgentInstances["claude-code-1"] = &domain.AgentInstance{
		InstanceID:    "claude-code-1",
		AgentType:     "claude-code",
		Role:          domain.RoleWorker,
		Status:        "offline",
		LastHeartbeat: staleTime,
	}
	state.AgentInstances["claude-code-2"] = &domain.AgentInstance{
		InstanceID:    "claude-code-2",
		AgentType:     "claude-code",
		Role:          domain.RoleWorker,
		Status:        "offline",
		LastHeartbeat: staleTime,
	}
	// Alive task-bound worker, heartbeating normally.
	state.AgentInstances["claude-code-task-1"] = &domain.AgentInstance{
		InstanceID:    "claude-code-task-1",
		AgentType:     "claude-code",
		Role:          domain.RoleWorker,
		Status:        "busy",
		CurrentTasks:  []int{1},
		LastHeartbeat: now.Add(-30 * time.Second),
	}
	state.DriverID = "cursor"

	state.Tasks = append(state.Tasks, domain.Task{
		ID:         1,
		Title:      "Review code",
		Status:     "in_progress",
		AssignedTo: "claude-code",
		UpdatedAt:  now.Add(-1 * time.Minute),
	})
	state.NextTaskID = 2
	state.NextMsgID = 1

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	wd := NewWatchdog(svc, registry, logger,
		WithHeartbeatThreshold(2*time.Minute),
		WithTaskStuckThreshold(10*time.Minute),
	)

	wd.CheckOnce()

	_ = svc.Query(func(s *domain.CollabState) error {
		if s.Tasks[0].Status != "in_progress" {
			t.Errorf("task should remain in_progress (task-bound worker is alive), got %q", s.Tasks[0].Status)
		}
		return nil
	})
}

func TestWatchdog_TaskBoundWorkerDead_RecoverTask(t *testing.T) {
	// All instances are dead, including the task-bound worker.
	// The task should be recovered.
	state := domain.NewCollabState()
	now := time.Now()
	staleTime := now.Add(-15 * time.Minute)

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID:    "cursor",
		AgentType:     "cursor",
		Role:          domain.RoleDriver,
		Status:        "idle",
		LastHeartbeat: now,
	}
	state.AgentInstances["claude-code-1"] = &domain.AgentInstance{
		InstanceID:    "claude-code-1",
		AgentType:     "claude-code",
		Role:          domain.RoleWorker,
		Status:        "offline",
		LastHeartbeat: staleTime,
	}
	// Task-bound worker also dead.
	state.AgentInstances["claude-code-task-1"] = &domain.AgentInstance{
		InstanceID:    "claude-code-task-1",
		AgentType:     "claude-code",
		Role:          domain.RoleWorker,
		Status:        "busy",
		CurrentTasks:  []int{1},
		LastHeartbeat: staleTime,
	}
	state.DriverID = "cursor"

	state.Tasks = append(state.Tasks, domain.Task{
		ID:         1,
		Title:      "Dead worker task",
		Status:     "in_progress",
		AssignedTo: "claude-code",
		UpdatedAt:  staleTime,
	})
	state.NextTaskID = 2
	state.NextMsgID = 1

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	wd := NewWatchdog(svc, registry, logger,
		WithHeartbeatThreshold(2*time.Minute),
	)

	wd.CheckOnce()

	_ = svc.Query(func(s *domain.CollabState) error {
		if s.Tasks[0].Status != "pending" {
			t.Errorf("task should be reset to pending (all instances dead), got %q", s.Tasks[0].Status)
		}
		return nil
	})
}

func TestWatchdog_AgentTypeNotDeadWhenAnyInstanceAlive(t *testing.T) {
	// One instance of "claude-code" is dead, another is alive.
	// Tasks assigned to a DIFFERENT dead instance should be recovered,
	// but the type "claude-code" itself should not be blanket-dead.
	state := domain.NewCollabState()
	now := time.Now()
	staleTime := now.Add(-15 * time.Minute)

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID:    "cursor",
		AgentType:     "cursor",
		Role:          domain.RoleDriver,
		Status:        "idle",
		LastHeartbeat: now,
	}
	// Dead instance with a task.
	state.AgentInstances["claude-code-1"] = &domain.AgentInstance{
		InstanceID:    "claude-code-1",
		AgentType:     "claude-code",
		Role:          domain.RoleWorker,
		Status:        "busy",
		CurrentTasks:  []int{1},
		LastHeartbeat: staleTime,
	}
	// Alive instance with a different task.
	state.AgentInstances["claude-code-2"] = &domain.AgentInstance{
		InstanceID:    "claude-code-2",
		AgentType:     "claude-code",
		Role:          domain.RoleWorker,
		Status:        "busy",
		CurrentTasks:  []int{2},
		LastHeartbeat: now,
	}
	state.DriverID = "cursor"

	state.Tasks = append(state.Tasks,
		domain.Task{
			ID: 1, Title: "Task on dead instance", Status: "in_progress",
			AssignedTo: "claude-code-1", UpdatedAt: staleTime,
		},
		domain.Task{
			ID: 2, Title: "Task on alive instance", Status: "in_progress",
			AssignedTo: "claude-code-2", UpdatedAt: now,
		},
	)
	state.NextTaskID = 3
	state.NextMsgID = 1

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	wd := NewWatchdog(svc, registry, logger,
		WithHeartbeatThreshold(2*time.Minute),
	)

	wd.CheckOnce()

	_ = svc.Query(func(s *domain.CollabState) error {
		for _, task := range s.Tasks {
			switch task.ID {
			case 1:
				if task.Status != "pending" {
					t.Errorf("task #1 (dead instance) should be pending, got %q", task.Status)
				}
			case 2:
				if task.Status != "in_progress" {
					t.Errorf("task #2 (alive instance) should remain in_progress, got %q", task.Status)
				}
			}
		}
		return nil
	})
}

func TestWatchdog_MultipleTaskBoundWorkers_IndependentLiveness(t *testing.T) {
	// Two tasks each with their own task-bound worker. One alive, one dead.
	// Only the dead one's task should be recovered.
	state := domain.NewCollabState()
	now := time.Now()
	staleTime := now.Add(-15 * time.Minute)

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID:    "cursor",
		AgentType:     "cursor",
		Role:          domain.RoleDriver,
		Status:        "idle",
		LastHeartbeat: now,
	}
	// Alive task-bound worker for task 1.
	state.AgentInstances["codex-task-1"] = &domain.AgentInstance{
		InstanceID:    "codex-task-1",
		AgentType:     "codex",
		Role:          domain.RoleWorker,
		Status:        "busy",
		CurrentTasks:  []int{1},
		LastHeartbeat: now.Add(-20 * time.Second),
	}
	// Dead task-bound worker for task 2.
	state.AgentInstances["codex-task-2"] = &domain.AgentInstance{
		InstanceID:    "codex-task-2",
		AgentType:     "codex",
		Role:          domain.RoleWorker,
		Status:        "busy",
		CurrentTasks:  []int{2},
		LastHeartbeat: staleTime,
	}
	state.DriverID = "cursor"

	state.Tasks = append(state.Tasks,
		domain.Task{
			ID: 1, Title: "Alive worker task", Status: "in_progress",
			AssignedTo: "codex", UpdatedAt: now.Add(-1 * time.Minute),
		},
		domain.Task{
			ID: 2, Title: "Dead worker task", Status: "in_progress",
			AssignedTo: "codex", UpdatedAt: staleTime,
		},
	)
	state.NextTaskID = 3
	state.NextMsgID = 1

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	wd := NewWatchdog(svc, registry, logger,
		WithHeartbeatThreshold(2*time.Minute),
	)

	wd.CheckOnce()

	_ = svc.Query(func(s *domain.CollabState) error {
		for _, task := range s.Tasks {
			switch task.ID {
			case 1:
				if task.Status != "in_progress" {
					t.Errorf("task #1 (alive task-bound worker) should remain in_progress, got %q", task.Status)
				}
			case 2:
				if task.Status != "pending" {
					t.Errorf("task #2 (dead task-bound worker) should be pending, got %q", task.Status)
				}
			}
		}
		return nil
	})
}

// mockTriggerable records Trigger calls.
type mockTriggerable struct {
	fn func()
}

func (m *mockTriggerable) Trigger() {
	if m.fn != nil {
		m.fn()
	}
}
