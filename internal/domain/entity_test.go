package domain

import (
	"testing"
)

func TestNewCollabState(t *testing.T) {
	s := NewCollabState()
	if s == nil {
		t.Fatal("NewCollabState() returned nil")
	}
	if s.Messages == nil {
		t.Error("Messages should not be nil")
	}
	if s.Tasks == nil {
		t.Error("Tasks should not be nil")
	}
	if s.Presence == nil {
		t.Error("Presence should not be nil")
	}
	if s.SessionNotes == nil {
		t.Error("SessionNotes should not be nil")
	}
	if s.Plans == nil {
		t.Error("Plans should not be nil")
	}
	if s.AgentContexts == nil {
		t.Error("AgentContexts should not be nil")
	}
	if s.FileLocks == nil {
		t.Error("FileLocks should not be nil")
	}
	if s.RegisteredAgents == nil {
		t.Error("RegisteredAgents should not be nil")
	}
	if s.NextMsgID != 1 || s.NextTaskID != 1 || s.NextNoteID != 1 {
		t.Errorf("Next IDs should be 1, got %d %d %d", s.NextMsgID, s.NextTaskID, s.NextNoteID)
	}
}
