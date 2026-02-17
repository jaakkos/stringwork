package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/jaakkos/stringwork/internal/app"
	"github.com/jaakkos/stringwork/internal/domain"
)

const schema = `
CREATE TABLE IF NOT EXISTS messages (
	id INTEGER PRIMARY KEY,
	from_agent TEXT NOT NULL,
	to_agent TEXT NOT NULL,
	content TEXT NOT NULL,
	timestamp TEXT NOT NULL,
	read_flag INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS tasks (
	id INTEGER PRIMARY KEY,
	title TEXT NOT NULL,
	description TEXT NOT NULL,
	status TEXT NOT NULL,
	assigned_to TEXT NOT NULL,
	created_by TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	priority INTEGER NOT NULL DEFAULT 3,
	blocked_by TEXT NOT NULL DEFAULT '',
	dependencies TEXT NOT NULL DEFAULT '[]',
	context_id TEXT NOT NULL DEFAULT '',
	worker_type TEXT NOT NULL DEFAULT '',
	capabilities TEXT NOT NULL DEFAULT '[]',
	result_summary TEXT NOT NULL DEFAULT '',
	expected_duration_sec INTEGER NOT NULL DEFAULT 0,
	progress_description TEXT NOT NULL DEFAULT '',
	progress_percent INTEGER NOT NULL DEFAULT 0,
	last_progress_at TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS agent_instances (
	instance_id TEXT PRIMARY KEY,
	agent_type TEXT NOT NULL,
	role TEXT NOT NULL,
	capabilities TEXT NOT NULL DEFAULT '[]',
	max_tasks INTEGER NOT NULL DEFAULT 1,
	status TEXT NOT NULL DEFAULT 'offline',
	current_tasks TEXT NOT NULL DEFAULT '[]',
	workspace TEXT NOT NULL DEFAULT '',
	last_heartbeat TEXT NOT NULL,
	progress TEXT NOT NULL DEFAULT '',
	progress_step INTEGER NOT NULL DEFAULT 0,
	progress_total_steps INTEGER NOT NULL DEFAULT 0,
	progress_updated_at TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS work_contexts (
	id TEXT PRIMARY KEY,
	task_id INTEGER NOT NULL,
	relevant_files TEXT NOT NULL DEFAULT '[]',
	background TEXT NOT NULL DEFAULT '',
	constraints TEXT NOT NULL DEFAULT '[]',
	shared_notes TEXT NOT NULL DEFAULT '{}',
	parent_ctx_id TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS presence (
	agent TEXT PRIMARY KEY,
	status TEXT NOT NULL,
	current_task_id INTEGER NOT NULL DEFAULT 0,
	note TEXT NOT NULL DEFAULT '',
	workspace TEXT NOT NULL DEFAULT '',
	last_seen TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS session_notes (
	id INTEGER PRIMARY KEY,
	author TEXT NOT NULL,
	content TEXT NOT NULL,
	category TEXT NOT NULL,
	timestamp TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS plans (
	id TEXT PRIMARY KEY,
	title TEXT NOT NULL,
	goal TEXT NOT NULL,
	context TEXT NOT NULL,
	created_by TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	status TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS plan_items (
	plan_id TEXT NOT NULL,
	item_id TEXT NOT NULL,
	title TEXT NOT NULL,
	description TEXT NOT NULL,
	reasoning TEXT NOT NULL DEFAULT '',
	acceptance TEXT NOT NULL DEFAULT '[]',
	constraints TEXT NOT NULL DEFAULT '[]',
	status TEXT NOT NULL,
	owner TEXT NOT NULL,
	dependencies TEXT NOT NULL DEFAULT '[]',
	blockers TEXT NOT NULL DEFAULT '[]',
	notes TEXT NOT NULL DEFAULT '[]',
	priority INTEGER NOT NULL DEFAULT 2,
	updated_by TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	PRIMARY KEY (plan_id, item_id),
	FOREIGN KEY (plan_id) REFERENCES plans(id)
);
CREATE TABLE IF NOT EXISTS agent_contexts (
	agent TEXT PRIMARY KEY,
	last_checked_msg_id INTEGER NOT NULL DEFAULT 0,
	last_checked_task_id INTEGER NOT NULL DEFAULT 0,
	last_check_time TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS file_locks (
	path TEXT PRIMARY KEY,
	locked_by TEXT NOT NULL,
	reason TEXT NOT NULL,
	locked_at TEXT NOT NULL,
	expires_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS registered_agents (
	name TEXT PRIMARY KEY,
	display_name TEXT NOT NULL DEFAULT '',
	capabilities TEXT NOT NULL DEFAULT '[]',
	workspace TEXT NOT NULL DEFAULT '',
	project TEXT NOT NULL DEFAULT '',
	registered_at TEXT NOT NULL,
	last_seen TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS meta (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
`

