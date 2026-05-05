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
			AssignedTo: "claude-code", UpdatedAt: staleTime,
		},
		domain.Task{
			ID: 2, Title: "Task on alive instance", Status: "in_progress",
			AssignedTo: "claude-code", UpdatedAt: now,
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

// TestWatchdog_TaskBoundWorker_StaticPoolAssignee_PreventsRecovery verifies
// the primary scenario described in fix-watchdog-task-correlation: a task is
// assigned to the parent type "claude-code" (because the orchestrator picked
// a static pool instance "claude-code-1"), and a task-bound child
// "claude-code-task-5" is actively doing the work. Even if the static pool
// instance itself has a stale heartbeat, the live task-bound worker's
// liveness must prevent the task from being reset.
func TestWatchdog_TaskBoundWorker_StaticPoolAssignee_PreventsRecovery(t *testing.T) {
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
	// Static pool instance (the one the orchestrator "assigned" the task to).
	// It is itself stale — mirrors real-world conditions where the idle pool
	// instance doesn't heartbeat because it has handed work off to a child.
	state.AgentInstances["claude-code-1"] = &domain.AgentInstance{
		InstanceID:    "claude-code-1",
		AgentType:     "claude-code",
		Role:          domain.RoleWorker,
		Status:        "busy",
		CurrentTasks:  []int{5},
		LastHeartbeat: staleTime,
	}
	// Task-bound child actually running the work. Alive.
	state.AgentInstances["claude-code-task-5"] = &domain.AgentInstance{
		InstanceID:    "claude-code-task-5",
		AgentType:     "claude-code",
		Role:          domain.RoleWorker,
		Status:        "busy",
		CurrentTasks:  []int{5},
		LastHeartbeat: now.Add(-30 * time.Second),
	}
	state.DriverID = "cursor"

	state.Tasks = append(state.Tasks, domain.Task{
		ID: 5, Title: "Owned by task-bound child", Status: "in_progress",
		AssignedTo: "claude-code", UpdatedAt: now.Add(-1 * time.Minute),
	})
	state.NextTaskID = 6
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
			if task.ID != 5 {
				continue
			}
			if task.Status != "in_progress" {
				t.Errorf("task #5 should stay in_progress because task-bound child is alive, got %q", task.Status)
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

func TestWatchdog_ActiveProcess_PreventsAgentKill(t *testing.T) {
	// Reproduces the Codex bug: agent has stale heartbeat (never called heartbeat)
	// but its spawned process is actively producing stdout output. The watchdog
	// should treat process output as a liveness signal and NOT mark it offline.
	state := domain.NewCollabState()
	now := time.Now()
	staleTime := now.Add(-10 * time.Minute)

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID:    "cursor",
		AgentType:     "cursor",
		Role:          domain.RoleDriver,
		Status:        "idle",
		LastHeartbeat: now,
	}
	state.AgentInstances["codex"] = &domain.AgentInstance{
		InstanceID:    "codex",
		AgentType:     "codex",
		Role:          domain.RoleWorker,
		Status:        "busy",
		CurrentTasks:  []int{3},
		LastHeartbeat: staleTime, // never heartbeated properly
	}
	state.AgentInstances["codex-task-3"] = &domain.AgentInstance{
		InstanceID:    "codex-task-3",
		AgentType:     "codex",
		Role:          domain.RoleWorker,
		Status:        "busy",
		CurrentTasks:  []int{3},
		LastHeartbeat: staleTime,
	}
	state.DriverID = "cursor"

	state.Tasks = append(state.Tasks, domain.Task{
		ID:         3,
		Title:      "Review plan",
		Status:     "in_progress",
		AssignedTo: "codex",
		UpdatedAt:  staleTime,
	})
	state.NextTaskID = 4
	state.NextMsgID = 1

	mock := &mockProcessActivity{
		procs: map[string]ProcessInfo{
			"codex-task-3": {
				InstanceID:   "codex-task-3",
				StartedAt:    now.Add(-5 * time.Minute),
				LastOutputAt: now.Add(-30 * time.Second), // actively producing output
				OutputBytes:  750000,
			},
		},
	}

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	wd := NewWatchdog(svc, registry, logger,
		WithHeartbeatThreshold(2*time.Minute),
		WithProcessActivity(mock),
	)
	wd.CheckOnce()

	_ = svc.Query(func(s *domain.CollabState) error {
		if s.Tasks[0].Status != "in_progress" {
			t.Errorf("task with active process output should remain in_progress, got %q", s.Tasks[0].Status)
		}
		return nil
	})

	_ = svc.Query(func(s *domain.CollabState) error {
		inst := s.AgentInstances["codex"]
		if inst.Status == "offline" {
			t.Error("agent with active process output should not be marked offline")
		}
		inst = s.AgentInstances["codex-task-3"]
		if inst.Status == "offline" {
			t.Error("task-bound instance with active process output should not be marked offline")
		}
		return nil
	})
}

func TestWatchdog_StaleProcess_StillKillsAgent(t *testing.T) {
	// Process exists but its output is older than the threshold.
	// Agent SHOULD be marked offline and task SHOULD be recovered.
	state := domain.NewCollabState()
	now := time.Now()
	staleTime := now.Add(-10 * time.Minute)

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID: "cursor", AgentType: "cursor",
		Role: domain.RoleDriver, Status: "idle", LastHeartbeat: now,
	}
	state.AgentInstances["codex"] = &domain.AgentInstance{
		InstanceID: "codex", AgentType: "codex",
		Role: domain.RoleWorker, Status: "busy",
		CurrentTasks: []int{1}, LastHeartbeat: staleTime,
	}
	state.DriverID = "cursor"
	state.Tasks = append(state.Tasks, domain.Task{
		ID: 1, Title: "Stale process task", Status: "in_progress",
		AssignedTo: "codex", UpdatedAt: staleTime,
	})
	state.NextTaskID = 2
	state.NextMsgID = 1

	mock := &mockProcessActivity{
		procs: map[string]ProcessInfo{
			"codex-task-1": {
				InstanceID:   "codex-task-1",
				StartedAt:    staleTime,
				LastOutputAt: now.Add(-5 * time.Minute), // output older than 2min threshold
				OutputBytes:  1000,
			},
		},
	}

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	wd := NewWatchdog(svc, registry, logger,
		WithHeartbeatThreshold(2*time.Minute),
		WithProcessActivity(mock),
	)
	wd.CheckOnce()

	_ = svc.Query(func(s *domain.CollabState) error {
		if s.Tasks[0].Status == "in_progress" {
			t.Error("task with stale process output should be recovered")
		}
		return nil
	})

	_ = svc.Query(func(s *domain.CollabState) error {
		inst := s.AgentInstances["codex"]
		if inst.Status != "offline" {
			t.Errorf("agent with stale process should be offline, got %q", inst.Status)
		}
		return nil
	})
}

func TestWatchdog_NoProcessProvider_BackwardCompatible(t *testing.T) {
	// When WithProcessActivity is NOT set, behavior should be unchanged:
	// stale heartbeat → agent is killed. No panic from nil provider.
	state := domain.NewCollabState()
	now := time.Now()
	staleTime := now.Add(-10 * time.Minute)

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID: "cursor", AgentType: "cursor",
		Role: domain.RoleDriver, Status: "idle", LastHeartbeat: now,
	}
	state.AgentInstances["codex"] = &domain.AgentInstance{
		InstanceID: "codex", AgentType: "codex",
		Role: domain.RoleWorker, Status: "busy",
		CurrentTasks: []int{1}, LastHeartbeat: staleTime,
	}
	state.DriverID = "cursor"
	state.Tasks = append(state.Tasks, domain.Task{
		ID: 1, Title: "No provider task", Status: "in_progress",
		AssignedTo: "codex", UpdatedAt: staleTime,
	})
	state.NextTaskID = 2
	state.NextMsgID = 1

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	// No WithProcessActivity — provider is nil
	wd := NewWatchdog(svc, registry, logger,
		WithHeartbeatThreshold(2*time.Minute),
	)
	wd.CheckOnce()

	_ = svc.Query(func(s *domain.CollabState) error {
		if s.Tasks[0].Status == "in_progress" {
			t.Error("without process provider, stale heartbeat should still kill agent")
		}
		return nil
	})
}

