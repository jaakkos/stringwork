package dashboard

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
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
	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"codex":       {InstanceID: "codex", AgentType: "codex", Role: domain.RoleWorker, Status: "idle", LastHeartbeat: now},
		"cursor":      {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "working", LastHeartbeat: now},
		"claude-code": {InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker, Status: "busy", LastHeartbeat: now},
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
	// Workers should be alphabetically ordered
	if snap.Agents[1].Name >= snap.Agents[2].Name {
		t.Errorf("expected workers sorted alphabetically: %q >= %q", snap.Agents[1].Name, snap.Agents[2].Name)
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
	running []string
	killed  []string
}

func (m *mockWorkerController) RestartWorkers() []string {
	m.killed = append([]string(nil), m.running...)
	return m.killed
}

func (m *mockWorkerController) RunningWorkers() []string {
	return m.running
}

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