// migrations add columns/tables that may not exist in older databases.
// Errors are ignored when the column/table already exists.

// indexes for common query patterns (read_messages, list_tasks, notifications)
const indexes = `
CREATE INDEX IF NOT EXISTS idx_messages_to_read ON messages(to_agent, read_flag);
CREATE INDEX IF NOT EXISTS idx_tasks_status_assigned ON tasks(status, assigned_to);
`

// Store implements app.StateRepository using SQLite.
type Store struct {
	db *sql.DB
}

// New opens the SQLite database at path (creating parent dirs and schema) and returns a StateRepository.
func New(path string) (app.StateRepository, error) {
	dir := filepath.Dir(path)
	if dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("sqlite mkdir: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("sqlite open: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite schema: %w", err)
	}
	if _, err := db.Exec(indexes); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite indexes: %w", err)
	}
	// Run migrations for existing databases (ignore errors for already-applied migrations).
	_ = runMigrations(db)
	return &Store{db: db}, nil
}

// runMigrations applies schema migrations for older databases. Errors are
// silently ignored because some may already be applied.
func runMigrations(db *sql.DB) error {
	_, _ = db.Exec("ALTER TABLE presence ADD COLUMN workspace TEXT NOT NULL DEFAULT ''")
	_, _ = db.Exec("ALTER TABLE tasks ADD COLUMN context_id TEXT NOT NULL DEFAULT ''")
	_, _ = db.Exec("ALTER TABLE tasks ADD COLUMN worker_type TEXT NOT NULL DEFAULT ''")
	_, _ = db.Exec("ALTER TABLE tasks ADD COLUMN capabilities TEXT NOT NULL DEFAULT '[]'")
	_, _ = db.Exec("ALTER TABLE tasks ADD COLUMN result_summary TEXT NOT NULL DEFAULT ''")
	_, _ = db.Exec("ALTER TABLE tasks ADD COLUMN expected_duration_sec INTEGER NOT NULL DEFAULT 0")
	_, _ = db.Exec("ALTER TABLE tasks ADD COLUMN progress_description TEXT NOT NULL DEFAULT ''")
	_, _ = db.Exec("ALTER TABLE tasks ADD COLUMN progress_percent INTEGER NOT NULL DEFAULT 0")
	_, _ = db.Exec("ALTER TABLE tasks ADD COLUMN last_progress_at TEXT NOT NULL DEFAULT ''")
	_, _ = db.Exec(schemaAgentInstances)
	_, _ = db.Exec("ALTER TABLE agent_instances ADD COLUMN progress TEXT NOT NULL DEFAULT ''")
	_, _ = db.Exec("ALTER TABLE agent_instances ADD COLUMN progress_step INTEGER NOT NULL DEFAULT 0")
	_, _ = db.Exec("ALTER TABLE agent_instances ADD COLUMN progress_total_steps INTEGER NOT NULL DEFAULT 0")
	_, _ = db.Exec("ALTER TABLE agent_instances ADD COLUMN progress_updated_at TEXT NOT NULL DEFAULT ''")
	_, _ = db.Exec(schemaWorkContexts)
	_, _ = db.Exec(schemaRegisteredAgents)
	return nil
}

