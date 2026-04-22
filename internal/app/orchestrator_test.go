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

// TestAssignTask_StoresParentType verifies that AssignTask sets task.AssignedTo
// to the worker's parent AgentType (e.g. "claude-code"), not the concrete
// InstanceID ("claude-code-1"). This is the key invariant that lets the
// watchdog correlate task-bound children like "claude-code-task-7" with
// their assigned task.
func TestAssignTask_StoresParentType(t *testing.T) {
	orch := NewTaskOrchestrator(nil, "least_loaded")

	state := &domain.CollabState{
		DriverID: "cursor",
		AgentInstances: map[string]*domain.AgentInstance{
			"cursor": {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "working", MaxTasks: 5},
			"claude-code-1": {
				InstanceID: "claude-code-1", AgentType: "claude-code",
				Role: domain.RoleWorker, Status: "idle", MaxTasks: 3,
			},
		},
	}

	task := &domain.Task{ID: 1, Title: "Parent-type test", Status: "pending", AssignedTo: "any"}

	result := orch.AssignTask(task, state)
	if result != "claude-code" {
		t.Fatalf("AssignTask should return parent type, got %q", result)
	}
	if task.AssignedTo != "claude-code" {
		t.Errorf("task.AssignedTo should be parent type 'claude-code', got %q", task.AssignedTo)
	}

	inst := state.AgentInstances["claude-code-1"]
	found := false
	for _, tid := range inst.CurrentTasks {
		if tid == task.ID {
			found = true
			break
		}
	}
	if !found {
		t.Error("concrete instance 'claude-code-1' should still own the task via CurrentTasks")
	}
}

// TestReassignTask_StoresParentType ensures ReassignTask follows the same
// parent-type convention as AssignTask.
func TestReassignTask_StoresParentType(t *testing.T) {
	orch := NewTaskOrchestrator(nil, "least_loaded")

	state := &domain.CollabState{
		DriverID: "cursor",
		AgentInstances: map[string]*domain.AgentInstance{
			"cursor": {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "working", MaxTasks: 5},
			"gemini-1": {
				InstanceID: "gemini-1", AgentType: "gemini",
				Role: domain.RoleWorker, Status: "busy", MaxTasks: 3, CurrentTasks: []int{1},
			},
			"codex-1": {
				InstanceID: "codex-1", AgentType: "codex",
				Role: domain.RoleWorker, Status: "idle", MaxTasks: 3,
			},
		},
	}

	task := &domain.Task{ID: 1, Title: "Reassign test", Status: "blocked", AssignedTo: "gemini"}

	result := orch.ReassignTask(task, state, []string{"gemini"})
	if result != "codex" {
		t.Fatalf("ReassignTask should return parent type 'codex', got %q", result)
	}
	if task.AssignedTo != "codex" {
		t.Errorf("task.AssignedTo should be parent type 'codex', got %q", task.AssignedTo)
	}
}
