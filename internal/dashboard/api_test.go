package dashboard

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jaakkos/stringwork/internal/app"
	"github.com/jaakkos/stringwork/internal/domain"
	"github.com/jaakkos/stringwork/internal/policy"
)

type mockRepo struct {
	state *domain.CollabState
	mu    sync.Mutex
}

func (m *mockRepo) Load() (*domain.CollabState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state, nil
}

func (m *mockRepo) Save(s *domain.CollabState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = s
	return nil
}

type mockPolicy struct {
	workspaceRoot string
}

func (p *mockPolicy) MessageRetentionMax() int                   { return 1000 }
func (p *mockPolicy) MessageRetentionDays() int                  { return 30 }
func (p *mockPolicy) PresenceTTLSeconds() int                    { return 300 }
func (p *mockPolicy) StateFile() string                          { return "" }
func (p *mockPolicy) SignalFilePath() string                     { return "" }
func (p *mockPolicy) WorkspaceRoot() string                      { return p.workspaceRoot }
func (p *mockPolicy) SetWorkspaceRoot(root string)               { p.workspaceRoot = root }
func (p *mockPolicy) IsToolEnabled(string) bool                  { return true }
func (p *mockPolicy) ValidatePath(path string) (string, error)   { return path, nil }
func (p *mockPolicy) Orchestration() *policy.OrchestrationConfig { return nil }
func (p *mockPolicy) MaxTaskFailures() int                       { return 3 }
func (p *mockPolicy) AuditEnabled() bool                         { return true }
func (p *mockPolicy) AuditArgsMaxLen() int                       { return 1000 }
func (p *mockPolicy) AuditRetentionDays() int                    { return 7 }
func (p *mockPolicy) PresenceRetentionDays() int                 { return 7 }
func (p *mockPolicy) InstanceRetentionDays() int                 { return 7 }
func (p *mockPolicy) TaskBoundInstanceRetentionHours() int       { return 24 }
func (p *mockPolicy) RespawnGrace() time.Duration                { return 60 * time.Second }
func (p *mockPolicy) SpawnSweepGrace() time.Duration             { return 30 * time.Second }

func newTestService() (*app.CollabService, *mockRepo) {
	repo := &mockRepo{state: domain.NewCollabState()}
	logger := log.New(io.Discard, "", 0)
	svc := app.NewCollabService(repo, &mockPolicy{workspaceRoot: "/tmp"}, logger)
	return svc, repo
}

func TestAPIState_Empty(t *testing.T) {
	svc, _ := newTestService()
	registry := app.NewSessionRegistry()
	h := NewHandler(svc, registry)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/state", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var snap StateSnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatalf("json decode: %v", err)
	}

	if snap.Timestamp == "" {
		t.Error("expected timestamp")
	}
}

