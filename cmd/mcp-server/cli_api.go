package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jaakkos/stringwork/internal/app"
	"github.com/jaakkos/stringwork/internal/domain"
	"github.com/jaakkos/stringwork/internal/tools/collab"
)

// workerAPI exposes lightweight REST endpoints for worker agents that use CLI
// communication instead of MCP. Handlers call the same CollabService that MCP
// tool handlers use, so state is consistent.
type workerAPI struct {
	svc               *app.CollabService
	registry          *app.SessionRegistry
	logger            *log.Logger
	spawner           taskSpawner
	sessionIDRecorder interface {
		SetWorkerSessionID(instanceID, sessionID string)
	}
}

// taskSpawner mirrors collab.TaskSpawner so CLI handlers can request a fresh
// worker process when a task is reassigned. Defined locally so this file
// stays compileable without the collab import already present.
type taskSpawner interface {
	SpawnForTask(taskID int, assignedTo string)
}

func newWorkerAPI(svc *app.CollabService, registry *app.SessionRegistry, logger *log.Logger) *workerAPI {
	return &workerAPI{svc: svc, registry: registry, logger: logger}
}

// touchCLISession creates or refreshes a synthetic session entry for a CLI worker
// so that session-based liveness checks (isAgentAlive, pruneStaleSessions) work
// uniformly for both MCP and CLI workers.
func (w *workerAPI) touchCLISession(agent string) {
	if w.registry == nil {
		return
	}
	sessionID := "cli-" + agent
	if !w.registry.HasActiveSession(agent) {
		w.registry.SetAgent(sessionID, agent)
	}
	w.registry.TouchSession(sessionID)
}

// RegisterRoutes mounts all worker API endpoints on the given mux.
func (w *workerAPI) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/w/heartbeat", w.handleHeartbeat)
	mux.HandleFunc("/api/w/progress", w.handleProgress)
	mux.HandleFunc("/api/w/send", w.handleSend)
	mux.HandleFunc("/api/w/task/update", w.handleTaskUpdate)
	mux.HandleFunc("/api/w/task/list", w.handleTaskList)
	mux.HandleFunc("/api/w/messages", w.handleMessages)
	mux.HandleFunc("/api/w/presence", w.handlePresence)
	mux.HandleFunc("/api/w/context", w.handleContext)
	mux.HandleFunc("/api/w/work-context", w.handleWorkContext)
}

// --- Handlers ---

func (w *workerAPI) handleHeartbeat(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Agent      string `json:"agent"`
		Progress   string `json:"progress"`
		Step       int    `json:"step"`
		TotalSteps int    `json:"total_steps"`
		SessionID  string `json:"session_id"`
	}
	if !decodeJSON(rw, r, &req) {
		return
	}
	if req.Agent == "" {
		writeError(rw, "agent is required")
		return
	}

	err := w.svc.Run(func(state *domain.CollabState) error {
		resolveWorkerAgent(state, req.Agent)
		inst := findInstance(state, req.Agent)
		if inst == nil {
			return fmt.Errorf("unknown agent %q", req.Agent)
		}
		now := time.Now()
		inst.LastHeartbeat = now
		if inst.Status == "offline" {
			inst.Status = "idle"
		}
		if req.Progress != "" {
			inst.Progress = req.Progress
			inst.ProgressUpdatedAt = now
		}
		if req.Step > 0 {
			inst.ProgressStep = req.Step
		}
		if req.TotalSteps > 0 {
			inst.ProgressTotalSteps = req.TotalSteps
		}
		if req.SessionID != "" {
			inst.SessionID = req.SessionID
		}
		return nil
	})
	if err != nil {
		writeError(rw, err.Error())
		return
	}
	w.touchCLISession(req.Agent)
	if req.SessionID != "" && w.sessionIDRecorder != nil {
		w.sessionIDRecorder.SetWorkerSessionID(req.Agent, req.SessionID)
	}
	if req.Progress != "" {
		w.logger.Printf("heartbeat from %s (progress: %s)", req.Agent, req.Progress)
	} else {
		w.logger.Printf("heartbeat from %s", req.Agent)
	}
	w.writeWithBanner(rw, "OK", req.Agent, "heartbeat")
}

