package app

import (
	"testing"

	"github.com/jaakkos/stringwork/internal/domain"
)

func TestReassignTask_ExcludesBlocker(t *testing.T) {
	orch := NewTaskOrchestrator(nil, "least_loaded")

	state := &domain.CollabState{
		DriverID: "cursor",
		AgentInstances: map[string]*domain.AgentInstance{
			"cursor":      {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "working", MaxTasks: 5},
			"gemini":      {InstanceID: "gemini", AgentType: "gemini", Role: domain.RoleWorker, Status: "busy", MaxTasks: 3, CurrentTasks: []int{1}},
			"claude-code": {InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker, Status: "idle", MaxTasks: 3},
			"codex":       {InstanceID: "codex", AgentType: "codex", Role: domain.RoleWorker, Status: "idle", MaxTasks: 3},
		},
	}

	task := &domain.Task{ID: 1, Title: "Review code", Status: "blocked", AssignedTo: "gemini"}

	result := orch.ReassignTask(task, state, []string{"gemini"})
	if result == "" {
		t.Fatal("expected reassignment to succeed")
	}
	if result == "gemini" {
		t.Error("should not reassign to the excluded agent type")
	}
	if task.AssignedTo == "gemini" {
		t.Error("task.AssignedTo should be updated away from gemini")
	}
}

func TestReassignTask_NoAlternative(t *testing.T) {
	orch := NewTaskOrchestrator(nil, "least_loaded")

	state := &domain.CollabState{
		DriverID: "cursor",
		AgentInstances: map[string]*domain.AgentInstance{
			"cursor": {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "working", MaxTasks: 5},
			"gemini": {InstanceID: "gemini", AgentType: "gemini", Role: domain.RoleWorker, Status: "busy", MaxTasks: 3, CurrentTasks: []int{1}},
		},
	}

	task := &domain.Task{ID: 1, Title: "Review code", Status: "blocked", AssignedTo: "gemini"}

	result := orch.ReassignTask(task, state, []string{"gemini"})
	if result != "" {
		t.Errorf("expected empty result when no alternative, got %q", result)
	}
}

func TestReassignTask_ExcludesMultipleTypes(t *testing.T) {
	orch := NewTaskOrchestrator(nil, "least_loaded")

	state := &domain.CollabState{
		DriverID: "cursor",
		AgentInstances: map[string]*domain.AgentInstance{
			"cursor":      {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "working", MaxTasks: 5},
			"gemini":      {InstanceID: "gemini", AgentType: "gemini", Role: domain.RoleWorker, Status: "idle", MaxTasks: 3},
			"claude-code": {InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker, Status: "idle", MaxTasks: 3},
			"codex":       {InstanceID: "codex", AgentType: "codex", Role: domain.RoleWorker, Status: "idle", MaxTasks: 3},
		},
	}

	task := &domain.Task{ID: 1, Title: "Review code", Status: "blocked", AssignedTo: "gemini"}

	result := orch.ReassignTask(task, state, []string{"gemini", "claude-code"})
	if result != "codex" {
		t.Errorf("expected reassignment to codex, got %q", result)
	}
}

func TestReassignTask_PicksLeastLoaded(t *testing.T) {
	orch := NewTaskOrchestrator(nil, "least_loaded")

	state := &domain.CollabState{
		DriverID: "cursor",
		AgentInstances: map[string]*domain.AgentInstance{
			"cursor":      {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "working", MaxTasks: 5},
			"gemini":      {InstanceID: "gemini", AgentType: "gemini", Role: domain.RoleWorker, Status: "busy", MaxTasks: 3, CurrentTasks: []int{1}},
			"claude-code": {InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker, Status: "busy", MaxTasks: 3, CurrentTasks: []int{2, 3}},
			"codex":       {InstanceID: "codex", AgentType: "codex", Role: domain.RoleWorker, Status: "idle", MaxTasks: 3},
		},
	}

	task := &domain.Task{ID: 1, Title: "Review code", Status: "blocked", AssignedTo: "gemini"}

	result := orch.ReassignTask(task, state, []string{"gemini"})
	if result != "codex" {
		t.Errorf("expected reassignment to least-loaded codex, got %q", result)
	}
}

