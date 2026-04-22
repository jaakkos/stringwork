package collab

import (
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/jaakkos/stringwork/internal/domain"
)

func TestReplayTask_BlockedTask(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{
			ID: 1, Title: "Blocked task", Status: "blocked",
			AssignedTo: "claude-code", CreatedBy: "cursor",
			FailureCount:        3,
			FailureReason:       "watchdog: agent dead",
			LastFailure:         time.Now().Add(-5 * time.Minute),
			BlockedBy:           "auto-blocked after 3 failures",
			ResultSummary:       "some old summary",
			ProgressDescription: "was at 50%",
			ProgressPercent:     50,
		},
	}
	repo.state.NextTaskID = 2

	srv := testServer(svc, logger)

	args := map[string]any{
		"id":         float64(1),
		"updated_by": "cursor",
	}

	result, err := callTool(t, srv, "replay_task", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !strings.Contains(text, "replayed") {
		t.Errorf("result should mention replayed: %s", text)
	}
	if !strings.Contains(text, "any") {
		t.Errorf("result should show default assignee 'any': %s", text)
	}

	task := repo.state.Tasks[0]
	if task.Status != "pending" {
		t.Errorf("Status = %q, want pending", task.Status)
	}
	if task.FailureCount != 0 {
		t.Errorf("FailureCount = %d, want 0", task.FailureCount)
	}
	if task.FailureReason != "" {
		t.Errorf("FailureReason = %q, want empty", task.FailureReason)
	}
	if !task.LastFailure.IsZero() {
		t.Errorf("LastFailure should be zero, got %v", task.LastFailure)
	}
	if task.BlockedBy != "" {
		t.Errorf("BlockedBy = %q, want empty", task.BlockedBy)
	}
	if task.ResultSummary != "" {
		t.Errorf("ResultSummary = %q, want empty", task.ResultSummary)
	}
	if task.ProgressDescription != "" {
		t.Errorf("ProgressDescription = %q, want empty", task.ProgressDescription)
	}
	if task.ProgressPercent != 0 {
		t.Errorf("ProgressPercent = %d, want 0", task.ProgressPercent)
	}
	if task.AssignedTo != "any" {
		t.Errorf("AssignedTo = %q, want any (default)", task.AssignedTo)
	}
}

func TestReplayTask_KeepAssignee(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{
			ID: 1, Title: "Blocked task", Status: "blocked",
			AssignedTo: "claude-code", CreatedBy: "cursor",
			FailureCount: 2,
		},
	}

	srv := testServer(svc, logger)

	args := map[string]any{
		"id":          float64(1),
		"updated_by":  "cursor",
		"reassign_to": "keep",
	}

	result, err := callTool(t, srv, "replay_task", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !strings.Contains(text, "claude-code") {
		t.Errorf("result should show kept assignee: %s", text)
	}

	if repo.state.Tasks[0].AssignedTo != "claude-code" {
		t.Errorf("AssignedTo = %q, want claude-code (kept)", repo.state.Tasks[0].AssignedTo)
	}
}

func TestReplayTask_ReassignToSpecificAgent(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{
			ID: 1, Title: "Blocked task", Status: "blocked",
			AssignedTo: "claude-code", CreatedBy: "cursor",
		},
	}

	srv := testServer(svc, logger)

	args := map[string]any{
		"id":          float64(1),
		"updated_by":  "cursor",
		"reassign_to": "codex",
	}

	result, err := callTool(t, srv, "replay_task", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !strings.Contains(text, "codex") {
		t.Errorf("result should show new assignee: %s", text)
	}

	if repo.state.Tasks[0].AssignedTo != "codex" {
		t.Errorf("AssignedTo = %q, want codex", repo.state.Tasks[0].AssignedTo)
	}
}

func TestReplayTask_PendingTaskAllowed(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{
			ID: 1, Title: "Pending task", Status: "pending",
			AssignedTo: "claude-code", CreatedBy: "cursor",
			FailureCount: 1,
		},
	}

	srv := testServer(svc, logger)

	args := map[string]any{
		"id":         float64(1),
		"updated_by": "cursor",
	}

	_, err := callTool(t, srv, "replay_task", args)
	if err != nil {
		t.Fatalf("should allow replaying pending tasks: %v", err)
	}

	if repo.state.Tasks[0].FailureCount != 0 {
		t.Errorf("FailureCount = %d, want 0", repo.state.Tasks[0].FailureCount)
	}
}

