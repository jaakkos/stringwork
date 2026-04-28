package sqlite

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/jaakkos/stringwork/internal/app"
	"github.com/jaakkos/stringwork/internal/domain"
)

// openSQLiteRaw opens the same database file the production code uses,
// but without running the package's migrations. Tests that want to
// inspect or seed pre-migration state use this directly.
func openSQLiteRaw(path string) (*sql.DB, error) {
	return sql.Open("sqlite", path+"?_journal_mode=WAL&_busy_timeout=5000")
}

func TestStoreRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.sqlite")

	store, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() {
		if c, ok := store.(*Store); ok {
			_ = c.Close()
		}
	}()

	state := domain.NewCollabState()
	state.Messages = append(state.Messages, domain.Message{
		ID: 1, From: "cursor", To: "claude-code", Content: "hello", Timestamp: time.Now(), Read: false,
	})
	state.Tasks = append(state.Tasks, domain.Task{
		ID: 1, Title: "Test", Description: "Desc", Status: "pending", AssignedTo: "any",
		CreatedBy: "cursor", CreatedAt: time.Now(), UpdatedAt: time.Now(), Priority: 3,
	})
	state.NextMsgID = 2
	state.NextTaskID = 2

	if err := store.Save(state); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Messages) != 1 {
		t.Errorf("len(Messages) = %d, want 1", len(loaded.Messages))
	} else if loaded.Messages[0].Content != "hello" {
		t.Errorf("Messages[0].Content = %q, want \"hello\"", loaded.Messages[0].Content)
	}
	if len(loaded.Tasks) != 1 {
		t.Errorf("len(Tasks) = %d, want 1", len(loaded.Tasks))
	} else if loaded.Tasks[0].Title != "Test" {
		t.Errorf("Tasks[0].Title = %q, want \"Test\"", loaded.Tasks[0].Title)
	}
	if loaded.NextMsgID != 2 || loaded.NextTaskID != 2 {
		t.Errorf("NextMsgID=%d NextTaskID=%d, want 2, 2", loaded.NextMsgID, loaded.NextTaskID)
	}
}

func TestStoreClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "closed.sqlite")

	store, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	st := store.(*Store)
	if err := st.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if st.db != nil {
		t.Error("Close should set db to nil")
	}
	// Second Close is no-op
	if err := st.Close(); err != nil {
		t.Errorf("Second Close: %v", err)
	}
}

func TestSelfHealingIDs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "heal.sqlite")

	store, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() {
		if c, ok := store.(*Store); ok {
			_ = c.Close()
		}
	}()

	now := time.Now()

	// Save state with counters intentionally behind the actual MAX(id).
	// This simulates the drift bug: meta says next_msg_id=5 but message ID 10 exists.
	state := domain.NewCollabState()
	state.Messages = append(state.Messages,
		domain.Message{ID: 3, From: "a", To: "b", Content: "m1", Timestamp: now},
		domain.Message{ID: 7, From: "a", To: "b", Content: "m2", Timestamp: now},
		domain.Message{ID: 10, From: "a", To: "b", Content: "m3", Timestamp: now},
	)
	state.Tasks = append(state.Tasks,
		domain.Task{ID: 2, Title: "t1", Status: "pending", AssignedTo: "any", CreatedBy: "a", CreatedAt: now, UpdatedAt: now, Priority: 3},
		domain.Task{ID: 8, Title: "t2", Status: "done", AssignedTo: "any", CreatedBy: "a", CreatedAt: now, UpdatedAt: now, Priority: 3},
	)
	state.SessionNotes = append(state.SessionNotes,
		domain.SessionNote{ID: 4, Author: "a", Content: "n1", Category: "note", Timestamp: now},
	)

	// Set counters to values BEHIND the actual data (simulating the bug)
	state.NextMsgID = 5  // should be 11 (max=10, so 10+1)
	state.NextTaskID = 3 // should be 9  (max=8, so 8+1)
	state.NextNoteID = 2 // should be 5  (max=4, so 4+1)

	if err := store.Save(state); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Load should self-heal the counters
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.NextMsgID != 11 {
		t.Errorf("NextMsgID = %d, want 11 (self-healed from stale counter 5)", loaded.NextMsgID)
	}
	if loaded.NextTaskID != 9 {
		t.Errorf("NextTaskID = %d, want 9 (self-healed from stale counter 3)", loaded.NextTaskID)
	}
	if loaded.NextNoteID != 5 {
		t.Errorf("NextNoteID = %d, want 5 (self-healed from stale counter 2)", loaded.NextNoteID)
	}
}

