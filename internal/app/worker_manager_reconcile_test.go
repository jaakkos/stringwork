package app

import (
	"strings"
	"sync"
	"testing"

	"github.com/jaakkos/stringwork/internal/domain"
)

// reconcileTestWM builds a WorkerManager wired with just enough state for
// reconcileAfterExit() to run end-to-end. Tests own the *domain.CollabState
// pointer and the mutator serialises against an external mutex so concurrent
// reconcile calls in production-like fan-outs stay race-free under -race.
func reconcileTestWM(t *testing.T, state *domain.CollabState) *WorkerManager {
	t.Helper()
	EnsureStateMaps(state)
	var mu sync.Mutex
	mutator := func(fn func(*domain.CollabState) error) error {
		mu.Lock()
		defer mu.Unlock()
		return fn(state)
	}
	return &WorkerManager{
		stateMutator: mutator,
		stateLoader:  func() (*domain.CollabState, error) { return state, nil },
		logger:       testLogger(t),
	}
}

// findTaskByID returns the *domain.Task with the given ID from state.Tasks,
// or nil. Tests use it to fetch the post-reconcile snapshot without indexing
// by slot (which moves around if more tasks are added later).
func findTaskByID(state *domain.CollabState, id int) *domain.Task {
	for i := range state.Tasks {
		if state.Tasks[i].ID == id {
			return &state.Tasks[i]
		}
	}
	return nil
}

// TestReconcileAfterExit_SkipsSiblingTypeAssignedTask is the regression test
// for the #24 incident: two tasks both assigned_to "claude-code"; the worker
// claude-code-task-1 only owns task #1 (in CurrentTasks). When it exits, the
// pre-fix code matched task #2 by AgentType ("claude-code") and reset it to
// pending with a misleading "Worker claude-code-task-1 exited" summary.
//
// Post-fix, only task #1 is touched.
func TestReconcileAfterExit_SkipsSiblingTypeAssignedTask(t *testing.T) {
	state := &domain.CollabState{
		Tasks: []domain.Task{
			{ID: 1, Title: "owned task", Status: "in_progress", AssignedTo: "claude-code"},
			{ID: 2, Title: "sibling task", Status: "in_progress", AssignedTo: "claude-code"},
		},
		AgentInstances: map[string]*domain.AgentInstance{
			"claude-code-task-1": {
				InstanceID:   "claude-code-task-1",
				AgentType:    "claude-code",
				Role:         domain.RoleWorker,
				CurrentTasks: []int{1},
				Status:       "busy",
			},
		},
	}
	wm := reconcileTestWM(t, state)

	wm.reconcileAfterExit(WorkerSpawnConfig{
		InstanceID: "claude-code-task-1",
		AgentType:  "claude-code",
	})

	owned := findTaskByID(state, 1)
	if owned == nil || owned.Status != "pending" {
		t.Errorf("task #1 (owned): status = %q, want pending", statusOrNil(owned))
	}
	sibling := findTaskByID(state, 2)
	if sibling == nil {
		t.Fatal("task #2 missing")
	}
	if sibling.Status != "in_progress" {
		t.Errorf("task #2 (sibling, NOT owned by exited worker): status = %q, want in_progress (must not be swept)", sibling.Status)
	}
	if sibling.ResultSummary != "" {
		t.Errorf("task #2: ResultSummary = %q, want empty (no misleading 'Worker claude-code-task-1 exited' message)", sibling.ResultSummary)
	}
}

