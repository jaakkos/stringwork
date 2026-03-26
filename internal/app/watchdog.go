package app

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"
	"unicode"

	"github.com/jaakkos/stringwork/internal/domain"
)

const (
	// defaultWatchdogInterval is how often the watchdog runs its checks.
	defaultWatchdogInterval = 60 * time.Second

	// defaultHeartbeatStaleThreshold is how long since the last heartbeat
	// before an agent is considered dead.
	defaultHeartbeatStaleThreshold = 5 * time.Minute

	// defaultTaskStuckThreshold is how long a task can stay in_progress
	// without its agent heartbeating before it is considered stuck.
	defaultTaskStuckThreshold = 10 * time.Minute

	// defaultSessionStaleThreshold is how long a session can exist without
	// its agent heartbeating before it is considered stale and removed.
	defaultSessionStaleThreshold = 5 * time.Minute

	// defaultProgressWarningThreshold is how long a task can go without a
	// progress report before a warning is sent to the driver.
	defaultProgressWarningThreshold = 3 * time.Minute

	// defaultProgressCriticalThreshold is how long without progress before
	// a critical alert is sent to the driver.
	defaultProgressCriticalThreshold = 5 * time.Minute

	// defaultMaxTaskFailures is the number of watchdog-detected failures
	// before a task is auto-blocked (DLQ behavior).
	defaultMaxTaskFailures = 3
)

// ProcessActivityProvider gives the watchdog access to live process metadata
// so it can distinguish workers that are actively producing output from those
// that are truly stuck or crashed.
type ProcessActivityProvider interface {
	GetProcessInfo() map[string]ProcessInfo
	GetRecentOutput(instanceID string) string
}

// Watchdog monitors agent liveness and recovers from stuck states.
// It runs periodically and:
// - Detects agent instances with stale heartbeats and marks them offline
// - Resets in_progress tasks whose agents are dead back to pending
// - Clears stale sessions from the registry so workers can be respawned
// - Sends system notifications about recovery actions
type Watchdog struct {
	svc                    *CollabService
	registry               *SessionRegistry
	logger                 *log.Logger
	interval               time.Duration
	heartbeatStaleThresh   time.Duration
	taskStuckThresh        time.Duration
	sessionStaleThresh     time.Duration
	progressWarningThresh  time.Duration
	progressCriticalThresh time.Duration
	maxTaskFailures        int
	notifier               Triggerable
	processActivity        ProcessActivityProvider
	stopCh                 chan struct{}
	doneCh                 chan struct{}
	// alertedTasks tracks which tasks have been alerted at which level to avoid spam.
	// Key: taskID, Value: "warning" or "critical".
	alertedTasks map[int]string
	pol          Policy
}

// WatchdogOption configures the watchdog.
type WatchdogOption func(*Watchdog)

// WithPolicy sets the policy for the watchdog to use for thresholds.
func WithPolicy(p Policy) WatchdogOption {
	return func(w *Watchdog) { w.pol = p }
}

// WithWatchdogInterval sets the check interval.
func WithWatchdogInterval(d time.Duration) WatchdogOption {
	return func(w *Watchdog) { w.interval = d }
}

// WithHeartbeatThreshold sets the threshold for considering a heartbeat stale.
func WithHeartbeatThreshold(d time.Duration) WatchdogOption {
	return func(w *Watchdog) { w.heartbeatStaleThresh = d }
}

// WithTaskStuckThreshold sets the threshold for considering a task stuck.
func WithTaskStuckThreshold(d time.Duration) WatchdogOption {
	return func(w *Watchdog) { w.taskStuckThresh = d }
}

// WithSessionStaleThreshold sets the threshold for considering a session stale.
func WithSessionStaleThreshold(d time.Duration) WatchdogOption {
	return func(w *Watchdog) { w.sessionStaleThresh = d }
}

