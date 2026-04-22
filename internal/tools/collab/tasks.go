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
func registerCreateTask(s *server.MCPServer, svc *app.CollabService, logger *log.Logger, orch *app.TaskOrchestrator, spawner TaskSpawner) {
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
			mcp.WithBoolean("requires_review", mcp.Description("Whether this task requires manual review approval before it can be marked completed")),
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
				// M5 — cycle detection at create. The task being
				// created has ID state.NextTaskID and the proposed
				// deps are `dependencies`.
				if app.HasDependencyCycle(state, state.NextTaskID, dependencies) {
					return fmt.Errorf("cannot create task: dependencies %v would form a circular dependency cycle", dependencies)
				}

				requiresReview, _ := args["requires_review"].(bool)
				reviewStatus := ""
				if requiresReview {
					reviewStatus = "pending"
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
					RequiresReview:      requiresReview,
					ReviewStatus:        reviewStatus,
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

			// Read back the effective assignee (auto-assignment may have changed it)
			var effectiveAssignee string
			_ = svc.Query(func(state *domain.CollabState) error {
				for _, t := range state.Tasks {
					if t.ID == taskID {
						effectiveAssignee = t.AssignedTo
						break
					}
				}
				return nil
			})
			if effectiveAssignee == "" {
				effectiveAssignee = assignedTo
			}

			if spawner != nil && effectiveAssignee != "" && effectiveAssignee != "any" && effectiveAssignee != createdBy &&
				revalidateSpawn(svc, taskID, effectiveAssignee) {
				spawner.SpawnForTask(taskID, effectiveAssignee)
			}

			depInfo := ""
			if len(dependencies) > 0 {
				depInfo = fmt.Sprintf(", depends on: %v", dependencies)
			}
			priorityNames := map[int]string{1: "critical", 2: "high", 3: "normal", 4: "low"}
			logger.Printf("Task #%d created by %s (priority: %s)", taskID, createdBy, priorityNames[priority])
			return mcp.NewToolResultText(fmt.Sprintf("Task #%d created: %s (assigned to: %s, priority: %s%s)",
				taskID, title, effectiveAssignee, priorityNames[priority], depInfo)), nil
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
					if len(task.Dependencies) > 0 {
						incomplete := checkDependenciesCompleteState(state, task.ID)
						if len(incomplete) > 0 {
							result += fmt.Sprintf("  Blocked by deps: %v\n", incomplete)
						} else {
							result += "  Dependencies satisfied\n"
						}
					}
					if task.RequiresReview {
						if task.ReviewedBy != "" {
							result += fmt.Sprintf("  Review: %s (by %s)\n", task.ReviewStatus, task.ReviewedBy)
						} else {
							result += fmt.Sprintf("  Review: %s\n", task.ReviewStatus)
						}
					}
					if task.FailureCount > 0 {
						result += fmt.Sprintf("  Failures: %d (last: %s)\n", task.FailureCount, task.FailureReason)
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
func registerUpdateTask(s *server.MCPServer, svc *app.CollabService, logger *log.Logger, orch *app.TaskOrchestrator, spawner TaskSpawner) {
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
			mcp.WithBoolean("requires_review", mcp.Description("Whether this task requires manual review before completion")),
			mcp.WithString("review_status", mcp.Description("New review status"), mcp.Enum("pending", "approved", "rejected")),
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
			var spawnAssignee string // set if we need to spawn a worker after update
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
						if v == "completed" && task.RequiresReview && task.ReviewStatus != "approved" {
							return fmt.Errorf("cannot complete task: review required (current status: %s)", task.ReviewStatus)
						}
						task.Status = v
					}
					if v, ok := args["requires_review"].(bool); ok {
						task.RequiresReview = v
						if v && task.ReviewStatus == "" {
							task.ReviewStatus = "pending"
						}
					}
					if v, ok := args["review_status"].(string); ok {
						updatedByType := app.ResolveParentAgentType(state, updatedBy)
						if v == "approved" && (task.AssignedTo == updatedBy || task.AssignedTo == updatedByType) {
							return fmt.Errorf("assignee cannot approve their own task")
						}
						task.ReviewStatus = v
						task.ReviewedBy = updatedBy
						if v == "rejected" {
							task.Status = "pending" // Re-open for fixes
						}
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
						app.RemoveTaskFromInstance(state, taskID, oldAssignee)
					}
					// Add to new owner when entering in_progress
					enteringInProgress := task.Status == "in_progress" && (oldStatus != "in_progress" || assigneeChanged)
					if enteringInProgress && task.AssignedTo != "" && task.AssignedTo != "any" {
						app.AddTaskToInstance(state, taskID, task.AssignedTo)
					}
					// Reap task-bound instances on terminal transitions. A
					// task-bound worker's whole reason for existing is the
					// owning task; once the task is completed/cancelled/
					// blocked we delete the AgentInstance + Presence rows so
					// they don't accumulate as zombies in worker_status.
					// task.AssignedTo stores the parent agent type, so we
					// reconstruct the conventional "<type>-task-<id>" name
					// via ReapTaskBoundInstanceForTask. Static pool workers
					// are left untouched.
					isTerminal := task.Status == "completed" || task.Status == "cancelled" || task.Status == "blocked"
					if isTerminal {
						if oldAssignee != "" {
							app.ReapTaskBoundInstanceForTask(state, oldAssignee, taskID)
						}
						if task.AssignedTo != "" && task.AssignedTo != "any" && task.AssignedTo != oldAssignee {
							app.ReapTaskBoundInstanceForTask(state, task.AssignedTo, taskID)
						}
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
						// H6 — clearing blocked_by must actively
						// reopen the task. If all dependencies are
						// complete, transition back to pending so
						// claim_next can pick it up. If any deps
						// remain incomplete, hold it as blocked
						// (the deps themselves are the new blocker).
						if v == "" && task.Status == "blocked" {
							incomplete := checkDependenciesCompleteState(state, task.ID)
							if len(incomplete) == 0 {
								task.Status = "pending"
							}
						}
						if v != "" && orch != nil {
							blockerType := updatedBy
							if inst, ok := state.AgentInstances[updatedBy]; ok && inst != nil {
								blockerType = inst.AgentType
							}
							app.RemoveTaskFromInstance(state, taskID, oldAssignee)

							newAssignee := orch.ReassignTask(task, state, []string{blockerType})
							driver := app.ConfiguredDriver(state)
							if newAssignee != "" {
								task.Status = "pending"
								state.Messages = append(state.Messages, domain.Message{
									ID:        state.NextMsgID,
									From:      "system",
									To:        driver,
									Content:   fmt.Sprintf("🔄 **Task #%d reassigned**: %s blocked by %q — reassigned to **%s**.", taskID, updatedBy, v, newAssignee),
									Timestamp: time.Now(),
								})
								state.NextMsgID++
							} else {
								state.Messages = append(state.Messages, domain.Message{
									ID:        state.NextMsgID,
									From:      "system",
									To:        driver,
									Content:   fmt.Sprintf("⊘ **Task #%d blocked**: %s blocked by %q — no alternative worker available.", taskID, updatedBy, v),
									Timestamp: time.Now(),
								})
								state.NextMsgID++
							}
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
						// M5 — disallow add-dep on already-active or
						// completed tasks. Adding a dep retroactively
						// changes its readiness, which is almost
						// always a bug. Caller can transition back to
						// pending first if they really need to.
						if task.Status == "in_progress" || task.Status == "completed" {
							return fmt.Errorf("cannot add dependency to task #%d in status %q; transition to pending first", task.ID, task.Status)
						}
						// M5 — cycle detection.
						if app.HasDependencyCycle(state, task.ID, []int{depIDInt}) {
							return fmt.Errorf("cannot add dependency #%d to task #%d: would create a circular dependency cycle", depIDInt, task.ID)
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

					// If task is reassigned to a worker and still pending, spawn for it
					if task.AssignedTo != oldAssignee && task.AssignedTo != "" &&
						task.AssignedTo != "any" && task.Status == "pending" {
						spawnAssignee = task.AssignedTo
					}

					return nil
				}
				return fmt.Errorf("task #%d not found", taskID)
			}); err != nil {
				return nil, err
			}

			if spawner != nil && spawnAssignee != "" && spawnAssignee != updatedBy &&
				revalidateSpawn(svc, taskID, spawnAssignee) {
				spawner.SpawnForTask(taskID, spawnAssignee)
			}

			logger.Printf("Task #%d updated by %s", taskID, updatedBy)
			return mcp.NewToolResultText(fmt.Sprintf("Task #%d updated", taskID)), nil
		},
	)
}

