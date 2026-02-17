package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFile_Markdown(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "README.md")
	content := "# Project\n\nThis is a test project with authentication."
	os.WriteFile(path, []byte(content), 0644)

	doc, err := ParseFile(path, dir)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	if doc.Path != "README.md" {
		t.Errorf("expected relative path README.md, got %s", doc.Path)
	}
	if doc.Title != "README.md" {
		t.Errorf("expected title README.md, got %s", doc.Title)
	}
	if doc.Category != "markdown" {
		t.Errorf("expected category markdown, got %s", doc.Category)
	}
	if doc.Content != content {
		t.Errorf("expected content to be pass-through for markdown")
	}
}

func TestParseFile_GoSource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "service.go")
	content := `package app

// CollabService is the main service.
type CollabService struct {
	repo StateRepository
}

// Run executes a mutating operation.
func (s *CollabService) Run(fn func() error) error {
	return fn()
}

func helper() {
	// internal
}
`
	os.WriteFile(path, []byte(content), 0644)

	doc, err := ParseFile(path, dir)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	if doc.Category != "go_source" {
		t.Errorf("expected category go_source, got %s", doc.Category)
	}

	// Should contain the package declaration
	if !strings.Contains(doc.Content, "package app") {
		t.Error("expected parsed content to contain package declaration")
	}
	// Should contain function signatures
	if !strings.Contains(doc.Content, "func (s *CollabService) Run") {
		t.Error("expected parsed content to contain Run method signature")
	}
	// Should contain type declarations
	if !strings.Contains(doc.Content, "type CollabService struct") {
		t.Error("expected parsed content to contain type declaration")
	}
	// Should contain doc comments
	if !strings.Contains(doc.Content, "CollabService is the main service") {
		t.Error("expected parsed content to contain doc comment")
	}
}

func TestParseFile_Config(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	content := "# Claude Instructions\n\nFollow these rules."
	os.WriteFile(path, []byte(content), 0644)

	doc, err := ParseFile(path, dir)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	if doc.Category != "config" {
		t.Errorf("expected category config, got %s", doc.Category)
	}
}

func TestCategorizeFile(t *testing.T) {
	tests := []struct {
		absPath  string
		relPath  string
		expected string
	}{
		{"/project/docs/readme.md", "docs/readme.md", "markdown"},
		{"/project/internal/app/service.go", "internal/app/service.go", "go_source"},
		{"/project/CLAUDE.md", "CLAUDE.md", "config"},
		{"/project/AGENTS.md", "AGENTS.md", "config"},
		{"/project/mcp/config.yaml", "mcp/config.yaml", "config"},
	}

	for _, tt := range tests {
		got := categorizeFile(tt.absPath, tt.relPath)
		if got != tt.expected {
			t.Errorf("categorizeFile(%s) = %s, want %s", tt.relPath, got, tt.expected)
		}
	}
}

func TestShouldIndex(t *testing.T) {
	tests := []struct {
		path          string
		indexGoSource bool
		expected      bool
	}{
		{"docs/README.md", true, true},
		{"internal/app/service.go", true, true},
		{"internal/app/service.go", false, false},
		{"internal/app/service_test.go", true, false},
		{".git/config", true, false},
		{"vendor/pkg/mod.go", true, false},
		{"node_modules/pkg/index.js", true, false},
		{"main.py", true, false},
	}

	for _, tt := range tests {
		got := ShouldIndex(tt.path, tt.indexGoSource)
		if got != tt.expected {
			t.Errorf("ShouldIndex(%s, %v) = %v, want %v", tt.path, tt.indexGoSource, got, tt.expected)
		}
	}
}

func TestFormatSessionNote(t *testing.T) {
	doc := FormatSessionNote(42, "cursor", "We decided to use FTS5.", "decision")
	if doc.Path != "session_note:42" {
		t.Errorf("expected path session_note:42, got %s", doc.Path)
	}
	if doc.Category != "session_note" {
		t.Errorf("expected category session_note, got %s", doc.Category)
	}
	if !strings.Contains(doc.Content, "We decided to use FTS5.") {
		t.Error("expected content to contain note text")
	}
}

func TestFormatTaskSummary(t *testing.T) {
	doc := FormatTaskSummary(5, "Add auth", "Implement JWT auth", "claude-code", "JWT middleware added")
	if doc.Path != "task:5" {
		t.Errorf("expected path task:5, got %s", doc.Path)
	}
	if doc.Category != "task_summary" {
		t.Errorf("expected category task_summary, got %s", doc.Category)
	}
	if !strings.Contains(doc.Content, "JWT middleware added") {
		t.Error("expected content to contain result summary")
	}
}

func TestExtractFuncSignature(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"func main() {", "func main()"},
		{"func (s *Service) Run(fn func() error) error {", "func (s *Service) Run(fn func() error) error"},
		{"func helper()", "func helper()"},
	}

	for _, tt := range tests {
		got := extractFuncSignature(tt.input)
		if got != tt.expected {
			t.Errorf("extractFuncSignature(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
