package knowledge

import (
	"log"
	"os"
	"path/filepath"
	"testing"
)

// mockStateProvider implements StateProvider for testing.
type mockStateProvider struct {
	notes []SessionNoteData
	tasks []TaskData
}

func (m *mockStateProvider) SessionNotes() []SessionNoteData { return m.notes }
func (m *mockStateProvider) CompletedTasks() []TaskData      { return m.tasks }

func TestIndexer_FullScan(t *testing.T) {
	dir := t.TempDir()
	store := tempStore(t)
	logger := log.New(os.Stderr, "[test] ", log.LstdFlags)

	// Create test files
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test Project\n\nWith authentication."), 0644)
	os.MkdirAll(filepath.Join(dir, "docs"), 0755)
	os.WriteFile(filepath.Join(dir, "docs", "setup.md"), []byte("# Setup\n\nInstall dependencies."), 0644)

	indexer := NewIndexer(store, IndexerConfig{
		WorkspaceRoot: dir,
		IndexGoSource: false,
		WatchEnabled:  false,
	}, nil, logger)

	indexed, removed := indexer.FullScan()
	if indexed < 2 {
		t.Errorf("expected at least 2 indexed files, got %d", indexed)
	}
	if removed != 0 {
		t.Errorf("expected 0 removed on first scan, got %d", removed)
	}

	// Query should find results
	results, err := store.Query("authentication", "", 10)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected results for 'authentication' query")
	}
}

func TestIndexer_FullScan_GoSource(t *testing.T) {
	dir := t.TempDir()
	store := tempStore(t)
	logger := log.New(os.Stderr, "[test] ", log.LstdFlags)

	// Create Go source file
	os.MkdirAll(filepath.Join(dir, "internal", "app"), 0755)
	os.WriteFile(filepath.Join(dir, "internal", "app", "service.go"), []byte(`package app

// AuthService handles user authentication.
func AuthService() {}
`), 0644)

	// Without Go source indexing
	indexer := NewIndexer(store, IndexerConfig{
		WorkspaceRoot: dir,
		IndexGoSource: false,
		WatchEnabled:  false,
	}, nil, logger)

	indexed, _ := indexer.FullScan()
	if indexed != 0 {
		t.Errorf("expected 0 indexed with IndexGoSource=false, got %d", indexed)
	}

	// With Go source indexing
	indexer2 := NewIndexer(store, IndexerConfig{
		WorkspaceRoot: dir,
		IndexGoSource: true,
		WatchEnabled:  false,
	}, nil, logger)

	indexed, _ = indexer2.FullScan()
	if indexed != 1 {
		t.Errorf("expected 1 indexed with IndexGoSource=true, got %d", indexed)
	}
}

func TestIndexer_FullScan_RemovesDeleted(t *testing.T) {
	dir := t.TempDir()
	store := tempStore(t)
	logger := log.New(os.Stderr, "[test] ", log.LstdFlags)

	path := filepath.Join(dir, "temp.md")
	os.WriteFile(path, []byte("temporary content"), 0644)

	indexer := NewIndexer(store, IndexerConfig{
		WorkspaceRoot: dir,
		IndexGoSource: false,
		WatchEnabled:  false,
	}, nil, logger)

	indexed, _ := indexer.FullScan()
	if indexed != 1 {
		t.Errorf("expected 1 indexed, got %d", indexed)
	}

	// Delete the file and rescan
	os.Remove(path)
	_, removed := indexer.FullScan()
	if removed != 1 {
		t.Errorf("expected 1 removed, got %d", removed)
	}
}

func TestIndexer_StateSync(t *testing.T) {
	dir := t.TempDir()
	store := tempStore(t)
	logger := log.New(os.Stderr, "[test] ", log.LstdFlags)

	state := &mockStateProvider{
		notes: []SessionNoteData{
			{ID: 1, Author: "cursor", Content: "Use FTS5 for knowledge search", Category: "decision"},
		},
		tasks: []TaskData{
			{ID: 10, Title: "Add auth", Description: "Implement JWT", AssignedTo: "claude-code", ResultSummary: "Done"},
		},
	}

	indexer := NewIndexer(store, IndexerConfig{
		WorkspaceRoot: dir,
		IndexGoSource: false,
		WatchEnabled:  false,
	}, state, logger)

	indexer.RunOnce()

	// Query for session note
	results, _ := store.Query("FTS5 knowledge", "", 10)
	if len(results) == 0 {
		t.Error("expected results for session note query")
	}

	// Query for task
	results, _ = store.Query("JWT auth", "", 10)
	if len(results) == 0 {
		t.Error("expected results for task query")
	}
}

func TestIndexer_RunOnce(t *testing.T) {
	dir := t.TempDir()
	store := tempStore(t)
	logger := log.New(os.Stderr, "[test] ", log.LstdFlags)

	os.WriteFile(filepath.Join(dir, "doc.md"), []byte("test document"), 0644)

	state := &mockStateProvider{
		notes: []SessionNoteData{
			{ID: 1, Author: "test", Content: "test note", Category: "note"},
		},
	}

	indexer := NewIndexer(store, IndexerConfig{
		WorkspaceRoot: dir,
		IndexGoSource: false,
		WatchEnabled:  false,
	}, state, logger)

	indexed, removed := indexer.RunOnce()
	if indexed < 1 {
		t.Errorf("expected at least 1 indexed, got %d", indexed)
	}
	if removed != 0 {
		t.Errorf("expected 0 removed, got %d", removed)
	}

	// Both file and note should be queryable
	paths, _ := store.IndexedPaths()
	if len(paths) < 2 {
		t.Errorf("expected at least 2 indexed paths (file + note), got %d: %v", len(paths), paths)
	}
}
