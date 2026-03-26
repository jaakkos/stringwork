package app

import (
	"log"
	"os"
	"testing"
	"time"

	"github.com/jaakkos/stringwork/internal/domain"
)

func TestWatchdog_ProgressWarning(t *testing.T) {
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
		LastHeartbeat: now, // heartbeat is fresh
	}
	state.DriverID = "cursor"

	// Task has been in_progress for 4 minutes with no progress report
	state.Tasks = append(state.Tasks, domain.Task{
		ID:         1,
		Title:      "Long running task",
		Status:     "in_progress",
		AssignedTo: "claude-code",
		UpdatedAt:  now.Add(-4 * time.Minute),
	})
	state.NextTaskID = 2
	state.NextMsgID = 1

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	wd := NewWatchdog(svc, registry, logger,
		WithProgressWarningThreshold(3*time.Minute),
		WithProgressCriticalThreshold(5*time.Minute),
	)

	wd.CheckOnce()

	// Should have a warning message to the driver
	_ = svc.Query(func(s *domain.CollabState) error {
		found := false
		for _, msg := range s.Messages {
			if msg.From == "system" && msg.To == "cursor" {
				if contains(msg.Content, "Warning") {
					found = true
				}
			}
		}
		if !found {
			t.Error("expected warning message to driver about no progress")
		}
		return nil
	})
}

func TestWatchdog_ProgressCritical(t *testing.T) {
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

	// Task has been in_progress for 6 minutes with no progress report
	state.Tasks = append(state.Tasks, domain.Task{
		ID:         1,
		Title:      "Very long running task",
		Status:     "in_progress",
		AssignedTo: "claude-code",
		UpdatedAt:  now.Add(-6 * time.Minute),
	})
	state.NextTaskID = 2
	state.NextMsgID = 1

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	wd := NewWatchdog(svc, registry, logger,
		WithProgressWarningThreshold(3*time.Minute),
		WithProgressCriticalThreshold(5*time.Minute),
	)

	wd.CheckOnce()

	_ = svc.Query(func(s *domain.CollabState) error {
		foundCritical := false
		for _, msg := range s.Messages {
			if msg.From == "system" && msg.To == "cursor" {
				// Without a ProcessActivityProvider, the watchdog classifies
				// as workerNoProcess and sends a "No process" critical message.
				if contains(msg.Content, "No process") || contains(msg.Content, "Critical") || contains(msg.Content, "Stuck") {
					foundCritical = true
				}
			}
		}
		if !foundCritical {
			t.Error("expected critical alert to driver about no progress")
		}
		return nil
	})
}

func TestWatchdog_RecentProgressNoAlert(t *testing.T) {
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

	// Task has been in_progress for 10 minutes but has a recent progress report
	state.Tasks = append(state.Tasks, domain.Task{
		ID:              1,
		Title:           "Active task",
		Status:          "in_progress",
		AssignedTo:      "claude-code",
		UpdatedAt:       now.Add(-10 * time.Minute),
		LastProgressAt:  now.Add(-1 * time.Minute), // recent progress
		ProgressPercent: 75,
	})
	state.NextTaskID = 2
	state.NextMsgID = 1

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	wd := NewWatchdog(svc, registry, logger,
		WithProgressWarningThreshold(3*time.Minute),
		WithProgressCriticalThreshold(5*time.Minute),
	)

	wd.CheckOnce()

	_ = svc.Query(func(s *domain.CollabState) error {
		for _, msg := range s.Messages {
			if msg.From == "system" && (contains(msg.Content, "Warning") || contains(msg.Content, "Critical")) {
				t.Error("expected no alert when recent progress reported")
			}
		}
		return nil
	})
}

func TestWatchdog_SLAExceeded(t *testing.T) {
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

	// Task has SLA of 5 minutes, but has been running for 8 minutes
	state.Tasks = append(state.Tasks, domain.Task{
		ID:                  1,
		Title:               "Time-boxed task",
		Status:              "in_progress",
		AssignedTo:          "claude-code",
		UpdatedAt:           now.Add(-8 * time.Minute),
		LastProgressAt:      now.Add(-1 * time.Minute), // recent progress (so no stale alert)
		ExpectedDurationSec: 300,                       // 5 minutes SLA
	})
	state.NextTaskID = 2
	state.NextMsgID = 1

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	wd := NewWatchdog(svc, registry, logger)
	wd.CheckOnce()

	_ = svc.Query(func(s *domain.CollabState) error {
		foundSLA := false
		for _, msg := range s.Messages {
			if msg.From == "system" && msg.To == "cursor" {
				if contains(msg.Content, "SLA exceeded") {
					foundSLA = true
				}
			}
		}
		if !foundSLA {
			t.Error("expected SLA exceeded alert to driver")
		}
		return nil
	})
}

