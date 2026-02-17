package collab

import (
	"io"
	"log"
	"strings"
	"testing"

	"github.com/jaakkos/stringwork/internal/domain"
)

// ========== create_plan tests ==========

func TestCreatePlan_Basic(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	args := map[string]any{
		"id":         "test-plan",
		"title":      "Test Plan",
		"goal":       "Test the planning feature",
		"context":    "This is a test context",
		"created_by": "cursor",
	}

	result, err := callTool(t, srv, "create_plan", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !strings.Contains(text, "Plan created") {
		t.Errorf("unexpected result: %s", text)
	}

	// Verify plan was stored
	plan, exists := repo.state.Plans["test-plan"]
	if !exists {
		t.Fatal("plan should exist")
	}

	if plan.Title != "Test Plan" {
		t.Errorf("unexpected title: %q", plan.Title)
	}
	if plan.Goal != "Test the planning feature" {
		t.Errorf("unexpected goal: %q", plan.Goal)
	}
	if plan.Context != "This is a test context" {
		t.Errorf("unexpected context: %q", plan.Context)
	}
	if plan.CreatedBy != "cursor" {
		t.Errorf("unexpected creator: %q", plan.CreatedBy)
	}
	if plan.Status != "active" {
		t.Errorf("unexpected status: %q", plan.Status)
	}

	// Should be set as active
	if repo.state.ActivePlanID != "test-plan" {
		t.Errorf("plan should be active, got %q", repo.state.ActivePlanID)
	}
}

func TestCreatePlan_MissingRequired(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	tests := []struct {
		name string
		args map[string]any
	}{
		{"missing id", map[string]any{"title": "T", "goal": "G", "created_by": "cursor"}},
		{"missing title", map[string]any{"id": "p", "goal": "G", "created_by": "cursor"}},
		{"missing goal", map[string]any{"id": "p", "title": "T", "created_by": "cursor"}},
		{"missing created_by", map[string]any{"id": "p", "title": "T", "goal": "G"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := callTool(t, srv, "create_plan", tt.args)
			if err == nil {
				t.Errorf("expected error for %s", tt.name)
			}
		})
	}
}

func TestCreatePlan_InvalidCreator(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	args := map[string]any{
		"id":         "plan",
		"title":      "Plan",
		"goal":       "Goal",
		"created_by": "unknown",
	}

	_, err := callTool(t, srv, "create_plan", args)
	if err == nil {
		t.Error("expected error for invalid creator")
	}
}

func TestCreatePlan_DuplicateID(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	// Pre-create a plan
	repo.state.Plans["existing"] = &domain.Plan{ID: "existing", Title: "Existing"}

	args := map[string]any{
		"id":         "existing",
		"title":      "New Plan",
		"goal":       "Goal",
		"created_by": "cursor",
	}

	_, err := callTool(t, srv, "create_plan", args)
	if err == nil {
		t.Error("expected error for duplicate plan ID")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error should mention 'already exists': %v", err)
	}
}

func TestCreatePlan_SetActiveTrue(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	args := map[string]any{
		"id":         "plan1",
		"title":      "Plan 1",
		"goal":       "Goal",
		"created_by": "cursor",
		"set_active": true,
	}

	_, err := callTool(t, srv, "create_plan", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if repo.state.ActivePlanID != "plan1" {
		t.Errorf("plan should be active")
	}
}

func TestCreatePlan_SetActiveFalse(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	// Set an existing active plan
	repo.state.ActivePlanID = "old-plan"

	args := map[string]any{
		"id":         "new-plan",
		"title":      "New Plan",
		"goal":       "Goal",
		"created_by": "cursor",
		"set_active": false,
	}

	_, err := callTool(t, srv, "create_plan", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Active plan should not change
	if repo.state.ActivePlanID != "old-plan" {
		t.Errorf("active plan should remain 'old-plan', got %q", repo.state.ActivePlanID)
	}
}

// ========== get_plan tests ==========

func TestGetPlan_NoActivePlan(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	result, err := callTool(t, srv, "get_plan", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !strings.Contains(text, "No active plan") {
		t.Errorf("should indicate no active plan: %s", text)
	}
}

func TestGetPlan_ByID(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	repo.state.Plans["my-plan"] = &domain.Plan{
		ID:        "my-plan",
		Title:     "My Plan",
		Goal:      "Test goal",
		Context:   "Test context",
		CreatedBy: "cursor",
		Status:    "active",
		Items:     []domain.PlanItem{},
	}

	args := map[string]any{"id": "my-plan"}
	result, err := callTool(t, srv, "get_plan", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !strings.Contains(text, "My Plan") {
		t.Error("should include plan title")
	}
	if !strings.Contains(text, "Test goal") {
		t.Error("should include goal")
	}
	if !strings.Contains(text, "Test context") {
		t.Error("should include context")
	}
}

func TestGetPlan_ActivePlan(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	repo.state.Plans["active-plan"] = &domain.Plan{
		ID:        "active-plan",
		Title:     "Active Plan",
		Goal:      "Be active",
		CreatedBy: "cursor",
		Status:    "active",
		Items:     []domain.PlanItem{},
	}
	repo.state.ActivePlanID = "active-plan"

	// No ID specified - should get active plan
	result, err := callTool(t, srv, "get_plan", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !strings.Contains(text, "Active Plan") {
		t.Error("should return active plan")
	}
}

func TestGetPlan_NotFound(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	args := map[string]any{"id": "nonexistent"}
	_, err := callTool(t, srv, "get_plan", args)
	if err == nil {
		t.Error("expected error for non-existent plan")
	}
}

func TestGetPlan_WithItems(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	repo.state.Plans["plan"] = &domain.Plan{
		ID:        "plan",
		Title:     "Plan with Items",
		Goal:      "Goal",
		CreatedBy: "cursor",
		Status:    "active",
		Items: []domain.PlanItem{
			{ID: "1", Title: "Pending Item", Status: "pending", Owner: "cursor"},
			{ID: "2", Title: "In Progress Item", Status: "in_progress", Owner: "claude-code"},
			{ID: "3", Title: "Completed Item", Status: "completed", Owner: "cursor"},
		},
	}
	repo.state.ActivePlanID = "plan"

	result, err := callTool(t, srv, "get_plan", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !strings.Contains(text, "Pending Item") {
		t.Error("should include pending item")
	}
	if !strings.Contains(text, "In Progress Item") {
		t.Error("should include in_progress item")
	}
	if !strings.Contains(text, "Completed Item") {
		t.Error("should include completed item")
	}
	if !strings.Contains(text, "Progress:") {
		t.Error("should include progress summary")
	}
}

// ========== update_plan add_item tests ==========

func TestUpdatePlan_AddItem_Basic(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	repo.state.Plans["plan"] = &domain.Plan{
		ID:     "plan",
		Title:  "Test Plan",
		Items:  []domain.PlanItem{},
		Status: "active",
	}
	repo.state.ActivePlanID = "plan"

	args := map[string]any{
		"action":      "add_item",
		"id":          "item-1",
		"title":       "First Item",
		"description": "Item description",
		"owner":       "cursor",
		"updated_by":  "cursor",
	}

	result, err := callTool(t, srv, "update_plan", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !strings.Contains(text, "Added item") {
		t.Errorf("unexpected result: %s", text)
	}

	// Verify item was added
	plan := repo.state.Plans["plan"]
	if len(plan.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(plan.Items))
	}

	item := plan.Items[0]
	if item.ID != "item-1" {
		t.Errorf("unexpected ID: %q", item.ID)
	}
	if item.Title != "First Item" {
		t.Errorf("unexpected title: %q", item.Title)
	}
	if item.Status != "pending" {
		t.Errorf("new items should be pending, got %q", item.Status)
	}
	if item.Owner != "cursor" {
		t.Errorf("unexpected owner: %q", item.Owner)
	}
}

func TestUpdatePlan_AddItem_WithAcceptance(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	repo.state.Plans["plan"] = &domain.Plan{ID: "plan", Items: []domain.PlanItem{}, Status: "active"}
	repo.state.ActivePlanID = "plan"

	args := map[string]any{
		"action":      "add_item",
		"id":          "item",
		"title":       "Item with Acceptance",
		"reasoning":   "Because testing",
		"acceptance":  []interface{}{"Tests pass", "No regressions"},
		"constraints": []interface{}{"No breaking changes"},
		"updated_by":  "cursor",
	}

	_, err := callTool(t, srv, "update_plan", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	item := repo.state.Plans["plan"].Items[0]
	if item.Reasoning != "Because testing" {
		t.Errorf("unexpected reasoning: %q", item.Reasoning)
	}
	if len(item.Acceptance) != 2 {
		t.Errorf("expected 2 acceptance criteria, got %d", len(item.Acceptance))
	}
	if len(item.Constraints) != 1 {
		t.Errorf("expected 1 constraint, got %d", len(item.Constraints))
	}
}

func TestUpdatePlan_AddItem_MissingRequired(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	repo.state.Plans["plan"] = &domain.Plan{ID: "plan", Items: []domain.PlanItem{}, Status: "active"}
	repo.state.ActivePlanID = "plan"

	tests := []struct {
		name string
		args map[string]any
	}{
		{"missing id", map[string]any{"action": "add_item", "title": "T", "updated_by": "cursor"}},
		{"missing title", map[string]any{"action": "add_item", "id": "1", "updated_by": "cursor"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := callTool(t, srv, "update_plan", tt.args)
			if err == nil {
				t.Errorf("expected error for %s", tt.name)
			}
		})
	}
}

func TestUpdatePlan_AddItem_DuplicateID(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	repo.state.Plans["plan"] = &domain.Plan{
		ID:     "plan",
		Items:  []domain.PlanItem{{ID: "existing", Title: "Existing"}},
		Status: "active",
	}
	repo.state.ActivePlanID = "plan"

	args := map[string]any{
		"action":     "add_item",
		"id":         "existing",
		"title":      "Duplicate",
		"updated_by": "cursor",
	}

	_, err := callTool(t, srv, "update_plan", args)
	if err == nil {
		t.Error("expected error for duplicate item ID")
	}
}

func TestUpdatePlan_AddItem_InvalidOwner(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	repo.state.Plans["plan"] = &domain.Plan{ID: "plan", Items: []domain.PlanItem{}, Status: "active"}
	repo.state.ActivePlanID = "plan"

	args := map[string]any{
		"action":     "add_item",
		"id":         "item",
		"title":      "Item",
		"owner":      "invalid-agent",
		"updated_by": "cursor",
	}

	_, err := callTool(t, srv, "update_plan", args)
	if err == nil {
		t.Error("expected error for invalid owner")
	}
}

// ========== update_plan update_item tests ==========

func TestUpdatePlan_UpdateItem_Status(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	repo.state.Plans["plan"] = &domain.Plan{
		ID:     "plan",
		Items:  []domain.PlanItem{{ID: "1", Title: "Item", Status: "pending", Owner: "cursor"}},
		Status: "active",
	}
	repo.state.ActivePlanID = "plan"

	args := map[string]any{
		"action":     "update_item",
		"item_id":    "1",
		"status":     "in_progress",
		"updated_by": "cursor",
	}

	result, err := callTool(t, srv, "update_plan", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !strings.Contains(text, "status -> in_progress") {
		t.Errorf("should report status change: %s", text)
	}

	if repo.state.Plans["plan"].Items[0].Status != "in_progress" {
		t.Error("status should be updated")
	}
}

func TestUpdatePlan_UpdateItem_Owner(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	repo.state.Plans["plan"] = &domain.Plan{
		ID:     "plan",
		Items:  []domain.PlanItem{{ID: "1", Title: "Item", Status: "pending", Owner: "cursor"}},
		Status: "active",
	}
	repo.state.ActivePlanID = "plan"

	args := map[string]any{
		"action":     "update_item",
		"item_id":    "1",
		"owner":      "claude-code",
		"updated_by": "cursor",
	}

	_, err := callTool(t, srv, "update_plan", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if repo.state.Plans["plan"].Items[0].Owner != "claude-code" {
		t.Error("owner should be updated")
	}
}

func TestUpdatePlan_UpdateItem_AddBlocker(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	repo.state.Plans["plan"] = &domain.Plan{
		ID:     "plan",
		Items:  []domain.PlanItem{{ID: "1", Title: "Item", Status: "pending", Blockers: []string{}}},
		Status: "active",
	}
	repo.state.ActivePlanID = "plan"

	args := map[string]any{
		"action":      "update_item",
		"item_id":     "1",
		"add_blocker": "Waiting for API access",
		"updated_by":  "cursor",
	}

	result, err := callTool(t, srv, "update_plan", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !strings.Contains(text, "added blocker") {
		t.Error("should report blocker added")
	}

	blockers := repo.state.Plans["plan"].Items[0].Blockers
	if len(blockers) != 1 || blockers[0] != "Waiting for API access" {
		t.Errorf("blocker should be added: %v", blockers)
	}
}

func TestUpdatePlan_UpdateItem_RemoveBlocker(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	repo.state.Plans["plan"] = &domain.Plan{
		ID:     "plan",
		Items:  []domain.PlanItem{{ID: "1", Title: "Item", Blockers: []string{"Blocker1", "Blocker2"}}},
		Status: "active",
	}
	repo.state.ActivePlanID = "plan"

	args := map[string]any{
		"action":         "update_item",
		"item_id":        "1",
		"remove_blocker": "Blocker1",
		"updated_by":     "cursor",
	}

	_, err := callTool(t, srv, "update_plan", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	blockers := repo.state.Plans["plan"].Items[0].Blockers
	if len(blockers) != 1 || blockers[0] != "Blocker2" {
		t.Errorf("blocker should be removed: %v", blockers)
	}
}

func TestUpdatePlan_UpdateItem_AddNote(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	repo.state.Plans["plan"] = &domain.Plan{
		ID:     "plan",
		Items:  []domain.PlanItem{{ID: "1", Title: "Item", Notes: []string{}}},
		Status: "active",
	}
	repo.state.ActivePlanID = "plan"

	args := map[string]any{
		"action":     "update_item",
		"item_id":    "1",
		"add_note":   "This is a progress note",
		"updated_by": "cursor",
	}

	_, err := callTool(t, srv, "update_plan", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	notes := repo.state.Plans["plan"].Items[0].Notes
	if len(notes) != 1 {
		t.Fatalf("expected 1 note, got %d", len(notes))
	}
	if !strings.Contains(notes[0], "[cursor]") || !strings.Contains(notes[0], "progress note") {
		t.Errorf("note should include author and content: %s", notes[0])
	}
}

func TestUpdatePlan_UpdateItem_NotFound(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	repo.state.Plans["plan"] = &domain.Plan{ID: "plan", Items: []domain.PlanItem{}, Status: "active"}
	repo.state.ActivePlanID = "plan"

	args := map[string]any{
		"action":     "update_item",
		"item_id":    "nonexistent",
		"status":     "completed",
		"updated_by": "cursor",
	}

	_, err := callTool(t, srv, "update_plan", args)
	if err == nil {
		t.Error("expected error for non-existent item")
	}
}

func TestUpdatePlan_UpdateItem_NoChanges(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	repo.state.Plans["plan"] = &domain.Plan{
		ID:     "plan",
		Items:  []domain.PlanItem{{ID: "1", Title: "Item", Status: "pending"}},
		Status: "active",
	}
	repo.state.ActivePlanID = "plan"

	args := map[string]any{
		"action":     "update_item",
		"item_id":    "1",
		"updated_by": "cursor",
	}

	result, err := callTool(t, srv, "update_plan", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !strings.Contains(text, "Updated item") || !strings.Contains(text, "[]") {
		t.Errorf("should indicate no changes: %s", text)
	}
}

func TestUpdatePlan_NoPlan(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	args := map[string]any{
		"action":     "add_item",
		"id":         "item",
		"title":      "Item",
		"updated_by": "cursor",
	}

	_, err := callTool(t, srv, "update_plan", args)
	if err == nil {
		t.Error("expected error when no plan exists")
	}
	if !strings.Contains(err.Error(), "no active plan") {
		t.Errorf("error should mention no active plan: %v", err)
	}
}

func TestUpdatePlan_SpecificPlanID(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	// Create two plans, set a different one as active
	repo.state.Plans["plan-a"] = &domain.Plan{ID: "plan-a", Items: []domain.PlanItem{}, Status: "active"}
	repo.state.Plans["plan-b"] = &domain.Plan{ID: "plan-b", Items: []domain.PlanItem{}, Status: "active"}
	repo.state.ActivePlanID = "plan-a"

	// Add item to plan-b (not active)
	args := map[string]any{
		"action":     "add_item",
		"plan_id":    "plan-b",
		"id":         "item",
		"title":      "Item in Plan B",
		"updated_by": "cursor",
	}

	_, err := callTool(t, srv, "update_plan", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Item should be in plan-b, not plan-a
	if len(repo.state.Plans["plan-a"].Items) != 0 {
		t.Error("plan-a should have no items")
	}
	if len(repo.state.Plans["plan-b"].Items) != 1 {
		t.Error("plan-b should have 1 item")
	}
}

func TestUpdatePlan_MissingRequired(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	repo.state.Plans["plan"] = &domain.Plan{ID: "plan", Items: []domain.PlanItem{}, Status: "active"}
	repo.state.ActivePlanID = "plan"

	tests := []struct {
		name string
		args map[string]any
	}{
		{"missing action", map[string]any{"updated_by": "cursor"}},
		{"missing updated_by", map[string]any{"action": "add_item"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := callTool(t, srv, "update_plan", tt.args)
			if err == nil {
				t.Errorf("expected error for %s", tt.name)
			}
		})
	}
}

func TestUpdatePlan_InvalidAction(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	repo.state.Plans["plan"] = &domain.Plan{ID: "plan", Items: []domain.PlanItem{}, Status: "active"}
	repo.state.ActivePlanID = "plan"

	args := map[string]any{
		"action":     "invalid_action",
		"updated_by": "cursor",
	}

	_, err := callTool(t, srv, "update_plan", args)
	if err == nil {
		t.Error("expected error for invalid action")
	}
}
