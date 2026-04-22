package app

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
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

// WorkerAutoCanceller allows the watchdog to force-cancel unresponsive workers.
type WorkerAutoCanceller interface {
	CancelWorker(instanceID string) bool
	GetRecentOutput(instanceID string) string
}

// Watchdog monitors agent liveness and recovers from stuck states.
// It runs periodically and:
// - Detects agent instances with stale heartbeats and marks them offline
// - Resets in_progress tasks whose agents are dead back to pending
// - Auto-cancels workers that are silent past the critical threshold
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
	// processSilentThresh is how long a process can go without stdout/stderr
	// before it is classified as "silent" (vs "active"). Derived from
	// progressWarningThresh to maintain a consistent threshold ladder:
	//   processSilentThresh < progressWarningThresh < progressCriticalThresh
	processSilentThresh time.Duration
	maxTaskFailures     int
	notifier            Triggerable
	processActivity     ProcessActivityProvider
	autoCanceller       WorkerAutoCanceller
	stopCh              chan struct{}
	doneCh              chan struct{}
	// alertedTasks tracks which tasks have been alerted at which level to avoid spam.
	// Key: taskID, Value: "warning" or "critical".
	alertedTasks map[int]string
	pol          Policy

	// Cumulative GC counters surfaced via GCStats() so dashboards can show
	// "N rows pruned since startup, last run Xs ago". These are a thin
	// observability layer on top of the per-tick prunedPresence/prunedInstances
	// already logged by check().
	prunedPresenceTotal  atomic.Int64
	prunedInstancesTotal atomic.Int64
	gcMu                 sync.Mutex
	lastGCRun            time.Time
}

// GCStats is a point-in-time snapshot of cumulative garbage-collection
// counters exposed via Watchdog.GCStats. Used by the dashboard to render
// a one-line "GC: N+M pruned, last run Xs ago" strip.
type GCStats struct {
	LastRun              time.Time
	PresencePrunedTotal  int64
	InstancesPrunedTotal int64
}

