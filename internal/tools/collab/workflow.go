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

// registerHandoff registers the handoff tool.
func registerHandoff(s *server.MCPServer, svc *app.CollabService, logger *log.Logger) {
	s.AddTool(
		mcp.NewTool("handoff",
			mcp.WithDescription("Hand off work to your pair. Marks current task as needing their attention and sends them a detailed message."),
			mcp.WithString("from", mcp.Required(), mcp.Description("Your agent identifier")),
			mcp.WithString("to", mcp.Required(), mcp.Description("Agent to hand off to")),
			mcp.WithNumber("task_id", mcp.Description("Task ID to hand off (optional - uses current in-progress task)")),
			mcp.WithString("summary", mcp.Required(), mcp.Description("Summary of what was done")),
			mcp.WithString("next_steps", mcp.Required(), mcp.Description("What the other agent should do next")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := req.GetArguments()
			from, _ := args["from"].(string)
			to, _ := args["to"].(string)
			summary, _ := args["summary"].(string)
			nextSteps, _ := args["next_steps"].(string)

			if from == "" || to == "" || summary == "" || nextSteps == "" {
				return nil, fmt.Errorf("from, to, summary, and next_steps are required")
			}
			var taskInfo string
			if err := svc.Run(func(state *domain.CollabState) error {
				extra := app.RegisteredAgentNames(state)
				if err := app.ValidateAgent(from, state, false, false, extra...); err != nil {
					return err
				}
				if err := app.ValidateAgent(to, state, false, false, extra...); err != nil {
					return err
				}

				var taskID int
				if id, ok := args["task_id"].(float64); ok {
					taskID = int(id)
				} else {
					fromType := app.ResolveParentAgentType(state, from)
					for _, task := range state.Tasks {
						if task.Status == "in_progress" && (task.AssignedTo == from || task.AssignedTo == fromType) {
							taskID = task.ID
							break
						}
					}
				}

				if taskID > 0 {
					// Normalize the new assignee to its parent agent type so
					// downstream watchdog / liveness correlation works
					// uniformly whether the caller passes a parent name
					// ("claude-code"), a static-pool instance ID
					// ("claude-code-1"), or a task-bound child ID
					// ("claude-code-task-7"). Storing the raw instance ID was
					// the root of long-lived AssignedTo corruption that
					// MigrateTaskBoundCorruption otherwise had to repair.
					normalizedTo := app.ResolveParentAgentType(state, to)
					for i := range state.Tasks {
						if state.Tasks[i].ID == taskID {
							state.Tasks[i].AssignedTo = normalizedTo
							state.Tasks[i].Status = "pending"
							state.Tasks[i].UpdatedAt = time.Now()
							taskInfo = fmt.Sprintf(" (Task #%d reassigned to %s)", taskID, normalizedTo)
							// Always-scan removal: a task can be carried by a
							// task-bound child ("claude-code-task-7") even
							// when the parent name ("claude-code") still has
							// its own static-pool row in AgentInstances. The
							// previous direct-lookup-first short-circuit
							// would hit the static row, find nothing, and
							// silently leave the actual owner with a stale
							// CurrentTasks entry. We strip the task from
							// every instance that lists it.
							for _, inst := range state.AgentInstances {
								if inst == nil {
									continue
								}
								owns := false
								for _, id := range inst.CurrentTasks {
									if id == taskID {
										owns = true
										break
									}
								}
								if !owns {
									continue
								}
								newTasks := make([]int, 0, len(inst.CurrentTasks))
								for _, tid := range inst.CurrentTasks {
									if tid != taskID {
										newTasks = append(newTasks, tid)
									}
								}
								inst.CurrentTasks = newTasks
								if len(inst.CurrentTasks) == 0 && inst.Status == "busy" {
									inst.Status = "idle"
								}
							}
							break
						}
					}
				}

				msg := domain.Message{
					ID:        state.NextMsgID,
					From:      from,
					To:        to,
					Content:   fmt.Sprintf("## Handoff from %s\n\n### Summary\n%s\n\n### Next Steps\n%s", from, summary, nextSteps),
					Timestamp: time.Now(),
					Read:      false,
				}
				state.Messages = append(state.Messages, msg)
				state.NextMsgID++
				return nil
			}); err != nil {
				return nil, err
			}

			logger.Printf("handoff from %s to %s%s", from, to, taskInfo)
			return mcp.NewToolResultText(fmt.Sprintf("Handoff complete. %s notified.%s", to, taskInfo)), nil
		},
	)
}

// registerClaimNext registers the claim_next tool.
func registerClaimNext(s *server.MCPServer, svc *app.CollabService, logger *log.Logger) {
	s.AddTool(
		mcp.NewTool("claim_next",
			mcp.WithDescription("Get and claim the next available task. Use dry_run=true to peek without claiming. Returns highest priority item: message, in-progress task, or pending task."),
			mcp.WithString("agent", mcp.Required(), mcp.Description("Your agent identifier (cursor or claude-code)")),
			mcp.WithBoolean("dry_run", mcp.Description("If true, just peek at next action without claiming (default: false)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := req.GetArguments()
			agent, _ := args["agent"].(string)
			dryRun, _ := args["dry_run"].(bool)

			if agent == "" {
				return nil, fmt.Errorf("agent is required")
			}

			var result *mcp.CallToolResult
			err := svc.Run(func(state *domain.CollabState) error {
				extra := app.RegisteredAgentNames(state)
				if err := app.ValidateAgent(agent, state, false, false, extra...); err != nil {
					return err
				}

				for i := len(state.Messages) - 1; i >= 0; i-- {
					msg := state.Messages[i]
					if (msg.To == agent || msg.To == "all") && !msg.Read {
						result = mcp.NewToolResultText(fmt.Sprintf(`{"action":"read_messages","priority":"high","from":"%s","preview":"%s"}`,
							msg.From, escapeJSON(app.Truncate(msg.Content, 100))))
						return nil
					}
				}

				agentType := app.ResolveParentAgentType(state, agent)
				for _, task := range state.Tasks {
					if task.Status == "in_progress" && (task.AssignedTo == agent || task.AssignedTo == agentType) {
						result = mcp.NewToolResultText(fmt.Sprintf(`{"action":"continue_task","priority":"medium","task_id":%d,"title":"%s"}`,
							task.ID, escapeJSON(task.Title)))
						return nil
					}
				}

				var bestTask *domain.Task
				var bestIdx int
				for i := range state.Tasks {
					task := &state.Tasks[i]
					if task.Status == "pending" && (task.AssignedTo == agent || task.AssignedTo == agentType || task.AssignedTo == "any") {
						incomplete := checkDependenciesCompleteState(state, task.ID)
						if len(incomplete) > 0 {
							continue
						}
						// Deterministic tie-break on (Priority, ID) so two
						// equal-priority tasks always pick the lower ID.
						if bestTask == nil ||
							task.Priority < bestTask.Priority ||
							(task.Priority == bestTask.Priority && task.ID < bestTask.ID) {
							bestTask = task
							bestIdx = i
						}
					}
				}

				if bestTask != nil {
					priorityNames := map[int]string{1: "critical", 2: "high", 3: "normal", 4: "low"}
					if dryRun {
						result = mcp.NewToolResultText(fmt.Sprintf(`{"action":"claim_task","priority":"%s","task_id":%d,"title":"%s","dry_run":true}`,
							priorityNames[bestTask.Priority], bestTask.ID, escapeJSON(bestTask.Title)))
						return nil
					}
					// Store the parent agent type so the task owner is tracked
					// by type (not by ephemeral instance ID). The CurrentTasks
					// bookkeeping is delegated to app.AddTaskToInstance, which
					// correctly skips task-bound siblings in its fallback scan
					// — so a static-pool worker is always preferred over a
					// task-bound twin of the same parent type.
					state.Tasks[bestIdx].Status = "in_progress"
					state.Tasks[bestIdx].AssignedTo = agentType
					state.Tasks[bestIdx].UpdatedAt = time.Now()
					app.AddTaskToInstance(state, bestTask.ID, agent)
					if state.Tasks[bestIdx].ContextID != "" {
						autoLockTaskContextFiles(state, state.Tasks[bestIdx].ContextID, agent, svc.Policy().ValidatePath)
					}
					claimText := fmt.Sprintf("Claimed task #%d [%s]: %s\n\nDescription: %s",
						bestTask.ID, priorityNames[bestTask.Priority], bestTask.Title, bestTask.Description)
					if state.Tasks[bestIdx].ContextID != "" {
						if wc := state.WorkContexts[state.Tasks[bestIdx].ContextID]; wc != nil && len(wc.Constraints) > 0 {
							claimText += "\n\n⚠️ CONSTRAINTS (set by driver — you must obey these):\n"
							for _, c := range wc.Constraints {
								claimText += "  - " + c + "\n"
							}
						}
					}
					result = mcp.NewToolResultText(claimText)
					return nil
				}

				if state.ActivePlanID != "" {
					if plan, exists := state.Plans[state.ActivePlanID]; exists && plan != nil {
						for _, item := range plan.Items {
							if item.Status == "pending" && (item.Owner == agent || item.Owner == "" || item.Owner == "unassigned") {
								result = mcp.NewToolResultText(fmt.Sprintf(`{"action":"work_on_plan_item","priority":"normal","plan":"%s","item_id":"%s","title":"%s"}`,
									plan.ID, item.ID, escapeJSON(item.Title)))
								return nil
							}
						}
					}
				}

				result = mcp.NewToolResultText(`{"action":"idle","priority":"low","message":"No pending work. Wait for messages or create new tasks."}`)
				return nil
			})
			if err != nil {
				return nil, err
			}
			logger.Printf("claim_next for %s", agent)
			return result, nil
		},
	)
}

// registerRequestReview registers the request_review tool.
func registerRequestReview(s *server.MCPServer, svc *app.CollabService, logger *log.Logger) {
	s.AddTool(
		mcp.NewTool("request_review",
			mcp.WithDescription("Request a code review from your pair. Creates a review task and notifies them."),
			mcp.WithString("from", mcp.Required(), mcp.Description("Your agent identifier")),
			mcp.WithString("to", mcp.Required(), mcp.Description("Agent to request review from")),
			mcp.WithString("description", mcp.Required(), mcp.Description("What to focus on in the review")),
			mcp.WithArray("files", mcp.Description("Files to review")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := req.GetArguments()
			from, _ := args["from"].(string)
			to, _ := args["to"].(string)
			description, _ := args["description"].(string)

			var files []string
			if f, ok := args["files"].([]interface{}); ok {
				for _, file := range f {
					if s, ok := file.(string); ok {
						files = append(files, s)
					}
				}
			}

			if from == "" || to == "" || description == "" {
				return nil, fmt.Errorf("from, to, and description are required")
			}

			taskDesc := fmt.Sprintf("## Code Review Request\n\n%s", description)
			if len(files) > 0 {
				taskDesc += "\n\n### Files to Review\n"
				for _, f := range files {
					taskDesc += fmt.Sprintf("- `%s`\n", f)
				}
			}

			var taskID int
			if err := svc.Run(func(state *domain.CollabState) error {
				extra := app.RegisteredAgentNames(state)
				if err := app.ValidateAgent(from, state, false, false, extra...); err != nil {
					return err
				}
				if err := app.ValidateAgent(to, state, false, false, extra...); err != nil {
					return err
				}

				task := domain.Task{
					ID:          state.NextTaskID,
					Title:       fmt.Sprintf("Review: %s", app.Truncate(description, 50)),
					Description: taskDesc,
					Status:      "pending",
					AssignedTo:  to,
					CreatedBy:   from,
					CreatedAt:   time.Now(),
					UpdatedAt:   time.Now(),
					Priority:    2,
				}
				state.Tasks = append(state.Tasks, task)
				taskID = task.ID
				state.NextTaskID++

				// H5 — notify the reviewer. Without this the review
				// task sits silent in their pending queue (no banner,
				// no nudge) until the reviewer happens to call
				// list_tasks. This was the original asymmetry between
				// "create work" and "request review".
				notice := fmt.Sprintf("📝 **Review requested for task #%d** by %s — %s",
					taskID, from, app.Truncate(description, 120))
				if len(files) > 0 {
					notice += fmt.Sprintf(" (files: %s)", strings.Join(files, ", "))
				}
				state.Messages = append(state.Messages, domain.Message{
					ID:        state.NextMsgID,
					From:      "system",
					To:        to,
					Content:   notice,
					Timestamp: time.Now(),
				})
				state.NextMsgID++
				return nil
			}); err != nil {
				return nil, err
			}

			logger.Printf("request_review: %s requested review from %s (task #%d)", from, to, taskID)
			return mcp.NewToolResultText(fmt.Sprintf("Review requested. Task #%d created for %s.", taskID, to)), nil
		},
	)
}
