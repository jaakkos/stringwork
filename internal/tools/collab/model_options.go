package collab

import (
	"context"
	"log"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/jaakkos/stringwork/internal/app"
	"github.com/jaakkos/stringwork/internal/policy"
)

// registerListModelOptions registers list_model_options for drivers to discover
// configured worker models and tier selection guidance.
func registerListModelOptions(s *server.MCPServer, svc *app.CollabService, logger *log.Logger) {
	s.AddTool(
		mcp.NewTool("list_model_options",
			mcp.WithDescription("List configured worker model tiers, per-provider CLI models, and guidance for choosing model_tier on create_task. Call at session start or before delegating tasks."),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			orch := svc.Policy().Orchestration()
			text := policy.FormatModelSelectionGuide(orch)
			if text == "" {
				text = "No orchestration workers configured. Model selection applies to spawned worker CLIs only."
			}
			logger.Printf("list_model_options")
			return mcp.NewToolResultText(text), nil
		},
	)
}
