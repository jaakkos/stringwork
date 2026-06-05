// Package domain holds collaboration entities and aggregate state.
// It has no dependencies on other packages.
package domain

import "time"

// Message is a message between AI agents.
type Message struct {
	ID        int       `json:"id"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
	Read      bool      `json:"read"`
}

// AgentRole is the role of an agent in the driver/worker model.
type AgentRole string

const (
	RoleDriver AgentRole = "driver"
	RoleWorker AgentRole = "worker"
)

// AgentInstance represents a single agent instance (driver or worker), including multi-instance workers.
type AgentInstance struct {
	InstanceID    string    `json:"instance_id"`
	AgentType     string    `json:"agent_type"`
	Role          AgentRole `json:"role"`
	Capabilities  []string  `json:"capabilities,omitempty"`
	MaxTasks      int       `json:"max_tasks"`
	Status        string    `json:"status"` // idle, busy, offline
	CurrentTasks  []int     `json:"current_tasks,omitempty"`
	Workspace     string    `json:"workspace,omitempty"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
	// LastSpawnedAt records the moment WorkerManager last initiated a spawn for
	// this instance. It is set BEFORE the worker process starts (and before its
	// first heartbeat arrives) so callers can distinguish "freshly spawned, give
	// it grace" from "actually unresponsive". Watchdog consults this alongside
	// LastHeartbeat before flipping a worker to offline.
	LastSpawnedAt      time.Time `json:"last_spawned_at,omitempty"`
	Progress           string    `json:"progress,omitempty"`             // free-text progress description from last heartbeat
	ProgressStep       int       `json:"progress_step,omitempty"`        // current step number (e.g. 3 of 5)
	ProgressTotalSteps int       `json:"progress_total_steps,omitempty"` // total steps
	ProgressUpdatedAt  time.Time `json:"progress_updated_at,omitempty"`  // when progress was last reported
	SessionID          string    `json:"session_id,omitempty"`           // CLI session/conversation ID for resume on restart
}

// WorkContext holds shared context for a task (files, background, constraints).
// WorktreeName is set by the orchestrator when assigning a task to a worker that
// uses Claude native worktrees (-w), so the worker runs in that scope.
type WorkContext struct {
	ID             string            `json:"id"`
	TaskID         int               `json:"task_id"`
	RelevantFiles  []string          `json:"relevant_files,omitempty"`
	Background     string            `json:"background,omitempty"`
	Constraints    []string          `json:"constraints,omitempty"`
	SharedNotes    map[string]string `json:"shared_notes,omitempty"`
	ParentCtxID    string            `json:"parent_ctx_id,omitempty"`
	WorktreeName   string            `json:"worktree_name,omitempty"`   // Claude -w scope; set by orchestrator on assign
	PreviousOutput string            `json:"previous_output,omitempty"` // captured output from a previous worker attempt
}

// Task is a shared task.
type Task struct {
	ID           int       `json:"id"`
	Title        string    `json:"title"`
	Description  string    `json:"description"`
	Status       string    `json:"status"` // pending, in_progress, completed, blocked, cancelled
	AssignedTo   string    `json:"assigned_to"`
	CreatedBy    string    `json:"created_by"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	Priority     int       `json:"priority"`
	Dependencies []int     `json:"dependencies"`
	BlockedBy    string    `json:"blocked_by"`
	ContextID    string    `json:"context_id,omitempty"`
	WorkerType   string    `json:"worker_type,omitempty"`
	Capabilities []string  `json:"capabilities,omitempty"`
	// Model is an explicit CLI model override set by the driver on create_task
	// (e.g. "opus", "haiku"). Takes precedence over ModelTier at spawn.
	Model string `json:"model,omitempty"`
	// ModelTier is a named cost/capability tier (e.g. "fast", "standard",
	// "capable") resolved via orchestration.model_tiers at spawn time.
	ModelTier     string `json:"model_tier,omitempty"`
	ResultSummary string `json:"result_summary,omitempty"`
	// Progress monitoring fields
	ExpectedDurationSec int       `json:"expected_duration_seconds,omitempty"` // SLA: expected task duration in seconds
	ProgressDescription string    `json:"progress_description,omitempty"`      // latest progress report text
	ProgressPercent     int       `json:"progress_percent,omitempty"`          // 0-100 completion estimate
	LastProgressAt      time.Time `json:"last_progress_at,omitempty"`          // when progress was last reported
	// Failure tracking
	FailureCount  int       `json:"failure_count,omitempty"`
	LastFailure   time.Time `json:"last_failure_at,omitempty"`
	FailureReason string    `json:"failure_reason,omitempty"`
	// RecoveryEvents is an append-only log of reconciler / watchdog actions
	// taken on this task. Newest last, capped at app.recoveryEventLogCap
	// entries (older entries dropped). ResultSummary is kept in sync with
	// the most recent event.Summary for backward compatibility with
	// existing UI / MCP / chrome-extension consumers that read the single
	// string field.
	RecoveryEvents []RecoveryEvent `json:"recovery_events,omitempty"`
	// LastReconciledAt is set by reconcileAfterExit when it resets a task
	// to pending. The watchdog uses it to suppress its own FailureCount
	// increment when it sees the same incident propagate within
	// 2*heartbeatStaleThresh — otherwise a single worker exit would burn
	// two of the MaxTaskFailures (default 3) DLQ slots.
	LastReconciledAt time.Time `json:"last_reconciled_at,omitempty"`
	// Review gates
	RequiresReview bool   `json:"requires_review,omitempty"`
	ReviewStatus   string `json:"review_status,omitempty"` // pending, approved, rejected
	ReviewedBy     string `json:"reviewed_by,omitempty"`
	// Task-template provenance.
	//
	// Template is the id of the task-templates template (see
	// internal/tasktemplates) that planned this task — e.g.
	// "code-review". Empty string means "not produced by a template"
	// (driver hand-crafted the task, or the task pre-dates Phase 2).
	// Aspect is the per-template aspect id that this task represents
	// — e.g. "security" inside the "code-review" template. Empty
	// when the template ships a single-aspect task or the task is
	// not template-produced.
	//
	// Both columns are stored as TEXT NOT NULL DEFAULT '' in sqlite
	// (see runMigrations) so existing rows back-fill cleanly without
	// a separate data migration. The "unset" signal at the Go layer
	// is the empty string, NOT sql.NullString — the constitution
	// alias rule (constitution.TaskKindForTask) and the list_tasks
	// `template` filter both compare against the empty string, so
	// keeping the column NOT NULL avoids an extra wrapping layer
	// without changing semantics.
	//
	// These fields exist to (a) drive the constitution alias rule
	// without re-deriving it from the title and (b) let `list_tasks
	// --template code-review` or driver dashboards group / filter
	// task-spawn output by template.
	Template string `json:"template,omitempty"`
	Aspect   string `json:"aspect,omitempty"`
}

