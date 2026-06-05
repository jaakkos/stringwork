package collab

import (
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/jaakkos/stringwork/internal/domain"
	"github.com/jaakkos/stringwork/internal/policy"
)

func TestWorkerStatus_Basic(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	result, err := callTool(t, srv, "worker_status", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "Worker Status") {
		t.Errorf("expected 'Worker Status' header, got: %s", text)
	}
	// Should list workers
	if !strings.Contains(text, "claude-code") {
		t.Errorf("expected claude-code in output: %s", text)
	}
	if !strings.Contains(text, "codex") {
		t.Errorf("expected codex in output: %s", text)
	}
}

func TestWorkerStatus_DriverExcluded(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	result, err := callTool(t, srv, "worker_status", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)

	// Driver should be shown as "Driver: cursor" but NOT in the instances list
	if !strings.Contains(text, "Driver: cursor") {
		t.Errorf("expected 'Driver: cursor' header: %s", text)
	}

	// The instances section should not have cursor as a worker entry
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- cursor") && strings.Contains(trimmed, "[cursor]") {
			t.Errorf("cursor should not appear as a worker instance: %s", line)
		}
	}
}

func TestWorkerStatus_WithHeartbeat(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	// Pre-seed instances with a recent heartbeat
	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"cursor":      {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "idle"},
		"claude-code": {InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker, Status: "idle", LastHeartbeat: time.Now(), CurrentTasks: []int{}},
	}

	srv := testServer(svc, logger)

	result, err := callTool(t, srv, "worker_status", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)

	// Should show a time-based "ago" instead of "never"
	if strings.Contains(text, "heartbeat: never") {
		t.Errorf("expected heartbeat time for claude-code, got 'never': %s", text)
	}
	if !strings.Contains(text, "ago") {
		t.Errorf("expected 'ago' in heartbeat display: %s", text)
	}
}

func TestWorkerStatus_WithCurrentTasks(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"cursor":      {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "idle"},
		"claude-code": {InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker, Status: "working", CurrentTasks: []int{1, 3}, LastHeartbeat: time.Now()},
	}

	srv := testServer(svc, logger)

	result, err := callTool(t, srv, "worker_status", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)

	if !strings.Contains(text, "tasks:") {
		t.Errorf("expected current tasks in output: %s", text)
	}
	if !strings.Contains(text, "working") {
		t.Errorf("expected 'working' status in output: %s", text)
	}
}

func TestFormatModelTiers_MultiProvider(t *testing.T) {
	mtp := stubModelTierProvider{
		tiers: map[string]map[string]string{
			"fast": {
				"claude-code": "haiku",
				"codex":       "o4-mini",
				"gemini":      "gemini-2.5-flash",
			},
		},
		workers: []string{"claude-code", "codex", "gemini"},
	}
	got := formatModelTiers(mtp)
	for _, want := range []string{"claude-code=haiku", "codex=o4-mini", "gemini=gemini-2.5-flash", "fast:"} {
		if !strings.Contains(got, want) {
			t.Errorf("formatModelTiers missing %q in:\n%s", want, got)
		}
	}
}

type stubModelTierProvider struct {
	tiers   map[string]map[string]string
	workers []string
}

func (s stubModelTierProvider) ModelTierMap() map[string]map[string]string { return s.tiers }
func (s stubModelTierProvider) WorkerAgentTypes() []string                 { return s.workers }

func TestWorkerStatus_NoWorkers(t *testing.T) {
	// Custom mock policy with no workers
	repo := newMockRepository()
	noWorkerPolicy := &mockPolicyNoWorkers{}
	logger := log.New(io.Discard, "", 0)
	svc := newTestServiceWith(repo, noWorkerPolicy, logger)
	srv := testServer(svc, logger)

	result, err := callTool(t, srv, "worker_status", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)

	// Should still have header and Instances label but no worker entries
	if !strings.Contains(text, "Worker Status") {
		t.Errorf("expected header: %s", text)
	}
}

// mockPolicyNoWorkers returns orchestration config with driver only, no workers.
type mockPolicyNoWorkers struct {
	mockPolicy
}

func (m *mockPolicyNoWorkers) Orchestration() *policy.OrchestrationConfig {
	return &policy.OrchestrationConfig{
		Driver:  "cursor",
		Workers: []policy.WorkerConfig{},
	}
}

// fakeProcessProvider satisfies ProcessInfoProvider for tests that need to
// distinguish "pool slot with a tracked process" from "pool slot is dormant".
type fakeProcessProvider struct {
	procs map[string]ProcessInfoSnapshot
}

func (f *fakeProcessProvider) GetProcessInfo() map[string]ProcessInfoSnapshot {
	if f.procs == nil {
		return map[string]ProcessInfoSnapshot{}
	}
	return f.procs
}

func (f *fakeProcessProvider) GetRecentOutput(instanceID string) string { return "" }

func (f *fakeProcessProvider) IsWorkerRunning(instanceID string) bool {
	_, ok := f.procs[instanceID]
	return ok
}

// TestLivenessVerdict_IdlePoolSlot covers the canonical dormant-pool case
// that drove the regfin-review skill to fall back to native subagents:
// a bootstrap-only or watchdog-flipped pool row with no tracked process
// must read as IDLE so the driver knows SpawnForTask will wake it on
// demand, not as UNRESPONSIVE / UNKNOWN (which read as "dead pool").
func TestLivenessVerdict_IdlePoolSlot(t *testing.T) {
	now := time.Now()
	inst := &domain.AgentInstance{
		InstanceID:    "claude-code-1",
		AgentType:     "claude-code",
		Role:          domain.RoleWorker,
		Status:        "offline",
		CurrentTasks:  []int{},
		LastHeartbeat: now.Add(-24 * time.Hour), // stale: would normally trigger UNRESPONSIVE
	}
	got := livenessVerdict("claude-code-1", inst, now, nil)
	if !strings.Contains(got, "[IDLE — wake on demand]") {
		t.Errorf("expected IDLE verdict for dormant pool slot with stale heartbeat, got: %s", got)
	}
}

