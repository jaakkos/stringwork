package collab

import (
	"strings"
	"testing"
)

func TestAgentNameForClient(t *testing.T) {
	tests := []struct {
		clientName string
		want       string
	}{
		{"Cursor", "cursor"},
		{"cursor-ide", "cursor"},
		{"Claude Desktop", "claude-code"},
		{"claude-code", "claude-code"},
		{"Codex", "codex"},
		{"codex-cli", "codex"},
		{"Gemini CLI", "gemini"},
		{"gemini", "gemini"},
		{"Windsurf", "windsurf"},
		{"VSCode", "vscode"},
		{"Visual Studio Code", "vscode"},
		{"unknown-client", "unknown-client"},
	}
	for _, tt := range tests {
		t.Run(tt.clientName, func(t *testing.T) {
			got := AgentNameForClient(tt.clientName)
			if got != tt.want {
				t.Errorf("AgentNameForClient(%q) = %q, want %q", tt.clientName, got, tt.want)
			}
		})
	}
}

func TestInstructions_CursorClient(t *testing.T) {
	text := DynamicInstructionsForClient("Cursor")

	// Should identify as cursor
	if !strings.Contains(text, `You are "cursor"`) {
		t.Error("instructions should identify agent as cursor")
	}
	if !strings.Contains(text, `Your pair is "claude-code"`) {
		t.Error("instructions should set pair as claude-code")
	}
	// Should include startup checklist
	if !strings.Contains(text, "get_session_context for 'cursor'") {
		t.Error("instructions should include startup checklist for cursor")
	}
	// Should include workflow commands
	if !strings.Contains(text, "send_message from='cursor' to='claude-code'") {
		t.Error("instructions should include workflow commands")
	}
}

func TestInstructions_ClaudeClient(t *testing.T) {
	text := DynamicInstructionsForClient("Claude Desktop")

	if !strings.Contains(text, `You are "claude-code"`) {
		t.Error("instructions should identify agent as claude-code")
	}
	if !strings.Contains(text, `Your pair is "cursor"`) {
		t.Error("instructions should set pair as cursor")
	}
	if !strings.Contains(text, "get_session_context for 'claude-code'") {
		t.Error("instructions should include startup checklist for claude-code")
	}
}

func TestInstructions_CodexClient(t *testing.T) {
	text := DynamicInstructionsForClient("Codex")

	if !strings.Contains(text, `You are "codex"`) {
		t.Error("instructions should identify agent as codex")
	}
	if !strings.Contains(text, `Your pair is "cursor"`) {
		t.Error("instructions should set pair as cursor")
	}
	if !strings.Contains(text, "get_session_context for 'codex'") {
		t.Error("instructions should include startup checklist for codex")
	}
}

func TestInstructions_GlobalState(t *testing.T) {
	text := DynamicInstructionsForClient("Cursor")

	if !strings.Contains(text, "~/.config/stringwork/state.sqlite") {
		t.Error("instructions should mention global state file")
	}
}

func TestPairForAgent_ClaudeCodeDriver(t *testing.T) {
	// Set claude-code as driver
	old := getDriverID()
	SetDriverID("claude-code")
	defer SetDriverID(old)

	// claude-code (driver) should pair with codex (first non-driver worker)
	got := pairForAgent("claude-code")
	if got == "claude-code" {
		t.Error("driver should not pair with itself")
	}
	// Should return a worker type (codex is first worker that isn't claude-code)
	if got != "codex" {
		t.Errorf("pairForAgent(\"claude-code\") with claude-code as driver = %q, want \"codex\"", got)
	}

	// Workers should pair with the driver (claude-code)
	if got := pairForAgent("codex"); got != "claude-code" {
		t.Errorf("pairForAgent(\"codex\") = %q, want \"claude-code\"", got)
	}
	if got := pairForAgent("cursor"); got != "claude-code" {
		t.Errorf("pairForAgent(\"cursor\") = %q, want \"claude-code\"", got)
	}
	if got := pairForAgent("gemini"); got != "claude-code" {
		t.Errorf("pairForAgent(\"gemini\") = %q, want \"claude-code\"", got)
	}
}

func TestPairForAgent_DefaultCursorDriver(t *testing.T) {
	old := getDriverID()
	SetDriverID("")
	defer SetDriverID(old)

	// With empty driver, default to cursor behavior
	if got := pairForAgent("cursor"); got != "claude-code" {
		t.Errorf("pairForAgent(\"cursor\") with default driver = %q, want \"claude-code\"", got)
	}
	if got := pairForAgent("claude-code"); got != "cursor" {
		t.Errorf("pairForAgent(\"claude-code\") with default driver = %q, want \"cursor\"", got)
	}
}

func TestInstructionsForRole_ClaudeCodeDriver(t *testing.T) {
	text := InstructionsForRole("claude-code", "claude-code")

	if !strings.Contains(text, "**driver**") {
		t.Error("should contain driver role")
	}
	if !strings.Contains(text, "get_session_context for 'claude-code'") {
		t.Error("should include claude-code in startup checklist")
	}
	if !strings.Contains(text, "cancelled_by='claude-code'") {
		t.Error("should reference claude-code as canceller")
	}
}

func TestInstructionsForRole_WorkerWithClaudeCodeDriver(t *testing.T) {
	text := InstructionsForRole("codex", "claude-code")

	if !strings.Contains(text, "**worker**") {
		t.Error("should contain worker role")
	}
	if !strings.Contains(text, "The driver is claude-code") {
		t.Error("should reference claude-code as driver")
	}
	if !strings.Contains(text, "send_message to 'claude-code'") {
		t.Error("should instruct sending messages to claude-code driver")
	}
}

func TestDynamicInstructions_ClaudeCodeDriver(t *testing.T) {
	old := getDriverID()
	SetDriverID("claude-code")
	defer SetDriverID(old)

	// When claude-code is driver, codex should pair with claude-code
	text := DynamicInstructionsForClient("Codex")
	if !strings.Contains(text, `Your pair is "claude-code"`) {
		t.Error("codex should pair with claude-code when it's driver")
	}

	// When claude-code is driver, cursor should also pair with claude-code
	text = DynamicInstructionsForClient("Cursor")
	if !strings.Contains(text, `Your pair is "claude-code"`) {
		t.Error("cursor should pair with claude-code when it's driver")
	}
}