func TestWatchdog_DirectProcessMatch_PreventsKill(t *testing.T) {
	// Process is registered under the exact agent name "codex" (not task-bound).
	// Should still prevent the agent from being killed.
	state := domain.NewCollabState()
	now := time.Now()
	staleTime := now.Add(-10 * time.Minute)

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID: "cursor", AgentType: "cursor",
		Role: domain.RoleDriver, Status: "idle", LastHeartbeat: now,
	}
	state.AgentInstances["codex"] = &domain.AgentInstance{
		InstanceID: "codex", AgentType: "codex",
		Role: domain.RoleWorker, Status: "busy",
		CurrentTasks: []int{1}, LastHeartbeat: staleTime,
	}
	state.DriverID = "cursor"
	state.Tasks = append(state.Tasks, domain.Task{
		ID: 1, Title: "Direct match task", Status: "in_progress",
		AssignedTo: "codex", UpdatedAt: staleTime,
	})
	state.NextTaskID = 2
	state.NextMsgID = 1

	mock := &mockProcessActivity{
		procs: map[string]ProcessInfo{
			"codex": {
				InstanceID:   "codex",
				StartedAt:    now.Add(-3 * time.Minute),
				LastOutputAt: now.Add(-10 * time.Second),
				OutputBytes:  50000,
			},
		},
	}

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	wd := NewWatchdog(svc, registry, logger,
		WithHeartbeatThreshold(2*time.Minute),
		WithProcessActivity(mock),
	)
	wd.CheckOnce()

	_ = svc.Query(func(s *domain.CollabState) error {
		if s.Tasks[0].Status != "in_progress" {
			t.Errorf("direct process match should keep task alive, got %q", s.Tasks[0].Status)
		}
		inst := s.AgentInstances["codex"]
		if inst.Status == "offline" {
			t.Error("direct process match should prevent offline")
		}
		return nil
	})
}

func TestWatchdog_ProcessPrefixNoFalsePositive(t *testing.T) {
	// Agent "codex-extra" has an active process. Agent "codex" should NOT be
	// kept alive by it — prefix match requires "codex-" but "codex-extra" is
	// a separate agent, not a task-bound child of "codex".
	// However, strings.HasPrefix("codex-extra", "codex-") IS true, so this
	// tests that the prefix-based approach does match other instances of the
	// same agent type. In practice, task-bound IDs use "codex-task-N" format
	// and "codex-extra" would be a different numbered instance.
	// The important safety check: "codex-extra" should NOT match agent "code".
	state := domain.NewCollabState()
	now := time.Now()
	staleTime := now.Add(-10 * time.Minute)

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID: "cursor", AgentType: "cursor",
		Role: domain.RoleDriver, Status: "idle", LastHeartbeat: now,
	}
	state.AgentInstances["code"] = &domain.AgentInstance{
		InstanceID: "code", AgentType: "code",
		Role: domain.RoleWorker, Status: "busy",
		CurrentTasks: []int{1}, LastHeartbeat: staleTime,
	}
	state.DriverID = "cursor"
	state.Tasks = append(state.Tasks, domain.Task{
		ID: 1, Title: "Prefix safety task", Status: "in_progress",
		AssignedTo: "code", UpdatedAt: staleTime,
	})
	state.NextTaskID = 2
	state.NextMsgID = 1

	mock := &mockProcessActivity{
		procs: map[string]ProcessInfo{
			// "codex-task-5" should NOT match agent "code" (prefix "code-" != "codex-task-5")
			"codex-task-5": {
				InstanceID:   "codex-task-5",
				StartedAt:    now.Add(-1 * time.Minute),
				LastOutputAt: now.Add(-5 * time.Second),
				OutputBytes:  100000,
			},
		},
	}

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	wd := NewWatchdog(svc, registry, logger,
		WithHeartbeatThreshold(2*time.Minute),
		WithProcessActivity(mock),
	)
	wd.CheckOnce()

	_ = svc.Query(func(s *domain.CollabState) error {
		if s.Tasks[0].Status == "in_progress" {
			t.Error("agent 'code' should not be kept alive by process 'codex-task-5'")
		}
		inst := s.AgentInstances["code"]
		if inst.Status != "offline" {
			t.Errorf("agent 'code' should be offline, got %q", inst.Status)
		}
		return nil
	})
}

func TestWatchdog_ActiveProcess_PreventsSessionPrune(t *testing.T) {
	// Agent has a session registered but stale heartbeat. Process is producing
	// output. Session should NOT be pruned.
	state := domain.NewCollabState()
	now := time.Now()
	staleTime := now.Add(-10 * time.Minute)

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID:    "cursor",
		AgentType:     "cursor",
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
		LastHeartbeat: staleTime,
	}
	state.DriverID = "cursor"
	state.NextMsgID = 1

	mock := &mockProcessActivity{
		procs: map[string]ProcessInfo{
			"codex-task-1": {
				InstanceID:   "codex-task-1",
				StartedAt:    now.Add(-3 * time.Minute),
				LastOutputAt: now.Add(-20 * time.Second),
				OutputBytes:  400000,
			},
		},
	}

	svc := testService(state)
	registry := NewSessionRegistry()
	registry.SetAgent("codex-session", "codex")
	registry.BackdateActivity("codex-session", staleTime)
	logger := log.New(os.Stderr, "[test] ", 0)

	wd := NewWatchdog(svc, registry, logger,
		WithHeartbeatThreshold(2*time.Minute),
		WithSessionStaleThreshold(2*time.Minute),
		WithProcessActivity(mock),
	)
	wd.CheckOnce()

	if !registry.HasActiveSession("codex") {
		t.Error("session should not be pruned when process is producing output")
	}
}

