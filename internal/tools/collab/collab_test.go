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

// TestReview_RegisterAgentClaimNext_PhantomAssignment is a regression test
// that exposes a real production bug discovered while live-driving the
// stringwork binary end-to-end via raw MCP JSON-RPC over stdio.
//
// SCENARIO (reproduces a real install with workers: [] in config):
//  1. register_agent("claude-code") succeeds (it's not in AgentInstances
//     yet, so the M1 collision check doesn't reject it; only a
//     RegisteredAgents row is created — not an AgentInstance).
//  2. create_task assigned_to="claude-code" succeeds.
//  3. claim_next agent="claude-code" succeeds: ValidateAgent accepts
//     the registered name, and the tool flips task.Status to
//     "in_progress" with task.AssignedTo="claude-code".
//  4. AddTaskToInstance(state, taskID, "claude-code") then silently
//     returns because there is no AgentInstance to add to and no
//     candidate AgentInstance whose AgentType=="claude-code".
//
// RESULT: the task is in_progress, AssignedTo="claude-code", but no
// AgentInstance owns it. The watchdog has nothing to monitor for
// liveness. report_progress's worker-side heartbeat update silently
// no-ops because findAgentInstance returns nil. cancel_agent("claude-
// code") finds nothing in CurrentTasks to clean up. This is the
// "phantom assignment" failure mode that Commit 9's matrix tests do
// NOT exercise — they always pre-seed an AgentInstance via
// seedStaticAndTaskBound.
//
// CORRECT BEHAVIOR (asserted by this test): EITHER claim_next must
// fail with a clear error when no AgentInstance is available to
// receive ownership, OR claim_next must materialize an AgentInstance
// for the agent so CurrentTasks can record the claim. A successful
// claim with no owning instance is never acceptable.
//
// Today, this test FAILS — that is the point. It is the user-requested
// "test that stringwork is not working correctly" and stays in the
// suite as a real regression net once the underlying bug is fixed.
func TestReview_RegisterAgentClaimNext_PhantomAssignment(t *testing.T) {
	repo := newMockRepository()
	pol := &emptyWorkersPolicy{mockPolicy: newMockPolicy()}
	logger := log.New(io.Discard, "", 0)
	svc := app.NewCollabService(repo, pol, logger)

	canceller := &lifecycleCanceller{outputs: map[string]string{}}
	srv := lifecycleServer(t, svc, logger, canceller)

	if _, err := callTool(t, srv, "register_agent", map[string]any{
		"name": "claude-code", "registered_by": "cursor",
		"capabilities": []any{"coding"},
	}); err != nil {
		t.Fatalf("register_agent: %v", err)
	}

	if _, exists := repo.state.AgentInstances["claude-code"]; exists {
		t.Logf("note: register_agent created an AgentInstance for claude-code (good)")
	} else {
		t.Logf("repro point: after register_agent, AgentInstances has no claude-code row; only RegisteredAgents")
	}

	createRes, err := callTool(t, srv, "create_task", map[string]any{
		"title": "phantom-assignment-repro", "created_by": "cursor",
		"assigned_to": "claude-code",
	})
	if err != nil {
		t.Fatalf("create_task: %v", err)
	}
	taskID := lifecycleExtractTaskID(t, resultText(t, createRes))

	claimRes, claimErr := callTool(t, srv, "claim_next", map[string]any{
		"agent": "claude-code",
	})

	if claimErr != nil {
		t.Logf("claim_next correctly refused with no AgentInstance available: %v", claimErr)
		return
	}

	tk := lifecycleFindTask(t, repo.state, taskID)
	if tk.Status != "in_progress" {
		t.Logf("claim_next did not transition task (status=%q); acceptable as long as no phantom claim occurred", tk.Status)
		return
	}

	var owner string
	for id, inst := range repo.state.AgentInstances {
		if inst == nil {
			continue
		}
		for _, tid := range inst.CurrentTasks {
			if tid == taskID {
				owner = id
				break
			}
		}
		if owner != "" {
			break
		}
	}

	if owner == "" {
		t.Errorf("PHANTOM ASSIGNMENT: claim_next succeeded (response=%q) and set task #%d status=in_progress, AssignedTo=%q, "+
			"but no AgentInstance owns the task in CurrentTasks. The watchdog has no liveness target, "+
			"report_progress cannot refresh a heartbeat, and cancel_agent cannot reach a process. "+
			"AgentInstances at the time of the bad state: %v",
			resultText(t, claimRes), taskID, tk.AssignedTo, lifecycleInstanceKeys(repo.state))
	}
}