// WithProgressWarningThreshold sets the threshold for progress warning alerts.
func WithProgressWarningThreshold(d time.Duration) WatchdogOption {
	return func(w *Watchdog) { w.progressWarningThresh = d }
}

// WithProgressCriticalThreshold sets the threshold for progress critical alerts.
func WithProgressCriticalThreshold(d time.Duration) WatchdogOption {
	return func(w *Watchdog) { w.progressCriticalThresh = d }
}

// WithMaxTaskFailures sets the failure threshold before a task is auto-blocked.
func WithMaxTaskFailures(n int) WatchdogOption {
	return func(w *Watchdog) { w.maxTaskFailures = n }
}

// WithWatchdogNotifier sets the notifier to trigger after recovery actions.
func WithWatchdogNotifier(n Triggerable) WatchdogOption {
	return func(w *Watchdog) { w.notifier = n }
}

// WithProcessActivity gives the watchdog access to live process data so it
// can automatically distinguish actively-working workers from stuck ones.
func WithProcessActivity(p ProcessActivityProvider) WatchdogOption {
	return func(w *Watchdog) { w.processActivity = p }
}

// NewWatchdog creates a new Watchdog.
func NewWatchdog(svc *CollabService, registry *SessionRegistry, logger *log.Logger, opts ...WatchdogOption) *Watchdog {
	w := &Watchdog{
		svc:                    svc,
		registry:               registry,
		logger:                 logger,
		interval:               defaultWatchdogInterval,
		heartbeatStaleThresh:   defaultHeartbeatStaleThreshold,
		taskStuckThresh:        defaultTaskStuckThreshold,
		sessionStaleThresh:     defaultSessionStaleThreshold,
		progressWarningThresh:  defaultProgressWarningThreshold,
		progressCriticalThresh: defaultProgressCriticalThreshold,
		maxTaskFailures:        defaultMaxTaskFailures,
		stopCh:                 make(chan struct{}),
		doneCh:                 make(chan struct{}),
		alertedTasks:           make(map[int]string),
	}
	for _, o := range opts {
		o(w)
	}
	// Use thresholds from policy if provided
	if w.pol != nil {
		if o := w.pol.Orchestration(); o != nil {
			if o.HeartbeatIntervalSeconds > 0 {
				w.interval = time.Duration(o.HeartbeatIntervalSeconds) * time.Second
			}
			if o.WorkerTimeoutSeconds > 0 {
				w.heartbeatStaleThresh = time.Duration(o.WorkerTimeoutSeconds) * time.Second
				w.sessionStaleThresh = w.heartbeatStaleThresh
				w.taskStuckThresh = w.heartbeatStaleThresh * 2
			}
		}
		if mf := w.pol.MaxTaskFailures(); mf > 0 {
			w.maxTaskFailures = mf
		}
	}
	return w
}

// Start begins the watchdog loop. Returns when ctx is cancelled or Stop is called.
func (w *Watchdog) Start(ctx context.Context) {
	defer close(w.doneCh)
	w.logger.Printf("Watchdog: started (interval=%s, heartbeat_stale=%s, task_stuck=%s, session_stale=%s)",
		w.interval, w.heartbeatStaleThresh, w.taskStuckThresh, w.sessionStaleThresh)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.logger.Println("Watchdog: stopped (context cancelled)")
			return
		case <-w.stopCh:
			w.logger.Println("Watchdog: stopped")
			return
		case <-ticker.C:
			w.check()
		}
	}
}

// Stop signals the watchdog to stop.
func (w *Watchdog) Stop() {
	close(w.stopCh)
	<-w.doneCh
}

// CheckOnce runs one watchdog cycle (for testing or manual trigger).
func (w *Watchdog) CheckOnce() {
	w.check()
}

