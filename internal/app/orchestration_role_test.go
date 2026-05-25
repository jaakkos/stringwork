package app

import (
	"testing"

	"github.com/jaakkos/stringwork/internal/domain"
	"github.com/jaakkos/stringwork/internal/policy"
)

func TestIsSpawnedWorkerAgent(t *testing.T) {
	orch := &policy.OrchestrationConfig{
		Driver: DriverAuto,
		Workers: []policy.WorkerConfig{
			{Type: "claude-code", Instances: 2},
			{Type: "codex", Instances: 1},
		},
	}
	types := WorkerTypesFromOrch(orch)
	state := domain.NewCollabState()
	state.AgentInstances = map[string]*domain.AgentInstance{
		"claude-code-1": {
			InstanceID: "claude-code-1", AgentType: "claude-code", Role: domain.RoleWorker,
		},
		"claude-code-task-9": {
			InstanceID: "claude-code-task-9", AgentType: "claude-code", Role: domain.RoleWorker,
		},
	}

	tests := []struct {
		agent string
		want  bool
	}{
		{"claude-code-task-9", true},
		{"claude-code-1", true},
		{"codex-1", true},
		{"claude-code", false},
		{"cursor", false},
		{"codex", false},
	}
	for _, tt := range tests {
		got := IsSpawnedWorkerAgent(state, tt.agent, types, nil)
		if got != tt.want {
			t.Errorf("IsSpawnedWorkerAgent(%q) = %v, want %v", tt.agent, got, tt.want)
		}
	}
}

func TestPromoteHumanDriver_Auto(t *testing.T) {
	orch := &policy.OrchestrationConfig{
		Driver: DriverAuto,
		Workers: []policy.WorkerConfig{
			{Type: "claude-code", Instances: 1},
		},
	}
	state := domain.NewCollabState()
	state.DriverID = "cursor"
	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver,
	}

	PromoteHumanDriver(state, "claude-code", orch)

	if state.DriverID != "claude-code" {
		t.Errorf("DriverID = %q, want claude-code", state.DriverID)
	}
	if inst := state.AgentInstances["claude-code"]; inst == nil || inst.Role != domain.RoleDriver {
		t.Errorf("expected claude-code driver row, got %+v", inst)
	}
	if inst := state.AgentInstances["cursor"]; inst == nil || inst.Role == domain.RoleDriver {
		t.Errorf("expected cursor demoted from driver, got role %q", inst.Role)
	}
}

func TestPromoteHumanDriver_SkipsSpawned(t *testing.T) {
	orch := &policy.OrchestrationConfig{Driver: DriverAuto, Workers: []policy.WorkerConfig{{Type: "codex"}}}
	state := domain.NewCollabState()
	state.DriverID = "cursor"
	PromoteHumanDriver(state, "codex-task-3", orch)
	if state.DriverID != "cursor" {
		t.Errorf("spawned agent must not steal driver; DriverID = %q", state.DriverID)
	}
}
