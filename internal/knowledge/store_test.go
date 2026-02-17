package knowledge

import (
	"os"
	"path/filepath"
	"testing"
)

func tempStore(t *testing.T) *KnowledgeStore {
	t.Helper()
	dir := t.TempDir()
	s, err := NewKnowledgeStore(filepath.Join(dir, "test-knowledge.db"))
	if err != nil {
		t.Fatalf("NewKnowledgeStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestStore_IndexAndQuery(t *testing.T) {
	s := tempStore(t)

	doc := Document{
		Path:     "docs/architecture.md",
		Title:    "architecture.md",
		Content:  "The system uses a driver-worker model for task orchestration.",
		Category: "markdown",
	}

	if err := s.Index(doc); err != nil {
		t.Fatalf("Index: %v", err)
	}

	results, err := s.Query("driver worker orchestration", "", 10)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}
	if results[0].Path != "docs/architecture.md" {
		t.Errorf("expected path docs/architecture.md, got %s", results[0].Path)
	}
	if results[0].Category != "markdown" {
		t.Errorf("expected category markdown, got %s", results[0].Category)
	}
}

func TestStore_QueryWithCategory(t *testing.T) {
	s := tempStore(t)

	s.Index(Document{Path: "a.md", Title: "a", Content: "authentication flow", Category: "markdown"})
	s.Index(Document{Path: "b.go", Title: "b", Content: "authentication middleware", Category: "go_source"})

	results, err := s.Query("authentication", "go_source", 10)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Path != "b.go" {
		t.Errorf("expected b.go, got %s", results[0].Path)
	}
}

func TestStore_IndexIfChanged(t *testing.T) {
	s := tempStore(t)

	doc := Document{Path: "test.md", Title: "test", Content: "hello world", Category: "markdown"}

	changed, err := s.IndexIfChanged(doc)
	if err != nil {
		t.Fatalf("first IndexIfChanged: %v", err)
	}
	if !changed {
		t.Error("expected changed=true on first index")
	}

	changed, err = s.IndexIfChanged(doc)
	if err != nil {
		t.Fatalf("second IndexIfChanged: %v", err)
	}
	if changed {
		t.Error("expected changed=false for identical content")
	}

	doc.Content = "updated content"
	changed, err = s.IndexIfChanged(doc)
	if err != nil {
		t.Fatalf("third IndexIfChanged: %v", err)
	}
	if !changed {
		t.Error("expected changed=true for updated content")
	}
}

func TestStore_Remove(t *testing.T) {
	s := tempStore(t)

	s.Index(Document{Path: "rm.md", Title: "rm", Content: "remove me", Category: "markdown"})

	results, _ := s.Query("remove", "", 10)
	if len(results) == 0 {
		t.Fatal("expected results before remove")
	}

	if err := s.Remove("rm.md"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	results, _ = s.Query("remove", "", 10)
	if len(results) != 0 {
		t.Errorf("expected 0 results after remove, got %d", len(results))
	}
}

func TestStore_RemoveByPrefix(t *testing.T) {
	s := tempStore(t)

	s.Index(Document{Path: "session_note:1", Title: "n1", Content: "first note", Category: "session_note"})
	s.Index(Document{Path: "session_note:2", Title: "n2", Content: "second note", Category: "session_note"})
	s.Index(Document{Path: "task:1", Title: "t1", Content: "task one", Category: "task_summary"})

	count, err := s.RemoveByPrefix("session_note:")
	if err != nil {
		t.Fatalf("RemoveByPrefix: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 removed, got %d", count)
	}

	paths, _ := s.IndexedPaths()
	if len(paths) != 1 || paths[0] != "task:1" {
		t.Errorf("expected only task:1 remaining, got %v", paths)
	}
}

func TestStore_IndexedPaths(t *testing.T) {
	s := tempStore(t)

	s.Index(Document{Path: "a.md", Title: "a", Content: "aaa", Category: "markdown"})
	s.Index(Document{Path: "b.go", Title: "b", Content: "bbb", Category: "go_source"})

	paths, err := s.IndexedPaths()
	if err != nil {
		t.Fatalf("IndexedPaths: %v", err)
	}
	if len(paths) != 2 {
		t.Errorf("expected 2 paths, got %d", len(paths))
	}
}

func TestStore_EmptyQuery(t *testing.T) {
	s := tempStore(t)

	results, err := s.Query("", "", 10)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(results) != 0 {
		t.Error("expected no results for empty query")
	}
}

func TestStore_Stats(t *testing.T) {
	s := tempStore(t)

	s.Index(Document{Path: "a.md", Title: "a", Content: "content a", Category: "markdown"})
	s.Index(Document{Path: "b.go", Title: "b", Content: "content b", Category: "go_source"})

	total, byCategory, err := s.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if total != 2 {
		t.Errorf("expected total 2, got %d", total)
	}
	if byCategory["markdown"] != 1 {
		t.Errorf("expected 1 markdown, got %d", byCategory["markdown"])
	}
	if byCategory["go_source"] != 1 {
		t.Errorf("expected 1 go_source, got %d", byCategory["go_source"])
	}
}

func TestChecksumFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("hello"), 0644)

	sum1, err := ChecksumFile(path)
	if err != nil {
		t.Fatalf("ChecksumFile: %v", err)
	}
	if sum1 == "" {
		t.Error("expected non-empty checksum")
	}

	os.WriteFile(path, []byte("world"), 0644)
	sum2, _ := ChecksumFile(path)
	if sum1 == sum2 {
		t.Error("expected different checksums for different content")
	}
}

func TestSanitizeFTSQuery(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello world", "hello world"},
		{"func (m *Worker) Check()", "func m Worker Check"},
		{"", ""},
		{"AND OR NOT", ""},
		{`"quoted" text`, "quoted text"},
	}

	for _, tt := range tests {
		got := sanitizeFTSQuery(tt.input)
		if got != tt.expected {
			t.Errorf("sanitizeFTSQuery(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