// isAgentAlive checks if an agent has shown any sign of life recently.
// It checks both the state heartbeat AND the session registry's activity tracking
// (updated on every tool call via PiggybackMiddleware.TouchSession).
func (w *Watchdog) isAgentAlive(agent string, inst *domain.AgentInstance, now time.Time, threshold time.Duration) bool {
	// Check 1: Session registry activity (most reliable — updated on every tool call)
	lastActivity := w.registry.LastActivityForAgent(agent)
	if !lastActivity.IsZero() && now.Sub(lastActivity) <= threshold {
		return true
	}
	// Also check by instance ID if different from agent type
	if inst != nil && inst.InstanceID != agent {
		lastActivity = w.registry.LastActivityForAgent(inst.InstanceID)
		if !lastActivity.IsZero() && now.Sub(lastActivity) <= threshold {
			return true
		}
	}

	// Check 2: Active session exists (agent is connected)
	if w.registry.HasActiveSession(agent) {
		// Session exists; check if it has any recorded activity
		lastActivity = w.registry.LastActivityForAgent(agent)
		if lastActivity.IsZero() {
			// Session exists but no activity recorded yet — agent just connected.
			// Give it the benefit of the doubt.
			return true
		}
	}

	// Check 3: State heartbeat (updated by heartbeat tool, set_presence, get_session_context)
	if inst != nil && !inst.LastHeartbeat.IsZero() && now.Sub(inst.LastHeartbeat) <= threshold {
		return true
	}

	return false
}