const schemaRegisteredAgents = `
CREATE TABLE IF NOT EXISTS registered_agents (
	name TEXT PRIMARY KEY,
	display_name TEXT NOT NULL DEFAULT '',
	capabilities TEXT NOT NULL DEFAULT '[]',
	workspace TEXT NOT NULL DEFAULT '',
	project TEXT NOT NULL DEFAULT '',
	registered_at TEXT NOT NULL,
	last_seen TEXT NOT NULL
)`

const schemaAgentInstances = `
CREATE TABLE IF NOT EXISTS agent_instances (
	instance_id TEXT PRIMARY KEY,
	agent_type TEXT NOT NULL,
	role TEXT NOT NULL,
	capabilities TEXT NOT NULL DEFAULT '[]',
	max_tasks INTEGER NOT NULL DEFAULT 1,
	status TEXT NOT NULL DEFAULT 'offline',
	current_tasks TEXT NOT NULL DEFAULT '[]',
	workspace TEXT NOT NULL DEFAULT '',
	last_heartbeat TEXT NOT NULL
)`
const schemaWorkContexts = `
CREATE TABLE IF NOT EXISTS work_contexts (
	id TEXT PRIMARY KEY,
	task_id INTEGER NOT NULL,
	relevant_files TEXT NOT NULL DEFAULT '[]',
	background TEXT NOT NULL DEFAULT '',
	constraints TEXT NOT NULL DEFAULT '[]',
	shared_notes TEXT NOT NULL DEFAULT '{}',
	parent_ctx_id TEXT NOT NULL DEFAULT ''
)`

// Close releases the database connection. Call on shutdown for clean exit.
func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

// parseTime parses RFC3339Nano or returns zero time and error.
func parseTime(s, context string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s: parse timestamp %q: %w", context, s, err)
	}
	return t, nil
}

// isNoSuchTableErr returns true if the error indicates the table doesn't exist.
func isNoSuchTableErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such table")
}

// parseJSON unmarshals b into v or returns error with context.
func parseJSON(b []byte, v interface{}, context string) error {
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w", context, err)
	}
	return nil
}

