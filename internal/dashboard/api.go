// Package dashboard provides a web dashboard and JSON API for monitoring
// the stringwork MCP server state in real time.
package dashboard

import (
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/jaakkos/stringwork/internal/app"
	"github.com/jaakkos/stringwork/internal/domain"
)

// StateSnapshot is the JSON response from /api/state.
type StateSnapshot struct {
	Timestamp    string              `json:"timestamp"`
	Workspace    string              `json:"workspace"`
	Agents       []AgentSnapshot     `json:"agents"`
	Tasks        []TaskSnapshot      `json:"tasks"`
	Messages     []MessageSnapshot   `json:"messages"`
	Plans        []PlanSnapshot      `json:"plans,omitempty"`
	Workers      []WorkerSnapshot    `json:"workers,omitempty"`
	SessionNotes []NoteSnapshot      `json:"session_notes,omitempty"`
	FileLocks    []FileLockSnapshot  `json:"file_locks,omitempty"`
}

// AgentSnapshot is a per-agent summary.
type AgentSnapshot struct {
	Name               string `json:"name"`
	Status             string `json:"status"`
	Role               string `json:"role"`
	Workspace          string `json:"workspace,omitempty"`
	CurrentTaskID      int    `json:"current_task_id,omitempty"`
	Note               string `json:"note,omitempty"`
	LastSeen           string `json:"last_seen,omitempty"`
	LastHeartbeat      string `json:"last_heartbeat,omitempty"`
	Connected          bool   `json:"connected"`
	Progress           string `json:"progress,omitempty"`
	ProgressStep       int    `json:"progress_step,omitempty"`
	ProgressTotalSteps int    `json:"progress_total_steps,omitempty"`
	ProgressAge        string `json:"progress_age,omitempty"`
}

// TaskSnapshot is a per-task summary.
type TaskSnapshot struct {
	ID                  int    `json:"id"`
	Title               string `json:"title"`
	Status              string `json:"status"`
	AssignedTo          string `json:"assigned_to"`
	CreatedBy           string `json:"created_by"`
	Priority            int    `json:"priority"`
	Age                 string `json:"age"`
	ResultSummary       string `json:"result_summary,omitempty"`
	ProgressDescription string `json:"progress_description,omitempty"`
	ProgressPercent     int    `json:"progress_percent,omitempty"`
	LastProgressAge     string `json:"last_progress_age,omitempty"`
	ExpectedDurationSec int    `json:"expected_duration_sec,omitempty"`
	SLAStatus           string `json:"sla_status,omitempty"`
}

// MessageSnapshot is a per-message summary.
type MessageSnapshot struct {
	ID        int    `json:"id"`
	From      string `json:"from"`
	To        string `json:"to"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp"`
	Read      bool   `json:"read"`
	Age       string `json:"age"`
}

// PlanSnapshot is a per-plan summary.
type PlanSnapshot struct {
	ID        string             `json:"id"`
	Title     string             `json:"title"`
	Goal      string             `json:"goal"`
	Status    string             `json:"status"`
	ItemCount int                `json:"item_count"`
	Items     []PlanItemSnapshot `json:"items,omitempty"`
}

// PlanItemSnapshot is a per-plan-item summary.
type PlanItemSnapshot struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Owner  string `json:"owner"`
}

// WorkerSnapshot shows a worker instance's state.
type WorkerSnapshot struct {
	InstanceID         string `json:"instance_id"`
	AgentType          string `json:"agent_type"`
	Status             string `json:"status"`
	CurrentTasks       []int  `json:"current_tasks"`
	LastHeartbeat      string `json:"last_heartbeat"`
	Progress           string `json:"progress,omitempty"`
	ProgressStep       int    `json:"progress_step,omitempty"`
	ProgressTotalSteps int    `json:"progress_total_steps,omitempty"`
	ProgressAge        string `json:"progress_age,omitempty"`
}

// NoteSnapshot is a per-session-note summary.
type NoteSnapshot struct {
	ID       int    `json:"id"`
	Author   string `json:"author"`
	Content  string `json:"content"`
	Category string `json:"category"`
	Age      string `json:"age"`
}

// FileLockSnapshot is a per-file-lock summary.
type FileLockSnapshot struct {
	Path     string `json:"path"`
	LockedBy string `json:"locked_by"`
	Reason   string `json:"reason"`
	Age      string `json:"age"`
	Expires  string `json:"expires"`
}

// WorkerController is implemented by WorkerManager. It allows the dashboard
// to restart workers without importing the full WorkerManager.
type WorkerController interface {
	RestartWorkers() []string
	RunningWorkers() []string
}