func TestSelfHealingIDs_CorrectCountersUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "correct.sqlite")

	store, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() {
		if c, ok := store.(*Store); ok {
			_ = c.Close()
		}
	}()

	now := time.Now()
	state := domain.NewCollabState()
	state.Messages = append(state.Messages,
		domain.Message{ID: 1, From: "a", To: "b", Content: "m1", Timestamp: now},
	)
	state.NextMsgID = 2  // already correct
	state.NextTaskID = 1 // no tasks, already correct
	state.NextNoteID = 1 // no notes, already correct

	if err := store.Save(state); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.NextMsgID != 2 {
		t.Errorf("NextMsgID = %d, want 2 (should remain unchanged when correct)", loaded.NextMsgID)
	}
	if loaded.NextTaskID != 1 {
		t.Errorf("NextTaskID = %d, want 1 (should remain unchanged when correct)", loaded.NextTaskID)
	}
	if loaded.NextNoteID != 1 {
		t.Errorf("NextNoteID = %d, want 1 (should remain unchanged when correct)", loaded.NextNoteID)
	}
}

func TestSelfHealingIDs_EmptyState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.sqlite")

	store, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() {
		if c, ok := store.(*Store); ok {
			_ = c.Close()
		}
	}()

	state := domain.NewCollabState()
	// Empty state, counters at 1
	if err := store.Save(state); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.NextMsgID != 1 {
		t.Errorf("NextMsgID = %d, want 1 (empty state)", loaded.NextMsgID)
	}
	if loaded.NextTaskID != 1 {
		t.Errorf("NextTaskID = %d, want 1 (empty state)", loaded.NextTaskID)
	}
	if loaded.NextNoteID != 1 {
		t.Errorf("NextNoteID = %d, want 1 (empty state)", loaded.NextNoteID)
	}
}

// ========== Audit log store tests ==========

func TestWriteAudit_Basic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.sqlite")
	store, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = store.(*Store).Close() }()

	st := store.(*Store)
	entry := domain.AuditEntry{
		Timestamp:   time.Now(),
		Agent:       "cursor",
		ToolName:    "create_task",
		ArgsSummary: `{"title":"test"}`,
		DurationMs:  42,
		SessionID:   "sess-1",
	}

	if err := st.WriteAudit(entry); err != nil {
		t.Fatalf("WriteAudit: %v", err)
	}

	entries, err := st.ReadAudit(app.AuditFilter{})
	if err != nil {
		t.Fatalf("ReadAudit: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Agent != "cursor" {
		t.Errorf("Agent = %q, want cursor", entries[0].Agent)
	}
	if entries[0].ToolName != "create_task" {
		t.Errorf("ToolName = %q, want create_task", entries[0].ToolName)
	}
	if entries[0].DurationMs != 42 {
		t.Errorf("DurationMs = %d, want 42", entries[0].DurationMs)
	}
	if entries[0].SessionID != "sess-1" {
		t.Errorf("SessionID = %q, want sess-1", entries[0].SessionID)
	}
}

func TestWriteAudit_WithError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.sqlite")
	store, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = store.(*Store).Close() }()

	st := store.(*Store)
	entry := domain.AuditEntry{
		Timestamp:  time.Now(),
		Agent:      "claude-code",
		ToolName:   "update_task",
		DurationMs: 10,
		Error:      "task not found",
	}
	if err := st.WriteAudit(entry); err != nil {
		t.Fatalf("WriteAudit: %v", err)
	}

	entries, err := st.ReadAudit(app.AuditFilter{})
	if err != nil {
		t.Fatalf("ReadAudit: %v", err)
	}
	if entries[0].Error != "task not found" {
		t.Errorf("Error = %q, want 'task not found'", entries[0].Error)
	}
}

func TestReadAudit_FilterByAgent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.sqlite")
	store, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = store.(*Store).Close() }()

	st := store.(*Store)
	now := time.Now()
	for _, agent := range []string{"cursor", "claude-code", "cursor", "codex"} {
		_ = st.WriteAudit(domain.AuditEntry{Timestamp: now, Agent: agent, ToolName: "test", DurationMs: 1})
	}

	entries, err := st.ReadAudit(app.AuditFilter{Agent: "cursor"})
	if err != nil {
		t.Fatalf("ReadAudit: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 cursor entries, got %d", len(entries))
	}
	for _, e := range entries {
		if e.Agent != "cursor" {
			t.Errorf("unexpected agent %q in filtered results", e.Agent)
		}
	}
}

func TestReadAudit_FilterByToolName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.sqlite")
	store, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = store.(*Store).Close() }()

	st := store.(*Store)
	now := time.Now()
	for _, tool := range []string{"create_task", "list_tasks", "create_task", "heartbeat"} {
		_ = st.WriteAudit(domain.AuditEntry{Timestamp: now, Agent: "a", ToolName: tool, DurationMs: 1})
	}

	entries, err := st.ReadAudit(app.AuditFilter{ToolName: "create_task"})
	if err != nil {
		t.Fatalf("ReadAudit: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 create_task entries, got %d", len(entries))
	}
}

