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

			// Phase 0: discover all instance IDs that should receive STOP
			// signals — the named target plus every sibling AgentInstance
			// whose AgentType matches the resolved parent type. We need
			// this before fetching outputs so each instance's tail can be
			// captured separately and routed back to the task it owned.
			var targetIDs []string
			seenTargets := make(map[string]bool)
			_ = svc.Query(func(state *domain.CollabState) error {
				parentType := app.ResolveParentAgentType(state, agent)
				addTarget := func(id string) {
					if id == "" || seenTargets[id] {
						return
					}
					seenTargets[id] = true
					targetIDs = append(targetIDs, id)
				}
				if _, ok := state.AgentInstances[agent]; ok {
					addTarget(agent)
				}
				for id, inst := range state.AgentInstances {
					if inst == nil {
						continue
					}
					if inst.AgentType == parentType || inst.AgentType == agent {
						addTarget(id)
					}
				}
				if len(targetIDs) == 0 {
					addTarget(agent)
				}
				return nil
			})

			// Phase 0.5: capture each target's own output BEFORE we kill
			// it. This is per-instance so outputs aren't smeared across
			// every cancelled task in the loop below.
			outputs := make(map[string]string)
			if canceller != nil {
				for _, id := range targetIDs {
					if out := canceller.GetRecentOutput(id); out != "" {
						outputs[id] = out
					}
				}
			}

			// Phase 1: Cancel all in-progress tasks, capture per-task
			// output, and send STOP messages to every target instance.
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

				agentType := app.ResolveParentAgentType(state, agent)

				// Resolve which instance currently owns each task by
				// scanning CurrentTasks; falls back to t.AssignedTo if
				// nobody claims it.
				ownerOf := func(taskID int) string {
					for id, inst := range state.AgentInstances {
						if inst == nil {
							continue
						}
						for _, tid := range inst.CurrentTasks {
							if tid == taskID {
								return id
							}
						}
					}
					return ""
				}

				for i := range state.Tasks {
					t := &state.Tasks[i]
					if t.Status != "in_progress" {
						continue
					}
					if t.AssignedTo != agent && t.AssignedTo != agentType {
						continue
					}

					t.Status = "cancelled"
					t.UpdatedAt = now
					if reason != "" {
						t.ResultSummary = fmt.Sprintf("Cancelled by %s: %s", cancelledBy, reason)
					} else {
						t.ResultSummary = fmt.Sprintf("Cancelled by %s", cancelledBy)
					}
					cancelledTasks = append(cancelledTasks, t.ID)

					owner := ownerOf(t.ID)
					out := outputs[owner]
					if out == "" {
						out = outputs[agent]
					}
					if out != "" {
						instLabel := owner
						if instLabel == "" {
							instLabel = agent
						}
						app.SaveOutputToWorkContext(state, t.ID, out, instLabel, t.ProgressDescription, logger)
					}

					app.RemoveTaskFromInstance(state, t.ID, t.AssignedTo)
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

				// Send STOP message to every targeted instance so the
				// actual worker process listening on its own InstanceID
				// inbox sees the signal — not just the parent type.
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

				stopRecipients := append([]string(nil), targetIDs...)
				if !seenTargets[agent] {
					stopRecipients = append(stopRecipients, agent)
				}
				for _, to := range stopRecipients {
					state.Messages = append(state.Messages, domain.Message{
						ID:        state.NextMsgID,
						From:      "system",
						To:        to,
						Content:   stopContent,
						Timestamp: now,
					})
					state.NextMsgID++
				}

				// Synthetic deliverable recovery (Bug D safety net),
				// per-instance: each target with captured output that
				// has NOT recently sent a message gets its own recovery
				// envelope. This avoids smearing tails across instances
				// and avoids duplicate recoveries for instances whose
				// worker already delivered cleanly.
				driver := app.ConfiguredDriver(state)
				if driver == "" {
					driver = cancelledBy
				}
				cutoff := now.Add(-time.Hour)
				for _, id := range targetIDs {
					tail := outputs[id]
					if tail == "" {
						continue
					}
					recentlySent := false
					if state.LastSendByAgent != nil {
						if lastSend, ok := state.LastSendByAgent[id]; ok && !lastSend.IsZero() {
							if now.Sub(lastSend) < time.Hour {
								recentlySent = true
							}
						}
					}
					if !recentlySent {
						for i := len(state.Messages) - 1; i >= 0; i-- {
							m := state.Messages[i]
							if m.Timestamp.Before(cutoff) {
								break
							}
							if m.From == id {
								recentlySent = true
								break
							}
						}
					}
					if recentlySent {
						continue
					}
					const maxRecoveryBytes = 4096
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
						From:      id,
						To:        driver,
						Content:   body,
						Timestamp: now,
					})
					state.NextMsgID++
				}

				return nil
			}); err != nil {
				return nil, err
			}

			if !agentFound {
				return nil, fmt.Errorf("agent %q not found", agent)
			}

			// Phase 2: Kill the spawned worker process(es) (if running).
			// We attempt cancellation on every target instance so that
			// task-bound processes (e.g. "claude-code-task-7") are
			// killed alongside any static-pool sibling.
			processKilled := false
			if canceller != nil {
				for _, id := range targetIDs {
					if canceller.CancelWorker(id) {
						processKilled = true
					}
				}
				if !seenTargets[agent] {
					if canceller.CancelWorker(agent) {
						processKilled = true
					}
				}
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