func TestWatchdog_StaleProcess_AllowsSessionPrune(t *testing.T) {
	// Process exists but output is stale. Session SHOULD be pruned.
	state := domain.NewCollabState()
	now := time.Now()
	staleTime := now.Add(-10 * time.Minute)

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID: "cursor", AgentType: "cursor",
		Role: domain.RoleDriver, Status: "idle", LastHeartbeat: now,
	}
	state.AgentInstances["codex"] = &domain.AgentInstance{
		InstanceID: "codex", AgentType: "codex",
		Role: domain.RoleWorker, Status: "busy",
		CurrentTasks: []int{1}, LastHeartbeat: staleTime,
	}
	state.DriverID = "cursor"
	state.NextMsgID = 1

	mock := &mockProcessActivity{
		procs: map[string]ProcessInfo{
			"codex-task-1": {
				InstanceID:   "codex-task-1",
				StartedAt:    staleTime,
				LastOutputAt: now.Add(-5 * time.Minute), // stale output
				OutputBytes:  1000,
			},
		},
	}

	svc := testService(state)
	registry := NewSessionRegistry()
	registry.SetAgent("codex-session", "codex")
	registry.BackdateActivity("codex-session", staleTime)
	logger := log.New(os.Stderr, "[test] ", 0)

	wd := NewWatchdog(svc, registry, logger,
		WithHeartbeatThreshold(2*time.Minute),
		WithSessionStaleThreshold(2*time.Minute),
		WithProcessActivity(mock),
	)
	wd.CheckOnce()

	if registry.HasActiveSession("codex") {
		t.Error("session should be pruned when process output is stale")
	}
}

func TestWatchdog_MultipleProcesses_MixedLiveness(t *testing.T) {
	// Two tasks for "codex", each with a task-bound process. One active, one stale.
	// The active one keeps the agent type alive, but the stale task should still
	// be recovered via checkTaskBoundWorker (existing logic).
	state := domain.NewCollabState()
	now := time.Now()
	staleTime := now.Add(-10 * time.Minute)

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID: "cursor", AgentType: "cursor",
		Role: domain.RoleDriver, Status: "idle", LastHeartbeat: now,
	}
	state.AgentInstances["codex"] = &domain.AgentInstance{
		InstanceID: "codex", AgentType: "codex",
		Role: domain.RoleWorker, Status: "busy",
		CurrentTasks: []int{1, 2}, LastHeartbeat: staleTime,
	}
	state.AgentInstances["codex-task-1"] = &domain.AgentInstance{
		InstanceID: "codex-task-1", AgentType: "codex",
		Role: domain.RoleWorker, Status: "busy",
		CurrentTasks: []int{1}, LastHeartbeat: now.Add(-30 * time.Second),
	}
	state.AgentInstances["codex-task-2"] = &domain.AgentInstance{
		InstanceID: "codex-task-2", AgentType: "codex",
		Role: domain.RoleWorker, Status: "busy",
		CurrentTasks: []int{2}, LastHeartbeat: staleTime,
	}
	state.DriverID = "cursor"
	state.Tasks = append(state.Tasks,
		domain.Task{
			ID: 1, Title: "Active task", Status: "in_progress",
			AssignedTo: "codex", UpdatedAt: now.Add(-1 * time.Minute),
		},
		domain.Task{
			ID: 2, Title: "Dead task", Status: "in_progress",
			AssignedTo: "codex", UpdatedAt: staleTime,
		},
	)
	state.NextTaskID = 3
	state.NextMsgID = 1

	mock := &mockProcessActivity{
		procs: map[string]ProcessInfo{
			"codex-task-1": {
				InstanceID:   "codex-task-1",
				StartedAt:    now.Add(-3 * time.Minute),
				LastOutputAt: now.Add(-15 * time.Second), // active
				OutputBytes:  500000,
			},
			"codex-task-2": {
				InstanceID:   "codex-task-2",
				StartedAt:    staleTime,
				LastOutputAt: now.Add(-8 * time.Minute), // stale
				OutputBytes:  2000,
			},
		},
	}

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	wd := NewWatchdog(svc, registry, logger,
		WithHeartbeatThreshold(2*time.Minute),
		WithProcessActivity(mock),
	)
	wd.CheckOnce()

	_ = svc.Query(func(s *domain.CollabState) error {
		for _, task := range s.Tasks {
			switch task.ID {
			case 1:
				if task.Status != "in_progress" {
					t.Errorf("task #1 (active process) should stay in_progress, got %q", task.Status)
				}
			case 2:
				if task.Status != "pending" {
					t.Errorf("task #2 (stale process) should be recovered to pending, got %q", task.Status)
				}
			}
		}
		return nil
	})

	// codex type should NOT be offline — it has an active child process
	_ = svc.Query(func(s *domain.CollabState) error {
		inst := s.AgentInstances["codex"]
		if inst.Status == "offline" {
			t.Error("agent type with an active child process should not be offline")
		}
		return nil
	})
}

// TestWatchdog_SLAExceededDoesNotBlockAutoCancel (H8) — once a task hits
// SLA exceeded the watchdog must still be able to escalate to the
// progress-critical auto-cancel branch. Previously SLA wrote
// "sla_exceeded" into alertedTasks, and the critical guard at the
// progress-critical branch read `currentLevel != "sla_exceeded"`,
// silently swallowing the auto-cancel for any task that had ever been
// SLA-flagged.
func TestWatchdog_SLAExceededDoesNotBlockAutoCancel(t *testing.T) {
	state := domain.NewCollabState()
	now := time.Now()

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver,
		Status: "idle", LastHeartbeat: now,
	}
	state.AgentInstances["claude-code"] = &domain.AgentInstance{
		InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker,
		Status: "busy", CurrentTasks: []int{1},
		// Heartbeat fresh so the dead-agent recovery path is NOT what
		// drives this test — we want the progress-critical auto-cancel
		// branch to fire on its own.
		LastHeartbeat: now,
	}
	state.DriverID = "cursor"

	// SLA budget = 1 minute, task started 10 minutes ago, no progress
	// for 10 minutes. Both SLA AND progress-critical conditions are
	// simultaneously true.
	taskStart := now.Add(-10 * time.Minute)
	state.Tasks = append(state.Tasks, domain.Task{
		ID: 1, Title: "Slow task", Status: "in_progress",
		AssignedTo: "claude-code", UpdatedAt: taskStart,
		LastProgressAt:      taskStart,
		ExpectedDurationSec: 60,
	})
	state.NextTaskID = 2
	state.NextMsgID = 1

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	wd := NewWatchdog(svc, registry, logger,
		WithHeartbeatThreshold(30*time.Minute),
		WithProgressWarningThreshold(2*time.Minute),
		WithProgressCriticalThreshold(5*time.Minute),
		WithMaxTaskFailures(3),
		WithAutoCanceller(&mockAutoCanceller{}),
	)
	wd.CheckOnce()

	_ = svc.Query(func(s *domain.CollabState) error {
		var sawSLA, sawAutoCancel bool
		for _, m := range s.Messages {
			if strings.Contains(m.Content, "SLA exceeded") {
				sawSLA = true
			}
			if strings.Contains(m.Content, "AUTO-CANCELLING") || strings.Contains(m.Content, "AUTO-RECOVERING") {
				sawAutoCancel = true
			}
		}
		if !sawSLA {
			t.Errorf("expected SLA-exceeded alert, none found in %d messages", len(s.Messages))
		}
		if !sawAutoCancel {
			t.Errorf("expected progress-critical auto-cancel alert to ALSO fire (SLA must not block it); messages=%d", len(s.Messages))
		}
		// Task should be reset to pending or auto-blocked.
		if s.Tasks[0].Status == "in_progress" {
			t.Errorf("task still in_progress; auto-cancel branch was suppressed by SLA flag")
		}
		return nil
	})
}

