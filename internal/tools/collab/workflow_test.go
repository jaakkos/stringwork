package collab

import (
	"fmt"
	"io"
	"log"
	"strconv"
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

// TestHandoff_RemovesTaskFromActualTaskBoundOwner (C2) — when the
// task is being run by a task-bound child (e.g. "claude-code-task-7")
// while a static-pool sibling ("claude-code") also exists, handoff must
// remove the task from the actual owner — the task-bound row — not just
// from whichever instance happens to match the parent agent name first.
// Previously the early-return-on-direct-lookup hit the static row,
// found nothing to remove, and left the task-bound row's CurrentTasks
// pointing at a now-handed-off task.
func TestHandoff_RemovesTaskFromActualTaskBoundOwner(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.DriverID = "cursor"
	repo.state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver,
		Status: "working", MaxTasks: 5,
	}
	staticID, taskBoundID := seedStaticAndTaskBound(t, repo.state, "claude-code", 1)
	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "T", Status: "in_progress", AssignedTo: "claude-code", CreatedBy: "cursor"},
	}
	repo.state.NextTaskID = 2

	srv := testServer(svc, logger)

	args := map[string]any{
		"from":       "claude-code",
		"to":         "cursor",
		"task_id":    float64(1),
		"summary":    "Handing back",
		"next_steps": "Take over",
	}
	if _, err := callTool(t, srv, "handoff", args); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	taskBoundInst := repo.state.AgentInstances[taskBoundID]
	if taskBoundInst == nil {
		t.Fatalf("task-bound instance %q missing", taskBoundID)
	}
	for _, id := range taskBoundInst.CurrentTasks {
		if id == 1 {
			t.Errorf("task-bound owner %q must drop task #1 after handoff; got CurrentTasks=%v", taskBoundID, taskBoundInst.CurrentTasks)
		}
	}
	if taskBoundInst.Status != "idle" {
		t.Errorf("task-bound instance should be idle after losing its only task; got %q", taskBoundInst.Status)
	}

	// The static-pool sibling never owned the task and should remain idle/empty.
	staticInst := repo.state.AgentInstances[staticID]
	if staticInst == nil {
		t.Fatalf("static instance %q missing", staticID)
	}
	if len(staticInst.CurrentTasks) != 0 {
		t.Errorf("static instance should still have no tasks; got %v", staticInst.CurrentTasks)
	}
}

// TestHandoff_NormalizesInstanceIDToParentType (H1) — when the user
// hands off to a concrete instance ID (e.g. "claude-code-1") or a
// task-bound ID, the task's AssignedTo must be normalized to the parent
// agent type so downstream watchdog and liveness checks can correlate
// task-bound child workers with the assignment. Storing the raw
// instance ID was the root cause of long-lived "AssignedTo holds an
// ephemeral ID" corruption that MigrateTaskBoundCorruption had to
// repair on load.
func TestHandoff_NormalizesInstanceIDToParentType(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.DriverID = "cursor"
	repo.state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver,
		Status: "working", MaxTasks: 5,
	}
	delete(repo.state.AgentInstances, "claude-code")
	repo.state.AgentInstances["claude-code-1"] = &domain.AgentInstance{
		InstanceID:    "claude-code-1",
		AgentType:     "claude-code",
		Role:          domain.RoleWorker,
		Status:        "idle",
		MaxTasks:      1,
		CurrentTasks:  []int{},
		LastHeartbeat: time.Now(),
	}
	repo.state.RegisteredAgents["claude-code"] = &domain.RegisteredAgent{
		Name: "claude-code", RegisteredAt: time.Now(), LastSeen: time.Now(),
	}

	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "T", Status: "in_progress", AssignedTo: "cursor", CreatedBy: "cursor"},
	}
	repo.state.NextTaskID = 2

	srv := testServer(svc, logger)

	args := map[string]any{
		"from":       "cursor",
		"to":         "claude-code-1",
		"task_id":    float64(1),
		"summary":    "Handing off",
		"next_steps": "Implement",
	}
	if _, err := callTool(t, srv, "handoff", args); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if repo.state.Tasks[0].AssignedTo != "claude-code" {
		t.Errorf("task.AssignedTo should normalize to parent type 'claude-code', got %q", repo.state.Tasks[0].AssignedTo)
	}
}

