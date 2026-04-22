package main

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/jaakkos/stringwork/internal/app"
	"github.com/jaakkos/stringwork/internal/domain"
	"github.com/jaakkos/stringwork/internal/policy"
	"github.com/jaakkos/stringwork/internal/repository"
)

func setupTestAPI(t *testing.T) (*workerAPI, *app.CollabService) {
	t.Helper()
	tmpDir := t.TempDir()
	repo, err := repository.NewStateRepository(tmpDir + "/test.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if c, ok := repo.(interface{ Close() error }); ok {
			c.Close()
		}
	})
	cfg := policy.DefaultConfig()
	pol := policy.New(cfg)
	logger := log.New(&bytes.Buffer{}, "", 0)
	svc := app.NewCollabService(repo, pol, logger)

	// Seed an agent instance so heartbeat/presence can find it
	_ = svc.Run(func(state *domain.CollabState) error {
		state.AgentInstances["codex-1"] = &domain.AgentInstance{
			InstanceID:   "codex-1",
			AgentType:    "codex",
			Role:         domain.RoleWorker,
			Status:       "idle",
			CurrentTasks: []int{},
		}
		state.Presence["cursor"] = &domain.Presence{Agent: "cursor", Status: "working"}
		return nil
	})

	registry := app.NewSessionRegistry()
	api := newWorkerAPI(svc, registry, logger)
	return api, svc
}

func postJSON(api *workerAPI, path string, body interface{}) *httptest.ResponseRecorder {
	data, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux := http.NewServeMux()
	api.RegisterRoutes(mux)
	mux.ServeHTTP(rr, req)
	return rr
}

func getPath(api *workerAPI, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rr := httptest.NewRecorder()
	mux := http.NewServeMux()
	api.RegisterRoutes(mux)
	mux.ServeHTTP(rr, req)
	return rr
}

func TestHeartbeatEndpoint(t *testing.T) {
	api, _ := setupTestAPI(t)

	rr := postJSON(api, "/api/w/heartbeat", map[string]interface{}{
		"agent": "codex-1", "progress": "reading files",
	})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "OK") {
		t.Error("response should contain OK")
	}
}

func TestHeartbeatEndpoint_MissingAgent(t *testing.T) {
	api, _ := setupTestAPI(t)

	rr := postJSON(api, "/api/w/heartbeat", map[string]interface{}{})

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestProgressEndpoint(t *testing.T) {
	api, svc := setupTestAPI(t)

	// Create a task in_progress
	_ = svc.Run(func(state *domain.CollabState) error {
		state.Tasks = append(state.Tasks, domain.Task{
			ID: 1, Title: "Test", Status: "in_progress", AssignedTo: "codex-1",
		})
		state.NextTaskID = 2
		return nil
	})

	rr := postJSON(api, "/api/w/progress", map[string]interface{}{
		"agent": "codex-1", "task_id": 1, "description": "50% done", "percent_complete": 50,
	})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Progress recorded") {
		t.Error("response should contain progress confirmation")
	}
	if !strings.Contains(rr.Body.String(), "50%") {
		t.Error("response should contain percentage")
	}
}

func TestSendEndpoint(t *testing.T) {
	api, _ := setupTestAPI(t)

	rr := postJSON(api, "/api/w/send", map[string]interface{}{
		"from": "codex-1", "to": "cursor", "content": "Found a bug",
	})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "sent to cursor") {
		t.Error("response should confirm message sent")
	}
}

func TestTaskUpdateEndpoint(t *testing.T) {
	api, svc := setupTestAPI(t)

	_ = svc.Run(func(state *domain.CollabState) error {
		state.Tasks = append(state.Tasks, domain.Task{
			ID: 1, Title: "Test", Status: "pending", AssignedTo: "codex-1",
		})
		state.NextTaskID = 2
		return nil
	})

	rr := postJSON(api, "/api/w/task/update", map[string]interface{}{
		"id": 1, "updated_by": "codex-1", "status": "in_progress",
	})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Task #1 updated") {
		t.Error("response should confirm task updated")
	}
}

