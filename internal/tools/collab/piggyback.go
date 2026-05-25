package collab

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/jaakkos/stringwork/internal/app"
	"github.com/jaakkos/stringwork/internal/domain"
)

const (
	// autoHeartbeatDebounce is the minimum interval between auto-heartbeat
	// state writes. Prevents excessive writes on every tool call.
	autoHeartbeatDebounce = 30 * time.Second

	// progressNudgeThreshold is how long without report_progress before
	// a soft reminder is appended to the piggyback banner. Bumped from
	// 90s in Q2/2026 alongside the watchdog progress thresholds so the
	// nudge ladder (nudge → urgent → WARNING → CRITICAL) stays ordered.
	progressNudgeThreshold = 120 * time.Second

	// progressUrgentThreshold is how long without report_progress before
	// an urgent warning is appended to the piggyback banner. Bumped from
	// 180s in Q2/2026 to keep the urgent banner ahead of the watchdog
	// WARNING (now 4 min) by ~1 minute.
	progressUrgentThreshold = 240 * time.Second

	// stopTombstoneTTL is how long a cancelled-task tombstone keeps
	// counting toward the STOP banner. Cancelled tasks linger in
	// state with AssignedTo = "<parent-type>" forever (they are
	// never reassigned or pruned by current GC paths). Even with
	// the spawnCutoff in BuildBanner, a cancellation issued moments
	// before the daemon process restarted, or by a driver that
	// never set its LastSpawnedAt, can still reach a freshly
	// spawned worker and STOP it. After this window the tombstone
	// is uncorrelated with anything currently happening — drop it
	// from the banner so the worker can proceed.
	//
	// 24h is a deliberate balance: short enough that yesterday's
	// noise does not block today's work, long enough that a
	// just-cancelled task still stops a respawn within one work
	// shift. Sites running multi-day batch jobs (long-form codex
	// benchmarks, migration runs) may want this longer; short-lived
	// CI test harnesses may want it shorter. Promote to a policy
	// field if/when an operator actually asks — see Worker A's
	// QUESTION on this constant in the constitution PR review.
	stopTombstoneTTL = 24 * time.Hour
)

// suppressNudgeTools lists tools that should not show progress nudges
// (they are themselves progress/status tools, or would cause feedback loops).
// Note: read_messages and get_session_context are also in suppressBannerTools
// which short-circuits before nudge evaluation; they are kept here defensively.
var suppressNudgeTools = map[string]struct{}{
	"heartbeat":           {},
	"report_progress":     {},
	"read_messages":       {},
	"get_session_context": {},
}

// ProcessLivenessProvider answers "is the spawned worker process for
// this instance still alive?" The piggyback auto-heartbeat consults
// this BEFORE bumping LastHeartbeat so a stray tool call (replay,
// late-arriving message) cannot extend the effective heartbeat of a
// dead worker past the watchdog staleness threshold (M4). Workers with
// no registered process row (HTTP-only, no spawn) bypass the gate and
// retain the legacy refresh-on-every-call behavior.
//
// HasWorker exists so the gate can distinguish "registered AND dead"
// (skip refresh) from "no row, HTTP-only worker" (refresh as before).
// IsWorkerRunning alone is ambiguous because a missing row also returns
// false in any natural implementation.
type ProcessLivenessProvider interface {
	IsWorkerRunning(instanceID string) bool
	HasWorker(instanceID string) bool
}

// heartbeatTracker debounces auto-heartbeat state writes.
// Each PiggybackMiddleware instance owns its own tracker, avoiding
// package-level mutable state and giving tests natural isolation.
type heartbeatTracker struct {
	mu       sync.Mutex
	last     map[string]time.Time
	liveness ProcessLivenessProvider
}

func newHeartbeatTracker() *heartbeatTracker {
	return &heartbeatTracker{last: make(map[string]time.Time)}
}

func newHeartbeatTrackerWithLiveness(p ProcessLivenessProvider) *heartbeatTracker {
	return &heartbeatTracker{last: make(map[string]time.Time), liveness: p}
}