// RecoveryEvent is one entry in Task.RecoveryEvents. Together they form a
// short timeline of automated recovery actions on a task — replaces the
// old "first writer wins" ResultSummary shape where the reconciler and the
// watchdog would silently overwrite each other and the user could not
// reconstruct the order of events.
//
// Source identifies the subsystem that wrote the entry ("reconciler",
// "watchdog", "auto_cancel"). Reason is a short machine-readable code (see
// app/recovery_events.go for the canonical list) so dashboards can render
// distinct icons / colors. Summary is the human-readable one-liner that
// also gets mirrored into Task.ResultSummary for backward compatibility.
type RecoveryEvent struct {
	At         time.Time `json:"at"`
	Source     string    `json:"source"`
	Reason     string    `json:"reason"`
	Summary    string    `json:"summary"`
	InstanceID string    `json:"instance_id,omitempty"`
}

// AuditEntry represents a single tool call recorded by the audit middleware.
type AuditEntry struct {
	ID          int64     `json:"id"`
	Timestamp   time.Time `json:"timestamp"`
	Agent       string    `json:"agent"`
	ToolName    string    `json:"tool_name"`
	ArgsSummary string    `json:"args_summary"`
	DurationMs  int64     `json:"duration_ms"`
	Error       string    `json:"error,omitempty"`
	SessionID   string    `json:"session_id,omitempty"`
}

// Presence is an agent's current status.
type Presence struct {
	Agent         string    `json:"agent"`
	Status        string    `json:"status"` // idle, working, reviewing, away
	CurrentTaskID int       `json:"current_task_id,omitempty"`
	Note          string    `json:"note,omitempty"`
	Workspace     string    `json:"workspace,omitempty"` // project workspace root this agent is working in
	LastSeen      time.Time `json:"last_seen"`
}

// SessionNote is a shared note or decision.
type SessionNote struct {
	ID        int       `json:"id"`
	Author    string    `json:"author"`
	Content   string    `json:"content"`
	Category  string    `json:"category"` // decision, note, question, blocker
	Timestamp time.Time `json:"timestamp"`
}

// PlanItem is a single item in a shared plan.
type PlanItem struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	Description  string    `json:"description"`
	Reasoning    string    `json:"reasoning,omitempty"`
	Acceptance   []string  `json:"acceptance,omitempty"`
	Constraints  []string  `json:"constraints,omitempty"`
	Status       string    `json:"status"`
	Owner        string    `json:"owner"`
	Dependencies []string  `json:"dependencies"`
	Blockers     []string  `json:"blockers"`
	Notes        []string  `json:"notes"`
	Priority     int       `json:"priority"`
	UpdatedBy    string    `json:"updated_by"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Plan is a shared planning document.
type Plan struct {
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	Goal      string     `json:"goal"`
	Context   string     `json:"context"`
	Items     []PlanItem `json:"items"`
	CreatedBy string     `json:"created_by"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	Status    string     `json:"status"` // active, completed, archived
}