// registerReplayTask registers the replay_task tool.
func registerReplayTask(s *server.MCPServer, svc *app.CollabService, logger *log.Logger, spawner TaskSpawner) {
	s.AddTool(
		mcp.NewTool("replay_task",
			mcp.WithDescription("Reset failure tracking and re-queue a blocked task. Only works on blocked or pending tasks (rejects in_progress to avoid races)."),
			mcp.WithNumber("id", mcp.Required(), mcp.Description("Task ID to replay")),
			mcp.WithString("updated_by", mcp.Required(), mcp.Description("Who is replaying the task")),
			mcp.WithString("reassign_to", mcp.Description("Who to assign the replayed task to. Default: 'any' (let orchestrator pick). Use 'keep' to preserve the current assignee.")),
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
			reassignTo, _ := args["reassign_to"].(string)
			if reassignTo == "" {
				reassignTo = "any"
			}

			taskID := int(id)
			var effectiveAssignee string
			if err := svc.Run(func(state *domain.CollabState) error {
				extra := app.RegisteredAgentNames(state)
				if err := app.ValidateAgent(updatedBy, state, false, false, extra...); err != nil {
					return err
				}
				if reassignTo != "keep" {
					if err := app.ValidateAgent(reassignTo, state, true, false, extra...); err != nil {
						return err
					}
				}
				for i := range state.Tasks {
					if state.Tasks[i].ID != taskID {
						continue
					}
					task := &state.Tasks[i]
					if task.Status != "blocked" && task.Status != "pending" {
						return fmt.Errorf("cannot replay task #%d: status is '%s' (only blocked or pending tasks can be replayed)", taskID, task.Status)
					}
					task.Status = "pending"
					task.FailureCount = 0
					task.FailureReason = ""
					task.LastFailure = time.Time{}
					task.BlockedBy = ""
					task.ResultSummary = ""
					task.ProgressDescription = ""
					task.ProgressPercent = 0
					task.UpdatedAt = time.Now()
					if task.RequiresReview {
						task.ReviewStatus = "pending"
						task.ReviewedBy = ""
					} else {
						task.ReviewStatus = ""
						task.ReviewedBy = ""
					}
					if reassignTo != "keep" {
						task.AssignedTo = reassignTo
					}
					effectiveAssignee = task.AssignedTo
					return nil
				}
				return fmt.Errorf("task #%d not found", taskID)
			}); err != nil {
				return nil, err
			}

			if spawner != nil && effectiveAssignee != "" && effectiveAssignee != "any" && effectiveAssignee != updatedBy &&
				revalidateSpawn(svc, taskID, effectiveAssignee) {
				spawner.SpawnForTask(taskID, effectiveAssignee)
			}

			logger.Printf("Task #%d replayed by %s (assigned to: %s)", taskID, updatedBy, effectiveAssignee)
			return mcp.NewToolResultText(fmt.Sprintf("Task #%d replayed and reset to pending (assigned to: %s)", taskID, effectiveAssignee)), nil
		},
	)
}

// removeTaskFromInstance and addTaskToInstance moved to internal/app
// (RemoveTaskFromInstance / AddTaskToInstance) — single source of truth
// shared with cli_api.go, dashboard/api.go, and watchdog.go.

// revalidateSpawn re-checks task assignment and status under a fresh
// state lock right before SpawnForTask is invoked. Between the original
// write transaction releasing the lock and the spawner firing, another
// writer (watchdog, dashboard, CLI) can cancel, reassign, or complete
// the task — at which point spawning a worker would launch a process
// that immediately finds nothing to do. Returns true only if the task
// is still assigned to the expected agent and is in an active state.
func revalidateSpawn(svc *app.CollabService, taskID int, expectedAssignee string) bool {
	if svc == nil || expectedAssignee == "" {
		return false
	}
	ok := false
	_ = svc.Query(func(state *domain.CollabState) error {
		for _, t := range state.Tasks {
			if t.ID != taskID {
				continue
			}
			if t.AssignedTo != expectedAssignee {
				return nil
			}
			if t.Status != "pending" && t.Status != "in_progress" {
				return nil
			}
			ok = true
			return nil
		}
		return nil
	})
	return ok
}