// mockAutoCanceller for tests that need autoCanceller wired up.
type mockAutoCanceller struct {
	cancelled []string
}

func (m *mockAutoCanceller) CancelWorker(id string) bool {
	m.cancelled = append(m.cancelled, id)
	return true
}
func (m *mockAutoCanceller) GetRecentOutput(id string) string { return "" }

// TestWatchdog_DeadAgentRecoveryWithStaleSession (H9) — when an agent
// has a session in the registry whose lastActivity is older than the
// progress-warning threshold, the watchdog must NOT treat the session
// as a sign of life. Sessions can outlive the underlying worker
// process (the registry doesn't get an explicit disconnect when a
// process is SIGKILLed); recovery must rely on the recency of activity,
// not the mere presence of a session.
func TestWatchdog_DeadAgentRecoveryWithStaleSession(t *testing.T) {
	state := domain.NewCollabState()
	now := time.Now()
	staleHB := now.Add(-30 * time.Minute)

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver,
		Status: "idle", LastHeartbeat: now,
	}
	state.AgentInstances["claude-code"] = &domain.AgentInstance{
		InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker,
		Status: "busy", CurrentTasks: []int{1},
		LastHeartbeat: staleHB,
	}
	state.DriverID = "cursor"
	state.Tasks = append(state.Tasks, domain.Task{
		ID: 1, Title: "Orphaned task", Status: "in_progress",
		AssignedTo: "claude-code", UpdatedAt: staleHB,
	})
	state.NextTaskID = 2
	state.NextMsgID = 1

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	// Simulate a registry session that has gone stale (e.g. the worker
	// was SIGKILLed, or the SSE stream dropped without an explicit
	// disconnect). The session row exists but no recent activity.
	registry.SetAgent("session-stale", "claude-code")
	registry.BackdateActivity("session-stale", now.Add(-30*time.Minute))

	wd := NewWatchdog(svc, registry, logger,
		WithHeartbeatThreshold(5*time.Minute),
		WithProgressWarningThreshold(3*time.Minute),
	)
	wd.CheckOnce()

	_ = svc.Query(func(s *domain.CollabState) error {
		if s.Tasks[0].Status != "pending" && s.Tasks[0].Status != "blocked" {
			t.Errorf("expected stuck task to be recovered (pending/blocked) when session is stale; got %q", s.Tasks[0].Status)
		}
		inst := s.AgentInstances["claude-code"]
		if inst == nil {
			t.Fatal("agent instance unexpectedly removed")
		}
		if inst.Status != "offline" {
			t.Errorf("expected agent to be marked offline after stale-session recovery; got %q", inst.Status)
		}
		return nil
	})
}

// TestWatchdog_AutoHeartbeatDebounceDoesNotMaskDeadWorker (M4) — the
// piggyback auto-heartbeat refresh must NOT bump LastHeartbeat for a
// worker whose underlying process is dead. Otherwise a stray tool call
// (replay, late-arriving message, etc.) extends the agent's effective
// heartbeat past the watchdog threshold and prevents recovery.
//
// This test exercises the watchdog half of the contract: an agent with
// stale process activity AND stale heartbeat should still be marked
// dead even after a recent registry-level signal. The piggyback gate
// is exercised in piggyback_test.go.
func TestWatchdog_AutoHeartbeatDebounceDoesNotMaskDeadWorker(t *testing.T) {
	state := domain.NewCollabState()
	now := time.Now()
	staleHB := now.Add(-30 * time.Minute)

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver,
		Status: "idle", LastHeartbeat: now,
	}
	state.AgentInstances["claude-code"] = &domain.AgentInstance{
		InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker,
		Status: "busy", CurrentTasks: []int{1},
		LastHeartbeat: staleHB,
	}
	state.DriverID = "cursor"
	state.Tasks = append(state.Tasks, domain.Task{
		ID: 1, Title: "Stuck on dead worker", Status: "in_progress",
		AssignedTo: "claude-code", UpdatedAt: staleHB,
	})
	state.NextTaskID = 2
	state.NextMsgID = 1

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	// processActivity reports a process row but with output that's far
	// older than the heartbeat staleness threshold. This is the "dead
	// worker, still in process registry" condition.
	mp := &mockProcessActivity{procs: map[string]ProcessInfo{
		"claude-code": {LastOutputAt: staleHB},
	}}

	wd := NewWatchdog(svc, registry, logger,
		WithHeartbeatThreshold(5*time.Minute),
		WithProgressWarningThreshold(3*time.Minute),
		WithProcessActivity(mp),
	)
	wd.CheckOnce()

	_ = svc.Query(func(s *domain.CollabState) error {
		if s.Tasks[0].Status != "pending" && s.Tasks[0].Status != "blocked" {
			t.Errorf("expected stuck task to be recovered when process is dead; got %q", s.Tasks[0].Status)
		}
		return nil
	})
}