// GCStats returns a snapshot of the watchdog's cumulative GC counters.
// Safe to call concurrently from HTTP handlers.
func (w *Watchdog) GCStats() GCStats {
	w.gcMu.Lock()
	last := w.lastGCRun
	w.gcMu.Unlock()
	return GCStats{
		LastRun:              last,
		PresencePrunedTotal:  w.prunedPresenceTotal.Load(),
		InstancesPrunedTotal: w.prunedInstancesTotal.Load(),
	}
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

// WithAutoCanceller gives the watchdog the ability to force-cancel unresponsive
// workers and capture their output before termination. When set, silent workers
// past the critical threshold are auto-cancelled instead of just warned about.
func WithAutoCanceller(c WorkerAutoCanceller) WatchdogOption {
	return func(w *Watchdog) { w.autoCanceller = c }
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
	// Derive processSilentThresh from progressWarningThresh to maintain
	// a consistent threshold ladder. Default: warning - 1min, min 1min.
	w.processSilentThresh = w.progressWarningThresh - 1*time.Minute
	if w.processSilentThresh < 1*time.Minute {
		w.processSilentThresh = 1 * time.Minute
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
// It checks the state heartbeat, session registry activity (updated on every
// tool call via PiggybackMiddleware.TouchSession), AND process output activity
// (stdout/stderr writes from spawned worker processes).
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

	// Check 4: Process output activity (stdout/stderr from spawned workers).
	// Workers like Codex may not call heartbeat reliably but actively produce
	// output while working. A process writing to stdout is clearly not dead.
	if w.processActivity != nil {
		if w.isProcessAlive(agent, now, threshold) {
			return true
		}
		if inst != nil && inst.InstanceID != agent {
			if w.isProcessAlive(inst.InstanceID, now, threshold) {
				return true
			}
		}
	}

	return false
}

// isProcessAlive checks whether a spawned process for the given agent (or any
// task-bound child like "codex-task-3") has produced output within threshold.
func (w *Watchdog) isProcessAlive(agent string, now time.Time, threshold time.Duration) bool {
	procs := w.processActivity.GetProcessInfo()
	if proc, ok := procs[agent]; ok {
		if !proc.LastOutputAt.IsZero() && now.Sub(proc.LastOutputAt) <= threshold {
			return true
		}
	}
	// Check task-bound instances (e.g. "codex-task-3" for agent "codex")
	prefix := agent + "-"
	for id, proc := range procs {
		if strings.HasPrefix(id, prefix) {
			if !proc.LastOutputAt.IsZero() && now.Sub(proc.LastOutputAt) <= threshold {
				return true
			}
		}
	}
	return false
}

// hasAnyActivity returns true if the instance has ever shown any sign of life
// from any source: session registry, process output, or a non-zero heartbeat.
// Used to distinguish "brand-new instance, no signals yet" from "instance that
// was active and is now dead."
func (w *Watchdog) hasAnyActivity(agent string, inst *domain.AgentInstance, now time.Time) bool {
	if !w.registry.LastActivityForAgent(agent).IsZero() {
		return true
	}
	if inst != nil && inst.InstanceID != agent {
		if !w.registry.LastActivityForAgent(inst.InstanceID).IsZero() {
			return true
		}
	}
	if w.registry.HasActiveSession(agent) {
		return true
	}
	if w.processActivity != nil {
		procs := w.processActivity.GetProcessInfo()
		if _, ok := procs[agent]; ok {
			return true
		}
		prefix := agent + "-"
		for id := range procs {
			if strings.HasPrefix(id, prefix) {
				return true
			}
		}
	}
	return false
}

// check runs all watchdog checks in a single state mutation.
func (w *Watchdog) check() {
	var recoveredTasks int
	var recoveredAgents int
	var prunedSessions int
	var prunedPresence int
	var prunedInstances int

	// Phase 1: Prune stale sessions from the registry.
	// This must happen outside the CollabService mutex to avoid ordering issues.
	prunedSessions = w.pruneStaleSessions()

	// Phase 2: Recover stuck agents and tasks in a single state mutation.
	err := w.svc.Run(func(state *domain.CollabState) error {
		now := time.Now()

		// Find dead agents: instances with no recent activity from any source.
		// Track per-instance liveness separately from per-type liveness.
		deadInstances := make(map[string]bool)
		aliveTypes := make(map[string]bool)
		for id, inst := range state.AgentInstances {
			if inst == nil {
				continue
			}
			if inst.Role == domain.RoleDriver {
				continue
			}
			if w.isAgentAlive(id, inst, now, w.heartbeatStaleThresh) {
				aliveTypes[inst.AgentType] = true
			} else if !inst.LastHeartbeat.IsZero() || w.hasAnyActivity(id, inst, now) {
				// Only mark as dead if the instance has shown SOME sign of life
				// in the past (non-zero heartbeat, session, or process). Brand-new
				// instances that haven't had time to register any signal get a pass.
				deadInstances[id] = true
			}
		}
		// An agent TYPE is only dead if ALL its instances (including task-bound) are dead.
		deadAgents := make(map[string]bool)
		for id := range deadInstances {
			deadAgents[id] = true
		}
		for id, inst := range state.AgentInstances {
			if inst == nil {
				continue
			}
			if deadInstances[id] && !aliveTypes[inst.AgentType] {
				deadAgents[inst.AgentType] = true
			}
		}

		// Recover stuck tasks: reset in_progress tasks assigned to dead agents.
		for i := range state.Tasks {
			t := &state.Tasks[i]
			if t.Status != "in_progress" {
				continue
			}

			// Check if a task-bound worker for THIS specific task exists.
			// When a task is assigned to "claude-code" but the actual worker is
			// "claude-code-task-3", the task-bound instance is the authoritative
			// liveness signal — not the pre-existing idle instances.
			tbExists, tbAlive := w.checkTaskBoundWorker(state, t, now)
			if tbAlive {
				continue
			}

			// Prefer the specific instance that currently owns the task via
			// CurrentTasks — under the new semantics task.AssignedTo is a
			// parent type, so findInstanceForAgent may return an arbitrary
			// sibling instance. The CurrentTasks owner is authoritative.
			// Driver-owned tasks are exempt: drivers don't heartbeat via tools.
			ownerInst := findOwnerInstanceForTask(state, t)
			isDriverOwned := ownerInst != nil && ownerInst.Role == domain.RoleDriver
			ownerAlive := ownerInst != nil && w.isAgentAlive(ownerInst.InstanceID, ownerInst, now, w.heartbeatStaleThresh)
			if ownerInst != nil && ownerAlive {
				continue
			}

			agentDead := deadAgents[t.AssignedTo]
			if tbExists {
				// A task-bound worker was created for this task and is dead.
				// This overrides type-level liveness — the dedicated worker is
				// the authoritative signal for this specific task.
				agentDead = true
			}
			if ownerInst != nil && !ownerAlive && !isDriverOwned {
				// The specific owner instance is dead — recover regardless of
				// other sibling instances' liveness. Drivers are exempt.
				agentDead = true
			}
			taskStuck := now.Sub(t.UpdatedAt) > w.taskStuckThresh

			if !agentDead && !taskStuck {
				continue
			}

			if !agentDead && taskStuck {
				assigneeInst := ownerInst
				if assigneeInst == nil {
					assigneeInst = findInstanceForAgent(state, t.AssignedTo)
				}
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
		driver := ConfiguredDriver(state)
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
			//
			// For workers without a process (HTTP-connected), session activity
			// (tool calls via PiggybackMiddleware) serves as a liveness signal.
			currentLevel := w.alertedTasks[t.ID]
			activity := w.classifyWorkerActivity(t.AssignedTo, now)

			// Session-aware override: if no process is found but the worker has
			// recent session activity (tool calls), treat it as active. This covers
			// HTTP-connected workers (e.g. claude-code via URL) that have no spawned process.
			// We reuse progressWarningThresh as the staleness cutoff: if session activity
			// is older than the progress warning threshold, the worker would be warned
			// about lack of progress anyway, making the session effectively stale.
			sessionActive := false
			if activity.status == workerNoProcess {
				lastSession := w.registry.LastActivityForAgent(t.AssignedTo)
				if !lastSession.IsZero() && now.Sub(lastSession) < w.progressWarningThresh {
					sessionActive = true
				}
			}

			// "Recent send" grace window: if the worker has just sent a
			// send_message we should not auto-cancel it — the deliverable
			// is in flight. Workers that died mid-`send` are exactly the
			// failure mode we are trying to avoid (Bug D), so this gate
			// runs ahead of any auto-cancel branch below.
			//
			// t.AssignedTo is the parent agent type (e.g. "codex"), but
			// workers usually send as their instance ID ("codex-task-9").
			// Check both the parent type and the conventional task-bound
			// ID so either sender wins the grace.
			recentlySent := false
			const recentSendGrace = 90 * time.Second
			if state.LastSendByAgent != nil {
				candidates := []string{t.AssignedTo, fmt.Sprintf("%s-task-%d", t.AssignedTo, t.ID)}
				for _, c := range candidates {
					if lastSend, ok := state.LastSendByAgent[c]; ok && !lastSend.IsZero() {
						if now.Sub(lastSend) < recentSendGrace {
							recentlySent = true
							break
						}
					}
				}
			}

			if sinceProgress > w.progressCriticalThresh && currentLevel != "critical" && currentLevel != "sla_exceeded" {
				var content string
				shouldAutoCancel := false
				switch {
				case activity.status == workerActive || sessionActive:
					w.alertedTasks[t.ID] = "critical"
					if sessionActive {
						content = fmt.Sprintf("🔴 **VIOLATION**: Worker %s has not called `report_progress` on task #%d (%s) for %s. "+
							"Session is active but progress reporting is MANDATORY. "+
							"Worker will be auto-cancelled if reporting does not resume within the next watchdog cycle.",
							t.AssignedTo, t.ID, t.Title, sinceProgress.Round(time.Second))
					} else {
						content = fmt.Sprintf("🔴 **VIOLATION**: Worker %s has not called `report_progress` on task #%d (%s) for %s "+
							"(process active, last output %s ago). "+
							"Progress reporting is MANDATORY. Worker will be auto-cancelled if reporting does not resume.",
							t.AssignedTo, t.ID, t.Title, sinceProgress.Round(time.Second),
							activity.sinceOutput.Round(time.Second))
					}
				case activity.status == workerSilent:
					w.alertedTasks[t.ID] = "critical"
					if recentlySent {
						content = fmt.Sprintf("🟠 **DELIVERY GRACE**: Worker %s on task #%d (%s) "+
							"is silent (%s since last output) but sent a message within the last %s — "+
							"deferring auto-cancel for one cycle to let the deliverable land.",
							t.AssignedTo, t.ID, t.Title,
							activity.sinceOutput.Round(time.Second), recentSendGrace)
					} else {
						shouldAutoCancel = true
						content = fmt.Sprintf("🔴 **AUTO-CANCELLING**: Worker %s on task #%d (%s) — no progress for %s, "+
							"process silent for %s. Terminating worker and preserving output for replacement.",
							t.AssignedTo, t.ID, t.Title, sinceProgress.Round(time.Second),
							activity.sinceOutput.Round(time.Second))
						if activity.snippet != "" {
							content += fmt.Sprintf("\n\nLast output:\n```\n%s\n```", activity.snippet)
						}
					}
				default: // workerNoProcess, no session activity
					w.alertedTasks[t.ID] = "critical"
					if recentlySent {
						content = fmt.Sprintf("🟠 **DELIVERY GRACE**: Worker %s on task #%d (%s) "+
							"has no running process but sent a message within the last %s — "+
							"deferring auto-recover to confirm the deliverable was the final action.",
							t.AssignedTo, t.ID, t.Title, recentSendGrace)
					} else {
						shouldAutoCancel = true
						content = fmt.Sprintf("💀 **AUTO-RECOVERING**: Worker %s on task #%d (%s) — no progress for %s, "+
							"no running process found. Capturing output and resetting task for reassignment.",
							t.AssignedTo, t.ID, t.Title, sinceProgress.Round(time.Second))
					}
				}

				if shouldAutoCancel && w.autoCanceller != nil {
					capturedOutput := w.autoCanceller.GetRecentOutput(t.AssignedTo)
					w.autoCanceller.CancelWorker(t.AssignedTo)

					SaveOutputToWorkContext(state, t.ID, capturedOutput, t.AssignedTo, t.ProgressDescription, w.logger)

					oldAssignee := t.AssignedTo
					t.FailureCount++
					t.LastFailure = now
					t.FailureReason = "auto-cancelled: mandatory progress reporting violation"
					t.UpdatedAt = now
					if t.FailureCount >= w.maxTaskFailures {
						t.Status = "blocked"
						t.BlockedBy = fmt.Sprintf("Auto-blocked after %d failures. Worker output preserved in work context.", t.FailureCount)
					} else {
						t.Status = "pending"
						t.ResultSummary = fmt.Sprintf("Auto-cancelled (failure %d/%d): worker did not report progress. Output captured for replacement.", t.FailureCount, w.maxTaskFailures)
					}
					removeTaskFromInstanceByID(state, t.ID, oldAssignee)
					recoveredTasks++
					content += fmt.Sprintf("\n\nTask reset to %s (failure %d/%d). Previous output captured (%d bytes).",
						t.Status, t.FailureCount, w.maxTaskFailures, len(capturedOutput))
				}

				state.Messages = append(state.Messages, domain.Message{
					ID: state.NextMsgID, From: "system", To: driver,
					Content: content, Timestamp: now,
				})
				state.NextMsgID++
				w.logger.Printf("Watchdog: CRITICAL — task #%d no progress for %s (activity: %s, sessionActive: %v, autoCancel: %v)", t.ID, sinceProgress.Round(time.Second), activity.status, sessionActive, shouldAutoCancel)

			} else if sinceProgress > w.progressWarningThresh && currentLevel == "" {
				switch {
				case activity.status == workerActive || sessionActive:
					w.alertedTasks[t.ID] = "warning"
					var content string
					if sessionActive {
						content = fmt.Sprintf("⚠️ **Reporting violation**: Worker %s has not called `report_progress` on task #%d (%s) for %s. "+
							"Session is active — worker MUST call report_progress every 2-3 minutes. "+
							"Auto-cancellation will follow at the critical threshold if reporting does not resume.",
							t.AssignedTo, t.ID, t.Title, sinceProgress.Round(time.Second))
					} else {
						content = fmt.Sprintf("⚠️ **Reporting violation**: Worker %s has not called `report_progress` on task #%d (%s) for %s "+
							"(process active, last output %s ago). "+
							"Worker MUST call report_progress every 2-3 minutes. Auto-cancellation imminent if not corrected.",
							t.AssignedTo, t.ID, t.Title, sinceProgress.Round(time.Second),
							activity.sinceOutput.Round(time.Second))
					}
					state.Messages = append(state.Messages, domain.Message{
						ID: state.NextMsgID, From: "system", To: driver,
						Content: content, Timestamp: now,
					})
					state.NextMsgID++
					w.logger.Printf("Watchdog: WARNING — task #%d no progress for %s (active but non-compliant)", t.ID, sinceProgress.Round(time.Second))
				case activity.status == workerSilent:
					w.alertedTasks[t.ID] = "warning"
					content := fmt.Sprintf("⚠️ **Warning — imminent auto-cancel**: Worker %s has not reported progress on task #%d (%s) for %s, "+
						"and the process has been silent for %s. Worker will be auto-cancelled at the critical threshold. "+
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
					w.logger.Printf("Watchdog: WARNING — task #%d no progress for %s (SILENT for %s, auto-cancel imminent)", t.ID, sinceProgress.Round(time.Second), activity.sinceOutput.Round(time.Second))
				default: // workerNoProcess, no session activity
					w.alertedTasks[t.ID] = "warning"
					content := fmt.Sprintf("⚠️ **Warning — imminent auto-cancel**: Worker %s has not reported progress on task #%d (%s) for %s, "+
						"and no running process was found. Worker will be auto-cancelled at the critical threshold.",
						t.AssignedTo, t.ID, t.Title, sinceProgress.Round(time.Second))
					state.Messages = append(state.Messages, domain.Message{
						ID: state.NextMsgID, From: "system", To: driver,
						Content: content, Timestamp: now,
					})
					state.NextMsgID++
					w.logger.Printf("Watchdog: WARNING — task #%d no progress for %s (no process, auto-cancel imminent)", t.ID, sinceProgress.Round(time.Second))
				}
			}
		}

		// Send notification if anything was recovered
		if recoveredTasks > 0 || recoveredAgents > 0 {
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

		// Phase 4: Garbage-collect stale Presence and AgentInstance rows so
		// the worker pool doesn't grow monotonically forever. This is the
		// safety net for cases where reap-on-event (cancel_agent /
		// update_task) was missed (server crash, race, older code path).
		if w.pol != nil {
			prunedPresence = PrunePresence(state, w.pol.PresenceRetentionDays())
			prunedInstances = PruneInstances(state,
				w.pol.InstanceRetentionDays(),
				w.pol.TaskBoundInstanceRetentionHours())
			if prunedPresence > 0 {
				w.logger.Printf("Watchdog: GC pruned %d stale presence row(s)", prunedPresence)
			}
			if prunedInstances > 0 {
				w.logger.Printf("Watchdog: GC pruned %d offline agent instance row(s)", prunedInstances)
			}
			// Record cumulative GC counters so dashboards can show
			// "N pruned since startup". lastGCRun ticks every cycle the
			// GC phase ran, regardless of whether anything was pruned —
			// the useful signal is "the watchdog is actually checking",
			// not just "stuff was deleted".
			if prunedPresence > 0 {
				w.prunedPresenceTotal.Add(int64(prunedPresence))
			}
			if prunedInstances > 0 {
				w.prunedInstancesTotal.Add(int64(prunedInstances))
			}
			w.gcMu.Lock()
			w.lastGCRun = now
			w.gcMu.Unlock()
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

	if recoveredTasks > 0 || recoveredAgents > 0 || prunedSessions > 0 || prunedPresence > 0 || prunedInstances > 0 {
		w.logger.Printf("Watchdog: cycle complete — recovered %d task(s), %d agent(s), pruned %d session(s), %d presence row(s), %d instance row(s)",
			recoveredTasks, recoveredAgents, prunedSessions, prunedPresence, prunedInstances)
	}
}

// checkTaskBoundWorker checks whether a task-bound worker instance (e.g.
// "claude-code-task-3") exists for the given task, and if so, whether it is
// alive. Returns (exists, alive). When exists=true the task-bound worker is
// the authoritative liveness signal — type-level checks should be overridden.
//
// task.AssignedTo stores the parent agent type (e.g. "claude-code"), so the
// match reduces to comparing the candidate instance's AgentType against
// task.AssignedTo.
func (w *Watchdog) checkTaskBoundWorker(state *domain.CollabState, t *domain.Task, now time.Time) (exists bool, alive bool) {
	taskSuffix := fmt.Sprintf("-task-%d", t.ID)
	for id, inst := range state.AgentInstances {
		if inst == nil {
			continue
		}
		if !strings.HasSuffix(id, taskSuffix) {
			continue
		}
		if inst.AgentType != t.AssignedTo {
			continue
		}
		exists = true
		if w.isAgentAlive(id, inst, now, w.heartbeatStaleThresh) {
			return true, true
		}
	}
	// Also check process activity: the task-bound worker may be actively
	// producing output even if it hasn't registered in AgentInstances yet
	// (e.g., still in its startup phase before the first heartbeat).
	if w.processActivity != nil {
		procs := w.processActivity.GetProcessInfo()
		for id, proc := range procs {
			if strings.HasSuffix(id, taskSuffix) {
				exists = true
				if now.Sub(proc.LastOutputAt) <= w.heartbeatStaleThresh {
					return true, true
				}
			}
		}
	}
	return exists, false
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

// findOwnerInstanceForTask returns the AgentInstance that lists the given
// task in its CurrentTasks. Since task.AssignedTo now stores the parent
// type (e.g. "claude-code"), CurrentTasks is the only reliable way to
// identify the concrete owning instance among sibling pool instances.
//
// When multiple instances claim the task (e.g. a static pool instance that
// received the assignment plus a task-bound child that actually runs it),
// the task-bound child is preferred — it's the authoritative owner because
// its liveness cannot be confused with sibling task-bound workers.
// Returns nil when no instance currently claims the task.
func findOwnerInstanceForTask(state *domain.CollabState, t *domain.Task) *domain.AgentInstance {
	if state == nil || t == nil {
		return nil
	}
	var taskBound, other *domain.AgentInstance
	for _, inst := range state.AgentInstances {
		if inst == nil {
			continue
		}
		for _, tid := range inst.CurrentTasks {
			if tid != t.ID {
				continue
			}
			if _, isTB := StripTaskBoundSuffix(inst.InstanceID); isTB {
				if taskBound == nil {
					taskBound = inst
				}
			} else if other == nil {
				other = inst
			}
			break
		}
	}
	if taskBound != nil {
		return taskBound
	}
	return other
}

// pruneStaleSessions removes sessions from the registry whose agents show no
// recent activity from ANY source checked by isAgentAlive: session tool calls,
// state heartbeats, or process output (stdout/stderr).
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

const maxSnippetBytes = 500

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

	if sinceOutput < w.processSilentThresh {
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
