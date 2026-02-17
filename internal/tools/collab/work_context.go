package collab

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/jaakkos/stringwork/internal/app"
	"github.com/jaakkos/stringwork/internal/domain"
)

// registerGetWorkContext registers the get_work_context tool.
func registerGetWorkContext(s *server.MCPServer, svc *app.CollabService, logger *log.Logger) {
	s.AddTool(
		mcp.NewTool("get_work_context",
			mcp.WithDescription("Get the work context for a task (relevant files, background, constraints, shared notes). Use this when working on an assigned task to stay in scope."),
			mcp.WithNumber("task_id", mcp.Required(), mcp.Description("Task ID to get context for")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := req.GetArguments()
			tid, err := requireFloat64(args, "task_id")
			if err != nil {
				return nil, err
			}
			taskID := int(tid)
			var result string
			err = svc.Query(func(state *domain.CollabState) error {
				var wc *domain.WorkContext
				for _, t := range state.Tasks {
					if t.ID == taskID && t.ContextID != "" {
						wc = state.WorkContexts[t.ContextID]
						break
					}
				}
				if wc == nil {
					result = fmt.Sprintf("No work context for task #%d", taskID)
					return nil
				}
				out := map[string]interface{}{
					"task_id":        wc.TaskID,
					"relevant_files": wc.RelevantFiles,
					"background":     wc.Background,
					"constraints":    wc.Constraints,
					"shared_notes":   wc.SharedNotes,
					"parent_ctx_id":  wc.ParentCtxID,
				}
				bytes, _ := json.MarshalIndent(out, "", "  ")
				result = string(bytes)
				return nil
			})
			if err != nil {
				return nil, err
			}
			logger.Printf("get_work_context task_id=%d", taskID)
			return mcp.NewToolResultText(result), nil
		},
	)
}

// registerUpdateWorkContext registers the update_work_context tool.
func registerUpdateWorkContext(s *server.MCPServer, svc *app.CollabService, logger *log.Logger) {
	s.AddTool(
		mcp.NewTool("update_work_context",
			mcp.WithDescription("Add or update shared notes in the work context for a task. Use this to record findings, decisions, or constraints for other workers."),
			mcp.WithNumber("task_id", mcp.Required(), mcp.Description("Task ID whose context to update")),
			mcp.WithString("key", mcp.Required(), mcp.Description("Note key (e.g. 'findings', 'decisions')")),
			mcp.WithString("value", mcp.Required(), mcp.Description("Note content")),
			mcp.WithString("author", mcp.Description("Your agent ID (for attribution)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := req.GetArguments()
			tid, err := requireFloat64(args, "task_id")
			if err != nil {
				return nil, err
			}
			taskID := int(tid)
			key, _ := args["key"].(string)
			value, _ := args["value"].(string)
			author, _ := args["author"].(string)
			if key == "" || value == "" {
				return nil, fmt.Errorf("key and value are required")
			}
			if author != "" {
				key = author + ":" + key
			}
			err = svc.Run(func(state *domain.CollabState) error {
				var ctxID string
				for _, t := range state.Tasks {
					if t.ID == taskID {
						ctxID = t.ContextID
						break
					}
				}
				if ctxID == "" {
					return fmt.Errorf("task #%d has no work context", taskID)
				}
				wc := state.WorkContexts[ctxID]
				if wc == nil {
					return fmt.Errorf("work context %s not found", ctxID)
				}
				if wc.SharedNotes == nil {
					wc.SharedNotes = make(map[string]string)
				}
				wc.SharedNotes[key] = value
				return nil
			})
			if err != nil {
				return nil, err
			}
			logger.Printf("update_work_context task_id=%d key=%s", taskID, key)
			return mcp.NewToolResultText("OK"), nil
		},
	)
}

// ensureWorkContextForTask creates a WorkContext for the task if context fields are provided and links it.
// Call from create_task when relevant_files, background, or constraints are provided.
func ensureWorkContextForTask(state *domain.CollabState, taskID int, relevantFiles []string, background string, constraints []string, parentCtxID string) (contextID string) {
	if len(relevantFiles) == 0 && background == "" && len(constraints) == 0 {
		return ""
	}
	contextID = fmt.Sprintf("ctx-%d-%d", taskID, time.Now().UnixNano())
	wc := &domain.WorkContext{
		ID:            contextID,
		TaskID:        taskID,
		RelevantFiles: relevantFiles,
		Background:    background,
		Constraints:   constraints,
		SharedNotes:   make(map[string]string),
		ParentCtxID:   parentCtxID,
	}
	state.WorkContexts[contextID] = wc
	for i := range state.Tasks {
		if state.Tasks[i].ID == taskID {
			state.Tasks[i].ContextID = contextID
			break
		}
	}
	return contextID
}

const defaultTaskContextLockMinutes = 60

// autoLockTaskContextFiles locks RelevantFiles from the task's work context for the given agent.
// validatePath is typically svc.Policy().ValidatePath. Skips paths that fail validation.
func autoLockTaskContextFiles(state *domain.CollabState, contextID string, agent string, validatePath func(string) (string, error)) {
	if contextID == "" {
		return
	}
	wc := state.WorkContexts[contextID]
	if wc == nil || len(wc.RelevantFiles) == 0 {
		return
	}
	now := time.Now()
	expires := now.Add(defaultTaskContextLockMinutes * time.Minute)
	for _, p := range wc.RelevantFiles {
		path, err := validatePath(p)
		if err != nil {
			continue
		}
		if existing, exists := state.FileLocks[path]; exists && existing != nil && existing.LockedBy != agent {
			continue
		}
		state.FileLocks[path] = &domain.FileLock{
			Path:      path,
			LockedBy:  agent,
			Reason:    "task context scope",
			LockedAt:  now,
			ExpiresAt: expires,
		}
	}
}