// TestWatchdog_AlertDeduplicationCoversSnippetVariants — the dedupe
// gate uses alertedTasks[id] to track which level was already sent, so
// changing snippet content between ticks must NOT cause a duplicate
// alert. Locks in the keyed-by-task dedup contract.
func TestWatchdog_AlertDeduplicationCoversSnippetVariants(t *testing.T) {
	state := domain.NewCollabState()
	now := time.Now()
	staleProgress := now.Add(-6 * time.Minute)

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver,
		Status: "idle", LastHeartbeat: now,
	}
	state.AgentInstances["claude-code"] = &domain.AgentInstance{
		InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker,
		Status: "busy", CurrentTasks: []int{1}, LastHeartbeat: now,
	}
	state.DriverID = "cursor"
	state.Tasks = append(state.Tasks, domain.Task{
		ID: 1, Title: "Critical task", Status: "in_progress",
		AssignedTo: "claude-code", UpdatedAt: staleProgress,
		LastProgressAt: staleProgress,
	})
	state.NextTaskID = 2
	state.NextMsgID = 1

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)
	mp := &mockProcessActivity{
		procs: map[string]ProcessInfo{
			"claude-code": {LastOutputAt: now.Add(-30 * time.Second), OutputBytes: 100},
		},
		output: map[string]string{"claude-code": "first snippet"},
	}

	wd := NewWatchdog(svc, registry, logger,
		WithHeartbeatThreshold(30*time.Minute),
		WithProgressWarningThreshold(2*time.Minute),
		WithProgressCriticalThreshold(5*time.Minute),
		WithProcessActivity(mp),
	)
	wd.CheckOnce()

	var firstCount int
	_ = svc.Query(func(s *domain.CollabState) error {
		firstCount = len(s.Messages)
		return nil
	})

	// Second tick: snippet changes. Should NOT generate another critical
	// alert for the same task.
	mp.output["claude-code"] = "second different snippet"
	wd.CheckOnce()

	_ = svc.Query(func(s *domain.CollabState) error {
		if len(s.Messages) != firstCount {
			t.Errorf("dedup failed: tick 2 added %d new messages despite same level", len(s.Messages)-firstCount)
		}
		return nil
	})
}

// TestWatchdog_DLQAtExactlyMaxFailuresNotPlusOne — when a task hits
// FailureCount == maxTaskFailures, it should be auto-blocked exactly
// at that count, not at maxTaskFailures+1. Locks in the >= boundary.
func TestWatchdog_DLQAtExactlyMaxFailuresNotPlusOne(t *testing.T) {
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
		ID: 1, Title: "Boundary task", Status: "in_progress",
		AssignedTo: "claude-code", UpdatedAt: staleTime,
		FailureCount: 1, // After this tick should hit 2.
	})
	state.NextTaskID = 2
	state.NextMsgID = 1

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	wd := NewWatchdog(svc, registry, logger,
		WithHeartbeatThreshold(1*time.Minute),
		WithMaxTaskFailures(2),
	)
	wd.CheckOnce()

	_ = svc.Query(func(s *domain.CollabState) error {
		task := s.Tasks[0]
		if task.FailureCount != 2 {
			t.Errorf("FailureCount = %d, want 2 (exact)", task.FailureCount)
		}
		if task.Status != "blocked" {
			t.Errorf("Status = %q, want blocked at exactly maxTaskFailures (not max+1)", task.Status)
		}
		return nil
	})
}

// TestWatchdog_DriverIsNeverPruned — driver instances are exempt from
// every prune/dead path: dead-agent recovery, instance pruning, session
// pruning. Locks in the role-based exemption.
func TestWatchdog_DriverIsNeverPruned(t *testing.T) {
	state := domain.NewCollabState()
	now := time.Now()
	veryOld := now.Add(-30 * 24 * time.Hour) // 30 days

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver,
		Status: "idle", LastHeartbeat: veryOld,
	}
	state.DriverID = "cursor"
	state.Presence = map[string]*domain.Presence{
		"cursor": {Agent: "cursor", LastSeen: veryOld},
	}
	state.NextTaskID = 1
	state.NextMsgID = 1

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	wd := NewWatchdog(svc, registry, logger,
		WithPolicy(testPolicy()),
		WithHeartbeatThreshold(1*time.Minute),
		WithSessionStaleThreshold(1*time.Minute),
	)
	wd.CheckOnce()

	_ = svc.Query(func(s *domain.CollabState) error {
		inst, ok := s.AgentInstances["cursor"]
		if !ok || inst == nil {
			t.Fatal("driver instance was pruned — drivers must never be removed")
		}
		if inst.Status == "offline" {
			t.Errorf("driver was marked offline; drivers are exempt from liveness checks")
		}
		if _, ok := s.Presence["cursor"]; !ok {
			t.Errorf("driver presence was pruned; drivers are exempt from presence GC")
		}
		return nil
	})
}

// TestWatchdog_RestartDoesNotMarkAliveAgentDead — after a server
// restart, RefreshHeartbeatsOnStartup primes LastHeartbeat = now for
// every instance. The next watchdog tick must NOT flip recently-primed
// agents back to dead just because they have not yet had a chance to
// reconnect their session.
func TestWatchdog_RestartDoesNotMarkAliveAgentDead(t *testing.T) {
	state := domain.NewCollabState()

	// Pre-restart state: task in_progress, agent had been alive.
	staleStart := time.Now().Add(-2 * time.Hour)
	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver,
		Status: "idle", LastHeartbeat: staleStart,
	}
	state.AgentInstances["claude-code"] = &domain.AgentInstance{
		InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker,
		Status: "busy", CurrentTasks: []int{1}, LastHeartbeat: staleStart,
	}
	state.DriverID = "cursor"
	state.Tasks = append(state.Tasks, domain.Task{
		ID: 1, Title: "In-flight task", Status: "in_progress",
		AssignedTo: "claude-code", UpdatedAt: staleStart,
	})
	state.NextTaskID = 2
	state.NextMsgID = 1

	// Simulate restart.
	RefreshHeartbeatsOnStartup(state)

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	wd := NewWatchdog(svc, registry, logger,
		WithHeartbeatThreshold(5*time.Minute),
		WithTaskStuckThreshold(15*time.Minute),
	)
	wd.CheckOnce()

	_ = svc.Query(func(s *domain.CollabState) error {
		// Worker is offline post-restart (correct), but the in-progress
		// task should NOT be reset on the very first tick — give workers
		// time to reconnect.
		if s.Tasks[0].Status != "in_progress" {
			t.Errorf("task status = %q, want in_progress (no reset on first post-restart tick)", s.Tasks[0].Status)
		}
		// Driver must still be present and alive.
		drv := s.AgentInstances["cursor"]
		if drv == nil {
			t.Fatal("driver removed post-restart")
		}
		if time.Since(drv.LastHeartbeat) > time.Minute {
			t.Errorf("driver LastHeartbeat not refreshed by RefreshHeartbeatsOnStartup")
		}
		return nil
	})
}

// TestWatchdog_RespectsRespawnGrace covers Fix C-grace: when an instance was
// just spawned (LastSpawnedAt within respawnGrace) but has not yet emitted
// its first heartbeat, the watchdog must NOT mark it offline. Without this,
// the orchestrator/watchdog interaction can ping-pong: spawn → mark offline
// → kill task assignment → spawn again, and so on.
func TestWatchdog_RespectsRespawnGrace(t *testing.T) {
	state := domain.NewCollabState()
	now := time.Now()
	staleHB := now.Add(-30 * time.Minute)
	freshSpawn := now.Add(-10 * time.Second)

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver,
		Status: "idle", LastHeartbeat: now,
	}
	state.AgentInstances["claude-code-1"] = &domain.AgentInstance{
		InstanceID: "claude-code-1", AgentType: "claude-code", Role: domain.RoleWorker,
		Status:        "idle",
		LastHeartbeat: staleHB,    // pre-respawn heartbeat is stale
		LastSpawnedAt: freshSpawn, // ...but we just respawned this instance
	}
	state.DriverID = "cursor"

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	wd := NewWatchdog(svc, registry, logger,
		WithHeartbeatThreshold(5*time.Minute),
		WithRespawnGrace(60*time.Second),
	)
	wd.CheckOnce()

	_ = svc.Query(func(s *domain.CollabState) error {
		inst := s.AgentInstances["claude-code-1"]
		if inst == nil {
			t.Fatal("freshly-spawned instance unexpectedly removed")
		}
		if inst.Status == "offline" {
			t.Errorf("watchdog flipped freshly-spawned instance to offline despite LastSpawnedAt %s ago (within %s grace)",
				now.Sub(freshSpawn).Round(time.Second), 60*time.Second)
		}
		return nil
	})
}

