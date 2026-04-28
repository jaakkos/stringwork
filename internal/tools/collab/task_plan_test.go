package collab

import (
	"encoding/json"
	"io"
	"log"
	"strings"
	"testing"

	"github.com/jaakkos/stringwork/internal/app"
	"github.com/jaakkos/stringwork/internal/tasktemplates"
)

// TestTaskPlan_HappyPath end-to-ends the MCP tool: a real CollabService
// backed by a policy that exposes the embedded defaults, calling
// task_plan with the shipped code-review template and a small input
// set. Asserts the JSON shape the driver actually consumes.
func TestTaskPlan_HappyPath(t *testing.T) {
	repo := newMockRepository()
	pol := newMockPolicy()
	pol.taskTemplateSources = []tasktemplates.Source{tasktemplates.DefaultEmbeddedSource()}
	logger := log.New(io.Discard, "", 0)
	svc := app.NewCollabService(repo, pol, logger)
	srv := testServer(svc, logger)

	args := map[string]any{
		"template": "code-review",
		"inputs": map[string]any{
			"files":   []any{"internal/auth/secret_helper.go", "proto/account.proto"},
			"summary": "rotate auth tokens",
		},
	}
	result, err := callTool(t, srv, "task_plan", args)
	if err != nil {
		t.Fatalf("call task_plan: %v", err)
	}
	body := resultText(t, result)

	var resp struct {
		Template string   `json:"template"`
		Tags     []string `json:"tags"`
		Aspects  []struct {
			Template      string   `json:"template"`
			Aspect        string   `json:"aspect"`
			Title         string   `json:"title"`
			Description   string   `json:"description"`
			RelevantFiles []string `json:"relevant_files"`
			SpawnedBy     []string `json:"spawned_by"`
		} `json:"aspects"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal task_plan response: %v\nbody: %s", err, body)
	}
	if resp.Template != "code-review" {
		t.Errorf("template = %q, want code-review", resp.Template)
	}
	wantTags := map[string]bool{"SECURITY": true, "PROTO": true}
	for _, tag := range resp.Tags {
		if !wantTags[tag] {
			t.Errorf("unexpected tag %q in %v", tag, resp.Tags)
		}
	}
	if len(resp.Aspects) < 3 {
		t.Fatalf("expected at least 3 aspects (correctness, code-quality, security), got %d", len(resp.Aspects))
	}
	gotAspectIDs := map[string]bool{}
	for _, a := range resp.Aspects {
		gotAspectIDs[a.Aspect] = true
		if a.Template != "code-review" {
			t.Errorf("aspect template = %q, want code-review", a.Template)
		}
		if !strings.Contains(a.Description, "Severity: MUST_FIX") {
			t.Errorf("aspect %q missing finding-format block", a.Aspect)
		}
	}
	for _, want := range []string{"correctness", "code-quality", "security"} {
		if !gotAspectIDs[want] {
			t.Errorf("missing expected aspect %q in plan", want)
		}
	}
}

// TestTaskPlan_MissingRequired returns an error rather than an empty
// plan when the caller forgets the required `files` input. The driver
// must see the validation failure so it can surface it to the user.
func TestTaskPlan_MissingRequired(t *testing.T) {
	repo := newMockRepository()
	pol := newMockPolicy()
	pol.taskTemplateSources = []tasktemplates.Source{tasktemplates.DefaultEmbeddedSource()}
	logger := log.New(io.Discard, "", 0)
	svc := app.NewCollabService(repo, pol, logger)
	srv := testServer(svc, logger)

	args := map[string]any{
		"template": "code-review",
		"inputs": map[string]any{
			"summary": "fix",
		},
	}
	if _, err := callTool(t, srv, "task_plan", args); err == nil {
		t.Fatalf("expected validation error for missing files input, got nil")
	} else if !strings.Contains(err.Error(), "files") {
		t.Fatalf("expected error mentioning files, got %v", err)
	}
}