func TestLivenessVerdict_IdlePoolSlot_NeverHeartbeated(t *testing.T) {
	now := time.Now()
	inst := &domain.AgentInstance{
		InstanceID:   "codex-2",
		AgentType:    "codex",
		Role:         domain.RoleWorker,
		Status:       "offline",
		CurrentTasks: []int{},
		// LastHeartbeat zero — bootstrap row that never spawned.
	}
	got := livenessVerdict("codex-2", inst, now, nil)
	if !strings.Contains(got, "[IDLE — wake on demand]") {
		t.Errorf("expected IDLE verdict for never-spawned pool slot, got: %s", got)
	}
}

func TestLivenessVerdict_TaskBoundOfflineStaysUnresponsive(t *testing.T) {
	now := time.Now()
	inst := &domain.AgentInstance{
		InstanceID:    "claude-code-task-7",
		AgentType:     "claude-code",
		Role:          domain.RoleWorker,
		Status:        "offline",
		CurrentTasks:  []int{},
		LastHeartbeat: now.Add(-10 * time.Minute),
	}
	got := livenessVerdict("claude-code-task-7", inst, now, nil)
	if strings.Contains(got, "IDLE") {
		t.Errorf("task-bound offline row must not show IDLE (died mid-task is not dormant): %s", got)
	}
	if !strings.Contains(got, "UNRESPONSIVE") {
		t.Errorf("expected UNRESPONSIVE for task-bound offline row with stale heartbeat: %s", got)
	}
}

func TestLivenessVerdict_OfflineWithCurrentTasksIsNotIdle(t *testing.T) {
	now := time.Now()
	inst := &domain.AgentInstance{
		InstanceID:    "claude-code-1",
		AgentType:     "claude-code",
		Role:          domain.RoleWorker,
		Status:        "offline",
		CurrentTasks:  []int{5}, // half-broken: offline but owns a task
		LastHeartbeat: now.Add(-10 * time.Minute),
	}
	got := livenessVerdict("claude-code-1", inst, now, nil)
	if strings.Contains(got, "IDLE") {
		t.Errorf("pool slot with CurrentTasks must not show IDLE (half-broken state, surface to driver): %s", got)
	}
}

func TestLivenessVerdict_OfflineWithTrackedProcessIsNotIdle(t *testing.T) {
	now := time.Now()
	inst := &domain.AgentInstance{
		InstanceID:    "claude-code-1",
		AgentType:     "claude-code",
		Role:          domain.RoleWorker,
		Status:        "offline",
		CurrentTasks:  []int{},
		LastHeartbeat: now.Add(-10 * time.Minute),
	}
	// Process is still tracked — mid-spawn / mid-shutdown, not dormant.
	pip := &fakeProcessProvider{procs: map[string]ProcessInfoSnapshot{
		"claude-code-1": {StartedAt: now.Add(-30 * time.Second), LastOutputAt: now.Add(-5 * time.Second)},
	}}
	got := livenessVerdict("claude-code-1", inst, now, pip)
	if strings.Contains(got, "IDLE") {
		t.Errorf("pool slot whose process is tracked must not show IDLE: %s", got)
	}
}

func TestLivenessVerdict_IdleWhenSiblingTaskBoundProcessRuns(t *testing.T) {
	now := time.Now()
	inst := &domain.AgentInstance{
		InstanceID:    "claude-code-1",
		AgentType:     "claude-code",
		Role:          domain.RoleWorker,
		Status:        "offline",
		CurrentTasks:  []int{},
		LastHeartbeat: now.Add(-24 * time.Hour),
	}
	// A task-bound worker for the same TYPE is running, but this specific
	// pool slot (claude-code-1) is not — it should still read IDLE so the
	// driver knows the slot is available for the next SpawnForTask.
	pip := &fakeProcessProvider{procs: map[string]ProcessInfoSnapshot{
		"claude-code-task-28": {StartedAt: now.Add(-30 * time.Second), LastOutputAt: now.Add(-5 * time.Second)},
	}}
	got := livenessVerdict("claude-code-1", inst, now, pip)
	if !strings.Contains(got, "[IDLE — wake on demand]") {
		t.Errorf("pool slot with no own process should be IDLE even when a sibling task-bound process runs: %s", got)
	}
}

// TestWorkerStatus_RendersIdleVerdict end-to-end verifies that
// worker_status surfaces the IDLE verdict so drivers reading the tool
// output see "wake on demand" rather than "UNRESPONSIVE — heartbeat
// 24h ago" for a dormant pool slot.
func TestWorkerStatus_RendersIdleVerdict(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"cursor":        {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "idle"},
		"claude-code-1": {InstanceID: "claude-code-1", AgentType: "claude-code", Role: domain.RoleWorker, Status: "offline", CurrentTasks: []int{}, LastHeartbeat: time.Now().Add(-24 * time.Hour)},
	}

	srv := testServer(svc, logger)
	result, err := callTool(t, srv, "worker_status", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)

	if !strings.Contains(text, "[IDLE — wake on demand]") {
		t.Errorf("expected IDLE verdict in worker_status output, got: %s", text)
	}
	if strings.Contains(text, "[UNRESPONSIVE") {
		t.Errorf("dormant pool slot must not render UNRESPONSIVE (regression: regfin-review fallback bug): %s", text)
	}
}