// TestAssignTask_StoresParentType verifies that AssignTask sets task.AssignedTo
// to the worker's parent AgentType (e.g. "claude-code"), not the concrete
// InstanceID ("claude-code-1"). This is the key invariant that lets the
// watchdog correlate task-bound children like "claude-code-task-7" with
// their assigned task.
func TestAssignTask_StoresParentType(t *testing.T) {
	orch := NewTaskOrchestrator(nil, "least_loaded")

	state := &domain.CollabState{
		DriverID: "cursor",
		AgentInstances: map[string]*domain.AgentInstance{
			"cursor": {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "working", MaxTasks: 5},
			"claude-code-1": {
				InstanceID: "claude-code-1", AgentType: "claude-code",
				Role: domain.RoleWorker, Status: "idle", MaxTasks: 3,
			},
		},
	}

	task := &domain.Task{ID: 1, Title: "Parent-type test", Status: "pending", AssignedTo: "any"}

	result := orch.AssignTask(task, state)
	if result != "claude-code" {
		t.Fatalf("AssignTask should return parent type, got %q", result)
	}
	if task.AssignedTo != "claude-code" {
		t.Errorf("task.AssignedTo should be parent type 'claude-code', got %q", task.AssignedTo)
	}

	inst := state.AgentInstances["claude-code-1"]
	found := false
	for _, tid := range inst.CurrentTasks {
		if tid == task.ID {
			found = true
			break
		}
	}
	if !found {
		t.Error("concrete instance 'claude-code-1' should still own the task via CurrentTasks")
	}
}

// TestOrchestrator_SkipsOfflineCandidates (H4) — workers with status
// "offline" must not be selected for new task assignments. Otherwise the
// orchestrator hands work to a process that isn't connected, the task
// sits idle until the watchdog reaps it, and the user sees mysterious
// "task assigned but never started" failures.
func TestOrchestrator_SkipsOfflineCandidates(t *testing.T) {
	orch := NewTaskOrchestrator(nil, "least_loaded")

	state := &domain.CollabState{
		DriverID: "cursor",
		AgentInstances: map[string]*domain.AgentInstance{
			"cursor":      {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "working", MaxTasks: 5},
			"claude-code": {InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker, Status: "offline", MaxTasks: 1, CurrentTasks: []int{}},
			"codex":       {InstanceID: "codex", AgentType: "codex", Role: domain.RoleWorker, Status: "idle", MaxTasks: 1, CurrentTasks: []int{}},
		},
	}

	task := &domain.Task{ID: 1, Title: "New work", Status: "pending", AssignedTo: "any"}

	result := orch.AssignTask(task, state)
	if result == "claude-code" {
		t.Errorf("orchestrator should not assign offline worker; got %q", result)
	}
	if result != "codex" {
		t.Errorf("expected codex (only online candidate); got %q", result)
	}
}

// TestOrchestrator_SkipsTaskBoundCandidates (M6) — task-bound instances
// (InstanceID "<type>-task-N") exist solely to run their owning task and
// must never be picked up for additional work. Selecting one for a new
// task results in two tasks racing on the same worker process and a
// reap surprise when the original task completes.
func TestOrchestrator_SkipsTaskBoundCandidates(t *testing.T) {
	orch := NewTaskOrchestrator(nil, "least_loaded")

	state := &domain.CollabState{
		DriverID: "cursor",
		AgentInstances: map[string]*domain.AgentInstance{
			"cursor": {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "working", MaxTasks: 5},
			"claude-code-task-99": {
				InstanceID: "claude-code-task-99", AgentType: "claude-code",
				Role: domain.RoleWorker, Status: "idle", MaxTasks: 1, CurrentTasks: []int{},
			},
		},
	}

	task := &domain.Task{ID: 7, Title: "New work", Status: "pending", AssignedTo: "any"}

	result := orch.AssignTask(task, state)
	if result == "claude-code" {
		t.Errorf("orchestrator must not select task-bound instance for new work; got %q", result)
	}
	if result != "" {
		t.Errorf("expected no assignment (only candidate is task-bound); got %q", result)
	}
}

