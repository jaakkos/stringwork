package collab

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/server"

	"github.com/jaakkos/stringwork/internal/app"
	"github.com/jaakkos/stringwork/internal/domain"
	"github.com/jaakkos/stringwork/internal/policy"
)

// mockRepository implements app.StateRepository for tests. State is kept in memory.
//
// afterSave is an optional hook invoked after each Save (with the lock held).
// Tests use it to simulate concurrent state mutations between successive
// CollabService.Run invocations — e.g. the post-lock revalidation window
// exploited by TestSpawnSideEffects_RevalidatesAfterStateLock.
type mockRepository struct {
	state     *domain.CollabState
	mu        sync.Mutex
	afterSave func(state *domain.CollabState)
}

func newMockRepository() *mockRepository {
	return &mockRepository{state: domain.NewCollabState()}
}

func (m *mockRepository) Load() (*domain.CollabState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state, nil
}

func (m *mockRepository) Save(state *domain.CollabState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = state
	if m.afterSave != nil {
		m.afterSave(state)
	}
	return nil
}

// mockPolicy implements app.Policy for tests.
type mockPolicy struct {
	workspaceRoot string
}

func newMockPolicy() *mockPolicy {
	dir, _ := os.MkdirTemp("", "collab-test-*")
	return &mockPolicy{workspaceRoot: dir}
}

func (m *mockPolicy) MessageRetentionMax() int             { return 1000 }
func (m *mockPolicy) MessageRetentionDays() int            { return 30 }
func (m *mockPolicy) PresenceTTLSeconds() int              { return 300 }
func (m *mockPolicy) StateFile() string                    { return "" }
func (m *mockPolicy) SignalFilePath() string               { return "" }
func (m *mockPolicy) WorkspaceRoot() string                { return m.workspaceRoot }
func (m *mockPolicy) SetWorkspaceRoot(root string)         { m.workspaceRoot = root }
func (m *mockPolicy) IsToolEnabled(name string) bool       { return true }
func (m *mockPolicy) MaxTaskFailures() int                 { return 3 }
func (m *mockPolicy) AuditEnabled() bool                   { return true }
func (m *mockPolicy) AuditArgsMaxLen() int                 { return 1000 }
func (m *mockPolicy) AuditRetentionDays() int              { return 7 }
func (m *mockPolicy) PresenceRetentionDays() int           { return 7 }
func (m *mockPolicy) InstanceRetentionDays() int           { return 7 }
func (m *mockPolicy) TaskBoundInstanceRetentionHours() int { return 24 }
func (m *mockPolicy) Orchestration() *policy.OrchestrationConfig {
	return &policy.OrchestrationConfig{
		Driver: "cursor",
		Workers: []policy.WorkerConfig{
			{Type: "claude-code", Instances: 1},
			{Type: "codex", Instances: 1},
		},
	}
}

func (m *mockPolicy) ValidatePath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	root := m.workspaceRoot
	if root == "" {
		root, _ = os.Getwd()
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("path %s is outside workspace", path)
	}
	return abs, nil
}

// newTestService returns a CollabService and mock repository for testing.
func newTestService() (*app.CollabService, *mockRepository) {
	repo := newMockRepository()
	pol := newMockPolicy()
	logger := log.New(io.Discard, "", 0)
	svc := app.NewCollabService(repo, pol, logger)
	return svc, repo
}

// newTestServiceWith returns a CollabService with a custom repository and policy.
func newTestServiceWith(repo *mockRepository, pol app.Policy, logger *log.Logger) *app.CollabService {
	return app.NewCollabService(repo, pol, logger)
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name  string
		input string
		max   int
		want  string
	}{
		{"short string", "hello", 10, "hello"},
		{"exact length", "hello", 5, "hello"},
		{"needs truncation", "hello world", 5, "hello..."},
		{"unicode safe", "こんにちは世界", 3, "こんに..."},
		{"emoji safe", "hello 👋 world", 8, "hello 👋 ..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := app.Truncate(tt.input, tt.max)
			if got != tt.want {
				t.Errorf("Truncate(%q, %d) = %q, want %q", tt.input, tt.max, got, tt.want)
			}
		})
	}
}

