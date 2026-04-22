package collab

import (
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/jaakkos/stringwork/internal/app"
	"github.com/jaakkos/stringwork/internal/domain"
)

// ========== create_task tests ==========

func TestCreateTask_Basic(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	args := map[string]any{
		"title":       "Implement feature X",
		"description": "Detailed description here",
		"assigned_to": "cursor",
		"created_by":  "claude-code",
	}

	result, err := callTool(t, srv, "create_task", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !strings.Contains(text, "Task #1 created") {
		t.Errorf("unexpected result: %s", text)
	}

	// Verify task was stored
	if len(repo.state.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(repo.state.Tasks))
	}

	task := repo.state.Tasks[0]
	if task.Title != "Implement feature X" {
		t.Errorf("unexpected title: %q", task.Title)
	}
	if task.Description != "Detailed description here" {
		t.Errorf("unexpected description: %q", task.Description)
	}
	if task.AssignedTo != "cursor" {
		t.Errorf("unexpected assignee: %q", task.AssignedTo)
	}
	if task.CreatedBy != "claude-code" {
		t.Errorf("unexpected creator: %q", task.CreatedBy)
	}
	if task.Status != "pending" {
		t.Errorf("unexpected status: %q", task.Status)
	}
	if task.Priority != 3 {
		t.Errorf("expected default priority 3, got %d", task.Priority)
	}
}

func TestCreateTask_MissingRequired(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	tests := []struct {
		name string
		args map[string]any
	}{
		{"missing title", map[string]any{"created_by": "cursor"}},
		{"missing created_by", map[string]any{"title": "Task"}},
		{"empty title", map[string]any{"title": "", "created_by": "cursor"}},
		{"empty created_by", map[string]any{"title": "Task", "created_by": ""}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := callTool(t, srv, "create_task", tt.args)
			if err == nil {
				t.Errorf("expected error for %s", tt.name)
			}
		})
	}
}

func TestCreateTask_InvalidAgents(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	// Invalid creator
	args := map[string]any{
		"title":      "Task",
		"created_by": "unknown-agent",
	}
	_, err := callTool(t, srv, "create_task", args)
	if err == nil {
		t.Error("expected error for invalid creator")
	}

	// Invalid assignee
	args = map[string]any{
		"title":       "Task",
		"created_by":  "cursor",
		"assigned_to": "invalid-agent",
	}
	_, err = callTool(t, srv, "create_task", args)
	if err == nil {
		t.Error("expected error for invalid assignee")
	}
}