func TestAPIState_WithData(t *testing.T) {
	svc, repo := newTestService()
	registry := app.NewSessionRegistry()
	h := NewHandler(svc, registry)

	now := time.Now()
	repo.state.Presence = map[string]*domain.Presence{
		"cursor": {Agent: "cursor", Status: "working", Workspace: "/home/user/proj", LastSeen: now},
	}
	repo.state.Tasks = []domain.Task{
		{
			ID: 1, Title: "Test task", Status: "in_progress", AssignedTo: "claude-code",
			CreatedBy: "cursor", CreatedAt: now, UpdatedAt: now.Add(-3 * time.Minute), Priority: 2,
			ProgressDescription: "Writing tests", ProgressPercent: 60,
			LastProgressAt: now.Add(-30 * time.Second), ExpectedDurationSec: 300,
		},
	}
	repo.state.Messages = []domain.Message{
		{ID: 1, From: "cursor", To: "claude-code", Content: "Hello", Timestamp: now, Read: false},
	}
	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"claude-code": {
			InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker,
			Status: "busy", LastHeartbeat: now,
			Progress: "Writing unit tests", ProgressStep: 3, ProgressTotalSteps: 5,
			ProgressUpdatedAt: now.Add(-10 * time.Second),
		},
	}
	repo.state.SessionNotes = []domain.SessionNote{
		{ID: 1, Author: "cursor", Content: "Use JWT for auth", Category: "decision", Timestamp: now},
	}
	repo.state.FileLocks = map[string]*domain.FileLock{
		"main.go": {Path: "main.go", LockedBy: "claude-code", Reason: "editing", LockedAt: now, ExpiresAt: now.Add(5 * time.Minute)},
	}

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/state", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var snap StateSnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatalf("json decode: %v", err)
	}

	if len(snap.Agents) == 0 {
		t.Error("expected agents")
	}
	if len(snap.Tasks) != 1 {
		t.Errorf("expected 1 task, got %d", len(snap.Tasks))
	}
	task := snap.Tasks[0]
	if task.Title != "Test task" {
		t.Errorf("unexpected title: %s", task.Title)
	}
	if task.ProgressDescription != "Writing tests" {
		t.Errorf("expected progress description, got %q", task.ProgressDescription)
	}
	if task.ProgressPercent != 60 {
		t.Errorf("expected progress 60%%, got %d", task.ProgressPercent)
	}
	if task.LastProgressAge == "" {
		t.Error("expected last_progress_age to be set")
	}
	if task.ExpectedDurationSec != 300 {
		t.Errorf("expected SLA 300s, got %d", task.ExpectedDurationSec)
	}
	if task.SLAStatus != "ok" {
		t.Errorf("expected SLA status ok, got %q", task.SLAStatus)
	}

	if len(snap.Messages) != 1 {
		t.Errorf("expected 1 message, got %d", len(snap.Messages))
	}
	if len(snap.Workers) != 1 {
		t.Errorf("expected 1 worker, got %d", len(snap.Workers))
	}
	worker := snap.Workers[0]
	if worker.Progress != "Writing unit tests" {
		t.Errorf("expected worker progress, got %q", worker.Progress)
	}
	if worker.ProgressStep != 3 || worker.ProgressTotalSteps != 5 {
		t.Errorf("expected step 3/5, got %d/%d", worker.ProgressStep, worker.ProgressTotalSteps)
	}

	if len(snap.SessionNotes) != 1 {
		t.Errorf("expected 1 session note, got %d", len(snap.SessionNotes))
	} else {
		if snap.SessionNotes[0].Content != "Use JWT for auth" {
			t.Errorf("unexpected note content: %s", snap.SessionNotes[0].Content)
		}
		if snap.SessionNotes[0].Category != "decision" {
			t.Errorf("unexpected note category: %s", snap.SessionNotes[0].Category)
		}
	}

	if len(snap.FileLocks) != 1 {
		t.Errorf("expected 1 file lock, got %d", len(snap.FileLocks))
	} else {
		if snap.FileLocks[0].Path != "main.go" {
			t.Errorf("unexpected lock path: %s", snap.FileLocks[0].Path)
		}
		if snap.FileLocks[0].LockedBy != "claude-code" {
			t.Errorf("unexpected lock owner: %s", snap.FileLocks[0].LockedBy)
		}
	}
}

func TestAPIState_AgentOrdering(t *testing.T) {
	svc, repo := newTestService()
	registry := app.NewSessionRegistry()
	h := NewHandler(svc, registry)

	now := time.Now()
	repo.state.Presence = map[string]*domain.Presence{
		"cursor":      {Status: "working", LastSeen: now},
		"claude-code": {Status: "idle", LastSeen: now},
		"codex":       {Status: "online", LastSeen: now},
	}
	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"cursor": {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "working", LastHeartbeat: now},
	}
	repo.state.DriverID = "cursor"

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/state", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	var snap StateSnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatalf("json decode: %v", err)
	}

	if len(snap.Agents) != 3 {
		t.Fatalf("expected 3 agents, got %d", len(snap.Agents))
	}
	// Driver should be first
	if snap.Agents[0].Role != "driver" {
		t.Errorf("expected driver first, got role=%q name=%q", snap.Agents[0].Role, snap.Agents[0].Name)
	}
	// Remaining should be alphabetically ordered
	if snap.Agents[1].Name >= snap.Agents[2].Name {
		t.Errorf("expected agents sorted alphabetically: %q >= %q", snap.Agents[1].Name, snap.Agents[2].Name)
	}
}

func TestAPIState_WorkerOrdering(t *testing.T) {
	svc, repo := newTestService()
	registry := app.NewSessionRegistry()
	h := NewHandler(svc, registry)

	now := time.Now()
	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"codex-1":       {InstanceID: "codex-1", AgentType: "codex", Role: domain.RoleWorker, Status: "idle", LastHeartbeat: now},
		"claude-code-2": {InstanceID: "claude-code-2", AgentType: "claude-code", Role: domain.RoleWorker, Status: "busy", LastHeartbeat: now},
		"claude-code-1": {InstanceID: "claude-code-1", AgentType: "claude-code", Role: domain.RoleWorker, Status: "idle", LastHeartbeat: now},
	}

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/state", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	var snap StateSnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatalf("json decode: %v", err)
	}

	if len(snap.Workers) != 3 {
		t.Fatalf("expected 3 workers, got %d", len(snap.Workers))
	}
	// Should be sorted by instance ID
	for i := 0; i < len(snap.Workers)-1; i++ {
		if snap.Workers[i].InstanceID >= snap.Workers[i+1].InstanceID {
			t.Errorf("workers not sorted: %q >= %q", snap.Workers[i].InstanceID, snap.Workers[i+1].InstanceID)
		}
	}
}