func TestPruneMessages(t *testing.T) {
	state := domain.NewCollabState()
	now := time.Now()
	for i := 1; i <= 10; i++ {
		state.Messages = append(state.Messages, domain.Message{
			ID: i, From: "cursor", To: "claude-code", Content: "test",
			Timestamp: now.Add(-time.Duration(i) * time.Hour),
		})
	}
	state.NextMsgID = 11

	pruned := app.PruneMessages(state, 5, 0)
	if pruned != 5 {
		t.Errorf("expected 5 pruned, got %d", pruned)
	}
	if len(state.Messages) != 5 {
		t.Errorf("expected 5 messages remaining, got %d", len(state.Messages))
	}

	state = domain.NewCollabState()
	for i := 1; i <= 5; i++ {
		state.Messages = append(state.Messages, domain.Message{
			ID: i, From: "cursor", To: "claude-code", Content: "recent",
			Timestamp: now.Add(-time.Duration(i) * time.Hour),
		})
	}
	for i := 6; i <= 10; i++ {
		state.Messages = append(state.Messages, domain.Message{
			ID: i, From: "cursor", To: "claude-code", Content: "old",
			Timestamp: now.Add(-time.Duration(i+10) * 24 * time.Hour),
		})
	}
	pruned = app.PruneMessages(state, 0, 7)
	if pruned != 5 {
		t.Errorf("expected 5 pruned by TTL, got %d", pruned)
	}
	if len(state.Messages) != 5 {
		t.Errorf("expected 5 messages after TTL prune, got %d", len(state.Messages))
	}
}

// TestMatrix_StaticPlusTaskBound_FullLifecycle is the integration test that
// would have caught the deep-review bug cluster (C1-C4, H1-H4) the first
// time it shipped. For each parent agent type we seed BOTH the static-pool
// row and a task-bound row of the same parent type — that's the recurring
// "two siblings, one parent type" shape that masked the original defects —
// and then drive a full task lifecycle through the public MCP tools:
//
//	create_task → claim_next → report_progress → handoff → cancel_agent
//
// At each step we assert two invariants:
//  1. Exactly one AgentInstance owns the task (no smear, no leak).
//  2. The dead/idle twin is never mistaken for the alive worker. In
//     particular CurrentTasks on the wrong sibling must remain empty.
//
// This is a regression net, not a fix-driving test — every commit in this
// series already passes it.
func TestMatrix_StaticPlusTaskBound_FullLifecycle(t *testing.T) {
	parents := []string{"claude-code", "codex"}

	for _, parentType := range parents {
		parentType := parentType
		t.Run(parentType, func(t *testing.T) {
			svc, repo := newTestService()
			logger := log.New(io.Discard, "", 0)
			canceller := &lifecycleCanceller{outputs: map[string]string{}}
			srv := lifecycleServer(t, svc, logger, canceller)

			_ = svc.Run(func(state *domain.CollabState) error {
				state.AgentInstances[parentType] = &domain.AgentInstance{
					InstanceID: parentType, AgentType: parentType,
					Role: domain.RoleWorker, Status: "idle", MaxTasks: 1,
					CurrentTasks: []int{}, LastHeartbeat: time.Now(),
				}
				state.AgentInstances["cursor"] = &domain.AgentInstance{
					InstanceID: "cursor", AgentType: "cursor",
					Role: domain.RoleDriver, Status: "idle",
				}
				return nil
			})

			createRes, err := callTool(t, srv, "create_task", map[string]any{
				"title": "matrix lifecycle " + parentType, "created_by": "cursor",
				"assigned_to": parentType,
			})
			if err != nil {
				t.Fatalf("create_task: %v", err)
			}
			taskID := lifecycleExtractTaskID(t, resultText(t, createRes))

			seedStaticAndTaskBound(t, repo.state, parentType, taskID)
			taskBoundID := fmt.Sprintf("%s-task-%d", parentType, taskID)

			lifecycleAssertSingleOwner(t, repo.state, taskID, "post-seed")

			_ = svc.Run(func(state *domain.CollabState) error {
				inst := state.AgentInstances[taskBoundID]
				inst.CurrentTasks = []int{}
				inst.Status = "idle"

				for i := range state.Tasks {
					if state.Tasks[i].ID == taskID {
						state.Tasks[i].Status = "pending"
						state.Tasks[i].AssignedTo = parentType
					}
				}
				return nil
			})

			if _, err := callTool(t, srv, "claim_next", map[string]any{
				"agent": taskBoundID,
			}); err != nil {
				t.Fatalf("claim_next(%s): %v", taskBoundID, err)
			}

			lifecycleAssertSingleOwner(t, repo.state, taskID, "post-claim")
			if got := repo.state.AgentInstances[taskBoundID].CurrentTasks; len(got) != 1 || got[0] != taskID {
				t.Fatalf("task-bound CurrentTasks = %v, want [%d]", got, taskID)
			}
			if got := repo.state.AgentInstances[parentType].CurrentTasks; len(got) != 0 {
				t.Errorf("static sibling CurrentTasks should be empty, got %v", got)
			}

			if _, err := callTool(t, srv, "report_progress", map[string]any{
				"agent": taskBoundID, "task_id": taskID, "description": "halfway",
				"percent_complete": 50,
			}); err != nil {
				t.Fatalf("report_progress: %v", err)
			}
			lifecycleAssertSingleOwner(t, repo.state, taskID, "post-progress")

			if _, err := callTool(t, srv, "handoff", map[string]any{
				"from": taskBoundID, "to": "cursor", "task_id": taskID,
				"summary": "did some work", "next_steps": "pls review",
			}); err != nil {
				t.Fatalf("handoff: %v", err)
			}

			for id, inst := range repo.state.AgentInstances {
				if inst == nil {
					continue
				}
				for _, tid := range inst.CurrentTasks {
					if tid == taskID {
						t.Errorf("after handoff, instance %q still owns task %d", id, taskID)
					}
				}
			}

			_ = svc.Run(func(state *domain.CollabState) error {
				for i := range state.Tasks {
					if state.Tasks[i].ID == taskID {
						state.Tasks[i].Status = "in_progress"
						state.Tasks[i].AssignedTo = parentType
					}
				}
				app.AddTaskToInstance(state, taskID, taskBoundID)
				return nil
			})
			lifecycleAssertSingleOwner(t, repo.state, taskID, "after re-claim")

			if _, err := callTool(t, srv, "cancel_agent", map[string]any{
				"agent": taskBoundID, "cancelled_by": "cursor", "reason": "matrix end",
			}); err != nil {
				t.Fatalf("cancel_agent: %v", err)
			}

			for _, tk := range repo.state.Tasks {
				if tk.ID == taskID && tk.Status == "in_progress" {
					t.Errorf("after cancel_agent, task %d still in_progress", taskID)
				}
			}
			if static := repo.state.AgentInstances[parentType]; static != nil {
				for _, tid := range static.CurrentTasks {
					if tid == taskID {
						t.Errorf("after cancel_agent, static sibling still owns task %d", taskID)
					}
				}
			}
		})
	}
}