// TestReconcileAfterExit_ResetsOwnedTask covers the happy path: the exiting
// worker actually had this task in CurrentTasks, so reconcile resets it and
// records a meaningful summary.
func TestReconcileAfterExit_ResetsOwnedTask(t *testing.T) {
	state := &domain.CollabState{
		Tasks: []domain.Task{
			{ID: 5, Title: "owned task", Status: "in_progress", AssignedTo: "claude-code"},
		},
		AgentInstances: map[string]*domain.AgentInstance{
			"claude-code-task-5": {
				InstanceID:   "claude-code-task-5",
				AgentType:    "claude-code",
				Role:         domain.RoleWorker,
				CurrentTasks: []int{5},
				Status:       "busy",
			},
		},
	}
	wm := reconcileTestWM(t, state)

	wm.reconcileAfterExit(WorkerSpawnConfig{
		InstanceID: "claude-code-task-5",
		AgentType:  "claude-code",
	})

	got := findTaskByID(state, 5)
	if got == nil {
		t.Fatal("task #5 missing")
	}
	if got.Status != "pending" {
		t.Errorf("status = %q, want pending", got.Status)
	}
	if !strings.Contains(got.ResultSummary, "claude-code-task-5") {
		t.Errorf("ResultSummary = %q, want it to mention the owning worker", got.ResultSummary)
	}
	if !strings.Contains(got.ResultSummary, "get_work_context") {
		t.Errorf("ResultSummary = %q, want it to point at get_work_context for previous attempt output", got.ResultSummary)
	}
	inst := state.AgentInstances["claude-code-task-5"]
	if inst == nil {
		t.Fatal("instance row missing")
	}
	if len(inst.CurrentTasks) != 0 {
		t.Errorf("CurrentTasks = %v, want emptied after reconcile", inst.CurrentTasks)
	}
}

// TestReconcileAfterExit_ResetsInstanceLevelAssignedTask exercises the
// "instance-level assignment, no CurrentTasks bookkeeping" fallback: a task
// was explicitly assigned to a specific InstanceID (not a type), the worker
// never made it into CurrentTasks (e.g. crashed before the claim landed),
// and the worker exited. Reconcile must still pick it up so the task doesn't
// orphan, and must use the "before claiming" phrasing rather than the
// owner-exit phrasing.
func TestReconcileAfterExit_ResetsInstanceLevelAssignedTask(t *testing.T) {
	state := &domain.CollabState{
		Tasks: []domain.Task{
			{ID: 9, Title: "instance-pinned task", Status: "in_progress", AssignedTo: "claude-code-task-9"},
		},
		AgentInstances: map[string]*domain.AgentInstance{
			"claude-code-task-9": {
				InstanceID:   "claude-code-task-9",
				AgentType:    "claude-code",
				Role:         domain.RoleWorker,
				CurrentTasks: nil,
				Status:       "busy",
			},
		},
	}
	wm := reconcileTestWM(t, state)

	wm.reconcileAfterExit(WorkerSpawnConfig{
		InstanceID: "claude-code-task-9",
		AgentType:  "claude-code",
	})

	got := findTaskByID(state, 9)
	if got == nil {
		t.Fatal("task #9 missing")
	}
	if got.Status != "pending" {
		t.Errorf("status = %q, want pending (instance-level assignment must still be reset)", got.Status)
	}
	if !strings.Contains(got.ResultSummary, "before claiming") {
		t.Errorf("ResultSummary = %q, want the 'before claiming this instance-assigned task' phrasing", got.ResultSummary)
	}
}

