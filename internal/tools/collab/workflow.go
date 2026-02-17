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
					for _, task := range state.Tasks {
						if task.Status == "in_progress" && task.AssignedTo == from {
							taskID = task.ID
							break
						}
					}
				}

				if taskID > 0 {
					for i := range state.Tasks {
						if state.Tasks[i].ID == taskID {
							oldAssignee := state.Tasks[i].AssignedTo
							state.Tasks[i].AssignedTo = to
							state.Tasks[i].Status = "pending"
							state.Tasks[i].UpdatedAt = time.Now()
							taskInfo = fmt.Sprintf(" (Task #%d reassigned to %s)", taskID, to)
							// Clean up old assignee's CurrentTasks.
							// Try direct instance lookup first; fall back to scanning all instances
							// (handles multi-instance agents where oldAssignee is "claude-code" but
							// the instance key is "claude-code-1").
							if oldAssignee != "" && oldAssignee != to {
								if inst, ok := state.AgentInstances[oldAssignee]; ok && inst != nil {
									newTasks := make([]int, 0, len(inst.CurrentTasks))
									for _, tid := range inst.CurrentTasks {
										if tid != taskID {
											newTasks = append(newTasks, tid)
										}
									}
									inst.CurrentTasks = newTasks
									if len(inst.CurrentTasks) == 0 {
										inst.Status = "idle"
									}
								} else {
									// Fallback: scan all instances for the task
									for _, inst := range state.AgentInstances {
										if inst == nil {
											continue
										}
										for _, id := range inst.CurrentTasks {
											if id == taskID {
												newTasks := make([]int, 0, len(inst.CurrentTasks))
												for _, tid := range inst.CurrentTasks {
													if tid != taskID {
														newTasks = append(newTasks, tid)
													}
												}
												inst.CurrentTasks = newTasks
												if len(inst.CurrentTasks) == 0 {
													inst.Status = "idle"
												}
												break
											}
										}
									}
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

				for _, task := range state.Tasks {
					if task.Status == "in_progress" && task.AssignedTo == agent {
						result = mcp.NewToolResultText(fmt.Sprintf(`{"action":"continue_task","priority":"medium","task_id":%d,"title":"%s"}`,
							task.ID, escapeJSON(task.Title)))
						return nil
					}
				}

				var bestTask *domain.Task
				var bestIdx int
				for i := range state.Tasks {
					task := &state.Tasks[i]
					if task.Status == "pending" && (task.AssignedTo == agent || task.AssignedTo == "any") {
						incomplete := checkDependenciesCompleteState(state, task.ID)
						if len(incomplete) > 0 {
							continue
						}
						if bestTask == nil || task.Priority < bestTask.Priority {
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
					// Check if task was previously assigned to someone else (e.g. "any")
					// BEFORE updating AssignedTo, to avoid always-false comparison.
					wasAssignedElsewhere := state.Tasks[bestIdx].AssignedTo != agent
					state.Tasks[bestIdx].Status = "in_progress"
					state.Tasks[bestIdx].AssignedTo = agent
					state.Tasks[bestIdx].UpdatedAt = time.Now()
					// Track task on the worker instance (orchestrator may have already added when driver assigned)
					if wasAssignedElsewhere {
						if inst, ok := state.AgentInstances[agent]; ok && inst != nil {
							inst.CurrentTasks = append(inst.CurrentTasks, bestTask.ID)
							inst.Status = "busy"
						} else {
							for _, i := range state.AgentInstances {
								if i != nil && i.AgentType == agent {
									i.CurrentTasks = append(i.CurrentTasks, bestTask.ID)
									i.Status = "busy"
									break
								}
							}
						}
					}
					if state.Tasks[bestIdx].ContextID != "" {
						autoLockTaskContextFiles(state, state.Tasks[bestIdx].ContextID, agent, svc.Policy().ValidatePath)
					}
					result = mcp.NewToolResultText(fmt.Sprintf("Claimed task #%d [%s]: %s\n\nDescription: %s",
						bestTask.ID, priorityNames[bestTask.Priority], bestTask.Title, bestTask.Description))
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
				return nil
			}); err != nil {
				return nil, err
			}

			logger.Printf("request_review: %s requested review from %s (task #%d)", from, to, taskID)
			return mcp.NewToolResultText(fmt.Sprintf("Review requested. Task #%d created for %s.", taskID, to)), nil
		},
	)
}
