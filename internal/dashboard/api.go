// Package dashboard provides a web dashboard and JSON API for monitoring
// the stringwork MCP server state in real time.
package dashboard

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jaakkos/stringwork/internal/app"
	"github.com/jaakkos/stringwork/internal/domain"
)

// recoveredMessagePrefix is the leading marker prepended by cancel_agent's
// synthetic-recovery branch when a worker died before sending its final
// send_message. The dashboard uses this to render a "[recovered]" pill
// instead of treating it as a normal worker-authored message.
const recoveredMessagePrefix = "⚠️ Auto-recovered output (worker did not send before cancel):"

// recentSendGraceWindow mirrors the watchdog's recent-send grace window
// (internal/app/watchdog.go). When the most recent send_message from an
// agent is within this window, the snapshot reports the agent as in
// "delivery grace" so the dashboard can de-emphasise its silence.
const recentSendGraceWindow = 90 * time.Second

// StateSnapshot is the JSON response from /api/state.
//
// Field categories:
//   - Display state (Agents, Tasks, Messages, Plans, Workers, …): mirror the
//     CollabState the dashboard renders.
//   - Operational state (GC): cumulative watchdog counters surfaced so the
//     dashboard can render a one-line "garbage collection running normally"
//     strip without separate polling. Populated only when the handler was
//     constructed with WithGCStatsProvider.
type StateSnapshot struct {
	Timestamp     string             `json:"timestamp"`
	Workspace     string             `json:"workspace"`
	Agents        []AgentSnapshot    `json:"agents"`
	Tasks         []TaskSnapshot     `json:"tasks"`
	TotalTasks    int                `json:"total_tasks"`
	Messages      []MessageSnapshot  `json:"messages"`
	TotalMessages int                `json:"total_messages"`
	Plans         []PlanSnapshot     `json:"plans,omitempty"`
	Workers       []WorkerSnapshot   `json:"workers,omitempty"`
	SessionNotes  []NoteSnapshot     `json:"session_notes,omitempty"`
	FileLocks     []FileLockSnapshot `json:"file_locks,omitempty"`
	GC            *GCSnapshot        `json:"gc,omitempty"`
}

