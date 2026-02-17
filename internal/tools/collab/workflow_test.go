package collab

import (
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/jaakkos/stringwork/internal/domain"
)

// ========== handoff tests ==========

func TestHandoff_Basic(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	args := map[string]any{
		"from":       "cursor",
		"to":         "claude-code",
		"summary":    "Completed initial implementation",
		"next_steps": "Please review and add tests",
	}

	result, err := callTool(t, srv, "handoff", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !strings.Contains(text, "Handoff complete") {
		t.Errorf("unexpected result: %s", text)
	}

	// Should create a message
	if len(repo.state.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(repo.state.Messages))
	}

	msg := repo.state.Messages[0]
	if msg.From != "cursor" || msg.To != "claude-code" {
		t.Errorf("message from/to incorrect")
	}
	if !strings.Contains(msg.Content, "Handoff from cursor") {
		t.Error("message should contain handoff header")
	}
	if !strings.Contains(msg.Content, "Completed initial implementation") {
		t.Error("message should contain summary")
	}
	if !strings.Contains(msg.Content, "Please review and add tests") {
		t.Error("message should contain next steps")
	}
}

func TestHandoff_MissingRequired(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	tests := []struct {
		name string
		args map[string]any
	}{
		{"missing from", map[string]any{"to": "claude-code", "summary": "s", "next_steps": "n"}},
		{"missing to", map[string]any{"from": "cursor", "summary": "s", "next_steps": "n"}},
		{"missing summary", map[string]any{"from": "cursor", "to": "claude-code", "next_steps": "n"}},
		{"missing next_steps", map[string]any{"from": "cursor", "to": "claude-code", "summary": "s"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := callTool(t, srv, "handoff", tt.args)
			if err == nil {
				t.Errorf("expected error for %s", tt.name)
			}
		})
	}
}

func TestHandoff_WithTaskID(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	// Create a task
	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "My Task", Status: "in_progress", AssignedTo: "cursor", CreatedBy: "cursor"},
	}
	repo.state.NextTaskID = 2

	srv := testServer(svc, logger)

	args := map[string]any{
		"from":       "cursor",
		"to":         "claude-code",
		"task_id":    float64(1),
		"summary":    "Done with my part",
		"next_steps": "Continue from here",
	}

	result, err := callTool(t, srv, "handoff", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Task should be reassigned
	task := repo.state.Tasks[0]
	if task.AssignedTo != "claude-code" {
		t.Errorf("task should be reassigned to claude-code, got %q", task.AssignedTo)
	}
	if task.Status != "pending" {
		t.Errorf("task status should be pending, got %q", task.Status)
	}

	text := resultText(t, result)
	if !strings.Contains(text, "Task #1 reassigned") {
		t.Errorf("result should mention task reassignment: %s", text)
	}
}

func TestHandoff_AutoFindInProgressTask(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Other Task", Status: "pending", AssignedTo: "claude-code", CreatedBy: "cursor"},
		{ID: 2, Title: "My Current Task", Status: "in_progress", AssignedTo: "cursor", CreatedBy: "cursor"},
	}
	repo.state.NextTaskID = 3

	srv := testServer(svc, logger)

	// No task_id provided - should find in_progress task assigned to 'from'
	args := map[string]any{
		"from":       "cursor",
		"to":         "claude-code",
		"summary":    "Handoff",
		"next_steps": "Continue",
	}

	result, err := callTool(t, srv, "handoff", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Task 2 should be reassigned (it was in_progress for cursor)
	if repo.state.Tasks[1].AssignedTo != "claude-code" {
		t.Error("should auto-find and reassign in_progress task")
	}

	text := resultText(t, result)
	if !strings.Contains(text, "Task #2") {
		t.Errorf("should mention task #2: %s", text)
	}
}

func TestHandoff_InvalidAgents(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	// Invalid from
	args := map[string]any{
		"from":       "unknown",
		"to":         "claude-code",
		"summary":    "s",
		"next_steps": "n",
	}
	_, err := callTool(t, srv, "handoff", args)
	if err == nil {
		t.Error("expected error for invalid from agent")
	}

	// Invalid to
	args = map[string]any{
		"from":       "cursor",
		"to":         "unknown",
		"summary":    "s",
		"next_steps": "n",
	}
	_, err = callTool(t, srv, "handoff", args)
	if err == nil {
		t.Error("expected error for invalid to agent")
	}
}

