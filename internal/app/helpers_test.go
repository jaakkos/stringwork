package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jaakkos/stringwork/internal/domain"
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
		"codex":      {AgentType: "codex"},
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