// Load implements app.StateRepository.
func (s *Store) Load() (*domain.CollabState, error) {
	state := domain.NewCollabState()

	rows, err := s.db.Query("SELECT key, value FROM meta")
	if err != nil {
		return nil, fmt.Errorf("meta: %w", err)
	}
	meta := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			_ = rows.Close()
			return nil, err
		}
		meta[k] = v
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("meta iteration: %w", err)
	}
	if v, ok := meta["next_msg_id"]; ok {
		if _, err := fmt.Sscanf(v, "%d", &state.NextMsgID); err != nil {
			return nil, fmt.Errorf("meta next_msg_id %q: %w", v, err)
		}
	}
	if v, ok := meta["next_task_id"]; ok {
		if _, err := fmt.Sscanf(v, "%d", &state.NextTaskID); err != nil {
			return nil, fmt.Errorf("meta next_task_id %q: %w", v, err)
		}
	}
	if v, ok := meta["next_note_id"]; ok {
		if _, err := fmt.Sscanf(v, "%d", &state.NextNoteID); err != nil {
			return nil, fmt.Errorf("meta next_note_id %q: %w", v, err)
		}
	}
	if v, ok := meta["active_plan_id"]; ok {
		state.ActivePlanID = v
	}
	if v, ok := meta["driver_id"]; ok {
		state.DriverID = v
	}

	rows, err = s.db.Query("SELECT id, from_agent, to_agent, content, timestamp, read_flag FROM messages ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("messages: %w", err)
	}
	for rows.Next() {
		var m domain.Message
		var ts string
		var readFlag int
		if err := rows.Scan(&m.ID, &m.From, &m.To, &m.Content, &ts, &readFlag); err != nil {
			_ = rows.Close()
			return nil, err
		}
		t, err := parseTime(ts, "messages")
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		m.Timestamp = t
		m.Read = readFlag != 0
		state.Messages = append(state.Messages, m)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("messages iteration: %w", err)
	}

	rows, err = s.db.Query("SELECT id, title, description, status, assigned_to, created_by, created_at, updated_at, priority, blocked_by, dependencies, context_id, worker_type, capabilities, result_summary, expected_duration_sec, progress_description, progress_percent, last_progress_at FROM tasks ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("tasks: %w", err)
	}
	for rows.Next() {
		var t domain.Task
		var ca, ua, deps, contextID, workerType, caps, resultSummary, progressDesc, lastProgressAt string
		if err := rows.Scan(&t.ID, &t.Title, &t.Description, &t.Status, &t.AssignedTo, &t.CreatedBy, &ca, &ua, &t.Priority, &t.BlockedBy, &deps, &contextID, &workerType, &caps, &resultSummary, &t.ExpectedDurationSec, &progressDesc, &t.ProgressPercent, &lastProgressAt); err != nil {
			_ = rows.Close()
			return nil, err
		}
		t.ContextID = contextID
		t.WorkerType = workerType
		t.ResultSummary = resultSummary
		t.ProgressDescription = progressDesc
		var err error
		if t.CreatedAt, err = parseTime(ca, "tasks"); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if t.UpdatedAt, err = parseTime(ua, "tasks"); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if lastProgressAt != "" {
			if t.LastProgressAt, err = parseTime(lastProgressAt, "tasks last_progress_at"); err != nil {
				t.LastProgressAt = time.Time{}
			}
		}
		if err := parseJSON([]byte(deps), &t.Dependencies, "tasks dependencies"); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if caps != "" && caps != "[]" {
			_ = parseJSON([]byte(caps), &t.Capabilities, "tasks capabilities")
		}
		state.Tasks = append(state.Tasks, t)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tasks iteration: %w", err)
	}

	rows, err = s.db.Query("SELECT agent, status, current_task_id, note, workspace, last_seen FROM presence")
	if err != nil {
		return nil, fmt.Errorf("presence: %w", err)
	}
	for rows.Next() {
		var p domain.Presence
		var ls string
		if err := rows.Scan(&p.Agent, &p.Status, &p.CurrentTaskID, &p.Note, &p.Workspace, &ls); err != nil {
			_ = rows.Close()
			return nil, err
		}
		t, err := parseTime(ls, "presence")
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		p.LastSeen = t
		state.Presence[p.Agent] = &p
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("presence iteration: %w", err)
	}

	rows, err = s.db.Query("SELECT id, author, content, category, timestamp FROM session_notes ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("session_notes: %w", err)
	}
	for rows.Next() {
		var n domain.SessionNote
		var ts string
		if err := rows.Scan(&n.ID, &n.Author, &n.Content, &n.Category, &ts); err != nil {
			_ = rows.Close()
			return nil, err
		}
		t, err := parseTime(ts, "session_notes")
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		n.Timestamp = t
		state.SessionNotes = append(state.SessionNotes, n)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("session_notes iteration: %w", err)
	}

	rows, err = s.db.Query("SELECT id, title, goal, context, created_by, created_at, updated_at, status FROM plans")
	if err != nil {
		return nil, fmt.Errorf("plans: %w", err)
	}
	for rows.Next() {
		var plan domain.Plan
		var ca, ua string
		if err := rows.Scan(&plan.ID, &plan.Title, &plan.Goal, &plan.Context, &plan.CreatedBy, &ca, &ua, &plan.Status); err != nil {
			_ = rows.Close()
			return nil, err
		}
		var err error
		if plan.CreatedAt, err = parseTime(ca, "plans"); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if plan.UpdatedAt, err = parseTime(ua, "plans"); err != nil {
			_ = rows.Close()
			return nil, err
		}
		plan.Items = []domain.PlanItem{}
		state.Plans[plan.ID] = &plan
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("plans iteration: %w", err)
	}

	rows, err = s.db.Query("SELECT plan_id, item_id, title, description, reasoning, acceptance, constraints, status, owner, dependencies, blockers, notes, priority, updated_by, updated_at FROM plan_items ORDER BY plan_id, item_id")
	if err != nil {
		return nil, fmt.Errorf("plan_items: %w", err)
	}
	for rows.Next() {
		var planID string
		var item domain.PlanItem
		var ua, acc, con, deps, block, notes string
		if err := rows.Scan(&planID, &item.ID, &item.Title, &item.Description, &item.Reasoning, &acc, &con, &item.Status, &item.Owner, &deps, &block, &notes, &item.Priority, &item.UpdatedBy, &ua); err != nil {
			_ = rows.Close()
			return nil, err
		}
		t, err := parseTime(ua, "plan_items")
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		item.UpdatedAt = t
		for _, pair := range []struct {
			raw []byte
			dst interface{}
			ctx string
		}{
			{[]byte(acc), &item.Acceptance, "plan_items acceptance"},
			{[]byte(con), &item.Constraints, "plan_items constraints"},
			{[]byte(deps), &item.Dependencies, "plan_items dependencies"},
			{[]byte(block), &item.Blockers, "plan_items blockers"},
			{[]byte(notes), &item.Notes, "plan_items notes"},
		} {
			if err := parseJSON(pair.raw, pair.dst, pair.ctx); err != nil {
				_ = rows.Close()
				return nil, err
			}
		}
		if p, ok := state.Plans[planID]; ok {
			p.Items = append(p.Items, item)
		}
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("plan_items iteration: %w", err)
	}

	rows, err = s.db.Query("SELECT agent, last_checked_msg_id, last_checked_task_id, last_check_time FROM agent_contexts")
	if err != nil {
		return nil, fmt.Errorf("agent_contexts: %w", err)
	}
	for rows.Next() {
		var ac domain.AgentContext
		var t string
		if err := rows.Scan(&ac.Agent, &ac.LastCheckedMsgID, &ac.LastCheckedTaskID, &t); err != nil {
			_ = rows.Close()
			return nil, err
		}
		parsed, err := parseTime(t, "agent_contexts")
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		ac.LastCheckTime = parsed
		state.AgentContexts[ac.Agent] = &ac
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("agent_contexts iteration: %w", err)
	}

	rows, err = s.db.Query("SELECT path, locked_by, reason, locked_at, expires_at FROM file_locks")
	if err != nil {
		return nil, fmt.Errorf("file_locks: %w", err)
	}
	for rows.Next() {
		var fl domain.FileLock
		var la, ex string
		if err := rows.Scan(&fl.Path, &fl.LockedBy, &fl.Reason, &la, &ex); err != nil {
			_ = rows.Close()
			return nil, err
		}
		lat, err := parseTime(la, "file_locks locked_at")
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		fl.LockedAt = lat
		exAt, err := parseTime(ex, "file_locks expires_at")
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		fl.ExpiresAt = exAt
		state.FileLocks[fl.Path] = &fl
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("file_locks iteration: %w", err)
	}

	// --- Self-healing ID reconciliation ---
	// The NextXxxID counters in the meta table can drift out of sync with actual
	// data due to crashes, partial saves, or increment ordering bugs. Rather than
	// trusting the stored counter, we derive the correct value from the actual data.
	// This makes the system self-healing: every Load() guarantees correct counters.
	for _, m := range state.Messages {
		if m.ID >= state.NextMsgID {
			state.NextMsgID = m.ID + 1
		}
	}
	for _, t := range state.Tasks {
		if t.ID >= state.NextTaskID {
			state.NextTaskID = t.ID + 1
		}
	}
	for _, n := range state.SessionNotes {
		if n.ID >= state.NextNoteID {
			state.NextNoteID = n.ID + 1
		}
	}

	// agent_instances (table may not exist in very old DBs; only skip "no such table")
	rows, err = s.db.Query("SELECT instance_id, agent_type, role, capabilities, max_tasks, status, current_tasks, workspace, last_heartbeat, progress, progress_step, progress_total_steps, progress_updated_at FROM agent_instances")
	if err != nil && !isNoSuchTableErr(err) {
		return nil, fmt.Errorf("agent_instances: %w", err)
	}
	if err == nil {
		for rows.Next() {
			var ai domain.AgentInstance
			var caps, curTasks, lh, progressUpdAt string
			if err := rows.Scan(&ai.InstanceID, &ai.AgentType, &ai.Role, &caps, &ai.MaxTasks, &ai.Status, &curTasks, &ai.Workspace, &lh, &ai.Progress, &ai.ProgressStep, &ai.ProgressTotalSteps, &progressUpdAt); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if err := parseJSON([]byte(caps), &ai.Capabilities, "agent_instances capabilities"); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if err := parseJSON([]byte(curTasks), &ai.CurrentTasks, "agent_instances current_tasks"); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if ai.LastHeartbeat, err = parseTime(lh, "agent_instances last_heartbeat"); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if progressUpdAt != "" {
				if ai.ProgressUpdatedAt, err = parseTime(progressUpdAt, "agent_instances progress_updated_at"); err != nil {
					ai.ProgressUpdatedAt = time.Time{}
				}
			}
			state.AgentInstances[ai.InstanceID] = &ai
		}
		_ = rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("agent_instances iteration: %w", err)
		}
	}

	// work_contexts (table may not exist in very old DBs; only skip "no such table")
	rows, err = s.db.Query("SELECT id, task_id, relevant_files, background, constraints, shared_notes, parent_ctx_id FROM work_contexts")
	if err != nil && !isNoSuchTableErr(err) {
		return nil, fmt.Errorf("work_contexts: %w", err)
	}
	if err == nil {
		for rows.Next() {
			var wc domain.WorkContext
			var rf, con, sn string
			if err := rows.Scan(&wc.ID, &wc.TaskID, &rf, &wc.Background, &con, &sn, &wc.ParentCtxID); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if err := parseJSON([]byte(rf), &wc.RelevantFiles, "work_contexts relevant_files"); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if err := parseJSON([]byte(con), &wc.Constraints, "work_contexts constraints"); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if sn != "" && sn != "{}" {
				wc.SharedNotes = make(map[string]string)
				if err := parseJSON([]byte(sn), &wc.SharedNotes, "work_contexts shared_notes"); err != nil {
					_ = rows.Close()
					return nil, err
				}
			}
			state.WorkContexts[wc.ID] = &wc
		}
		_ = rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("work_contexts iteration: %w", err)
		}
	}

	// registered_agents (table may not exist in very old DBs; only skip "no such table")
	rows, err = s.db.Query("SELECT name, display_name, capabilities, workspace, project, registered_at, last_seen FROM registered_agents")
	if err != nil && !isNoSuchTableErr(err) {
		return nil, fmt.Errorf("registered_agents: %w", err)
	}
	if err == nil {
		for rows.Next() {
			var ra domain.RegisteredAgent
			var caps, regAt, ls string
			if err := rows.Scan(&ra.Name, &ra.DisplayName, &caps, &ra.Workspace, &ra.Project, &regAt, &ls); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if err := parseJSON([]byte(caps), &ra.Capabilities, "registered_agents capabilities"); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if ra.RegisteredAt, err = parseTime(regAt, "registered_agents registered_at"); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if ra.LastSeen, err = parseTime(ls, "registered_agents last_seen"); err != nil {
				_ = rows.Close()
				return nil, err
			}
			state.RegisteredAgents[ra.Name] = &ra
		}
		_ = rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("registered_agents iteration: %w", err)
		}
	}

	return state, nil
}

