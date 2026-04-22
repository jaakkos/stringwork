package collab

import (
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/jaakkos/stringwork/internal/domain"
)

func TestHeartbeat_Success(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	result, err := callTool(t, srv, "heartbeat", map[string]any{"agent": "claude-code"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if text != "OK" {
		t.Errorf("expected OK, got %q", text)
	}
}

func TestHeartbeat_OfflineToIdle(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	// Pre-seed AgentInstances with claude-code set to "offline"
	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"cursor":      {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "idle"},
		"claude-code": {InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker, Status: "offline", CurrentTasks: []int{}},
	}

	srv := testServer(svc, logger)

	_, err := callTool(t, srv, "heartbeat", map[string]any{"agent": "claude-code"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	inst := repo.state.AgentInstances["claude-code"]
	if inst.Status != "idle" {
		t.Errorf("expected status 'idle' after heartbeat from offline, got %q", inst.Status)
	}
}

func TestHeartbeat_UpdatesTimestamp(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	oldTime := time.Now().Add(-10 * time.Minute)
	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"cursor":      {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "idle"},
		"claude-code": {InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker, Status: "idle", LastHeartbeat: oldTime, CurrentTasks: []int{}},
	}

	srv := testServer(svc, logger)

	before := time.Now()
	_, err := callTool(t, srv, "heartbeat", map[string]any{"agent": "claude-code"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	inst := repo.state.AgentInstances["claude-code"]
	if inst.LastHeartbeat.Before(before) {
		t.Errorf("expected LastHeartbeat to be updated, but it's still %v (before %v)", inst.LastHeartbeat, before)
	}
}

func TestHeartbeat_MissingAgent(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	_, err := callTool(t, srv, "heartbeat", map[string]any{})
	if err == nil {
		t.Error("expected error for missing agent")
	}
}

func TestHeartbeat_UnknownAgent(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	_, err := callTool(t, srv, "heartbeat", map[string]any{"agent": "totally-unknown"})
	if err == nil {
		t.Error("expected error for unknown agent")
	}
	if err != nil && !strings.Contains(err.Error(), "unknown agent") {
		t.Errorf("expected 'unknown agent' error, got: %v", err)
	}
}

func TestHeartbeat_RegisteredAgent(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	// Register an agent that is NOT in orchestration config
	repo.state.RegisteredAgents["custom-bot"] = &domain.RegisteredAgent{
		Name:        "custom-bot",
		DisplayName: "Custom Bot",
	}

	srv := testServer(svc, logger)

	// Heartbeat from registered-only agent should now succeed
	result, err := callTool(t, srv, "heartbeat", map[string]any{"agent": "custom-bot"})
	if err != nil {
		t.Fatalf("registered agent heartbeat should succeed: %v", err)
	}
	text := resultText(t, result)
	if text != "OK" {
		t.Errorf("expected OK, got %q", text)
	}

	// Should have created an ephemeral AgentInstance
	inst, ok := repo.state.AgentInstances["custom-bot"]
	if !ok {
		t.Fatal("expected AgentInstance to be created for registered agent")
	}
	if inst.Role != domain.RoleWorker {
		t.Errorf("expected worker role, got %q", inst.Role)
	}
	if inst.Status != "idle" {
		t.Errorf("expected idle status, got %q", inst.Status)
	}
	if inst.LastHeartbeat.IsZero() {
		t.Error("expected LastHeartbeat to be set")
	}
}

// TestHeartbeat_AutoCreate_ResolvesParentType verifies that when a task-bound
// worker (e.g. "my-bot-task-4") heartbeats and no AgentInstance exists yet,
// the auto-created entry's AgentType resolves to the parent type "my-bot"
// rather than the raw task-bound ID. This prevents the write-path pollution
// that historically broke the watchdog's type-grouping logic.
func TestHeartbeat_AutoCreate_ResolvesParentType(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	// Register the parent type so the heartbeat auto-create path fires.
	repo.state.RegisteredAgents["my-bot"] = &domain.RegisteredAgent{
		Name:        "my-bot",
		DisplayName: "My Bot",
	}

	srv := testServer(svc, logger)

	_, err := callTool(t, srv, "heartbeat", map[string]any{"agent": "my-bot-task-4"})
	if err != nil {
		t.Fatalf("heartbeat for task-bound worker should succeed: %v", err)
	}

	inst, ok := repo.state.AgentInstances["my-bot-task-4"]
	if !ok {
		t.Fatal("expected AgentInstance to be auto-created for task-bound worker")
	}
	if inst.AgentType != "my-bot" {
		t.Errorf("AgentType should resolve to parent 'my-bot', got %q", inst.AgentType)
	}
	if inst.InstanceID != "my-bot-task-4" {
		t.Errorf("InstanceID should preserve the task-bound ID, got %q", inst.InstanceID)
	}
	if inst.Role != domain.RoleWorker {
		t.Errorf("expected worker role, got %q", inst.Role)
	}

	// Ensure the task-bound name was not mistakenly registered as a
	// top-level agent.
	if _, exists := repo.state.RegisteredAgents["my-bot-task-4"]; exists {
		t.Error("task-bound heartbeat must not register the child as a top-level agent")
	}
}

func TestHeartbeat_ByAgentType(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	// Set up multi-instance scenario: claude-code-1 as the instance ID, claude-code as agent type
	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"cursor":        {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "idle"},
		"claude-code-1": {InstanceID: "claude-code-1", AgentType: "claude-code", Role: domain.RoleWorker, Status: "offline", CurrentTasks: []int{}},
	}

	srv := testServer(svc, logger)

	// Heartbeat using the agent type "claude-code" should match instance "claude-code-1"
	_, err := callTool(t, srv, "heartbeat", map[string]any{"agent": "claude-code"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	inst := repo.state.AgentInstances["claude-code-1"]
	if inst.Status != "idle" {
		t.Errorf("expected status 'idle' after heartbeat by type, got %q", inst.Status)
	}
	if inst.LastHeartbeat.IsZero() {
		t.Error("expected LastHeartbeat to be updated on type-matched instance")
	}
}

func TestHeartbeat_DoesNotChangeNonOfflineStatus(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"cursor":      {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "idle"},
		"claude-code": {InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker, Status: "working", CurrentTasks: []int{1}},
	}

	srv := testServer(svc, logger)

	_, err := callTool(t, srv, "heartbeat", map[string]any{"agent": "claude-code"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Status should remain "working", not be reset to "idle"
	inst := repo.state.AgentInstances["claude-code"]
	if inst.Status != "working" {
		t.Errorf("heartbeat should not change non-offline status; expected 'working', got %q", inst.Status)
	}
}

func TestHeartbeat_StoresSessionID(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"cursor":      {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "idle"},
		"claude-code": {InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker, Status: "idle", CurrentTasks: []int{}},
	}

	srv := testServer(svc, logger)

	_, err := callTool(t, srv, "heartbeat", map[string]any{
		"agent":      "claude-code",
		"progress":   "starting work",
		"session_id": "cli-session-abc123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	inst := repo.state.AgentInstances["claude-code"]
	if inst.SessionID != "cli-session-abc123" {
		t.Errorf("expected SessionID 'cli-session-abc123', got %q", inst.SessionID)
	}
}

func TestHeartbeat_SessionIDNotOverwrittenByEmpty(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"cursor":      {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "idle"},
		"claude-code": {InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker, Status: "idle", SessionID: "existing-session", CurrentTasks: []int{}},
	}

	srv := testServer(svc, logger)

	// Heartbeat without session_id should not clear existing one
	_, err := callTool(t, srv, "heartbeat", map[string]any{
		"agent":    "claude-code",
		"progress": "continuing work",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	inst := repo.state.AgentInstances["claude-code"]
	if inst.SessionID != "existing-session" {
		t.Errorf("heartbeat without session_id should not clear existing; got %q, want %q", inst.SessionID, "existing-session")
	}
}
