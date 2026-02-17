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
		"cursor":     {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "idle"},
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
		"cursor":     {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "idle"},
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

func TestHeartbeat_ByAgentType(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	// Set up multi-instance scenario: claude-code-1 as the instance ID, claude-code as agent type
	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"cursor":       {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "idle"},
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
		"cursor":     {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "idle"},
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