func TestReplayTask_InProgressRejected(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{
			ID: 1, Title: "Active task", Status: "in_progress",
			AssignedTo: "claude-code", CreatedBy: "cursor",
		},
	}

	srv := testServer(svc, logger)

	args := map[string]any{
		"id":         float64(1),
		"updated_by": "cursor",
	}

	_, err := callTool(t, srv, "replay_task", args)
	if err == nil {
		t.Fatal("should reject in_progress tasks")
	}
	if !strings.Contains(err.Error(), "in_progress") {
		t.Errorf("error should mention in_progress: %v", err)
	}

	if repo.state.Tasks[0].Status != "in_progress" {
		t.Error("status should not change on error")
	}
}

func TestReplayTask_CompletedRejected(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{
			ID: 1, Title: "Done task", Status: "completed",
			AssignedTo: "cursor", CreatedBy: "cursor",
		},
	}

	srv := testServer(svc, logger)

	args := map[string]any{
		"id":         float64(1),
		"updated_by": "cursor",
	}

	_, err := callTool(t, srv, "replay_task", args)
	if err == nil {
		t.Fatal("should reject completed tasks")
	}
	if !strings.Contains(err.Error(), "completed") {
		t.Errorf("error should mention completed: %v", err)
	}
}

func TestReplayTask_CancelledRejected(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)

	// Pre-populate with cancelled task
	svc.Run(func(state *domain.CollabState) error {
		state.Tasks = append(state.Tasks, domain.Task{
			ID: 1, Title: "Cancelled task", Status: "cancelled",
			AssignedTo: "cursor", CreatedBy: "cursor",
		})
		state.NextTaskID = 2
		return nil
	})

	srv := testServer(svc, logger)

	args := map[string]any{
		"id":         float64(1),
		"updated_by": "cursor",
	}

	_, err := callTool(t, srv, "replay_task", args)
	if err == nil {
		t.Fatal("should reject cancelled tasks")
	}
}

func TestReplayTask_NotFound(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	args := map[string]any{
		"id":         float64(999),
		"updated_by": "cursor",
	}

	_, err := callTool(t, srv, "replay_task", args)
	if err == nil {
		t.Fatal("expected error for non-existent task")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention not found: %v", err)
	}
}

func TestReplayTask_MissingUpdatedBy(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)

	svc.Run(func(state *domain.CollabState) error {
		state.Tasks = append(state.Tasks, domain.Task{
			ID: 1, Title: "Task", Status: "blocked",
			AssignedTo: "cursor", CreatedBy: "cursor",
		})
		state.NextTaskID = 2
		return nil
	})

	srv := testServer(svc, logger)

	args := map[string]any{
		"id": float64(1),
	}

	_, err := callTool(t, srv, "replay_task", args)
	if err == nil {
		t.Fatal("expected error for missing updated_by")
	}
}

func TestReplayTask_InvalidUpdatedBy(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)

	svc.Run(func(state *domain.CollabState) error {
		state.Tasks = append(state.Tasks, domain.Task{
			ID: 1, Title: "Task", Status: "blocked",
			AssignedTo: "cursor", CreatedBy: "cursor",
		})
		state.NextTaskID = 2
		return nil
	})

	srv := testServer(svc, logger)

	args := map[string]any{
		"id":         float64(1),
		"updated_by": "nonexistent-agent",
	}

	_, err := callTool(t, srv, "replay_task", args)
	if err == nil {
		t.Fatal("expected error for invalid updated_by agent")
	}
}

func TestReplayTask_InvalidReassignTo(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)

	svc.Run(func(state *domain.CollabState) error {
		state.Tasks = append(state.Tasks, domain.Task{
			ID: 1, Title: "Task", Status: "blocked",
			AssignedTo: "cursor", CreatedBy: "cursor",
		})
		state.NextTaskID = 2
		return nil
	})

	srv := testServer(svc, logger)

	args := map[string]any{
		"id":          float64(1),
		"updated_by":  "cursor",
		"reassign_to": "totally-invalid-agent",
	}

	_, err := callTool(t, srv, "replay_task", args)
	if err == nil {
		t.Fatal("expected error for invalid reassign_to agent")
	}
}

func TestReplayTask_ReviewGatePreserved(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{
			ID: 1, Title: "Reviewed task", Status: "blocked",
			AssignedTo: "claude-code", CreatedBy: "cursor",
			RequiresReview: true, ReviewStatus: "rejected", ReviewedBy: "cursor",
			FailureCount: 2,
		},
	}

	srv := testServer(svc, logger)

	args := map[string]any{
		"id":         float64(1),
		"updated_by": "cursor",
	}

	_, err := callTool(t, srv, "replay_task", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	task := repo.state.Tasks[0]
	if !task.RequiresReview {
		t.Error("RequiresReview should be preserved")
	}
	if task.ReviewStatus != "pending" {
		t.Errorf("ReviewStatus = %q, want pending (reset)", task.ReviewStatus)
	}
	if task.ReviewedBy != "" {
		t.Errorf("ReviewedBy = %q, want empty (reset)", task.ReviewedBy)
	}
}