// Save implements app.StateRepository.
func (s *Store) Save(state *domain.CollabState) error {
	if state == nil {
		return fmt.Errorf("state is nil")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, t := range []string{"messages", "tasks", "presence", "session_notes", "plan_items", "plans", "agent_contexts", "file_locks", "agent_instances", "work_contexts", "registered_agents", "meta"} {
		if _, err := tx.Exec("DELETE FROM " + t); err != nil {
			return err
		}
	}

	meta := map[string]string{
		"next_msg_id":    fmt.Sprintf("%d", state.NextMsgID),
		"next_task_id":   fmt.Sprintf("%d", state.NextTaskID),
		"next_note_id":   fmt.Sprintf("%d", state.NextNoteID),
		"active_plan_id": state.ActivePlanID,
		"driver_id":      state.DriverID,
	}
	for k, v := range meta {
		if _, err := tx.Exec("INSERT INTO meta (key, value) VALUES (?, ?)", k, v); err != nil {
			return err
		}
	}

	for _, m := range state.Messages {
		readFlag := 0
		if m.Read {
			readFlag = 1
		}
		if _, err := tx.Exec("INSERT INTO messages (id, from_agent, to_agent, content, timestamp, read_flag) VALUES (?, ?, ?, ?, ?, ?)",
			m.ID, m.From, m.To, m.Content, m.Timestamp.Format(time.RFC3339Nano), readFlag); err != nil {
			return err
		}
	}

	for _, t := range state.Tasks {
		deps, _ := json.Marshal(t.Dependencies)
		caps, _ := json.Marshal(t.Capabilities)
		lastProgressAt := ""
		if !t.LastProgressAt.IsZero() {
			lastProgressAt = t.LastProgressAt.Format(time.RFC3339Nano)
		}
		if _, err := tx.Exec("INSERT INTO tasks (id, title, description, status, assigned_to, created_by, created_at, updated_at, priority, blocked_by, dependencies, context_id, worker_type, capabilities, result_summary, expected_duration_sec, progress_description, progress_percent, last_progress_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			t.ID, t.Title, t.Description, t.Status, t.AssignedTo, t.CreatedBy, t.CreatedAt.Format(time.RFC3339Nano), t.UpdatedAt.Format(time.RFC3339Nano), t.Priority, t.BlockedBy, string(deps), t.ContextID, t.WorkerType, string(caps), t.ResultSummary, t.ExpectedDurationSec, t.ProgressDescription, t.ProgressPercent, lastProgressAt); err != nil {
			return err
		}
	}

	for _, p := range state.Presence {
		if p == nil {
			continue
		}
		if _, err := tx.Exec("INSERT INTO presence (agent, status, current_task_id, note, workspace, last_seen) VALUES (?, ?, ?, ?, ?, ?)",
			p.Agent, p.Status, p.CurrentTaskID, p.Note, p.Workspace, p.LastSeen.Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}

	for _, n := range state.SessionNotes {
		if _, err := tx.Exec("INSERT INTO session_notes (id, author, content, category, timestamp) VALUES (?, ?, ?, ?, ?)",
			n.ID, n.Author, n.Content, n.Category, n.Timestamp.Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}

	for _, plan := range state.Plans {
		if plan == nil {
			continue
		}
		if _, err := tx.Exec("INSERT INTO plans (id, title, goal, context, created_by, created_at, updated_at, status) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
			plan.ID, plan.Title, plan.Goal, plan.Context, plan.CreatedBy, plan.CreatedAt.Format(time.RFC3339Nano), plan.UpdatedAt.Format(time.RFC3339Nano), plan.Status); err != nil {
			return err
		}
		for _, item := range plan.Items {
			acc, _ := json.Marshal(item.Acceptance)
			con, _ := json.Marshal(item.Constraints)
			deps, _ := json.Marshal(item.Dependencies)
			block, _ := json.Marshal(item.Blockers)
			notes, _ := json.Marshal(item.Notes)
			if _, err := tx.Exec("INSERT INTO plan_items (plan_id, item_id, title, description, reasoning, acceptance, constraints, status, owner, dependencies, blockers, notes, priority, updated_by, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
				plan.ID, item.ID, item.Title, item.Description, item.Reasoning, string(acc), string(con), item.Status, item.Owner, string(deps), string(block), string(notes), item.Priority, item.UpdatedBy, item.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
				return err
			}
		}
	}

	for _, ac := range state.AgentContexts {
		if ac == nil {
			continue
		}
		if _, err := tx.Exec("INSERT INTO agent_contexts (agent, last_checked_msg_id, last_checked_task_id, last_check_time) VALUES (?, ?, ?, ?)",
			ac.Agent, ac.LastCheckedMsgID, ac.LastCheckedTaskID, ac.LastCheckTime.Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}

	for _, fl := range state.FileLocks {
		if fl == nil {
			continue
		}
		if _, err := tx.Exec("INSERT INTO file_locks (path, locked_by, reason, locked_at, expires_at) VALUES (?, ?, ?, ?, ?)",
			fl.Path, fl.LockedBy, fl.Reason, fl.LockedAt.Format(time.RFC3339Nano), fl.ExpiresAt.Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}

	for _, ai := range state.AgentInstances {
		if ai == nil {
			continue
		}
		caps, _ := json.Marshal(ai.Capabilities)
		curTasks, _ := json.Marshal(ai.CurrentTasks)
		progressUpdAt := ""
		if !ai.ProgressUpdatedAt.IsZero() {
			progressUpdAt = ai.ProgressUpdatedAt.Format(time.RFC3339Nano)
		}
		if _, err := tx.Exec("INSERT INTO agent_instances (instance_id, agent_type, role, capabilities, max_tasks, status, current_tasks, workspace, last_heartbeat, progress, progress_step, progress_total_steps, progress_updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			ai.InstanceID, ai.AgentType, string(ai.Role), string(caps), ai.MaxTasks, ai.Status, string(curTasks), ai.Workspace, ai.LastHeartbeat.Format(time.RFC3339Nano), ai.Progress, ai.ProgressStep, ai.ProgressTotalSteps, progressUpdAt); err != nil {
			return err
		}
	}

	for _, wc := range state.WorkContexts {
		if wc == nil {
			continue
		}
		rf, _ := json.Marshal(wc.RelevantFiles)
		con, _ := json.Marshal(wc.Constraints)
		sn, _ := json.Marshal(wc.SharedNotes)
		if _, err := tx.Exec("INSERT INTO work_contexts (id, task_id, relevant_files, background, constraints, shared_notes, parent_ctx_id) VALUES (?, ?, ?, ?, ?, ?, ?)",
			wc.ID, wc.TaskID, string(rf), wc.Background, string(con), string(sn), wc.ParentCtxID); err != nil {
			return err
		}
	}

	for _, ra := range state.RegisteredAgents {
		if ra == nil {
			continue
		}
		caps, _ := json.Marshal(ra.Capabilities)
		if _, err := tx.Exec("INSERT INTO registered_agents (name, display_name, capabilities, workspace, project, registered_at, last_seen) VALUES (?, ?, ?, ?, ?, ?, ?)",
			ra.Name, ra.DisplayName, string(caps), ra.Workspace, ra.Project, ra.RegisteredAt.Format(time.RFC3339Nano), ra.LastSeen.Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}

	return tx.Commit()
}
