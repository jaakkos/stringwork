package app

import (
	"testing"
	"time"

	"github.com/jaakkos/stringwork/internal/domain"
)

func TestPruneMessages_maxCount(t *testing.T) {
	state := domain.NewCollabState()
	now := time.Now()
	for i := 1; i <= 10; i++ {
		state.Messages = append(state.Messages, domain.Message{
			ID: i, From: "a", To: "b", Content: "x", Timestamp: now.Add(-time.Duration(i) * time.Hour),
		})
	}
	state.NextMsgID = 11

	pruned := PruneMessages(state, 5, 0)
	if pruned != 5 {
		t.Errorf("PruneMessages(maxCount=5): pruned = %d, want 5", pruned)
	}
	if len(state.Messages) != 5 {
		t.Errorf("PruneMessages(maxCount=5): len(Messages) = %d, want 5", len(state.Messages))
	}
	// Should keep newest 5 (IDs 6..10)
	for i, m := range state.Messages {
		if m.ID != 6+i {
			t.Errorf("Messages[%d].ID = %d, want %d", i, m.ID, 6+i)
		}
	}
}

func TestPruneMessages_maxAgeDays(t *testing.T) {
	state := domain.NewCollabState()
	now := time.Now()
	for i := 1; i <= 5; i++ {
		state.Messages = append(state.Messages, domain.Message{
			ID: i, From: "a", To: "b", Content: "recent", Timestamp: now.Add(-time.Duration(i) * time.Hour),
		})
	}
	for i := 6; i <= 10; i++ {
		state.Messages = append(state.Messages, domain.Message{
			ID: i, From: "a", To: "b", Content: "old", Timestamp: now.Add(-time.Duration(i+10) * 24 * time.Hour),
		})
	}

	pruned := PruneMessages(state, 0, 7)
	if pruned != 5 {
		t.Errorf("PruneMessages(maxAgeDays=7): pruned = %d, want 5", pruned)
	}
	if len(state.Messages) != 5 {
		t.Errorf("PruneMessages(maxAgeDays=7): len(Messages) = %d, want 5", len(state.Messages))
	}
}

func TestPruneMessages_nilOrEmpty(t *testing.T) {
	if got := PruneMessages(nil, 5, 0); got != 0 {
		t.Errorf("PruneMessages(nil) = %d, want 0", got)
	}
	empty := domain.NewCollabState()
	if got := PruneMessages(empty, 5, 0); got != 0 {
		t.Errorf("PruneMessages(empty) = %d, want 0", got)
	}
}

func TestEnsureStateMaps(t *testing.T) {
	state := &domain.CollabState{} // nil maps/slices
	EnsureStateMaps(state)

	if state.Presence == nil {
		t.Error("Presence should be initialized")
	}
	if state.Plans == nil {
		t.Error("Plans should be initialized")
	}
	if state.AgentContexts == nil {
		t.Error("AgentContexts should be initialized")
	}
	if state.FileLocks == nil {
		t.Error("FileLocks should be initialized")
	}
	if state.RegisteredAgents == nil {
		t.Error("RegisteredAgents should be initialized")
	}
	if state.Messages == nil {
		t.Error("Messages should be initialized")
	}
	if state.Tasks == nil {
		t.Error("Tasks should be initialized")
	}
	if state.NextMsgID != 1 || state.NextTaskID != 1 || state.NextNoteID != 1 {
		t.Errorf("Next IDs should be 1, got %d %d %d", state.NextMsgID, state.NextTaskID, state.NextNoteID)
	}
}

func TestEnsureStateMaps_nilState(t *testing.T) {
	EnsureStateMaps(nil) // must not panic
}
