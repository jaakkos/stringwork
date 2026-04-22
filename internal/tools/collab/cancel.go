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
	GetRecentOutput(instanceID string) string
}

// registerCancelAgent registers the cancel_agent tool.
// canceller is optional; when nil, only soft cancellation (state + messages) is performed.
func registerCancelAgent(s *server.MCPServer, svc *app.CollabService, logger *log.Logger, canceller WorkerCanceller, pip ProcessInfoProvider) {
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

			// Capture output before cancellation so it's not lost
			var capturedOutput string
			if canceller != nil {
				capturedOutput = canceller.GetRecentOutput(agent)
			}

			// Phase 1: Cancel all in-progress tasks, capture output, and send STOP message.
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

				for i := range state.Tasks {
					t := &state.Tasks[i]
					if t.Status != "in_progress" {
						continue
					}
					if t.AssignedTo != agent {
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

					if capturedOutput != "" {
						app.SaveOutputToWorkContext(state, t.ID, capturedOutput, agent, t.ProgressDescription, logger)
					}

					removeTaskFromInstance(state, t.ID, t.AssignedTo)
				}

				// Reap task-bound instance rows; idle out static pool rows.
				// Task-bound instances (e.g. "claude-code-task-7") have no
				// reason to outlive their task, so we delete both the
				// AgentInstance and Presence rows. Static pool workers
				// (e.g. "claude-code") get marked idle so they can be
				// re-used for the next task.
				var toReap []string
				if inst, ok := state.AgentInstances[agent]; ok && inst != nil {
					if app.IsTaskBoundInstance(state, agent) {
						toReap = append(toReap, agent)
					} else {
						inst.CurrentTasks = nil
						inst.Status = "idle"
					}
				}
				for id, inst := range state.AgentInstances {
					if inst == nil || inst.AgentType != agent || id == agent {
						continue
					}
					if app.IsTaskBoundInstance(state, id) {
						toReap = append(toReap, id)
					} else {
						inst.CurrentTasks = nil
						inst.Status = "idle"
					}
				}
				for _, id := range toReap {
					delete(state.AgentInstances, id)
					delete(state.Presence, id)
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

				// Synthetic deliverable recovery (Bug D safety net):
				// If the worker captured output but never managed to call
				// send_message (typical when the LLM was killed mid-final
				// `send`), surface a truncated tail to the driver so the
				// work isn't silently lost.
				if capturedOutput != "" {
					recentlySent := false
					if state.LastSendByAgent != nil {
						if lastSend, ok := state.LastSendByAgent[agent]; ok && !lastSend.IsZero() {
							if now.Sub(lastSend) < time.Hour {
								recentlySent = true
							}
						}
					}
					// Fallback for when LastSendByAgent isn't populated yet
					// (e.g. server restart between the worker's send and
					// this cancel): scan recent messages for any send from
					// the agent in the last hour.
					if !recentlySent {
						cutoff := now.Add(-time.Hour)
						for i := len(state.Messages) - 1; i >= 0; i-- {
							m := state.Messages[i]
							if m.Timestamp.Before(cutoff) {
								break
							}
							if m.From == agent {
								recentlySent = true
								break
							}
						}
					}
					if !recentlySent {
						driver := app.ConfiguredDriver(state)
						if driver == "" {
							driver = cancelledBy
						}
						const maxRecoveryBytes = 4096
						tail := capturedOutput
						truncated := false
						if len(tail) > maxRecoveryBytes {
							tail = tail[len(tail)-maxRecoveryBytes:]
							truncated = true
						}
						body := "⚠️ Auto-recovered output (worker did not send before cancel):\n\n```\n"
						if truncated {
							body += "...(truncated)...\n"
						}
						body += tail
						body += "\n```"

						state.Messages = append(state.Messages, domain.Message{
							ID:        state.NextMsgID,
							From:      agent,
							To:        driver,
							Content:   body,
							Timestamp: now,
						})
						state.NextMsgID++
					}
				}

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

			// Warn if the worker was actively producing output when cancelled
			if pip != nil {
				procs := pip.GetProcessInfo()
				prefix := agent + "-"
				for id, p := range procs {
					if id == agent || strings.HasPrefix(id, prefix) {
						if !p.LastOutputAt.IsZero() {
							age := time.Since(p.LastOutputAt)
							if age < 30*time.Second {
								parts = append(parts, fmt.Sprintf("⚠️  worker was actively producing output (last output %s ago, %d bytes total)", age.Round(time.Second), p.OutputBytes))
							}
						}
					}
				}
			}

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
