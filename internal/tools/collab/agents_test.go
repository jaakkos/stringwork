package collab

import (
	"io"
	"log"
	"strings"
	"testing"

	"github.com/jaakkos/stringwork/internal/domain"
)

func TestRegisterAgent_NewAgent(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	args := map[string]any{
		"name":         "my-custom-agent",
		"display_name": "My Custom Agent",
		"capabilities": []any{"testing", "code-review"},
		"workspace":    "/path/to/workspace",
		"project":      "test-project",
	}

	result, err := callTool(t, srv, "register_agent", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !strings.Contains(text, "registered successfully") {
		t.Errorf("unexpected result: %s", text)
	}

	// Verify agent was stored
	agent, exists := repo.state.RegisteredAgents["my-custom-agent"]
	if !exists {
		t.Fatal("agent should be registered")
	}

	if agent.DisplayName != "My Custom Agent" {
		t.Errorf("expected display name 'My Custom Agent', got %q", agent.DisplayName)
	}

	if len(agent.Capabilities) != 2 {
		t.Errorf("expected 2 capabilities, got %d", len(agent.Capabilities))
	}

	if agent.Workspace != "/path/to/workspace" {
		t.Errorf("expected workspace '/path/to/workspace', got %q", agent.Workspace)
	}

	if agent.Project != "test-project" {
		t.Errorf("expected project 'test-project', got %q", agent.Project)
	}
}

func TestRegisterAgent_UpdateExisting(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	// First registration
	_, err := callTool(t, srv, "register_agent", map[string]any{
		"name":    "my-agent",
		"project": "project-v1",
	})
	if err != nil {
		t.Fatalf("first registration failed: %v", err)
	}

	originalTime := repo.state.RegisteredAgents["my-agent"].RegisteredAt

	// Update registration
	args := map[string]any{
		"name":         "my-agent",
		"project":      "project-v2",
		"display_name": "Updated Agent",
	}
	result, err := callTool(t, srv, "register_agent", args)
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}

	text := resultText(t, result)
	if !strings.Contains(text, "updated") {
		t.Errorf("expected update message, got: %s", text)
	}

	// Verify update
	agent := repo.state.RegisteredAgents["my-agent"]
	if agent.Project != "project-v2" {
		t.Errorf("project should be updated to 'project-v2', got %q", agent.Project)
	}
	if agent.DisplayName != "Updated Agent" {
		t.Errorf("display name should be updated")
	}
	if agent.RegisteredAt != originalTime {
		t.Error("RegisteredAt should not change on update")
	}
}

