package collab

import (
	"io"
	"log"
	"strings"
	"testing"

	"github.com/jaakkos/stringwork/internal/policy"
)

func TestListModelOptions(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	result, err := callTool(t, srv, "list_model_options", map[string]any{})
	if err != nil {
		t.Fatalf("list_model_options: %v", err)
	}
	text := resultText(t, result)
	for _, want := range []string{"model_tier", "create_task", "fast", "standard", "capable"} {
		if !strings.Contains(text, want) {
			t.Errorf("list_model_options missing %q:\n%s", want, text)
		}
	}
}

func TestGetSessionContext_IncludesModelGuideForDriver(t *testing.T) {
	repo := newMockRepository()
	pol := newMockPolicy()
	pol.orch.ModelTiers = policy.ExampleModelTiers
	pol.orch.Workers = []policy.WorkerConfig{
		{Type: "claude-code", Model: "sonnet"},
		{Type: "codex", Model: "gpt-5-codex"},
	}
	logger := log.New(io.Discard, "", 0)
	svc := newTestServiceWith(repo, pol, logger)
	srv := testServer(svc, logger)

	result, err := callTool(t, srv, "get_session_context", map[string]any{"for": "cursor"})
	if err != nil {
		t.Fatalf("get_session_context: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "DRIVER") {
		t.Fatalf("expected driver role, got:\n%s", text)
	}
	if !strings.Contains(text, "Worker model selection") {
		t.Errorf("expected model guide for driver, got:\n%s", text)
	}
	if !strings.Contains(text, "codex=o4-mini") {
		t.Errorf("expected codex tier mapping in guide, got:\n%s", text)
	}
}

func TestGetSessionContext_OmitsModelGuideForWorker(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	result, err := callTool(t, srv, "get_session_context", map[string]any{"for": "claude-code"})
	if err != nil {
		t.Fatalf("get_session_context: %v", err)
	}
	text := resultText(t, result)
	if strings.Contains(text, "Worker model selection") {
		t.Errorf("worker should not see driver model guide:\n%s", text)
	}
}