func TestWatchdog_SLANotExceeded(t *testing.T) {
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

	// Task has SLA of 10 minutes, only been running for 3 minutes
	state.Tasks = append(state.Tasks, domain.Task{
		ID:                  1,
		Title:               "On-track task",
		Status:              "in_progress",
		AssignedTo:          "claude-code",
		UpdatedAt:           now.Add(-3 * time.Minute),
		LastProgressAt:      now.Add(-1 * time.Minute),
		ExpectedDurationSec: 600, // 10 minutes SLA
	})
	state.NextTaskID = 2
	state.NextMsgID = 1

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	wd := NewWatchdog(svc, registry, logger)
	wd.CheckOnce()

	_ = svc.Query(func(s *domain.CollabState) error {
		for _, msg := range s.Messages {
			if msg.From == "system" && contains(msg.Content, "SLA") {
				t.Error("expected no SLA alert when within expected duration")
			}
		}
		return nil
	})
}

func TestWatchdog_AlertDeduplication(t *testing.T) {
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
		Title:      "No progress task",
		Status:     "in_progress",
		AssignedTo: "claude-code",
		UpdatedAt:  now.Add(-4 * time.Minute),
	})
	state.NextTaskID = 2
	state.NextMsgID = 1

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	wd := NewWatchdog(svc, registry, logger,
		WithProgressWarningThreshold(3*time.Minute),
		WithProgressCriticalThreshold(5*time.Minute),
		WithHeartbeatThreshold(20*time.Minute),
		WithTaskStuckThreshold(20*time.Minute),
	)

	// Run twice
	wd.CheckOnce()
	wd.CheckOnce()

	// Should only have one warning message (deduplicated)
	_ = svc.Query(func(s *domain.CollabState) error {
		count := 0
		for _, msg := range s.Messages {
			if msg.From == "system" && contains(msg.Content, "Warning") {
				count++
			}
		}
		if count != 1 {
			t.Errorf("expected exactly 1 warning message (dedup), got %d", count)
		}
		return nil
	})
}

func TestWatchdog_CompletedTaskClearsAlerts(t *testing.T) {
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
		Title:      "Eventually completed",
		Status:     "in_progress",
		AssignedTo: "claude-code",
		UpdatedAt:  now.Add(-4 * time.Minute),
	})
	state.NextTaskID = 2
	state.NextMsgID = 1

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	wd := NewWatchdog(svc, registry, logger,
		WithProgressWarningThreshold(3*time.Minute),
		WithProgressCriticalThreshold(5*time.Minute),
		WithHeartbeatThreshold(20*time.Minute),
		WithTaskStuckThreshold(20*time.Minute),
	)

	// First check triggers warning
	wd.CheckOnce()

	// Complete the task
	_ = svc.Run(func(s *domain.CollabState) error {
		s.Tasks[0].Status = "completed"
		return nil
	})

	// Second check should clear the alert tracking
	wd.CheckOnce()

	// Re-create a new in_progress task with same ID
	_ = svc.Run(func(s *domain.CollabState) error {
		s.Tasks = append(s.Tasks, domain.Task{
			ID:         2,
			Title:      "New task, no progress yet",
			Status:     "in_progress",
			AssignedTo: "claude-code",
			UpdatedAt:  now.Add(-4 * time.Minute),
		})
		s.NextTaskID = 3
		return nil
	})

	// CheckOnce for the new task
	wd.CheckOnce()

	// Should get a new warning for task 2
	_ = svc.Query(func(s *domain.CollabState) error {
		found := false
		for _, msg := range s.Messages {
			if msg.From == "system" && contains(msg.Content, "#2") && contains(msg.Content, "Warning") {
				found = true
			}
		}
		if !found {
			t.Error("expected warning for new task #2")
		}
		return nil
	})
}

func TestTaskProgressFields_Persistence(t *testing.T) {
	state := domain.NewCollabState()
	now := time.Now()

	task := domain.Task{
		ID:                  1,
		Title:               "Test task",
		Status:              "in_progress",
		AssignedTo:          "claude-code",
		CreatedBy:           "cursor",
		CreatedAt:           now,
		UpdatedAt:           now,
		ExpectedDurationSec: 300,
		ProgressDescription: "Working on tests",
		ProgressPercent:     50,
		LastProgressAt:      now,
	}
	state.Tasks = append(state.Tasks, task)
	state.NextTaskID = 2

	if task.ExpectedDurationSec != 300 {
		t.Errorf("expected_duration_sec = %d, want 300", task.ExpectedDurationSec)
	}
	if task.ProgressPercent != 50 {
		t.Errorf("progress_percent = %d, want 50", task.ProgressPercent)
	}
	if task.ProgressDescription != "Working on tests" {
		t.Errorf("progress_description = %q, want 'Working on tests'", task.ProgressDescription)
	}
}