func TestReplayTask_NoReviewGateCleared(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{
			ID: 1, Title: "No review task", Status: "blocked",
			AssignedTo: "claude-code", CreatedBy: "cursor",
			RequiresReview: false,
		},
	}

	srv := testServer(svc, logger)

	args := map[string]any{
		"id":         float64(1),
		"updated_by": "cursor",
	}

	_, err := callTool(t, srv, "replay_task", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	task := repo.state.Tasks[0]
	if task.ReviewStatus != "" {
		t.Errorf("ReviewStatus = %q, want empty for non-review task", task.ReviewStatus)
	}
	if task.ReviewedBy != "" {
		t.Errorf("ReviewedBy = %q, want empty", task.ReviewedBy)
	}
}

// TestReplayTask_PreservesPreviousOutputAcrossAgentTypes (M5
// guardrail) — when a task is replayed and reassigned from one agent
// type to another (e.g. claude-code → codex), the previously captured
// worker output stored on the WorkContext must survive the replay so
// the new assignee can read what the prior worker did.
func TestReplayTask_PreservesPreviousOutputAcrossAgentTypes(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{
			ID: 1, Title: "Cross-type replay", Status: "blocked",
			AssignedTo:    "claude-code",
			CreatedBy:     "cursor",
			ContextID:     "ctx-1",
			FailureCount:  1,
			FailureReason: "watchdog timeout",
		},
	}
	repo.state.WorkContexts = map[string]*domain.WorkContext{
		"ctx-1": {
			ID:             "ctx-1",
			TaskID:         1,
			PreviousOutput: "claude-code transcript: started step 1, hit error E",
			SharedNotes:    map[string]string{},
		},
	}
	repo.state.NextTaskID = 2

	srv := testServer(svc, logger)
	args := map[string]any{
		"id":          float64(1),
		"updated_by":  "cursor",
		"reassign_to": "codex",
	}
	if _, err := callTool(t, srv, "replay_task", args); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	task := repo.state.Tasks[0]
	if task.AssignedTo != "codex" {
		t.Errorf("expected reassign to codex, got %q", task.AssignedTo)
	}
	wc := repo.state.WorkContexts[task.ContextID]
	if wc == nil {
		t.Fatalf("expected WorkContext to survive replay; ContextID=%q", task.ContextID)
	}
	if !strings.Contains(wc.PreviousOutput, "started step 1") {
		t.Errorf("PreviousOutput must survive replay across agent types; got %q", wc.PreviousOutput)
	}
}

// TestTaskBoundCycle_PendingInProgressBlockedRepeated (M5 guardrail) —
// drives a task through pending → in_progress → blocked → pending →
// in_progress → completed, asserting that the AgentInstance.CurrentTasks
// slice never accumulates duplicates and never leaks between cycles.
// This catches the recurring class of CurrentTasks corruption bugs that
// motivated the "always-scan removal" pattern in handoff/cancel.
func TestTaskBoundCycle_PendingInProgressBlockedRepeated(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"cursor":      {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "idle"},
		"claude-code": {InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker, Status: "idle"},
	}
	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Cycle", Status: "pending", AssignedTo: "claude-code",
			CreatedBy: "cursor", CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}
	repo.state.NextTaskID = 2

	steps := []map[string]any{
		{"id": float64(1), "updated_by": "claude-code", "status": "in_progress"},
		{"id": float64(1), "updated_by": "claude-code", "status": "blocked"},
		{"id": float64(1), "updated_by": "cursor", "status": "pending"},
		{"id": float64(1), "updated_by": "claude-code", "status": "in_progress"},
		{"id": float64(1), "updated_by": "claude-code", "status": "completed"},
	}
	for i, args := range steps {
		if _, err := callTool(t, srv, "update_task", args); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}

	cc := repo.state.AgentInstances["claude-code"]
	if cc == nil {
		t.Fatal("claude-code instance unexpectedly missing")
	}
	for _, id := range cc.CurrentTasks {
		if id == 1 {
			t.Errorf("CurrentTasks should not contain completed task #1; got %v", cc.CurrentTasks)
		}
	}
	seen := map[int]bool{}
	for _, id := range cc.CurrentTasks {
		if seen[id] {
			t.Errorf("CurrentTasks contains duplicate task IDs: %v", cc.CurrentTasks)
		}
		seen[id] = true
	}
}
