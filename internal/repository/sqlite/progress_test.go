package sqlite

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jaakkos/stringwork/internal/domain"
)

func TestStore_TaskProgressRoundTrip(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	storeIface, err := New(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	store := storeIface.(*Store)
	defer func() {
		store.db.Close()
		os.Remove(dbPath)
	}()

	now := time.Now().Truncate(time.Second)
	state := domain.NewCollabState()
	state.Tasks = append(state.Tasks, domain.Task{
		ID:                  1,
		Title:               "Progress test task",
		Description:         "testing progress fields",
		Status:              "in_progress",
		AssignedTo:          "claude-code",
		CreatedBy:           "cursor",
		CreatedAt:           now,
		UpdatedAt:           now,
		Priority:            3,
		ExpectedDurationSec: 300,
		ProgressDescription: "Working on unit tests",
		ProgressPercent:     65,
		LastProgressAt:      now,
	})
	state.NextTaskID = 2

	if err := store.Save(state); err != nil {
		t.Fatalf("failed to save: %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("failed to load: %v", err)
	}

	if len(loaded.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(loaded.Tasks))
	}

	task := loaded.Tasks[0]
	if task.ExpectedDurationSec != 300 {
		t.Errorf("expected_duration_sec = %d, want 300", task.ExpectedDurationSec)
	}
	if task.ProgressDescription != "Working on unit tests" {
		t.Errorf("progress_description = %q, want 'Working on unit tests'", task.ProgressDescription)
	}
	if task.ProgressPercent != 65 {
		t.Errorf("progress_percent = %d, want 65", task.ProgressPercent)
	}
	if task.LastProgressAt.IsZero() {
		t.Error("last_progress_at should not be zero")
	}
}

func TestStore_AgentProgressRoundTrip(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	storeIface, err := New(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	store := storeIface.(*Store)
	defer func() {
		store.db.Close()
		os.Remove(dbPath)
	}()

	now := time.Now().Truncate(time.Second)
	state := domain.NewCollabState()
	state.AgentInstances["claude-code-1"] = &domain.AgentInstance{
		InstanceID:         "claude-code-1",
		AgentType:          "claude-code",
		Role:               domain.RoleWorker,
		MaxTasks:           1,
		Status:             "busy",
		CurrentTasks:       []int{1},
		LastHeartbeat:      now,
		Progress:           "Implementing auth middleware",
		ProgressStep:       3,
		ProgressTotalSteps: 5,
		ProgressUpdatedAt:  now,
	}

	if err := store.Save(state); err != nil {
		t.Fatalf("failed to save: %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("failed to load: %v", err)
	}

	ai, ok := loaded.AgentInstances["claude-code-1"]
	if !ok {
		t.Fatal("expected agent instance claude-code-1")
	}
	if ai.Progress != "Implementing auth middleware" {
		t.Errorf("progress = %q, want 'Implementing auth middleware'", ai.Progress)
	}
	if ai.ProgressStep != 3 {
		t.Errorf("progress_step = %d, want 3", ai.ProgressStep)
	}
	if ai.ProgressTotalSteps != 5 {
		t.Errorf("progress_total_steps = %d, want 5", ai.ProgressTotalSteps)
	}
	if ai.ProgressUpdatedAt.IsZero() {
		t.Error("progress_updated_at should not be zero")
	}
}

func TestStore_EmptyProgressFields(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	storeIface, err := New(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	store := storeIface.(*Store)
	defer func() {
		store.db.Close()
		os.Remove(dbPath)
	}()

	now := time.Now().Truncate(time.Second)
	state := domain.NewCollabState()
	state.Tasks = append(state.Tasks, domain.Task{
		ID:         1,
		Title:      "No progress task",
		Status:     "pending",
		AssignedTo: "any",
		CreatedBy:  "cursor",
		CreatedAt:  now,
		UpdatedAt:  now,
		Priority:   3,
	})
	state.NextTaskID = 2

	state.AgentInstances["worker-1"] = &domain.AgentInstance{
		InstanceID:    "worker-1",
		AgentType:     "claude-code",
		Role:          domain.RoleWorker,
		MaxTasks:      1,
		Status:        "idle",
		CurrentTasks:  []int{},
		LastHeartbeat: now,
	}

	if err := store.Save(state); err != nil {
		t.Fatalf("failed to save: %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("failed to load: %v", err)
	}

	task := loaded.Tasks[0]
	if task.ExpectedDurationSec != 0 {
		t.Errorf("expected_duration_sec = %d, want 0", task.ExpectedDurationSec)
	}
	if task.ProgressPercent != 0 {
		t.Errorf("progress_percent = %d, want 0", task.ProgressPercent)
	}

	ai := loaded.AgentInstances["worker-1"]
	if ai.Progress != "" {
		t.Errorf("progress = %q, want empty", ai.Progress)
	}
	if ai.ProgressStep != 0 {
		t.Errorf("progress_step = %d, want 0", ai.ProgressStep)
	}
}