func TestAgentProgressFields(t *testing.T) {
	ai := &domain.AgentInstance{
		InstanceID:         "claude-code-1",
		AgentType:          "claude-code",
		Role:               domain.RoleWorker,
		Status:             "busy",
		LastHeartbeat:      time.Now(),
		Progress:           "Implementing auth middleware",
		ProgressStep:       2,
		ProgressTotalSteps: 5,
		ProgressUpdatedAt:  time.Now(),
	}

	if ai.Progress != "Implementing auth middleware" {
		t.Errorf("progress = %q", ai.Progress)
	}
	if ai.ProgressStep != 2 {
		t.Errorf("progress_step = %d, want 2", ai.ProgressStep)
	}
	if ai.ProgressTotalSteps != 5 {
		t.Errorf("progress_total_steps = %d, want 5", ai.ProgressTotalSteps)
	}
}

func TestProcessInfo(t *testing.T) {
	now := time.Now()
	pi := ProcessInfo{
		InstanceID:   "claude-code-1",
		StartedAt:    now.Add(-5 * time.Minute),
		LastOutputAt: now.Add(-30 * time.Second),
		OutputBytes:  1024,
		WorkspaceDir: "/tmp/test",
	}

	if pi.InstanceID != "claude-code-1" {
		t.Errorf("instance_id = %q", pi.InstanceID)
	}
	if pi.OutputBytes != 1024 {
		t.Errorf("output_bytes = %d, want 1024", pi.OutputBytes)
	}
}

func TestWatchdog_WarningNoProcess(t *testing.T) {
	state := domain.NewCollabState()
	now := time.Now()

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID: "cursor", AgentType: "cursor",
		Role: domain.RoleDriver, Status: "idle", LastHeartbeat: now,
	}
	state.AgentInstances["codex-task-5"] = &domain.AgentInstance{
		InstanceID: "codex-task-5", AgentType: "codex",
		Role: domain.RoleWorker, Status: "busy",
		CurrentTasks: []int{5}, LastHeartbeat: now,
	}
	state.DriverID = "cursor"
	state.Tasks = append(state.Tasks, domain.Task{
		ID: 5, Title: "Review code", Status: "in_progress",
		AssignedTo: "codex-task-5",
		UpdatedAt:  now.Add(-4 * time.Minute),
	})
	state.NextTaskID = 6
	state.NextMsgID = 1

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	// No ProcessActivityProvider — should fall back to workerNoProcess
	wd := NewWatchdog(svc, registry, logger,
		WithProgressWarningThreshold(3*time.Minute),
		WithProgressCriticalThreshold(5*time.Minute),
	)
	wd.CheckOnce()

	_ = svc.Query(func(s *domain.CollabState) error {
		for _, msg := range s.Messages {
			if msg.From == "system" && msg.To == "cursor" && contains(msg.Content, "Warning") {
				if !contains(msg.Content, "no running process") {
					t.Error("warning should mention no running process")
				}
				if !contains(msg.Content, "worker_output") {
					t.Error("warning should include worker_output hint")
				}
				return nil
			}
		}
		t.Error("expected a warning message")
		return nil
	})
}

func TestWatchdog_CriticalNoProcess(t *testing.T) {
	state := domain.NewCollabState()
	now := time.Now()

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID: "cursor", AgentType: "cursor",
		Role: domain.RoleDriver, Status: "idle", LastHeartbeat: now,
	}
	state.AgentInstances["codex-task-7"] = &domain.AgentInstance{
		InstanceID: "codex-task-7", AgentType: "codex",
		Role: domain.RoleWorker, Status: "busy",
		CurrentTasks: []int{7}, LastHeartbeat: now,
	}
	state.DriverID = "cursor"
	state.Tasks = append(state.Tasks, domain.Task{
		ID: 7, Title: "Long task", Status: "in_progress",
		AssignedTo: "codex-task-7",
		UpdatedAt:  now.Add(-6 * time.Minute),
	})
	state.NextTaskID = 8
	state.NextMsgID = 1

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	wd := NewWatchdog(svc, registry, logger,
		WithProgressWarningThreshold(3*time.Minute),
		WithProgressCriticalThreshold(5*time.Minute),
	)
	wd.CheckOnce()

	_ = svc.Query(func(s *domain.CollabState) error {
		for _, msg := range s.Messages {
			if msg.From == "system" && msg.To == "cursor" && contains(msg.Content, "No process") {
				if !contains(msg.Content, "worker_output") {
					t.Error("no-process critical should include worker_output hint")
				}
				return nil
			}
		}
		t.Error("expected a No process critical message")
		return nil
	})
}

