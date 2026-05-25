package app

import (
	"strings"

	"github.com/jaakkos/stringwork/internal/domain"
	"github.com/jaakkos/stringwork/internal/policy"
)

// DriverAuto is the orchestration.driver value that promotes whichever
// human-facing MCP client connects (parent agent type) to runtime driver.
// Stringwork-spawned worker instances never become driver under this mode.
const DriverAuto = "auto"

// IsAutoDriver reports whether orchestration uses dynamic human-as-driver mode.
func IsAutoDriver(driver string) bool {
	return strings.EqualFold(strings.TrimSpace(driver), DriverAuto)
}

// WorkerTypesFromOrch returns configured worker type names (e.g. claude-code, codex).
func WorkerTypesFromOrch(orch *policy.OrchestrationConfig) []string {
	if orch == nil {
		return nil
	}
	types := make([]string, 0, len(orch.Workers))
	for _, w := range orch.Workers {
		if w.Type != "" {
			types = append(types, w.Type)
		}
	}
	return types
}

// IsSpawnedWorkerAgent reports whether agent identifies a Stringwork-managed
// worker process (task-bound instance, pool slot, or active spawn) rather than
// a human using an IDE/CLI directly.
//
// Human drivers use the parent agent type (cursor, claude-code, codex) in tool
// calls. Workers use claude-code-task-N, codex-1, etc.
func IsSpawnedWorkerAgent(
	state *domain.CollabState,
	agent string,
	workerTypes []string,
	isProcessRunning func(instanceID string) bool,
) bool {
	if agent == "" {
		return false
	}
	if IsTaskBoundInstance(state, agent) {
		return true
	}
	if _, ok := StripTaskBoundSuffix(agent); ok {
		return true
	}
	for _, wt := range workerTypes {
		if isPoolInstanceID(wt, agent) {
			return true
		}
	}
	if isProcessRunning != nil && isProcessRunning(agent) {
		return true
	}
	if state != nil {
		if inst, ok := state.AgentInstances[agent]; ok && inst != nil && inst.Role == domain.RoleWorker {
			// Parent-type name reused as instance ID only for human MCP — workers
			// use suffixed / task-bound IDs. A RoleWorker row keyed exactly as a
			// worker type without suffix is a bootstrap pool slot, not the human.
			if agent != inst.AgentType || isPoolInstanceID(inst.AgentType, agent) {
				return true
			}
		}
	}
	return false
}

// isPoolInstanceID reports whether instanceID is a static pool slot like
// "claude-code-1" (not the bare parent type "claude-code").
func isPoolInstanceID(agentType, instanceID string) bool {
	if agentType == "" || instanceID == "" || instanceID == agentType {
		return false
	}
	if strings.Contains(instanceID, "-task-") {
		return false
	}
	prefix := agentType + "-"
	if !strings.HasPrefix(instanceID, prefix) {
		return false
	}
	suffix := instanceID[len(prefix):]
	if suffix == "" {
		return false
	}
	for _, r := range suffix {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// PromoteHumanDriver sets the runtime driver to the human-connected parent
// agent type. No-op when agent is a spawned worker identifier.
func PromoteHumanDriver(state *domain.CollabState, agent string, orch *policy.OrchestrationConfig) {
	if state == nil || agent == "" || orch == nil {
		return
	}
	if !IsAutoDriver(orch.Driver) {
		return
	}
	if IsSpawnedWorkerAgent(state, agent, WorkerTypesFromOrch(orch), nil) {
		return
	}
	parent := ResolveParentAgentType(state, agent)
	if parent == "" {
		parent = agent
	}
	state.DriverID = parent

	// Demote previous driver row to idle (keep row for history / presence).
	for id, inst := range state.AgentInstances {
		if inst == nil || id == parent {
			continue
		}
		if inst.Role == domain.RoleDriver {
			inst.Role = domain.RoleWorker
			if inst.Status == "" {
				inst.Status = "idle"
			}
		}
	}

	if inst, ok := state.AgentInstances[parent]; ok && inst != nil {
		inst.Role = domain.RoleDriver
		inst.AgentType = parent
		inst.InstanceID = parent
		inst.Status = "idle"
		return
	}
	state.AgentInstances[parent] = &domain.AgentInstance{
		InstanceID: parent,
		AgentType:  parent,
		Role:       domain.RoleDriver,
		Capabilities: []string{
			"orchestrate", "code-edit", "code-review", "search", "terminal",
		},
		Status: "idle",
	}
}

// EffectiveDriverID returns the driver agent type for messaging and instructions.
func EffectiveDriverID(state *domain.CollabState, configuredDriver string) string {
	if state != nil && state.DriverID != "" {
		return state.DriverID
	}
	if configuredDriver != "" && !IsAutoDriver(configuredDriver) {
		return configuredDriver
	}
	return "cursor"
}
