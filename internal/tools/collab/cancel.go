package collab

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/jaakkos/stringwork/internal/app"
	"github.com/jaakkos/stringwork/internal/domain"
)

// WorkerCanceller is implemented by WorkerManager. It allows the cancel_agent
// tool to kill spawned worker processes without importing the full WorkerManager.
type WorkerCanceller interface {
	CancelWorker(instanceID string) bool
	IsWorkerRunning(instanceID string) bool
}

// registerCancelAgent registers the cancel_agent tool.
// canceller is optional; when nil, only soft cancellation (state + messages) is performed.
func registerCancelAgent(s *server.MCPServer, svc *app.CollabService, logger *log.Logger, canceller WorkerCanceller) {
	s.AddTool(
		mcp.NewTool("cancel_agent",
			mcp.WithDescription("Cancel a worker agent's current work. Cancels all in-progress tasks for the agent, sends a STOP message, and kills the spawned process if running. Use this when you no longer need the agent's work."),
			mcp.WithString("agent", mcp.Required(), mcp.Description("Agent to cancel (e.g. 'claude-code', 'codex')")),
			mcp.WithString("cancelled_by", mcp.Required(), mcp.Description("Who is cancelling (e.g. 'cursor')")),
			mcp.WithString("reason", mcp.Description("Why the agent is being cancelled (optional)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := req.GetArguments()
			agent, _ := args["agent"].(string)
			cancelledBy, _ := args["cancelled_by"].(string)
			reason, _ := args["reason"].(string)

			if agent == "" || cancelledBy == "" {
				return nil, fmt.Errorf("agent and cancelled_by are required")
			}

			var cancelledTasks []int
			var agentFound bool

			// Phase 1: Cancel all in-progress tasks and send STOP message.
			if err := svc.Run(func(state *domain.CollabState) error {
				extra := app.RegisteredAgentNames(state)
				if err := app.ValidateAgent(cancelledBy, state, false, false, extra...); err != nil {
					return err
				}
				if err := app.ValidateAgent(agent, state, false, false, extra...); err != nil {
					return err
				}
				agentFound = true

				now := time.Now()

				// Cancel all in_progress tasks for this agent
				for i := range state.Tasks {
					t := &state.Tasks[i]
					if t.Status != "in_progress" {
						continue
					}
					if t.AssignedTo != agent {
						// Also check by agent type for multi-instance workers
						matchesType := false
						for _, inst := range state.AgentInstances {
							if inst != nil && inst.InstanceID == t.AssignedTo && inst.AgentType == agent {
								matchesType = true
								break
							}
						}
						if !matchesType {
							continue
						}
					}

					t.Status = "cancelled"
					t.UpdatedAt = now
					if reason != "" {
						t.ResultSummary = fmt.Sprintf("Cancelled by %s: %s", cancelledBy, reason)
					} else {
						t.ResultSummary = fmt.Sprintf("Cancelled by %s", cancelledBy)
					}
					cancelledTasks = append(cancelledTasks, t.ID)

					// Clean up agent instance task tracking
					removeTaskFromInstance(state, t.ID, t.AssignedTo)
				}

				// Mark agent instance as idle
				if inst, ok := state.AgentInstances[agent]; ok && inst != nil {
					inst.CurrentTasks = nil
					inst.Status = "idle"
				}
				for _, inst := range state.AgentInstances {
					if inst != nil && inst.AgentType == agent {
						inst.CurrentTasks = nil
						inst.Status = "idle"
					}
				}

				// Send STOP message to the agent
				stopContent := fmt.Sprintf("🛑 **STOP**: %s has cancelled your work.", cancelledBy)
				if reason != "" {
					stopContent += fmt.Sprintf(" Reason: %s.", reason)
				}
				if len(cancelledTasks) > 0 {
					taskIDs := make([]string, len(cancelledTasks))
					for i, id := range cancelledTasks {
						taskIDs[i] = fmt.Sprintf("#%d", id)
					}
					stopContent += fmt.Sprintf(" Cancelled tasks: %s.", strings.Join(taskIDs, ", "))
				}
				stopContent += " **Stop all work immediately and exit.**"

				state.Messages = append(state.Messages, domain.Message{
					ID:        state.NextMsgID,
					From:      "system",
					To:        agent,
					Content:   stopContent,
					Timestamp: now,
				})
				state.NextMsgID++

				return nil
			}); err != nil {
				return nil, err
			}

			if !agentFound {
				return nil, fmt.Errorf("agent %q not found", agent)
			}

			// Phase 2: Kill the spawned worker process (if running).
			processKilled := false
			if canceller != nil {
				processKilled = canceller.CancelWorker(agent)
			}

			// Build response
			var parts []string
			if len(cancelledTasks) > 0 {
				taskIDs := make([]string, len(cancelledTasks))
				for i, id := range cancelledTasks {
					taskIDs[i] = fmt.Sprintf("#%d", id)
				}
				parts = append(parts, fmt.Sprintf("cancelled %d task(s): %s", len(cancelledTasks), strings.Join(taskIDs, ", ")))
			}
			parts = append(parts, "STOP message sent")
			if processKilled {
				parts = append(parts, "worker process killed")
			}

			result := fmt.Sprintf("Agent %s stopped: %s", agent, strings.Join(parts, ", "))
			logger.Printf("cancel_agent: %s cancelled %s — %s", cancelledBy, agent, strings.Join(parts, ", "))
			return mcp.NewToolResultText(result), nil
		},
	)
}