// Handler holds dependencies for dashboard HTTP handlers.
type Handler struct {
	svc      *app.CollabService
	registry *app.SessionRegistry
	workers  WorkerController // optional; nil when no orchestration configured
}

// NewHandler creates a dashboard handler.
func NewHandler(svc *app.CollabService, registry *app.SessionRegistry, opts ...HandlerOption) *Handler {
	h := &Handler{svc: svc, registry: registry}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// HandlerOption configures optional dependencies for the dashboard handler.
type HandlerOption func(*Handler)

// WithWorkerController sets the WorkerController for the restart-workers endpoint.
func WithWorkerController(wc WorkerController) HandlerOption {
	return func(h *Handler) { h.workers = wc }
}

// RegisterRoutes adds dashboard routes to the given mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/state", h.handleAPIState)
	mux.HandleFunc("/api/reset", h.handleAPIReset)
	mux.HandleFunc("/api/restart-workers", h.handleAPIRestartWorkers)
	mux.HandleFunc("/api/switch-project", h.handleAPISwitchProject)
	mux.HandleFunc("/dashboard", h.handleDashboard)
	mux.HandleFunc("/dashboard/", h.handleDashboard)
}

func (h *Handler) handleAPIReset(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		w.Write([]byte(`{"error":"POST required"}`))
		return
	}

	// Determine what to keep based on query params (default: reset everything)
	keepAgents := r.URL.Query().Get("keep_agents") == "true"

	err := h.svc.Run(func(state *domain.CollabState) error {
		state.Tasks = []domain.Task{}
		state.Messages = []domain.Message{}
		state.SessionNotes = []domain.SessionNote{}
		state.Plans = make(map[string]*domain.Plan)
		state.ActivePlanID = ""
		state.FileLocks = make(map[string]*domain.FileLock)
		state.WorkContexts = make(map[string]*domain.WorkContext)
		state.AgentContexts = make(map[string]*domain.AgentContext)
		state.NextTaskID = 1
		state.NextMsgID = 1
		state.NextNoteID = 1

		if !keepAgents {
			state.Presence = make(map[string]*domain.Presence)
			state.RegisteredAgents = make(map[string]*domain.RegisteredAgent)
			// Reset agent instance task lists but keep the instances themselves
			for _, inst := range state.AgentInstances {
				if inst != nil {
					inst.CurrentTasks = nil
					inst.Status = "idle"
				}
			}
		} else {
			// Just clear current tasks from instances
			for _, inst := range state.AgentInstances {
				if inst != nil {
					inst.CurrentTasks = nil
					inst.Status = "idle"
				}
			}
		}

		return nil
	})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"` + err.Error() + `"}`))
		return
	}

	w.Write([]byte(`{"status":"ok","message":"State has been reset"}`))
}

func (h *Handler) handleAPIRestartWorkers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		w.Write([]byte(`{"error":"POST required"}`))
		return
	}
	if h.workers == nil {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"no orchestration configured — workers are not managed by this server"}`))
		return
	}

	killed := h.workers.RestartWorkers()

	resp := map[string]any{
		"status":  "ok",
		"message": "Workers restarted",
		"killed":  killed,
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(resp)
}

func (h *Handler) handleAPISwitchProject(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		w.Write([]byte(`{"error":"POST required"}`))
		return
	}

	workspace := r.URL.Query().Get("workspace")
	if workspace == "" {
		// Try JSON body
		var body struct {
			Workspace string `json:"workspace"`
		}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&body)
			workspace = body.Workspace
		}
	}
	if workspace == "" {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"workspace parameter is required"}`))
		return
	}

	var steps []string

	// Step 1: Kill running workers
	if h.workers != nil {
		killed := h.workers.RestartWorkers()
		if len(killed) > 0 {
			steps = append(steps, "killed "+itoa(len(killed))+" worker(s)")
		}
	}

	// Step 2: Clear working scope (tasks, messages, plans, notes, locks)
	err := h.svc.Run(func(state *domain.CollabState) error {
		state.Tasks = []domain.Task{}
		state.Messages = []domain.Message{}
		state.SessionNotes = []domain.SessionNote{}
		state.Plans = make(map[string]*domain.Plan)
		state.ActivePlanID = ""
		state.FileLocks = make(map[string]*domain.FileLock)
		state.WorkContexts = make(map[string]*domain.WorkContext)
		state.AgentContexts = make(map[string]*domain.AgentContext)
		state.NextTaskID = 1
		state.NextMsgID = 1
		state.NextNoteID = 1

		// Reset agent instance task lists but keep agents registered
		for _, inst := range state.AgentInstances {
			if inst != nil {
				inst.CurrentTasks = nil
				inst.Status = "idle"
			}
		}

		// Update workspace in presence for all agents
		for _, p := range state.Presence {
			if p != nil {
				p.Workspace = workspace
			}
		}

		return nil
	})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"` + err.Error() + `"}`))
		return
	}
	steps = append(steps, "cleared working scope")

	// Step 3: Update workspace root in policy
	h.svc.Policy().SetWorkspaceRoot(workspace)
	steps = append(steps, "workspace set to "+workspace)

	resp := map[string]any{
		"status":    "ok",
		"workspace": workspace,
		"steps":     steps,
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(resp)
}