type lifecycleCanceller struct {
	outputs map[string]string
	cancels []string
}

func (l *lifecycleCanceller) CancelWorker(instanceID string) bool {
	l.cancels = append(l.cancels, instanceID)
	return true
}
func (l *lifecycleCanceller) IsWorkerRunning(instanceID string) bool {
	return false
}
func (l *lifecycleCanceller) GetRecentOutput(instanceID string) string {
	return l.outputs[instanceID]
}

func lifecycleServer(t *testing.T, svc *app.CollabService, logger *log.Logger, c WorkerCanceller) *server.MCPServer {
	t.Helper()
	s := server.NewMCPServer("matrix", "1.0.0")
	registry := app.NewSessionRegistry()
	Register(s, svc, logger, registry, nil, WithCanceller(c))
	return s
}

func lifecycleExtractTaskID(t *testing.T, resp string) int {
	t.Helper()
	for _, tok := range strings.Fields(resp) {
		tok = strings.TrimPrefix(tok, "#")
		tok = strings.TrimRight(tok, ":,)")
		if n, err := strconv.Atoi(tok); err == nil && n > 0 {
			return n
		}
	}
	t.Fatalf("could not extract task id from create_task response: %s", resp)
	return 0
}

func lifecycleAssertSingleOwner(t *testing.T, state *domain.CollabState, taskID int, label string) {
	t.Helper()
	owners := []string{}
	for id, inst := range state.AgentInstances {
		if inst == nil {
			continue
		}
		for _, tid := range inst.CurrentTasks {
			if tid == taskID {
				owners = append(owners, id)
			}
		}
	}
	if len(owners) > 1 {
		t.Errorf("[%s] task %d has %d owners %v; expected at most one", label, taskID, len(owners), owners)
	}
}

func TestValidateAgent(t *testing.T) {
	stateWithAgents := domain.NewCollabState()
	stateWithAgents.AgentInstances = map[string]*domain.AgentInstance{
		"cursor": {AgentType: "cursor"}, "claude-code": {AgentType: "claude-code"},
	}
	tests := []struct {
		name     string
		agent    string
		state    *domain.CollabState
		allowAny bool
		allowAll bool
		wantErr  bool
	}{
		{"valid cursor", "cursor", stateWithAgents, false, false, false},
		{"valid claude-code", "claude-code", stateWithAgents, false, false, false},
		{"invalid agent", "unknown", stateWithAgents, false, false, true},
		{"any allowed", "any", nil, true, false, false},
		{"any not allowed", "any", nil, false, false, true},
		{"all allowed", "all", nil, false, true, false},
		{"all not allowed", "all", nil, false, false, true},
		{"empty agent", "", nil, false, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := app.ValidateAgent(tt.agent, tt.state, tt.allowAny, tt.allowAll)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateAgent(%q, %v, %v) error = %v, wantErr %v",
					tt.agent, tt.allowAny, tt.allowAll, err, tt.wantErr)
			}
		})
	}
}

