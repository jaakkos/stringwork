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

// registerCreateTask registers the create_task tool.
func registerCreateTask(s *server.MCPServer, svc *app.CollabService, logger *log.Logger, orch *app.TaskOrchestrator) {
	s.AddTool(
		mcp.NewTool("create_task",
			mcp.WithDescription("Create a shared task for the pair programming session. Use this to coordinate work and track progress."),
			mcp.WithString("title", mcp.Required(), mcp.Description("Short task title")),
			mcp.WithString("description", mcp.Description("Detailed task description")),
			mcp.WithString("assigned_to", mcp.Description("Who should work on this (e.g., 'cursor', 'claude-code', 'any')")),
			mcp.WithString("created_by", mcp.Required(), mcp.Description("Who created this task")),
			mcp.WithNumber("priority", mcp.Description("Task priority: 1=critical, 2=high, 3=normal (default), 4=low")),
			mcp.WithArray("relevant_files", mcp.Description("Files this task should focus on (for work context)")),
			mcp.WithString("background", mcp.Description("Architectural/background context for workers")),
			mcp.WithArray("constraints", mcp.Description("Constraints (e.g. 'do not modify X')")),
			mcp.WithString("parent_context_id", mcp.Description("Parent work context ID for subtask inheritance")),
			mcp.WithArray("depends_on", mcp.Description("Task IDs this task depends on")),
			mcp.WithNumber("expected_duration_seconds", mcp.Description("Expected task duration in seconds. The watchdog alerts the driver if this SLA is exceeded. Example: 300 for a 5-minute task.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := req.GetArguments()
			title, _ := args["title"].(string)
			description, _ := args["description"].(string)
			assignedTo, _ := args["assigned_to"].(string)
			createdBy, _ := args["created_by"].(string)

			priority := 3 // default normal
			if p, ok := args["priority"].(float64); ok {
				priority = int(p)
				if priority < 1 {
					priority = 1
				}
				if priority > 4 {
					priority = 4
				}
			}

			var dependencies []int
			if deps, ok := args["depends_on"].([]interface{}); ok {
				for _, d := range deps {
					if id, ok := d.(float64); ok {
						dependencies = append(dependencies, int(id))
					}
				}
			}
			var relevantFiles []string
			if rf, ok := args["relevant_files"].([]interface{}); ok {
				for _, x := range rf {
					if s, ok := x.(string); ok {
						relevantFiles = append(relevantFiles, s)
					}
				}
			}
			background, _ := args["background"].(string)
			var constraints []string
			if c, ok := args["constraints"].([]interface{}); ok {
				for _, x := range c {
					if s, ok := x.(string); ok {
						constraints = append(constraints, s)
					}
				}
			}
			parentContextID, _ := args["parent_context_id"].(string)

			expectedDurationSec := 0
			if eds, ok := args["expected_duration_seconds"].(float64); ok {
				expectedDurationSec = int(eds)
				if expectedDurationSec < 0 {
					expectedDurationSec = 0
				}
			}

			if title == "" || createdBy == "" {
				return nil, fmt.Errorf("title and created_by are required")
			}

			if assignedTo == "" {
				assignedTo = "any"
			}

			var taskID int
			if err := svc.Run(func(state *domain.CollabState) error {
				extra := app.RegisteredAgentNames(state)
				if err := app.ValidateAgent(createdBy, state, false, false, extra...); err != nil {
					return err
				}
				if err := app.ValidateAgent(assignedTo, state, true, false, extra...); err != nil {
					return err
				}

				for _, depID := range dependencies {
					found := false
					for _, t := range state.Tasks {
						if t.ID == depID {
							found = true
							break
						}
					}
					if !found {
						return fmt.Errorf("dependency task #%d not found", depID)
					}
				}

				task := domain.Task{
					ID:                  state.NextTaskID,
					Title:               title,
					Description:         description,
					Status:              "pending",
					AssignedTo:          assignedTo,
					CreatedBy:           createdBy,
					CreatedAt:           time.Now(),
					UpdatedAt:           time.Now(),
					Priority:            priority,
					Dependencies:        dependencies,
					ExpectedDurationSec: expectedDurationSec,
				}
				state.Tasks = append(state.Tasks, task)
				taskID = state.NextTaskID
				state.NextTaskID++

				if orch != nil && state.DriverID != "" && createdBy == state.DriverID && assignedTo == "any" {
					orch.AssignTask(&state.Tasks[len(state.Tasks)-1], state)
				}
				if len(relevantFiles) > 0 || background != "" || len(constraints) > 0 {
					ensureWorkContextForTask(state, taskID, relevantFiles, background, constraints, parentContextID)
				}
				return nil
			}); err != nil {
				return nil, err
			}

			depInfo := ""
			if len(dependencies) > 0 {
				depInfo = fmt.Sprintf(", depends on: %v", dependencies)
			}
			priorityNames := map[int]string{1: "critical", 2: "high", 3: "normal", 4: "low"}
			logger.Printf("Task #%d created by %s (priority: %s)", taskID, createdBy, priorityNames[priority])
			return mcp.NewToolResultText(fmt.Sprintf("Task #%d created: %s (assigned to: %s, priority: %s%s)",
				taskID, title, assignedTo, priorityNames[priority], depInfo)), nil
		},
	)
}

// registerListTasks registers the list_tasks tool.
func registerListTasks(s *server.MCPServer, svc *app.CollabService, logger *log.Logger) {
	s.AddTool(
		mcp.NewTool("list_tasks",
			mcp.WithDescription("List shared tasks. Check this to see what work needs to be done."),
			mcp.WithString("status", mcp.Description("Filter by status (default: 'all')"), mcp.Enum("all", "pending", "in_progress", "completed", "blocked")),
			mcp.WithString("assigned_to", mcp.Description("Filter by assignee")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := req.GetArguments()
			statusFilter := "all"
			if v, ok := args["status"].(string); ok {
				statusFilter = v
			}

			assignedFilter := ""
			if v, ok := args["assigned_to"].(string); ok {
				assignedFilter = v
			}

			var result string
			var count int
			var needsCtxUpdate bool
			// Use Query (read-only) for the listing itself.
			if err := svc.Query(func(state *domain.CollabState) error {
				if assignedFilter != "" {
					extra := app.RegisteredAgentNames(state)
					if err := app.ValidateAgent(assignedFilter, state, true, false, extra...); err != nil {
						return err
					}
				}

				count = 0
				for _, task := range state.Tasks {
					if statusFilter != "all" && task.Status != statusFilter {
						continue
					}
					if assignedFilter != "" && task.AssignedTo != assignedFilter && task.AssignedTo != "any" {
						continue
					}
					result += fmt.Sprintf("Task #%d [%s] - %s\n", task.ID, task.Status, task.Title)
					if task.Description != "" {
						result += fmt.Sprintf("  Description: %s\n", task.Description)
					}
					result += fmt.Sprintf("  Assigned to: %s, Created by: %s\n\n", task.AssignedTo, task.CreatedBy)
					count++
				}
				needsCtxUpdate = assignedFilter != "" && assignedFilter != "any" && count > 0
				return nil
			}); err != nil {
				return nil, err
			}
			// Update agent context in a separate write pass (only when needed).
			if needsCtxUpdate {
				_ = svc.Run(func(state *domain.CollabState) error {
					if agentCtx, exists := state.AgentContexts[assignedFilter]; exists {
						agentCtx.LastCheckedTaskID = state.NextTaskID - 1
						agentCtx.LastCheckTime = time.Now()
					} else {
						state.AgentContexts[assignedFilter] = &domain.AgentContext{
							Agent:             assignedFilter,
							LastCheckedMsgID:  state.NextMsgID - 1,
							LastCheckedTaskID: state.NextTaskID - 1,
							LastCheckTime:     time.Now(),
						}
					}
					return nil
				})
			}

			if count == 0 {
				return mcp.NewToolResultText("No tasks found"), nil
			}

			logger.Printf("Listed %d tasks", count)
			return mcp.NewToolResultText(result), nil
		},
	)
}

func checkDependenciesCompleteState(state *domain.CollabState, taskID int) []int {
	var task *domain.Task
	for i := range state.Tasks {
		if state.Tasks[i].ID == taskID {
			task = &state.Tasks[i]
			break
		}
	}
	if task == nil || len(task.Dependencies) == 0 {
		return nil
	}
	var incomplete []int
	for _, depID := range task.Dependencies {
		for _, t := range state.Tasks {
			if t.ID == depID && t.Status != "completed" {
				incomplete = append(incomplete, depID)
				break
			}
		}
	}
	return incomplete
}

// registerUpdateTask registers the update_task tool.
func registerUpdateTask(s *server.MCPServer, svc *app.CollabService, logger *log.Logger) {
	s.AddTool(
		mcp.NewTool("update_task",
			mcp.WithDescription("Update a shared task's status, assignment, priority, or dependencies."),
			mcp.WithNumber("id", mcp.Required(), mcp.Description("Task ID to update")),
			mcp.WithString("status", mcp.Description("New status"), mcp.Enum("pending", "in_progress", "completed", "blocked", "cancelled")),
			mcp.WithString("assigned_to", mcp.Description("New assignee")),
			mcp.WithString("updated_by", mcp.Required(), mcp.Description("Who is making this update")),
			mcp.WithNumber("priority", mcp.Description("New priority: 1=critical, 2=high, 3=normal, 4=low")),
			mcp.WithNumber("add_dependency", mcp.Description("Task ID to add as dependency")),
			mcp.WithNumber("remove_dependency", mcp.Description("Task ID to remove from dependencies")),
			mcp.WithString("blocked_by", mcp.Description("External blocker description (set to empty to clear)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := req.GetArguments()
			id, err := requireFloat64(args, "id")
			if err != nil {
				return nil, err
			}

			updatedBy, err := requireString(args, "updated_by")
			if err != nil {
				return nil, err
			}

			taskID := int(id)
			if err := svc.Run(func(state *domain.CollabState) error {
				extra := app.RegisteredAgentNames(state)
				if err := app.ValidateAgent(updatedBy, state, false, false, extra...); err != nil {
					return err
				}

				for i := range state.Tasks {
					if state.Tasks[i].ID != taskID {
						continue
					}
					task := &state.Tasks[i]
					oldStatus := task.Status
					oldAssignee := task.AssignedTo

					if v, ok := args["status"].(string); ok {
						if v == "in_progress" {
							incomplete := checkDependenciesCompleteState(state, task.ID)
							if len(incomplete) > 0 {
								return fmt.Errorf("cannot start: dependencies not complete: %v", incomplete)
							}
						}
						task.Status = v
					}
					if v, ok := args["assigned_to"].(string); ok {
						if err := app.ValidateAgent(v, state, true, false, extra...); err != nil {
							return err
						}
						task.AssignedTo = v
					}

					// --- CurrentTasks maintenance ---
					// Remove from old owner when:
					// - transitioning OUT of in_progress (completed, pending, blocked)
					// - assignee changed (task moves to a different worker)
					leavingInProgress := oldStatus == "in_progress" && task.Status != "in_progress"
					assigneeChanged := oldAssignee != "" && task.AssignedTo != oldAssignee
					if (leavingInProgress || assigneeChanged) && oldAssignee != "" {
						removeTaskFromInstance(state, taskID, oldAssignee)
					}
					// Add to new owner when entering in_progress
					enteringInProgress := task.Status == "in_progress" && (oldStatus != "in_progress" || assigneeChanged)
					if enteringInProgress && task.AssignedTo != "" && task.AssignedTo != "any" {
						addTaskToInstance(state, taskID, task.AssignedTo)
					}
					if v, ok := args["priority"].(float64); ok {
						p := int(v)
						if p < 1 {
							p = 1
						}
						if p > 4 {
							p = 4
						}
						task.Priority = p
					}
					if v, ok := args["blocked_by"].(string); ok {
						task.BlockedBy = v
						if v != "" && task.Status != "blocked" {
							task.Status = "blocked"
						}
					}
					if depID, ok := args["add_dependency"].(float64); ok {
						depIDInt := int(depID)
						found := false
						for _, t := range state.Tasks {
							if t.ID == depIDInt {
								found = true
								break
							}
						}
						if !found {
							return fmt.Errorf("dependency task #%d not found", depIDInt)
						}
						if depIDInt == task.ID {
							return fmt.Errorf("task cannot depend on itself")
						}
						alreadyDep := false
						for _, d := range task.Dependencies {
							if d == depIDInt {
								alreadyDep = true
								break
							}
						}
						if !alreadyDep {
							task.Dependencies = append(task.Dependencies, depIDInt)
						}
					}
					if depID, ok := args["remove_dependency"].(float64); ok {
						depIDInt := int(depID)
						newDeps := []int{}
						for _, d := range task.Dependencies {
							if d != depIDInt {
								newDeps = append(newDeps, d)
							}
						}
						task.Dependencies = newDeps
					}
					task.UpdatedAt = time.Now()

					return nil
				}
				return fmt.Errorf("task #%d not found", taskID)
			}); err != nil {
				return nil, err
			}

			logger.Printf("Task #%d updated by %s", taskID, updatedBy)
			return mcp.NewToolResultText(fmt.Sprintf("Task #%d updated", taskID)), nil
		},
	)
}

// removeTaskFromInstance removes taskID from the given agent's CurrentTasks.
// Tries direct instance lookup first, then falls back to scanning all instances
// (handles multi-instance agents where agent name differs from instance key).
func removeTaskFromInstance(state *domain.CollabState, taskID int, agent string) {
	if inst, ok := state.AgentInstances[agent]; ok && inst != nil {
		newTasks := make([]int, 0, len(inst.CurrentTasks))
		for _, id := range inst.CurrentTasks {
			if id != taskID {
				newTasks = append(newTasks, id)
			}
		}
		inst.CurrentTasks = newTasks
		if len(inst.CurrentTasks) == 0 && inst.Status == "busy" {
			inst.Status = "idle"
		}
		return
	}
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
				if len(inst.CurrentTasks) == 0 && inst.Status == "busy" {
					inst.Status = "idle"
				}
				return
			}
		}
	}
}

// addTaskToInstance adds taskID to the given agent's CurrentTasks (if not already present).
// Tries direct instance lookup first, then falls back to matching by AgentType.
func addTaskToInstance(state *domain.CollabState, taskID int, agent string) {
	inst, ok := state.AgentInstances[agent]
	if !ok || inst == nil {
		// Fallback: find by AgentType
		for _, i := range state.AgentInstances {
			if i != nil && i.AgentType == agent {
				inst = i
				break
			}
		}
	}
	if inst == nil {
		return
	}
	for _, id := range inst.CurrentTasks {
		if id == taskID {
			return // already tracked
		}
	}
	inst.CurrentTasks = append(inst.CurrentTasks, taskID)
	inst.Status = "busy"
}