// track updates the agent's LastHeartbeat in state, debounced at
// autoHeartbeatDebounce to avoid excessive state writes on every tool call.
func (h *heartbeatTracker) track(svc *app.CollabService, agent string) {
	now := time.Now()

	h.mu.Lock()
	last, ok := h.last[agent]
	if ok && now.Sub(last) < autoHeartbeatDebounce {
		h.mu.Unlock()
		return
	}
	h.last[agent] = now
	h.mu.Unlock()

	// M4 gate: when a ProcessLivenessProvider is registered AND knows
	// about this agent, only refresh when the process is alive. If the
	// provider has no row for this agent (HTTP-only worker), fall
	// through to the legacy refresh-on-every-call behavior.
	if h.liveness != nil {
		// Resolve the parent-type ping to a concrete instance ID.
		// Tests often pass the parent type as `agent`; the spawn
		// registry keys on InstanceID.
		instanceID := agent
		if !h.liveness.HasWorker(agent) {
			_ = svc.Query(func(state *domain.CollabState) error {
				if _, ok := state.AgentInstances[agent]; !ok {
					for id, inst := range state.AgentInstances {
						if inst != nil && inst.AgentType == agent && h.liveness.HasWorker(id) {
							instanceID = id
							break
						}
					}
				}
				return nil
			})
		}
		if h.liveness.HasWorker(instanceID) && !h.liveness.IsWorkerRunning(instanceID) {
			return
		}
	}

	_ = svc.Run(func(state *domain.CollabState) error {
		// H3 — two-pass match. First pass: exact InstanceID. Second
		// pass: parent-type fallback that excludes task-bound siblings,
		// and updates EVERY matching static-pool instance (not just
		// whichever the map iteration lands on first), so the same
		// ping cannot leave one sibling stale while refreshing another
		// non-deterministically.
		if inst, ok := state.AgentInstances[agent]; ok && inst != nil {
			inst.LastHeartbeat = now
			return nil
		}
		for id, inst := range state.AgentInstances {
			if inst == nil || inst.AgentType != agent {
				continue
			}
			if app.IsTaskBoundInstance(state, id) {
				continue
			}
			inst.LastHeartbeat = now
		}
		return nil
	})
}

// suppressBannerTools lists tools that already display unread state or would
// cause redundant loops if they included the piggyback banner.
var suppressBannerTools = map[string]struct{}{
	"read_messages":       {},
	"get_session_context": {},
}

// PiggybackMiddleware returns a mcp-go ToolHandlerMiddleware that appends a
// notification banner to tool responses when the connected agent has unread
// messages or pending tasks. Tools in suppressBannerTools are skipped.
// It also records session activity for watchdog liveness tracking.
func PiggybackMiddleware(svc *app.CollabService, registry *app.SessionRegistry) server.ToolHandlerMiddleware {
	return PiggybackMiddlewareWithLiveness(svc, registry, nil)
}

// PiggybackMiddlewareWithLiveness is like PiggybackMiddleware but also wires
// a ProcessLivenessProvider so the auto-heartbeat refresh skips workers
// whose underlying spawned process has died (M4). Pass nil for the provider
// to retain the legacy refresh-on-every-call behavior.
func PiggybackMiddlewareWithLiveness(svc *app.CollabService, registry *app.SessionRegistry, liveness ProcessLivenessProvider) server.ToolHandlerMiddleware {
	var hbt *heartbeatTracker
	if liveness != nil {
		hbt = newHeartbeatTrackerWithLiveness(liveness)
	} else {
		hbt = newHeartbeatTracker()
	}
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			// Record session activity for watchdog liveness tracking.
			if session := server.ClientSessionFromContext(ctx); session != nil {
				registry.TouchSession(session.SessionID())
			}

			// Keep agent's LastHeartbeat fresh from tool activity (debounced).
			agent := agentFromContext(ctx, registry)
			if agent != "" {
				hbt.track(svc, agent)
			}

			result, err := next(ctx, req)
			if err != nil || result == nil {
				return result, err
			}
			if result.IsError {
				return result, nil
			}

			toolName := req.Params.Name
			if _, suppress := suppressBannerTools[toolName]; suppress {
				return result, nil
			}

			if agent == "" {
				return result, nil
			}

			banner := BuildBanner(svc, agent, toolName)
			if banner == "" {
				return result, nil
			}

			appendBannerToResult(result, banner)
			return result, nil
		}
	}
}