func TestRegisterAgent_MissingName(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	args := map[string]any{
		"display_name": "No Name Agent",
	}

	_, err := callTool(t, srv, "register_agent", args)
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

// TestRegisterAgent_RejectsTaskBoundNames verifies that names matching the
// task-bound instance convention ("<base>-task-<N>") are rejected. Allowing
// these to become top-level RegisteredAgents historically broke the watchdog
// because heartbeat auto-create paths picked them up as their own AgentType.
func TestRegisterAgent_RejectsTaskBoundNames(t *testing.T) {
	cases := []string{
		"claude-code-task-2",
		"codex-task-17",
		"my-custom-task-99",
	}
	for _, name := range cases {
		name := name
		t.Run(name, func(t *testing.T) {
			svc, repo := newTestService()
			logger := log.New(io.Discard, "", 0)
			srv := testServer(svc, logger)

			_, err := callTool(t, srv, "register_agent", map[string]any{
				"name": name,
			})
			if err == nil {
				t.Fatalf("expected error for task-bound name %q", name)
			}
			if !strings.Contains(err.Error(), "task-bound") {
				t.Errorf("error should mention task-bound semantics, got %q", err.Error())
			}
			if _, exists := repo.state.RegisteredAgents[name]; exists {
				t.Errorf("agent %q should not have been registered", name)
			}
		})
	}
}

// TestRegisterAgent_RejectsBuiltinNameCollision pins the M1 invariant: a
// built-in (already-instanced) agent type like "claude-code" or "codex" is
// owned by the orchestrator's spawn pool. Allowing user-driven register_agent
// to overwrite it via RegisteredAgents creates a phantom RegisteredAgent row
// that confuses ValidateAgent and lets workers self-publish capabilities the
// pool didn't grant.
//
// After the fix, register_agent must reject a name that is already an
// AgentInstance (or AgentType) so the built-in pool stays canonical.
func TestRegisterAgent_RejectsBuiltinNameCollision(t *testing.T) {
	cases := []string{"claude-code", "codex", "cursor"}
	for _, name := range cases {
		name := name
		t.Run(name, func(t *testing.T) {
			svc, repo := newTestService()
			_ = svc.Run(func(state *domain.CollabState) error {
				state.AgentInstances[name] = &domain.AgentInstance{
					InstanceID: name, AgentType: name,
					Role: domain.RoleWorker, Status: "idle",
				}
				return nil
			})

			logger := log.New(io.Discard, "", 0)
			srv := testServer(svc, logger)

			_, err := callTool(t, srv, "register_agent", map[string]any{
				"name": name,
			})
			if err == nil {
				t.Fatalf("expected error for built-in name collision %q", name)
			}
			if !strings.Contains(err.Error(), "built-in") && !strings.Contains(err.Error(), "reserved") {
				t.Errorf("error should mention built-in collision, got %q", err.Error())
			}
			if _, exists := repo.state.RegisteredAgents[name]; exists {
				t.Errorf("agent %q should not have been registered (collides with built-in)", name)
			}
		})
	}
}

// TestAgentInstance_TaskBoundIDDoesNotConflictWithStatic ensures that a
// task-bound instance ID (e.g. "claude-code-task-1") never overwrites or
// pollutes the static-pool row "claude-code". This was the seed of several
// historical bugs: heartbeat and resolveWorkerAgent paths used to upsert
// task-bound IDs into AgentInstances with AgentType="claude-code-task-1",
// which then masked the real "claude-code" pool entry from list_agents.
func TestAgentInstance_TaskBoundIDDoesNotConflictWithStatic(t *testing.T) {
	svc, repo := newTestService()
	_ = svc.Run(func(state *domain.CollabState) error {
		state.AgentInstances["claude-code"] = &domain.AgentInstance{
			InstanceID: "claude-code", AgentType: "claude-code",
			Role: domain.RoleWorker, Status: "idle", CurrentTasks: []int{},
		}
		return nil
	})

	_, taskBoundID := seedStaticAndTaskBound(t, repo.state, "claude-code", 7)

	if _, exists := repo.state.AgentInstances["claude-code"]; !exists {
		t.Fatal("static pool row 'claude-code' must remain after task-bound seed")
	}
	staticInst := repo.state.AgentInstances["claude-code"]
	if staticInst.AgentType != "claude-code" {
		t.Errorf("static AgentType corrupted: %q", staticInst.AgentType)
	}
	if staticInst.InstanceID != "claude-code" {
		t.Errorf("static InstanceID corrupted: %q", staticInst.InstanceID)
	}

	tbInst, exists := repo.state.AgentInstances[taskBoundID]
	if !exists {
		t.Fatalf("task-bound instance %q missing", taskBoundID)
	}
	if tbInst.AgentType == taskBoundID {
		t.Errorf("task-bound AgentType holds task-bound ID %q (should be parent type)", tbInst.AgentType)
	}
	if tbInst.AgentType != "claude-code" {
		t.Errorf("task-bound AgentType = %q, want \"claude-code\"", tbInst.AgentType)
	}

	if _, exists := repo.state.RegisteredAgents[taskBoundID]; exists {
		t.Errorf("task-bound ID %q must NOT appear in RegisteredAgents", taskBoundID)
	}
}

func TestRegisterAgent_MinimalRegistration(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	args := map[string]any{
		"name": "minimal-agent",
	}

	_, err := callTool(t, srv, "register_agent", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	agent, exists := repo.state.RegisteredAgents["minimal-agent"]
	if !exists {
		t.Fatal("agent should be registered")
	}

	if agent.Name != "minimal-agent" {
		t.Errorf("expected name 'minimal-agent', got %q", agent.Name)
	}
}

func TestListAgents_Empty(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	result, err := callTool(t, srv, "list_agents", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !strings.Contains(text, "Built-in Agents") {
		t.Error("should include built-in agents section")
	}
	if !strings.Contains(text, "cursor") {
		t.Error("should list cursor")
	}
	if !strings.Contains(text, "claude-code") {
		t.Error("should list claude-code")
	}
	if !strings.Contains(text, "(none)") {
		t.Error("should indicate no registered agents")
	}
}

func TestListAgents_WithRegistered(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	// Pre-register an agent
	repo.state.RegisteredAgents["test-agent"] = &domain.RegisteredAgent{
		Name:         "test-agent",
		DisplayName:  "Test Agent",
		Capabilities: []string{"testing"},
		Project:      "my-project",
	}

	srv := testServer(svc, logger)

	result, err := callTool(t, srv, "list_agents", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !strings.Contains(text, "test-agent") {
		t.Error("should list registered agent")
	}
	if !strings.Contains(text, "Test Agent") {
		t.Error("should show display name")
	}
	if !strings.Contains(text, "testing") {
		t.Error("should show capabilities")
	}
	if !strings.Contains(text, "my-project") {
		t.Error("should show project")
	}
}

func TestListAgents_ExcludeBuiltin(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.RegisteredAgents["custom"] = &domain.RegisteredAgent{
		Name: "custom",
	}

	srv := testServer(svc, logger)

	args := map[string]any{
		"include_builtin": false,
	}

	result, err := callTool(t, srv, "list_agents", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if strings.Contains(text, "Built-in Agents") {
		t.Error("should not include built-in agents section")
	}
	if strings.Contains(text, "cursor") || strings.Contains(text, "claude-code") {
		t.Error("should not list built-in agents")
	}
	if !strings.Contains(text, "custom") {
		t.Error("should still list registered agents")
	}
}