// TestWatchdog_RespawnGraceExpires verifies the inverse: once the grace
// window has elapsed and the heartbeat is still stale, the instance IS
// flipped to offline. We are gating the watchdog, not disabling it.
func TestWatchdog_RespawnGraceExpires(t *testing.T) {
	state := domain.NewCollabState()
	now := time.Now()
	staleHB := now.Add(-30 * time.Minute)
	oldSpawn := now.Add(-10 * time.Minute) // far past any reasonable grace

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver,
		Status: "idle", LastHeartbeat: now,
	}
	state.AgentInstances["claude-code-1"] = &domain.AgentInstance{
		InstanceID: "claude-code-1", AgentType: "claude-code", Role: domain.RoleWorker,
		Status:        "idle",
		LastHeartbeat: staleHB,
		LastSpawnedAt: oldSpawn,
	}
	state.DriverID = "cursor"

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	wd := NewWatchdog(svc, registry, logger,
		WithHeartbeatThreshold(5*time.Minute),
		WithRespawnGrace(60*time.Second),
	)
	wd.CheckOnce()

	_ = svc.Query(func(s *domain.CollabState) error {
		inst := s.AgentInstances["claude-code-1"]
		if inst == nil {
			t.Fatal("instance unexpectedly removed")
		}
		if inst.Status != "offline" {
			t.Errorf("expected offline once grace window expired and heartbeat is stale, got %q", inst.Status)
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

// fakeSpawnDriver is a SpawnDriver double for Fix D.2 tests. It records every
// SpawnForTask call (so tests can assert which tasks were re-driven) and lets
// each test stub IsSpawnQueued without setting up a full WorkerManager.
type fakeSpawnDriver struct {
	queued      map[int]bool
	spawned     []int
	drainCalls  int
	drainResult int // value DrainAllQueues returns; 0 by default
}

func (f *fakeSpawnDriver) SpawnForTask(taskID int, _ string) {
	f.spawned = append(f.spawned, taskID)
}

func (f *fakeSpawnDriver) IsSpawnQueued(taskID int) bool { return f.queued[taskID] }

func (f *fakeSpawnDriver) DrainAllQueues() int {
	f.drainCalls++
	return f.drainResult
}

// newFakeSpawnDriver returns a driver that pre-marks the given task IDs as
// already-queued (so the watchdog will skip them).
func newFakeSpawnDriver(alreadyQueued ...int) *fakeSpawnDriver {
	q := make(map[int]bool, len(alreadyQueued))
	for _, id := range alreadyQueued {
		q[id] = true
	}
	return &fakeSpawnDriver{queued: q}
}

// TestWatchdog_DriveSpawnsRevivesOrphanPendingTask covers Fix D.2: a pending
// task with a configured AssignedTo, no live owner, and no active spawn must
// trigger a SpawnForTask call from the watchdog. This is the scenario where
// the user explicitly hit a stuck queue (legacy task or the daemon crashed
// between create_task and SpawnForTask).
func TestWatchdog_DriveSpawnsRevivesOrphanPendingTask(t *testing.T) {
	state := domain.NewCollabState()
	now := time.Now()
	staleEnough := now.Add(-2 * time.Minute) // older than the 30s sweep grace

	state.DriverID = "cursor"
	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver,
		Status: "idle", LastHeartbeat: now,
	}
	state.Tasks = append(state.Tasks, domain.Task{
		ID:         42,
		Title:      "Orphan",
		Status:     "pending",
		AssignedTo: "claude-code",
		UpdatedAt:  staleEnough,
	})

	svc := testService(state)
	driver := newFakeSpawnDriver()
	wd := NewWatchdog(svc, NewSessionRegistry(), log.New(os.Stderr, "[test] ", 0),
		WithSpawnDriver(driver),
		WithSpawnSweepGrace(30*time.Second),
	)

	wd.CheckOnce()

	if len(driver.spawned) != 1 || driver.spawned[0] != 42 {
		t.Fatalf("expected spawn for task #42, got %v", driver.spawned)
	}
}

// TestWatchdog_DriveSpawnsRespectsSweepGrace ensures freshly-created tasks
// (younger than spawnSweepGrace) are NOT re-driven — that would race with
// the in-flight create_task → SpawnForTask path and could double-spawn.
func TestWatchdog_DriveSpawnsRespectsSweepGrace(t *testing.T) {
	state := domain.NewCollabState()
	now := time.Now()

	state.DriverID = "cursor"
	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver,
		Status: "idle", LastHeartbeat: now,
	}
	state.Tasks = append(state.Tasks, domain.Task{
		ID:         7,
		Title:      "Just-created",
		Status:     "pending",
		AssignedTo: "claude-code",
		UpdatedAt:  now.Add(-5 * time.Second), // way newer than sweep grace
	})

	svc := testService(state)
	driver := newFakeSpawnDriver()
	wd := NewWatchdog(svc, NewSessionRegistry(), log.New(os.Stderr, "[test] ", 0),
		WithSpawnDriver(driver),
		WithSpawnSweepGrace(30*time.Second),
	)

	wd.CheckOnce()

	if len(driver.spawned) != 0 {
		t.Errorf("watchdog must not re-drive young tasks (would race the immediate path), spawned=%v", driver.spawned)
	}
}

// TestWatchdog_DriveSpawnsSkipsAlreadyQueued — IsSpawnQueued says the worker
// manager already has this task in its pendingSpawns or running queue, so
// the watchdog stays out of the way.
func TestWatchdog_DriveSpawnsSkipsAlreadyQueued(t *testing.T) {
	state := domain.NewCollabState()
	now := time.Now()
	staleEnough := now.Add(-2 * time.Minute)

	state.DriverID = "cursor"
	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver,
		Status: "idle", LastHeartbeat: now,
	}
	state.Tasks = append(state.Tasks, domain.Task{
		ID:         13,
		Title:      "Already queued",
		Status:     "pending",
		AssignedTo: "claude-code",
		UpdatedAt:  staleEnough,
	})

	svc := testService(state)
	driver := newFakeSpawnDriver(13) // pre-mark as queued
	wd := NewWatchdog(svc, NewSessionRegistry(), log.New(os.Stderr, "[test] ", 0),
		WithSpawnDriver(driver),
		WithSpawnSweepGrace(30*time.Second),
	)

	wd.CheckOnce()

	if len(driver.spawned) != 0 {
		t.Errorf("watchdog must not double-spawn when IsSpawnQueued reports queued, spawned=%v", driver.spawned)
	}
}

