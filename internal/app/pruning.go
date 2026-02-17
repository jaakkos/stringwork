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