// fakeSpawner records SpawnForTask calls for assertion in tests.
type fakeSpawner struct {
	calls []spawnCall
}

type spawnCall struct {
	TaskID     int
	AssignedTo string
}

func (f *fakeSpawner) SpawnForTask(taskID int, assignedTo string) {
	f.calls = append(f.calls, spawnCall{TaskID: taskID, AssignedTo: assignedTo})
}

// seedTaskBound seeds a static-pool agent + a task-bound child instance so
// tests can assert reap behavior without depending on the collab package's
// helpers.
func seedTaskBoundCLI(t *testing.T, svc *app.CollabService, agentType string, taskID int) (staticID, taskBoundID string) {
	t.Helper()
	staticID = agentType
	taskBoundID = agentType + "-task-" + strconv.Itoa(taskID)
	_ = svc.Run(func(state *domain.CollabState) error {
		state.RegisteredAgents[agentType] = &domain.RegisteredAgent{Name: agentType}
		state.AgentInstances[staticID] = &domain.AgentInstance{
			InstanceID: staticID, AgentType: agentType, Role: domain.RoleWorker,
			Status: "idle", MaxTasks: 1, CurrentTasks: []int{},
		}
		state.AgentInstances[taskBoundID] = &domain.AgentInstance{
			InstanceID: taskBoundID, AgentType: agentType, Role: domain.RoleWorker,
			Status: "busy", MaxTasks: 1, CurrentTasks: []int{taskID},
		}
		state.Presence[taskBoundID] = &domain.Presence{Agent: taskBoundID, Status: "working"}
		return nil
	})
	return staticID, taskBoundID
}