func (w *workerAPI) handleProgress(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Agent           string `json:"agent"`
		TaskID          int    `json:"task_id"`
		Description     string `json:"description"`
		PercentComplete int    `json:"percent_complete"`
		ETASeconds      int    `json:"eta_seconds"`
	}
	req.PercentComplete = -1
	if !decodeJSON(rw, r, &req) {
		return
	}
	if req.Agent == "" || req.TaskID == 0 || req.Description == "" {
		writeError(rw, "agent, task_id, and description are required")
		return
	}

	err := w.svc.Run(func(state *domain.CollabState) error {
		now := time.Now()
		taskFound := false
		for i := range state.Tasks {
			t := &state.Tasks[i]
			if t.ID != req.TaskID {
				continue
			}
			taskFound = true
			if t.Status != "in_progress" {
				return fmt.Errorf("task #%d is not in_progress (status: %s)", req.TaskID, t.Status)
			}
			t.ProgressDescription = req.Description
			t.LastProgressAt = now
			if req.PercentComplete >= 0 {
				pc := req.PercentComplete
				if pc > 100 {
					pc = 100
				}
				t.ProgressPercent = pc
			}
			break
		}
		if !taskFound {
			return fmt.Errorf("task #%d not found", req.TaskID)
		}

		// Mirror handleHeartbeat: a worker that calls `progress` before
		// its first `heartbeat` has no AgentInstance row yet. Without
		// resolveWorkerAgent here, findInstance returns nil, the
		// inst-level LastHeartbeat / Progress updates silently no-op,
		// and the watchdog (worker_status pane, SLA timers) reads
		// stale values — defeating the very liveness signal
		// report_progress is documented to provide.
		resolveWorkerAgent(state, req.Agent)
		inst := findInstance(state, req.Agent)
		if inst != nil {
			inst.LastHeartbeat = now
			inst.Progress = req.Description
			inst.ProgressUpdatedAt = now
			if inst.Status == "offline" {
				inst.Status = "busy"
			}
		}
		return nil
	})
	if err != nil {
		writeError(rw, err.Error())
		return
	}

	w.touchCLISession(req.Agent)
	response := fmt.Sprintf("Progress recorded for task #%d", req.TaskID)
	if req.PercentComplete >= 0 {
		response += fmt.Sprintf(" (%d%% complete)", req.PercentComplete)
	}
	if req.ETASeconds > 0 {
		response += fmt.Sprintf(", ETA: %s", collab.FormatDuration(req.ETASeconds))
	}
	w.logger.Printf("report_progress: task #%d by %s: %s", req.TaskID, req.Agent, req.Description)
	w.writeWithBanner(rw, response, req.Agent, "report_progress")
}

func (w *workerAPI) handleSend(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		From    string `json:"from"`
		To      string `json:"to"`
		Content string `json:"content"`
	}
	if !decodeJSON(rw, r, &req) {
		return
	}
	if req.From == "" || req.To == "" || req.Content == "" {
		writeError(rw, "from, to, and content are required")
		return
	}

	var msgID int
	err := w.svc.Run(func(state *domain.CollabState) error {
		resolveWorkerAgent(state, req.From)
		extra := app.RegisteredAgentNames(state)
		if err := app.ValidateAgent(req.From, state, false, false, extra...); err != nil {
			return err
		}
		if err := app.ValidateAgent(req.To, state, false, true, extra...); err != nil {
			return err
		}
		msg := domain.Message{
			ID:        state.NextMsgID,
			From:      req.From,
			To:        req.To,
			Content:   req.Content,
			Timestamp: time.Now(),
			Read:      false,
		}
		state.Messages = append(state.Messages, msg)
		msgID = state.NextMsgID
		state.NextMsgID++
		pruned := app.PruneMessages(state, w.svc.Policy().MessageRetentionMax(), w.svc.Policy().MessageRetentionDays())
		if pruned > 0 {
			w.logger.Printf("Pruned %d old messages", pruned)
		}
		return nil
	})
	if err != nil {
		writeError(rw, err.Error())
		return
	}

	w.logger.Printf("Message sent from %s to %s", req.From, req.To)
	w.writeWithBanner(rw, fmt.Sprintf("Message #%d sent to %s", msgID, req.To), req.From, "send_message")
}

