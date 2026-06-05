package policy

import (
	"strings"
	"testing"
)

func TestResolveModelTier_MultiProvider(t *testing.T) {
	tiers := ExampleModelTiers

	tests := []struct {
		tier, worker, fallback, want string
	}{
		{"fast", "claude-code", "sonnet", "haiku"},
		{"fast", "codex", "gpt-5-codex", "o4-mini"},
		{"fast", "gemini", "gemini-2.5-pro", "gemini-2.5-flash"},
		{"standard", "codex", "default", "gpt-5-codex"},
		{"capable", "gemini", "flash", "gemini-2.5-pro"},
		{"unknown", "codex", "fallback", "fallback"},
		{"fast", "unknown-worker", "fallback", "fallback"},
	}
	for _, tc := range tests {
		got := ResolveModelTier(tiers, tc.tier, tc.worker, tc.fallback)
		if got != tc.want {
			t.Errorf("ResolveModelTier(%q, %q) = %q, want %q", tc.tier, tc.worker, got, tc.want)
		}
	}
}

func TestFormatModelSelectionGuide(t *testing.T) {
	orch := &OrchestrationConfig{
		Workers: []WorkerConfig{
			{Type: "claude-code", Model: "sonnet"},
			{Type: "codex", Model: "gpt-5-codex"},
			{Type: "gemini", Model: "gemini-2.5-pro"},
		},
		ModelTiers: ExampleModelTiers,
	}
	got := FormatModelSelectionGuide(orch)
	for _, want := range []string{
		"fast —",
		"standard —",
		"capable —",
		"claude-code=haiku",
		"codex=o4-mini",
		"gemini=gemini-2.5-flash",
		"model_tier",
		"create_task",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("FormatModelSelectionGuide missing %q:\n%s", want, got)
		}
	}
}

func TestFormatModelSelectionGuide_NoTiersConfigured(t *testing.T) {
	orch := &OrchestrationConfig{
		Workers: []WorkerConfig{{Type: "claude-code", Model: "sonnet"}},
	}
	got := FormatModelSelectionGuide(orch)
	if !strings.Contains(got, "not configured") {
		t.Errorf("expected unconfigured message, got:\n%s", got)
	}
	if !strings.Contains(got, "claude-code: sonnet") {
		t.Errorf("expected worker default, got:\n%s", got)
	}
}

func TestModelTierCoverageWarnings(t *testing.T) {
	orch := &OrchestrationConfig{
		Workers: []WorkerConfig{
			{Type: "claude-code"},
			{Type: "codex"},
			{Type: "gemini"},
		},
		ModelTiers: map[string]map[string]string{
			"fast": {
				"claude-code": "haiku",
				"codex":       "o4-mini",
				// gemini missing
			},
		},
	}
	warnings := orch.ModelTierCoverageWarnings()
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %v", warnings)
	}
	if !strings.Contains(warnings[0], "gemini") {
		t.Errorf("expected gemini in warning, got %q", warnings[0])
	}
}