// mockProcessActivity implements ProcessActivityProvider for testing.
type mockProcessActivity struct {
	procs  map[string]ProcessInfo
	output map[string]string
}

func (m *mockProcessActivity) GetProcessInfo() map[string]ProcessInfo {
	if m.procs == nil {
		return map[string]ProcessInfo{}
	}
	return m.procs
}

func (m *mockProcessActivity) GetRecentOutput(instanceID string) string {
	if m.output == nil {
		return ""
	}
	return m.output[instanceID]
}

func TestWatchdog_ActiveWorkerSuppressesWarning(t *testing.T) {
	state := domain.NewCollabState()
	now := time.Now()

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID: "cursor", AgentType: "cursor",
		Role: domain.RoleDriver, Status: "idle", LastHeartbeat: now,
	}
	state.AgentInstances["codex-task-3"] = &domain.AgentInstance{
		InstanceID: "codex-task-3", AgentType: "codex",
		Role: domain.RoleWorker, Status: "busy",
		CurrentTasks: []int{3}, LastHeartbeat: now,
	}
	state.DriverID = "cursor"
	state.Tasks = append(state.Tasks, domain.Task{
		ID: 3, Title: "Active task", Status: "in_progress",
		AssignedTo: "codex-task-3",
		UpdatedAt:  now.Add(-4 * time.Minute),
	})
	state.NextTaskID = 4
	state.NextMsgID = 1

	mock := &mockProcessActivity{
		procs: map[string]ProcessInfo{
			"codex-task-3": {
				InstanceID:   "codex-task-3",
				StartedAt:    now.Add(-5 * time.Minute),
				LastOutputAt: now.Add(-10 * time.Second), // recent output
				OutputBytes:  50000,
			},
		},
	}

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	wd := NewWatchdog(svc, registry, logger,
		WithProgressWarningThreshold(3*time.Minute),
		WithProgressCriticalThreshold(5*time.Minute),
		WithProcessActivity(mock),
	)
	wd.CheckOnce()

	_ = svc.Query(func(s *domain.CollabState) error {
		for _, msg := range s.Messages {
			if msg.From == "system" && msg.To == "cursor" {
				if contains(msg.Content, "Warning") || contains(msg.Content, "Stuck") {
					t.Errorf("expected NO warning for active worker, got message: %s", msg.Content)
				}
			}
		}
		return nil
	})
}

func TestWatchdog_ActiveWorkerSoftCritical(t *testing.T) {
	state := domain.NewCollabState()
	now := time.Now()

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID: "cursor", AgentType: "cursor",
		Role: domain.RoleDriver, Status: "idle", LastHeartbeat: now,
	}
	state.AgentInstances["codex-task-4"] = &domain.AgentInstance{
		InstanceID: "codex-task-4", AgentType: "codex",
		Role: domain.RoleWorker, Status: "busy",
		CurrentTasks: []int{4}, LastHeartbeat: now,
	}
	state.DriverID = "cursor"
	state.Tasks = append(state.Tasks, domain.Task{
		ID: 4, Title: "Long active task", Status: "in_progress",
		AssignedTo: "codex-task-4",
		UpdatedAt:  now.Add(-6 * time.Minute),
	})
	state.NextTaskID = 5
	state.NextMsgID = 1

	mock := &mockProcessActivity{
		procs: map[string]ProcessInfo{
			"codex-task-4": {
				InstanceID:   "codex-task-4",
				StartedAt:    now.Add(-7 * time.Minute),
				LastOutputAt: now.Add(-5 * time.Second), // very recent
				OutputBytes:  120000,
			},
		},
	}

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	wd := NewWatchdog(svc, registry, logger,
		WithProgressWarningThreshold(3*time.Minute),
		WithProgressCriticalThreshold(5*time.Minute),
		WithProcessActivity(mock),
	)
	wd.CheckOnce()

	_ = svc.Query(func(s *domain.CollabState) error {
		for _, msg := range s.Messages {
			if msg.From == "system" && msg.To == "cursor" && contains(msg.Content, "Note") {
				if !contains(msg.Content, "actively producing output") {
					t.Error("active critical should mention actively producing output")
				}
				if !contains(msg.Content, "report_progress") {
					t.Error("active critical should suggest report_progress")
				}
				return nil
			}
		}
		t.Error("expected a soft Note message for active worker at critical threshold")
		return nil
	})
}

