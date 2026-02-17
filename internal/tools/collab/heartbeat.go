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

// registerHeartbeat registers the heartbeat tool (workers call this to signal liveness).
func registerHeartbeat(s *server.MCPServer, svc *app.CollabService, logger *log.Logger) {
	s.AddTool(
		mcp.NewTool("heartbeat",
			mcp.WithDescription(
				"Signal liveness and optionally report progress. REQUIRED: call this every 60-90 seconds while working. "+
					"Include progress details so the driver and server know you're making progress (not stuck). "+
					"Workers that don't heartbeat are considered dead after 5 minutes and their tasks are recovered."),
			mcp.WithString("agent", mcp.Required(), mcp.Description("Your agent or instance ID (e.g. claude-code-1, codex)")),
			mcp.WithString("progress", mcp.Description("What you're currently doing (e.g. 'writing unit tests for auth middleware'). STRONGLY RECOMMENDED on every heartbeat.")),
			mcp.WithNumber("step", mcp.Description("Current step number (e.g. 3 of 5). Use with total_steps.")),
			mcp.WithNumber("total_steps", mcp.Description("Total number of steps in your current work.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := req.GetArguments()
			agent, _ := args["agent"].(string)
			if agent == "" {
				return nil, fmt.Errorf("agent is required")
			}

			progress, _ := args["progress"].(string)
			step := 0
			if s, ok := args["step"].(float64); ok {
				step = int(s)
			}
			totalSteps := 0
			if ts, ok := args["total_steps"].(float64); ok {
				totalSteps = int(ts)
			}

			err := svc.Run(func(state *domain.CollabState) error {
				inst, ok := state.AgentInstances[agent]
				if !ok {
					var match *domain.AgentInstance
					matchCount := 0
					for _, i := range state.AgentInstances {
						if i != nil && i.AgentType == agent {
							match = i
							matchCount++
						}
					}
					if matchCount == 1 {
						inst = match
					} else if matchCount > 1 {
						return fmt.Errorf("ambiguous agent %q: %d instances exist — use the specific instance ID (e.g. %q)", agent, matchCount, agent+"-1")
					}
				}
				if inst == nil {
					if _, registered := state.RegisteredAgents[agent]; registered {
						inst = &domain.AgentInstance{
							InstanceID:   agent,
							AgentType:    agent,
							Role:         domain.RoleWorker,
							Status:       "idle",
							CurrentTasks: []int{},
						}
						state.AgentInstances[agent] = inst
					}
				}
				if inst == nil {
					return fmt.Errorf("unknown agent %q", agent)
				}
				now := time.Now()
				inst.LastHeartbeat = now
				if inst.Status == "offline" {
					inst.Status = "idle"
				}
				// Update progress metadata
				if progress != "" {
					inst.Progress = progress
					inst.ProgressUpdatedAt = now
				}
				if step > 0 {
					inst.ProgressStep = step
				}
				if totalSteps > 0 {
					inst.ProgressTotalSteps = totalSteps
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
			if progress != "" {
				logger.Printf("heartbeat from %s (progress: %s)", agent, progress)
			} else {
				logger.Printf("heartbeat from %s", agent)
			}
			return mcp.NewToolResultText("OK"), nil
		},
	)
}