// check runs all watchdog checks in a single state mutation.
func (w *Watchdog) check() {
	var recoveredTasks int
	var recoveredAgents int
	var prunedSessions int

	// Phase 1: Prune stale sessions from the registry.
	// This must happen outside the CollabService mutex to avoid ordering issues.
	prunedSessions = w.pruneStaleSessions()

	// Phase 2: Recover stuck agents and tasks in a single state mutation.
	err := w.svc.Run(func(state *domain.CollabState) error {
		now := time.Now()

		// Find dead agents: instances with no recent activity from any source.
		deadAgents := make(map[string]bool)
		for id, inst := range state.AgentInstances {
			if inst == nil {
				continue
			}
			// Skip the driver — its presence is tracked by the MCP session lifecycle.
			if inst.Role == domain.RoleDriver {
				continue
			}
			if inst.LastHeartbeat.IsZero() {
				continue
			}
			if !w.isAgentAlive(id, inst, now, w.heartbeatStaleThresh) {
				deadAgents[id] = true
				deadAgents[inst.AgentType] = true
			}
		}

		// Recover stuck tasks: reset in_progress tasks assigned to dead agents.
		for i := range state.Tasks {
			t := &state.Tasks[i]
			if t.Status != "in_progress" {
				continue
			}

			agentDead := deadAgents[t.AssignedTo]
			taskStuck := now.Sub(t.UpdatedAt) > w.taskStuckThresh

			if !agentDead && !taskStuck {
				continue
			}

			// For stuck-threshold tasks, also verify the agent isn't alive.
			// If the agent is alive but the task is old, the agent may still be working.
			if !agentDead && taskStuck {
				// Check if the assigned agent has any recent activity.
				// If the agent IS alive (has session activity), don't recover the task —
				// the agent is connected and presumably still working on it.
				assigneeInst := findInstanceForAgent(state, t.AssignedTo)
				if w.isAgentAlive(t.AssignedTo, assigneeInst, now, w.heartbeatStaleThresh) {
					continue
				}
			}

			reason := "agent heartbeat stale"
			if !agentDead && taskStuck {
				reason = fmt.Sprintf("no progress for %s and agent unresponsive", w.taskStuckThresh)
			}

			w.logger.Printf("Watchdog: recovering stuck task #%d (%s) assigned to %s — %s",
				t.ID, t.Title, t.AssignedTo, reason)

			oldAssignee := t.AssignedTo
			t.FailureCount++
			t.LastFailure = now
			t.FailureReason = reason
			t.UpdatedAt = now

			if t.FailureCount >= w.maxTaskFailures {
				t.Status = "blocked"
				t.BlockedBy = fmt.Sprintf("Watchdog: auto-blocked after %d failures. Last reason: %s", t.FailureCount, reason)
				if t.ResultSummary == "" {
					t.ResultSummary = t.BlockedBy
				}
				w.logger.Printf("Watchdog: task #%d auto-blocked after %d failures", t.ID, t.FailureCount)
			} else {
				t.Status = "pending"
				if t.ResultSummary == "" {
					t.ResultSummary = fmt.Sprintf("Watchdog: reset to pending (failure %d/%d) — %s", t.FailureCount, w.maxTaskFailures, reason)
				}
			}

			// Clean up the agent instance's task list
			removeTaskFromInstanceByID(state, t.ID, oldAssignee)
			recoveredTasks++
		}

		// Recover dead agents: mark stale instances as offline and clear their task lists.
		for id, inst := range state.AgentInstances {
			if inst == nil || inst.Role == domain.RoleDriver {
				continue
			}
			if !deadAgents[id] {
				continue
			}
			if inst.Status == "offline" && len(inst.CurrentTasks) == 0 {
				continue // already cleaned up
			}

			w.logger.Printf("Watchdog: marking agent %s as offline (last heartbeat: %s ago)",
				id, now.Sub(inst.LastHeartbeat).Round(time.Second))

			inst.Status = "offline"
			inst.CurrentTasks = nil
			recoveredAgents++
		}

		// Phase 3: Tiered progress alerts and SLA checks for in_progress tasks.
		// This generates warnings/critical alerts BEFORE the task hits the stuck threshold.
		driver := state.DriverID
		if driver == "" {
			driver = "cursor"
		}
		for i := range state.Tasks {
			t := &state.Tasks[i]
			if t.Status != "in_progress" {
				// Clear alert tracking for non-in_progress tasks
				delete(w.alertedTasks, t.ID)
				continue
			}

			// Determine the last activity time for this task.
			// Use LastProgressAt if available (more specific), otherwise fall back to UpdatedAt.
			lastActivity := t.UpdatedAt
			if !t.LastProgressAt.IsZero() {
				lastActivity = t.LastProgressAt
			}
			sinceProgress := now.Sub(lastActivity)

			// SLA check: alert if expected duration is exceeded
			if t.ExpectedDurationSec > 0 {
				expectedDur := time.Duration(t.ExpectedDurationSec) * time.Second
				sinceStart := now.Sub(t.UpdatedAt) // UpdatedAt is set when task moves to in_progress
				if sinceStart > expectedDur {
					slaLevel := w.alertedTasks[t.ID]
					if slaLevel != "sla_exceeded" {
						w.alertedTasks[t.ID] = "sla_exceeded"
						overBy := sinceStart - expectedDur
						content := fmt.Sprintf("⏱️ **SLA exceeded**: Task #%d (%s) assigned to %s has been running for %s (expected: %s, over by %s). Consider checking on the worker or cancelling.",
							t.ID, t.Title, t.AssignedTo,
							sinceStart.Round(time.Second), expectedDur.Round(time.Second), overBy.Round(time.Second))
						state.Messages = append(state.Messages, domain.Message{
							ID: state.NextMsgID, From: "system", To: driver,
							Content: content, Timestamp: now,
						})
						state.NextMsgID++
						w.logger.Printf("Watchdog: SLA exceeded for task #%d (%s over)", t.ID, overBy.Round(time.Second))
					}
				}
			}

			// Tiered progress alerts with smart stuck detection.
			// When process activity data is available, the watchdog classifies the
			// worker as ACTIVE (producing output), SILENT (no recent output), or
			// NO_PROCESS (crashed/exited) and adjusts alerts accordingly.
			currentLevel := w.alertedTasks[t.ID]
			activity := w.classifyWorkerActivity(t.AssignedTo, now)

			if sinceProgress > w.progressCriticalThresh && currentLevel != "critical" && currentLevel != "sla_exceeded" {
				var content string
				switch activity.status {
				case workerActive:
					w.alertedTasks[t.ID] = "critical"
					content = fmt.Sprintf("ℹ️ **Note**: Worker %s has not called `report_progress` on task #%d (%s) for %s, "+
						"but the process is actively producing output (last output %s ago, %d bytes written). "+
						"The worker should call `report_progress` periodically.",
						t.AssignedTo, t.ID, t.Title, sinceProgress.Round(time.Second),
						activity.sinceOutput.Round(time.Second), activity.outputBytes)
				case workerSilent:
					w.alertedTasks[t.ID] = "critical"
					content = fmt.Sprintf("🔴 **Stuck**: Worker %s has not reported progress on task #%d (%s) for %s, "+
						"and the process has been silent for %s — likely stuck. "+
						"Cancel with `cancel_agent agent='%s'`.",
						t.AssignedTo, t.ID, t.Title, sinceProgress.Round(time.Second),
						activity.sinceOutput.Round(time.Second), t.AssignedTo)
					if activity.snippet != "" {
						content += fmt.Sprintf("\n\nLast output:\n```\n%s\n```", activity.snippet)
					}
				default: // workerNoProcess
					w.alertedTasks[t.ID] = "critical"
					content = fmt.Sprintf("💀 **No process**: Worker %s has not reported progress on task #%d (%s) for %s, "+
						"and no running process was found — the worker may have crashed or exited. "+
						"Check the log with `worker_output task_id=%d` or reassign the task.",
						t.AssignedTo, t.ID, t.Title, sinceProgress.Round(time.Second), t.ID)
				}
				state.Messages = append(state.Messages, domain.Message{
					ID: state.NextMsgID, From: "system", To: driver,
					Content: content, Timestamp: now,
				})
				state.NextMsgID++
				w.logger.Printf("Watchdog: CRITICAL — task #%d no progress for %s (activity: %s)", t.ID, sinceProgress.Round(time.Second), activity.status)

			} else if sinceProgress > w.progressWarningThresh && currentLevel == "" {
				switch activity.status {
				case workerActive:
					// Suppress warning — worker is actively producing output, just not
					// calling report_progress. Will alert at critical threshold if it continues.
					w.logger.Printf("Watchdog: task #%d no progress for %s but process is active (last output %s ago) — suppressing warning",
						t.ID, sinceProgress.Round(time.Second), activity.sinceOutput.Round(time.Second))
				case workerSilent:
					w.alertedTasks[t.ID] = "warning"
					content := fmt.Sprintf("⚠️ **Warning**: Worker %s has not reported progress on task #%d (%s) for %s, "+
						"and the process has been silent for %s. The worker may be stuck. "+
						"Use `worker_output task_id=%d` to check.",
						t.AssignedTo, t.ID, t.Title, sinceProgress.Round(time.Second),
						activity.sinceOutput.Round(time.Second), t.ID)
					if activity.snippet != "" {
						content += fmt.Sprintf("\n\nLast output:\n```\n%s\n```", activity.snippet)
					}
					state.Messages = append(state.Messages, domain.Message{
						ID: state.NextMsgID, From: "system", To: driver,
						Content: content, Timestamp: now,
					})
					state.NextMsgID++
					w.logger.Printf("Watchdog: WARNING — task #%d no progress for %s (SILENT for %s)", t.ID, sinceProgress.Round(time.Second), activity.sinceOutput.Round(time.Second))
				default: // workerNoProcess
					w.alertedTasks[t.ID] = "warning"
					content := fmt.Sprintf("⚠️ **Warning**: Worker %s has not reported progress on task #%d (%s) for %s, "+
						"and no running process was found. Use `worker_output task_id=%d` to check the log.",
						t.AssignedTo, t.ID, t.Title, sinceProgress.Round(time.Second), t.ID)
					state.Messages = append(state.Messages, domain.Message{
						ID: state.NextMsgID, From: "system", To: driver,
						Content: content, Timestamp: now,
					})
					state.NextMsgID++
					w.logger.Printf("Watchdog: WARNING — task #%d no progress for %s (no process)", t.ID, sinceProgress.Round(time.Second))
				}
			}
		}

		// Send notification if anything was recovered
		if recoveredTasks > 0 || recoveredAgents > 0 {
			driver := state.DriverID
			if driver == "" {
				driver = "cursor"
			}
			parts := []string{}
			if recoveredTasks > 0 {
				parts = append(parts, fmt.Sprintf("%d stuck task(s) reset to pending", recoveredTasks))
			}
			if recoveredAgents > 0 {
				parts = append(parts, fmt.Sprintf("%d stale agent(s) marked offline", recoveredAgents))
			}
			content := fmt.Sprintf("🔧 **Watchdog recovery**: %s. Check task list for tasks needing re-assignment.",
				joinParts(parts))

			state.Messages = append(state.Messages, domain.Message{
				ID:        state.NextMsgID,
				From:      "system",
				To:        driver,
				Content:   content,
				Timestamp: now,
			})
			state.NextMsgID++
		}

		return nil
	})

	if err != nil {
		w.logger.Printf("Watchdog: state mutation error: %v", err)
		return
	}

	// Trigger notifier if we recovered anything (so workers get respawned for the pending tasks)
	if (recoveredTasks > 0 || recoveredAgents > 0 || prunedSessions > 0) && w.notifier != nil {
		w.notifier.Trigger()
	}

	if recoveredTasks > 0 || recoveredAgents > 0 || prunedSessions > 0 {
		w.logger.Printf("Watchdog: cycle complete — recovered %d task(s), %d agent(s), pruned %d session(s)",
			recoveredTasks, recoveredAgents, prunedSessions)
	}
}