func (w *workerAPI) handleTaskUpdate(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID         int    `json:"id"`
		UpdatedBy  string `json:"updated_by"`
		Status     string `json:"status"`
		AssignedTo string `json:"assigned_to"`
	}
	if !decodeJSON(rw, r, &req) {
		return
	}
	if req.ID == 0 || req.UpdatedBy == "" {
		writeError(rw, "id and updated_by are required")
		return
	}
	if req.Status != "" {
		valid := map[string]bool{"pending": true, "in_progress": true, "completed": true, "blocked": true, "cancelled": true}
		if !valid[req.Status] {
			writeError(rw, fmt.Sprintf("invalid status %q (must be pending, in_progress, completed, blocked, or cancelled)", req.Status))
			return
		}
	}

	var result *app.ApplyTaskTransitionResult
	var taskTitle string
	err := w.svc.Run(func(state *domain.CollabState) error {
		resolveWorkerAgent(state, req.UpdatedBy)
		extra := app.RegisteredAgentNames(state)
		if err := app.ValidateAgent(req.UpdatedBy, state, false, false, extra...); err != nil {
			return err
		}
		if req.AssignedTo != "" {
			if err := app.ValidateAgent(req.AssignedTo, state, true, false, extra...); err != nil {
				return err
			}
		}

		res, err := app.ApplyTaskTransition(state, req.ID, app.ApplyTaskTransitionOpts{
			NewStatus:   req.Status,
			NewAssignee: req.AssignedTo,
			UpdatedBy:   req.UpdatedBy,
		})
		if err != nil {
			return err
		}
		result = res
		taskTitle = res.Task.Title

		if req.Status == "completed" && res.OldStatus != "completed" {
			driver := app.ConfiguredDriver(state)
			state.Messages = append(state.Messages, domain.Message{
				ID:        state.NextMsgID,
				From:      "system",
				To:        driver,
				Content:   fmt.Sprintf("Task #%d **%s** completed by %s", req.ID, taskTitle, req.UpdatedBy),
				Timestamp: time.Now(),
			})
			state.NextMsgID++
		}
		return nil
	})
	if err != nil {
		writeError(rw, err.Error())
		return
	}
	if w.spawner != nil && result != nil && result.NeedsSpawn {
		w.spawner.SpawnForTask(req.ID, result.SpawnAssignee)
	}
	w.logger.Printf("Task #%d updated by %s (status: %s)", req.ID, req.UpdatedBy, req.Status)
	w.writeWithBanner(rw, fmt.Sprintf("Task #%d updated", req.ID), req.UpdatedBy, "update_task")
}

func (w *workerAPI) handleTaskList(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(rw, "GET required", http.StatusMethodNotAllowed)
		return
	}
	statusFilter := r.URL.Query().Get("status")
	if statusFilter == "" {
		statusFilter = "all"
	}
	assignedFilter := r.URL.Query().Get("assigned_to")
	agent := r.URL.Query().Get("agent")

	var result string
	var count int
	_ = w.svc.Query(func(state *domain.CollabState) error {
		for _, task := range state.Tasks {
			if statusFilter != "all" && task.Status != statusFilter {
				continue
			}
			if assignedFilter != "" && task.AssignedTo != assignedFilter && task.AssignedTo != "any" {
				continue
			}
			result += fmt.Sprintf("Task #%d [%s] - %s\n", task.ID, task.Status, task.Title)
			if task.Description != "" {
				result += fmt.Sprintf("  Description: %s\n", task.Description)
			}
			if task.FailureCount > 0 {
				result += fmt.Sprintf("  Failures: %d (last: %s)\n", task.FailureCount, task.FailureReason)
			}
			result += fmt.Sprintf("  Assigned to: %s, Created by: %s\n\n", task.AssignedTo, task.CreatedBy)
			count++
		}
		return nil
	})

	if count == 0 {
		result = "No tasks found"
	}
	w.writeWithBanner(rw, result, agent, "list_tasks")
}