func TestAPIState_SLAOver(t *testing.T) {
	svc, repo := newTestService()
	registry := app.NewSessionRegistry()
	h := NewHandler(svc, registry)

	now := time.Now()
	repo.state.Tasks = []domain.Task{
		{
			ID: 1, Title: "Over SLA", Status: "in_progress", AssignedTo: "claude-code",
			CreatedBy: "cursor", CreatedAt: now.Add(-10 * time.Minute),
			UpdatedAt: now.Add(-10 * time.Minute), Priority: 3,
			ExpectedDurationSec: 300, // 5 min SLA, but running for 10 min
		},
	}
	repo.state.NextTaskID = 2

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/state", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	var snap StateSnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatalf("json decode: %v", err)
	}

	if len(snap.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(snap.Tasks))
	}
	if snap.Tasks[0].SLAStatus != "over" {
		t.Errorf("expected SLA status 'over', got %q", snap.Tasks[0].SLAStatus)
	}
}

func TestAPIReset_ClearsState(t *testing.T) {
	svc, repo := newTestService()
	registry := app.NewSessionRegistry()
	h := NewHandler(svc, registry)

	now := time.Now()
	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "T1", Status: "in_progress", AssignedTo: "claude-code", CreatedBy: "cursor", CreatedAt: now},
		{ID: 2, Title: "T2", Status: "pending", AssignedTo: "cursor", CreatedBy: "claude-code", CreatedAt: now},
	}
	repo.state.Messages = []domain.Message{
		{ID: 1, From: "cursor", To: "claude-code", Content: "hello", Timestamp: now},
	}
	repo.state.Presence = map[string]*domain.Presence{
		"cursor": {Agent: "cursor", Status: "working", LastSeen: now},
	}
	repo.state.NextTaskID = 3
	repo.state.NextMsgID = 2

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("POST", "/api/reset", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if len(repo.state.Tasks) != 0 {
		t.Errorf("expected 0 tasks after reset, got %d", len(repo.state.Tasks))
	}
	if len(repo.state.Messages) != 0 {
		t.Errorf("expected 0 messages after reset, got %d", len(repo.state.Messages))
	}
	if repo.state.NextTaskID != 1 {
		t.Errorf("expected NextTaskID=1, got %d", repo.state.NextTaskID)
	}
	if repo.state.NextMsgID != 1 {
		t.Errorf("expected NextMsgID=1, got %d", repo.state.NextMsgID)
	}
	// Presence should be cleared (keep_agents not set)
	if len(repo.state.Presence) != 0 {
		t.Errorf("expected 0 presence after reset, got %d", len(repo.state.Presence))
	}
}

func TestAPIReset_KeepAgents(t *testing.T) {
	svc, repo := newTestService()
	registry := app.NewSessionRegistry()
	h := NewHandler(svc, registry)

	now := time.Now()
	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "T1", Status: "in_progress", AssignedTo: "claude-code", CreatedBy: "cursor", CreatedAt: now},
	}
	repo.state.Presence = map[string]*domain.Presence{
		"cursor": {Agent: "cursor", Status: "working", LastSeen: now},
	}
	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"claude-code": {InstanceID: "claude-code", AgentType: "claude-code", Status: "busy", CurrentTasks: []int{1}},
	}

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("POST", "/api/reset?keep_agents=true", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Tasks cleared
	if len(repo.state.Tasks) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(repo.state.Tasks))
	}
	// Presence preserved
	if len(repo.state.Presence) != 1 {
		t.Errorf("expected presence kept, got %d", len(repo.state.Presence))
	}
	// Agent instance kept but tasks cleared and status reset
	inst := repo.state.AgentInstances["claude-code"]
	if inst == nil {
		t.Fatal("expected agent instance to be kept")
	}
	if inst.Status != "idle" {
		t.Errorf("expected idle, got %s", inst.Status)
	}
	if len(inst.CurrentTasks) != 0 {
		t.Errorf("expected no current tasks, got %v", inst.CurrentTasks)
	}
}

func TestAPIReset_RequiresPOST(t *testing.T) {
	svc, _ := newTestService()
	registry := app.NewSessionRegistry()
	h := NewHandler(svc, registry)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/reset", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET, got %d", w.Code)
	}
}

type mockWorkerController struct {
	running       []string
	killed        []string
	cancelled     []string
	recentOutput  map[string]string
	cancelReturns map[string]bool
}

func (m *mockWorkerController) RestartWorkers() []string {
	m.killed = append([]string(nil), m.running...)
	return m.killed
}

func (m *mockWorkerController) RunningWorkers() []string {
	return m.running
}

func (m *mockWorkerController) CancelWorker(instanceID string) bool {
	m.cancelled = append(m.cancelled, instanceID)
	if m.cancelReturns != nil {
		return m.cancelReturns[instanceID]
	}
	return true
}