func TestReadAudit_DefaultLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.sqlite")
	store, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = store.(*Store).Close() }()

	st := store.(*Store)
	now := time.Now()
	for i := 0; i < 5; i++ {
		_ = st.WriteAudit(domain.AuditEntry{Timestamp: now.Add(time.Duration(i) * time.Second), Agent: "a", ToolName: "t", DurationMs: 1})
	}

	entries, err := st.ReadAudit(app.AuditFilter{})
	if err != nil {
		t.Fatalf("ReadAudit: %v", err)
	}
	if len(entries) != 5 {
		t.Errorf("expected 5 entries with default limit, got %d", len(entries))
	}
}

func TestReadAudit_CustomLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.sqlite")
	store, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = store.(*Store).Close() }()

	st := store.(*Store)
	now := time.Now()
	for i := 0; i < 10; i++ {
		_ = st.WriteAudit(domain.AuditEntry{Timestamp: now.Add(time.Duration(i) * time.Second), Agent: "a", ToolName: "t", DurationMs: 1})
	}

	entries, err := st.ReadAudit(app.AuditFilter{Limit: 3})
	if err != nil {
		t.Fatalf("ReadAudit: %v", err)
	}
	if len(entries) != 3 {
		t.Errorf("expected 3 entries with limit=3, got %d", len(entries))
	}
}

func TestReadAudit_FilterByTimeRange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.sqlite")
	store, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = store.(*Store).Close() }()

	st := store.(*Store)
	base := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

	_ = st.WriteAudit(domain.AuditEntry{Timestamp: base, Agent: "a", ToolName: "t1", DurationMs: 1})
	_ = st.WriteAudit(domain.AuditEntry{Timestamp: base.Add(1 * time.Hour), Agent: "a", ToolName: "t2", DurationMs: 1})
	_ = st.WriteAudit(domain.AuditEntry{Timestamp: base.Add(2 * time.Hour), Agent: "a", ToolName: "t3", DurationMs: 1})
	_ = st.WriteAudit(domain.AuditEntry{Timestamp: base.Add(3 * time.Hour), Agent: "a", ToolName: "t4", DurationMs: 1})

	entries, err := st.ReadAudit(app.AuditFilter{
		From: base.Add(30 * time.Minute),
		To:   base.Add(2*time.Hour + 30*time.Minute),
	})
	if err != nil {
		t.Fatalf("ReadAudit: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 entries in time range, got %d", len(entries))
	}
}

func TestPruneAudit_RemovesOldEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.sqlite")
	store, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = store.(*Store).Close() }()

	st := store.(*Store)
	now := time.Now()

	_ = st.WriteAudit(domain.AuditEntry{Timestamp: now.Add(-10 * 24 * time.Hour), Agent: "a", ToolName: "old1", DurationMs: 1})
	_ = st.WriteAudit(domain.AuditEntry{Timestamp: now.Add(-8 * 24 * time.Hour), Agent: "a", ToolName: "old2", DurationMs: 1})
	_ = st.WriteAudit(domain.AuditEntry{Timestamp: now.Add(-1 * 24 * time.Hour), Agent: "a", ToolName: "recent1", DurationMs: 1})
	_ = st.WriteAudit(domain.AuditEntry{Timestamp: now, Agent: "a", ToolName: "recent2", DurationMs: 1})

	pruned, err := st.PruneAudit(now.Add(-7 * 24 * time.Hour))
	if err != nil {
		t.Fatalf("PruneAudit: %v", err)
	}
	if pruned != 2 {
		t.Errorf("pruned = %d, want 2", pruned)
	}

	remaining, err := st.ReadAudit(app.AuditFilter{})
	if err != nil {
		t.Fatalf("ReadAudit: %v", err)
	}
	if len(remaining) != 2 {
		t.Errorf("expected 2 remaining entries, got %d", len(remaining))
	}
}

func TestReadAudit_EmptyTable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.sqlite")
	store, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = store.(*Store).Close() }()

	entries, err := store.(*Store).ReadAudit(app.AuditFilter{})
	if err != nil {
		t.Fatalf("ReadAudit: %v", err)
	}
	if entries != nil {
		t.Errorf("expected nil for empty table, got %d entries", len(entries))
	}
}

func TestNew_failsOnInvalidDir(t *testing.T) {
	// Parent path is a file (e.g. /dev/null), so MkdirAll fails
	path := filepath.Join(os.DevNull, "sub", "state.sqlite")
	_, err := New(path)
	if err == nil {
		t.Error("New should fail when parent is not a directory")
	}
}