func (w *workerAPI) handleMessages(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(rw, "GET required", http.StatusMethodNotAllowed)
		return
	}
	recipient := r.URL.Query().Get("for")
	if recipient == "" {
		writeError(rw, "'for' is required")
		return
	}
	limit := 10
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			if n > 100 {
				n = 100
			}
			limit = n
		}
	}

	var messages []domain.Message
	err := w.svc.Run(func(state *domain.CollabState) error {
		resolveWorkerAgent(state, recipient)
		extra := app.RegisteredAgentNames(state)
		if err := app.ValidateAgent(recipient, state, false, false, extra...); err != nil {
			return err
		}
		collected := make([]domain.Message, 0, limit)
		for i := len(state.Messages) - 1; i >= 0 && len(collected) < limit; i-- {
			msg := state.Messages[i]
			if msg.To == recipient || msg.To == "all" {
				collected = append(collected, msg)
				state.Messages[i].Read = true
			}
		}
		messages = collected
		if len(messages) > 0 {
			if agentCtx, exists := state.AgentContexts[recipient]; exists {
				agentCtx.LastCheckedMsgID = state.NextMsgID - 1
				agentCtx.LastCheckTime = time.Now()
			} else {
				state.AgentContexts[recipient] = &domain.AgentContext{
					Agent:             recipient,
					LastCheckedMsgID:  state.NextMsgID - 1,
					LastCheckedTaskID: state.NextTaskID - 1,
					LastCheckTime:     time.Now(),
				}
			}
		}
		return nil
	})
	if err != nil {
		writeError(rw, err.Error())
		return
	}

	if len(messages) == 0 {
		writeText(rw, "No messages")
		return
	}
	var result string
	for _, msg := range messages {
		result += fmt.Sprintf("--- Message #%d from %s (%s) ---\n%s\n\n",
			msg.ID, msg.From, msg.Timestamp.Format("2006-01-02 15:04:05"), msg.Content)
	}
	writeText(rw, result)
}

func (w *workerAPI) handlePresence(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Agent     string `json:"agent"`
		Status    string `json:"status"`
		Workspace string `json:"workspace"`
	}
	if !decodeJSON(rw, r, &req) {
		return
	}
	if req.Agent == "" || req.Status == "" {
		writeError(rw, "agent and status are required")
		return
	}

	var workspaceChanged bool
	err := w.svc.Run(func(state *domain.CollabState) error {
		resolveWorkerAgent(state, req.Agent)
		extra := app.RegisteredAgentNames(state)
		if err := app.ValidateAgent(req.Agent, state, false, false, extra...); err != nil {
			return err
		}
		now := time.Now()
		presence := &domain.Presence{
			Agent:    req.Agent,
			Status:   req.Status,
			LastSeen: now,
		}
		if req.Workspace != "" {
			presence.Workspace = req.Workspace
			old := ""
			if existing, ok := state.Presence[req.Agent]; ok && existing != nil {
				old = existing.Workspace
			}
			workspaceChanged = (req.Workspace != old)
		} else if existing, ok := state.Presence[req.Agent]; ok && existing != nil {
			presence.Workspace = existing.Workspace
		}
		state.Presence[req.Agent] = presence

		if inst := findInstance(state, req.Agent); inst != nil {
			inst.LastHeartbeat = now
			if inst.Status == "offline" {
				inst.Status = "idle"
			}
		}
		return nil
	})
	if err != nil {
		writeError(rw, err.Error())
		return
	}

	if workspaceChanged && req.Workspace != "" {
		w.svc.Policy().SetWorkspaceRoot(req.Workspace)
		w.logger.Printf("Workspace root updated to %s (set by %s)", req.Workspace, req.Agent)
	}

	w.touchCLISession(req.Agent)
	msg := fmt.Sprintf("Presence updated: %s is now %s", req.Agent, req.Status)
	if req.Workspace != "" {
		msg += fmt.Sprintf(" (workspace: %s)", req.Workspace)
	}
	w.logger.Printf("Presence updated for %s: %s workspace=%s", req.Agent, req.Status, req.Workspace)
	w.writeWithBanner(rw, msg, req.Agent, "set_presence")
}

