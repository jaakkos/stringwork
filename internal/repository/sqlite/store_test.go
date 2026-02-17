package sqlite

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jaakkos/stringwork/internal/domain"
)

func TestStoreRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.sqlite")

	store, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() {
		if c, ok := store.(*Store); ok {
			_ = c.Close()
		}
	}()

	state := domain.NewCollabState()
	state.Messages = append(state.Messages, domain.Message{
		ID: 1, From: "cursor", To: "claude-code", Content: "hello", Timestamp: time.Now(), Read: false,
	})
	state.Tasks = append(state.Tasks, domain.Task{
		ID: 1, Title: "Test", Description: "Desc", Status: "pending", AssignedTo: "any",
		CreatedBy: "cursor", CreatedAt: time.Now(), UpdatedAt: time.Now(), Priority: 3,
	})
	state.NextMsgID = 2
	state.NextTaskID = 2

	if err := store.Save(state); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Messages) != 1 {
		t.Errorf("len(Messages) = %d, want 1", len(loaded.Messages))
	} else if loaded.Messages[0].Content != "hello" {
		t.Errorf("Messages[0].Content = %q, want \"hello\"", loaded.Messages[0].Content)
	}
	if len(loaded.Tasks) != 1 {
		t.Errorf("len(Tasks) = %d, want 1", len(loaded.Tasks))
	} else if loaded.Tasks[0].Title != "Test" {
		t.Errorf("Tasks[0].Title = %q, want \"Test\"", loaded.Tasks[0].Title)
	}
	if loaded.NextMsgID != 2 || loaded.NextTaskID != 2 {
		t.Errorf("NextMsgID=%d NextTaskID=%d, want 2, 2", loaded.NextMsgID, loaded.NextTaskID)
	}
}

func TestStoreClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "closed.sqlite")

	store, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	st := store.(*Store)
	if err := st.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if st.db != nil {
		t.Error("Close should set db to nil")
	}
	// Second Close is no-op
	if err := st.Close(); err != nil {
		t.Errorf("Second Close: %v", err)
	}
}

func TestSelfHealingIDs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "heal.sqlite")

	store, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() {
		if c, ok := store.(*Store); ok {
			_ = c.Close()
		}
	}()

	now := time.Now()

	// Save state with counters intentionally behind the actual MAX(id).
	// This simulates the drift bug: meta says next_msg_id=5 but message ID 10 exists.
	state := domain.NewCollabState()
	state.Messages = append(state.Messages,
		domain.Message{ID: 3, From: "a", To: "b", Content: "m1", Timestamp: now},
		domain.Message{ID: 7, From: "a", To: "b", Content: "m2", Timestamp: now},
		domain.Message{ID: 10, From: "a", To: "b", Content: "m3", Timestamp: now},
	)
	state.Tasks = append(state.Tasks,
		domain.Task{ID: 2, Title: "t1", Status: "pending", AssignedTo: "any", CreatedBy: "a", CreatedAt: now, UpdatedAt: now, Priority: 3},
		domain.Task{ID: 8, Title: "t2", Status: "done", AssignedTo: "any", CreatedBy: "a", CreatedAt: now, UpdatedAt: now, Priority: 3},
	)
	state.SessionNotes = append(state.SessionNotes,
		domain.SessionNote{ID: 4, Author: "a", Content: "n1", Category: "note", Timestamp: now},
	)

	// Set counters to values BEHIND the actual data (simulating the bug)
	state.NextMsgID = 5  // should be 11 (max=10, so 10+1)
	state.NextTaskID = 3 // should be 9  (max=8, so 8+1)
	state.NextNoteID = 2 // should be 5  (max=4, so 4+1)

	if err := store.Save(state); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Load should self-heal the counters
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.NextMsgID != 11 {
		t.Errorf("NextMsgID = %d, want 11 (self-healed from stale counter 5)", loaded.NextMsgID)
	}
	if loaded.NextTaskID != 9 {
		t.Errorf("NextTaskID = %d, want 9 (self-healed from stale counter 3)", loaded.NextTaskID)
	}
	if loaded.NextNoteID != 5 {
		t.Errorf("NextNoteID = %d, want 5 (self-healed from stale counter 2)", loaded.NextNoteID)
	}
}

func TestSelfHealingIDs_CorrectCountersUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "correct.sqlite")

	store, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() {
		if c, ok := store.(*Store); ok {
			_ = c.Close()
		}
	}()

	now := time.Now()
	state := domain.NewCollabState()
	state.Messages = append(state.Messages,
		domain.Message{ID: 1, From: "a", To: "b", Content: "m1", Timestamp: now},
	)
	state.NextMsgID = 2  // already correct
	state.NextTaskID = 1 // no tasks, already correct
	state.NextNoteID = 1 // no notes, already correct

	if err := store.Save(state); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.NextMsgID != 2 {
		t.Errorf("NextMsgID = %d, want 2 (should remain unchanged when correct)", loaded.NextMsgID)
	}
	if loaded.NextTaskID != 1 {
		t.Errorf("NextTaskID = %d, want 1 (should remain unchanged when correct)", loaded.NextTaskID)
	}
	if loaded.NextNoteID != 1 {
		t.Errorf("NextNoteID = %d, want 1 (should remain unchanged when correct)", loaded.NextNoteID)
	}
}

func TestSelfHealingIDs_EmptyState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.sqlite")

	store, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() {
		if c, ok := store.(*Store); ok {
			_ = c.Close()
		}
	}()

	state := domain.NewCollabState()
	// Empty state, counters at 1
	if err := store.Save(state); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.NextMsgID != 1 {
		t.Errorf("NextMsgID = %d, want 1 (empty state)", loaded.NextMsgID)
	}
	if loaded.NextTaskID != 1 {
		t.Errorf("NextTaskID = %d, want 1 (empty state)", loaded.NextTaskID)
	}
	if loaded.NextNoteID != 1 {
		t.Errorf("NextNoteID = %d, want 1 (empty state)", loaded.NextNoteID)
	}
}

func TestNew_failsOnInvalidDir(t *testing.T) {
	// Parent path is a file (e.g. /dev/null), so MkdirAll fails
	path := filepath.Join(os.DevNull, "sub", "state.sqlite")
	_, err := New(path)
	if err == nil {
		t.Error("New should fail when parent is not a directory")
	}
}
