package collab

import (
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/server"

	"github.com/jaakkos/stringwork/internal/app"
	"github.com/jaakkos/stringwork/internal/domain"
)

type mockCanceller struct {
	cancelled map[string]bool
	running   map[string]bool
	output    map[string]string
}

func newMockCanceller() *mockCanceller {
	return &mockCanceller{
		cancelled: make(map[string]bool),
		running:   make(map[string]bool),
		output:    make(map[string]string),
	}
}

func (m *mockCanceller) CancelWorker(instanceID string) bool {
	if m.running[instanceID] {
		m.cancelled[instanceID] = true
		return true
	}
	return false
}

func (m *mockCanceller) IsWorkerRunning(instanceID string) bool {
	return m.running[instanceID]
}

func (m *mockCanceller) GetRecentOutput(instanceID string) string {
	if m.output == nil {
		return ""
	}
	return m.output[instanceID]
}

func testServerWithCanceller(svc *app.CollabService, logger *log.Logger, c WorkerCanceller) *server.MCPServer {
	s := server.NewMCPServer("test", "1.0.0")
	registry := app.NewSessionRegistry()
	var opts []RegisterOption
	if c != nil {
		opts = append(opts, WithCanceller(c))
	}
	Register(s, svc, logger, registry, nil, opts...)
	return s
}

