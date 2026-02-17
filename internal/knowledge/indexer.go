package knowledge

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// StateProvider is an interface for pulling session notes and tasks from the
// collaboration service. This avoids a direct dependency on the app package.
type StateProvider interface {
	SessionNotes() []SessionNoteData
	CompletedTasks() []TaskData
}

// SessionNoteData carries the fields needed to index a session note.
type SessionNoteData struct {
	ID       int
	Author   string
	Content  string
	Category string
}

// TaskData carries the fields needed to index a completed task.
type TaskData struct {
	ID            int
	Title         string
	Description   string
	AssignedTo    string
	ResultSummary string
}

// IndexerConfig controls what and how content is indexed.
type IndexerConfig struct {
	WorkspaceRoot     string
	IndexGoSource     bool
	WatchEnabled      bool
	StateSyncInterval time.Duration // how often to sync state items (default 60s)
}

// Indexer scans the workspace and watches for file changes, keeping the
// knowledge store up to date. It also periodically syncs session notes
// and completed tasks from the collaboration state.
type Indexer struct {
	store    *KnowledgeStore
	config   IndexerConfig
	state    StateProvider // may be nil if state sync is not configured
	logger   *log.Logger
	watcher  *fsnotify.Watcher
	mu       sync.Mutex
	debounce map[string]time.Time
}

// NewIndexer creates a new Indexer.
func NewIndexer(store *KnowledgeStore, config IndexerConfig, state StateProvider, logger *log.Logger) *Indexer {
	return &Indexer{
		store:    store,
		config:   config,
		state:    state,
		logger:   logger,
		debounce: make(map[string]time.Time),
	}
}

// Start runs the indexer: performs a full scan, then watches for changes.
// Blocks until ctx is cancelled.
func (idx *Indexer) Start(ctx context.Context) {
	idx.logger.Println("Knowledge indexer: starting full scan...")
	start := time.Now()
	indexed, removed := idx.FullScan()
	idx.logger.Printf("Knowledge indexer: full scan done in %s (indexed=%d, removed=%d)", time.Since(start).Round(time.Millisecond), indexed, removed)

	// Sync state items immediately
	if idx.state != nil {
		idx.syncState()
	}

	// Start file watcher if enabled
	if idx.config.WatchEnabled {
		if err := idx.startWatcher(ctx); err != nil {
			idx.logger.Printf("Knowledge indexer: file watcher failed: %v (polling disabled)", err)
		}
	}

	// Periodic state sync loop
	stateSyncInterval := idx.config.StateSyncInterval
	if stateSyncInterval <= 0 {
		stateSyncInterval = 60 * time.Second
	}
	stateTicker := time.NewTicker(stateSyncInterval)
	defer stateTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			idx.stopWatcher()
			idx.logger.Println("Knowledge indexer: stopped")
			return
		case <-stateTicker.C:
			if idx.state != nil {
				idx.syncState()
			}
		}
	}
}

// RunOnce performs a one-shot full scan and state sync without watching.
// Useful for testing.
func (idx *Indexer) RunOnce() (indexed, removed int) {
	indexed, removed = idx.FullScan()
	if idx.state != nil {
		idx.syncState()
	}
	return
}

// FullScan walks the workspace and indexes all eligible files.
// It also removes entries for files that no longer exist.
func (idx *Indexer) FullScan() (indexed, removed int) {
	root := idx.config.WorkspaceRoot
	if root == "" {
		return 0, 0
	}

	// Collect currently indexed file paths to detect deletions
	existingPaths, _ := idx.store.IndexedPaths()
	existing := make(map[string]bool, len(existingPaths))
	for _, p := range existingPaths {
		// Only track file paths (not state references like session_note:42)
		if !strings.Contains(p, ":") {
			existing[p] = true
		}
	}

	seen := make(map[string]bool)

	walkFn := func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		// Skip hidden directories
		if info.IsDir() {
			base := filepath.Base(path)
			if strings.HasPrefix(base, ".") || base == "vendor" || base == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}

		if !ShouldIndex(path, idx.config.IndexGoSource) {
			return nil
		}

		doc, err := ParseFile(path, root)
		if err != nil {
			idx.logger.Printf("Knowledge indexer: parse error %s: %v", path, err)
			return nil
		}

		changed, err := idx.store.IndexIfChanged(doc)
		if err != nil {
			idx.logger.Printf("Knowledge indexer: index error %s: %v", path, err)
			return nil
		}

		seen[doc.Path] = true
		if changed {
			indexed++
		}
		return nil
	}

	if err := filepath.Walk(root, walkFn); err != nil {
		idx.logger.Printf("Knowledge indexer: walk error: %v", err)
	}

	// Remove entries for deleted files
	for p := range existing {
		if !seen[p] {
			if err := idx.store.Remove(p); err != nil {
				idx.logger.Printf("Knowledge indexer: remove error %s: %v", p, err)
			} else {
				removed++
			}
		}
	}

	return indexed, removed
}