// TestReassignTask_StoresParentType ensures ReassignTask follows the same
// parent-type convention as AssignTask.
func TestReassignTask_StoresParentType(t *testing.T) {
	orch := NewTaskOrchestrator(nil, "least_loaded")

	state := &domain.CollabState{
		DriverID: "cursor",
		AgentInstances: map[string]*domain.AgentInstance{
			"cursor": {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "working", MaxTasks: 5},
			"gemini-1": {
				InstanceID: "gemini-1", AgentType: "gemini",
				Role: domain.RoleWorker, Status: "busy", MaxTasks: 3, CurrentTasks: []int{1},
			},
			"codex-1": {
				InstanceID: "codex-1", AgentType: "codex",
				Role: domain.RoleWorker, Status: "idle", MaxTasks: 3,
			},
		},
	}

	task := &domain.Task{ID: 1, Title: "Reassign test", Status: "blocked", AssignedTo: "gemini"}

	result := orch.ReassignTask(task, state, []string{"gemini"})
	if result != "codex" {
		t.Fatalf("ReassignTask should return parent type 'codex', got %q", result)
	}
	if task.AssignedTo != "codex" {
		t.Errorf("task.AssignedTo should be parent type 'codex', got %q", task.AssignedTo)
	}
}

// fakeKnownTypes implements KnownTypesProvider for tests.
type fakeKnownTypes struct{ types []string }

func (f fakeKnownTypes) KnownAgentTypes() []string { return f.types }

// fakeBackoffChecker implements BackoffChecker for tests.
type fakeBackoffChecker struct{ excluded []string }

func (f fakeBackoffChecker) BackedOffAgentTypes() []string { return f.excluded }

// TestAssignTask_FallsBackToConfiguredTypeWhenAllWorkersOffline covers Fix A:
// when every AgentInstance is offline (typical right after server start, or
// when a daemon comes back to find an empty live pool), AssignTask must NOT
// silently return "". The KnownTypesProvider fallback gives create_task an
// agent type to spawn, so SpawnForTask actually fires.
func TestAssignTask_FallsBackToConfiguredTypeWhenAllWorkersOffline(t *testing.T) {
	orch := NewTaskOrchestrator(nil, "least_loaded")
	orch.SetKnownTypesProvider(fakeKnownTypes{types: []string{"claude-code", "codex"}})

	state := &domain.CollabState{
		DriverID: "cursor",
		AgentInstances: map[string]*domain.AgentInstance{
			"cursor":        {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "working", MaxTasks: 5},
			"claude-code-1": {InstanceID: "claude-code-1", AgentType: "claude-code", Role: domain.RoleWorker, Status: "offline", MaxTasks: 1},
			"codex-1":       {InstanceID: "codex-1", AgentType: "codex", Role: domain.RoleWorker, Status: "offline", MaxTasks: 1},
		},
	}
	task := &domain.Task{ID: 1, Title: "Cold start", Status: "pending", AssignedTo: "any"}

	result := orch.AssignTask(task, state)
	if result == "" {
		t.Fatal("expected fallback assignment when all workers offline; got empty result (Bug A)")
	}
	if result != "claude-code" {
		t.Errorf("expected first declared type 'claude-code', got %q", result)
	}
	if task.AssignedTo != "claude-code" {
		t.Errorf("task.AssignedTo should be set to 'claude-code', got %q", task.AssignedTo)
	}
}

// TestAssignTask_FallbackHonorsWorkerType ensures that when task.WorkerType
// pins a specific type, the fallback respects it instead of taking the first
// configured type.
func TestAssignTask_FallbackHonorsWorkerType(t *testing.T) {
	orch := NewTaskOrchestrator(nil, "least_loaded")
	orch.SetKnownTypesProvider(fakeKnownTypes{types: []string{"claude-code", "codex"}})

	state := &domain.CollabState{
		DriverID:       "cursor",
		AgentInstances: map[string]*domain.AgentInstance{}, // empty pool
	}
	task := &domain.Task{ID: 1, Title: "Pinned", Status: "pending", AssignedTo: "any", WorkerType: "codex"}

	result := orch.AssignTask(task, state)
	if result != "codex" {
		t.Errorf("expected fallback to honor task.WorkerType='codex', got %q", result)
	}
}

// TestAssignTask_FallbackSkipsBackedOffTypes ensures backed-off types aren't
// chosen even by the fallback path — picking a rate-limited worker just to
// have something to assign would queue the task behind the backoff.
func TestAssignTask_FallbackSkipsBackedOffTypes(t *testing.T) {
	orch := NewTaskOrchestrator(nil, "least_loaded")
	orch.SetKnownTypesProvider(fakeKnownTypes{types: []string{"claude-code", "codex"}})
	orch.SetBackoffChecker(fakeBackoffChecker{excluded: []string{"claude-code"}})

	state := &domain.CollabState{
		DriverID:       "cursor",
		AgentInstances: map[string]*domain.AgentInstance{}, // empty pool
	}
	task := &domain.Task{ID: 1, Title: "Skip backed off", Status: "pending", AssignedTo: "any"}

	result := orch.AssignTask(task, state)
	if result == "claude-code" {
		t.Errorf("fallback must not pick backed-off type; got %q", result)
	}
	if result != "codex" {
		t.Errorf("expected fallback to land on 'codex' (next non-backed-off type), got %q", result)
	}
}

