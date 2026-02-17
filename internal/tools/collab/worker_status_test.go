package collab

import (
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/jaakkos/stringwork/internal/domain"
	"github.com/jaakkos/stringwork/internal/policy"
)

func TestWorkerStatus_Basic(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	result, err := callTool(t, srv, "worker_status", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "Worker Status") {
		t.Errorf("expected 'Worker Status' header, got: %s", text)
	}
	// Should list workers
	if !strings.Contains(text, "claude-code") {
		t.Errorf("expected claude-code in output: %s", text)
	}
	if !strings.Contains(text, "codex") {
		t.Errorf("expected codex in output: %s", text)
	}
}

func TestWorkerStatus_DriverExcluded(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	result, err := callTool(t, srv, "worker_status", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)

	// Driver should be shown as "Driver: cursor" but NOT in the instances list
	if !strings.Contains(text, "Driver: cursor") {
		t.Errorf("expected 'Driver: cursor' header: %s", text)
	}

	// The instances section should not have cursor as a worker entry
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- cursor") && strings.Contains(trimmed, "[cursor]") {
			t.Errorf("cursor should not appear as a worker instance: %s", line)
		}
	}
}

func TestWorkerStatus_WithHeartbeat(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	// Pre-seed instances with a recent heartbeat
	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"cursor":      {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "idle"},
		"claude-code": {InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker, Status: "idle", LastHeartbeat: time.Now(), CurrentTasks: []int{}},
	}

	srv := testServer(svc, logger)

	result, err := callTool(t, srv, "worker_status", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)

	// Should show a time-based "ago" instead of "never"
	if strings.Contains(text, "heartbeat: never") {
		t.Errorf("expected heartbeat time for claude-code, got 'never': %s", text)
	}
	if !strings.Contains(text, "ago") {
		t.Errorf("expected 'ago' in heartbeat display: %s", text)
	}
}

func TestWorkerStatus_WithCurrentTasks(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"cursor":      {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "idle"},
		"claude-code": {InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker, Status: "working", CurrentTasks: []int{1, 3}, LastHeartbeat: time.Now()},
	}

	srv := testServer(svc, logger)

	result, err := callTool(t, srv, "worker_status", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)

	if !strings.Contains(text, "tasks:") {
		t.Errorf("expected current tasks in output: %s", text)
	}
	if !strings.Contains(text, "working") {
		t.Errorf("expected 'working' status in output: %s", text)
	}
}

func TestWorkerStatus_NoWorkers(t *testing.T) {
	// Custom mock policy with no workers
	repo := newMockRepository()
	noWorkerPolicy := &mockPolicyNoWorkers{}
	logger := log.New(io.Discard, "", 0)
	svc := newTestServiceWith(repo, noWorkerPolicy, logger)
	srv := testServer(svc, logger)

	result, err := callTool(t, srv, "worker_status", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)

	// Should still have header and Instances label but no worker entries
	if !strings.Contains(text, "Worker Status") {
		t.Errorf("expected header: %s", text)
	}
}

// mockPolicyNoWorkers returns orchestration config with driver only, no workers.
type mockPolicyNoWorkers struct {
	mockPolicy
}

func (m *mockPolicyNoWorkers) Orchestration() *policy.OrchestrationConfig {
	return &policy.OrchestrationConfig{
		Driver:  "cursor",
		Workers: []policy.WorkerConfig{},
	}
}