func TestCreateTask_DefaultAssignee(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	args := map[string]any{
		"title":      "Task without assignee",
		"created_by": "cursor",
	}

	_, err := callTool(t, srv, "create_task", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if repo.state.Tasks[0].AssignedTo != "any" {
		t.Errorf("expected default assignee 'any', got %q", repo.state.Tasks[0].AssignedTo)
	}
}

func TestCreateTask_Priority(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	tests := []struct {
		priority     float64
		expectedPrio int
	}{
		{1, 1},  // critical
		{2, 2},  // high
		{3, 3},  // normal
		{4, 4},  // low
		{0, 1},  // clamped to 1
		{-5, 1}, // clamped to 1
		{10, 4}, // clamped to 4
	}

	for i, tt := range tests {
		args := map[string]any{
			"title":      "Task",
			"created_by": "cursor",
			"priority":   tt.priority,
		}
		_, err := callTool(t, srv, "create_task", args)
		if err != nil {
			t.Fatalf("test %d: unexpected error: %v", i, err)
		}

		task := repo.state.Tasks[i]
		if task.Priority != tt.expectedPrio {
			t.Errorf("test %d: priority %.0f should become %d, got %d", i, tt.priority, tt.expectedPrio, task.Priority)
		}
	}
}

func TestCreateTask_Dependencies(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	// Create first task
	args := map[string]any{
		"title":      "Task 1",
		"created_by": "cursor",
	}
	_, _ = callTool(t, srv, "create_task", args)

	// Create second task depending on first
	args = map[string]any{
		"title":      "Task 2",
		"created_by": "cursor",
		"depends_on": []interface{}{float64(1)},
	}
	_, err := callTool(t, srv, "create_task", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	task2 := repo.state.Tasks[1]
	if len(task2.Dependencies) != 1 || task2.Dependencies[0] != 1 {
		t.Errorf("expected dependency on task 1, got %v", task2.Dependencies)
	}
}

func TestCreateTask_InvalidDependency(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	args := map[string]any{
		"title":      "Task",
		"created_by": "cursor",
		"depends_on": []interface{}{float64(999)}, // non-existent
	}
	_, err := callTool(t, srv, "create_task", args)
	if err == nil {
		t.Error("expected error for non-existent dependency")
	}
}

func TestCreateTask_RequiresReview(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	args := map[string]any{
		"title":           "Task with review",
		"created_by":      "cursor",
		"requires_review": true,
	}

	_, err := callTool(t, srv, "create_task", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	task := repo.state.Tasks[0]
	if !task.RequiresReview {
		t.Error("expected RequiresReview to be true")
	}
	if task.ReviewStatus != "pending" {
		t.Errorf("expected ReviewStatus pending, got %q", task.ReviewStatus)
	}
}

// ========== list_tasks tests ==========

func TestListTasks_Empty(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	result, err := callTool(t, srv, "list_tasks", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !strings.Contains(text, "No tasks found") {
		t.Errorf("expected 'No tasks found', got: %s", text)
	}
}

func TestListTasks_All(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	// Pre-populate tasks
	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Task A", Status: "pending", AssignedTo: "cursor", CreatedBy: "claude-code"},
		{ID: 2, Title: "Task B", Status: "in_progress", AssignedTo: "claude-code", CreatedBy: "cursor"},
		{ID: 3, Title: "Task C", Status: "completed", AssignedTo: "cursor", CreatedBy: "cursor"},
	}
	repo.state.NextTaskID = 4

	srv := testServer(svc, logger)

	result, err := callTool(t, srv, "list_tasks", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !strings.Contains(text, "Task A") || !strings.Contains(text, "Task B") || !strings.Contains(text, "Task C") {
		t.Errorf("should list all tasks: %s", text)
	}
}

func TestListTasks_FilterByStatus(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Pending Task", Status: "pending", AssignedTo: "any", CreatedBy: "cursor"},
		{ID: 2, Title: "In Progress Task", Status: "in_progress", AssignedTo: "cursor", CreatedBy: "cursor"},
		{ID: 3, Title: "Done Task", Status: "completed", AssignedTo: "cursor", CreatedBy: "cursor"},
	}

	srv := testServer(svc, logger)

	args := map[string]any{"status": "pending"}
	result, err := callTool(t, srv, "list_tasks", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !strings.Contains(text, "Pending Task") {
		t.Error("should include pending task")
	}
	if strings.Contains(text, "In Progress Task") || strings.Contains(text, "Done Task") {
		t.Error("should not include non-pending tasks")
	}
}

func TestListTasks_FilterByAssignee(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Cursor Task", Status: "pending", AssignedTo: "cursor", CreatedBy: "claude-code"},
		{ID: 2, Title: "Claude Task", Status: "pending", AssignedTo: "claude-code", CreatedBy: "cursor"},
		{ID: 3, Title: "Anyone Task", Status: "pending", AssignedTo: "any", CreatedBy: "cursor"},
	}

	srv := testServer(svc, logger)

	args := map[string]any{"assigned_to": "cursor"}
	result, err := callTool(t, srv, "list_tasks", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !strings.Contains(text, "Cursor Task") {
		t.Error("should include task assigned to cursor")
	}
	if !strings.Contains(text, "Anyone Task") {
		t.Error("should include task assigned to 'any'")
	}
	if strings.Contains(text, "Claude Task") {
		t.Error("should not include task assigned to claude-code")
	}
}

func TestListTasks_InvalidAssignee(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	args := map[string]any{"assigned_to": "invalid-agent"}
	_, err := callTool(t, srv, "list_tasks", args)
	if err == nil {
		t.Error("expected error for invalid assignee filter")
	}
}

// ========== update_task tests ==========

func TestUpdateTask_Status(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Task", Status: "pending", AssignedTo: "cursor", CreatedBy: "cursor"},
	}
	repo.state.NextTaskID = 2

	srv := testServer(svc, logger)

	args := map[string]any{
		"id":         float64(1),
		"status":     "in_progress",
		"updated_by": "cursor",
	}

	result, err := callTool(t, srv, "update_task", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !strings.Contains(text, "updated") {
		t.Errorf("unexpected result: %s", text)
	}

	if repo.state.Tasks[0].Status != "in_progress" {
		t.Errorf("expected status in_progress, got %q", repo.state.Tasks[0].Status)
	}
}

func TestUpdateTask_MissingRequired(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Task", Status: "pending", AssignedTo: "cursor", CreatedBy: "cursor"},
	}

	srv := testServer(svc, logger)

	tests := []struct {
		name string
		args map[string]any
	}{
		{"missing id", map[string]any{"updated_by": "cursor"}},
		{"missing updated_by", map[string]any{"id": float64(1)}},
		{"empty updated_by", map[string]any{"id": float64(1), "updated_by": ""}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := callTool(t, srv, "update_task", tt.args)
			if err == nil {
				t.Errorf("expected error for %s", tt.name)
			}
		})
	}
}

func TestUpdateTask_NotFound(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	args := map[string]any{
		"id":         float64(999),
		"updated_by": "cursor",
	}

	_, err := callTool(t, srv, "update_task", args)
	if err == nil {
		t.Error("expected error for non-existent task")
	}
	if err != nil && !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention 'not found': %v", err)
	}
}

func TestUpdateTask_Assignee(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Task", Status: "pending", AssignedTo: "cursor", CreatedBy: "cursor"},
	}

	srv := testServer(svc, logger)

	args := map[string]any{
		"id":          float64(1),
		"assigned_to": "claude-code",
		"updated_by":  "cursor",
	}

	_, err := callTool(t, srv, "update_task", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if repo.state.Tasks[0].AssignedTo != "claude-code" {
		t.Errorf("expected assignee claude-code, got %q", repo.state.Tasks[0].AssignedTo)
	}
}

func TestUpdateTask_Priority(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Task", Status: "pending", AssignedTo: "cursor", CreatedBy: "cursor", Priority: 3},
	}

	srv := testServer(svc, logger)

	args := map[string]any{
		"id":         float64(1),
		"priority":   float64(1),
		"updated_by": "cursor",
	}

	_, err := callTool(t, srv, "update_task", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if repo.state.Tasks[0].Priority != 1 {
		t.Errorf("expected priority 1, got %d", repo.state.Tasks[0].Priority)
	}
}

func TestUpdateTask_AddDependency(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Task 1", Status: "pending", AssignedTo: "cursor", CreatedBy: "cursor"},
		{ID: 2, Title: "Task 2", Status: "pending", AssignedTo: "cursor", CreatedBy: "cursor"},
	}
	repo.state.NextTaskID = 3

	srv := testServer(svc, logger)

	args := map[string]any{
		"id":             float64(2),
		"add_dependency": float64(1),
		"updated_by":     "cursor",
	}

	_, err := callTool(t, srv, "update_task", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(repo.state.Tasks[1].Dependencies) != 1 || repo.state.Tasks[1].Dependencies[0] != 1 {
		t.Errorf("expected dependency on task 1, got %v", repo.state.Tasks[1].Dependencies)
	}
}

func TestUpdateTask_SelfDependencyError(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Task", Status: "pending", AssignedTo: "cursor", CreatedBy: "cursor"},
	}

	srv := testServer(svc, logger)

	args := map[string]any{
		"id":             float64(1),
		"add_dependency": float64(1), // self-reference
		"updated_by":     "cursor",
	}

	_, err := callTool(t, srv, "update_task", args)
	if err == nil {
		t.Error("expected error for self-dependency")
	}
}

