package app

import (
	"fmt"
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

// BackoffChecker reports which agent types are currently in failure backoff
// (rate-limited, auth failure, etc.) and should be skipped during assignment.
type BackoffChecker interface {
	BackedOffAgentTypes() []string
}

// SetWorktreeForAssignedTask is called after a task is assigned to set the worktree
// in the task's work context when the assigned worker uses Claude native worktrees.
// It receives the live state, the assigned task, and the chosen instance.
type SetWorktreeForAssignedTask func(state *domain.CollabState, task *domain.Task, inst *domain.AgentInstance)

// TaskOrchestrator assigns new tasks to workers using a strategy.
type TaskOrchestrator struct {
	svc            *CollabService
	strategy       func(*domain.Task, *domain.CollabState) *domain.AgentInstance
	backoffChecker BackoffChecker
	// setWorktree is called after assignment to set WorktreeName on the task's work context when needed.
	setWorktree SetWorktreeForAssignedTask
}

// SetBackoffChecker sets the backoff checker used to skip rate-limited
// agent types during task assignment.
func (o *TaskOrchestrator) SetBackoffChecker(c BackoffChecker) {
	o.backoffChecker = c
}

// SetWorktreeForAssignedTask sets the callback that updates the task's work context
// with the worktree name when the assigned worker uses Claude worktrees.
func (o *TaskOrchestrator) SetWorktreeForAssignedTask(fn SetWorktreeForAssignedTask) {
	o.setWorktree = fn
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
// Rate-limited/backed-off agent types are automatically excluded from assignment.
//
// task.AssignedTo is set to the selected worker's parent AgentType (e.g.
// "claude-code"), not its concrete InstanceID ("claude-code-1"). The chosen
// instance still owns the task via its CurrentTasks list; storing the type
// lets the watchdog and liveness checks correlate cleanly with task-bound
// child workers like "claude-code-task-7".
//
// Returns the assigned parent agent type, or "" if no worker was available.
func (o *TaskOrchestrator) AssignTask(task *domain.Task, state *domain.CollabState) string {
	if state.DriverID == "" {
		return ""
	}
	var inst *domain.AgentInstance
	if o.backoffChecker != nil {
		excludeTypes := o.backoffChecker.BackedOffAgentTypes()
		if len(excludeTypes) > 0 {
			inst = selectWithExclusions(task, state, excludeTypes)
		} else {
			inst = o.strategy(task, state)
		}
	} else {
		inst = o.strategy(task, state)
	}
	if inst == nil {
		return ""
	}
	task.AssignedTo = inst.AgentType
	inst.CurrentTasks = append(inst.CurrentTasks, task.ID)
	inst.Status = "busy"
	inst.LastHeartbeat = time.Now()
	if o.setWorktree != nil {
		o.setWorktree(state, task, inst)
	}
	return inst.AgentType
}

// ReassignTask finds a different worker for a task, excluding the given agent types.
// Used when a worker marks a task as blocked — the task should move to another worker type.
// Returns the new parent agent type, or "" if no alternative worker is available.
func (o *TaskOrchestrator) ReassignTask(task *domain.Task, state *domain.CollabState, excludeTypes []string) string {
	if state.DriverID == "" {
		return ""
	}
	inst := selectWithExclusions(task, state, excludeTypes)
	if inst == nil {
		return ""
	}
	task.AssignedTo = inst.AgentType
	inst.CurrentTasks = append(inst.CurrentTasks, task.ID)
	inst.Status = "busy"
	inst.LastHeartbeat = time.Now()
	if o.setWorktree != nil {
		o.setWorktree(state, task, inst)
	}
	return inst.AgentType
}

// EnsureWorkContextWorktree ensures the task has a work context and sets its
// WorktreeName to the given value. Used by the orchestrator's setWorktree callback
// so that scope and work setup include the Claude worktree when necessary.
// If the task already has a context, it is updated; otherwise a minimal context is created.
func EnsureWorkContextWorktree(state *domain.CollabState, task *domain.Task, worktreeName string) {
	if worktreeName == "" {
		return
	}
	if state.WorkContexts == nil {
		state.WorkContexts = make(map[string]*domain.WorkContext)
	}
	contextID := task.ContextID
	if contextID != "" {
		if wc := state.WorkContexts[contextID]; wc != nil {
			wc.WorktreeName = worktreeName
			return
		}
	}
	contextID = fmt.Sprintf("ctx-%d-%d", task.ID, time.Now().UnixNano())
	state.WorkContexts[contextID] = &domain.WorkContext{
		ID:           contextID,
		TaskID:       task.ID,
		WorktreeName: worktreeName,
		SharedNotes:  make(map[string]string),
	}
	for i := range state.Tasks {
		if state.Tasks[i].ID == task.ID {
			state.Tasks[i].ContextID = contextID
			break
		}
	}
}

// selectWithExclusions picks the least-loaded worker that matches task capabilities
// but is not one of the excluded agent types.
func selectWithExclusions(task *domain.Task, state *domain.CollabState, excludeTypes []string) *domain.AgentInstance {
	excluded := make(map[string]bool, len(excludeTypes))
	for _, t := range excludeTypes {
		excluded[t] = true
	}

	var best *domain.AgentInstance
	for _, inst := range state.AgentInstances {
		if inst == nil || inst.Role != domain.RoleWorker {
			continue
		}
		if excluded[inst.AgentType] {
			continue
		}
		if task.WorkerType != "" && inst.AgentType != task.WorkerType {
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
		if len(inst.CurrentTasks) >= inst.MaxTasks {
			continue
		}
		if best == nil || len(inst.CurrentTasks) < len(best.CurrentTasks) {
			best = inst
		}
	}
	return best
}