func (w *workerAPI) handleContext(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(rw, "GET required", http.StatusMethodNotAllowed)
		return
	}
	agent := r.URL.Query().Get("for")
	if agent == "" {
		writeError(rw, "'for' is required")
		return
	}

	workspacePath := w.svc.Policy().WorkspaceRoot()
	projectInfo := app.DetectProjectInfo(workspacePath)

	var result string
	err := w.svc.Run(func(state *domain.CollabState) error {
		resolveWorkerAgent(state, agent)
		extra := app.RegisteredAgentNames(state)
		if err := app.ValidateAgent(agent, state, false, false, extra...); err != nil {
			return err
		}
		state.ProjectInfo = projectInfo

		now := time.Now()
		if inst := findInstance(state, agent); inst != nil {
			inst.LastHeartbeat = now
			if inst.Status == "offline" {
				inst.Status = "idle"
			}
		}

		ttl := time.Duration(w.svc.Policy().PresenceTTLSeconds()) * time.Second
		var buf strings.Builder
		fmt.Fprintf(&buf, "=== Session Context for %s ===\n\n", agent)
		buf.WriteString("Pair Status:\n")
		for _, p := range state.Presence {
			if p == nil {
				continue
			}
			statusStr := p.Status
			if now.Sub(p.LastSeen) > ttl {
				statusStr += " (offline)"
			}
			fmt.Fprintf(&buf, "  %s: %s", p.Agent, statusStr)
			if p.CurrentTaskID > 0 {
				fmt.Fprintf(&buf, " (Task #%d)", p.CurrentTaskID)
			}
			if p.Workspace != "" {
				fmt.Fprintf(&buf, " [%s]", p.Workspace)
			}
			buf.WriteByte('\n')
		}
		buf.WriteByte('\n')

		pendingCount := 0
		inProgressCount := 0
		agentType := app.ResolveParentAgentType(state, agent)
		for _, task := range state.Tasks {
			if task.AssignedTo == agent || task.AssignedTo == agentType || task.AssignedTo == "any" {
				switch task.Status {
				case "pending":
					pendingCount++
				case "in_progress":
					inProgressCount++
				}
			}
		}
		fmt.Fprintf(&buf, "Your Tasks: %d pending, %d in progress\n\n", pendingCount, inProgressCount)

		buf.WriteString("Project:\n")
		fmt.Fprintf(&buf, "  Name: %s\n", projectInfo.Name)
		fmt.Fprintf(&buf, "  Path: %s\n", projectInfo.Path)
		if projectInfo.IsGitRepo {
			fmt.Fprintf(&buf, "  Git Branch: %s\n", projectInfo.GitBranch)
		}

		result = buf.String()
		return nil
	})
	if err != nil {
		writeError(rw, err.Error())
		return
	}
	w.touchCLISession(agent)
	writeText(rw, result)
}

func (w *workerAPI) handleWorkContext(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(rw, "GET required", http.StatusMethodNotAllowed)
		return
	}
	taskIDStr := r.URL.Query().Get("task_id")
	if taskIDStr == "" {
		writeError(rw, "task_id is required")
		return
	}
	taskID, err := strconv.Atoi(taskIDStr)
	if err != nil {
		writeError(rw, "task_id must be a number")
		return
	}

	var result string
	_ = w.svc.Query(func(state *domain.CollabState) error {
		var wc *domain.WorkContext
		for _, t := range state.Tasks {
			if t.ID == taskID && t.ContextID != "" {
				wc = state.WorkContexts[t.ContextID]
				break
			}
		}
		if wc == nil {
			result = fmt.Sprintf("No work context for task #%d", taskID)
			return nil
		}
		out := map[string]interface{}{
			"task_id":        wc.TaskID,
			"relevant_files": wc.RelevantFiles,
			"background":     wc.Background,
			"constraints":    wc.Constraints,
			"shared_notes":   wc.SharedNotes,
		}
		bytes, _ := json.MarshalIndent(out, "", "  ")
		body := string(bytes)
		if len(wc.Constraints) > 0 {
			body = "CONSTRAINTS (set by driver — you must obey these):\n" +
				formatConstraintsCLI(wc.Constraints) +
				"\n" + body
		}
		result = body
		return nil
	})
	writeText(rw, result)
}

// --- Helpers ---

