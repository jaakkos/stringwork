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
	InstanceID         string    `json:"instance_id"`
	AgentType          string    `json:"agent_type"`
	Role               AgentRole `json:"role"`
	Capabilities       []string  `json:"capabilities,omitempty"`
	MaxTasks           int       `json:"max_tasks"`
	Status             string    `json:"status"` // idle, busy, offline
	CurrentTasks       []int     `json:"current_tasks,omitempty"`
	Workspace          string    `json:"workspace,omitempty"`
	LastHeartbeat      time.Time `json:"last_heartbeat"`
	Progress           string    `json:"progress,omitempty"`             // free-text progress description from last heartbeat
	ProgressStep       int       `json:"progress_step,omitempty"`        // current step number (e.g. 3 of 5)
	ProgressTotalSteps int       `json:"progress_total_steps,omitempty"` // total steps
	ProgressUpdatedAt  time.Time `json:"progress_updated_at,omitempty"`  // when progress was last reported
}

// WorkContext holds shared context for a task (files, background, constraints).
type WorkContext struct {
	ID            string            `json:"id"`
	TaskID        int               `json:"task_id"`
	RelevantFiles []string          `json:"relevant_files,omitempty"`
	Background    string            `json:"background,omitempty"`
	Constraints   []string          `json:"constraints,omitempty"`
	SharedNotes   map[string]string `json:"shared_notes,omitempty"`
	ParentCtxID   string            `json:"parent_ctx_id,omitempty"`
}

// Task is a shared task.
type Task struct {
	ID            int       `json:"id"`
	Title         string    `json:"title"`
	Description   string    `json:"description"`
	Status        string    `json:"status"` // pending, in_progress, completed, blocked, cancelled
	AssignedTo    string    `json:"assigned_to"`
	CreatedBy     string    `json:"created_by"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	Priority      int       `json:"priority"`
	Dependencies  []int     `json:"dependencies"`
	BlockedBy     string    `json:"blocked_by"`
	ContextID     string    `json:"context_id,omitempty"`
	WorkerType    string    `json:"worker_type,omitempty"`
	Capabilities  []string  `json:"capabilities,omitempty"`
	ResultSummary string    `json:"result_summary,omitempty"`
	// Progress monitoring fields
	ExpectedDurationSec int       `json:"expected_duration_seconds,omitempty"` // SLA: expected task duration in seconds
	ProgressDescription string    `json:"progress_description,omitempty"`      // latest progress report text
	ProgressPercent     int       `json:"progress_percent,omitempty"`          // 0-100 completion estimate
	LastProgressAt      time.Time `json:"last_progress_at,omitempty"`          // when progress was last reported
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
}

// NewCollabState returns an empty CollabState with maps and IDs initialized.
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
		NextMsgID:        1,
		NextTaskID:       1,
		NextNoteID:       1,
	}
}