// findInstanceForAgent returns the AgentInstance for an agent name (direct or by type).
func findInstanceForAgent(state *domain.CollabState, agent string) *domain.AgentInstance {
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

// pruneStaleSessions removes sessions from the registry whose agents show no
// recent activity from ANY source: session tool calls, state heartbeats, or presence.
func (w *Watchdog) pruneStaleSessions() int {
	pruned := 0
	now := time.Now()

	// Get all connected agents from the registry
	agents := w.registry.ConnectedAgents()
	if len(agents) == 0 {
		return 0
	}

	var deadSessions []string
	_ = w.svc.Query(func(state *domain.CollabState) error {
		for _, agent := range agents {
			// Skip the driver — never prune it
			if state.DriverID == agent {
				continue
			}

			inst := findInstanceForAgent(state, agent)
			if inst != nil && inst.Role == domain.RoleDriver {
				continue
			}

			// Use the unified liveness check — considers session activity,
			// active session existence, and state heartbeat.
			if w.isAgentAlive(agent, inst, now, w.sessionStaleThresh) {
				continue
			}

			deadSessions = append(deadSessions, agent)
		}
		return nil
	})

	// Remove stale sessions
	seen := make(map[string]bool)
	for _, agent := range deadSessions {
		if seen[agent] {
			continue
		}
		seen[agent] = true

		sid := w.registry.GetSessionForAgent(agent)
		if sid == "" {
			continue
		}
		w.logger.Printf("Watchdog: pruning stale session for agent %s (session=%s)", agent, sid)
		w.registry.RemoveSession(sid)
		pruned++
	}

	return pruned
}

// removeTaskFromInstanceByID removes a task ID from the given agent's CurrentTasks.
// Similar to removeTaskFromInstance in tasks.go but works by instance ID directly.
func removeTaskFromInstanceByID(state *domain.CollabState, taskID int, agent string) {
	// Direct instance lookup
	if inst, ok := state.AgentInstances[agent]; ok && inst != nil {
		newTasks := make([]int, 0, len(inst.CurrentTasks))
		for _, id := range inst.CurrentTasks {
			if id != taskID {
				newTasks = append(newTasks, id)
			}
		}
		inst.CurrentTasks = newTasks
		if len(inst.CurrentTasks) == 0 && inst.Status == "busy" {
			inst.Status = "idle"
		}
		return
	}
	// Fallback: scan all instances
	for _, inst := range state.AgentInstances {
		if inst == nil {
			continue
		}
		for _, id := range inst.CurrentTasks {
			if id == taskID {
				newTasks := make([]int, 0, len(inst.CurrentTasks))
				for _, tid := range inst.CurrentTasks {
					if tid != taskID {
						newTasks = append(newTasks, tid)
					}
				}
				inst.CurrentTasks = newTasks
				if len(inst.CurrentTasks) == 0 && inst.Status == "busy" {
					inst.Status = "idle"
				}
				return
			}
		}
	}
}

// workerActivityStatus classifies the state of a worker's process.
type workerActivityStatus string

const (
	workerActive    workerActivityStatus = "active"
	workerSilent    workerActivityStatus = "silent"
	workerNoProcess workerActivityStatus = "no_process"
)

// workerActivityInfo holds the classification result for a worker's process.
type workerActivityInfo struct {
	status      workerActivityStatus
	sinceOutput time.Duration // time since last output (zero if no process)
	outputBytes int64
	snippet     string // sanitized tail of recent output (for SILENT workers)
}

const (
	silentThreshold = 2 * time.Minute
	maxSnippetBytes = 500
)

// classifyWorkerActivity checks process activity for a worker and returns
// a classification. Falls back to workerNoProcess when no provider is set
// (backward compatible).
func (w *Watchdog) classifyWorkerActivity(assignedTo string, now time.Time) workerActivityInfo {
	if w.processActivity == nil {
		return workerActivityInfo{status: workerNoProcess}
	}
	procs := w.processActivity.GetProcessInfo()

	// Try exact instance ID first, then check for task-instance patterns
	proc, found := procs[assignedTo]
	if !found {
		for id, p := range procs {
			if strings.HasPrefix(id, assignedTo+"-") || strings.HasPrefix(id, assignedTo+"-task-") {
				proc = p
				found = true
				break
			}
		}
	}
	if !found {
		return workerActivityInfo{status: workerNoProcess}
	}

	sinceOutput := now.Sub(proc.LastOutputAt)
	info := workerActivityInfo{
		sinceOutput: sinceOutput,
		outputBytes: proc.OutputBytes,
	}

	if sinceOutput < silentThreshold {
		info.status = workerActive
	} else {
		info.status = workerSilent
		raw := w.processActivity.GetRecentOutput(assignedTo)
		if raw == "" {
			// Try task-instance pattern
			for id := range procs {
				if strings.HasPrefix(id, assignedTo+"-") || strings.HasPrefix(id, assignedTo+"-task-") {
					raw = w.processActivity.GetRecentOutput(id)
					if raw != "" {
						break
					}
				}
			}
		}
		if raw != "" {
			info.snippet = sanitizeSnippet(raw, maxSnippetBytes)
		}
	}

	return info
}

// sanitizeSnippet truncates output to the last maxBytes, strips control chars,
// and trims to the last complete line.
func sanitizeSnippet(s string, maxBytes int) string {
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || r == '\r' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	if len(s) > maxBytes {
		s = s[len(s)-maxBytes:]
		// Trim to the next complete line to avoid a garbled first line
		if idx := strings.Index(s, "\n"); idx >= 0 && idx < len(s)-1 {
			s = s[idx+1:]
		}
	}
	return strings.TrimSpace(s)
}

// joinParts joins string parts with " and " for the last element, ", " otherwise.
func joinParts(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	case 2:
		return parts[0] + " and " + parts[1]
	default:
		result := ""
		for i, p := range parts {
			if i == len(parts)-1 {
				result += " and " + p
			} else if i > 0 {
				result += ", " + p
			} else {
				result += p
			}
		}
		return result
	}
}