func TestWatchdog_SilentWorkerWarningIncludesSnippet(t *testing.T) {
	state := domain.NewCollabState()
	now := time.Now()

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID: "cursor", AgentType: "cursor",
		Role: domain.RoleDriver, Status: "idle", LastHeartbeat: now,
	}
	state.AgentInstances["codex-task-6"] = &domain.AgentInstance{
		InstanceID: "codex-task-6", AgentType: "codex",
		Role: domain.RoleWorker, Status: "busy",
		CurrentTasks: []int{6}, LastHeartbeat: now,
	}
	state.DriverID = "cursor"
	state.Tasks = append(state.Tasks, domain.Task{
		ID: 6, Title: "Silent task", Status: "in_progress",
		AssignedTo: "codex-task-6",
		UpdatedAt:  now.Add(-4 * time.Minute),
	})
	state.NextTaskID = 7
	state.NextMsgID = 1

	mock := &mockProcessActivity{
		procs: map[string]ProcessInfo{
			"codex-task-6": {
				InstanceID:   "codex-task-6",
				StartedAt:    now.Add(-5 * time.Minute),
				LastOutputAt: now.Add(-3 * time.Minute), // silent for 3 min
				OutputBytes:  5000,
			},
		},
		output: map[string]string{
			"codex-task-6": "Connecting to MCP server...\nWaiting for tool response...\n",
		},
	}

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	wd := NewWatchdog(svc, registry, logger,
		WithProgressWarningThreshold(3*time.Minute),
		WithProgressCriticalThreshold(5*time.Minute),
		WithProcessActivity(mock),
	)
	wd.CheckOnce()

	_ = svc.Query(func(s *domain.CollabState) error {
		for _, msg := range s.Messages {
			if msg.From == "system" && msg.To == "cursor" && contains(msg.Content, "Warning") {
				if !contains(msg.Content, "silent") {
					t.Error("silent warning should mention process is silent")
				}
				if !contains(msg.Content, "Waiting for tool response") {
					t.Errorf("silent warning should include output snippet, got: %s", msg.Content)
				}
				return nil
			}
		}
		t.Error("expected a warning message for silent worker")
		return nil
	})
}

func TestWatchdog_SilentWorkerCriticalIncludesSnippet(t *testing.T) {
	state := domain.NewCollabState()
	now := time.Now()

	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID: "cursor", AgentType: "cursor",
		Role: domain.RoleDriver, Status: "idle", LastHeartbeat: now,
	}
	state.AgentInstances["codex-task-8"] = &domain.AgentInstance{
		InstanceID: "codex-task-8", AgentType: "codex",
		Role: domain.RoleWorker, Status: "busy",
		CurrentTasks: []int{8}, LastHeartbeat: now,
	}
	state.DriverID = "cursor"
	state.Tasks = append(state.Tasks, domain.Task{
		ID: 8, Title: "Stuck task", Status: "in_progress",
		AssignedTo: "codex-task-8",
		UpdatedAt:  now.Add(-6 * time.Minute),
	})
	state.NextTaskID = 9
	state.NextMsgID = 1

	mock := &mockProcessActivity{
		procs: map[string]ProcessInfo{
			"codex-task-8": {
				InstanceID:   "codex-task-8",
				StartedAt:    now.Add(-7 * time.Minute),
				LastOutputAt: now.Add(-5 * time.Minute), // silent for 5 min
				OutputBytes:  2000,
			},
		},
		output: map[string]string{
			"codex-task-8": "Error: connection timeout\nRetrying...\n",
		},
	}

	svc := testService(state)
	registry := NewSessionRegistry()
	logger := log.New(os.Stderr, "[test] ", 0)

	wd := NewWatchdog(svc, registry, logger,
		WithProgressWarningThreshold(3*time.Minute),
		WithProgressCriticalThreshold(5*time.Minute),
		WithProcessActivity(mock),
	)
	wd.CheckOnce()

	_ = svc.Query(func(s *domain.CollabState) error {
		for _, msg := range s.Messages {
			if msg.From == "system" && msg.To == "cursor" && contains(msg.Content, "Stuck") {
				if !contains(msg.Content, "cancel_agent") {
					t.Error("stuck critical should suggest cancel_agent")
				}
				if !contains(msg.Content, "connection timeout") {
					t.Errorf("stuck critical should include output snippet, got: %s", msg.Content)
				}
				return nil
			}
		}
		t.Error("expected a Stuck critical message for silent worker")
		return nil
	})
}

// contains checks if s contains substr (for test assertions).
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