func (h *Handler) handleAPIState(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-cache")

	now := time.Now()
	snap := StateSnapshot{
		Timestamp: now.Format(time.RFC3339),
		Workspace: h.svc.Policy().WorkspaceRoot(),
	}

	connectedAgents := make(map[string]bool)
	for _, a := range h.registry.ConnectedAgents() {
		connectedAgents[a] = true
	}

	_ = h.svc.Query(func(state *domain.CollabState) error {
		// ── Agents: merge presence + instances ──
		agentsSeen := make(map[string]bool)
		for name, p := range state.Presence {
			a := AgentSnapshot{
				Name:          name,
				Status:        p.Status,
				Workspace:     p.Workspace,
				CurrentTaskID: p.CurrentTaskID,
				Note:          p.Note,
				LastSeen:      relTime(p.LastSeen, now),
				Connected:     connectedAgents[name],
			}
			if inst, ok := state.AgentInstances[name]; ok && inst != nil {
				a.Role = string(inst.Role)
				a.LastHeartbeat = relTime(inst.LastHeartbeat, now)
				a.Progress = inst.Progress
				a.ProgressStep = inst.ProgressStep
				a.ProgressTotalSteps = inst.ProgressTotalSteps
				if !inst.ProgressUpdatedAt.IsZero() {
					a.ProgressAge = relTime(inst.ProgressUpdatedAt, now)
				}
			}
			snap.Agents = append(snap.Agents, a)
			agentsSeen[name] = true
		}
		for id, inst := range state.AgentInstances {
			if inst == nil || agentsSeen[id] {
				continue
			}
			a := AgentSnapshot{
				Name:               id,
				Status:             inst.Status,
				Role:               string(inst.Role),
				LastHeartbeat:      relTime(inst.LastHeartbeat, now),
				Connected:          connectedAgents[id],
				Progress:           inst.Progress,
				ProgressStep:       inst.ProgressStep,
				ProgressTotalSteps: inst.ProgressTotalSteps,
			}
			if !inst.ProgressUpdatedAt.IsZero() {
				a.ProgressAge = relTime(inst.ProgressUpdatedAt, now)
			}
			snap.Agents = append(snap.Agents, a)
		}
		// Sort agents: drivers first, then alphabetically by name
		sort.Slice(snap.Agents, func(i, j int) bool {
			if snap.Agents[i].Role != snap.Agents[j].Role {
				if snap.Agents[i].Role == "driver" {
					return true
				}
				if snap.Agents[j].Role == "driver" {
					return false
				}
			}
			return snap.Agents[i].Name < snap.Agents[j].Name
		})

		// ── Tasks (most recent first, limit 50) ──
		start := 0
		if len(state.Tasks) > 50 {
			start = len(state.Tasks) - 50
		}
		for i := len(state.Tasks) - 1; i >= start; i-- {
			t := state.Tasks[i]
			ts := TaskSnapshot{
				ID:                  t.ID,
				Title:               truncate(t.Title, 80),
				Status:              t.Status,
				AssignedTo:          t.AssignedTo,
				CreatedBy:           t.CreatedBy,
				Priority:            t.Priority,
				Age:                 relTime(t.CreatedAt, now),
				ResultSummary:       truncate(t.ResultSummary, 120),
				ProgressDescription: truncate(t.ProgressDescription, 120),
				ProgressPercent:     t.ProgressPercent,
				ExpectedDurationSec: t.ExpectedDurationSec,
			}
			if !t.LastProgressAt.IsZero() {
				ts.LastProgressAge = relTime(t.LastProgressAt, now)
			}
			if t.ExpectedDurationSec > 0 && t.Status == "in_progress" {
				expected := time.Duration(t.ExpectedDurationSec) * time.Second
				actual := now.Sub(t.UpdatedAt)
				if actual > expected {
					ts.SLAStatus = "over"
				} else {
					ts.SLAStatus = "ok"
				}
			}
			snap.Tasks = append(snap.Tasks, ts)
		}

		// ── Messages (most recent first, limit 30) ──
		msgStart := 0
		if len(state.Messages) > 30 {
			msgStart = len(state.Messages) - 30
		}
		for i := len(state.Messages) - 1; i >= msgStart; i-- {
			m := state.Messages[i]
			snap.Messages = append(snap.Messages, MessageSnapshot{
				ID:        m.ID,
				From:      m.From,
				To:        m.To,
				Content:   truncate(m.Content, 200),
				Timestamp: m.Timestamp.Format("15:04:05"),
				Read:      m.Read,
				Age:       relTime(m.Timestamp, now),
			})
		}

		// ── Plans (sorted by ID for consistency) ──
		planIDs := make([]string, 0, len(state.Plans))
		for id := range state.Plans {
			planIDs = append(planIDs, id)
		}
		sort.Strings(planIDs)
		for _, id := range planIDs {
			plan := state.Plans[id]
			if plan == nil {
				continue
			}
			ps := PlanSnapshot{
				ID:        id,
				Title:     plan.Title,
				Goal:      truncate(plan.Goal, 100),
				Status:    plan.Status,
				ItemCount: len(plan.Items),
			}
			for _, item := range plan.Items {
				ps.Items = append(ps.Items, PlanItemSnapshot{
					ID:     item.ID,
					Title:  truncate(item.Title, 60),
					Status: item.Status,
					Owner:  item.Owner,
				})
			}
			snap.Plans = append(snap.Plans, ps)
		}

		// ── Workers (sorted by instance ID) ──
		for id, inst := range state.AgentInstances {
			if inst == nil || inst.Role != domain.RoleWorker {
				continue
			}
			ws := WorkerSnapshot{
				InstanceID:         id,
				AgentType:          inst.AgentType,
				Status:             inst.Status,
				CurrentTasks:       inst.CurrentTasks,
				LastHeartbeat:      relTime(inst.LastHeartbeat, now),
				Progress:           inst.Progress,
				ProgressStep:       inst.ProgressStep,
				ProgressTotalSteps: inst.ProgressTotalSteps,
			}
			if !inst.ProgressUpdatedAt.IsZero() {
				ws.ProgressAge = relTime(inst.ProgressUpdatedAt, now)
			}
			snap.Workers = append(snap.Workers, ws)
		}
		sort.Slice(snap.Workers, func(i, j int) bool {
			return snap.Workers[i].InstanceID < snap.Workers[j].InstanceID
		})

		// ── Session notes (most recent first, limit 20) ──
		noteStart := 0
		if len(state.SessionNotes) > 20 {
			noteStart = len(state.SessionNotes) - 20
		}
		for i := len(state.SessionNotes) - 1; i >= noteStart; i-- {
			n := state.SessionNotes[i]
			snap.SessionNotes = append(snap.SessionNotes, NoteSnapshot{
				ID:       n.ID,
				Author:   n.Author,
				Content:  truncate(n.Content, 200),
				Category: n.Category,
				Age:      relTime(n.Timestamp, now),
			})
		}

		// ── File locks (sorted by path) ──
		lockPaths := make([]string, 0, len(state.FileLocks))
		for p := range state.FileLocks {
			lockPaths = append(lockPaths, p)
		}
		sort.Strings(lockPaths)
		for _, p := range lockPaths {
			fl := state.FileLocks[p]
			if fl == nil {
				continue
			}
			expires := "never"
			if !fl.ExpiresAt.IsZero() {
				if fl.ExpiresAt.After(now) {
					expires = "in " + relTime(now.Add(fl.ExpiresAt.Sub(now)), now)
				} else {
					expires = "expired"
				}
			}
			snap.FileLocks = append(snap.FileLocks, FileLockSnapshot{
				Path:     fl.Path,
				LockedBy: fl.LockedBy,
				Reason:   fl.Reason,
				Age:      relTime(fl.LockedAt, now),
				Expires:  expires,
			})
		}

		return nil
	})

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(snap)
}

func relTime(t time.Time, now time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := now.Sub(t)
	switch {
	case d < time.Second:
		return "just now"
	case d < time.Minute:
		return formatDuration(d, "s")
	case d < time.Hour:
		return formatDuration(d, "m")
	case d < 24*time.Hour:
		return formatDuration(d, "h")
	default:
		return t.Format("Jan 2 15:04")
	}
}

func formatDuration(d time.Duration, unit string) string {
	switch unit {
	case "s":
		return itoa(int(d.Seconds())) + "s ago"
	case "m":
		return itoa(int(d.Minutes())) + "m ago"
	case "h":
		return itoa(int(d.Hours())) + "h ago"
	default:
		return d.String()
	}
}

func itoa(n int) string {
	if n < 0 {
		n = -n
	}
	if n == 0 {
		return "0"
	}
	buf := make([]byte, 0, 4)
	for n > 0 {
		buf = append(buf, byte('0'+n%10))
		n /= 10
	}
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return string(buf)
}

func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "..."
}
