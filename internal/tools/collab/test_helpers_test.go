package collab

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/jaakkos/stringwork/internal/app"
	"github.com/jaakkos/stringwork/internal/domain"
)

// testServer creates a MCPServer with all tools registered for testing.
func testServer(svc *app.CollabService, logger *log.Logger) *server.MCPServer {
	s := server.NewMCPServer("test", "1.0.0")
	registry := app.NewSessionRegistry()
	Register(s, svc, logger, registry, nil)
	return s
}

// testServerWithOrch creates a MCPServer with an orchestrator for testing reassignment.
func testServerWithOrch(svc *app.CollabService, logger *log.Logger, orch *app.TaskOrchestrator) *server.MCPServer {
	s := server.NewMCPServer("test", "1.0.0")
	registry := app.NewSessionRegistry()
	Register(s, svc, logger, registry, orch)
	return s
}

// testServerWithSpawner creates a MCPServer with a TaskSpawner for testing
// post-lock spawn behaviour (create_task / update_task / replay_task spawn paths).
func testServerWithSpawner(svc *app.CollabService, logger *log.Logger, spawner TaskSpawner) *server.MCPServer {
	s := server.NewMCPServer("test", "1.0.0")
	registry := app.NewSessionRegistry()
	Register(s, svc, logger, registry, nil, WithTaskSpawner(spawner))
	return s
}

// fakeSpawner records SpawnForTask calls made by the task tools so tests can
// assert spawn behaviour (or lack thereof). Optionally invokes onSpawn before
// recording, useful for tests that want to assert state at the moment of spawn.
type fakeSpawner struct {
	calls   []spawnCall
	onSpawn func(taskID int, assignedTo string)
}

type spawnCall struct {
	TaskID     int
	AssignedTo string
}

func (f *fakeSpawner) SpawnForTask(taskID int, assignedTo string) {
	if f.onSpawn != nil {
		f.onSpawn(taskID, assignedTo)
	}
	f.calls = append(f.calls, spawnCall{TaskID: taskID, AssignedTo: assignedTo})
}

// callTool calls a registered tool via the MCPServer's HandleMessage.
// Returns the parsed CallToolResult or an error.
func callTool(t *testing.T, s *server.MCPServer, name string, args map[string]any) (*mcp.CallToolResult, error) {
	t.Helper()

	reqJSON, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      name,
			"arguments": args,
		},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	respJSON := s.HandleMessage(context.Background(), reqJSON)

	respBytes, marshalErr := json.Marshal(respJSON)
	if marshalErr != nil {
		t.Fatalf("marshal response: %v", marshalErr)
	}

	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("RPC error %d: %s", resp.Error.Code, resp.Error.Message)
	}

	var result mcp.CallToolResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	return &result, nil
}

// resultText extracts the first text content from a CallToolResult.
func resultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if result == nil {
		t.Fatal("result is nil")
	}
	for _, c := range result.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			return tc.Text
		}
	}
	t.Fatal("no text content in result")
	return ""
}

// seedStaticAndTaskBound seeds the coexistence pattern that exposes most of
// the deep-review findings: a static pool instance (the parent agentType
// itself, e.g. "claude-code") AND a task-bound child instance
// ("claude-code-task-N") for the same parent agent type. The static row
// owns no tasks and is idle; the task-bound row owns taskID and is busy.
//
// Both instances share AgentType=agentType — that's the critical detail
// that triggers the parent-type-vs-instance-id bugs. Tests can override
// any field after the helper returns.
//
// The agent type is registered in RegisteredAgents so ValidateAgent and
// ResolveParentAgentType resolve cleanly.
func seedStaticAndTaskBound(t *testing.T, state *domain.CollabState, agentType string, taskID int) (staticID, taskBoundID string) {
	t.Helper()
	if state == nil {
		t.Fatal("seedStaticAndTaskBound: state is nil")
	}
	if state.RegisteredAgents == nil {
		state.RegisteredAgents = make(map[string]*domain.RegisteredAgent)
	}
	if _, ok := state.RegisteredAgents[agentType]; !ok {
		state.RegisteredAgents[agentType] = &domain.RegisteredAgent{
			Name:         agentType,
			RegisteredAt: time.Now(),
			LastSeen:     time.Now(),
		}
	}
	if state.AgentInstances == nil {
		state.AgentInstances = make(map[string]*domain.AgentInstance)
	}
	staticID = agentType
	taskBoundID = fmt.Sprintf("%s-task-%d", agentType, taskID)
	now := time.Now()
	state.AgentInstances[staticID] = &domain.AgentInstance{
		InstanceID:    staticID,
		AgentType:     agentType,
		Role:          domain.RoleWorker,
		Status:        "idle",
		MaxTasks:      1,
		CurrentTasks:  []int{},
		LastHeartbeat: now,
	}
	state.AgentInstances[taskBoundID] = &domain.AgentInstance{
		InstanceID:    taskBoundID,
		AgentType:     agentType,
		Role:          domain.RoleWorker,
		Status:        "busy",
		MaxTasks:      1,
		CurrentTasks:  []int{taskID},
		LastHeartbeat: now,
	}
	return staticID, taskBoundID
}

// seedDeadAgent seeds a single AgentInstance whose LastHeartbeat is in the
// past by lastHeartbeatAgo. The instance is registered in RegisteredAgents
// and marked offline. Used to test watchdog dead-agent recovery, session
// fallback heartbeat broadcast, and piggyback liveness gates.
//
// Returns the seeded instance ID (== agentType, since it represents the
// canonical static-pool row).
func seedDeadAgent(t *testing.T, state *domain.CollabState, agentType string, lastHeartbeatAgo time.Duration) string {
	t.Helper()
	if state == nil {
		t.Fatal("seedDeadAgent: state is nil")
	}
	if state.RegisteredAgents == nil {
		state.RegisteredAgents = make(map[string]*domain.RegisteredAgent)
	}
	if _, ok := state.RegisteredAgents[agentType]; !ok {
		state.RegisteredAgents[agentType] = &domain.RegisteredAgent{
			Name:         agentType,
			RegisteredAt: time.Now().Add(-2 * lastHeartbeatAgo),
			LastSeen:     time.Now().Add(-lastHeartbeatAgo),
		}
	}
	if state.AgentInstances == nil {
		state.AgentInstances = make(map[string]*domain.AgentInstance)
	}
	state.AgentInstances[agentType] = &domain.AgentInstance{
		InstanceID:    agentType,
		AgentType:     agentType,
		Role:          domain.RoleWorker,
		Status:        "offline",
		MaxTasks:      1,
		CurrentTasks:  []int{},
		LastHeartbeat: time.Now().Add(-lastHeartbeatAgo),
	}
	return agentType
}