func TestUpdateTask_RemoveDependency(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Task 1", Status: "pending", AssignedTo: "cursor", CreatedBy: "cursor"},
		{ID: 2, Title: "Task 2", Status: "pending", AssignedTo: "cursor", CreatedBy: "cursor", Dependencies: []int{1}},
	}

	srv := testServer(svc, logger)

	args := map[string]any{
		"id":                float64(2),
		"remove_dependency": float64(1),
		"updated_by":        "cursor",
	}

	_, err := callTool(t, srv, "update_task", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(repo.state.Tasks[1].Dependencies) != 0 {
		t.Errorf("dependency should be removed, got %v", repo.state.Tasks[1].Dependencies)
	}
}

func TestUpdateTask_BlockedBy(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Task", Status: "pending", AssignedTo: "cursor", CreatedBy: "cursor"},
	}

	srv := testServer(svc, logger)

	args := map[string]any{
		"id":         float64(1),
		"blocked_by": "Waiting for API access",
		"updated_by": "cursor",
	}

	_, err := callTool(t, srv, "update_task", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	task := repo.state.Tasks[0]
	if task.BlockedBy != "Waiting for API access" {
		t.Errorf("expected blocked_by message, got %q", task.BlockedBy)
	}
	if task.Status != "blocked" {
		t.Errorf("status should be 'blocked' when blocked_by is set, got %q", task.Status)
	}
}

