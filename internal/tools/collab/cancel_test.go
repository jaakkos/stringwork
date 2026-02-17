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
}

func newMockCanceller() *mockCanceller {
	return &mockCanceller{
		cancelled: make(map[string]bool),
		running:   make(map[string]bool),
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