// agentFromContext extracts the agent name for the current session.
func agentFromContext(ctx context.Context, registry *app.SessionRegistry) string {
	session := server.ClientSessionFromContext(ctx)
	if session == nil {
		return ""
	}
	return registry.GetAgent(session.SessionID())
}

// BuildBanner checks state for the given agent and returns a notification
// banner string. Returns "" if there is nothing to report.
// If the agent has cancelled tasks, a STOP directive is returned instead of a normal banner.
// toolName is the current tool being invoked (used to suppress nudges on progress tools).
func BuildBanner(svc *app.CollabService, agent, toolName string) string {
	if agent == "" {
		return ""
	}

	var unread, pending, cancelled int
	var nudgeText string
	_ = svc.Query(func(state *domain.CollabState) error {
		for _, msg := range state.Messages {
			if (msg.To == agent || msg.To == "all") && !msg.Read {
				unread++
			}
		}
		now := time.Now()
		var stalestSince time.Duration
		var stalestTaskID int
		agentType := app.ResolveParentAgentType(state, agent)
		// spawnCutoff is the moment the currently-running worker for this
		// agent was last (re)spawned. STOP banners must be scoped to that
		// lifetime: a fresh worker that inherits a parent type whose
		// PREVIOUS occupant was cancelled should not see those stale
		// cancellations. Without this gate, every cancel_agent leaves a
		// "STOP" tombstone that infects every future spawn of the same
		// type until the cancelled task is reassigned/terminal — which is
		// what was looping the daemon (claude-code-task-26 cancelled at
		// 12:23:17 → claude-code-task-28/30/...-1/-2 all immediately
		// exited on first tool call).
		//
		// Resolution: prefer an exact InstanceID match; fall back to the
		// most recently spawned non-task-bound sibling of agentType. A
		// zero cutoff (drivers, HTTP-only callers, custom agents that
		// never spawn) preserves the legacy count-all behavior.
		var spawnCutoff time.Time
		if inst, ok := state.AgentInstances[agent]; ok && inst != nil {
			spawnCutoff = inst.LastSpawnedAt
		} else if agentType != "" {
			for _, inst := range state.AgentInstances {
				if inst == nil || inst.AgentType != agentType {
					continue
				}
				if app.IsTaskBoundInstance(state, inst.InstanceID) {
					continue
				}
				if inst.LastSpawnedAt.After(spawnCutoff) {
					spawnCutoff = inst.LastSpawnedAt
				}
			}
		}
		// Driver-side fallback. The cursor driver has no AgentInstance
		// row in classic deployments, and even when it does the row
		// may have been created with a zero LastSpawnedAt by a code
		// path predating Fix #3b. Without a cutoff, every cancelled
		// task triggers a STOP banner on every cursor tool call —
		// including ones for tasks cancelled long before this daemon
		// process started. DaemonStartedAt seeds the cutoff so the
		// driver never sees STOPs for those stale tombstones.
		if spawnCutoff.IsZero() && agent == state.DriverID && !state.DaemonStartedAt.IsZero() {
			spawnCutoff = state.DaemonStartedAt
		}
		for _, task := range state.Tasks {
			if task.AssignedTo != agent && task.AssignedTo != agentType && task.AssignedTo != "any" {
				continue
			}
			switch task.Status {
			case "pending":
				pending++
			case "cancelled":
				if !spawnCutoff.IsZero() && task.UpdatedAt.Before(spawnCutoff) {
					continue
				}
				// Stale-tombstone TTL — see stopTombstoneTTL below
				// for the rationale and tuning notes.
				if !task.UpdatedAt.IsZero() && now.Sub(task.UpdatedAt) > stopTombstoneTTL {
					continue
				}
				cancelled++
			case "in_progress":
				// Only check tasks directly assigned to this agent (not "any").
				if task.AssignedTo != agent && task.AssignedTo != agentType {
					continue
				}
				lastProgress := task.UpdatedAt
				if !task.LastProgressAt.IsZero() {
					lastProgress = task.LastProgressAt
				}
				since := now.Sub(lastProgress)
				if since > stalestSince {
					stalestSince = since
					stalestTaskID = task.ID
				}
			}
		}

		if _, suppress := suppressNudgeTools[toolName]; !suppress {
			orch := svc.Policy().Orchestration()
			if orch != nil && AgentIsDriverForSession(state, agent, orch, nil) && !driverHasOwnedInProgressTask(state, agent) {
				// Orchestrating driver: in_progress tasks assigned to the parent
				// type (e.g. "claude-code") are owned by worker instances via
				// CurrentTasks — do not nudge the driver to report_progress.
			} else {
				nudgeText = progressNudgeText(stalestTaskID, stalestSince)
			}
		}
		return nil
	})

	// Cancellation takes priority — inject a hard STOP directive
	if cancelled > 0 {
		return fmt.Sprintf("\n\n---\n🛑 **STOP: %d of your task(s) have been cancelled.** The driver no longer needs this work. Stop immediately, call read_messages to see details, and exit.", cancelled)
	}

	var banner string
	if unread > 0 || pending > 0 {
		parts := ""
		if unread > 0 {
			parts += fmt.Sprintf("%d unread message(s)", unread)
		}
		if pending > 0 {
			if parts != "" {
				parts += " and "
			}
			parts += fmt.Sprintf("%d pending task(s)", pending)
		}
		banner = fmt.Sprintf("\n\n---\nYou have %s. Call read_messages or get_session_context to see them.", parts)
	}

	if nudgeText != "" {
		if banner == "" {
			banner = "\n\n---\n" + nudgeText
		} else {
			banner += "\n" + nudgeText
		}
	}

	return banner
}