// TestWatchdog_DriveSpawnsSkipsLiveOwner — when an idle pool worker already
// owns the task via CurrentTasks AND is alive, no spawn is needed.
func TestWatchdog_DriveSpawnsSkipsLiveOwner(t *testing.T) {
	state := domain.NewCollabState()
	now := time.Now()
	staleEnough := now.Add(-2 * time.Minute)

	state.DriverID = "cursor"
	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver,
		Status: "idle", LastHeartbeat: now,
	}
	state.AgentInstances["claude-code-1"] = &domain.AgentInstance{
		InstanceID: "claude-code-1", AgentType: "claude-code", Role: domain.RoleWorker,
		Status: "busy", LastHeartbeat: now, CurrentTasks: []int{99},
	}
	state.Tasks = append(state.Tasks, domain.Task{
		ID:         99,
		Title:      "Owned + alive",
		Status:     "pending", // still pending in state, but a live owner has it
		AssignedTo: "claude-code",
		UpdatedAt:  staleEnough,
	})

	svc := testService(state)
	driver := newFakeSpawnDriver()
	wd := NewWatchdog(svc, NewSessionRegistry(), log.New(os.Stderr, "[test] ", 0),
		WithSpawnDriver(driver),
		WithSpawnSweepGrace(30*time.Second),
	)

	wd.CheckOnce()

	if len(driver.spawned) != 0 {
		t.Errorf("watchdog must not spawn when a live owner already has the task, spawned=%v", driver.spawned)
	}
}

// TestWatchdog_DriveSpawnsSkipsAnyAndUnassigned — Fix A's fallback should set
// task.AssignedTo to a concrete type. If it didn't (no provider wired, or
// the task is older than Fix A), the watchdog has no concrete type to drive
// and must not invent one. Same for "any".
func TestWatchdog_DriveSpawnsSkipsAnyAndUnassigned(t *testing.T) {
	state := domain.NewCollabState()
	now := time.Now()
	staleEnough := now.Add(-2 * time.Minute)

	state.DriverID = "cursor"
	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver,
		Status: "idle", LastHeartbeat: now,
	}
	state.Tasks = append(state.Tasks,
		domain.Task{ID: 1, Title: "Empty assignee", Status: "pending", AssignedTo: "", UpdatedAt: staleEnough},
		domain.Task{ID: 2, Title: "Any assignee", Status: "pending", AssignedTo: "any", UpdatedAt: staleEnough},
	)

	svc := testService(state)
	driver := newFakeSpawnDriver()
	wd := NewWatchdog(svc, NewSessionRegistry(), log.New(os.Stderr, "[test] ", 0),
		WithSpawnDriver(driver),
		WithSpawnSweepGrace(30*time.Second),
	)

	wd.CheckOnce()

	if len(driver.spawned) != 0 {
		t.Errorf("watchdog must skip empty/any AssignedTo (Fix A's job), spawned=%v", driver.spawned)
	}
}

// TestWatchdog_DriveSpawnsDisabled confirms the safety-net is opt-in. With
// no SpawnDriver wired, the watchdog must not crash and must not log spawn
// activity — preserves the legacy behavior for callers that haven't
// adopted Fix D.2.
func TestWatchdog_DriveSpawnsDisabled(t *testing.T) {
	state := domain.NewCollabState()
	now := time.Now()

	state.DriverID = "cursor"
	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver,
		Status: "idle", LastHeartbeat: now,
	}
	state.Tasks = append(state.Tasks, domain.Task{
		ID: 1, Title: "Orphan", Status: "pending", AssignedTo: "claude-code",
		UpdatedAt: now.Add(-2 * time.Minute),
	})

	svc := testService(state)
	wd := NewWatchdog(svc, NewSessionRegistry(), log.New(os.Stderr, "[test] ", 0),
		WithSpawnSweepGrace(30*time.Second), // grace is set, but no driver wired
	)

	// No panic and no error is the success criteria; driveSpawns short-circuits.
	wd.CheckOnce()
}

// TestWatchdog_DriveSpawnsDisabledByZeroGrace — even with a SpawnDriver,
// setting spawnSweepGrace<=0 disables the sweep entirely. Lets operators
// turn the safety net off in production while keeping the wiring intact.
func TestWatchdog_DriveSpawnsDisabledByZeroGrace(t *testing.T) {
	state := domain.NewCollabState()
	now := time.Now()

	state.DriverID = "cursor"
	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver,
		Status: "idle", LastHeartbeat: now,
	}
	state.Tasks = append(state.Tasks, domain.Task{
		ID: 1, Title: "Orphan", Status: "pending", AssignedTo: "claude-code",
		UpdatedAt: now.Add(-2 * time.Minute),
	})

	svc := testService(state)
	driver := newFakeSpawnDriver()
	wd := NewWatchdog(svc, NewSessionRegistry(), log.New(os.Stderr, "[test] ", 0),
		WithSpawnDriver(driver),
		WithSpawnSweepGrace(0), // explicit disable
	)

	wd.CheckOnce()

	if len(driver.spawned) != 0 {
		t.Errorf("zero spawnSweepGrace must disable driveSpawns, spawned=%v", driver.spawned)
	}
}

// TestWatchdog_SuppressesFailureIncrementWithinReconcileWindow guards the
// goroutine race described at watchdog.go:reconcileSuppressionWindow. When
// reconcileAfterExit just stamped LastReconciledAt within the suppression
// window, the watchdog must NOT bump FailureCount on top of the
// reconciler's own recovery — that would double-count one incident toward
// the DLQ threshold.
func TestWatchdog_SuppressesFailureIncrementWithinReconcileWindow(t *testing.T) {
	state := domain.NewCollabState()
	now := time.Now()
	staleTime := now.Add(-15 * time.Minute)

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver,
		Status: "idle", LastHeartbeat: now,
	}
	state.AgentInstances["claude-code"] = &domain.AgentInstance{
		InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker,
		Status: "busy", CurrentTasks: []int{1}, LastHeartbeat: staleTime,
	}
	state.DriverID = "cursor"

	state.Tasks = append(state.Tasks, domain.Task{
		ID: 1, Title: "Just reconciled", Status: "in_progress",
		AssignedTo: "claude-code", UpdatedAt: staleTime,
		FailureCount: 0,
		// Within the (5s) suppression window — reconciler just ran.
		LastReconciledAt: now.Add(-1 * time.Second),
	})
	state.NextTaskID = 2

	svc := testService(state)
	wd := NewWatchdog(svc, NewSessionRegistry(), log.New(os.Stderr, "[test] ", 0),
		WithHeartbeatThreshold(1*time.Minute),
		WithMaxTaskFailures(3),
	)
	wd.CheckOnce()

	_ = svc.Query(func(s *domain.CollabState) error {
		task := s.Tasks[0]
		if task.FailureCount != 0 {
			t.Errorf("FailureCount = %d, want 0 (must NOT bump within suppression window)", task.FailureCount)
		}
		if task.Status != "in_progress" {
			t.Errorf("Status = %q, want in_progress (suppression skips the recovery branch)", task.Status)
		}
		// One breadcrumb event recorded so operators can see the suppression.
		if len(task.RecoveryEvents) != 1 {
			t.Fatalf("RecoveryEvents len = %d, want 1 suppression breadcrumb", len(task.RecoveryEvents))
		}
		ev := task.RecoveryEvents[0]
		if ev.Source != RecoverySourceWatchdog || ev.Reason != RecoveryReasonSuppressedPostReconcile {
			t.Errorf("event = {Source: %q, Reason: %q}, want {watchdog, suppressed_post_reconcile}", ev.Source, ev.Reason)
		}
		return nil
	})
}

