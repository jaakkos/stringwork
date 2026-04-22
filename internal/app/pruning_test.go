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

// --- PrunePresence ---

func TestPrunePresence_byAge(t *testing.T) {
	state := domain.NewCollabState()
	state.DriverID = "cursor"
	now := time.Now()
	state.Presence = map[string]*domain.Presence{
		"cursor":     {Agent: "cursor", LastSeen: now.AddDate(0, 0, -30)},    // driver — keep regardless
		"fresh":      {Agent: "fresh", LastSeen: now.Add(-1 * time.Hour)},    // recent — keep
		"stale":      {Agent: "stale", LastSeen: now.AddDate(0, 0, -10)},     // 10d > 7d — prune
		"borderline": {Agent: "borderline", LastSeen: now.AddDate(0, 0, -8)}, // just past — prune
	}

	pruned := PrunePresence(state, 7)
	if pruned != 2 {
		t.Errorf("PrunePresence(7): pruned = %d, want 2", pruned)
	}
	if _, ok := state.Presence["cursor"]; !ok {
		t.Error("driver presence must be preserved")
	}
	if _, ok := state.Presence["fresh"]; !ok {
		t.Error("fresh presence must be preserved")
	}
	if _, ok := state.Presence["stale"]; ok {
		t.Error("stale presence should be pruned")
	}
	if _, ok := state.Presence["borderline"]; ok {
		t.Error("borderline presence should be pruned")
	}
}

func TestPrunePresence_disabled(t *testing.T) {
	state := domain.NewCollabState()
	state.Presence["old"] = &domain.Presence{Agent: "old", LastSeen: time.Now().AddDate(0, 0, -100)}
	if got := PrunePresence(state, 0); got != 0 {
		t.Errorf("PrunePresence(0) should be no-op, pruned = %d", got)
	}
	if _, ok := state.Presence["old"]; !ok {
		t.Error("nothing should have been pruned with maxAgeDays=0")
	}
}

func TestPrunePresence_nilOrEmpty(t *testing.T) {
	if got := PrunePresence(nil, 7); got != 0 {
		t.Errorf("PrunePresence(nil) = %d, want 0", got)
	}
	empty := domain.NewCollabState()
	if got := PrunePresence(empty, 7); got != 0 {
		t.Errorf("PrunePresence(empty) = %d, want 0", got)
	}
}

// --- PruneInstances ---

func TestPruneInstances_taskBoundFastPath(t *testing.T) {
	state := domain.NewCollabState()
	now := time.Now()
	state.AgentInstances = map[string]*domain.AgentInstance{
		"cursor": {
			InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver,
			Status: "idle", LastHeartbeat: now.AddDate(0, 0, -30), // ancient — driver always preserved
		},
		"claude-code": {
			InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker,
			Status: "offline", LastHeartbeat: now.AddDate(0, 0, -3), // 3d offline, < 7d — keep
		},
		"claude-code-old-pool": {
			InstanceID: "claude-code-old-pool", AgentType: "claude-code", Role: domain.RoleWorker,
			Status: "offline", LastHeartbeat: now.AddDate(0, 0, -10), // 10d offline > 7d — prune
		},
		"claude-code-task-7": {
			InstanceID: "claude-code-task-7", AgentType: "claude-code", Role: domain.RoleWorker,
			Status: "offline", LastHeartbeat: now.Add(-25 * time.Hour), // 25h > 24h — prune (TB fast path)
		},
		"claude-code-task-8": {
			InstanceID: "claude-code-task-8", AgentType: "claude-code", Role: domain.RoleWorker,
			Status: "offline", LastHeartbeat: now.Add(-12 * time.Hour), // 12h < 24h — keep
		},
		"claude-code-task-9": {
			InstanceID: "claude-code-task-9", AgentType: "claude-code", Role: domain.RoleWorker,
			Status: "busy", LastHeartbeat: now.Add(-25 * time.Hour), // not offline — keep
		},
		"claude-code-task-10": {
			InstanceID: "claude-code-task-10", AgentType: "claude-code", Role: domain.RoleWorker,
			Status: "offline", CurrentTasks: []int{10},
			LastHeartbeat: now.AddDate(0, 0, -100), // has tasks — keep
		},
	}

	pruned := PruneInstances(state, 7, 24)
	if pruned != 2 {
		t.Errorf("PruneInstances(7d, 24h): pruned = %d, want 2", pruned)
	}
	if _, ok := state.AgentInstances["claude-code-old-pool"]; ok {
		t.Error("old static-pool offline instance should be pruned")
	}
	if _, ok := state.AgentInstances["claude-code-task-7"]; ok {
		t.Error("task-bound 25h-old instance should be pruned")
	}
	for _, name := range []string{"cursor", "claude-code", "claude-code-task-8", "claude-code-task-9", "claude-code-task-10"} {
		if _, ok := state.AgentInstances[name]; !ok {
			t.Errorf("instance %q should be preserved", name)
		}
	}
}

func TestPruneInstances_disabledKnobs(t *testing.T) {
	state := domain.NewCollabState()
	now := time.Now()
	state.AgentInstances = map[string]*domain.AgentInstance{
		"static-old": {
			InstanceID: "static-old", AgentType: "claude-code", Role: domain.RoleWorker,
			Status: "offline", LastHeartbeat: now.AddDate(0, 0, -30),
		},
		"claude-code-task-7": {
			InstanceID: "claude-code-task-7", AgentType: "claude-code", Role: domain.RoleWorker,
			Status: "offline", LastHeartbeat: now.AddDate(0, 0, -30),
		},
	}

	// instance disabled, task-bound disabled — no-op.
	if got := PruneInstances(state, 0, 0); got != 0 {
		t.Errorf("both knobs disabled should be no-op, pruned = %d", got)
	}
	if len(state.AgentInstances) != 2 {
		t.Error("nothing should have been pruned")
	}

	// only task-bound enabled — only task-bound row pruned.
	if got := PruneInstances(state, 0, 24); got != 1 {
		t.Errorf("only task-bound enabled: pruned = %d, want 1", got)
	}
	if _, ok := state.AgentInstances["static-old"]; !ok {
		t.Error("static-old should be preserved when instanceMaxAgeDays=0")
	}
	if _, ok := state.AgentInstances["claude-code-task-7"]; ok {
		t.Error("task-bound should have been pruned")
	}
}

func TestPruneInstances_nilOrEmpty(t *testing.T) {
	if got := PruneInstances(nil, 7, 24); got != 0 {
		t.Errorf("PruneInstances(nil) = %d, want 0", got)
	}
	empty := domain.NewCollabState()
	if got := PruneInstances(empty, 7, 24); got != 0 {
		t.Errorf("PruneInstances(empty) = %d, want 0", got)
	}
}

func TestPruneInstances_driverNeverPruned(t *testing.T) {
	state := domain.NewCollabState()
	state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver,
		Status: "offline", LastHeartbeat: time.Now().AddDate(0, 0, -100),
	}
	if got := PruneInstances(state, 7, 24); got != 0 {
		t.Errorf("driver row should never be pruned, pruned = %d", got)
	}
	if _, ok := state.AgentInstances["cursor"]; !ok {
		t.Error("driver row was pruned")
	}
}
