package app

import (
	"time"

	"github.com/jaakkos/stringwork/internal/domain"
)

// AssignmentStrategy selects which worker instance should get a task.
type AssignmentStrategy interface {
	SelectWorker(task *domain.Task, state *domain.CollabState) *domain.AgentInstance
}

// CapabilityMatchStrategy assigns to a worker that has all required task capabilities (if any).
// Otherwise falls back to least loaded.
func CapabilityMatchStrategy(task *domain.Task, state *domain.CollabState) *domain.AgentInstance {
	return selectByCapabilityOrLoad(task, state)
}

// LeastLoadedStrategy assigns to the worker instance with the fewest current tasks.
func LeastLoadedStrategy(task *domain.Task, state *domain.CollabState) *domain.AgentInstance {
	return selectLeastLoaded(state, task.Capabilities)
}

// RoundRobinStrategy is not deterministic without an index; we use least loaded as a proxy.
func RoundRobinStrategy(task *domain.Task, state *domain.CollabState) *domain.AgentInstance {
	return selectLeastLoaded(state, task.Capabilities)
}

func selectByCapabilityOrLoad(task *domain.Task, state *domain.CollabState) *domain.AgentInstance {
	candidates := make([]*domain.AgentInstance, 0)
	for _, inst := range state.AgentInstances {
		if inst == nil || inst.Role != domain.RoleWorker {
			continue
		}
		if len(task.Capabilities) > 0 {
			hasAll := true
			for _, need := range task.Capabilities {
				found := false
				for _, c := range inst.Capabilities {
					if c == need {
						found = true
						break
					}
				}
				if !found {
					hasAll = false
					break
				}
			}
			if !hasAll {
				continue
			}
		}
		if task.WorkerType != "" && inst.AgentType != task.WorkerType {
			continue
		}
		if len(inst.CurrentTasks) >= inst.MaxTasks {
			continue
		}
		candidates = append(candidates, inst)
	}
	if len(candidates) == 0 {
		return nil
	}
	// Pick least loaded
	best := candidates[0]
	for _, c := range candidates[1:] {
		if len(c.CurrentTasks) < len(best.CurrentTasks) {
			best = c
		}
	}
	return best
}

func selectLeastLoaded(state *domain.CollabState, requiredCaps []string) *domain.AgentInstance {
	var best *domain.AgentInstance
	for _, inst := range state.AgentInstances {
		if inst == nil || inst.Role != domain.RoleWorker {
			continue
		}
		if len(requiredCaps) > 0 {
			hasAll := true
			for _, need := range requiredCaps {
				found := false
				for _, c := range inst.Capabilities {
					if c == need {
						found = true
						break
					}
				}
				if !found {
					hasAll = false
					break
				}
			}
			if !hasAll {
				continue
			}
		}
		if len(inst.CurrentTasks) >= inst.MaxTasks {
			continue
		}
		if best == nil || len(inst.CurrentTasks) < len(best.CurrentTasks) {
			best = inst
		}
	}
	return best
}

// TaskOrchestrator assigns new tasks to workers using a strategy.
type TaskOrchestrator struct {
	svc      *CollabService
	strategy func(*domain.Task, *domain.CollabState) *domain.AgentInstance
}

// NewTaskOrchestrator creates an orchestrator. Strategy name: capability_match, least_loaded, round_robin.
func NewTaskOrchestrator(svc *CollabService, strategyName string) *TaskOrchestrator {
	var strategy func(*domain.Task, *domain.CollabState) *domain.AgentInstance
	switch strategyName {
	case "least_loaded":
		strategy = LeastLoadedStrategy
	case "round_robin":
		strategy = RoundRobinStrategy
	default:
		strategy = CapabilityMatchStrategy
	}
	return &TaskOrchestrator{svc: svc, strategy: strategy}
}

// AssignTask assigns a task to the best available worker and updates state (AssignedTo).
// Call from within a state-mutating fn; the given task and state are the live references.
// Returns the instance ID assigned, or "" if none.
func (o *TaskOrchestrator) AssignTask(task *domain.Task, state *domain.CollabState) string {
	if state.DriverID == "" {
		return ""
	}
	inst := o.strategy(task, state)
	if inst == nil {
		return ""
	}
	task.AssignedTo = inst.InstanceID
	inst.CurrentTasks = append(inst.CurrentTasks, task.ID)
	inst.Status = "busy"
	inst.LastHeartbeat = time.Now()
	return inst.InstanceID
}
