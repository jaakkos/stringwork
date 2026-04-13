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
	// a soft reminder is appended to the piggyback banner.
	progressNudgeThreshold = 90 * time.Second

	// progressUrgentThreshold is how long without report_progress before
	// an urgent warning is appended to the piggyback banner.
	progressUrgentThreshold = 180 * time.Second
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

// heartbeatTracker debounces auto-heartbeat state writes.
// Each PiggybackMiddleware instance owns its own tracker, avoiding
// package-level mutable state and giving tests natural isolation.
type heartbeatTracker struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func newHeartbeatTracker() *heartbeatTracker {
	return &heartbeatTracker{last: make(map[string]time.Time)}
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

	_ = svc.Run(func(state *domain.CollabState) error {
		if inst, ok := state.AgentInstances[agent]; ok && inst != nil {
			inst.LastHeartbeat = now
			return nil
		}
		for _, inst := range state.AgentInstances {
			if inst != nil && inst.AgentType == agent {
				inst.LastHeartbeat = now
				return nil
			}
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
	hbt := newHeartbeatTracker()
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
		for _, task := range state.Tasks {
			if task.AssignedTo != agent && task.AssignedTo != "any" {
				continue
			}
			switch task.Status {
			case "pending":
				pending++
			case "cancelled":
				cancelled++
			case "in_progress":
				// Only check tasks directly assigned to this agent (not "any").
				if task.AssignedTo != agent {
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
			nudgeText = progressNudgeText(stalestTaskID, stalestSince)
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