func TestAssignTask_FallbackSkipsQuotaBlockedType(t *testing.T) {
	orch := NewTaskOrchestrator(nil, "least_loaded")
	orch.SetKnownTypesProvider(fakeKnownTypes{types: []string{"claude-code", "codex"}})
	orch.SetBackoffChecker(fakeBackoffChecker{excluded: []string{"claude-code"}})

	state := &domain.CollabState{
		DriverID:       "cursor",
		AgentInstances: map[string]*domain.AgentInstance{},
	}
	task := &domain.Task{ID: 1, Title: "Quota skip", Status: "pending", AssignedTo: "any"}

	result := orch.AssignTask(task, state)
	if result != "codex" {
		t.Errorf("expected codex when claude-code quota-blocked, got %q", result)
	}
}

// TestAssignTask_FallbackReturnsEmptyWhenAllBackedOff confirms that the
// fallback doesn't manufacture an answer when every configured type is in
// backoff. Returning "" here is the right behavior — no point spawning into
// a known-failing pool.
func TestAssignTask_FallbackReturnsEmptyWhenAllBackedOff(t *testing.T) {
	orch := NewTaskOrchestrator(nil, "least_loaded")
	orch.SetKnownTypesProvider(fakeKnownTypes{types: []string{"claude-code", "codex"}})
	orch.SetBackoffChecker(fakeBackoffChecker{excluded: []string{"claude-code", "codex"}})

	state := &domain.CollabState{
		DriverID:       "cursor",
		AgentInstances: map[string]*domain.AgentInstance{},
	}
	task := &domain.Task{ID: 1, Title: "All blocked", Status: "pending", AssignedTo: "any"}

	if result := orch.AssignTask(task, state); result != "" {
		t.Errorf("expected empty result when all known types are backed off, got %q", result)
	}
}

// TestAssignTask_LiveWorkerStillBeatsFallback verifies that the fallback
// only kicks in when no live AgentInstance matches — a healthy idle worker
// must still be picked by the regular strategy path.
func TestAssignTask_LiveWorkerStillBeatsFallback(t *testing.T) {
	orch := NewTaskOrchestrator(nil, "least_loaded")
	orch.SetKnownTypesProvider(fakeKnownTypes{types: []string{"codex"}}) // wrong order on purpose

	state := &domain.CollabState{
		DriverID: "cursor",
		AgentInstances: map[string]*domain.AgentInstance{
			"cursor":        {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "working", MaxTasks: 5},
			"claude-code-1": {InstanceID: "claude-code-1", AgentType: "claude-code", Role: domain.RoleWorker, Status: "idle", MaxTasks: 3},
		},
	}
	task := &domain.Task{ID: 1, Title: "Use live worker", Status: "pending", AssignedTo: "any"}

	result := orch.AssignTask(task, state)
	if result != "claude-code" {
		t.Errorf("live idle worker should beat fallback path, got %q", result)
	}
	inst := state.AgentInstances["claude-code-1"]
	if len(inst.CurrentTasks) != 1 || inst.CurrentTasks[0] != 1 {
		t.Errorf("CurrentTasks should be updated for live-worker assignment, got %v", inst.CurrentTasks)
	}
}

// TestAssignTask_NoFallbackProviderReturnsEmpty preserves the legacy contract
// for callers (e.g. tests) that don't wire a KnownTypesProvider — there is
// no behavioral change in that case.
func TestAssignTask_NoFallbackProviderReturnsEmpty(t *testing.T) {
	orch := NewTaskOrchestrator(nil, "least_loaded")
	state := &domain.CollabState{
		DriverID:       "cursor",
		AgentInstances: map[string]*domain.AgentInstance{},
	}
	task := &domain.Task{ID: 1, Title: "No provider", Status: "pending", AssignedTo: "any"}

	if result := orch.AssignTask(task, state); result != "" {
		t.Errorf("expected legacy empty-result behavior when no fallback provider wired, got %q", result)
	}
}
