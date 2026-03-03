package collab

import (
	"io"
	"log"
	"strings"
	"testing"

	"github.com/jaakkos/stringwork/internal/domain"
)

// ========== create_task requires_review tests ==========

func TestCreateTask_RequiresReview_SetsReviewStatusPending(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	args := map[string]any{
		"title":           "Review-gated task",
		"created_by":      "cursor",
		"assigned_to":     "claude-code",
		"requires_review": true,
	}

	result, err := callTool(t, srv, "create_task", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !strings.Contains(text, "Task #1 created") {
		t.Errorf("unexpected result: %s", text)
	}

	task := repo.state.Tasks[0]
	if !task.RequiresReview {
		t.Error("RequiresReview should be true")
	}
	if task.ReviewStatus != "pending" {
		t.Errorf("ReviewStatus = %q, want pending", task.ReviewStatus)
	}
}

func TestCreateTask_NoReview_EmptyReviewStatus(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	args := map[string]any{
		"title":      "Normal task",
		"created_by": "cursor",
	}

	_, err := callTool(t, srv, "create_task", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	task := repo.state.Tasks[0]
	if task.RequiresReview {
		t.Error("RequiresReview should be false")
	}
	if task.ReviewStatus != "" {
		t.Errorf("ReviewStatus should be empty, got %q", task.ReviewStatus)
	}
}

// ========== update_task review gate tests ==========

func TestUpdateTask_CompletionBlockedByReviewGate(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{
			ID: 1, Title: "Gated task", Status: "in_progress",
			AssignedTo: "claude-code", CreatedBy: "cursor",
			RequiresReview: true, ReviewStatus: "pending",
		},
	}

	srv := testServer(svc, logger)

	args := map[string]any{
		"id":         float64(1),
		"status":     "completed",
		"updated_by": "claude-code",
	}

	_, err := callTool(t, srv, "update_task", args)
	if err == nil {
		t.Fatal("expected error when completing task with pending review")
	}
	if !strings.Contains(err.Error(), "review required") {
		t.Errorf("error should mention review required: %v", err)
	}

	if repo.state.Tasks[0].Status != "in_progress" {
		t.Errorf("status should remain in_progress, got %q", repo.state.Tasks[0].Status)
	}
}

func TestUpdateTask_CompletionAllowedAfterApproval(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{
			ID: 1, Title: "Approved task", Status: "in_progress",
			AssignedTo: "claude-code", CreatedBy: "cursor",
			RequiresReview: true, ReviewStatus: "approved", ReviewedBy: "cursor",
		},
	}

	srv := testServer(svc, logger)

	args := map[string]any{
		"id":         float64(1),
		"status":     "completed",
		"updated_by": "claude-code",
	}

	_, err := callTool(t, srv, "update_task", args)
	if err != nil {
		t.Fatalf("should allow completion after approval: %v", err)
	}

	if repo.state.Tasks[0].Status != "completed" {
		t.Errorf("status should be completed, got %q", repo.state.Tasks[0].Status)
	}
}

func TestUpdateTask_SelfApprovalRejected(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{
			ID: 1, Title: "My task", Status: "in_progress",
			AssignedTo: "claude-code", CreatedBy: "cursor",
			RequiresReview: true, ReviewStatus: "pending",
		},
	}

	srv := testServer(svc, logger)

	args := map[string]any{
		"id":            float64(1),
		"review_status": "approved",
		"updated_by":    "claude-code",
	}

	_, err := callTool(t, srv, "update_task", args)
	if err == nil {
		t.Fatal("expected error for self-approval")
	}
	if !strings.Contains(err.Error(), "cannot approve their own") {
		t.Errorf("error should mention self-approval: %v", err)
	}

	if repo.state.Tasks[0].ReviewStatus != "pending" {
		t.Errorf("ReviewStatus should remain pending, got %q", repo.state.Tasks[0].ReviewStatus)
	}
}

func TestUpdateTask_ApprovalByNonAssignee(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{
			ID: 1, Title: "Review me", Status: "in_progress",
			AssignedTo: "claude-code", CreatedBy: "cursor",
			RequiresReview: true, ReviewStatus: "pending",
		},
	}

	srv := testServer(svc, logger)

	args := map[string]any{
		"id":            float64(1),
		"review_status": "approved",
		"updated_by":    "cursor",
	}

	_, err := callTool(t, srv, "update_task", args)
	if err != nil {
		t.Fatalf("non-assignee should be able to approve: %v", err)
	}

	task := repo.state.Tasks[0]
	if task.ReviewStatus != "approved" {
		t.Errorf("ReviewStatus = %q, want approved", task.ReviewStatus)
	}
	if task.ReviewedBy != "cursor" {
		t.Errorf("ReviewedBy = %q, want cursor", task.ReviewedBy)
	}
}

func TestUpdateTask_RejectionSetsStatusPending(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{
			ID: 1, Title: "Rejected task", Status: "in_progress",
			AssignedTo: "claude-code", CreatedBy: "cursor",
			RequiresReview: true, ReviewStatus: "pending",
		},
	}

	srv := testServer(svc, logger)

	args := map[string]any{
		"id":            float64(1),
		"review_status": "rejected",
		"updated_by":    "cursor",
	}

	_, err := callTool(t, srv, "update_task", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	task := repo.state.Tasks[0]
	if task.ReviewStatus != "rejected" {
		t.Errorf("ReviewStatus = %q, want rejected", task.ReviewStatus)
	}
	if task.Status != "pending" {
		t.Errorf("Status should be pending after rejection, got %q", task.Status)
	}
	if task.ReviewedBy != "cursor" {
		t.Errorf("ReviewedBy = %q, want cursor", task.ReviewedBy)
	}
}

func TestUpdateTask_CompletionBlockedAfterRejection(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{
			ID: 1, Title: "Rejected then retry", Status: "in_progress",
			AssignedTo: "claude-code", CreatedBy: "cursor",
			RequiresReview: true, ReviewStatus: "rejected", ReviewedBy: "cursor",
		},
	}

	srv := testServer(svc, logger)

	args := map[string]any{
		"id":         float64(1),
		"status":     "completed",
		"updated_by": "claude-code",
	}

	_, err := callTool(t, srv, "update_task", args)
	if err == nil {
		t.Fatal("completion should be blocked after rejection until re-approved")
	}
	if !strings.Contains(err.Error(), "review required") {
		t.Errorf("error should mention review required: %v", err)
	}

	if repo.state.Tasks[0].Status != "in_progress" {
		t.Errorf("status should remain in_progress, got %q", repo.state.Tasks[0].Status)
	}
}

func TestUpdateTask_CompletionWithoutReviewGate(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{
			ID: 1, Title: "No review needed", Status: "in_progress",
			AssignedTo: "cursor", CreatedBy: "cursor",
			RequiresReview: false,
		},
	}

	srv := testServer(svc, logger)

	args := map[string]any{
		"id":         float64(1),
		"status":     "completed",
		"updated_by": "cursor",
	}

	_, err := callTool(t, srv, "update_task", args)
	if err != nil {
		t.Fatalf("should complete without review gate: %v", err)
	}

	if repo.state.Tasks[0].Status != "completed" {
		t.Errorf("status = %q, want completed", repo.state.Tasks[0].Status)
	}
}
