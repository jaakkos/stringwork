package collab

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/jaakkos/stringwork/internal/app"
	"github.com/jaakkos/stringwork/internal/domain"
)

const defaultArgsMaxLen = 1000

// AuditMiddleware returns a ToolHandlerMiddleware that records every tool call
// into the audit log via synchronous writes. Returns a no-op passthrough if
// writer is nil (audit disabled).
func AuditMiddleware(writer app.AuditWriter, registry *app.SessionRegistry, argsMaxLen int) server.ToolHandlerMiddleware {
	if argsMaxLen <= 0 {
		argsMaxLen = defaultArgsMaxLen
	}
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		if writer == nil {
			return next
		}
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			start := time.Now()

			agent := agentFromContext(ctx, registry)
			session := server.ClientSessionFromContext(ctx)
			sessionID := ""
			if session != nil {
				sessionID = session.SessionID()
			}

			result, err := next(ctx, req)

			entry := domain.AuditEntry{
				Timestamp:  start,
				Agent:      agent,
				ToolName:   req.Params.Name,
				DurationMs: time.Since(start).Milliseconds(),
				SessionID:  sessionID,
			}

			if err != nil {
				entry.Error = err.Error()
			} else if result != nil && result.IsError {
				for _, c := range result.Content {
					if tc, ok := c.(mcp.TextContent); ok {
						entry.Error = tc.Text
						break
					}
				}
			}

			if req.Params.Arguments != nil {
				args, ok := req.Params.Arguments.(map[string]any)
				if !ok {
					// Fallback if not a map
					summary, _ := json.Marshal(req.Params.Arguments)
					entry.ArgsSummary = string(summary)
				} else {
					// Basic redaction for sensitive keys
					redacted := make(map[string]any)
					for k, v := range args {
						lk := strings.ToLower(k)
						if strings.Contains(lk, "api_key") || strings.Contains(lk, "secret") ||
							strings.Contains(lk, "token") || strings.Contains(lk, "password") {
							redacted[k] = "[REDACTED]"
						} else {
							redacted[k] = v
						}
					}
					summary, _ := json.Marshal(redacted)
					entry.ArgsSummary = string(summary)
				}
				if len(entry.ArgsSummary) > argsMaxLen {
					if argsMaxLen > 3 {
						entry.ArgsSummary = entry.ArgsSummary[:argsMaxLen-3] + "..."
					} else {
						entry.ArgsSummary = entry.ArgsSummary[:argsMaxLen]
					}
				}
			}

			_ = writer.WriteAudit(entry)

			return result, err
		}
	}
}