func (m *mockWorkerController) IsWorkerRunning(instanceID string) bool {
	for _, r := range m.running {
		if r == instanceID {
			return true
		}
	}
	return false
}

func (m *mockWorkerController) GetRecentOutput(instanceID string) string {
	if m.recentOutput == nil {
		return ""
	}
	return m.recentOutput[instanceID]
}

type mockGCStatsProvider struct {
	stats app.GCStats
}

func (m *mockGCStatsProvider) GCStats() app.GCStats { return m.stats }

func TestAPIRestartWorkers_WithController(t *testing.T) {
	svc, _ := newTestService()
	registry := app.NewSessionRegistry()
	wc := &mockWorkerController{running: []string{"claude-code-1", "codex-1"}}
	h := NewHandler(svc, registry, WithWorkerController(wc))

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("POST", "/api/restart-workers", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if resp["status"] != "ok" {
		t.Errorf("expected status ok, got: %v", resp["status"])
	}
	killed, ok := resp["killed"].([]any)
	if !ok || len(killed) != 2 {
		t.Errorf("expected 2 killed workers, got: %v", resp["killed"])
	}
}

func TestAPIRestartWorkers_NoController(t *testing.T) {
	svc, _ := newTestService()
	registry := app.NewSessionRegistry()
	h := NewHandler(svc, registry) // no worker controller

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("POST", "/api/restart-workers", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Fatalf("expected 404 when no controller, got %d", w.Code)
	}
}

func TestAPIRestartWorkers_RequiresPOST(t *testing.T) {
	svc, _ := newTestService()
	registry := app.NewSessionRegistry()
	wc := &mockWorkerController{}
	h := NewHandler(svc, registry, WithWorkerController(wc))

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/restart-workers", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET, got %d", w.Code)
	}
}

func TestDashboardPage_ServesHTML(t *testing.T) {
	svc, _ := newTestService()
	registry := app.NewSessionRegistry()
	h := NewHandler(svc, registry)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/dashboard", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	ct := w.Header().Get("Content-Type")
	if ct != "text/html; charset=utf-8" {
		t.Errorf("unexpected content-type: %s", ct)
	}

	body := w.Body.String()
	if len(body) < 100 {
		t.Error("dashboard HTML seems too short")
	}
}

func TestAPISwitchProject_ClearsAndUpdatesWorkspace(t *testing.T) {
	svc, repo := newTestService()
	registry := app.NewSessionRegistry()
	wc := &mockWorkerController{running: []string{"claude-code"}}
	h := NewHandler(svc, registry, WithWorkerController(wc))

	now := time.Now()
	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Old task", Status: "in_progress", AssignedTo: "claude-code", CreatedBy: "cursor", CreatedAt: now},
	}
	repo.state.Messages = []domain.Message{
		{ID: 1, From: "cursor", To: "claude-code", Content: "old msg", Timestamp: now},
	}
	repo.state.Presence = map[string]*domain.Presence{
		"cursor": {Agent: "cursor", Status: "working", Workspace: "/old/project", LastSeen: now},
	}

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("POST", "/api/switch-project?workspace=/new/project", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Tasks and messages cleared
	if len(repo.state.Tasks) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(repo.state.Tasks))
	}
	if len(repo.state.Messages) != 0 {
		t.Errorf("expected 0 messages, got %d", len(repo.state.Messages))
	}

	// Presence workspace updated
	if p := repo.state.Presence["cursor"]; p == nil || p.Workspace != "/new/project" {
		t.Errorf("expected presence workspace to be /new/project, got %v", repo.state.Presence["cursor"])
	}

	// Policy workspace updated
	if ws := svc.Policy().WorkspaceRoot(); ws != "/new/project" {
		t.Errorf("expected policy workspace /new/project, got %s", ws)
	}

	// Workers were restarted
	if len(wc.killed) != 1 || wc.killed[0] != "claude-code" {
		t.Errorf("expected workers to be killed, got %v", wc.killed)
	}
}

func TestAPISwitchProject_RequiresWorkspace(t *testing.T) {
	svc, _ := newTestService()
	registry := app.NewSessionRegistry()
	h := NewHandler(svc, registry)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("POST", "/api/switch-project", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 without workspace, got %d", w.Code)
	}
}

// ── Phase 1d: snapshot-field tests ───────────────────────────────────────