func TestUpdateTask_InProgressBlockedByDependencies(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Task 1", Status: "pending", AssignedTo: "cursor", CreatedBy: "cursor"},
		{ID: 2, Title: "Task 2", Status: "pending", AssignedTo: "cursor", CreatedBy: "cursor", Dependencies: []int{1}},
	}

	srv := testServer(svc, logger)

	// Try to start task 2 while task 1 is not completed
	args := map[string]any{
		"id":         float64(2),
		"status":     "in_progress",
		"updated_by": "cursor",
	}

	_, err := callTool(t, srv, "update_task", args)
	if err == nil {
		t.Error("expected error when starting task with incomplete dependencies")
	}
	if err != nil && !strings.Contains(err.Error(), "dependencies not complete") {
		t.Errorf("error should mention incomplete dependencies: %v", err)
	}
}

func TestUpdateTask_InProgressAfterDependencyComplete(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Task 1", Status: "completed", AssignedTo: "cursor", CreatedBy: "cursor"},
		{ID: 2, Title: "Task 2", Status: "pending", AssignedTo: "cursor", CreatedBy: "cursor", Dependencies: []int{1}},
	}

	srv := testServer(svc, logger)

	args := map[string]any{
		"id":         float64(2),
		"status":     "in_progress",
		"updated_by": "cursor",
	}

	_, err := callTool(t, srv, "update_task", args)
	if err != nil {
		t.Fatalf("should allow starting task after dependency is complete: %v", err)
	}

	if repo.state.Tasks[1].Status != "in_progress" {
		t.Error("task should be in_progress")
	}
}

func TestUpdateTask_BlockedByReassignsToOtherWorker(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	orch := app.NewTaskOrchestrator(svc, "least_loaded")

	repo.state.DriverID = "cursor"
	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"cursor":      {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "working", MaxTasks: 5},
		"gemini":      {InstanceID: "gemini", AgentType: "gemini", Role: domain.RoleWorker, Status: "busy", MaxTasks: 3, CurrentTasks: []int{1}},
		"claude-code": {InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker, Status: "idle", MaxTasks: 3},
	}
	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Review code", Status: "in_progress", AssignedTo: "gemini", CreatedBy: "cursor"},
	}
	repo.state.NextMsgID = 1

	srv := testServerWithOrch(svc, logger, orch)

	args := map[string]any{
		"id":         float64(1),
		"blocked_by": "Cannot access repository",
		"updated_by": "gemini",
	}

	_, err := callTool(t, srv, "update_task", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	task := repo.state.Tasks[0]
	if task.AssignedTo != "claude-code" {
		t.Errorf("task should be reassigned to claude-code, got %q", task.AssignedTo)
	}
	if task.Status != "pending" {
		t.Errorf("status should be 'pending' after reassignment, got %q", task.Status)
	}
	if task.BlockedBy != "Cannot access repository" {
		t.Errorf("blocked_by should be preserved, got %q", task.BlockedBy)
	}

	found := false
	for _, msg := range repo.state.Messages {
		if msg.From == "system" && msg.To == "cursor" && strings.Contains(msg.Content, "reassigned") {
			found = true
			break
		}
	}
	if !found {
		t.Error("driver should receive a system message about the reassignment")
	}
}

