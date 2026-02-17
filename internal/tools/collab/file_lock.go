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

func cleanupExpiredLocks(state *domain.CollabState) int {
	now := time.Now()
	removed := 0
	for path, lock := range state.FileLocks {
		if lock != nil && now.After(lock.ExpiresAt) {
			delete(state.FileLocks, path)
			removed++
		}
	}
	return removed
}

// registerLockFile registers the unified lock_file tool (lock, unlock, check, list via action param).
func registerLockFile(s *server.MCPServer, svc *app.CollabService, logger *log.Logger) {
	s.AddTool(
		mcp.NewTool("lock_file",
			mcp.WithDescription("Manage file locks to prevent simultaneous edits. Actions: lock (acquire lock), unlock (release lock), check (check if locked), list (list all locks). Locks auto-expire to prevent deadlocks."),
			mcp.WithString("action", mcp.Description("Action to perform (default: lock)"), mcp.Enum("lock", "unlock", "check", "list")),
			mcp.WithString("agent", mcp.Description("Your agent identifier (required for lock/unlock)")),
			mcp.WithString("path", mcp.Description("File path (required for lock/unlock/check)")),
			mcp.WithString("reason", mcp.Description("Why you're locking this file (required for lock)")),
			mcp.WithNumber("duration_minutes", mcp.Description("Lock duration in minutes (default: 30, max: 120)")),
			mcp.WithBoolean("force", mcp.Description("Force unlock even if locked by other agent (for unlock action)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := req.GetArguments()
			action := "lock"
			if a, ok := args["action"].(string); ok && a != "" {
				action = a
			}

			switch action {
			case "lock":
				return handleLock(svc, args, logger)
			case "unlock":
				return handleUnlock(svc, args, logger)
			case "check":
				return handleCheck(svc, args, logger)
			case "list":
				return handleList(svc, args, logger)
			default:
				return nil, fmt.Errorf("unknown action: %s", action)
			}
		},
	)
}

func handleLock(svc *app.CollabService, args map[string]any, logger *log.Logger) (*mcp.CallToolResult, error) {
	agent, _ := args["agent"].(string)
	path, _ := args["path"].(string)
	reason, _ := args["reason"].(string)

	duration := 30
	if d, ok := args["duration_minutes"].(float64); ok {
		duration = int(d)
		if duration > 120 {
			duration = 120
		}
		if duration < 1 {
			duration = 1
		}
	}

	if agent == "" || path == "" || reason == "" {
		return nil, fmt.Errorf("agent, path, and reason are required for lock action")
	}
	path, err := svc.Policy().ValidatePath(path)
	if err != nil {
		return nil, err
	}

	var lockResult *mcp.CallToolResult
	if runErr := svc.Run(func(state *domain.CollabState) error {
		extra := app.RegisteredAgentNames(state)
		if err := app.ValidateAgent(agent, state, false, false, extra...); err != nil {
			return err
		}

		cleanupExpiredLocks(state)

		if existing, exists := state.FileLocks[path]; exists && existing != nil {
			if existing.LockedBy != agent {
				return fmt.Errorf("file locked by %s until %s: %s",
					existing.LockedBy, existing.ExpiresAt.Format("15:04:05"), existing.Reason)
			}
			existing.ExpiresAt = time.Now().Add(time.Duration(duration) * time.Minute)
			existing.Reason = reason
			lockResult = mcp.NewToolResultText(fmt.Sprintf("Lock extended on %s until %s",
				path, existing.ExpiresAt.Format("15:04:05")))
			return nil
		}

		now := time.Now()
		state.FileLocks[path] = &domain.FileLock{
			Path:      path,
			LockedBy:  agent,
			Reason:    reason,
			LockedAt:  now,
			ExpiresAt: now.Add(time.Duration(duration) * time.Minute),
		}
		lockResult = mcp.NewToolResultText(fmt.Sprintf("Locked %s for %d minutes. Expires at %s",
			path, duration, state.FileLocks[path].ExpiresAt.Format("15:04:05")))
		return nil
	}); runErr != nil {
		return nil, runErr
	}
	logger.Printf("lock_file: %s locked %s for %d minutes", agent, path, duration)
	return lockResult, nil
}

func handleUnlock(svc *app.CollabService, args map[string]any, logger *log.Logger) (*mcp.CallToolResult, error) {
	agent, _ := args["agent"].(string)
	path, _ := args["path"].(string)
	force, _ := args["force"].(bool)

	if agent == "" || path == "" {
		return nil, fmt.Errorf("agent and path are required for unlock action")
	}
	path, err := svc.Policy().ValidatePath(path)
	if err != nil {
		return nil, err
	}

	var lockResult *mcp.CallToolResult
	err = svc.Run(func(state *domain.CollabState) error {
		extra := app.RegisteredAgentNames(state)
		if err := app.ValidateAgent(agent, state, false, false, extra...); err != nil {
			return err
		}

		cleanupExpiredLocks(state)
		lock, exists := state.FileLocks[path]
		if !exists || lock == nil {
			lockResult = mcp.NewToolResultText(fmt.Sprintf("%s is not locked", path))
			return nil
		}
		if lock.LockedBy != agent && !force {
			return fmt.Errorf("cannot unlock: file locked by %s (use force=true to override)", lock.LockedBy)
		}
		wasBy := lock.LockedBy
		delete(state.FileLocks, path)
		if wasBy != agent {
			lockResult = mcp.NewToolResultText(fmt.Sprintf("Force-unlocked %s (was locked by %s)", path, wasBy))
		} else {
			lockResult = mcp.NewToolResultText(fmt.Sprintf("Unlocked %s", path))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	logger.Printf("lock_file unlock: %s unlocked %s", agent, path)
	return lockResult, nil
}

func handleCheck(svc *app.CollabService, args map[string]any, logger *log.Logger) (*mcp.CallToolResult, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return nil, fmt.Errorf("path is required for check action")
	}
	path, err := svc.Policy().ValidatePath(path)
	if err != nil {
		return nil, err
	}

	var lockResult *mcp.CallToolResult
	err = svc.Run(func(state *domain.CollabState) error {
		cleanupExpiredLocks(state)
		lock, exists := state.FileLocks[path]
		if !exists || lock == nil {
			lockResult = mcp.NewToolResultText(fmt.Sprintf(`{"locked":false,"path":"%s"}`, escapeJSON(path)))
			return nil
		}
		lockResult = mcp.NewToolResultText(fmt.Sprintf(`{"locked":true,"path":"%s","locked_by":"%s","reason":"%s","expires_at":"%s"}`,
			escapeJSON(path), escapeJSON(lock.LockedBy), escapeJSON(lock.Reason), lock.ExpiresAt.Format(time.RFC3339)))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return lockResult, nil
}

func handleList(svc *app.CollabService, args map[string]any, logger *log.Logger) (*mcp.CallToolResult, error) {
	filterAgent, _ := args["agent"].(string)

	var result string
	err := svc.Run(func(state *domain.CollabState) error {
		cleanupExpiredLocks(state)
		if len(state.FileLocks) == 0 {
			result = "No active file locks"
			return nil
		}
		for p, lock := range state.FileLocks {
			if lock == nil {
				continue
			}
			if filterAgent != "" && lock.LockedBy != filterAgent {
				continue
			}
			timeLeft := time.Until(lock.ExpiresAt).Round(time.Minute)
			result += fmt.Sprintf("- **%s** (locked by %s, %v remaining)\n  Reason: %s\n",
				p, lock.LockedBy, timeLeft, lock.Reason)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if result == "" {
		return mcp.NewToolResultText("No locks for specified agent"), nil
	}
	logger.Printf("lock_file list")
	return mcp.NewToolResultText(result), nil
}