// TestHandoff_TaskIDAcrossMultipleSiblings (C2) — when several
// task-bound siblings of the same parent type exist and the handoff
// targets a specific task ID, removal must scan every instance and
// strip the task from whichever one is actually carrying it. The pre-
// fix early-return logic broke this when state.AgentInstances had a
// direct hit on the parent name but the real owner was a sibling.
func TestHandoff_TaskIDAcrossMultipleSiblings(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.DriverID = "cursor"
	repo.state.AgentInstances["cursor"] = &domain.AgentInstance{
		InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver,
		Status: "working", MaxTasks: 5,
	}
	delete(repo.state.AgentInstances, "claude-code")
	repo.state.RegisteredAgents["claude-code"] = &domain.RegisteredAgent{
		Name: "claude-code", RegisteredAt: time.Now(), LastSeen: time.Now(),
	}
	repo.state.AgentInstances["claude-code"] = &domain.AgentInstance{
		InstanceID: "claude-code", AgentType: "claude-code",
		Role: domain.RoleWorker, Status: "idle", MaxTasks: 1, CurrentTasks: []int{},
	}
	repo.state.AgentInstances["claude-code-task-1"] = &domain.AgentInstance{
		InstanceID: "claude-code-task-1", AgentType: "claude-code",
		Role: domain.RoleWorker, Status: "busy", MaxTasks: 1, CurrentTasks: []int{1},
	}
	repo.state.AgentInstances["claude-code-task-2"] = &domain.AgentInstance{
		InstanceID: "claude-code-task-2", AgentType: "claude-code",
		Role: domain.RoleWorker, Status: "busy", MaxTasks: 1, CurrentTasks: []int{2},
	}

	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "T1", Status: "in_progress", AssignedTo: "claude-code", CreatedBy: "cursor"},
		{ID: 2, Title: "T2", Status: "in_progress", AssignedTo: "claude-code", CreatedBy: "cursor"},
	}
	repo.state.NextTaskID = 3

	srv := testServer(svc, logger)

	args := map[string]any{
		"from":       "claude-code",
		"to":         "cursor",
		"task_id":    float64(2),
		"summary":    "Handing back T2",
		"next_steps": "Finish",
	}
	if _, err := callTool(t, srv, "handoff", args); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	owner1 := repo.state.AgentInstances["claude-code-task-1"]
	owner2 := repo.state.AgentInstances["claude-code-task-2"]

	if owner1 == nil || len(owner1.CurrentTasks) != 1 || owner1.CurrentTasks[0] != 1 {
		t.Errorf("claude-code-task-1 should still hold task 1, got %v", owner1.CurrentTasks)
	}
	if owner2 == nil {
		t.Fatal("claude-code-task-2 missing")
	}
	for _, id := range owner2.CurrentTasks {
		if id == 2 {
			t.Errorf("claude-code-task-2 should drop task #2 after handoff, got %v", owner2.CurrentTasks)
		}
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

// TestClaimNext_AddsCurrentTasksWhenPreAssigned (C1) — when a task is
// already assigned to the parent agent type but no instance has it in
// CurrentTasks (e.g. orchestrator picked the assignee but bookkeeping
// wasn't applied, or the instance was respawned), claim_next must still
// add the task to the claimant's CurrentTasks so the worker actually
// owns the work. The previous "wasAssignedElsewhere" guard suppressed
// this fix when AssignedTo already matched the parent type.
func TestClaimNext_AddsCurrentTasksWhenPreAssigned(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Pre-assigned", Status: "pending", AssignedTo: "claude-code", CreatedBy: "cursor", Priority: 3},
	}
	repo.state.NextTaskID = 2
	if inst, ok := repo.state.AgentInstances["claude-code"]; ok && inst != nil {
		inst.CurrentTasks = []int{}
		inst.Status = "idle"
	}

	srv := testServer(svc, logger)

	args := map[string]any{"agent": "claude-code"}
	if _, err := callTool(t, srv, "claim_next", args); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	inst := repo.state.AgentInstances["claude-code"]
	if inst == nil {
		t.Fatal("claude-code instance missing after claim_next")
	}
	found := false
	for _, id := range inst.CurrentTasks {
		if id == 1 {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected task 1 in CurrentTasks for static instance after claim_next; got %v", inst.CurrentTasks)
	}
	if inst.Status != "busy" {
		t.Errorf("expected static instance to be 'busy' after claiming, got %q", inst.Status)
	}
}

// TestClaimNext_DistinguishesMultipleTaskBoundInstances (C1/M6) — when
// no instance keyed by the agent name exists but multiple instances of
// the same parent type do (one static-pool, several task-bound siblings
// for unrelated tasks), claim_next must add the new task to the static
// pool row, not to a task-bound sibling. The previous fallback iterated
// the AgentInstances map non-deterministically and could land on a
// task-bound row, which would later be reaped when its OWN task
// completed — silently losing the just-claimed task.
//
// We seed many task-bound siblings against a single static row and
// repeat the claim across fresh states so the random map iteration is
// statistically guaranteed to expose the bug pre-fix.
func TestClaimNext_DistinguishesMultipleTaskBoundInstances(t *testing.T) {
	const iterations = 30
	const taskBoundSiblings = 8

	for iter := 0; iter < iterations; iter++ {
		svc, repo := newTestService()
		logger := log.New(io.Discard, "", 0)

		delete(repo.state.AgentInstances, "claude-code")
		repo.state.AgentInstances["claude-code-1"] = &domain.AgentInstance{
			InstanceID:    "claude-code-1",
			AgentType:     "claude-code",
			Role:          domain.RoleWorker,
			Status:        "idle",
			MaxTasks:      1,
			CurrentTasks:  []int{},
			LastHeartbeat: time.Now(),
		}
		for i := 0; i < taskBoundSiblings; i++ {
			oldTaskID := 100 + i
			id := "claude-code-task-" + strconv.Itoa(oldTaskID)
			repo.state.AgentInstances[id] = &domain.AgentInstance{
				InstanceID:    id,
				AgentType:     "claude-code",
				Role:          domain.RoleWorker,
				Status:        "busy",
				MaxTasks:      1,
				CurrentTasks:  []int{oldTaskID},
				LastHeartbeat: time.Now(),
			}
		}

		repo.state.Tasks = []domain.Task{
			{ID: 7, Title: "New work", Status: "pending", AssignedTo: "any", CreatedBy: "cursor", Priority: 3},
		}
		repo.state.NextTaskID = 8

		srv := testServer(svc, logger)

		args := map[string]any{"agent": "claude-code"}
		if _, err := callTool(t, srv, "claim_next", args); err != nil {
			t.Fatalf("iter %d: unexpected error: %v", iter, err)
		}

		staticInst := repo.state.AgentInstances["claude-code-1"]
		staticOwns := false
		for _, id := range staticInst.CurrentTasks {
			if id == 7 {
				staticOwns = true
				break
			}
		}
		if !staticOwns {
			t.Fatalf("iter %d: static instance should own claimed task #7, got CurrentTasks=%v", iter, staticInst.CurrentTasks)
		}

		for instID, inst := range repo.state.AgentInstances {
			if instID == "claude-code-1" {
				continue
			}
			for _, id := range inst.CurrentTasks {
				if id == 7 {
					t.Fatalf("iter %d: task-bound sibling %q must not own task #7, got CurrentTasks=%v", iter, instID, inst.CurrentTasks)
				}
			}
		}
	}
}

// TestPriorityTieBreak_DeterministicByTaskID (M7) — when multiple
// pending tasks share the same priority, the lowest task ID wins. The
// previous code used strict `<` on priority alone and then took the
// first-encountered task in slice order, which is non-deterministic
// once tasks are inserted out of ID order (e.g. after a replay).
func TestPriorityTieBreak_DeterministicByTaskID(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.Tasks = []domain.Task{
		{ID: 5, Title: "Higher ID first", Status: "pending", AssignedTo: "any", CreatedBy: "cursor", Priority: 2},
		{ID: 1, Title: "Lower ID later", Status: "pending", AssignedTo: "any", CreatedBy: "cursor", Priority: 2},
	}
	repo.state.NextTaskID = 6

	srv := testServer(svc, logger)

	args := map[string]any{"agent": "cursor"}
	result, err := callTool(t, srv, "claim_next", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !strings.Contains(text, "Lower ID later") {
		t.Errorf("expected lower-ID task to win priority tie; got: %s", text)
	}

	if repo.state.Tasks[1].Status != "in_progress" {
		t.Errorf("expected task #1 to be claimed; got status=%q", repo.state.Tasks[1].Status)
	}
	if repo.state.Tasks[0].Status != "pending" {
		t.Errorf("expected task #5 to remain pending; got status=%q", repo.state.Tasks[0].Status)
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

// TestRequestReview_StampsCodeReviewTemplate locks in Phase-3
// behaviour: the legacy single-task entry point must tag the
// resulting task with Template="code-review" so the constitution
// alias rule attaches review-scoped sources without the title
// containing "review". Aspect stays empty because request_review
// does NOT decompose into multiple aspects (the task_plan path
// owns multi-aspect spawns).
func TestRequestReview_StampsCodeReviewTemplate(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	_, err := callTool(t, srv, "request_review", map[string]any{
		"from":        "cursor",
		"to":          "claude-code",
		"description": "Audit auth flow", // intentionally no "review" substring in title
		"files":       []interface{}{"auth.go"},
	})
	if err != nil {
		t.Fatalf("request_review: %v", err)
	}
	if len(repo.state.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(repo.state.Tasks))
	}
	task := repo.state.Tasks[0]
	if task.Template != "code-review" {
		t.Errorf("Template = %q, want \"code-review\"", task.Template)
	}
	if task.Aspect != "" {
		t.Errorf("Aspect = %q, want empty (request_review is single-task)", task.Aspect)
	}
}

// TestRequestReview_NotifiesReviewer (H5) — request_review must enqueue
// a system message addressed to the reviewer. Without it, a review task
// silently sits in the reviewer's pending queue with no banner, no
// nudge, no signal — the original asymmetry that masked H5 in code
// reviews where the reviewer never started reading.
func TestRequestReview_NotifiesReviewer(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	args := map[string]any{
		"from":        "cursor",
		"to":          "claude-code",
		"description": "review the auth refactor",
		"files":       []interface{}{"auth.go"},
	}

	_, err := callTool(t, srv, "request_review", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(repo.state.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(repo.state.Tasks))
	}
	taskID := repo.state.Tasks[0].ID

	var found bool
	for _, msg := range repo.state.Messages {
		if msg.To != "claude-code" {
			continue
		}
		if msg.From != "system" && msg.From != "cursor" {
			continue
		}
		if !strings.Contains(strings.ToLower(msg.Content), "review") {
			continue
		}
		if !strings.Contains(msg.Content, fmt.Sprintf("#%d", taskID)) {
			continue
		}
		found = true
		break
	}
	if !found {
		t.Errorf("expected a system/cursor message to claude-code referencing review task #%d; got %d messages: %+v",
			taskID, len(repo.state.Messages), repo.state.Messages)
	}
}