func TestUpdateTask_BlockedByNoAlternativeWorker(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	orch := app.NewTaskOrchestrator(svc, "least_loaded")

	repo.state.DriverID = "cursor"
	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"cursor": {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "working", MaxTasks: 5},
		"gemini": {InstanceID: "gemini", AgentType: "gemini", Role: domain.RoleWorker, Status: "busy", MaxTasks: 3, CurrentTasks: []int{1}},
	}
	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Review code", Status: "in_progress", AssignedTo: "gemini", CreatedBy: "cursor"},
	}
	repo.state.NextMsgID = 1

	srv := testServerWithOrch(svc, logger, orch)

	args := map[string]any{
		"id":         float64(1),
		"blocked_by": "Cannot access repository",
		"updated_by": "gemini",
	}

	_, err := callTool(t, srv, "update_task", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	task := repo.state.Tasks[0]
	if task.Status != "blocked" {
		t.Errorf("status should remain 'blocked' when no alternative, got %q", task.Status)
	}
	if task.BlockedBy != "Cannot access repository" {
		t.Errorf("blocked_by should be set, got %q", task.BlockedBy)
	}

	found := false
	for _, msg := range repo.state.Messages {
		if msg.From == "system" && msg.To == "cursor" && strings.Contains(msg.Content, "no alternative") {
			found = true
			break
		}
	}
	if !found {
		t.Error("driver should receive a system message about no alternative worker")
	}
}

func TestUpdateTask_BlockedByNilOrchestrator(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.DriverID = "cursor"
	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"cursor":      {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "working", MaxTasks: 5},
		"claude-code": {InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker, Status: "idle", MaxTasks: 3},
	}
	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Review code", Status: "in_progress", AssignedTo: "claude-code", CreatedBy: "cursor"},
	}

	srv := testServer(svc, logger)

	args := map[string]any{
		"id":         float64(1),
		"blocked_by": "Waiting for API key",
		"updated_by": "claude-code",
	}

	_, err := callTool(t, srv, "update_task", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	task := repo.state.Tasks[0]
	if task.Status != "blocked" {
		t.Errorf("status should be 'blocked' without orchestrator, got %q", task.Status)
	}
	if task.AssignedTo != "claude-code" {
		t.Errorf("assignee should not change without orchestrator, got %q", task.AssignedTo)
	}
}

func TestUpdateTask_UpdatesTimestamp(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	oldTime := time.Now().Add(-time.Hour)
	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Task", Status: "pending", AssignedTo: "cursor", CreatedBy: "cursor", UpdatedAt: oldTime},
	}

	srv := testServer(svc, logger)

	args := map[string]any{
		"id":         float64(1),
		"status":     "in_progress",
		"updated_by": "cursor",
	}

	_, err := callTool(t, srv, "update_task", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !repo.state.Tasks[0].UpdatedAt.After(oldTime) {
		t.Error("UpdatedAt should be updated to current time")
	}
}

func TestUpdateTask_CompletionBlockedByReview(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Task", Status: "in_progress", AssignedTo: "cursor", CreatedBy: "cursor", RequiresReview: true, ReviewStatus: "pending"},
	}

	srv := testServer(svc, logger)

	args := map[string]any{
		"id":         float64(1),
		"status":     "completed",
		"updated_by": "cursor",
	}

	_, err := callTool(t, srv, "update_task", args)
	if err == nil {
		t.Error("expected error when completing task with pending review")
	}
}

func TestUpdateTask_SelfApprovalGuard(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Task", Status: "in_progress", AssignedTo: "claude-code", CreatedBy: "cursor", RequiresReview: true, ReviewStatus: "pending"},
	}

	srv := testServer(svc, logger)

	args := map[string]any{
		"id":            float64(1),
		"review_status": "approved",
		"updated_by":    "claude-code", // assignee
	}

	_, err := callTool(t, srv, "update_task", args)
	if err == nil {
		t.Error("expected error when assignee tries to approve their own task")
	}
}