// writeWithBanner writes the response text, appending a piggyback-style banner
// with STOP signals, unread counts, and progress nudges.
func (w *workerAPI) writeWithBanner(rw http.ResponseWriter, text, agent, toolName string) {
	if agent != "" {
		banner := collab.BuildBanner(w.svc, agent, toolName)
		if banner != "" {
			text += banner
		}
	}
	writeText(rw, text)
}

func writeText(rw http.ResponseWriter, text string) {
	rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprint(rw, text)
}

func writeError(rw http.ResponseWriter, msg string) {
	rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
	rw.WriteHeader(http.StatusBadRequest)
	fmt.Fprintf(rw, "error: %s", msg)
}

func decodeJSON(rw http.ResponseWriter, r *http.Request, v interface{}) bool {
	r.Body = http.MaxBytesReader(rw, r.Body, 1<<20) // 1 MB limit
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(rw, "failed to read request body")
		return false
	}
	if len(body) == 0 {
		writeError(rw, "request body is empty")
		return false
	}
	if err := json.Unmarshal(body, v); err != nil {
		writeError(rw, "invalid JSON: "+err.Error())
		return false
	}
	return true
}

// findInstance looks up an AgentInstance by instance ID or agent type.
func findInstance(state *domain.CollabState, agent string) *domain.AgentInstance {
	if inst, ok := state.AgentInstances[agent]; ok {
		return inst
	}
	for _, inst := range state.AgentInstances {
		if inst != nil && inst.AgentType == agent {
			return inst
		}
	}
	return nil
}

// resolveWorkerAgent maps a dynamic worker instance ID (e.g. "codex-task-6")
// to a registered agent type and ensures an AgentInstance exists for it. This
// allows workers spawned with ephemeral instance IDs to use all REST endpoints.
//
// Task-bound IDs (matching "<base>-task-<digits>") never short-circuit on an
// exact RegisteredAgents hit — that hit would indicate leftover corrupt state
// and would cause AgentType to be set to the full task-bound ID. Instead we
// always route through app.ResolveParentAgentType to derive the correct
// parent AgentType and only add the concrete AgentInstance, never pollute
// RegisteredAgents.
func resolveWorkerAgent(state *domain.CollabState, agent string) string {
	if _, ok := state.AgentInstances[agent]; ok {
		return agent
	}
	for _, inst := range state.AgentInstances {
		if inst != nil && inst.AgentType == agent {
			return agent
		}
	}
	_, isTaskBound := app.StripTaskBoundSuffix(agent)
	if !isTaskBound {
		if _, ok := state.RegisteredAgents[agent]; ok {
			return agent
		}
	}
	parentType := app.ResolveParentAgentType(state, agent)
	if parentType == "" || parentType == agent {
		return agent
	}
	// M1: Only auto-materialize an AgentInstance when the resolved parent
	// type is actually known to the system. Otherwise a typo
	// ("claud-code-task-99") or a worker that was never spawned by the
	// pool ("ghost-agent-task-5") silently leaks a phantom instance into
	// AgentInstances, polluting list_agents and corrupting watchdog
	// liveness tracking. Caller-side ValidateAgent then surfaces the
	// "unknown agent" error to the worker, which is the correct outcome.
	_, parentRegistered := state.RegisteredAgents[parentType]
	parentHasInstance := false
	for _, inst := range state.AgentInstances {
		if inst != nil && inst.AgentType == parentType {
			parentHasInstance = true
			break
		}
	}
	if !parentRegistered && !parentHasInstance {
		return agent
	}
	// LastSpawnedAt seeds the STOP-banner spawn cutoff for the
	// task-bound instance row materialised on first contact through
	// the CLI/REST surface. See heartbeat.go for the rationale.
	state.AgentInstances[agent] = &domain.AgentInstance{
		InstanceID:    agent,
		AgentType:     parentType,
		Role:          domain.RoleWorker,
		Status:        "idle",
		CurrentTasks:  []int{},
		LastSpawnedAt: time.Now(),
	}
	return agent
}

func formatConstraintsCLI(constraints []string) string {
	var sb strings.Builder
	for _, c := range constraints {
		sb.WriteString("  - ")
		sb.WriteString(c)
		sb.WriteString("\n")
	}
	return sb.String()
}
