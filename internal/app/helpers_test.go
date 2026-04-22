package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jaakkos/stringwork/internal/domain"
	"github.com/jaakkos/stringwork/internal/policy"
)

func TestTruncate(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		max    int
		expect string
	}{
		{"short string", "hello", 10, "hello"},
		{"exact length", "hello", 5, "hello"},
		{"truncate", "hello world", 5, "hello..."},
		{"empty", "", 5, ""},
		{"unicode", "你好世界", 2, "你好..."},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := Truncate(tc.input, tc.max)
			if result != tc.expect {
				t.Errorf("Truncate(%q, %d) = %q, want %q", tc.input, tc.max, result, tc.expect)
			}
		})
	}
}

func TestValidateAgent(t *testing.T) {
	stateWithAgents := domain.NewCollabState()
	stateWithAgents.AgentInstances = map[string]*domain.AgentInstance{
		"cursor": {AgentType: "cursor"}, "claude-code": {AgentType: "claude-code"}, "codex": {AgentType: "codex"},
	}
	tests := []struct {
		name      string
		agent     string
		state     *domain.CollabState
		allowAny  bool
		allowAll  bool
		extra     []string
		wantError bool
	}{
		{"valid cursor", "cursor", stateWithAgents, false, false, nil, false},
		{"valid claude-code", "claude-code", stateWithAgents, false, false, nil, false},
		{"valid codex", "codex", stateWithAgents, false, false, nil, false},
		{"empty agent", "", nil, false, false, nil, true},
		{"unknown agent", "unknown", stateWithAgents, false, false, nil, true},
		{"unknown when state nil", "cursor", nil, false, false, nil, true},
		{"any without allow", "any", nil, false, false, nil, true},
		{"any with allow", "any", nil, true, false, nil, false},
		{"all without allow", "all", nil, false, false, nil, true},
		{"all with allow", "all", nil, false, true, nil, false},
		{"extra allowed", "custom-agent", nil, false, false, []string{"custom-agent"}, false},
		{"extra not matched", "other", nil, false, false, []string{"custom-agent"}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var state *domain.CollabState
			if tc.state != nil {
				state = tc.state
			}
			err := ValidateAgent(tc.agent, state, tc.allowAny, tc.allowAll, tc.extra...)
			if (err != nil) != tc.wantError {
				t.Errorf("ValidateAgent(%q) error = %v, wantError %v", tc.agent, err, tc.wantError)
			}
		})
	}
}

func TestIsBuiltinAgent(t *testing.T) {
	state := domain.NewCollabState()
	state.AgentInstances = map[string]*domain.AgentInstance{
		"cursor": {AgentType: "cursor"}, "claude-code": {AgentType: "claude-code"}, "codex": {AgentType: "codex"},
	}
	if !IsBuiltinAgent("cursor", state) {
		t.Error("cursor should be a builtin agent when in state")
	}
	if !IsBuiltinAgent("claude-code", state) {
		t.Error("claude-code should be a builtin agent when in state")
	}
	if IsBuiltinAgent("unknown", state) {
		t.Error("unknown should not be a builtin agent")
	}
	if IsBuiltinAgent("cursor", nil) {
		t.Error("cursor with nil state should not be builtin (no fallback)")
	}
}

func TestGetBuiltinAgents(t *testing.T) {
	// nil or empty state returns nil (no builtin fallback)
	if got := GetBuiltinAgents(nil); got != nil {
		t.Errorf("GetBuiltinAgents(nil) = %v, want nil", got)
	}
	state := domain.NewCollabState()
	state.AgentInstances = map[string]*domain.AgentInstance{
		"cursor":      {AgentType: "cursor"},
		"claude-code": {AgentType: "claude-code"},
		"codex":       {AgentType: "codex"},
	}
	agents := GetBuiltinAgents(state)
	if len(agents) != 3 {
		t.Errorf("expected 3 agent types, got %d", len(agents))
	}
	found := make(map[string]bool)
	for _, a := range agents {
		found[a] = true
	}
	if !found["cursor"] || !found["claude-code"] || !found["codex"] {
		t.Error("expected cursor, claude-code, and codex in builtin agents")
	}
}

func TestJoinStrings(t *testing.T) {
	tests := []struct {
		name   string
		strs   []string
		sep    string
		expect string
	}{
		{"empty", []string{}, ", ", ""},
		{"single", []string{"a"}, ", ", "a"},
		{"multiple", []string{"a", "b", "c"}, ", ", "a, b, c"},
		{"different sep", []string{"a", "b"}, "-", "a-b"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := JoinStrings(tc.strs, tc.sep)
			if result != tc.expect {
				t.Errorf("JoinStrings(%v, %q) = %q, want %q", tc.strs, tc.sep, result, tc.expect)
			}
		})
	}
}

