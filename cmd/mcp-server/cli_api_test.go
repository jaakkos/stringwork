package main

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
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

	api := newWorkerAPI(svc, logger)
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
