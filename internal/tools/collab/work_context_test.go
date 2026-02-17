package collab

import (
	"io"
	"log"
	"strings"
	"testing"

	"github.com/jaakkos/stringwork/internal/domain"
)

// === get_work_context tests ===

func TestGetWorkContext_Success(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	// Create a task and attach work context
	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Test Task", Status: "in_progress", AssignedTo: "claude-code", CreatedBy: "cursor"},
	}
	repo.state.NextTaskID = 2
	ensureWorkContextForTask(repo.state, 1, []string{"main.go", "utils.go"}, "Background info here", []string{"do not modify auth"}, "")

	srv := testServer(svc, logger)

	result, err := callTool(t, srv, "get_work_context", map[string]any{"task_id": float64(1)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)

	if !strings.Contains(text, "main.go") {
		t.Errorf("expected relevant_files in output: %s", text)
	}
	if !strings.Contains(text, "Background info here") {
		t.Errorf("expected background in output: %s", text)
	}
	if !strings.Contains(text, "do not modify auth") {
		t.Errorf("expected constraints in output: %s", text)
	}
}

func TestGetWorkContext_MissingTaskID(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	_, err := callTool(t, srv, "get_work_context", map[string]any{})
	if err == nil {
		t.Error("expected error for missing task_id")
	}
	if err != nil && !strings.Contains(err.Error(), "task_id") {
		t.Errorf("expected error about task_id, got: %v", err)
	}
}

func TestGetWorkContext_WrongTypeTaskID(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	_, err := callTool(t, srv, "get_work_context", map[string]any{"task_id": "abc"})
	if err == nil {
		t.Error("expected error for wrong type task_id")
	}
	if err != nil && !strings.Contains(err.Error(), "must be a number") {
		t.Errorf("expected 'must be a number' error, got: %v", err)
	}
}

func TestGetWorkContext_NonExistentTask(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	result, err := callTool(t, srv, "get_work_context", map[string]any{"task_id": float64(999)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "No work context") {
		t.Errorf("expected 'No work context' message, got: %s", text)
	}
}

func TestGetWorkContext_TaskWithoutContext(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	// Task exists but has no work context
	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "No Context Task", Status: "pending", AssignedTo: "cursor", CreatedBy: "cursor"},
	}
	repo.state.NextTaskID = 2

	srv := testServer(svc, logger)

	result, err := callTool(t, srv, "get_work_context", map[string]any{"task_id": float64(1)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "No work context") {
		t.Errorf("expected 'No work context' message, got: %s", text)
	}
}

// === update_work_context tests ===

