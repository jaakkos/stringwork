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

// registerCreatePlan registers the create_plan tool.
func registerCreatePlan(s *server.MCPServer, svc *app.CollabService, logger *log.Logger) {
	s.AddTool(
		mcp.NewTool("create_plan",
			mcp.WithDescription("Create a new shared plan for collaborative work. Both agents can add items and update progress."),
			mcp.WithString("id", mcp.Required(), mcp.Description("Unique plan ID (e.g., 'auth-refactor', 'p0-features')")),
			mcp.WithString("title", mcp.Required(), mcp.Description("Plan title")),
			mcp.WithString("goal", mcp.Required(), mcp.Description("What we're trying to achieve")),
			mcp.WithString("context", mcp.Description("Background, constraints, or scope")),
			mcp.WithString("created_by", mcp.Required(), mcp.Description("Agent creating the plan (cursor or claude-code)")),
			mcp.WithBoolean("set_active", mcp.Description("Set this as the active plan (default: true)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := req.GetArguments()
			id, _ := args["id"].(string)
			title, _ := args["title"].(string)
			goal, _ := args["goal"].(string)
			planContext, _ := args["context"].(string)
			createdBy, _ := args["created_by"].(string)

			setActive := true
			if v, ok := args["set_active"].(bool); ok {
				setActive = v
			}

			if id == "" || title == "" || goal == "" || createdBy == "" {
				return nil, fmt.Errorf("id, title, goal, and created_by are required")
			}

			if err := svc.Run(func(state *domain.CollabState) error {
				extra := app.RegisteredAgentNames(state)
				if err := app.ValidateAgent(createdBy, state, false, false, extra...); err != nil {
					return err
				}

				if _, exists := state.Plans[id]; exists {
					return fmt.Errorf("plan %q already exists", id)
				}
				now := time.Now()
				plan := &domain.Plan{
					ID:        id,
					Title:     title,
					Goal:      goal,
					Context:   planContext,
					Items:     []domain.PlanItem{},
					CreatedBy: createdBy,
					CreatedAt: now,
					UpdatedAt: now,
					Status:    "active",
				}
				state.Plans[id] = plan
				if setActive {
					state.ActivePlanID = id
				}
				return nil
			}); err != nil {
				return nil, err
			}

			logger.Printf("Plan %q created by %s", id, createdBy)
			result := fmt.Sprintf("Plan created: %s\n\nGoal: %s", title, goal)
			if setActive {
				result += "\n\n(Set as active plan)"
			}
			return mcp.NewToolResultText(result), nil
		},
	)
}

// registerGetPlan registers the get_plan tool.
func registerGetPlan(s *server.MCPServer, svc *app.CollabService, logger *log.Logger) {
	s.AddTool(
		mcp.NewTool("get_plan",
			mcp.WithDescription("Get a shared plan with all items and their status. Shows the active plan if no ID specified."),
			mcp.WithString("id", mcp.Description("Plan ID (omit to get the active plan)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := req.GetArguments()
			id, _ := args["id"].(string)

			var result string
			var planNotFound bool
			if err := svc.Query(func(state *domain.CollabState) error {
				planID := id
				if planID == "" {
					planID = state.ActivePlanID
				}
				if planID == "" {
					result = "No active plan. Use create_plan to start one."
					return nil
				}
				plan, exists := state.Plans[planID]
				if !exists {
					planNotFound = true
					return nil
				}
				result += fmt.Sprintf("# %s\n", plan.Title)
				result += fmt.Sprintf("ID: %s | Status: %s | Created by: %s\n", plan.ID, plan.Status, plan.CreatedBy)
				result += fmt.Sprintf("\n## Goal\n%s\n", plan.Goal)
				if plan.Context != "" {
					result += fmt.Sprintf("\n## Context\n%s\n", plan.Context)
				}
				result += "\n## Items\n"
				if len(plan.Items) == 0 {
					result += "(No items yet - use update_plan action='add_item' to add work items)\n"
				} else {
					statusOrder := []string{"in_progress", "pending", "blocked", "completed", "cancelled"}
					statusEmoji := map[string]string{
						"pending": "○", "in_progress": "●", "completed": "✓", "blocked": "⊘", "cancelled": "✗",
					}
					for _, status := range statusOrder {
						var items []domain.PlanItem
						for _, item := range plan.Items {
							if item.Status == status {
								items = append(items, item)
							}
						}
						if len(items) == 0 {
							continue
						}
						result += fmt.Sprintf("\n### %s\n", status)
						for _, item := range items {
							emoji := statusEmoji[item.Status]
							owner := item.Owner
							if owner == "" {
								owner = "unassigned"
							}
							result += fmt.Sprintf("%s [%s] %s - %s\n", emoji, item.ID, item.Title, owner)
							if item.Description != "" {
								result += fmt.Sprintf("    %s\n", app.Truncate(item.Description, 80))
							}
							if len(item.Dependencies) > 0 {
								result += fmt.Sprintf("    Depends on: %v\n", item.Dependencies)
							}
							if len(item.Blockers) > 0 {
								result += fmt.Sprintf("    Blockers: %v\n", item.Blockers)
							}
							if item.Reasoning != "" {
								result += fmt.Sprintf("    Reasoning: %s\n", app.Truncate(item.Reasoning, 120))
							}
							if len(item.Acceptance) > 0 {
								result += fmt.Sprintf("    Acceptance: %v\n", item.Acceptance)
							}
							if len(item.Constraints) > 0 {
								result += fmt.Sprintf("    Constraints: %v\n", item.Constraints)
							}
						}
					}
				}
				pendingCount, inProgressCount, completedCount := 0, 0, 0
				for _, item := range plan.Items {
					switch item.Status {
					case "pending":
						pendingCount++
					case "in_progress":
						inProgressCount++
					case "completed":
						completedCount++
					}
				}
				result += fmt.Sprintf("\n---\nProgress: %d completed, %d in progress, %d pending\n",
					completedCount, inProgressCount, pendingCount)
				return nil
			}); err != nil {
				return nil, err
			}
			if planNotFound {
				return nil, fmt.Errorf("plan %q not found", id)
			}
			logger.Printf("Got plan %q", id)
			return mcp.NewToolResultText(result), nil
		},
	)
}

// registerUpdatePlan registers the update_plan tool (combines add_item and update_item).
func registerUpdatePlan(s *server.MCPServer, svc *app.CollabService, logger *log.Logger) {
	s.AddTool(
		mcp.NewTool("update_plan",
			mcp.WithDescription("Add or update items in a shared plan. Use action='add_item' to add new items, action='update_item' to modify existing items."),
			mcp.WithString("action", mcp.Required(), mcp.Description("Action to perform"), mcp.Enum("add_item", "update_item")),
			mcp.WithString("plan_id", mcp.Description("Plan ID (omit to use active plan)")),
			mcp.WithString("id", mcp.Description("Item ID (e.g., '1', '1.1', 'auth-setup') - required for add_item")),
			mcp.WithString("item_id", mcp.Description("Item ID to update - required for update_item")),
			mcp.WithString("title", mcp.Description("Short item title (for add_item)")),
			mcp.WithString("description", mcp.Description("Detailed description")),
			mcp.WithString("owner", mcp.Description("Who will work on this (cursor, claude-code, codex, or empty)")),
			mcp.WithString("status", mcp.Description("New status (for update_item)"), mcp.Enum("pending", "in_progress", "completed", "blocked", "cancelled")),
			mcp.WithNumber("priority", mcp.Description("Priority 1-3 (1=highest)")),
			mcp.WithString("reasoning", mcp.Description("Why this approach")),
			mcp.WithString("add_blocker", mcp.Description("Add a blocker description (for update_item)")),
			mcp.WithString("remove_blocker", mcp.Description("Remove a blocker (exact match)")),
			mcp.WithString("add_note", mcp.Description("Add a note to this item")),
			mcp.WithString("updated_by", mcp.Required(), mcp.Description("Agent making this update")),
			mcp.WithArray("dependencies", mcp.Description("Item IDs this item depends on (for add_item)")),
			mcp.WithArray("acceptance", mcp.Description("Acceptance criteria (for add_item)")),
			mcp.WithArray("constraints", mcp.Description("Constraints (for add_item)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := req.GetArguments()
			action, _ := args["action"].(string)
			planID, _ := args["plan_id"].(string)
			updatedBy, _ := args["updated_by"].(string)

			if action == "" || updatedBy == "" {
				return nil, fmt.Errorf("action and updated_by are required")
			}

			now := time.Now()
			var res *mcp.CallToolResult
			err := svc.Run(func(state *domain.CollabState) error {
				extra := app.RegisteredAgentNames(state)
				if err := app.ValidateAgent(updatedBy, state, false, false, extra...); err != nil {
					return err
				}

				pID := planID
				if pID == "" {
					pID = state.ActivePlanID
				}
				if pID == "" {
					return fmt.Errorf("no plan specified and no active plan")
				}
				plan, exists := state.Plans[pID]
				if !exists {
					return fmt.Errorf("plan %q not found", pID)
				}
				var err2 error
				switch action {
				case "add_item":
					res, err2 = addPlanItem(plan, state, args, updatedBy, now, logger, extra)
				case "update_item":
					res, err2 = updatePlanItem(plan, state, args, updatedBy, now, logger, extra)
				default:
					return fmt.Errorf("unknown action: %s", action)
				}
				return err2
			})
			if err != nil {
				return nil, err
			}
			return res, nil
		},
	)
}

func addPlanItem(plan *domain.Plan, state *domain.CollabState, args map[string]any, updatedBy string, now time.Time, logger *log.Logger, extra []string) (*mcp.CallToolResult, error) {
	id, _ := args["id"].(string)
	title, _ := args["title"].(string)
	description, _ := args["description"].(string)
	owner, _ := args["owner"].(string)
	reasoning, _ := args["reasoning"].(string)

	priority := 2
	if v, ok := args["priority"].(float64); ok {
		priority = int(v)
	}

	var dependencies []string
	if deps, ok := args["dependencies"].([]interface{}); ok {
		for _, d := range deps {
			if s, ok := d.(string); ok {
				dependencies = append(dependencies, s)
			}
		}
	}
	var acceptance []string
	if acc, ok := args["acceptance"].([]interface{}); ok {
		for _, a := range acc {
			if s, ok := a.(string); ok {
				acceptance = append(acceptance, s)
			}
		}
	}
	var constraints []string
	if c, ok := args["constraints"].([]interface{}); ok {
		for _, x := range c {
			if s, ok := x.(string); ok {
				constraints = append(constraints, s)
			}
		}
	}

	if id == "" || title == "" {
		return nil, fmt.Errorf("id and title are required for add_item")
	}

	if owner != "" && owner != "unassigned" {
		if err := app.ValidateAgent(owner, state, false, false, extra...); err != nil {
			return nil, err
		}
	}

	for _, item := range plan.Items {
		if item.ID == id {
			return nil, fmt.Errorf("item %q already exists in plan", id)
		}
	}

	item := domain.PlanItem{
		ID:           id,
		Title:        title,
		Description:  description,
		Reasoning:    reasoning,
		Acceptance:   acceptance,
		Constraints:  constraints,
		Status:       "pending",
		Owner:        owner,
		Dependencies: dependencies,
		Blockers:     []string{},
		Notes:        []string{},
		Priority:     priority,
		UpdatedBy:    updatedBy,
		UpdatedAt:    now,
	}

	plan.Items = append(plan.Items, item)
	plan.UpdatedAt = now

	ownerStr := owner
	if ownerStr == "" {
		ownerStr = "unassigned"
	}
	logger.Printf("Plan item %q added to %q by %s", id, plan.ID, updatedBy)
	return mcp.NewToolResultText(fmt.Sprintf("Added item [%s] %s (owner: %s)", id, title, ownerStr)), nil
}

func updatePlanItem(plan *domain.Plan, state *domain.CollabState, args map[string]any, updatedBy string, now time.Time, logger *log.Logger, extra []string) (*mcp.CallToolResult, error) {
	itemID, _ := args["item_id"].(string)
	if itemID == "" {
		return nil, fmt.Errorf("item_id is required for update_item")
	}

	var item *domain.PlanItem
	for i := range plan.Items {
		if plan.Items[i].ID == itemID {
			item = &plan.Items[i]
			break
		}
	}
	if item == nil {
		return nil, fmt.Errorf("item %q not found in plan", itemID)
	}

	changes := []string{}

	if status, ok := args["status"].(string); ok && status != "" {
		item.Status = status
		changes = append(changes, fmt.Sprintf("status -> %s", status))
	}

	if owner, ok := args["owner"].(string); ok {
		if owner != "" && owner != "unassigned" {
			if err := app.ValidateAgent(owner, state, false, false, extra...); err != nil {
				return nil, err
			}
		}
		item.Owner = owner
		ownerStr := owner
		if ownerStr == "" {
			ownerStr = "unassigned"
		}
		changes = append(changes, fmt.Sprintf("owner -> %s", ownerStr))
	}

	if blocker, ok := args["add_blocker"].(string); ok && blocker != "" {
		item.Blockers = append(item.Blockers, blocker)
		changes = append(changes, "added blocker")
	}

	if blocker, ok := args["remove_blocker"].(string); ok && blocker != "" {
		for i, b := range item.Blockers {
			if b == blocker {
				item.Blockers = append(item.Blockers[:i], item.Blockers[i+1:]...)
				changes = append(changes, "removed blocker")
				break
			}
		}
	}

	if note, ok := args["add_note"].(string); ok && note != "" {
		noteWithAuthor := fmt.Sprintf("[%s] %s", updatedBy, note)
		item.Notes = append(item.Notes, noteWithAuthor)
		changes = append(changes, "added note")
	}

	if reasoning, ok := args["reasoning"].(string); ok {
		item.Reasoning = reasoning
		changes = append(changes, "set reasoning")
	}
	if acc, ok := args["acceptance"].([]interface{}); ok {
		item.Acceptance = nil
		for _, a := range acc {
			if s, ok := a.(string); ok {
				item.Acceptance = append(item.Acceptance, s)
			}
		}
		changes = append(changes, "set acceptance")
	}
	if c, ok := args["constraints"].([]interface{}); ok {
		item.Constraints = nil
		for _, x := range c {
			if s, ok := x.(string); ok {
				item.Constraints = append(item.Constraints, s)
			}
		}
		changes = append(changes, "set constraints")
	}

	item.UpdatedBy = updatedBy
	item.UpdatedAt = now
	plan.UpdatedAt = now

	logger.Printf("Plan item %q updated by %s: %v", itemID, updatedBy, changes)
	return mcp.NewToolResultText(fmt.Sprintf("Updated item [%s]: %v", itemID, changes)), nil
}
