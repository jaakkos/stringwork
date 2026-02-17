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