// TestMigration_AddsLastSpawnedAtColumn proves the runtime migration
// adds last_spawned_at to a pre-existing agent_instances table that
// pre-dates the column. Regression guard for MUST_FIX #3a — the kill-
// respawn loop diagnosed during the codex review (claude-code-task-32)
// was caused by AgentInstance rows reloading with zero LastSpawnedAt
// because the column did not exist on disk, even though the runtime
// struct had the field.
func TestMigration_AddsLastSpawnedAtColumn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "premigrate.sqlite")

	// Create a database with a pre-migration agent_instances table that
	// lacks last_spawned_at. We use the raw sqlite driver to skip the
	// store's New() (which would run migrations and add the column).
	rawDB, err := openSQLiteRaw(path)
	if err != nil {
		t.Fatalf("openSQLiteRaw: %v", err)
	}
	if _, err := rawDB.Exec(`
CREATE TABLE agent_instances (
	instance_id TEXT PRIMARY KEY,
	agent_type TEXT NOT NULL,
	role TEXT NOT NULL,
	capabilities TEXT NOT NULL DEFAULT '[]',
	max_tasks INTEGER NOT NULL DEFAULT 1,
	status TEXT NOT NULL DEFAULT 'offline',
	current_tasks TEXT NOT NULL DEFAULT '[]',
	workspace TEXT NOT NULL DEFAULT '',
	last_heartbeat TEXT NOT NULL
)`); err != nil {
		t.Fatalf("create pre-migration table: %v", err)
	}
	if err := rawDB.Close(); err != nil {
		t.Fatalf("close raw db: %v", err)
	}

	store, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = store.(*Store).Close() }()

	rawDB2, err := openSQLiteRaw(path)
	if err != nil {
		t.Fatalf("openSQLiteRaw post-migration: %v", err)
	}
	defer func() { _ = rawDB2.Close() }()

	rows, err := rawDB2.Query("PRAGMA table_info(agent_instances)")
	if err != nil {
		t.Fatalf("PRAGMA: %v", err)
	}
	defer func() { _ = rows.Close() }()

	found := false
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notnull int
			dflt    interface{}
			pk      int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if name == "last_spawned_at" {
			found = true
		}
	}
	if !found {
		t.Error("migration did not add last_spawned_at column to agent_instances")
	}
}

// TestSaveLoad_PreservesLastSpawnedAt verifies the timestamp round-trips
// through the SQLite store. Pairs with TestMigration_AddsLastSpawnedAtColumn
// to lock in MUST_FIX #3a end-to-end.
func TestSaveLoad_PreservesLastSpawnedAt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "spawn.sqlite")

	store, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = store.(*Store).Close() }()

	now := time.Now().UTC().Truncate(time.Microsecond)
	state := domain.NewCollabState()
	state.AgentInstances["claude-code-task-99"] = &domain.AgentInstance{
		InstanceID:    "claude-code-task-99",
		AgentType:     "claude-code",
		Role:          domain.RoleWorker,
		MaxTasks:      1,
		Status:        "idle",
		CurrentTasks:  []int{},
		LastHeartbeat: now,
		LastSpawnedAt: now.Add(-2 * time.Minute),
	}

	if err := store.Save(state); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	got, ok := loaded.AgentInstances["claude-code-task-99"]
	if !ok {
		t.Fatal("instance row missing after reload")
	}
	expected := now.Add(-2 * time.Minute)
	if !got.LastSpawnedAt.Equal(expected) {
		t.Errorf("LastSpawnedAt = %v, want %v", got.LastSpawnedAt, expected)
	}
}

// TestSaveLoad_ZeroLastSpawnedAtRoundTrips covers the edge case where
// LastSpawnedAt has never been set: the empty-string default in the
// schema must round-trip as time.Time{} rather than poisoning Load().
func TestSaveLoad_ZeroLastSpawnedAtRoundTrips(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "zero-spawn.sqlite")

	store, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = store.(*Store).Close() }()

	state := domain.NewCollabState()
	state.AgentInstances["claude-code"] = &domain.AgentInstance{
		InstanceID:    "claude-code",
		AgentType:     "claude-code",
		Role:          domain.RoleWorker,
		MaxTasks:      1,
		Status:        "offline",
		CurrentTasks:  []int{},
		LastHeartbeat: time.Now(),
	}

	if err := store.Save(state); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, ok := loaded.AgentInstances["claude-code"]
	if !ok {
		t.Fatal("instance row missing after reload")
	}
	if !got.LastSpawnedAt.IsZero() {
		t.Errorf("zero LastSpawnedAt did not round-trip cleanly: %v", got.LastSpawnedAt)
	}
}