// TestReconcileAfterExit_IgnoresOtherWorkersTasks pins down the new ownership
// rule: a task owned by a sibling instance of the same type is not touched
// when a different worker exits. Pre-fix this would fail because the
// AgentType match would catch the sibling's task too.
func TestReconcileAfterExit_IgnoresOtherWorkersTasks(t *testing.T) {
	state := &domain.CollabState{
		Tasks: []domain.Task{
			{ID: 7, Title: "owned by claude-code-2", Status: "in_progress", AssignedTo: "claude-code"},
		},
		AgentInstances: map[string]*domain.AgentInstance{
			"claude-code-1": {
				InstanceID:   "claude-code-1",
				AgentType:    "claude-code",
				Role:         domain.RoleWorker,
				CurrentTasks: nil,
				Status:       "idle",
			},
			"claude-code-2": {
				InstanceID:   "claude-code-2",
				AgentType:    "claude-code",
				Role:         domain.RoleWorker,
				CurrentTasks: []int{7},
				Status:       "busy",
			},
		},
	}
	wm := reconcileTestWM(t, state)

	wm.reconcileAfterExit(WorkerSpawnConfig{
		InstanceID: "claude-code-1",
		AgentType:  "claude-code",
	})

	got := findTaskByID(state, 7)
	if got == nil {
		t.Fatal("task #7 missing")
	}
	if got.Status != "in_progress" {
		t.Errorf("status = %q, want in_progress (the exiting worker did not own this task)", got.Status)
	}
	owner := state.AgentInstances["claude-code-2"]
	if owner == nil || len(owner.CurrentTasks) != 1 || owner.CurrentTasks[0] != 7 {
		t.Errorf("claude-code-2 CurrentTasks = %v, want [7] (must not have been mutated)", owner.CurrentTasks)
	}
}

func statusOrNil(t *domain.Task) string {
	if t == nil {
		return "<nil>"
	}
	return t.Status
}

// TestReconcileAfterExit_OwnerExitMessage and
// TestReconcileAfterExit_UnclaimedAssignmentMessage are the Phase 5
// message-text guards. They live here because they exercise the same
// reconciler entry point as Phase 1's ownership tests.
func TestReconcileAfterExit_OwnerExitMessage(t *testing.T) {
	state := &domain.CollabState{
		Tasks: []domain.Task{
			{ID: 11, Title: "owned task", Status: "in_progress", AssignedTo: "claude-code"},
		},
		AgentInstances: map[string]*domain.AgentInstance{
			"claude-code-task-11": {
				InstanceID:   "claude-code-task-11",
				AgentType:    "claude-code",
				Role:         domain.RoleWorker,
				CurrentTasks: []int{11},
			},
		},
	}
	wm := reconcileTestWM(t, state)
	wm.reconcileAfterExit(WorkerSpawnConfig{InstanceID: "claude-code-task-11", AgentType: "claude-code"})

	got := findTaskByID(state, 11)
	if got == nil {
		t.Fatal("task #11 missing")
	}
	const wantPhrase = "exited while task in_progress"
	if !strings.Contains(got.ResultSummary, wantPhrase) {
		t.Errorf("ResultSummary = %q, want it to contain %q (owner-exit phrasing)", got.ResultSummary, wantPhrase)
	}
	if !strings.Contains(got.ResultSummary, "get_work_context task_id=11") {
		t.Errorf("ResultSummary = %q, want explicit pointer to `get_work_context task_id=11`", got.ResultSummary)
	}
	if strings.Contains(got.ResultSummary, "Check worker log for details") {
		t.Errorf("ResultSummary = %q, must NOT contain the old generic 'Check worker log for details' phrasing", got.ResultSummary)
	}
}

func TestReconcileAfterExit_UnclaimedAssignmentMessage(t *testing.T) {
	state := &domain.CollabState{
		Tasks: []domain.Task{
			{ID: 13, Title: "instance-pinned task", Status: "in_progress", AssignedTo: "claude-code-task-13"},
		},
		AgentInstances: map[string]*domain.AgentInstance{
			"claude-code-task-13": {
				InstanceID:   "claude-code-task-13",
				AgentType:    "claude-code",
				Role:         domain.RoleWorker,
				CurrentTasks: nil,
			},
		},
	}
	wm := reconcileTestWM(t, state)
	wm.reconcileAfterExit(WorkerSpawnConfig{InstanceID: "claude-code-task-13", AgentType: "claude-code"})

	got := findTaskByID(state, 13)
	if got == nil {
		t.Fatal("task #13 missing")
	}
	const wantPhrase = "before claiming this instance-assigned task"
	if !strings.Contains(got.ResultSummary, wantPhrase) {
		t.Errorf("ResultSummary = %q, want it to contain %q (unclaimed-assignment phrasing)", got.ResultSummary, wantPhrase)
	}
}