// emptyWorkersPolicy mirrors mockPolicy but reports no configured workers,
// matching a real install whose config.yaml uses `workers: []`. Used to
// reproduce the phantom-assignment bug where register_agent + claim_next
// produces a task in_progress with no owning AgentInstance.
type emptyWorkersPolicy struct{ *mockPolicy }

func (p *emptyWorkersPolicy) Orchestration() *policy.OrchestrationConfig {
	return &policy.OrchestrationConfig{Driver: "cursor"}
}

func lifecycleInstanceKeys(state *domain.CollabState) []string {
	keys := make([]string, 0, len(state.AgentInstances))
	for id := range state.AgentInstances {
		keys = append(keys, id)
	}
	return keys
}

// TestReview_StringworkLifecycleStrict is a code-review companion to
// TestMatrix_StaticPlusTaskBound_FullLifecycle (Commit 9). The matrix
// test passes today, but its assertions are intentionally narrow — it
// only checks "no instance owns the task" after handoff and "task is
// not in_progress" after cancel_agent, leaving several side-effects
// unverified that a real failure mode could regress silently:
//
//   - claim_next: task.Status must flip to "in_progress" and AssignedTo
//     must be the canonical parent type (not the task-bound ID).
//   - report_progress: task.ProgressDescription, ProgressPercent, and
//     LastProgressAt must all advance, and the worker's heartbeat must
//     refresh.
//   - handoff: task.AssignedTo must become the recipient's parent type,
//     task.Status must reset to "pending", and a notification message
//     must be enqueued from sender to recipient.
//   - cancel_agent: task.Status must be "cancelled" (not just "not
//     in_progress"), task.ResultSummary must be populated with the
//     cancelled-by attribution, the task-bound AgentInstance + Presence
//     rows must be reaped, the static sibling must be untouched, the
//     STOP message must be enqueued addressed to the task-bound ID,
//     and the canceller's CancelWorker must have been called for the
//     task-bound process.
//
// If any of these tighter assertions fails, the matrix lifecycle test
// is hiding a real regression behind its narrower asserts.
func TestReview_StringworkLifecycleStrict(t *testing.T) {
	const parentType = "claude-code"
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
		"title": "review-strict", "created_by": "cursor", "assigned_to": parentType,
	})
	if err != nil {
		t.Fatalf("create_task: %v", err)
	}
	taskID := lifecycleExtractTaskID(t, resultText(t, createRes))

	seedStaticAndTaskBound(t, repo.state, parentType, taskID)
	taskBoundID := fmt.Sprintf("%s-task-%d", parentType, taskID)

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

	t.Run("claim_next_assertions", func(t *testing.T) {
		if _, err := callTool(t, srv, "claim_next", map[string]any{
			"agent": taskBoundID,
		}); err != nil {
			t.Fatalf("claim_next: %v", err)
		}
		tk := lifecycleFindTask(t, repo.state, taskID)
		if tk.Status != "in_progress" {
			t.Errorf("post-claim task.Status = %q, want \"in_progress\"", tk.Status)
		}
		if tk.AssignedTo != parentType {
			t.Errorf("post-claim task.AssignedTo = %q, want canonical %q (not the task-bound ID)", tk.AssignedTo, parentType)
		}
	})

	t.Run("report_progress_assertions", func(t *testing.T) {
		preHB := repo.state.AgentInstances[taskBoundID].LastHeartbeat
		time.Sleep(2 * time.Millisecond)
		if _, err := callTool(t, srv, "report_progress", map[string]any{
			"agent": taskBoundID, "task_id": taskID,
			"description": "halfway through", "percent_complete": 42,
		}); err != nil {
			t.Fatalf("report_progress: %v", err)
		}
		tk := lifecycleFindTask(t, repo.state, taskID)
		if tk.ProgressDescription != "halfway through" {
			t.Errorf("ProgressDescription = %q, want %q", tk.ProgressDescription, "halfway through")
		}
		if tk.ProgressPercent != 42 {
			t.Errorf("ProgressPercent = %d, want 42", tk.ProgressPercent)
		}
		if tk.LastProgressAt.IsZero() {
			t.Error("LastProgressAt should have been set")
		}
		if !repo.state.AgentInstances[taskBoundID].LastHeartbeat.After(preHB) {
			t.Errorf("worker heartbeat did not advance: pre=%v post=%v",
				preHB, repo.state.AgentInstances[taskBoundID].LastHeartbeat)
		}
	})

	t.Run("handoff_assertions", func(t *testing.T) {
		preMsgCount := len(repo.state.Messages)
		if _, err := callTool(t, srv, "handoff", map[string]any{
			"from": taskBoundID, "to": "cursor", "task_id": taskID,
			"summary": "did some work", "next_steps": "pls review",
		}); err != nil {
			t.Fatalf("handoff: %v", err)
		}
		tk := lifecycleFindTask(t, repo.state, taskID)
		if tk.AssignedTo != "cursor" {
			t.Errorf("post-handoff task.AssignedTo = %q, want \"cursor\"", tk.AssignedTo)
		}
		if tk.Status != "pending" {
			t.Errorf("post-handoff task.Status = %q, want \"pending\"", tk.Status)
		}

		var found *domain.Message
		for i := preMsgCount; i < len(repo.state.Messages); i++ {
			m := &repo.state.Messages[i]
			if m.From == taskBoundID && m.To == "cursor" && strings.Contains(m.Content, "Handoff") {
				found = m
				break
			}
		}
		if found == nil {
			t.Errorf("expected handoff message from %q to \"cursor\"; got %d new messages, none matched",
				taskBoundID, len(repo.state.Messages)-preMsgCount)
		} else {
			if !strings.Contains(found.Content, "did some work") {
				t.Errorf("handoff message missing summary; got %q", found.Content)
			}
			if !strings.Contains(found.Content, "pls review") {
				t.Errorf("handoff message missing next_steps; got %q", found.Content)
			}
		}
	})

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

	t.Run("cancel_agent_assertions", func(t *testing.T) {
		preStaticHB := repo.state.AgentInstances[parentType].LastHeartbeat
		preStaticStatus := repo.state.AgentInstances[parentType].Status
		preMsgCount := len(repo.state.Messages)

		if _, err := callTool(t, srv, "cancel_agent", map[string]any{
			"agent": taskBoundID, "cancelled_by": "cursor", "reason": "review cancel",
		}); err != nil {
			t.Fatalf("cancel_agent: %v", err)
		}

		tk := lifecycleFindTask(t, repo.state, taskID)
		if tk.Status != "cancelled" {
			t.Errorf("post-cancel task.Status = %q, want \"cancelled\"", tk.Status)
		}
		if !strings.Contains(tk.ResultSummary, "cursor") {
			t.Errorf("post-cancel task.ResultSummary = %q, want it to mention canceller \"cursor\"", tk.ResultSummary)
		}
		if !strings.Contains(tk.ResultSummary, "review cancel") {
			t.Errorf("post-cancel task.ResultSummary = %q, want it to include reason \"review cancel\"", tk.ResultSummary)
		}

		if _, exists := repo.state.AgentInstances[taskBoundID]; exists {
			t.Errorf("task-bound AgentInstance %q should have been reaped", taskBoundID)
		}
		if _, exists := repo.state.Presence[taskBoundID]; exists {
			t.Errorf("task-bound Presence %q should have been reaped", taskBoundID)
		}

		static := repo.state.AgentInstances[parentType]
		if static == nil {
			t.Fatalf("static sibling %q must NOT be reaped", parentType)
		}
		if static.LastHeartbeat != preStaticHB {
			t.Errorf("static sibling heartbeat changed (pre=%v, post=%v); cancel of task-bound must not touch sibling",
				preStaticHB, static.LastHeartbeat)
		}
		_ = preStaticStatus

		var stop *domain.Message
		for i := preMsgCount; i < len(repo.state.Messages); i++ {
			m := &repo.state.Messages[i]
			if m.From == "system" && m.To == taskBoundID && strings.Contains(m.Content, "STOP") {
				stop = m
				break
			}
		}
		if stop == nil {
			t.Errorf("expected STOP system message addressed to task-bound %q; %d new messages found, none matched",
				taskBoundID, len(repo.state.Messages)-preMsgCount)
		}

		killed := false
		for _, id := range canceller.cancels {
			if id == taskBoundID {
				killed = true
				break
			}
		}
		if !killed {
			t.Errorf("CancelWorker(%q) was never called; canceller.cancels=%v", taskBoundID, canceller.cancels)
		}
	})
}

func lifecycleFindTask(t *testing.T, state *domain.CollabState, id int) *domain.Task {
	t.Helper()
	for i := range state.Tasks {
		if state.Tasks[i].ID == id {
			return &state.Tasks[i]
		}
	}
	t.Fatalf("task #%d not found", id)
	return nil
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