func TestDetectProjectInfo_NonGitDir(t *testing.T) {
	// Create a temp dir without git
	dir := t.TempDir()

	info := DetectProjectInfo(dir)

	if info == nil {
		t.Fatal("info should not be nil")
	}
	if info.Path != dir {
		t.Errorf("Path = %q, want %q", info.Path, dir)
	}
	if info.Name != filepath.Base(dir) {
		t.Errorf("Name = %q, want %q", info.Name, filepath.Base(dir))
	}
	if info.IsGitRepo {
		t.Error("IsGitRepo should be false for non-git directory")
	}
	if info.GitBranch != "" {
		t.Errorf("GitBranch should be empty, got %q", info.GitBranch)
	}
}

func TestDetectProjectInfo_GitDir(t *testing.T) {
	// Create a temp dir with a git repo
	dir := t.TempDir()

	// Initialize git repo
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatalf("failed to create .git dir: %v", err)
	}

	// Initialize a proper git repo for the test
	if _, err := runGitCommand(dir, "init"); err != nil {
		t.Skip("git not available")
	}
	if _, err := runGitCommand(dir, "config", "user.email", "test@test.com"); err != nil {
		t.Skip("git config failed")
	}
	if _, err := runGitCommand(dir, "config", "user.name", "Test"); err != nil {
		t.Skip("git config failed")
	}

	// Create a commit so we have a branch
	testFile := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	if _, err := runGitCommand(dir, "add", "."); err != nil {
		t.Skip("git add failed")
	}
	if _, err := runGitCommand(dir, "commit", "-m", "initial"); err != nil {
		t.Skip("git commit failed")
	}

	info := DetectProjectInfo(dir)

	if info == nil {
		t.Fatal("info should not be nil")
	}
	if !info.IsGitRepo {
		t.Error("IsGitRepo should be true for git directory")
	}
	if info.GitBranch == "" {
		t.Error("GitBranch should not be empty for initialized git repo")
	}
}

func TestRegisteredAgentNames_Nil(t *testing.T) {
	names := RegisteredAgentNames(nil)
	if len(names) != 0 {
		t.Errorf("nil state should return empty, got %v", names)
	}
}

func TestRegisteredAgentNames_Empty(t *testing.T) {
	state := domain.NewCollabState()
	names := RegisteredAgentNames(state)
	if len(names) != 0 {
		t.Errorf("empty registered agents should return empty, got %v", names)
	}
}

func TestRegisteredAgentNames_WithAgents(t *testing.T) {
	state := domain.NewCollabState()
	state.RegisteredAgents["bot-a"] = &domain.RegisteredAgent{Name: "bot-a"}
	state.RegisteredAgents["bot-b"] = &domain.RegisteredAgent{Name: "bot-b"}

	names := RegisteredAgentNames(state)
	if len(names) != 2 {
		t.Fatalf("expected 2 names, got %d", len(names))
	}

	found := make(map[string]bool)
	for _, n := range names {
		found[n] = true
	}
	if !found["bot-a"] || !found["bot-b"] {
		t.Errorf("expected bot-a and bot-b, got %v", names)
	}
}