// TestWatchdog_IncrementsFailureCountAfterSuppressionWindow — outside the
// suppression window the watchdog bumps normally. Critical regression
// guard: if the suppression window were measured in heartbeat-staleness
// units (minutes), a legitimate failure of a SUBSEQUENT spawn attempt
// would be silently swallowed. This test pins the window to its small,
// race-only size.
func TestWatchdog_IncrementsFailureCountAfterSuppressionWindow(t *testing.T) {
	state := domain.NewCollabState()
	now := time.Now()
	staleTime := now.Add(-15 * time.Minute)

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver,
		Status: "idle", LastHeartbeat: now,
	}
	state.AgentInstances["claude-code"] = &domain.AgentInstance{
		InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker,
		Status: "busy", CurrentTasks: []int{1}, LastHeartbeat: staleTime,
	}
	state.DriverID = "cursor"

	state.Tasks = append(state.Tasks, domain.Task{
		ID: 1, Title: "Reconcile too long ago", Status: "in_progress",
		AssignedTo: "claude-code", UpdatedAt: staleTime,
		FailureCount: 0,
		// Well outside the (5s) suppression window — different incident.
		LastReconciledAt: now.Add(-1 * time.Hour),
	})
	state.NextTaskID = 2

	svc := testService(state)
	wd := NewWatchdog(svc, NewSessionRegistry(), log.New(os.Stderr, "[test] ", 0),
		WithHeartbeatThreshold(1*time.Minute),
		WithMaxTaskFailures(3),
	)
	wd.CheckOnce()

	_ = svc.Query(func(s *domain.CollabState) error {
		task := s.Tasks[0]
		if task.FailureCount != 1 {
			t.Errorf("FailureCount = %d, want 1 (legitimate watchdog catch outside suppression window)", task.FailureCount)
		}
		if task.Status != "pending" {
			t.Errorf("Status = %q, want pending", task.Status)
		}
		return nil
	})
}

// TestWatchdog_NoSuppressionWhenLastReconciledAtZero — a task that was
// never touched by the reconciler must increment normally on a real
// failure. Regression guard: an `if !LastReconciledAt.IsZero()` check
// without the IsZero clause would silently suppress every first-time
// failure (since zero-time is "before any window").
func TestWatchdog_NoSuppressionWhenLastReconciledAtZero(t *testing.T) {
	state := domain.NewCollabState()
	now := time.Now()
	staleTime := now.Add(-15 * time.Minute)

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver,
		Status: "idle", LastHeartbeat: now,
	}
	state.AgentInstances["claude-code"] = &domain.AgentInstance{
		InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker,
		Status: "busy", CurrentTasks: []int{1}, LastHeartbeat: staleTime,
	}
	state.DriverID = "cursor"

	state.Tasks = append(state.Tasks, domain.Task{
		ID: 1, Title: "Never reconciled", Status: "in_progress",
		AssignedTo: "claude-code", UpdatedAt: staleTime,
		FailureCount: 0,
		// LastReconciledAt left zero — task has never been touched by reconciler.
	})
	state.NextTaskID = 2

	svc := testService(state)
	wd := NewWatchdog(svc, NewSessionRegistry(), log.New(os.Stderr, "[test] ", 0),
		WithHeartbeatThreshold(1*time.Minute),
		WithMaxTaskFailures(3),
	)
	wd.CheckOnce()

	_ = svc.Query(func(s *domain.CollabState) error {
		task := s.Tasks[0]
		if task.FailureCount != 1 {
			t.Errorf("FailureCount = %d, want 1 (zero LastReconciledAt must NOT suppress)", task.FailureCount)
		}
		return nil
	})
}

// TestWatchdog_DrainAllQueuesEachCycle — every watchdog tick must call
// SpawnDriver.DrainAllQueues so queued task spawns get re-evaluated even
// when the event-driven drain (the trailing call inside spawn goroutines)
// missed them. This is the key safety net behind the spawn-starvation
// fix: without it, a task enqueued during a transient backoff or while
// pool slots were saturated by message-driven spawns would sit in
// pendingSpawns until the next external SpawnForTask call.
func TestWatchdog_DrainAllQueuesEachCycle(t *testing.T) {
	state := domain.NewCollabState()
	now := time.Now()
	state.DriverID = "cursor"
	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver,
		Status: "idle", LastHeartbeat: now,
	}

	svc := testService(state)
	driver := newFakeSpawnDriver()
	driver.drainResult = 2 // pretend two types had work to drain
	wd := NewWatchdog(svc, NewSessionRegistry(), log.New(os.Stderr, "[test] ", 0),
		WithSpawnDriver(driver),
		WithSpawnSweepGrace(30*time.Second),
	)

	wd.CheckOnce()

	if driver.drainCalls != 1 {
		t.Errorf("DrainAllQueues called %d times in one cycle, want 1", driver.drainCalls)
	}
	if len(driver.spawned) != 0 {
		t.Errorf("no orphan tasks present, expected zero SpawnForTask calls, got %v", driver.spawned)
	}
}

// TestWatchdog_DrainAllQueuesNoOpWhenSpawnDriverNil — a watchdog wired
// without a SpawnDriver (legacy / single-process tests) must not panic
// and must still complete a normal cycle.
func TestWatchdog_DrainAllQueuesNoOpWhenSpawnDriverNil(t *testing.T) {
	state := domain.NewCollabState()
	state.DriverID = "cursor"
	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver,
		Status: "idle", LastHeartbeat: time.Now(),
	}

	svc := testService(state)
	wd := NewWatchdog(svc, NewSessionRegistry(), log.New(os.Stderr, "[test] ", 0))

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("watchdog cycle with nil SpawnDriver panicked: %v", r)
		}
	}()
	wd.CheckOnce()
}