// AgentContext tracks what an agent has seen for notifications.
type AgentContext struct {
	Agent             string    `json:"agent"`
	LastCheckedMsgID  int       `json:"last_checked_msg_id"`
	LastCheckedTaskID int       `json:"last_checked_task_id"`
	LastCheckTime     time.Time `json:"last_check_time"`
}

// FileLock is a lock on a file to prevent simultaneous edits.
type FileLock struct {
	Path      string    `json:"path"`
	LockedBy  string    `json:"locked_by"`
	Reason    string    `json:"reason"`
	LockedAt  time.Time `json:"locked_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// RegisteredAgent is an agent that has registered with the system.
type RegisteredAgent struct {
	Name         string    `json:"name"`
	DisplayName  string    `json:"display_name,omitempty"`
	Capabilities []string  `json:"capabilities,omitempty"`
	Workspace    string    `json:"workspace,omitempty"`
	Project      string    `json:"project,omitempty"`
	RegisteredAt time.Time `json:"registered_at"`
	LastSeen     time.Time `json:"last_seen"`
}

// ProjectInfo holds information about the current project/workspace.
type ProjectInfo struct {
	Name        string    `json:"name"`
	Path        string    `json:"path"`
	GitBranch   string    `json:"git_branch,omitempty"`
	GitRemote   string    `json:"git_remote,omitempty"`
	IsGitRepo   bool      `json:"is_git_repo"`
	LastUpdated time.Time `json:"last_updated"`
}

// CollabState is the aggregate collaboration state.
type CollabState struct {
	Messages         []Message                   `json:"messages"`
	Tasks            []Task                      `json:"tasks"`
	Presence         map[string]*Presence        `json:"presence"`
	SessionNotes     []SessionNote               `json:"session_notes"`
	Plans            map[string]*Plan            `json:"plans"`
	ActivePlanID     string                      `json:"active_plan_id"`
	AgentContexts    map[string]*AgentContext    `json:"agent_contexts"`
	FileLocks        map[string]*FileLock        `json:"file_locks"`
	RegisteredAgents map[string]*RegisteredAgent `json:"registered_agents"`
	ProjectInfo      *ProjectInfo                `json:"project_info,omitempty"`
	NextMsgID        int                         `json:"next_msg_id"`
	NextTaskID       int                         `json:"next_task_id"`
	NextNoteID       int                         `json:"next_note_id"`
	AgentInstances   map[string]*AgentInstance   `json:"agent_instances"`
	WorkContexts     map[string]*WorkContext     `json:"work_contexts"`
	DriverID         string                      `json:"driver_id"`

	// LastSendByAgent tracks the most recent send_message timestamp per
	// agent. Used by the watchdog to grant a "recent send" grace window so
	// workers actively delivering output aren't auto-cancelled mid-message.
	// Ephemeral — not persisted to SQLite; rebuilt as send_message fires.
	LastSendByAgent map[string]time.Time `json:"-"`

	// DaemonStartedAt is the moment the current mcp-server process booted.
	// It is the driver-side fallback for the STOP-banner spawn cutoff so
	// agents without a per-instance LastSpawnedAt (notably the cursor
	// driver, which has no AgentInstance row) do not see STOP banners
	// triggered by tasks cancelled before the daemon started. Ephemeral
	// per-process state — not persisted to SQLite. A fresh daemon means
	// a fresh cutoff, by design.
	DaemonStartedAt time.Time `json:"-"`
}

// NewCollabState returns an empty CollabState with maps and IDs initialized.
//
// DaemonStartedAt is seeded to time.Now() so the driver-side STOP-banner
// spawn cutoff (piggyback.BuildBanner) has a non-zero reference even for
// CollabStates constructed outside main.go's boot path — unit tests,
// future entrypoints, or any helper that allocates fresh state without
// going through Service.Run-then-set. main.go overwrites the field
// inside its boot Run callback (no behavioural change there); this
// constructor anchor exists so the per-process invariant is enforced
// structurally instead of being a documentation-only convention.
func NewCollabState() *CollabState {
	return &CollabState{
		Messages:         []Message{},
		Tasks:            []Task{},
		Presence:         make(map[string]*Presence),
		SessionNotes:     []SessionNote{},
		Plans:            make(map[string]*Plan),
		AgentContexts:    make(map[string]*AgentContext),
		FileLocks:        make(map[string]*FileLock),
		RegisteredAgents: make(map[string]*RegisteredAgent),
		AgentInstances:   make(map[string]*AgentInstance),
		WorkContexts:     make(map[string]*WorkContext),
		LastSendByAgent:  make(map[string]time.Time),
		NextMsgID:        1,
		NextTaskID:       1,
		NextNoteID:       1,
		DaemonStartedAt:  time.Now(),
	}
}