func TestStateSnapshot_TaskBoundFieldsPopulated(t *testing.T) {
	svc, repo := newTestService()
	registry := app.NewSessionRegistry()
	h := NewHandler(svc, registry)

	now := time.Now()
	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"claude-code-task-7": {
			InstanceID:    "claude-code-task-7",
			AgentType:     "claude-code",
			Role:          domain.RoleWorker,
			Status:        "busy",
			CurrentTasks:  []int{7},
			LastHeartbeat: now,
		},
		"claude-code-pool": {
			InstanceID:    "claude-code-pool",
			AgentType:     "claude-code",
			Role:          domain.RoleWorker,
			Status:        "idle",
			LastHeartbeat: now,
		},
	}

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/state", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	var snap StateSnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatalf("json decode: %v", err)
	}

	var taskBound, poolWorker *WorkerSnapshot
	for i := range snap.Workers {
		if snap.Workers[i].InstanceID == "claude-code-task-7" {
			taskBound = &snap.Workers[i]
		}
		if snap.Workers[i].InstanceID == "claude-code-pool" {
			poolWorker = &snap.Workers[i]
		}
	}
	if taskBound == nil {
		t.Fatal("expected task-bound worker in snapshot")
	}
	if !taskBound.IsTaskBound {
		t.Errorf("expected is_task_bound=true for %q", taskBound.InstanceID)
	}
	if taskBound.BoundTaskID != 7 {
		t.Errorf("expected bound_task_id=7, got %d", taskBound.BoundTaskID)
	}
	if poolWorker == nil {
		t.Fatal("expected pool worker in snapshot")
	}
	if poolWorker.IsTaskBound {
		t.Errorf("pool worker should not be task-bound (instance=%q)", poolWorker.InstanceID)
	}
	if poolWorker.BoundTaskID != 0 {
		t.Errorf("pool worker bound_task_id should be 0, got %d", poolWorker.BoundTaskID)
	}
}

func TestStateSnapshot_InDeliveryGraceWithinWindow(t *testing.T) {
	svc, repo := newTestService()
	registry := app.NewSessionRegistry()
	h := NewHandler(svc, registry)

	now := time.Now()
	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"claude-code": {
			InstanceID:    "claude-code",
			AgentType:     "claude-code",
			Role:          domain.RoleWorker,
			Status:        "busy",
			LastHeartbeat: now,
		},
	}
	repo.state.LastSendByAgent = map[string]time.Time{
		"claude-code": now.Add(-30 * time.Second),
	}

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/state", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	var snap StateSnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if len(snap.Workers) != 1 {
		t.Fatalf("expected 1 worker, got %d", len(snap.Workers))
	}
	if !snap.Workers[0].InDeliveryGrace {
		t.Errorf("expected in_delivery_grace=true within 90s window")
	}
	if snap.Workers[0].LastSendAge == "" {
		t.Errorf("expected last_send_age to be populated")
	}
}

func TestStateSnapshot_InDeliveryGraceOutsideWindow(t *testing.T) {
	svc, repo := newTestService()
	registry := app.NewSessionRegistry()
	h := NewHandler(svc, registry)

	now := time.Now()
	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"claude-code": {
			InstanceID:    "claude-code",
			AgentType:     "claude-code",
			Role:          domain.RoleWorker,
			Status:        "busy",
			LastHeartbeat: now,
		},
	}
	repo.state.LastSendByAgent = map[string]time.Time{
		"claude-code": now.Add(-5 * time.Minute),
	}

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/state", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	var snap StateSnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if len(snap.Workers) != 1 {
		t.Fatalf("expected 1 worker, got %d", len(snap.Workers))
	}
	if snap.Workers[0].InDeliveryGrace {
		t.Errorf("expected in_delivery_grace=false outside 90s window")
	}
	if snap.Workers[0].LastSendAge == "" {
		t.Errorf("expected last_send_age to still be populated")
	}
}

func TestStateSnapshot_RecoveredMessageDetected(t *testing.T) {
	svc, repo := newTestService()
	registry := app.NewSessionRegistry()
	h := NewHandler(svc, registry)

	now := time.Now()
	repo.state.Messages = []domain.Message{
		{ID: 1, From: "cursor", To: "claude-code", Content: "hello", Timestamp: now},
		{ID: 2, From: "claude-code", To: "cursor", Content: recoveredMessagePrefix + "\n\n```\nrecovered tail\n```", Timestamp: now},
	}

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/state", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	var snap StateSnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if len(snap.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(snap.Messages))
	}
	var plain, recovered *MessageSnapshot
	for i := range snap.Messages {
		if snap.Messages[i].ID == 1 {
			plain = &snap.Messages[i]
		}
		if snap.Messages[i].ID == 2 {
			recovered = &snap.Messages[i]
		}
	}
	if plain == nil || recovered == nil {
		t.Fatalf("expected both messages present")
	}
	if plain.Recovered {
		t.Errorf("plain message should not be marked recovered")
	}
	if !recovered.Recovered {
		t.Errorf("synthetic recovery message should be marked recovered=true")
	}
}