// TestCLIHandleTaskUpdate_TerminalReapsTaskBound asserts that completing a
// task via the CLI handler deletes the task-bound AgentInstance and its
// Presence row (parity with MCP update_task). C3.
func TestCLIHandleTaskUpdate_TerminalReapsTaskBound(t *testing.T) {
	api, svc := setupTestAPI(t)
	_, tbID := seedTaskBoundCLI(t, svc, "claude-code", 1)
	_ = svc.Run(func(state *domain.CollabState) error {
		state.Tasks = append(state.Tasks, domain.Task{
			ID: 1, Title: "T", Status: "in_progress", AssignedTo: "claude-code",
		})
		state.NextTaskID = 2
		return nil
	})

	rr := postJSON(api, "/api/w/task/update", map[string]interface{}{
		"id": 1, "updated_by": "claude-code", "status": "completed",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	_ = svc.Run(func(state *domain.CollabState) error {
		if _, exists := state.AgentInstances[tbID]; exists {
			t.Errorf("task-bound instance %q should be reaped on completed, but still present", tbID)
		}
		if _, exists := state.Presence[tbID]; exists {
			t.Errorf("task-bound presence %q should be reaped on completed, but still present", tbID)
		}
		return nil
	})
}

// TestCLIHandleTaskUpdate_BlockedIsTerminal asserts that blocked is treated
// as a terminal state by the CLI handler (parity with MCP). The task-bound
// row should be reaped just like for completed/cancelled. C3.
func TestCLIHandleTaskUpdate_BlockedIsTerminal(t *testing.T) {
	api, svc := setupTestAPI(t)
	_, tbID := seedTaskBoundCLI(t, svc, "claude-code", 1)
	_ = svc.Run(func(state *domain.CollabState) error {
		state.Tasks = append(state.Tasks, domain.Task{
			ID: 1, Title: "T", Status: "in_progress", AssignedTo: "claude-code",
		})
		state.NextTaskID = 2
		return nil
	})

	rr := postJSON(api, "/api/w/task/update", map[string]interface{}{
		"id": 1, "updated_by": "claude-code", "status": "blocked",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	_ = svc.Run(func(state *domain.CollabState) error {
		if _, exists := state.AgentInstances[tbID]; exists {
			t.Errorf("task-bound instance %q should be reaped on blocked, but still present", tbID)
		}
		// Also assert CurrentTasks is cleaned on the static row when
		// blocked transitions out of in_progress.
		if inst := state.AgentInstances["claude-code"]; inst != nil {
			for _, id := range inst.CurrentTasks {
				if id == 1 {
					t.Errorf("static instance still lists task 1 in CurrentTasks after blocked transition")
				}
			}
		}
		return nil
	})
}

// TestCLIHandleTaskUpdate_SpawnsWorkerOnReassign asserts that reassigning a
// task to a new worker via the CLI handler triggers spawner.SpawnForTask
// for the new assignee (parity with MCP). C3.
func TestCLIHandleTaskUpdate_SpawnsWorkerOnReassign(t *testing.T) {
	api, svc := setupTestAPI(t)
	spawner := &fakeSpawner{}
	api.spawner = spawner

	_ = svc.Run(func(state *domain.CollabState) error {
		state.RegisteredAgents["claude-code"] = &domain.RegisteredAgent{Name: "claude-code"}
		state.AgentInstances["claude-code"] = &domain.AgentInstance{
			InstanceID: "claude-code", AgentType: "claude-code", Role: domain.RoleWorker,
			Status: "idle", MaxTasks: 1, CurrentTasks: []int{},
		}
		state.Tasks = append(state.Tasks, domain.Task{
			ID: 1, Title: "T", Status: "pending", AssignedTo: "codex",
		})
		state.NextTaskID = 2
		return nil
	})

	rr := postJSON(api, "/api/w/task/update", map[string]interface{}{
		"id": 1, "updated_by": "cursor", "assigned_to": "claude-code",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	if len(spawner.calls) != 1 {
		t.Fatalf("expected 1 spawn call, got %d (calls=%+v)", len(spawner.calls), spawner.calls)
	}
	if spawner.calls[0].TaskID != 1 || spawner.calls[0].AssignedTo != "claude-code" {
		t.Errorf("spawn call mismatch: got %+v, want TaskID=1 AssignedTo=claude-code", spawner.calls[0])
	}
}

// TestCLIHandleTaskUpdate_RejectsCompletedToInProgress asserts the CLI
// handler refuses to walk a terminal status back to active. Use replay_task
// to re-open. C3.
func TestCLIHandleTaskUpdate_RejectsCompletedToInProgress(t *testing.T) {
	api, svc := setupTestAPI(t)
	_ = svc.Run(func(state *domain.CollabState) error {
		state.Tasks = append(state.Tasks, domain.Task{
			ID: 1, Title: "T", Status: "completed", AssignedTo: "codex",
		})
		state.NextTaskID = 2
		return nil
	})

	rr := postJSON(api, "/api/w/task/update", map[string]interface{}{
		"id": 1, "updated_by": "codex-1", "status": "in_progress",
	})
	if rr.Code == http.StatusOK {
		t.Fatalf("expected non-200 for completed→in_progress regression, got 200: %s", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "terminal") && !strings.Contains(rr.Body.String(), "completed") {
		t.Errorf("expected error to mention terminal/completed, got: %s", rr.Body.String())
	}

	_ = svc.Run(func(state *domain.CollabState) error {
		if state.Tasks[0].Status != "completed" {
			t.Errorf("task status should be unchanged, got %q", state.Tasks[0].Status)
		}
		return nil
	})
}

// TestCLIHandleTaskUpdate_EnforcesDependencies asserts that the CLI handler
// blocks pending → in_progress when a dependency is incomplete (parity
// with MCP update_task). C3.
func TestCLIHandleTaskUpdate_EnforcesDependencies(t *testing.T) {
	api, svc := setupTestAPI(t)
	_ = svc.Run(func(state *domain.CollabState) error {
		state.Tasks = append(state.Tasks,
			domain.Task{ID: 1, Title: "blocker", Status: "in_progress", AssignedTo: "codex"},
			domain.Task{ID: 2, Title: "dependent", Status: "pending", AssignedTo: "codex", Dependencies: []int{1}},
		)
		state.NextTaskID = 3
		return nil
	})

	rr := postJSON(api, "/api/w/task/update", map[string]interface{}{
		"id": 2, "updated_by": "codex-1", "status": "in_progress",
	})
	if rr.Code == http.StatusOK {
		t.Fatalf("expected non-200 when dependencies incomplete, got 200: %s", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "dependencies") {
		t.Errorf("expected error to mention dependencies, got: %s", rr.Body.String())
	}
}

func TestTaskListEndpoint(t *testing.T) {
	api, svc := setupTestAPI(t)

	_ = svc.Run(func(state *domain.CollabState) error {
		state.Tasks = append(state.Tasks,
			domain.Task{ID: 1, Title: "Task A", Status: "pending", AssignedTo: "codex-1"},
			domain.Task{ID: 2, Title: "Task B", Status: "completed", AssignedTo: "codex-1"},
		)
		state.NextTaskID = 3
		return nil
	})

	rr := getPath(api, "/api/w/task/list?assigned_to=codex-1&status=pending")

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Task A") {
		t.Error("response should contain pending task")
	}
	if strings.Contains(rr.Body.String(), "Task B") {
		t.Error("response should NOT contain completed task when filtering for pending")
	}
}

func TestMessagesEndpoint(t *testing.T) {
	api, svc := setupTestAPI(t)

	_ = svc.Run(func(state *domain.CollabState) error {
		state.Messages = append(state.Messages, domain.Message{
			ID: 1, From: "cursor", To: "codex-1", Content: "Please review",
		})
		state.NextMsgID = 2
		return nil
	})

	rr := getPath(api, "/api/w/messages?for=codex-1")

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Please review") {
		t.Error("response should contain message content")
	}
}

func TestPresenceEndpoint(t *testing.T) {
	api, _ := setupTestAPI(t)

	rr := postJSON(api, "/api/w/presence", map[string]interface{}{
		"agent": "codex-1", "status": "working", "workspace": "/tmp/ws",
	})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Presence updated") {
		t.Error("response should confirm presence update")
	}
}

func TestContextEndpoint(t *testing.T) {
	api, _ := setupTestAPI(t)

	rr := getPath(api, "/api/w/context?for=codex-1")

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Session Context for codex-1") {
		t.Error("response should contain session context header")
	}
}

func TestWorkContextEndpoint(t *testing.T) {
	api, svc := setupTestAPI(t)

	_ = svc.Run(func(state *domain.CollabState) error {
		state.Tasks = append(state.Tasks, domain.Task{
			ID: 5, Title: "Task with context", Status: "pending", ContextID: "ctx-5",
		})
		state.NextTaskID = 6
		state.WorkContexts["ctx-5"] = &domain.WorkContext{
			ID:            "ctx-5",
			TaskID:        5,
			RelevantFiles: []string{"main.go"},
			Constraints:   []string{"read-only"},
		}
		return nil
	})

	rr := getPath(api, "/api/w/work-context?task_id=5")

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "main.go") {
		t.Error("response should contain relevant files")
	}
	if !strings.Contains(body, "CONSTRAINTS") {
		t.Error("response should contain constraints header")
	}
	if !strings.Contains(body, "read-only") {
		t.Error("response should contain constraint text")
	}
}

func TestHeartbeatEndpoint_BannerWithUnread(t *testing.T) {
	api, svc := setupTestAPI(t)

	_ = svc.Run(func(state *domain.CollabState) error {
		state.Messages = append(state.Messages, domain.Message{
			ID: 1, From: "cursor", To: "codex-1", Content: "Check this",
		})
		state.NextMsgID = 2
		return nil
	})

	rr := postJSON(api, "/api/w/heartbeat", map[string]interface{}{
		"agent": "codex-1", "progress": "working",
	})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "unread message") {
		t.Error("response should contain unread message banner")
	}
}