// ========== claim_next tests ==========

func TestClaimNext_NoWork(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	args := map[string]any{"agent": "cursor"}

	result, err := callTool(t, srv, "claim_next", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !strings.Contains(text, "idle") {
		t.Errorf("should return idle when no work: %s", text)
	}
}

func TestClaimNext_UnreadMessage(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Messages = []domain.Message{
		{ID: 1, From: "claude-code", To: "cursor", Content: "Hello!", Timestamp: time.Now(), Read: false},
	}

	srv := testServer(svc, logger)

	args := map[string]any{"agent": "cursor"}
	result, err := callTool(t, srv, "claim_next", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !strings.Contains(text, "read_messages") {
		t.Errorf("should return read_messages action: %s", text)
	}
}

func TestClaimNext_InProgressTask(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Current Work", Status: "in_progress", AssignedTo: "cursor", CreatedBy: "cursor"},
	}

	srv := testServer(svc, logger)

	args := map[string]any{"agent": "cursor"}
	result, err := callTool(t, srv, "claim_next", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !strings.Contains(text, "continue_task") {
		t.Errorf("should return continue_task action: %s", text)
	}
	if !strings.Contains(text, "Current Work") {
		t.Errorf("should include task title: %s", text)
	}
}

func TestClaimNext_PendingTask(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Available Task", Status: "pending", AssignedTo: "cursor", CreatedBy: "claude-code", Priority: 3},
	}
	repo.state.NextTaskID = 2

	srv := testServer(svc, logger)

	args := map[string]any{"agent": "cursor"}
	result, err := callTool(t, srv, "claim_next", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	// Should claim the task (not dry_run)
	if !strings.Contains(text, "Claimed task #1") {
		t.Errorf("should claim the task: %s", text)
	}

	// Task should now be in_progress
	if repo.state.Tasks[0].Status != "in_progress" {
		t.Errorf("task should be in_progress, got %q", repo.state.Tasks[0].Status)
	}
}

func TestClaimNext_DryRun(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Available Task", Status: "pending", AssignedTo: "cursor", CreatedBy: "claude-code", Priority: 3},
	}

	srv := testServer(svc, logger)

	args := map[string]any{
		"agent":   "cursor",
		"dry_run": true,
	}
	result, err := callTool(t, srv, "claim_next", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !strings.Contains(text, "claim_task") {
		t.Errorf("should return claim_task action: %s", text)
	}
	if !strings.Contains(text, "dry_run") {
		t.Errorf("should indicate dry_run: %s", text)
	}

	// Task should still be pending (not claimed)
	if repo.state.Tasks[0].Status != "pending" {
		t.Error("task should remain pending in dry_run mode")
	}
}

func TestClaimNext_HighestPriority(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Low Priority", Status: "pending", AssignedTo: "any", CreatedBy: "cursor", Priority: 4},
		{ID: 2, Title: "Critical Task", Status: "pending", AssignedTo: "any", CreatedBy: "cursor", Priority: 1},
		{ID: 3, Title: "Normal Task", Status: "pending", AssignedTo: "any", CreatedBy: "cursor", Priority: 3},
	}
	repo.state.NextTaskID = 4

	srv := testServer(svc, logger)

	args := map[string]any{"agent": "cursor"}
	result, err := callTool(t, srv, "claim_next", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	// Should claim the critical task (highest priority = lowest number)
	if !strings.Contains(text, "Critical Task") {
		t.Errorf("should claim highest priority task: %s", text)
	}

	// Task 2 should be claimed
	if repo.state.Tasks[1].Status != "in_progress" {
		t.Error("critical task should be claimed")
	}
}

func TestClaimNext_SkipBlockedTasks(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Dependency", Status: "pending", AssignedTo: "any", CreatedBy: "cursor", Priority: 3},
		{ID: 2, Title: "Blocked Task", Status: "pending", AssignedTo: "any", CreatedBy: "cursor", Priority: 1, Dependencies: []int{1}},
	}
	repo.state.NextTaskID = 3

	srv := testServer(svc, logger)

	args := map[string]any{"agent": "cursor"}
	result, err := callTool(t, srv, "claim_next", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	// Should claim task 1 (task 2 is blocked by incomplete dependency)
	if !strings.Contains(text, "Dependency") {
		t.Errorf("should claim non-blocked task: %s", text)
	}
}

func TestClaimNext_AnyAssignee(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "For Anyone", Status: "pending", AssignedTo: "any", CreatedBy: "cursor", Priority: 3},
	}
	repo.state.NextTaskID = 2

	srv := testServer(svc, logger)

	args := map[string]any{"agent": "claude-code"}
	_, err := callTool(t, srv, "claim_next", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Task assigned to 'any' should be claimable by claude-code
	if repo.state.Tasks[0].AssignedTo != "claude-code" {
		t.Errorf("task should be assigned to claude-code, got %q", repo.state.Tasks[0].AssignedTo)
	}
}

func TestClaimNext_MissingAgent(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	args := map[string]any{}
	_, err := callTool(t, srv, "claim_next", args)
	if err == nil {
		t.Error("expected error for missing agent")
	}
}

func TestClaimNext_InvalidAgent(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	args := map[string]any{"agent": "unknown"}
	_, err := callTool(t, srv, "claim_next", args)
	if err == nil {
		t.Error("expected error for invalid agent")
	}
}

// ========== request_review tests ==========

func TestRequestReview_Basic(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	args := map[string]any{
		"from":        "cursor",
		"to":          "claude-code",
		"description": "Please review the authentication changes",
	}

	result, err := callTool(t, srv, "request_review", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !strings.Contains(text, "Review requested") {
		t.Errorf("unexpected result: %s", text)
	}

	// Should create a task
	if len(repo.state.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(repo.state.Tasks))
	}

	task := repo.state.Tasks[0]
	if task.AssignedTo != "claude-code" {
		t.Errorf("task should be assigned to claude-code")
	}
	if task.CreatedBy != "cursor" {
		t.Errorf("task should be created by cursor")
	}
	if task.Priority != 2 {
		t.Errorf("review tasks should have high priority (2), got %d", task.Priority)
	}
	if !strings.Contains(task.Title, "Review:") {
		t.Errorf("task title should start with 'Review:': %s", task.Title)
	}
	if !strings.Contains(task.Description, "Code Review Request") {
		t.Error("task description should contain review request header")
	}
}

func TestRequestReview_WithFiles(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	args := map[string]any{
		"from":        "cursor",
		"to":          "claude-code",
		"description": "Review auth changes",
		"files":       []interface{}{"auth.go", "auth_test.go", "middleware.go"},
	}

	_, err := callTool(t, srv, "request_review", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	task := repo.state.Tasks[0]
	if !strings.Contains(task.Description, "auth.go") {
		t.Error("task description should include files")
	}
	if !strings.Contains(task.Description, "Files to Review") {
		t.Error("task description should have files section")
	}
}

func TestRequestReview_MissingRequired(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	tests := []struct {
		name string
		args map[string]any
	}{
		{"missing from", map[string]any{"to": "claude-code", "description": "d"}},
		{"missing to", map[string]any{"from": "cursor", "description": "d"}},
		{"missing description", map[string]any{"from": "cursor", "to": "claude-code"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := callTool(t, srv, "request_review", tt.args)
			if err == nil {
				t.Errorf("expected error for %s", tt.name)
			}
		})
	}
}

func TestRequestReview_InvalidAgents(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	// Invalid from
	args := map[string]any{
		"from":        "unknown",
		"to":          "claude-code",
		"description": "review",
	}
	_, err := callTool(t, srv, "request_review", args)
	if err == nil {
		t.Error("expected error for invalid from agent")
	}

	// Invalid to
	args = map[string]any{
		"from":        "cursor",
		"to":          "unknown",
		"description": "review",
	}
	_, err = callTool(t, srv, "request_review", args)
	if err == nil {
		t.Error("expected error for invalid to agent")
	}
}

func TestRequestReview_TruncatesLongDescription(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	longDesc := strings.Repeat("a", 200)
	args := map[string]any{
		"from":        "cursor",
		"to":          "claude-code",
		"description": longDesc,
	}

	_, err := callTool(t, srv, "request_review", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	task := repo.state.Tasks[0]
	// Title should be truncated (Review: + truncated description + ...)
	// The Truncate function adds "..." so we allow for that
	if len(task.Title) > 65 {
		t.Errorf("task title should be truncated, got length %d", len(task.Title))
	}
	if !strings.HasSuffix(task.Title, "...") {
		t.Error("truncated title should end with ...")
	}
}