func TestStateSnapshot_GCBlockExposed(t *testing.T) {
	svc, _ := newTestService()
	registry := app.NewSessionRegistry()
	gc := &mockGCStatsProvider{
		stats: app.GCStats{
			LastRun:              time.Now().Add(-5 * time.Minute),
			PresencePrunedTotal:  12,
			InstancesPrunedTotal: 7,
		},
	}
	h := NewHandler(svc, registry, WithGCStatsProvider(gc))

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/state", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	var snap StateSnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if snap.GC == nil {
		t.Fatal("expected GC block in snapshot")
	}
	if snap.GC.PresencePrunedTotal != 12 {
		t.Errorf("expected presence_pruned_total=12, got %d", snap.GC.PresencePrunedTotal)
	}
	if snap.GC.InstancesPrunedTotal != 7 {
		t.Errorf("expected instances_pruned_total=7, got %d", snap.GC.InstancesPrunedTotal)
	}
	if snap.GC.LastRun == "" {
		t.Errorf("expected last_run to be populated")
	}
	if snap.GC.PresenceRetentionDays != 7 {
		t.Errorf("expected presence_retention_days=7 from mockPolicy, got %d", snap.GC.PresenceRetentionDays)
	}
	if snap.GC.InstanceRetentionDays != 7 {
		t.Errorf("expected instance_retention_days=7 from mockPolicy, got %d", snap.GC.InstanceRetentionDays)
	}
	if snap.GC.TaskBoundInstanceRetentionHours != 24 {
		t.Errorf("expected task_bound_instance_retention_hours=24 from mockPolicy, got %d", snap.GC.TaskBoundInstanceRetentionHours)
	}
}

func TestStateSnapshot_GCBlockOmittedWithoutProvider(t *testing.T) {
	svc, _ := newTestService()
	registry := app.NewSessionRegistry()
	h := NewHandler(svc, registry)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/state", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	var snap StateSnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if snap.GC != nil {
		t.Errorf("expected GC block to be omitted without provider")
	}
}

// ── Phase 1d: endpoint tests ──────────────────────────────────────────────

func TestAPICancelAgent_HappyPathReturnsRecoveredMessage(t *testing.T) {
	svc, repo := newTestService()
	registry := app.NewSessionRegistry()
	wc := &mockWorkerController{
		running: []string{"claude-code"},
		recentOutput: map[string]string{
			"claude-code": "tail of buffered worker output\nlast line of analysis",
		},
	}
	h := NewHandler(svc, registry, WithWorkerController(wc))

	now := time.Now()
	repo.state.DriverID = "cursor"
	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"cursor": {
			InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver,
			Status: "working", LastHeartbeat: now,
		},
		"claude-code": {
			InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker,
			Status: "busy", CurrentTasks: []int{1}, LastHeartbeat: now,
		},
	}
	repo.state.Tasks = []domain.Task{
		{
			ID: 1, Title: "Run review", Status: "in_progress",
			AssignedTo: "claude-code", CreatedBy: "cursor",
			CreatedAt: now.Add(-2 * time.Minute), UpdatedAt: now.Add(-30 * time.Second),
		},
	}
	repo.state.NextTaskID = 2
	repo.state.NextMsgID = 1

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	body := `{"agent":"claude-code","cancelled_by":"cursor","reason":"stuck"}`
	req := httptest.NewRequest("POST", "/api/cancel-agent", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp CancelAgentResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("expected status ok, got %q", resp.Status)
	}
	if len(resp.CancelledTasks) != 1 || resp.CancelledTasks[0] != 1 {
		t.Errorf("expected cancelled_tasks=[1], got %v", resp.CancelledTasks)
	}
	if !resp.ProcessKilled {
		t.Errorf("expected process_killed=true (mock returns true)")
	}
	if resp.RecoveredFrom != "claude-code" {
		t.Errorf("expected recovered_from=claude-code, got %q", resp.RecoveredFrom)
	}

	if len(wc.cancelled) != 1 || wc.cancelled[0] != "claude-code" {
		t.Errorf("expected CancelWorker invoked for claude-code, got %v", wc.cancelled)
	}

	var stopFound, recoveredFound bool
	for _, m := range repo.state.Messages {
		if m.From == "system" && m.To == "claude-code" {
			stopFound = true
		}
		if m.From == "claude-code" && strings.HasPrefix(m.Content, recoveredMessagePrefix) {
			recoveredFound = true
		}
	}
	if !stopFound {
		t.Errorf("expected synthetic STOP message from system")
	}
	if !recoveredFound {
		t.Errorf("expected synthetic recovery message from claude-code")
	}
	if repo.state.Tasks[0].Status != "cancelled" {
		t.Errorf("expected task status cancelled, got %q", repo.state.Tasks[0].Status)
	}
}

func TestAPICancelAgent_RequiresPOST(t *testing.T) {
	svc, _ := newTestService()
	registry := app.NewSessionRegistry()
	h := NewHandler(svc, registry)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/cancel-agent", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET, got %d", w.Code)
	}
}