// AgentSnapshot is a per-agent summary.
//
// LastSendAge / InDeliveryGrace mirror the watchdog's recent-send grace
// window (90s by default) so the dashboard can soft-tint agents that are
// briefly silent because they just sent a deliverable, distinguishing them
// from agents that are silent for unexplained reasons.
type AgentSnapshot struct {
	Name               string `json:"name"`
	Status             string `json:"status"`
	Role               string `json:"role"`
	Workspace          string `json:"workspace,omitempty"`
	CurrentTaskID      int    `json:"current_task_id,omitempty"`
	CurrentTasks       []int  `json:"current_tasks,omitempty"`
	Note               string `json:"note,omitempty"`
	LastSeen           string `json:"last_seen,omitempty"`
	LastHeartbeat      string `json:"last_heartbeat,omitempty"`
	Connected          bool   `json:"connected"`
	Reachable          bool   `json:"reachable"`
	Progress           string `json:"progress,omitempty"`
	ProgressStep       int    `json:"progress_step,omitempty"`
	ProgressTotalSteps int    `json:"progress_total_steps,omitempty"`
	ProgressAge        string `json:"progress_age,omitempty"`
	LastSendAge        string `json:"last_send_age,omitempty"`
	InDeliveryGrace    bool   `json:"in_delivery_grace,omitempty"`
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
//
// Recovered is set to true when the message is the synthetic "auto-recovered
// output" emitted by cancel_agent's safety net (worker died before sending
// its final send_message). Detected by content prefix; we don't promote a
// Kind field on domain.Message yet because the safety net is opt-in and
// fairly localised.
type MessageSnapshot struct {
	ID        int    `json:"id"`
	From      string `json:"from"`
	To        string `json:"to"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp"`
	Read      bool   `json:"read"`
	Age       string `json:"age"`
	Recovered bool   `json:"recovered,omitempty"`
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
//
// IsTaskBound / BoundTaskID expose the runtime distinction the watchdog and
// pruner already enforce: a task-bound instance ("claude-code-task-7") has
// its lifetime tied to a single task and is reaped when that task reaches
// a terminal state. The dashboard groups task-bound rows under their pool
// type so users can see the full picture of "claude-code workers right now".
//
// LastSendAge / InDeliveryGrace work the same way as on AgentSnapshot.
type WorkerSnapshot struct {
	InstanceID         string `json:"instance_id"`
	AgentType          string `json:"agent_type"`
	Status             string `json:"status"`
	CurrentTasks       []int  `json:"current_tasks"`
	LastHeartbeat      string `json:"last_heartbeat"`
	Reachable          bool   `json:"reachable"`
	Progress           string `json:"progress,omitempty"`
	ProgressStep       int    `json:"progress_step,omitempty"`
	ProgressTotalSteps int    `json:"progress_total_steps,omitempty"`
	ProgressAge        string `json:"progress_age,omitempty"`
	IsTaskBound        bool   `json:"is_task_bound,omitempty"`
	BoundTaskID        int    `json:"bound_task_id,omitempty"`
	LastSendAge        string `json:"last_send_age,omitempty"`
	InDeliveryGrace    bool   `json:"in_delivery_grace,omitempty"`
}

// GCSnapshot exposes cumulative watchdog garbage-collection counters and
// the configured retention policy. Surfaced in StateSnapshot.GC when the
// handler is configured with WithGCStatsProvider.
type GCSnapshot struct {
	LastRun                         string `json:"last_run,omitempty"`
	PresencePrunedTotal             int64  `json:"presence_pruned_total"`
	InstancesPrunedTotal            int64  `json:"instances_pruned_total"`
	PresenceRetentionDays           int    `json:"presence_retention_days"`
	InstanceRetentionDays           int    `json:"instance_retention_days"`
	TaskBoundInstanceRetentionHours int    `json:"task_bound_instance_retention_hours"`
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
// to manage workers (restart, cancel, inspect) without importing the full
// WorkerManager.
//
// CancelWorker / GetRecentOutput / IsWorkerRunning are needed by the
// /api/cancel-agent endpoint so it can match the cancel_agent MCP tool's
// behaviour: capture pending output, kill the spawned process, then surface
// a synthetic recovery message via the standard state mutation.
type WorkerController interface {
	RestartWorkers() []string
	RunningWorkers() []string
	CancelWorker(instanceID string) bool
	IsWorkerRunning(instanceID string) bool
	GetRecentOutput(instanceID string) string
}

// GCStatsProvider returns cumulative garbage-collection counters from the
// watchdog. Implemented by *app.Watchdog. Set via WithGCStatsProvider so the
// /api/state response can include the GC block.
type GCStatsProvider interface {
	GCStats() app.GCStats
}

// Handler holds dependencies for dashboard HTTP handlers.
type Handler struct {
	svc                *app.CollabService
	registry           *app.SessionRegistry
	workers            WorkerController // optional; nil when no orchestration configured
	gcStats            GCStatsProvider  // optional; nil when no watchdog configured
	heartbeatThreshold time.Duration    // max age for heartbeat to be "reachable"; 0 = 5min default
	logger             *log.Logger      // optional; nil → no-op logging
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

// WithWorkerController sets the WorkerController for worker management
// endpoints (restart, cancel).
func WithWorkerController(wc WorkerController) HandlerOption {
	return func(h *Handler) { h.workers = wc }
}

// WithGCStatsProvider wires in the watchdog (or any GCStatsProvider) so the
// /api/state response includes cumulative GC counters and the configured
// retention policy.
func WithGCStatsProvider(p GCStatsProvider) HandlerOption {
	return func(h *Handler) { h.gcStats = p }
}

// WithLogger attaches a logger for handler-level operations (cancel/prune
// audit lines). When nil, those operations are silent.
func WithLogger(l *log.Logger) HandlerOption {
	return func(h *Handler) { h.logger = l }
}

// WithHeartbeatThreshold sets the reachability threshold for heartbeat age,
// matching the watchdog's worker_timeout_seconds from config.
func WithHeartbeatThreshold(d time.Duration) HandlerOption {
	return func(h *Handler) { h.heartbeatThreshold = d }
}

func (h *Handler) logf(format string, args ...any) {
	if h.logger == nil {
		return
	}
	h.logger.Printf(format, args...)
}

func (h *Handler) heartbeatReachableThreshold() time.Duration {
	if h.heartbeatThreshold > 0 {
		return h.heartbeatThreshold
	}
	return 5 * time.Minute
}

// RegisterRoutes adds dashboard routes to the given mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/state", h.handleAPIState)
	mux.HandleFunc("/api/reset", h.handleAPIReset)
	mux.HandleFunc("/api/restart-workers", h.handleAPIRestartWorkers)
	mux.HandleFunc("/api/switch-project", h.handleAPISwitchProject)
	mux.HandleFunc("/api/cancel-agent", h.handleAPICancelAgent)
	mux.HandleFunc("/api/prune", h.handleAPIPrune)
	mux.HandleFunc("/api/pool-status", h.handleAPIPoolStatus)
	mux.HandleFunc("/api/send-message", h.handleAPISendMessage)
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
		// Identify worker instance IDs so they only appear in the Workers section
		workerIDs := make(map[string]bool)
		for id, inst := range state.AgentInstances {
			if inst != nil && inst.Role == domain.RoleWorker {
				workerIDs[id] = true
			}
		}

		// ── Agents: merge presence + instances (excluding worker instances) ──
		agentsSeen := make(map[string]bool)
		for name, p := range state.Presence {
			if workerIDs[name] {
				continue
			}
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
				a.CurrentTasks = inst.CurrentTasks
				a.Progress = inst.Progress
				a.ProgressStep = inst.ProgressStep
				a.ProgressTotalSteps = inst.ProgressTotalSteps
				if !inst.ProgressUpdatedAt.IsZero() {
					a.ProgressAge = relTime(inst.ProgressUpdatedAt, now)
				}
			}
			applyDeliveryGrace(&a, state, name, now)
			hbThresh := h.heartbeatReachableThreshold()
			a.Reachable = a.Connected
			if !a.Reachable {
				if inst, ok := state.AgentInstances[name]; ok && inst != nil &&
					!inst.LastHeartbeat.IsZero() && now.Sub(inst.LastHeartbeat) < hbThresh {
					a.Reachable = true
				}
				if !a.Reachable && !p.LastSeen.IsZero() && now.Sub(p.LastSeen) < 2*time.Minute &&
					p.Status != "" && p.Status != "offline" {
					a.Reachable = true
				}
			}
			snap.Agents = append(snap.Agents, a)
			agentsSeen[name] = true
		}
		for id, inst := range state.AgentInstances {
			if inst == nil || agentsSeen[id] || workerIDs[id] {
				continue
			}
			a := AgentSnapshot{
				Name:               id,
				Status:             inst.Status,
				Role:               string(inst.Role),
				LastHeartbeat:      relTime(inst.LastHeartbeat, now),
				Connected:          connectedAgents[id],
				CurrentTasks:       inst.CurrentTasks,
				Progress:           inst.Progress,
				ProgressStep:       inst.ProgressStep,
				ProgressTotalSteps: inst.ProgressTotalSteps,
			}
			if !inst.ProgressUpdatedAt.IsZero() {
				a.ProgressAge = relTime(inst.ProgressUpdatedAt, now)
			}
			applyDeliveryGrace(&a, state, id, now)
			a.Reachable = a.Connected
			if !a.Reachable && !inst.LastHeartbeat.IsZero() && now.Sub(inst.LastHeartbeat) < h.heartbeatReachableThreshold() {
				a.Reachable = true
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
		snap.TotalTasks = len(state.Tasks)
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
		snap.TotalMessages = len(state.Messages)
		msgStart := 0
		if len(state.Messages) > 30 {
			msgStart = len(state.Messages) - 30
		}
		for i := len(state.Messages) - 1; i >= msgStart; i-- {
			m := state.Messages[i]
			tsFmt := "15:04:05"
			if m.Timestamp.Day() != now.Day() || m.Timestamp.Month() != now.Month() || m.Timestamp.Year() != now.Year() {
				tsFmt = "Jan 2 15:04"
			}
			snap.Messages = append(snap.Messages, MessageSnapshot{
				ID:        m.ID,
				From:      m.From,
				To:        m.To,
				Content:   m.Content,
				Timestamp: m.Timestamp.Format(tsFmt),
				Read:      m.Read,
				Age:       relTime(m.Timestamp, now),
				Recovered: strings.HasPrefix(m.Content, recoveredMessagePrefix),
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
			reachable := connectedAgents[id]
			if !reachable && !inst.LastHeartbeat.IsZero() && now.Sub(inst.LastHeartbeat) < h.heartbeatReachableThreshold() {
				reachable = true
			}
			ws := WorkerSnapshot{
				InstanceID:         id,
				AgentType:          inst.AgentType,
				Status:             inst.Status,
				CurrentTasks:       inst.CurrentTasks,
				LastHeartbeat:      relTime(inst.LastHeartbeat, now),
				Reachable:          reachable,
				Progress:           inst.Progress,
				ProgressStep:       inst.ProgressStep,
				ProgressTotalSteps: inst.ProgressTotalSteps,
			}
			if !inst.ProgressUpdatedAt.IsZero() {
				ws.ProgressAge = relTime(inst.ProgressUpdatedAt, now)
			}
			if app.IsTaskBoundInstance(state, id) {
				ws.IsTaskBound = true
				ws.BoundTaskID = parseBoundTaskID(id)
			}
			// Delivery grace is keyed by the agent name used in send_message.
			// Workers usually send as their AgentType (e.g. "claude-code"),
			// not the instance ID — fall back to the type when no instance-
			// keyed entry exists.
			if lastSend, ok := lookupLastSend(state, id, inst.AgentType); ok {
				ws.LastSendAge = relTime(lastSend, now)
				if now.Sub(lastSend) < recentSendGraceWindow {
					ws.InDeliveryGrace = true
				}
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

	// GC observability: surface cumulative watchdog counters + retention so
	// the dashboard can render a one-line "GC: 12+7 pruned, last run 3m ago"
	// strip without separate polling.
	if h.gcStats != nil {
		stats := h.gcStats.GCStats()
		gc := &GCSnapshot{
			PresencePrunedTotal:  stats.PresencePrunedTotal,
			InstancesPrunedTotal: stats.InstancesPrunedTotal,
		}
		if !stats.LastRun.IsZero() {
			gc.LastRun = relTime(stats.LastRun, now)
		}
		if pol := h.svc.Policy(); pol != nil {
			gc.PresenceRetentionDays = pol.PresenceRetentionDays()
			gc.InstanceRetentionDays = pol.InstanceRetentionDays()
			gc.TaskBoundInstanceRetentionHours = pol.TaskBoundInstanceRetentionHours()
		}
		snap.GC = gc
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(snap)
}

// ── Admin endpoints (CLI-parity) ─────────────────────────────────────────
//
// These mirror the cancel_agent / send_message MCP tools and the admin
// CLI's prune / pool-status subcommands. Kept here (rather than imported
// from collab) so the dashboard package stays free of mcp-go dependencies.

// CancelAgentRequest is the JSON body for POST /api/cancel-agent.
type CancelAgentRequest struct {
	Agent       string `json:"agent"`
	CancelledBy string `json:"cancelled_by"`
	Reason      string `json:"reason"`
}

// CancelAgentResponse is the JSON response from POST /api/cancel-agent.
type CancelAgentResponse struct {
	Status         string `json:"status"`
	Agent          string `json:"agent"`
	CancelledTasks []int  `json:"cancelled_tasks"`
	ProcessKilled  bool   `json:"process_killed"`
	RecoveredFrom  string `json:"recovered_from,omitempty"`
}

func (h *Handler) handleAPICancelAgent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var req CancelAgentRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	req.Agent = strings.TrimSpace(req.Agent)
	req.CancelledBy = strings.TrimSpace(req.CancelledBy)
	if req.Agent == "" || req.CancelledBy == "" {
		writeJSONError(w, http.StatusBadRequest, "agent and cancelled_by are required")
		return
	}

	// Capture output before mutating state so the synthetic recovery
	// message reflects what the worker actually had buffered.
	var capturedOutput string
	if h.workers != nil {
		capturedOutput = h.workers.GetRecentOutput(req.Agent)
	}

	var (
		cancelledTasks []int
		recoveredFrom  string
		stateErr       error
	)
	stateErr = h.svc.Run(func(state *domain.CollabState) error {
		extra := app.RegisteredAgentNames(state)
		if err := app.ValidateAgent(req.CancelledBy, state, false, false, extra...); err != nil {
			return err
		}
		if err := app.ValidateAgent(req.Agent, state, false, false, extra...); err != nil {
			return err
		}

		now := time.Now()

		agentType := app.ResolveParentAgentType(state, req.Agent)
		for i := range state.Tasks {
			t := &state.Tasks[i]
			if t.Status != "in_progress" {
				continue
			}
			if t.AssignedTo != req.Agent && t.AssignedTo != agentType {
				continue
			}
			t.Status = "cancelled"
			t.UpdatedAt = now
			if req.Reason != "" {
				t.ResultSummary = fmt.Sprintf("Cancelled by %s: %s", req.CancelledBy, req.Reason)
			} else {
				t.ResultSummary = fmt.Sprintf("Cancelled by %s", req.CancelledBy)
			}
			cancelledTasks = append(cancelledTasks, t.ID)
			if capturedOutput != "" {
				app.SaveOutputToWorkContext(state, t.ID, capturedOutput, req.Agent, t.ProgressDescription, h.logger)
			}
			removeTaskFromInstanceCancel(state, t.ID, t.AssignedTo)
		}

		// Reap task-bound rows; idle out static-pool rows (matches
		// cancel_agent MCP tool semantics).
		var toReap []string
		if inst, ok := state.AgentInstances[req.Agent]; ok && inst != nil {
			if app.IsTaskBoundInstance(state, req.Agent) {
				toReap = append(toReap, req.Agent)
			} else {
				inst.CurrentTasks = nil
				inst.Status = "idle"
			}
		}
		for id, inst := range state.AgentInstances {
			if inst == nil || inst.AgentType != req.Agent || id == req.Agent {
				continue
			}
			if app.IsTaskBoundInstance(state, id) {
				toReap = append(toReap, id)
			} else {
				inst.CurrentTasks = nil
				inst.Status = "idle"
			}
		}
		for _, id := range toReap {
			delete(state.AgentInstances, id)
			delete(state.Presence, id)
		}

		stopContent := fmt.Sprintf("🛑 **STOP**: %s has cancelled your work.", req.CancelledBy)
		if req.Reason != "" {
			stopContent += fmt.Sprintf(" Reason: %s.", req.Reason)
		}
		if len(cancelledTasks) > 0 {
			ids := make([]string, len(cancelledTasks))
			for i, id := range cancelledTasks {
				ids[i] = fmt.Sprintf("#%d", id)
			}
			stopContent += fmt.Sprintf(" Cancelled tasks: %s.", strings.Join(ids, ", "))
		}
		stopContent += " **Stop all work immediately and exit.**"
		state.Messages = append(state.Messages, domain.Message{
			ID: state.NextMsgID, From: "system", To: req.Agent,
			Content: stopContent, Timestamp: now,
		})
		state.NextMsgID++

		// Synthetic deliverable recovery: same logic as cancel_agent tool.
		if capturedOutput != "" {
			recentlySent := false
			if state.LastSendByAgent != nil {
				if lastSend, ok := state.LastSendByAgent[req.Agent]; ok && !lastSend.IsZero() {
					if now.Sub(lastSend) < time.Hour {
						recentlySent = true
					}
				}
			}
			if !recentlySent {
				cutoff := now.Add(-time.Hour)
				for i := len(state.Messages) - 1; i >= 0; i-- {
					m := state.Messages[i]
					if m.Timestamp.Before(cutoff) {
						break
					}
					if m.From == req.Agent {
						recentlySent = true
						break
					}
				}
			}
			if !recentlySent {
				driver := app.ConfiguredDriver(state)
				if driver == "" {
					driver = req.CancelledBy
				}
				const maxRecoveryBytes = 4096
				tail := capturedOutput
				truncated := false
				if len(tail) > maxRecoveryBytes {
					tail = tail[len(tail)-maxRecoveryBytes:]
					truncated = true
				}
				body := recoveredMessagePrefix + "\n\n```\n"
				if truncated {
					body += "...(truncated)...\n"
				}
				body += tail + "\n```"
				state.Messages = append(state.Messages, domain.Message{
					ID: state.NextMsgID, From: req.Agent, To: driver,
					Content: body, Timestamp: now,
				})
				state.NextMsgID++
				recoveredFrom = req.Agent
			}
		}
		return nil
	})

	if stateErr != nil {
		writeJSONError(w, http.StatusBadRequest, stateErr.Error())
		return
	}

	processKilled := false
	if h.workers != nil {
		processKilled = h.workers.CancelWorker(req.Agent)
	}

	h.logf("dashboard cancel_agent: %s cancelled %s (tasks=%d, killed=%v, recovered=%v)",
		req.CancelledBy, req.Agent, len(cancelledTasks), processKilled, recoveredFrom != "")

	resp := CancelAgentResponse{
		Status:         "ok",
		Agent:          req.Agent,
		CancelledTasks: cancelledTasks,
		ProcessKilled:  processKilled,
		RecoveredFrom:  recoveredFrom,
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(resp)
}

// PruneRequest is the JSON body for POST /api/prune.
//
// All fields are optional; defaults match `mcp-stringwork admin prune`:
// presence + instances both pruned, dry_run=false, retention from policy.
// When OlderThanHours > 0 it overrides both presence and instance days
// (presence becomes 0d → skipped, instances/task-bound use the hours value).
type PruneRequest struct {
	Presence            *bool `json:"presence,omitempty"`
	Instances           *bool `json:"instances,omitempty"`
	OlderThanDays       int   `json:"older_than_days,omitempty"`
	OlderThanHours      int   `json:"older_than_hours,omitempty"`
	TaskBoundOlderHours int   `json:"task_bound_older_hours,omitempty"`
	DryRun              bool  `json:"dry_run,omitempty"`
}

// PruneResponse is the JSON response from POST /api/prune.
type PruneResponse struct {
	Status                          string `json:"status"`
	DryRun                          bool   `json:"dry_run"`
	PresencePruned                  int    `json:"presence_pruned"`
	InstancesPruned                 int    `json:"instances_pruned"`
	PresenceRetentionDays           int    `json:"presence_retention_days"`
	InstanceRetentionDays           int    `json:"instance_retention_days"`
	TaskBoundInstanceRetentionHours int    `json:"task_bound_instance_retention_hours"`
}

func (h *Handler) handleAPIPrune(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var req PruneRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	prunePresence := req.Presence == nil || *req.Presence
	pruneInstances := req.Instances == nil || *req.Instances
	if !prunePresence && !pruneInstances {
		writeJSONError(w, http.StatusBadRequest, "at least one of presence/instances must be enabled")
		return
	}

	pol := h.svc.Policy()
	presenceDays := pol.PresenceRetentionDays()
	instanceDays := pol.InstanceRetentionDays()
	taskBoundHours := pol.TaskBoundInstanceRetentionHours()
	if req.OlderThanDays > 0 {
		presenceDays = req.OlderThanDays
		instanceDays = req.OlderThanDays
	}
	if req.OlderThanHours > 0 {
		// Sub-day overrides match the CLI: skip presence (days==0), use
		// hours for task-bound only.
		presenceDays = 0
		instanceDays = 0
		taskBoundHours = req.OlderThanHours
	}
	if req.TaskBoundOlderHours > 0 {
		taskBoundHours = req.TaskBoundOlderHours
	}

	resp := PruneResponse{
		Status:                          "ok",
		DryRun:                          req.DryRun,
		PresenceRetentionDays:           presenceDays,
		InstanceRetentionDays:           instanceDays,
		TaskBoundInstanceRetentionHours: taskBoundHours,
	}

	if req.DryRun {
		// Dry run: clone state via Query so we don't write anything back.
		_ = h.svc.Query(func(state *domain.CollabState) error {
			clone := clonePruneState(state)
			if prunePresence {
				resp.PresencePruned = app.PrunePresence(clone, presenceDays)
			}
			if pruneInstances {
				resp.InstancesPruned = app.PruneInstances(clone, instanceDays, taskBoundHours)
			}
			return nil
		})
	} else {
		_ = h.svc.Run(func(state *domain.CollabState) error {
			if prunePresence {
				resp.PresencePruned = app.PrunePresence(state, presenceDays)
			}
			if pruneInstances {
				resp.InstancesPruned = app.PruneInstances(state, instanceDays, taskBoundHours)
			}
			return nil
		})
	}

	h.logf("dashboard prune: dry_run=%v presence=%d instances=%d (retention p=%dd i=%dd tb=%dh)",
		req.DryRun, resp.PresencePruned, resp.InstancesPruned,
		presenceDays, instanceDays, taskBoundHours)

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(resp)
}

// PoolStatusResponse is the JSON response from GET /api/pool-status.
//
// Mirrors the `admin pool-status` CLI subcommand — quick "is the pool
// healthy?" snapshot for the dashboard's pool panel. All ages are
// rendered as relative strings ("3m 12s ago") for direct UI display.
type PoolStatusResponse struct {
	Driver                string             `json:"driver"`
	TotalInstances        int                `json:"total_instances"`
	ActiveInstances       int                `json:"active_instances"`
	OfflineInstances      int                `json:"offline_instances"`
	TaskBoundIdleRows     int                `json:"task_bound_idle_rows"`
	OldestActive          *PoolInstanceRow   `json:"oldest_active,omitempty"`
	OldestOffline         *PoolInstanceRow   `json:"oldest_offline,omitempty"`
	TotalPresence         int                `json:"total_presence"`
	StalePresence         int                `json:"stale_presence"`
	StalePresenceCutoffH  int                `json:"stale_presence_cutoff_hours"`
	OldestPresence        *PoolPresenceRow   `json:"oldest_presence,omitempty"`
	InFlightTasks         []PoolInFlightTask `json:"in_flight_tasks,omitempty"`
	InFlightTaskCount     int                `json:"in_flight_task_count"`
	GeneratedAt           string             `json:"generated_at"`
	WorkerStatusByType    map[string]int     `json:"worker_status_by_type,omitempty"`
	WorkerOfflineByType   map[string]int     `json:"worker_offline_by_type,omitempty"`
	WorkerTaskBoundByType map[string]int     `json:"worker_task_bound_by_type,omitempty"`
}

// PoolInstanceRow is a single oldest-active/offline instance summary.
type PoolInstanceRow struct {
	InstanceID   string `json:"instance_id"`
	AgentType    string `json:"agent_type"`
	HeartbeatAge string `json:"heartbeat_age"`
	IsTaskBound  bool   `json:"is_task_bound,omitempty"`
}

// PoolPresenceRow is a single oldest-presence summary.
type PoolPresenceRow struct {
	Agent       string `json:"agent"`
	LastSeenAge string `json:"last_seen_age"`
}

// PoolInFlightTask is a single in-flight task summary.
type PoolInFlightTask struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
	Owner string `json:"owner"`
	Age   string `json:"age"`
}

func (h *Handler) handleAPIPoolStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}

	now := time.Now()
	resp := PoolStatusResponse{
		GeneratedAt:           now.Format(time.RFC3339),
		StalePresenceCutoffH:  24,
		WorkerStatusByType:    make(map[string]int),
		WorkerOfflineByType:   make(map[string]int),
		WorkerTaskBoundByType: make(map[string]int),
	}

	_ = h.svc.Query(func(state *domain.CollabState) error {
		resp.Driver = app.ConfiguredDriver(state)
		resp.TotalInstances = len(state.AgentInstances)
		resp.TotalPresence = len(state.Presence)

		var oldestActive, oldestOffline *domain.AgentInstance
		for id, inst := range state.AgentInstances {
			if inst == nil || inst.Role == domain.RoleDriver {
				continue
			}
			tb := app.IsTaskBoundInstance(state, id)
			if tb && len(inst.CurrentTasks) == 0 {
				resp.TaskBoundIdleRows++
				resp.WorkerTaskBoundByType[inst.AgentType]++
			}
			if inst.Status == "offline" {
				resp.OfflineInstances++
				resp.WorkerOfflineByType[inst.AgentType]++
				if oldestOffline == nil || inst.LastHeartbeat.Before(oldestOffline.LastHeartbeat) {
					oldestOffline = inst
				}
			} else {
				resp.ActiveInstances++
				resp.WorkerStatusByType[inst.AgentType]++
				if oldestActive == nil || inst.LastHeartbeat.Before(oldestActive.LastHeartbeat) {
					oldestActive = inst
				}
			}
		}
		if oldestActive != nil {
			resp.OldestActive = &PoolInstanceRow{
				InstanceID:   oldestActive.InstanceID,
				AgentType:    oldestActive.AgentType,
				HeartbeatAge: relTime(oldestActive.LastHeartbeat, now),
				IsTaskBound:  app.IsTaskBoundInstance(state, oldestActive.InstanceID),
			}
		}
		if oldestOffline != nil {
			resp.OldestOffline = &PoolInstanceRow{
				InstanceID:   oldestOffline.InstanceID,
				AgentType:    oldestOffline.AgentType,
				HeartbeatAge: relTime(oldestOffline.LastHeartbeat, now),
				IsTaskBound:  app.IsTaskBoundInstance(state, oldestOffline.InstanceID),
			}
		}

		stalePresenceCut := time.Duration(resp.StalePresenceCutoffH) * time.Hour
		var oldestPresence *domain.Presence
		for agent, p := range state.Presence {
			if p == nil || agent == resp.Driver {
				continue
			}
			if !p.LastSeen.IsZero() && now.Sub(p.LastSeen) > stalePresenceCut {
				resp.StalePresence++
			}
			if oldestPresence == nil || p.LastSeen.Before(oldestPresence.LastSeen) {
				oldestPresence = p
			}
		}
		if oldestPresence != nil {
			resp.OldestPresence = &PoolPresenceRow{
				Agent:       oldestPresence.Agent,
				LastSeenAge: relTime(oldestPresence.LastSeen, now),
			}
		}

		for _, t := range state.Tasks {
			if t.Status != "in_progress" {
				continue
			}
			resp.InFlightTasks = append(resp.InFlightTasks, PoolInFlightTask{
				ID:    t.ID,
				Title: truncate(t.Title, 80),
				Owner: t.AssignedTo,
				Age:   relTime(t.UpdatedAt, now),
			})
		}
		resp.InFlightTaskCount = len(resp.InFlightTasks)
		sort.Slice(resp.InFlightTasks, func(i, j int) bool {
			return resp.InFlightTasks[i].ID < resp.InFlightTasks[j].ID
		})
		return nil
	})

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(resp)
}

// SendMessageRequest is the JSON body for POST /api/send-message.
type SendMessageRequest struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Content string `json:"content"`
}

// SendMessageResponse is the JSON response from POST /api/send-message.
type SendMessageResponse struct {
	Status string `json:"status"`
	ID     int    `json:"id"`
}

func (h *Handler) handleAPISendMessage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var req SendMessageRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	req.From = strings.TrimSpace(req.From)
	req.To = strings.TrimSpace(req.To)
	if req.From == "" || req.To == "" || req.Content == "" {
		writeJSONError(w, http.StatusBadRequest, "from, to, and content are required")
		return
	}

	// Auth gate: the dashboard has no real auth, so we only allow the
	// configured driver to send. This prevents UI users from impersonating
	// a worker (which would short-circuit the watchdog's recent-send grace
	// window and confuse the audit trail).
	var (
		msgID    int
		stateErr error
	)
	stateErr = h.svc.Run(func(state *domain.CollabState) error {
		driver := app.ConfiguredDriver(state)
		if req.From != driver {
			return fmt.Errorf("from must equal configured driver %q (UI cannot impersonate workers)", driver)
		}
		extra := app.RegisteredAgentNames(state)
		if err := app.ValidateAgent(req.From, state, false, false, extra...); err != nil {
			return err
		}
		if err := app.ValidateAgent(req.To, state, false, true, extra...); err != nil {
			return err
		}

		now := time.Now()
		msg := domain.Message{
			ID:        state.NextMsgID,
			From:      req.From,
			To:        req.To,
			Content:   req.Content,
			Timestamp: now,
			Read:      false,
		}
		state.Messages = append(state.Messages, msg)
		msgID = state.NextMsgID
		state.NextMsgID++

		if state.LastSendByAgent == nil {
			state.LastSendByAgent = make(map[string]time.Time)
		}
		state.LastSendByAgent[req.From] = now
		return nil
	})
	if stateErr != nil {
		// "from must equal driver" is a 403; agent validation errors are 400.
		status := http.StatusBadRequest
		if strings.Contains(stateErr.Error(), "configured driver") {
			status = http.StatusForbidden
		}
		writeJSONError(w, status, stateErr.Error())
		return
	}

	h.logf("dashboard send_message: %s -> %s (#%d)", req.From, req.To, msgID)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(SendMessageResponse{Status: "ok", ID: msgID})
}

// writeJSONError writes a {"error":"..."} response with the given status.
func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	_ = enc.Encode(map[string]string{"error": msg})
}

// removeTaskFromInstanceCancel mirrors the helper in collab/tasks.go;
// duplicated here so the dashboard package can stay free of the collab
// import (which would pull in mcp-go server bindings into the HTTP layer).
func removeTaskFromInstanceCancel(state *domain.CollabState, taskID int, agent string) {
	if state == nil {
		return
	}
	inst, ok := state.AgentInstances[agent]
	if !ok || inst == nil {
		for _, candidate := range state.AgentInstances {
			if candidate != nil && candidate.AgentType == agent {
				inst = candidate
				break
			}
		}
	}
	if inst == nil {
		return
	}
	filtered := inst.CurrentTasks[:0]
	for _, id := range inst.CurrentTasks {
		if id != taskID {
			filtered = append(filtered, id)
		}
	}
	inst.CurrentTasks = filtered
}

// clonePruneState shallow-copies the maps PrunePresence/PruneInstances
// touch so dry-run pruning doesn't mutate live state. Mirrors the helper
// in cmd/mcp-server/cli_admin.go.
func clonePruneState(state *domain.CollabState) *domain.CollabState {
	if state == nil {
		return domain.NewCollabState()
	}
	clone := *state
	clone.Presence = make(map[string]*domain.Presence, len(state.Presence))
	for k, v := range state.Presence {
		clone.Presence[k] = v
	}
	clone.AgentInstances = make(map[string]*domain.AgentInstance, len(state.AgentInstances))
	for k, v := range state.AgentInstances {
		clone.AgentInstances[k] = v
	}
	return &clone
}

// applyDeliveryGrace populates LastSendAge / InDeliveryGrace on an agent
// snapshot from state.LastSendByAgent.
func applyDeliveryGrace(a *AgentSnapshot, state *domain.CollabState, name string, now time.Time) {
	if a == nil || state == nil {
		return
	}
	lastSend, ok := state.LastSendByAgent[name]
	if !ok || lastSend.IsZero() {
		return
	}
	a.LastSendAge = relTime(lastSend, now)
	if now.Sub(lastSend) < recentSendGraceWindow {
		a.InDeliveryGrace = true
	}
}

// lookupLastSend resolves the most recent send_message timestamp for either
// a worker's instance ID or its agent type, whichever is present. Workers
// usually send as their AgentType, but bookkeeping for instance-keyed sends
// is honoured first.
func lookupLastSend(state *domain.CollabState, instanceID, agentType string) (time.Time, bool) {
	if state == nil {
		return time.Time{}, false
	}
	if t, ok := state.LastSendByAgent[instanceID]; ok && !t.IsZero() {
		return t, true
	}
	if agentType != "" && agentType != instanceID {
		if t, ok := state.LastSendByAgent[agentType]; ok && !t.IsZero() {
			return t, true
		}
	}
	return time.Time{}, false
}

// parseBoundTaskID extracts the task ID from a task-bound instance ID like
// "claude-code-task-7". Returns 0 when the suffix isn't present or isn't a
// number — caller treats 0 as "unknown" (omitempty in JSON).
func parseBoundTaskID(instanceID string) int {
	idx := strings.LastIndex(instanceID, "-task-")
	if idx < 0 {
		return 0
	}
	tail := instanceID[idx+len("-task-"):]
	n, err := strconv.Atoi(tail)
	if err != nil || n < 0 {
		return 0
	}
	return n
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
