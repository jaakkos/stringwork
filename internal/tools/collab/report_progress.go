package collab

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/jaakkos/stringwork/internal/app"
	"github.com/jaakkos/stringwork/internal/domain"
)

// registerReportProgress registers the report_progress tool for structured task progress reporting.
func registerReportProgress(s *server.MCPServer, svc *app.CollabService, logger *log.Logger) {
	s.AddTool(
		mcp.NewTool("report_progress",
			mcp.WithDescription(
				"Report progress on a task you're working on. REQUIRED: call this every 2-3 minutes while working on a task. "+
					"This updates the task's progress fields AND refreshes your heartbeat. "+
					"The driver and watchdog use this to determine if you're making progress or stuck. "+
					"Tasks without progress reports for 3+ minutes trigger a warning to the driver."),
			mcp.WithString("agent", mcp.Required(), mcp.Description("Your agent or instance ID")),
			mcp.WithNumber("task_id", mcp.Required(), mcp.Description("ID of the task you're working on")),
			mcp.WithString("description", mcp.Required(), mcp.Description(
				"What you've done and what you're doing now. Be specific. "+
					"Example: 'Finished auth middleware (12/15 tests passing). Now fixing 3 failing tests. ~2 min remaining.'")),
			mcp.WithNumber("percent_complete", mcp.Description("Estimated completion percentage (0-100). Helps the driver gauge progress.")),
			mcp.WithNumber("eta_seconds", mcp.Description("Estimated seconds until task completion. Helps the driver decide whether to wait or cancel.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := req.GetArguments()
			agent, _ := args["agent"].(string)
			taskID := 0
			if id, ok := args["task_id"].(float64); ok {
				taskID = int(id)
			}
			description, _ := args["description"].(string)
			percentComplete := -1
			if pc, ok := args["percent_complete"].(float64); ok {
				percentComplete = int(pc)
				if percentComplete < 0 {
					percentComplete = 0
				}
				if percentComplete > 100 {
					percentComplete = 100
				}
			}
			etaSeconds := 0
			if eta, ok := args["eta_seconds"].(float64); ok {
				etaSeconds = int(eta)
			}

			if agent == "" || taskID == 0 || description == "" {
				return nil, fmt.Errorf("agent, task_id, and description are required")
			}

			err := svc.Run(func(state *domain.CollabState) error {
				now := time.Now()

				// Update the task's progress
				taskFound := false
				for i := range state.Tasks {
					t := &state.Tasks[i]
					if t.ID != taskID {
						continue
					}
					taskFound = true
					if t.Status != "in_progress" {
						return fmt.Errorf("task #%d is not in_progress (status: %s)", taskID, t.Status)
					}
					t.ProgressDescription = description
					t.LastProgressAt = now
					if percentComplete >= 0 {
						t.ProgressPercent = percentComplete
					}
					break
				}
				if !taskFound {
					return fmt.Errorf("task #%d not found", taskID)
				}

				// Also refresh the agent's heartbeat and progress
				inst := findAgentInstance(state, agent)
				if inst != nil {
					inst.LastHeartbeat = now
					inst.Progress = description
					inst.ProgressUpdatedAt = now
					if inst.Status == "offline" {
						inst.Status = "busy"
					}
				}

				return nil
			})
			if err != nil {
				return nil, err
			}

			response := fmt.Sprintf("Progress recorded for task #%d", taskID)
			if percentComplete >= 0 {
				response += fmt.Sprintf(" (%d%% complete)", percentComplete)
			}
			if etaSeconds > 0 {
				response += fmt.Sprintf(", ETA: %s", formatDuration(etaSeconds))
			}
			logger.Printf("report_progress: task #%d by %s: %s", taskID, agent, description)
			return mcp.NewToolResultText(response), nil
		},
	)
}

// findAgentInstance looks up an AgentInstance by instance ID or agent type.
func findAgentInstance(state *domain.CollabState, agent string) *domain.AgentInstance {
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

// formatDuration formats seconds into a human-readable duration string.
func formatDuration(seconds int) string {
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	if seconds < 3600 {
		return fmt.Sprintf("%dm %ds", seconds/60, seconds%60)
	}
	return fmt.Sprintf("%dh %dm", seconds/3600, (seconds%3600)/60)
}