func TestAPICancelAgent_RejectsMissingFields(t *testing.T) {
	svc, _ := newTestService()
	registry := app.NewSessionRegistry()
	h := NewHandler(svc, registry)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("POST", "/api/cancel-agent", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing fields, got %d", w.Code)
	}
}

func TestAPIPrune_DryRunReportsCountsNoMutation(t *testing.T) {
	svc, repo := newTestService()
	registry := app.NewSessionRegistry()
	h := NewHandler(svc, registry)

	now := time.Now()
	old := now.Add(-30 * 24 * time.Hour)
	repo.state.Presence = map[string]*domain.Presence{
		"stale": {Agent: "stale", Status: "offline", LastSeen: old},
		"fresh": {Agent: "fresh", Status: "working", LastSeen: now},
	}
	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"claude-code-task-1": {
			InstanceID: "claude-code-task-1", AgentType: "claude-code",
			Role: domain.RoleWorker, Status: "offline", LastHeartbeat: old,
		},
		"claude-code-pool": {
			InstanceID: "claude-code-pool", AgentType: "claude-code",
			Role: domain.RoleWorker, Status: "idle", LastHeartbeat: now,
		},
	}
	presenceBefore := len(repo.state.Presence)
	instancesBefore := len(repo.state.AgentInstances)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	body := `{"dry_run":true}`
	req := httptest.NewRequest("POST", "/api/prune", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp PruneResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if !resp.DryRun {
		t.Errorf("expected dry_run=true in response")
	}
	if resp.PresenceRetentionDays != 7 || resp.InstanceRetentionDays != 7 {
		t.Errorf("expected default retention 7d/7d, got p=%d i=%d", resp.PresenceRetentionDays, resp.InstanceRetentionDays)
	}
	if resp.TaskBoundInstanceRetentionHours != 24 {
		t.Errorf("expected default task-bound retention 24h, got %d", resp.TaskBoundInstanceRetentionHours)
	}
	if resp.PresencePruned <= 0 {
		t.Errorf("expected stale presence to be counted, got %d", resp.PresencePruned)
	}
	if resp.InstancesPruned <= 0 {
		t.Errorf("expected stale instance to be counted, got %d", resp.InstancesPruned)
	}

	if len(repo.state.Presence) != presenceBefore {
		t.Errorf("dry-run mutated presence: before=%d after=%d", presenceBefore, len(repo.state.Presence))
	}
	if len(repo.state.AgentInstances) != instancesBefore {
		t.Errorf("dry-run mutated instances: before=%d after=%d", instancesBefore, len(repo.state.AgentInstances))
	}
}

func TestAPIPrune_CommitMutatesState(t *testing.T) {
	svc, repo := newTestService()
	registry := app.NewSessionRegistry()
	h := NewHandler(svc, registry)

	now := time.Now()
	old := now.Add(-30 * 24 * time.Hour)
	repo.state.Presence = map[string]*domain.Presence{
		"stale": {Agent: "stale", Status: "offline", LastSeen: old},
	}
	presenceBefore := len(repo.state.Presence)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	body := `{"presence":true,"instances":false}`
	req := httptest.NewRequest("POST", "/api/prune", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(repo.state.Presence) >= presenceBefore {
		t.Errorf("expected presence pruned: before=%d after=%d", presenceBefore, len(repo.state.Presence))
	}
}

func TestAPIPrune_RejectsAllDisabled(t *testing.T) {
	svc, _ := newTestService()
	registry := app.NewSessionRegistry()
	h := NewHandler(svc, registry)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	body := `{"presence":false,"instances":false}`
	req := httptest.NewRequest("POST", "/api/prune", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when both targets disabled, got %d", w.Code)
	}
}

func TestAPIPoolStatus_MatchesCLISummary(t *testing.T) {
	svc, repo := newTestService()
	registry := app.NewSessionRegistry()
	h := NewHandler(svc, registry)

	now := time.Now()
	repo.state.DriverID = "cursor"
	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"cursor": {
			InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver,
			Status: "working", LastHeartbeat: now,
		},
		"claude-code-1": {
			InstanceID: "claude-code-1", AgentType: "claude-code", Role: domain.RoleWorker,
			Status: "idle", LastHeartbeat: now.Add(-1 * time.Minute),
		},
		"claude-code-task-7": {
			InstanceID: "claude-code-task-7", AgentType: "claude-code", Role: domain.RoleWorker,
			Status: "offline", LastHeartbeat: now.Add(-2 * time.Hour),
		},
		"codex-1": {
			InstanceID: "codex-1", AgentType: "codex", Role: domain.RoleWorker,
			Status: "busy", CurrentTasks: []int{2}, LastHeartbeat: now,
		},
	}
	repo.state.Tasks = []domain.Task{
		{
			ID: 2, Title: "Code review", Status: "in_progress",
			AssignedTo: "codex-1", CreatedBy: "cursor",
			CreatedAt: now.Add(-5 * time.Minute), UpdatedAt: now.Add(-1 * time.Minute),
		},
	}

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/pool-status", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp PoolStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if resp.Driver != "cursor" {
		t.Errorf("expected driver=cursor, got %q", resp.Driver)
	}
	if resp.TotalInstances != 4 {
		t.Errorf("expected total_instances=4, got %d", resp.TotalInstances)
	}
	// Active = idle + busy worker rows (cursor is driver, excluded).
	if resp.ActiveInstances != 2 {
		t.Errorf("expected active_instances=2, got %d", resp.ActiveInstances)
	}
	if resp.OfflineInstances != 1 {
		t.Errorf("expected offline_instances=1, got %d", resp.OfflineInstances)
	}
	if resp.InFlightTaskCount != 1 || len(resp.InFlightTasks) != 1 {
		t.Errorf("expected 1 in-flight task, got count=%d list=%v", resp.InFlightTaskCount, resp.InFlightTasks)
	}
	if resp.OldestOffline == nil || resp.OldestOffline.InstanceID != "claude-code-task-7" {
		t.Errorf("expected oldest_offline=claude-code-task-7, got %+v", resp.OldestOffline)
	}
	if resp.OldestOffline != nil && !resp.OldestOffline.IsTaskBound {
		t.Errorf("expected oldest_offline.is_task_bound=true")
	}
	if resp.WorkerStatusByType["claude-code"] != 1 {
		t.Errorf("expected 1 active claude-code, got %d", resp.WorkerStatusByType["claude-code"])
	}
	if resp.WorkerOfflineByType["claude-code"] != 1 {
		t.Errorf("expected 1 offline claude-code, got %d", resp.WorkerOfflineByType["claude-code"])
	}
}