func TestUpdateTask_ApprovalAllowsCompletion(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Task", Status: "in_progress", AssignedTo: "claude-code", CreatedBy: "cursor", RequiresReview: true, ReviewStatus: "pending"},
	}

	srv := testServer(svc, logger)

	// Approve as driver
	args := map[string]any{
		"id":            float64(1),
		"review_status": "approved",
		"updated_by":    "cursor",
	}
	_, err := callTool(t, srv, "update_task", args)
	if err != nil {
		t.Fatalf("unexpected error on approval: %v", err)
	}

	// Now complete it
	args = map[string]any{
		"id":         float64(1),
		"status":     "completed",
		"updated_by": "claude-code",
	}
	_, err = callTool(t, srv, "update_task", args)
	if err != nil {
		t.Fatalf("should allow completion after approval: %v", err)
	}

	if repo.state.Tasks[0].Status != "completed" {
		t.Errorf("expected status completed, got %q", repo.state.Tasks[0].Status)
	}
}

// TestSpawnSideEffects_RevalidatesAfterStateLock (M8) — when update_task
// queues a post-lock spawn for a newly assigned worker, the spawn must
// be re-validated against the latest state. If another writer cancels
// the task in the gap between Run releasing the lock and SpawnForTask
// firing, the spawn is now wasted (a new process starts, finds no work
// to do, and either idles or self-cancels — but we lit the user's
// terminal up for nothing). The fix: a final svc.Run that re-checks
// AssignedTo and Status before letting the spawn through.
func TestSpawnSideEffects_RevalidatesAfterStateLock(t *testing.T) {
	repo := newMockRepository()
	policy := newMockPolicy()
	logger := log.New(io.Discard, "", 0)
	svc := app.NewCollabService(repo, policy, logger)

	repo.state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver,
		Status: "working", MaxTasks: 5,
	}
	repo.state.AgentInstances["claude-code"] = &domain.AgentInstance{
		InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker,
		Status: "idle", MaxTasks: 1, CurrentTasks: []int{},
	}
	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "T", Status: "pending", AssignedTo: "any", CreatedBy: "cursor", Priority: 3},
	}
	repo.state.NextTaskID = 2

	saveCount := 0
	repo.afterSave = func(state *domain.CollabState) {
		saveCount++
		if saveCount == 1 {
			for i := range state.Tasks {
				if state.Tasks[i].ID == 1 {
					state.Tasks[i].Status = "cancelled"
					state.Tasks[i].AssignedTo = ""
					return
				}
			}
		}
	}

	spawner := &fakeSpawner{}
	srv := testServerWithSpawner(svc, logger, spawner)

	args := map[string]any{
		"id":          float64(1),
		"assigned_to": "claude-code",
		"updated_by":  "cursor",
	}
	if _, err := callTool(t, srv, "update_task", args); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(spawner.calls) != 0 {
		t.Errorf("spawn should be skipped after state changed; got calls=%v", spawner.calls)
	}
}

// ========== replay_task tests ==========

func TestReplayTask_Basic(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{
			ID: 1, Title: "Failed task", Status: "blocked", AssignedTo: "claude-code",
			FailureCount: 3, FailureReason: "Watchdog", ResultSummary: "Error",
			RequiresReview: true, ReviewStatus: "rejected", ReviewedBy: "cursor",
		},
	}

	srv := testServer(svc, logger)

	args := map[string]any{
		"id":          float64(1),
		"updated_by":  "cursor",
		"reassign_to": "any",
	}

	_, err := callTool(t, srv, "replay_task", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	task := repo.state.Tasks[0]
	if task.Status != "pending" {
		t.Errorf("expected status pending, got %q", task.Status)
	}
	if task.FailureCount != 0 {
		t.Errorf("expected FailureCount 0, got %d", task.FailureCount)
	}
	if task.FailureReason != "" {
		t.Errorf("expected empty FailureReason, got %q", task.FailureReason)
	}
	if task.AssignedTo != "any" {
		t.Errorf("expected assigned_to any, got %q", task.AssignedTo)
	}
	if task.ReviewStatus != "pending" {
		t.Errorf("expected ReviewStatus pending for RequiresReview task, got %q", task.ReviewStatus)
	}
}
