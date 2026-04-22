package app

import (
	"time"

	"github.com/jaakkos/stringwork/internal/domain"
)

// PruneMessages removes old messages by TTL and max count. Returns number pruned.
func PruneMessages(state *domain.CollabState, maxCount, maxAgeDays int) int {
	if state == nil || len(state.Messages) == 0 {
		return 0
	}
	pruned := 0
	now := time.Now()
	if maxAgeDays > 0 {
		cutoff := now.AddDate(0, 0, -maxAgeDays)
		filtered := make([]domain.Message, 0, len(state.Messages))
		for _, msg := range state.Messages {
			if msg.Timestamp.After(cutoff) {
				filtered = append(filtered, msg)
			} else {
				pruned++
			}
		}
		state.Messages = filtered
	}
	if maxCount > 0 && len(state.Messages) > maxCount {
		excess := len(state.Messages) - maxCount
		state.Messages = state.Messages[excess:]
		pruned += excess
	}
	return pruned
}

// PrunePresence removes Presence rows for non-driver agents whose LastSeen
// is older than maxAgeDays days. Returns the number of rows removed.
//
// Driver agents (state.DriverID) are always preserved — losing the driver
// presence row would erase the user from get_session_context.
//
// Pass maxAgeDays <= 0 to skip pruning entirely.
func PrunePresence(state *domain.CollabState, maxAgeDays int) int {
	if state == nil || maxAgeDays <= 0 || len(state.Presence) == 0 {
		return 0
	}
	cutoff := time.Now().AddDate(0, 0, -maxAgeDays)
	driver := ConfiguredDriver(state)
	pruned := 0
	for agent, p := range state.Presence {
		if agent == driver || p == nil {
			continue
		}
		if p.LastSeen.Before(cutoff) {
			delete(state.Presence, agent)
			pruned++
		}
	}
	return pruned
}

// PruneInstances removes AgentInstance rows for offline non-driver instances
// that have no current tasks and last heartbeated long enough ago to be
// considered abandoned.
//
// Two retention windows are used:
//   - Task-bound instances (e.g. "claude-code-task-7") use the SHORTER
//     taskBoundMaxAgeHours window. Their lifetime is meant to be tied to
//     a single task; the reap-on-cancel / reap-on-terminal logic in
//     update_task and cancel_agent normally deletes them, so this GC is the
//     fast-path safety net for cases where the in-event reap was missed
//     (server crash, race, older code path).
//   - Static pool instances (e.g. "claude-code") use the LONGER
//     instanceMaxAgeDays window. They're meant to be re-used across tasks
//     and only get deleted when they've been quietly offline for days.
//
// Pass either knob <= 0 to skip pruning that category. Returns the number of
// rows removed.
func PruneInstances(state *domain.CollabState, instanceMaxAgeDays, taskBoundMaxAgeHours int) int {
	if state == nil || len(state.AgentInstances) == 0 {
		return 0
	}
	if instanceMaxAgeDays <= 0 && taskBoundMaxAgeHours <= 0 {
		return 0
	}
	now := time.Now()
	var instanceCutoff, tbCutoff time.Time
	if instanceMaxAgeDays > 0 {
		instanceCutoff = now.AddDate(0, 0, -instanceMaxAgeDays)
	}
	if taskBoundMaxAgeHours > 0 {
		tbCutoff = now.Add(-time.Duration(taskBoundMaxAgeHours) * time.Hour)
	}
	pruned := 0
	for id, inst := range state.AgentInstances {
		if inst == nil || inst.Role == domain.RoleDriver {
			continue
		}
		if inst.Status != "offline" || len(inst.CurrentTasks) != 0 {
			continue
		}
		var cutoff time.Time
		if IsTaskBoundInstance(state, id) {
			if taskBoundMaxAgeHours <= 0 {
				continue
			}
			cutoff = tbCutoff
		} else {
			if instanceMaxAgeDays <= 0 {
				continue
			}
			cutoff = instanceCutoff
		}
		if inst.LastHeartbeat.IsZero() || inst.LastHeartbeat.Before(cutoff) {
			delete(state.AgentInstances, id)
			pruned++
		}
	}
	return pruned
}

// PruneAuditEntries removes audit logs older than the given days.
func PruneAuditEntries(writer AuditWriter, logger interface{ Printf(string, ...interface{}) }, maxAgeDays int) {
	if writer == nil || maxAgeDays <= 0 {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -maxAgeDays)
	count, err := writer.PruneAudit(cutoff)
	if err != nil {
		logger.Printf("Warning: failed to prune audit logs: %v", err)
		return
	}
	if count > 0 {
		logger.Printf("Pruned %d audit logs older than %d days", count, maxAgeDays)
	}
}

// EnsureStateMaps initializes nil maps/slices on state for backward compatibility.
func EnsureStateMaps(state *domain.CollabState) {
	if state == nil {
		return
	}
	if state.Presence == nil {
		state.Presence = make(map[string]*domain.Presence)
	}
	if state.SessionNotes == nil {
		state.SessionNotes = []domain.SessionNote{}
	}
	if state.Plans == nil {
		state.Plans = make(map[string]*domain.Plan)
	}
	if state.AgentContexts == nil {
		state.AgentContexts = make(map[string]*domain.AgentContext)
	}
	if state.FileLocks == nil {
		state.FileLocks = make(map[string]*domain.FileLock)
	}
	if state.RegisteredAgents == nil {
		state.RegisteredAgents = make(map[string]*domain.RegisteredAgent)
	}
	if state.AgentInstances == nil {
		state.AgentInstances = make(map[string]*domain.AgentInstance)
	}
	if state.WorkContexts == nil {
		state.WorkContexts = make(map[string]*domain.WorkContext)
	}
	if state.LastSendByAgent == nil {
		state.LastSendByAgent = make(map[string]time.Time)
	}
	if state.Messages == nil {
		state.Messages = []domain.Message{}
	}
	if state.Tasks == nil {
		state.Tasks = []domain.Task{}
	}
	if state.NextMsgID == 0 {
		state.NextMsgID = 1
	}
	if state.NextTaskID == 0 {
		state.NextTaskID = 1
	}
	if state.NextNoteID == 0 {
		state.NextNoteID = 1
	}
}