func TestNewState(t *testing.T) {
	s := domain.NewCollabState()
	if s.Messages == nil {
		t.Error("Messages should not be nil")
	}
	if s.Tasks == nil {
		t.Error("Tasks should not be nil")
	}
	if s.Presence == nil {
		t.Error("Presence should not be nil")
	}
	if s.SessionNotes == nil {
		t.Error("SessionNotes should not be nil")
	}
	if s.Plans == nil {
		t.Error("Plans should not be nil")
	}
	if s.NextMsgID != 1 || s.NextTaskID != 1 || s.NextNoteID != 1 {
		t.Errorf("next IDs should be 1, got %d %d %d", s.NextMsgID, s.NextTaskID, s.NextNoteID)
	}
}

func TestJoinStrings(t *testing.T) {
	tests := []struct {
		name string
		strs []string
		sep  string
		want string
	}{
		{"multiple", []string{"a", "b", "c"}, ", ", "a, b, c"},
		{"single", []string{"a"}, ", ", "a"},
		{"empty", []string{}, ", ", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := app.JoinStrings(tt.strs, tt.sep)
			if got != tt.want {
				t.Errorf("JoinStrings(%v, %q) = %q, want %q", tt.strs, tt.sep, got, tt.want)
			}
		})
	}
}

func TestPlanEnhancementFields(t *testing.T) {
	state := domain.NewCollabState()
	now := time.Now()
	plan := &domain.Plan{
		ID: "test-plan", Title: "Test Plan", Goal: "Test reasoning/acceptance/constraints",
		Items: []domain.PlanItem{}, CreatedBy: "cursor", CreatedAt: now, UpdatedAt: now, Status: "active",
	}
	state.Plans["test-plan"] = plan
	state.ActivePlanID = "test-plan"

	t.Run("add item with enhancement fields", func(t *testing.T) {
		item := domain.PlanItem{
			ID: "1", Title: "Implement auth", Description: "Add JWT authentication",
			Reasoning:   "JWT is stateless and works well with microservices",
			Acceptance:  []string{"JWT tokens are issued on login", "Tokens are validated on protected routes", "Refresh tokens are supported"},
			Constraints: []string{"Must use existing user table", "Cannot break backward compatibility"},
			Status:      "pending", Owner: "claude-code", UpdatedBy: "cursor", UpdatedAt: now,
		}
		plan.Items = append(plan.Items, item)
		if len(plan.Items) != 1 {
			t.Fatalf("expected 1 item, got %d", len(plan.Items))
		}
		added := plan.Items[0]
		if added.Reasoning != "JWT is stateless and works well with microservices" {
			t.Errorf("Reasoning = %q", added.Reasoning)
		}
		if len(added.Acceptance) != 3 || len(added.Constraints) != 2 {
			t.Errorf("Acceptance=%d Constraints=%d", len(added.Acceptance), len(added.Constraints))
		}
	})

	t.Run("update item enhancement fields", func(t *testing.T) {
		plan.Items[0].Reasoning = "Updated: JWT + session hybrid for better security"
		plan.Items[0].Acceptance = []string{"All original criteria", "Plus rate limiting implemented"}
		plan.Items[0].Constraints = append(plan.Items[0].Constraints, "Performance budget: <100ms auth check")
		u := plan.Items[0]
		if u.Reasoning != "Updated: JWT + session hybrid for better security" {
			t.Errorf("Updated Reasoning = %q", u.Reasoning)
		}
		if len(u.Acceptance) != 2 || len(u.Constraints) != 3 {
			t.Errorf("Updated Acceptance=%d Constraints=%d", len(u.Acceptance), len(u.Constraints))
		}
	})

	t.Run("item with empty enhancement fields", func(t *testing.T) {
		plan.Items = append(plan.Items, domain.PlanItem{
			ID: "2", Title: "Simple task", Status: "pending", UpdatedBy: "cursor", UpdatedAt: now,
		})
		item := plan.Items[1]
		if item.Reasoning != "" {
			t.Errorf("Expected empty reasoning, got %q", item.Reasoning)
		}
		if len(item.Acceptance) > 0 || len(item.Constraints) > 0 {
			t.Errorf("Expected empty acceptance/constraints")
		}
	})
}