func TestConfiguredDriver(t *testing.T) {
	tests := []struct {
		name  string
		state *domain.CollabState
		want  string
	}{
		{"nil state", nil, "cursor"},
		{"empty DriverID", &domain.CollabState{}, "cursor"},
		{"cursor driver", &domain.CollabState{DriverID: "cursor"}, "cursor"},
		{"claude-code driver", &domain.CollabState{DriverID: "claude-code"}, "claude-code"},
		{"custom driver", &domain.CollabState{DriverID: "my-driver"}, "my-driver"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ConfiguredDriver(tc.state)
			if got != tc.want {
				t.Errorf("ConfiguredDriver() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEnsureAgentInstances_ClaudeCodeDriver(t *testing.T) {
	state := domain.NewCollabState()
	orch := &policy.OrchestrationConfig{
		Driver: "claude-code",
		Workers: []policy.WorkerConfig{
			{Type: "codex", Instances: 1},
			{Type: "gemini", Instances: 1},
		},
	}

	EnsureAgentInstances(state, orch)

	// DriverID should be set to claude-code
	if state.DriverID != "claude-code" {
		t.Errorf("DriverID = %q, want \"claude-code\"", state.DriverID)
	}

	// claude-code should be the driver instance
	inst := state.AgentInstances["claude-code"]
	if inst == nil {
		t.Fatal("expected claude-code agent instance")
	}
	if inst.Role != domain.RoleDriver {
		t.Errorf("claude-code role = %q, want driver", inst.Role)
	}

	// Workers should exist
	if state.AgentInstances["codex"] == nil {
		t.Error("expected codex agent instance")
	}
	if state.AgentInstances["gemini"] == nil {
		t.Error("expected gemini agent instance")
	}
	if state.AgentInstances["codex"].Role != domain.RoleWorker {
		t.Errorf("codex role = %q, want worker", state.AgentInstances["codex"].Role)
	}
}

func TestOrchestrationAgentTypes_Nil(t *testing.T) {
	got := OrchestrationAgentTypes(nil)
	if len(got) != 1 || got[0] != "cursor" {
		t.Errorf("OrchestrationAgentTypes(nil) = %v, want [\"cursor\"]", got)
	}
}

func TestEscapeAppleScript(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{"plain", "hello", "hello"},
		{"quotes", `say "hello"`, `say \"hello\"`},
		{"backslash", `path\to`, `path\\to`},
		{"newline", "line1\nline2", `line1\nline2`},
		{"mixed", "say \"hello\nworld\"", `say \"hello\nworld\"`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := EscapeAppleScript(tc.input)
			if result != tc.expect {
				t.Errorf("EscapeAppleScript(%q) = %q, want %q", tc.input, result, tc.expect)
			}
		})
	}
}

// TestMigrateTaskBoundCorruption_FixesAllThreeMutationKinds exercises the
// one-time startup migration against a state that contains all three
// historical corruption patterns: instance-ID in task.AssignedTo,
// AgentInstance.AgentType carrying a task-bound value, and a task-bound
// name registered as a top-level agent.
func TestMigrateTaskBoundCorruption_FixesAllThreeMutationKinds(t *testing.T) {
	state := domain.NewCollabState()

	state.AgentInstances["claude-code-1"] = &domain.AgentInstance{
		InstanceID: "claude-code-1",
		AgentType:  "claude-code",
		Role:       domain.RoleWorker,
		Status:     "busy",
	}
	state.AgentInstances["claude-code-task-2"] = &domain.AgentInstance{
		InstanceID: "claude-code-task-2",
		// historical bug: AgentType set to the task-bound ID
		AgentType: "claude-code-task-2",
		Role:      domain.RoleWorker,
		Status:    "busy",
	}
	state.AgentInstances["codex"] = &domain.AgentInstance{
		InstanceID: "codex",
		AgentType:  "codex",
		Role:       domain.RoleWorker,
		Status:     "idle",
	}

	// Registered agents: one legitimate top-level agent + a polluted
	// task-bound leaker that must be removed.
	state.RegisteredAgents["my-bot"] = &domain.RegisteredAgent{Name: "my-bot"}
	state.RegisteredAgents["claude-code-task-2"] = &domain.RegisteredAgent{Name: "claude-code-task-2"}

	state.Tasks = append(state.Tasks,
		// historical bug: AssignedTo is an instance ID rather than type
		domain.Task{ID: 1, Title: "Legacy instance assignment", Status: "in_progress", AssignedTo: "claude-code-1"},
		// historical bug: AssignedTo is a task-bound ID
		domain.Task{ID: 2, Title: "Legacy task-bound assignment", Status: "in_progress", AssignedTo: "claude-code-task-2"},
		// already-correct parent type assignment — must be left alone
		domain.Task{ID: 3, Title: "Already canonical", Status: "in_progress", AssignedTo: "codex"},
		// "any" is a special sentinel — must not be rewritten
		domain.Task{ID: 4, Title: "Open pool task", Status: "pending", AssignedTo: "any"},
		// Empty assignment — must not be rewritten
		domain.Task{ID: 5, Title: "Unassigned", Status: "pending", AssignedTo: ""},
	)

	report := MigrateTaskBoundCorruption(state)

	if report.TasksReassigned != 2 {
		t.Errorf("TasksReassigned = %d, want 2", report.TasksReassigned)
	}
	if report.InstancesRetyped != 1 {
		t.Errorf("InstancesRetyped = %d, want 1", report.InstancesRetyped)
	}
	if report.RegisteredAgentsGone != 1 {
		t.Errorf("RegisteredAgentsGone = %d, want 1", report.RegisteredAgentsGone)
	}
	if total := report.Total(); total != 4 {
		t.Errorf("Total = %d, want 4", total)
	}

	// Tasks: legacy instance/task-bound assignments should now hold parent type.
	for _, tc := range []struct {
		id   int
		want string
	}{
		{1, "claude-code"},
		{2, "claude-code"},
		{3, "codex"},
		{4, "any"},
		{5, ""},
	} {
		var got string
		for _, tk := range state.Tasks {
			if tk.ID == tc.id {
				got = tk.AssignedTo
				break
			}
		}
		if got != tc.want {
			t.Errorf("task #%d AssignedTo = %q, want %q", tc.id, got, tc.want)
		}
	}

	// Instance retype: claude-code-task-2 must now carry the parent type.
	if inst := state.AgentInstances["claude-code-task-2"]; inst == nil {
		t.Fatal("expected claude-code-task-2 instance to remain")
	} else if inst.AgentType != "claude-code" {
		t.Errorf("claude-code-task-2 AgentType = %q, want \"claude-code\"", inst.AgentType)
	}

	// Registered agents: legit parent remains, task-bound entry deleted.
	if _, ok := state.RegisteredAgents["my-bot"]; !ok {
		t.Error("legitimate registered agent 'my-bot' should be preserved")
	}
	if _, ok := state.RegisteredAgents["claude-code-task-2"]; ok {
		t.Error("task-bound registered agent 'claude-code-task-2' should be removed")
	}
}

// TestMigrateTaskBoundCorruption_Idempotent verifies that running the
// migration on an already-clean state is a no-op and that a second run
// after a first repair does not mutate further.
func TestMigrateTaskBoundCorruption_Idempotent(t *testing.T) {
	state := domain.NewCollabState()
	state.AgentInstances["claude-code-task-1"] = &domain.AgentInstance{
		InstanceID: "claude-code-task-1",
		AgentType:  "claude-code-task-1",
		Role:       domain.RoleWorker,
	}
	state.Tasks = append(state.Tasks, domain.Task{
		ID: 1, Title: "t", Status: "in_progress", AssignedTo: "claude-code-task-1",
	})
	state.RegisteredAgents["claude-code-task-1"] = &domain.RegisteredAgent{Name: "claude-code-task-1"}

	first := MigrateTaskBoundCorruption(state)
	if first.Total() == 0 {
		t.Fatal("expected first run to repair at least one row")
	}

	second := MigrateTaskBoundCorruption(state)
	if second.Total() != 0 {
		t.Errorf("second run should be a no-op, got %d mutations (%v)", second.Total(), second.Mutations)
	}
}

// TestMigrateTaskBoundCorruption_NilState guards against nil input.
func TestMigrateTaskBoundCorruption_NilState(t *testing.T) {
	report := MigrateTaskBoundCorruption(nil)
	if report.Total() != 0 {
		t.Errorf("nil state should yield empty report, got %d", report.Total())
	}
}

// TestResolveParentAgentType_Table covers the resolution precedence order
// used by every write path and watchdog correlation site. The resolver
// must never emit a "-task-N" fragment as an AgentType under any input.
func TestResolveParentAgentType_Table(t *testing.T) {
	state := domain.NewCollabState()
	state.RegisteredAgents["my-bot"] = &domain.RegisteredAgent{Name: "my-bot"}
	state.RegisteredAgents["custom-bot"] = &domain.RegisteredAgent{Name: "custom-bot"}
	state.AgentInstances["claude-code-1"] = &domain.AgentInstance{
		InstanceID: "claude-code-1", AgentType: "claude-code",
		Role: domain.RoleWorker,
	}
	state.AgentInstances["codex"] = &domain.AgentInstance{
		InstanceID: "codex", AgentType: "codex",
		Role: domain.RoleWorker,
	}
	// simulate a corrupted instance whose AgentType still carries the
	// task-bound suffix — the resolver should NOT trust that value.
	state.AgentInstances["codex-task-9"] = &domain.AgentInstance{
		InstanceID: "codex-task-9", AgentType: "codex-task-9",
		Role: domain.RoleWorker,
	}

	tests := []struct {
		name  string
		agent string
		want  string
	}{
		{"empty input", "", ""},
		{"known parent type via instance", "claude-code-1", "claude-code"},
		{"parent type in registered agents", "my-bot", "my-bot"},
		{"task-bound of registered parent", "my-bot-task-4", "my-bot"},
		{"task-bound of instance AgentType", "codex-task-11", "codex"},
		{"task-bound ignores corrupted self AgentType", "codex-task-9", "codex"},
		{"unknown parent", "brand-new-agent", "brand-new-agent"},
		{"unknown task-bound falls back to base", "unknown-task-7", "unknown"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveParentAgentType(state, tc.agent)
			if got != tc.want {
				t.Errorf("ResolveParentAgentType(%q) = %q, want %q", tc.agent, got, tc.want)
			}
		})
	}
}

// TestResolveParentAgentType_NilState exercises the fast-path for nil state.
func TestResolveParentAgentType_NilState(t *testing.T) {
	if got := ResolveParentAgentType(nil, ""); got != "" {
		t.Errorf("nil state + empty agent = %q, want empty", got)
	}
	if got := ResolveParentAgentType(nil, "claude-code-task-3"); got != "claude-code" {
		t.Errorf("nil state + task-bound = %q, want \"claude-code\"", got)
	}
	if got := ResolveParentAgentType(nil, "unknown"); got != "unknown" {
		t.Errorf("nil state + plain agent = %q, want \"unknown\"", got)
	}
}
