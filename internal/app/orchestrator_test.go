package app

import (
	"testing"

	"github.com/jaakkos/stringwork/internal/domain"
)

func TestReassignTask_ExcludesBlocker(t *testing.T) {
	orch := NewTaskOrchestrator(nil, "least_loaded")

	state := &domain.CollabState{
		DriverID: "cursor",
		AgentInstances: map[string]*domain.AgentInstance{
			"cursor":      {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "working", MaxTasks: 5},
			"gemini":      {InstanceID: "gemini", AgentType: "gemini", Role: domain.RoleWorker, Status: "busy", MaxTasks: 3, CurrentTasks: []int{1}},
			"claude-code": {InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker, Status: "idle", MaxTasks: 3},
			"codex":       {InstanceID: "codex", AgentType: "codex", Role: domain.RoleWorker, Status: "idle", MaxTasks: 3},
		},
	}

	task := &domain.Task{ID: 1, Title: "Review code", Status: "blocked", AssignedTo: "gemini"}

	result := orch.ReassignTask(task, state, []string{"gemini"})
	if result == "" {
		t.Fatal("expected reassignment to succeed")
	}
	if result == "gemini" {
		t.Error("should not reassign to the excluded agent type")
	}
	if task.AssignedTo == "gemini" {
		t.Error("task.AssignedTo should be updated away from gemini")
	}
}

func TestReassignTask_NoAlternative(t *testing.T) {
	orch := NewTaskOrchestrator(nil, "least_loaded")

	state := &domain.CollabState{
		DriverID: "cursor",
		AgentInstances: map[string]*domain.AgentInstance{
			"cursor": {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "working", MaxTasks: 5},
			"gemini": {InstanceID: "gemini", AgentType: "gemini", Role: domain.RoleWorker, Status: "busy", MaxTasks: 3, CurrentTasks: []int{1}},
		},
	}

	task := &domain.Task{ID: 1, Title: "Review code", Status: "blocked", AssignedTo: "gemini"}

	result := orch.ReassignTask(task, state, []string{"gemini"})
	if result != "" {
		t.Errorf("expected empty result when no alternative, got %q", result)
	}
}

func TestReassignTask_ExcludesMultipleTypes(t *testing.T) {
	orch := NewTaskOrchestrator(nil, "least_loaded")

	state := &domain.CollabState{
		DriverID: "cursor",
		AgentInstances: map[string]*domain.AgentInstance{
			"cursor":      {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "working", MaxTasks: 5},
			"gemini":      {InstanceID: "gemini", AgentType: "gemini", Role: domain.RoleWorker, Status: "idle", MaxTasks: 3},
			"claude-code": {InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker, Status: "idle", MaxTasks: 3},
			"codex":       {InstanceID: "codex", AgentType: "codex", Role: domain.RoleWorker, Status: "idle", MaxTasks: 3},
		},
	}

	task := &domain.Task{ID: 1, Title: "Review code", Status: "blocked", AssignedTo: "gemini"}

	result := orch.ReassignTask(task, state, []string{"gemini", "claude-code"})
	if result != "codex" {
		t.Errorf("expected reassignment to codex, got %q", result)
	}
}

func TestReassignTask_PicksLeastLoaded(t *testing.T) {
	orch := NewTaskOrchestrator(nil, "least_loaded")

	state := &domain.CollabState{
		DriverID: "cursor",
		AgentInstances: map[string]*domain.AgentInstance{
			"cursor":      {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "working", MaxTasks: 5},
			"gemini":      {InstanceID: "gemini", AgentType: "gemini", Role: domain.RoleWorker, Status: "busy", MaxTasks: 3, CurrentTasks: []int{1}},
			"claude-code": {InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker, Status: "busy", MaxTasks: 3, CurrentTasks: []int{2, 3}},
			"codex":       {InstanceID: "codex", AgentType: "codex", Role: domain.RoleWorker, Status: "idle", MaxTasks: 3},
		},
	}

	task := &domain.Task{ID: 1, Title: "Review code", Status: "blocked", AssignedTo: "gemini"}

	result := orch.ReassignTask(task, state, []string{"gemini"})
	if result != "codex" {
		t.Errorf("expected reassignment to least-loaded codex, got %q", result)
	}
}
