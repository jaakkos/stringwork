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