func TestCancelAgent_CancelsInProgressTasks(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	canceller := newMockCanceller()
	srv := testServerWithCanceller(svc, logger, canceller)

	// Setup: create an in_progress task for claude-code
	repo.state.Tasks = []domain.Task{
		{
			ID:         1,
			Title:      "Implement feature",
			Status:     "in_progress",
			AssignedTo: "claude-code",
			CreatedBy:  "cursor",
			CreatedAt:  time.Now().Add(-5 * time.Minute),
			UpdatedAt:  time.Now().Add(-5 * time.Minute),
		},
		{
			ID:         2,
			Title:      "Another task",
			Status:     "pending",
			AssignedTo: "claude-code",
			CreatedBy:  "cursor",
		},
	}
	repo.state.NextTaskID = 3

	result, err := callTool(t, srv, "cancel_agent", map[string]any{
		"agent":        "claude-code",
		"cancelled_by": "cursor",
		"reason":       "no longer needed",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !strings.Contains(text, "cancelled 1 task") {
		t.Errorf("expected cancelled task count, got: %s", text)
	}
	if !strings.Contains(text, "#1") {
		t.Errorf("expected task #1 in result, got: %s", text)
	}
	if !strings.Contains(text, "STOP message sent") {
		t.Errorf("expected STOP message mention, got: %s", text)
	}

	// Verify task was cancelled
	if repo.state.Tasks[0].Status != "cancelled" {
		t.Errorf("task #1 should be cancelled, got: %s", repo.state.Tasks[0].Status)
	}
	if !strings.Contains(repo.state.Tasks[0].ResultSummary, "Cancelled by cursor") {
		t.Errorf("unexpected result summary: %s", repo.state.Tasks[0].ResultSummary)
	}

	// Verify pending task was NOT cancelled
	if repo.state.Tasks[1].Status != "pending" {
		t.Errorf("task #2 should still be pending, got: %s", repo.state.Tasks[1].Status)
	}

	// Verify STOP message was sent
	found := false
	for _, msg := range repo.state.Messages {
		if msg.To == "claude-code" && strings.Contains(msg.Content, "STOP") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected STOP message to claude-code")
	}
}

func TestCancelAgent_KillsRunningProcess(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)
	canceller := newMockCanceller()
	canceller.running["claude-code"] = true
	srv := testServerWithCanceller(svc, logger, canceller)

	result, err := callTool(t, srv, "cancel_agent", map[string]any{
		"agent":        "claude-code",
		"cancelled_by": "cursor",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !strings.Contains(text, "worker process killed") {
		t.Errorf("expected process killed mention, got: %s", text)
	}
	if !canceller.cancelled["claude-code"] {
		t.Error("expected CancelWorker to be called")
	}
}

func TestCancelAgent_NoCancellerStillSoftCancels(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServerWithCanceller(svc, logger, nil) // no canceller

	repo.state.Tasks = []domain.Task{
		{
			ID:         1,
			Title:      "Task",
			Status:     "in_progress",
			AssignedTo: "claude-code",
			CreatedBy:  "cursor",
		},
	}
	repo.state.NextTaskID = 2

	result, err := callTool(t, srv, "cancel_agent", map[string]any{
		"agent":        "claude-code",
		"cancelled_by": "cursor",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if strings.Contains(text, "worker process killed") {
		t.Error("should NOT mention process killed when no canceller")
	}

	// Task should still be cancelled
	if repo.state.Tasks[0].Status != "cancelled" {
		t.Errorf("task should be cancelled, got: %s", repo.state.Tasks[0].Status)
	}
}

func TestCancelAgent_ClearsInstanceTasks(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServerWithCanceller(svc, logger, nil)

	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"cursor": {
			InstanceID: "cursor",
			AgentType:  "cursor",
			Role:       domain.RoleDriver,
			Status:     "idle",
		},
		"claude-code": {
			InstanceID:   "claude-code",
			AgentType:    "claude-code",
			Role:         domain.RoleWorker,
			Status:       "busy",
			CurrentTasks: []int{1, 2},
		},
	}
	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "T1", Status: "in_progress", AssignedTo: "claude-code", CreatedBy: "cursor"},
		{ID: 2, Title: "T2", Status: "in_progress", AssignedTo: "claude-code", CreatedBy: "cursor"},
	}
	repo.state.NextTaskID = 3

	_, err := callTool(t, srv, "cancel_agent", map[string]any{
		"agent":        "claude-code",
		"cancelled_by": "cursor",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Agent instance should be idle with no tasks
	inst := repo.state.AgentInstances["claude-code"]
	if inst.Status != "idle" {
		t.Errorf("expected idle, got: %s", inst.Status)
	}
	if len(inst.CurrentTasks) != 0 {
		t.Errorf("expected no current tasks, got: %v", inst.CurrentTasks)
	}
}

func TestCancelAgent_RequiresAgent(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServerWithCanceller(svc, logger, nil)

	_, err := callTool(t, srv, "cancel_agent", map[string]any{
		"cancelled_by": "cursor",
	})
	if err == nil {
		t.Fatal("expected error for missing agent")
	}
}

func TestCancelAgent_MultipleTasks(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServerWithCanceller(svc, logger, nil)

	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "T1", Status: "in_progress", AssignedTo: "claude-code", CreatedBy: "cursor"},
		{ID: 2, Title: "T2", Status: "in_progress", AssignedTo: "claude-code", CreatedBy: "cursor"},
		{ID: 3, Title: "T3", Status: "completed", AssignedTo: "claude-code", CreatedBy: "cursor"},
	}
	repo.state.NextTaskID = 4

	result, err := callTool(t, srv, "cancel_agent", map[string]any{
		"agent":        "claude-code",
		"cancelled_by": "cursor",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !strings.Contains(text, "cancelled 2 task") {
		t.Errorf("expected 2 cancelled tasks, got: %s", text)
	}

	// Only in_progress tasks should be cancelled
	if repo.state.Tasks[0].Status != "cancelled" {
		t.Errorf("task #1 should be cancelled")
	}
	if repo.state.Tasks[1].Status != "cancelled" {
		t.Errorf("task #2 should be cancelled")
	}
	if repo.state.Tasks[2].Status != "completed" {
		t.Errorf("task #3 should remain completed")
	}
}

// TestCancelAgent_ReapsTaskBoundKeepsStaticPool verifies the reap-on-cancel
// behavior: when cancel_agent is called against an agent type, every
// task-bound instance of that type is deleted from state.AgentInstances and
// state.Presence, while the static pool worker is just marked idle and
// preserved for re-use.
func TestCancelAgent_ReapsTaskBoundKeepsStaticPool(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServerWithCanceller(svc, logger, nil)

	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"cursor": {
			InstanceID: "cursor",
			AgentType:  "cursor",
			Role:       domain.RoleDriver,
			Status:     "idle",
		},
		"claude-code": {
			InstanceID:   "claude-code",
			AgentType:    "claude-code",
			Role:         domain.RoleWorker,
			Status:       "busy",
			CurrentTasks: []int{},
		},
		"claude-code-task-7": {
			InstanceID:   "claude-code-task-7",
			AgentType:    "claude-code",
			Role:         domain.RoleWorker,
			Status:       "busy",
			CurrentTasks: []int{7},
		},
	}
	repo.state.Presence = map[string]*domain.Presence{
		"claude-code":        {Agent: "claude-code", Status: "working", LastSeen: time.Now()},
		"claude-code-task-7": {Agent: "claude-code-task-7", Status: "working", LastSeen: time.Now()},
	}
	repo.state.Tasks = []domain.Task{
		{ID: 7, Title: "T7", Status: "in_progress", AssignedTo: "claude-code", CreatedBy: "cursor"},
	}
	repo.state.NextTaskID = 8

	_, err := callTool(t, srv, "cancel_agent", map[string]any{
		"agent":        "claude-code",
		"cancelled_by": "cursor",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Task-bound row must be GONE from both maps.
	if _, ok := repo.state.AgentInstances["claude-code-task-7"]; ok {
		t.Errorf("task-bound AgentInstance should be reaped after cancel")
	}
	if _, ok := repo.state.Presence["claude-code-task-7"]; ok {
		t.Errorf("task-bound Presence should be reaped after cancel")
	}

	// Static pool row must be PRESERVED, marked idle, with no current tasks.
	staticInst, ok := repo.state.AgentInstances["claude-code"]
	if !ok || staticInst == nil {
		t.Fatal("static pool instance 'claude-code' should be preserved")
	}
	if staticInst.Status != "idle" {
		t.Errorf("static pool instance should be idle, got %q", staticInst.Status)
	}
	if len(staticInst.CurrentTasks) != 0 {
		t.Errorf("static pool should have no current tasks, got %v", staticInst.CurrentTasks)
	}
	// Presence for static pool is fine to keep — it's not stale.
	if _, ok := repo.state.Presence["claude-code"]; !ok {
		t.Errorf("static pool Presence should be preserved")
	}
}

// TestCancelAgent_ReapsByInstanceID verifies that calling cancel_agent
// directly with the task-bound instance id (instead of the agent type) also
// reaps the row.
func TestCancelAgent_ReapsByInstanceID(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServerWithCanceller(svc, logger, nil)

	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"cursor": {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "idle"},
		"codex-task-12": {
			InstanceID:   "codex-task-12",
			AgentType:    "codex",
			Role:         domain.RoleWorker,
			Status:       "busy",
			CurrentTasks: []int{12},
		},
	}
	repo.state.Presence = map[string]*domain.Presence{
		"codex-task-12": {Agent: "codex-task-12", Status: "working", LastSeen: time.Now()},
	}
	repo.state.Tasks = []domain.Task{
		{ID: 12, Title: "T12", Status: "in_progress", AssignedTo: "codex", CreatedBy: "cursor"},
	}
	repo.state.NextTaskID = 13

	_, err := callTool(t, srv, "cancel_agent", map[string]any{
		"agent":        "codex-task-12",
		"cancelled_by": "cursor",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := repo.state.AgentInstances["codex-task-12"]; ok {
		t.Errorf("task-bound AgentInstance should be reaped when cancel called by instance id")
	}
	if _, ok := repo.state.Presence["codex-task-12"]; ok {
		t.Errorf("task-bound Presence should be reaped when cancel called by instance id")
	}
}

// TestUpdateTask_TerminalReapsTaskBound verifies that transitioning a task
// to a terminal state (completed/cancelled/blocked) reaps the task-bound
// worker that owned it.
func TestUpdateTask_TerminalReapsTaskBound(t *testing.T) {
	cases := []struct {
		name     string
		newState string
	}{
		{"completed", "completed"},
		{"cancelled", "cancelled"},
		{"blocked", "blocked"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo := newTestService()
			logger := log.New(io.Discard, "", 0)
			srv := testServer(svc, logger)

			repo.state.AgentInstances = map[string]*domain.AgentInstance{
				"cursor": {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "idle"},
				"claude-code": {
					InstanceID: "claude-code", AgentType: "claude-code",
					Role: domain.RoleWorker, Status: "idle",
				},
				"claude-code-task-3": {
					InstanceID: "claude-code-task-3", AgentType: "claude-code",
					Role: domain.RoleWorker, Status: "busy", CurrentTasks: []int{3},
				},
			}
			repo.state.Presence = map[string]*domain.Presence{
				"claude-code-task-3": {Agent: "claude-code-task-3", Status: "working", LastSeen: time.Now()},
			}
			repo.state.Tasks = []domain.Task{
				{ID: 3, Title: "Test", Status: "in_progress", AssignedTo: "claude-code", CreatedBy: "cursor"},
			}
			repo.state.NextTaskID = 4

			_, err := callTool(t, srv, "update_task", map[string]any{
				"id":         float64(3),
				"status":     tc.newState,
				"updated_by": "cursor",
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if _, ok := repo.state.AgentInstances["claude-code-task-3"]; ok {
				t.Errorf("task-bound AgentInstance should be reaped after %s transition", tc.newState)
			}
			if _, ok := repo.state.Presence["claude-code-task-3"]; ok {
				t.Errorf("task-bound Presence should be reaped after %s transition", tc.newState)
			}
			if _, ok := repo.state.AgentInstances["claude-code"]; !ok {
				t.Errorf("static pool instance must be preserved across %s transition", tc.newState)
			}
		})
	}
}

// TestCancelAgent_AutoRecoversCapturedOutput verifies the Bug D synthetic-send
// safety net: if the worker captured output but never sent a send_message, the
// cancel handler emits a synthetic recovery message from the worker to the
// driver so the deliverable isn't silently dropped.
func TestCancelAgent_AutoRecoversCapturedOutput(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	canceller := newMockCanceller()
	canceller.running["claude-code"] = true
	canceller.output["claude-code"] = "FINAL REPORT: All checks passed.\nSending to driver...\n"
	srv := testServerWithCanceller(svc, logger, canceller)

	repo.state.DriverID = "cursor"
	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"cursor":      {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "idle"},
		"claude-code": {InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker, Status: "busy", CurrentTasks: []int{1}},
	}
	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Captured output", Status: "in_progress", AssignedTo: "claude-code", CreatedBy: "cursor"},
	}
	repo.state.NextTaskID = 2

	_, err := callTool(t, srv, "cancel_agent", map[string]any{
		"agent":        "claude-code",
		"cancelled_by": "cursor",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var recovery *domain.Message
	for i := range repo.state.Messages {
		m := &repo.state.Messages[i]
		if m.From == "claude-code" && m.To == "cursor" && strings.Contains(m.Content, "Auto-recovered output") {
			recovery = m
			break
		}
	}
	if recovery == nil {
		t.Fatalf("expected synthetic recovery message; messages=%v", repo.state.Messages)
	}
	if !strings.Contains(recovery.Content, "FINAL REPORT") {
		t.Errorf("recovery message should include captured output, got: %s", recovery.Content)
	}
}

// TestCancelAgent_NoRecoveryWhenAgentSentRecently verifies the dedupe branch:
// if the worker already sent a send_message in the last hour, we do NOT emit a
// synthetic recovery.
func TestCancelAgent_NoRecoveryWhenAgentSentRecently(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	canceller := newMockCanceller()
	canceller.running["claude-code"] = true
	canceller.output["claude-code"] = "tail of output that should NOT be auto-recovered"
	srv := testServerWithCanceller(svc, logger, canceller)

	now := time.Now()
	repo.state.DriverID = "cursor"
	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"cursor":      {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "idle"},
		"claude-code": {InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker, Status: "busy", CurrentTasks: []int{1}},
	}
	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Already sent", Status: "in_progress", AssignedTo: "claude-code", CreatedBy: "cursor"},
	}
	repo.state.NextTaskID = 2
	repo.state.LastSendByAgent = map[string]time.Time{
		"claude-code": now.Add(-30 * time.Second), // recent send
	}

	_, err := callTool(t, srv, "cancel_agent", map[string]any{
		"agent":        "claude-code",
		"cancelled_by": "cursor",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, m := range repo.state.Messages {
		if m.From == "claude-code" && strings.Contains(m.Content, "Auto-recovered output") {
			t.Fatalf("did not expect synthetic recovery when agent already sent recently; got: %s", m.Content)
		}
	}
}

// TestCancelAgent_RoutesSTOPToTaskBoundInstance (C4) — when the agent
// being cancelled is a parent type with one or more task-bound child
// instances, every task-bound child must receive its own STOP message
// addressed to its specific InstanceID. Sending only one STOP to the
// parent name leaves the actual worker process listening on the wrong
// inbox and never noticing it has been cancelled.
func TestCancelAgent_RoutesSTOPToTaskBoundInstance(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServerWithCanceller(svc, logger, nil)

	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"cursor":      {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "idle"},
		"claude-code": {InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker, Status: "idle"},
		"claude-code-task-7": {
			InstanceID: "claude-code-task-7", AgentType: "claude-code",
			Role: domain.RoleWorker, Status: "busy", CurrentTasks: []int{7},
		},
		"claude-code-task-9": {
			InstanceID: "claude-code-task-9", AgentType: "claude-code",
			Role: domain.RoleWorker, Status: "busy", CurrentTasks: []int{9},
		},
	}
	repo.state.Tasks = []domain.Task{
		{ID: 7, Title: "T7", Status: "in_progress", AssignedTo: "claude-code", CreatedBy: "cursor"},
		{ID: 9, Title: "T9", Status: "in_progress", AssignedTo: "claude-code", CreatedBy: "cursor"},
	}
	repo.state.NextTaskID = 10

	_, err := callTool(t, srv, "cancel_agent", map[string]any{
		"agent":        "claude-code",
		"cancelled_by": "cursor",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedTargets := map[string]bool{
		"claude-code":        false,
		"claude-code-task-7": false,
		"claude-code-task-9": false,
	}
	for _, m := range repo.state.Messages {
		if m.From != "system" || !strings.Contains(m.Content, "STOP") {
			continue
		}
		if _, ok := expectedTargets[m.To]; ok {
			expectedTargets[m.To] = true
		}
	}
	for to, gotIt := range expectedTargets {
		if !gotIt {
			t.Errorf("expected STOP message to %q after cancel; not found", to)
		}
	}
}

// TestCancelAgent_PerTaskOutputCapture (C4) — output captured per
// task-bound instance must be persisted on its OWN task's work context,
// not smeared across every cancelled task. The previous code captured
// once with GetRecentOutput(parentName), then wrote the same blob to
// every task in the loop — silently overwriting whatever the actual
// per-instance output was.
func TestCancelAgent_PerTaskOutputCapture(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	canceller := newMockCanceller()
	canceller.output["claude-code-task-7"] = "OUTPUT FROM TASK 7 RUN"
	canceller.output["claude-code-task-9"] = "OUTPUT FROM TASK 9 RUN"
	srv := testServerWithCanceller(svc, logger, canceller)

	repo.state.DriverID = "cursor"
	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"cursor":      {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "idle"},
		"claude-code": {InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker, Status: "idle"},
		"claude-code-task-7": {
			InstanceID: "claude-code-task-7", AgentType: "claude-code",
			Role: domain.RoleWorker, Status: "busy", CurrentTasks: []int{7},
		},
		"claude-code-task-9": {
			InstanceID: "claude-code-task-9", AgentType: "claude-code",
			Role: domain.RoleWorker, Status: "busy", CurrentTasks: []int{9},
		},
	}
	repo.state.Tasks = []domain.Task{
		{ID: 7, Title: "T7", Status: "in_progress", AssignedTo: "claude-code", CreatedBy: "cursor"},
		{ID: 9, Title: "T9", Status: "in_progress", AssignedTo: "claude-code", CreatedBy: "cursor"},
	}
	repo.state.WorkContexts = map[string]*domain.WorkContext{
		"task-7": {ID: "task-7", TaskID: 7},
		"task-9": {ID: "task-9", TaskID: 9},
	}
	repo.state.NextTaskID = 10

	_, err := callTool(t, srv, "cancel_agent", map[string]any{
		"agent":        "claude-code",
		"cancelled_by": "cursor",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wc7 := repo.state.WorkContexts["task-7"]
	wc9 := repo.state.WorkContexts["task-9"]
	if wc7 == nil || wc9 == nil {
		t.Fatalf("expected work contexts to remain after cancel; got wc7=%v wc9=%v", wc7, wc9)
	}

	if !strings.Contains(wc7.PreviousOutput, "OUTPUT FROM TASK 7 RUN") {
		t.Errorf("task #7 work context should hold its own captured output; got %q", wc7.PreviousOutput)
	}
	if !strings.Contains(wc9.PreviousOutput, "OUTPUT FROM TASK 9 RUN") {
		t.Errorf("task #9 work context should hold its own captured output; got %q", wc9.PreviousOutput)
	}
	if strings.Contains(wc7.PreviousOutput, "OUTPUT FROM TASK 9 RUN") {
		t.Errorf("task #7 must not contain task #9's output (smeared capture); got %q", wc7.PreviousOutput)
	}
	if strings.Contains(wc9.PreviousOutput, "OUTPUT FROM TASK 7 RUN") {
		t.Errorf("task #9 must not contain task #7's output (smeared capture); got %q", wc9.PreviousOutput)
	}
}

// TestCancelAgent_RaceWithWorkerCompletion (C4) — when one task-bound
// sibling has already completed its task (Status="completed", existing
// PreviousOutput captured) and the other is still in_progress when
// cancel_agent fires, the cancel must:
//   - NOT mark the completed task as "cancelled" or rewrite its
//     ResultSummary
//   - NOT smear the cancelled sibling's captured output onto the
//     completed task's work context
//   - NOT emit a synthetic auto-recovery message FROM the
//     already-completed task-bound instance (worker already sent its
//     deliverable cleanly via update_task)
//   - Still cancel the active sibling and emit auto-recovery for it
func TestCancelAgent_RaceWithWorkerCompletion(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	canceller := newMockCanceller()
	// canceller still has cached output for both task-bound instances
	// (it doesn't know task-7 has already finished cleanly).
	canceller.output["claude-code-task-7"] = "STALE TAIL FROM TASK 7 — DO NOT REPLAY"
	canceller.output["claude-code-task-9"] = "ACTIVE OUTPUT FROM TASK 9"
	srv := testServerWithCanceller(svc, logger, canceller)

	repo.state.DriverID = "cursor"
	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"cursor":      {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "idle"},
		"claude-code": {InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker, Status: "idle"},
		"claude-code-task-7": {
			InstanceID: "claude-code-task-7", AgentType: "claude-code",
			Role: domain.RoleWorker, Status: "idle",
		},
		"claude-code-task-9": {
			InstanceID: "claude-code-task-9", AgentType: "claude-code",
			Role: domain.RoleWorker, Status: "busy", CurrentTasks: []int{9},
		},
	}
	repo.state.Tasks = []domain.Task{
		{ID: 7, Title: "T7", Status: "completed", AssignedTo: "claude-code",
			CreatedBy: "cursor", ResultSummary: "Worker finished cleanly"},
		{ID: 9, Title: "T9", Status: "in_progress", AssignedTo: "claude-code",
			CreatedBy: "cursor"},
	}
	repo.state.WorkContexts = map[string]*domain.WorkContext{
		"task-7": {ID: "task-7", TaskID: 7, PreviousOutput: "FINAL DELIVERABLE FROM 7"},
		"task-9": {ID: "task-9", TaskID: 9},
	}
	// Task 7's worker already sent a send_message just before completing.
	repo.state.LastSendByAgent = map[string]time.Time{
		"claude-code-task-7": time.Now().Add(-15 * time.Second),
	}
	repo.state.NextTaskID = 10

	_, err := callTool(t, srv, "cancel_agent", map[string]any{
		"agent":        "claude-code",
		"cancelled_by": "cursor",
		"reason":       "no longer needed",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if repo.state.Tasks[0].Status != "completed" {
		t.Errorf("task #7 was already completed; cancel must leave it alone, got status %q", repo.state.Tasks[0].Status)
	}
	if repo.state.Tasks[0].ResultSummary != "Worker finished cleanly" {
		t.Errorf("completed task ResultSummary must be preserved, got %q", repo.state.Tasks[0].ResultSummary)
	}
	if repo.state.Tasks[1].Status != "cancelled" {
		t.Errorf("task #9 was active; expected cancelled, got %q", repo.state.Tasks[1].Status)
	}

	wc7 := repo.state.WorkContexts["task-7"]
	if wc7 == nil {
		t.Fatalf("work context for task #7 must remain")
	}
	if wc7.PreviousOutput != "FINAL DELIVERABLE FROM 7" {
		t.Errorf("completed task #7 PreviousOutput must NOT be overwritten by stale capture; got %q", wc7.PreviousOutput)
	}
	if strings.Contains(wc7.PreviousOutput, "ACTIVE OUTPUT FROM TASK 9") {
		t.Errorf("completed task #7 must NOT receive task #9's captured output; got %q", wc7.PreviousOutput)
	}

	// Auto-recovery: only task-9's instance should produce one. task-7's
	// instance already sent its deliverable; emitting a recovery msg
	// from it would duplicate the already-delivered result.
	for _, m := range repo.state.Messages {
		if m.From == "claude-code-task-7" && strings.Contains(m.Content, "Auto-recovered") {
			t.Errorf("must NOT auto-recover for task-7 (worker already sent recently); got: %s", m.Content)
		}
	}
}

// TestUpdateTask_CompletedKeepsStaticPool ensures that a static pool worker
// finishing its task does NOT get reaped — it's needed for the next task.
func TestUpdateTask_CompletedKeepsStaticPool(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"cursor":      {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "idle"},
		"claude-code": {InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker, Status: "busy", CurrentTasks: []int{1}},
	}
	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Test", Status: "in_progress", AssignedTo: "claude-code", CreatedBy: "cursor"},
	}
	repo.state.NextTaskID = 2

	_, err := callTool(t, srv, "update_task", map[string]any{
		"id":         float64(1),
		"status":     "completed",
		"updated_by": "cursor",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := repo.state.AgentInstances["claude-code"]; !ok {
		t.Errorf("static pool worker must NOT be reaped on task completion")
	}
}