func TestUpdateWorkContext_Success(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	// Create a task with work context
	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Test Task", Status: "in_progress", AssignedTo: "claude-code", CreatedBy: "cursor"},
	}
	repo.state.NextTaskID = 2
	ctxID := ensureWorkContextForTask(repo.state, 1, []string{"main.go"}, "bg", nil, "")

	srv := testServer(svc, logger)

	// Update work context
	result, err := callTool(t, srv, "update_work_context", map[string]any{
		"task_id": float64(1),
		"key":     "findings",
		"value":   "Found a race condition in handler",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if text != "OK" {
		t.Errorf("expected OK, got %q", text)
	}

	// Verify the note was saved
	wc := repo.state.WorkContexts[ctxID]
	if wc == nil {
		t.Fatal("work context should exist")
	}
	if wc.SharedNotes["findings"] != "Found a race condition in handler" {
		t.Errorf("shared note not saved: %v", wc.SharedNotes)
	}
}

func TestUpdateWorkContext_WithAuthor(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Test Task", Status: "in_progress", AssignedTo: "claude-code", CreatedBy: "cursor"},
	}
	repo.state.NextTaskID = 2
	ctxID := ensureWorkContextForTask(repo.state, 1, []string{"main.go"}, "bg", nil, "")

	srv := testServer(svc, logger)

	_, err := callTool(t, srv, "update_work_context", map[string]any{
		"task_id": float64(1),
		"key":     "findings",
		"value":   "Found an issue",
		"author":  "claude-code",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Key should be prefixed with author
	wc := repo.state.WorkContexts[ctxID]
	if _, ok := wc.SharedNotes["claude-code:findings"]; !ok {
		t.Errorf("expected key prefixed with author, got notes: %v", wc.SharedNotes)
	}
}

func TestUpdateWorkContext_MissingKey(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Test Task", Status: "in_progress", AssignedTo: "claude-code", CreatedBy: "cursor"},
	}
	repo.state.NextTaskID = 2
	ensureWorkContextForTask(repo.state, 1, []string{"main.go"}, "bg", nil, "")

	srv := testServer(svc, logger)

	_, err := callTool(t, srv, "update_work_context", map[string]any{
		"task_id": float64(1),
		"key":     "",
		"value":   "some value",
	})
	if err == nil {
		t.Error("expected error for empty key")
	}
}

func TestUpdateWorkContext_MissingValue(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Test Task", Status: "in_progress", AssignedTo: "claude-code", CreatedBy: "cursor"},
	}
	repo.state.NextTaskID = 2
	ensureWorkContextForTask(repo.state, 1, []string{"main.go"}, "bg", nil, "")

	srv := testServer(svc, logger)

	_, err := callTool(t, srv, "update_work_context", map[string]any{
		"task_id": float64(1),
		"key":     "findings",
		"value":   "",
	})
	if err == nil {
		t.Error("expected error for empty value")
	}
}

func TestUpdateWorkContext_NoContext(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	// Task exists but has NO work context
	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "No Context", Status: "pending", AssignedTo: "cursor", CreatedBy: "cursor"},
	}
	repo.state.NextTaskID = 2

	srv := testServer(svc, logger)

	_, err := callTool(t, srv, "update_work_context", map[string]any{
		"task_id": float64(1),
		"key":     "findings",
		"value":   "some value",
	})
	if err == nil {
		t.Error("expected error for task without context")
	}
	if err != nil && !strings.Contains(err.Error(), "no work context") {
		t.Errorf("expected 'no work context' error, got: %v", err)
	}
}

func TestUpdateWorkContext_MissingTaskID(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	_, err := callTool(t, srv, "update_work_context", map[string]any{
		"key":   "findings",
		"value": "some value",
	})
	if err == nil {
		t.Error("expected error for missing task_id")
	}
}

func TestGetWorkContext_RoundTrip(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Round Trip Task", Status: "in_progress", AssignedTo: "claude-code", CreatedBy: "cursor"},
	}
	repo.state.NextTaskID = 2
	ensureWorkContextForTask(repo.state, 1, []string{"api.go"}, "API refactoring", []string{"backward compat"}, "")

	srv := testServer(svc, logger)

	// Update work context
	_, err := callTool(t, srv, "update_work_context", map[string]any{
		"task_id": float64(1),
		"key":     "status",
		"value":   "halfway done",
		"author":  "claude-code",
	})
	if err != nil {
		t.Fatalf("update_work_context error: %v", err)
	}

	// Read it back via get_work_context
	result, err := callTool(t, srv, "get_work_context", map[string]any{"task_id": float64(1)})
	if err != nil {
		t.Fatalf("get_work_context error: %v", err)
	}
	text := resultText(t, result)

	// Verify everything is there
	if !strings.Contains(text, "api.go") {
		t.Errorf("expected relevant files: %s", text)
	}
	if !strings.Contains(text, "API refactoring") {
		t.Errorf("expected background: %s", text)
	}
	if !strings.Contains(text, "backward compat") {
		t.Errorf("expected constraints: %s", text)
	}
	if !strings.Contains(text, "halfway done") {
		t.Errorf("expected shared note in output: %s", text)
	}
}