func TestAPIPoolStatus_RequiresGET(t *testing.T) {
	svc, _ := newTestService()
	registry := app.NewSessionRegistry()
	h := NewHandler(svc, registry)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("POST", "/api/pool-status", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for POST, got %d", w.Code)
	}
}

func TestAPISendMessage_RejectsNonDriverFrom(t *testing.T) {
	svc, repo := newTestService()
	registry := app.NewSessionRegistry()
	h := NewHandler(svc, registry)

	now := time.Now()
	repo.state.DriverID = "cursor"
	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"cursor": {
			InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver,
			Status: "working", LastHeartbeat: now,
		},
		"claude-code": {
			InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker,
			Status: "idle", LastHeartbeat: now,
		},
	}
	repo.state.NextMsgID = 1

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	body := `{"from":"claude-code","to":"cursor","content":"impersonation attempt"}`
	req := httptest.NewRequest("POST", "/api/send-message", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-driver from, got %d: %s", w.Code, w.Body.String())
	}
	if len(repo.state.Messages) != 0 {
		t.Errorf("expected no message persisted, got %d", len(repo.state.Messages))
	}
}

func TestAPISendMessage_DriverHappyPath(t *testing.T) {
	svc, repo := newTestService()
	registry := app.NewSessionRegistry()
	h := NewHandler(svc, registry)

	now := time.Now()
	repo.state.DriverID = "cursor"
	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"cursor": {
			InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver,
			Status: "working", LastHeartbeat: now,
		},
		"claude-code": {
			InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker,
			Status: "idle", LastHeartbeat: now,
		},
	}
	repo.state.NextMsgID = 1

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	body := `{"from":"cursor","to":"claude-code","content":"please run the tests"}`
	req := httptest.NewRequest("POST", "/api/send-message", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp SendMessageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if resp.Status != "ok" || resp.ID != 1 {
		t.Errorf("unexpected response: %+v", resp)
	}
	if len(repo.state.Messages) != 1 {
		t.Fatalf("expected 1 message persisted, got %d", len(repo.state.Messages))
	}
	if repo.state.Messages[0].From != "cursor" || repo.state.Messages[0].To != "claude-code" {
		t.Errorf("unexpected persisted message: %+v", repo.state.Messages[0])
	}
	if _, ok := repo.state.LastSendByAgent["cursor"]; !ok {
		t.Errorf("expected LastSendByAgent[cursor] to be updated")
	}
}

func TestAPISendMessage_RejectsEmptyFields(t *testing.T) {
	svc, _ := newTestService()
	registry := app.NewSessionRegistry()
	h := NewHandler(svc, registry)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	body := `{"from":"cursor","to":"","content":"hello"}`
	req := httptest.NewRequest("POST", "/api/send-message", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing field, got %d", w.Code)
	}
}
