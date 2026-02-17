package app

import (
	"context"
	"log"
	"os"
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

// testService returns a CollabService backed by an in-memory repo.
func testService(state *domain.CollabState) *CollabService {
	repo := &notifierTestRepo{state: state}
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

// mockTriggerable records Trigger calls.
type mockTriggerable struct {
	fn func()
}

func (m *mockTriggerable) Trigger() {
	if m.fn != nil {
		m.fn()
	}
}