// syncState indexes session notes and completed tasks from the collaboration state.
func (idx *Indexer) syncState() {
	if idx.state == nil {
		return
	}

	notes := idx.state.SessionNotes()
	for _, n := range notes {
		doc := FormatSessionNote(n.ID, n.Author, n.Content, n.Category)
		if _, err := idx.store.IndexIfChanged(doc); err != nil {
			idx.logger.Printf("Knowledge indexer: note index error %d: %v", n.ID, err)
		}
	}

	tasks := idx.state.CompletedTasks()
	for _, t := range tasks {
		doc := FormatTaskSummary(t.ID, t.Title, t.Description, t.AssignedTo, t.ResultSummary)
		if _, err := idx.store.IndexIfChanged(doc); err != nil {
			idx.logger.Printf("Knowledge indexer: task index error %d: %v", t.ID, err)
		}
	}
}

// startWatcher sets up fsnotify watching on the workspace root.
func (idx *Indexer) startWatcher(ctx context.Context) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	idx.watcher = w

	root := idx.config.WorkspaceRoot

	// Add workspace root
	if err := w.Add(root); err != nil {
		idx.logger.Printf("Knowledge indexer: watch root error: %v", err)
	}

	// Add docs/ subdirectory if it exists
	docsDir := filepath.Join(root, "docs")
	if info, err := os.Stat(docsDir); err == nil && info.IsDir() {
		if err := w.Add(docsDir); err != nil {
			idx.logger.Printf("Knowledge indexer: watch docs error: %v", err)
		}
	}

	// Add internal/ subdirectories for Go source
	if idx.config.IndexGoSource {
		internalDir := filepath.Join(root, "internal")
		if info, err := os.Stat(internalDir); err == nil && info.IsDir() {
			filepath.Walk(internalDir, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return nil
				}
				if info.IsDir() {
					base := filepath.Base(path)
					if strings.HasPrefix(base, ".") {
						return filepath.SkipDir
					}
					_ = w.Add(path)
				}
				return nil
			})
		}
		cmdDir := filepath.Join(root, "cmd")
		if info, err := os.Stat(cmdDir); err == nil && info.IsDir() {
			filepath.Walk(cmdDir, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return nil
				}
				if info.IsDir() {
					_ = w.Add(path)
				}
				return nil
			})
		}
	}

	go idx.watchLoop(ctx)
	return nil
}

// watchLoop processes fsnotify events with debouncing.
func (idx *Indexer) watchLoop(ctx context.Context) {
	const debounceWindow = 2 * time.Second

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-idx.watcher.Events:
			if !ok {
				return
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) == 0 {
				continue
			}

			path := event.Name
			if !ShouldIndex(path, idx.config.IndexGoSource) {
				continue
			}

			// Debounce: skip if we indexed this file recently
			idx.mu.Lock()
			if last, ok := idx.debounce[path]; ok && time.Since(last) < debounceWindow {
				idx.mu.Unlock()
				continue
			}
			idx.debounce[path] = time.Now()
			idx.mu.Unlock()

			if event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
				relPath, _ := filepath.Rel(idx.config.WorkspaceRoot, path)
				if relPath == "" {
					relPath = path
				}
				if err := idx.store.Remove(relPath); err != nil {
					idx.logger.Printf("Knowledge indexer: remove on delete %s: %v", relPath, err)
				}
				continue
			}

			doc, err := ParseFile(path, idx.config.WorkspaceRoot)
			if err != nil {
				continue
			}
			if changed, err := idx.store.IndexIfChanged(doc); err != nil {
				idx.logger.Printf("Knowledge indexer: re-index %s: %v", path, err)
			} else if changed {
				idx.logger.Printf("Knowledge indexer: re-indexed %s", doc.Path)
			}

		case err, ok := <-idx.watcher.Errors:
			if !ok {
				return
			}
			idx.logger.Printf("Knowledge indexer: watcher error: %v", err)
		}
	}
}

// stopWatcher closes the fsnotify watcher if active.
func (idx *Indexer) stopWatcher() {
	if idx.watcher != nil {
		idx.watcher.Close()
	}
}
