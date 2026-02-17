package collab

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/jaakkos/stringwork/internal/app"
	"github.com/jaakkos/stringwork/internal/domain"
)

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
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			// Record session activity for watchdog liveness tracking.
			if session := server.ClientSessionFromContext(ctx); session != nil {
				registry.TouchSession(session.SessionID())
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

			// Determine connected agent from the session registry.
			agent := agentFromContext(ctx, registry)
			if agent == "" {
				return result, nil
			}

			banner := buildBanner(svc, agent)
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

// buildBanner checks state for the given agent and returns a notification
// banner string. Returns "" if there is nothing to report.
// If the agent has cancelled tasks, a STOP directive is returned instead of a normal banner.
func buildBanner(svc *app.CollabService, agent string) string {
	if agent == "" {
		return ""
	}

	var unread, pending, cancelled int
	_ = svc.Query(func(state *domain.CollabState) error {
		for _, msg := range state.Messages {
			if (msg.To == agent || msg.To == "all") && !msg.Read {
				unread++
			}
		}
		for _, task := range state.Tasks {
			if task.AssignedTo != agent && task.AssignedTo != "any" {
				continue
			}
			switch task.Status {
			case "pending":
				pending++
			case "cancelled":
				cancelled++
			}
		}
		return nil
	})

	// Cancellation takes priority — inject a hard STOP directive
	if cancelled > 0 {
		return fmt.Sprintf("\n\n---\n🛑 **STOP: %d of your task(s) have been cancelled.** The driver no longer needs this work. Stop immediately, call read_messages to see details, and exit.", cancelled)
	}

	if unread == 0 && pending == 0 {
		return ""
	}

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

	return fmt.Sprintf("\n\n---\nYou have %s. Call read_messages or get_session_context to see them.", parts)
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