// driverHasOwnedInProgressTask is true when the configured driver is doing
// hybrid work. Ownership is CurrentTasks on the driver's instance, or an
// in_progress task assigned to a concrete instance ID other than the parent
// agent type (pool alias AssignedTo "claude-code" is worker-owned).
func driverHasOwnedInProgressTask(state *domain.CollabState, agent string) bool {
	if state == nil || agent == "" {
		return false
	}
	inst := findAgentInstance(state, agent)
	if inst == nil {
		return false
	}
	for _, tid := range inst.CurrentTasks {
		for i := range state.Tasks {
			t := &state.Tasks[i]
			if t.ID == tid && t.Status == "in_progress" {
				return true
			}
		}
	}
	parentType := inst.AgentType
	if parentType == "" {
		parentType = app.ResolveParentAgentType(state, agent)
	}
	for i := range state.Tasks {
		t := &state.Tasks[i]
		if t.Status != "in_progress" || t.AssignedTo != agent {
			continue
		}
		// Parent-type pool assignment is not hybrid driver work.
		if t.AssignedTo == parentType {
			continue
		}
		return true
	}
	return false
}

// progressNudgeText returns a progress reminder string for a stale task,
// or "" if the task is within acceptable thresholds.
func progressNudgeText(taskID int, since time.Duration) string {
	if taskID == 0 {
		return ""
	}
	mins := int(since.Minutes())
	if since >= progressUrgentThreshold {
		return fmt.Sprintf("⛔ MANDATORY: Task #%d has no progress report for %dm. "+
			"You MUST call report_progress with task_id=%d IMMEDIATELY. "+
			"The watchdog is monitoring you — workers that do not report progress are AUTO-CANCELLED "+
			"and their tasks reassigned. This is your final warning before escalation.",
			taskID, mins, taskID)
	}
	if since >= progressNudgeThreshold {
		return fmt.Sprintf("⚠️ REQUIRED: Task #%d last progress report was %dm ago. "+
			"Call report_progress NOW with task_id=%d. "+
			"Progress reporting every 2-3 minutes is MANDATORY, not optional. "+
			"Failure to comply will result in auto-cancellation.",
			taskID, mins, taskID)
	}
	return ""
}

// appendBannerToResult appends text to the last text content block, or adds a new one.
func appendBannerToResult(result *mcp.CallToolResult, banner string) {
	for i := len(result.Content) - 1; i >= 0; i-- {
		if tc, ok := result.Content[i].(mcp.TextContent); ok {
			result.Content[i] = mcp.TextContent{
				Annotated: tc.Annotated,
				Type:      "text",
				Text:      tc.Text + banner,
			}
			return
		}
	}
	// No text block found; add one
	result.Content = append(result.Content, mcp.TextContent{
		Type: "text",
		Text: banner,
	})
}
