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

// registerGetSessionContext registers the get_session_context tool.
func registerGetSessionContext(s *server.MCPServer, svc *app.CollabService, logger *log.Logger, registry *app.SessionRegistry) {
	s.AddTool(
		mcp.NewTool("get_session_context",
			mcp.WithDescription("Get the current pair programming session context including unread messages, pending tasks, presence, and session notes. Call this at the start of your turn."),
			mcp.WithString("for", mcp.Required(), mcp.Description("Get context for this agent (e.g., 'cursor', 'claude-code')")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := req.GetArguments()
			agent, _ := args["for"].(string)
			if agent == "" {
				return nil, fmt.Errorf("'for' is required")
			}

			// Associate this session with the agent.
			if session := server.ClientSessionFromContext(ctx); session != nil {
				registry.SetAgent(session.SessionID(), agent)
			}

			// Detect project info OUTSIDE the mutex — git commands can be slow
			// and we don't want to block all other state operations.
			workspacePath := svc.Policy().WorkspaceRoot()
			projectInfo := app.DetectProjectInfo(workspacePath)

			var result string
			if err := svc.Run(func(state *domain.CollabState) error {
				extra := app.RegisteredAgentNames(state)
				if err := app.ValidateAgent(agent, state, false, false, extra...); err != nil {
					return err
				}

				// Store project info (computed outside the lock).
				state.ProjectInfo = projectInfo

				now := time.Now()

				// Update agent heartbeat so the watchdog knows this agent is alive.
				touchAgentHeartbeat(state, agent, now)

				ttl := time.Duration(svc.Policy().PresenceTTLSeconds()) * time.Second

				var buf strings.Builder
				fmt.Fprintf(&buf, "=== Pair Programming Session Context for %s ===\n\n", agent)
				buf.WriteString("Pair Status:\n")
				for _, p := range state.Presence {
					if p == nil {
						continue
					}
					isStale := now.Sub(p.LastSeen) > ttl
					statusStr := p.Status
					if isStale {
						statusStr += " (offline)"
					}
					fmt.Fprintf(&buf, "  %s: %s", p.Agent, statusStr)
					if p.CurrentTaskID > 0 {
						fmt.Fprintf(&buf, " (Task #%d)", p.CurrentTaskID)
					}
					if p.Workspace != "" {
						fmt.Fprintf(&buf, " [%s]", p.Workspace)
					}
					buf.WriteByte('\n')
				}
				if len(state.Presence) == 0 {
					buf.WriteString("  No presence information\n")
				}
				buf.WriteByte('\n')

				unreadCount := 0
				var unreadBuf strings.Builder
				for _, msg := range state.Messages {
					if (msg.To == agent || msg.To == "all") && !msg.Read {
						unreadCount++
						fmt.Fprintf(&unreadBuf, "  From %s: %s\n", msg.From, app.Truncate(msg.Content, 100))
					}
				}
				fmt.Fprintf(&buf, "Unread Messages: %d\n", unreadCount)
				if unreadBuf.Len() > 0 {
					buf.WriteString(unreadBuf.String())
				}
				buf.WriteByte('\n')

				pendingCount := 0
				inProgressCount := 0
				var taskBuf strings.Builder
				for _, task := range state.Tasks {
					if task.AssignedTo == agent || task.AssignedTo == "any" {
						switch task.Status {
						case "pending":
							pendingCount++
							fmt.Fprintf(&taskBuf, "  [pending] #%d: %s\n", task.ID, task.Title)
						case "in_progress":
							inProgressCount++
							fmt.Fprintf(&taskBuf, "  [in_progress] #%d: %s\n", task.ID, task.Title)
						}
					}
				}
				fmt.Fprintf(&buf, "Your Tasks: %d pending, %d in progress\n", pendingCount, inProgressCount)
				if taskBuf.Len() > 0 {
					buf.WriteString(taskBuf.String())
				}
				buf.WriteByte('\n')

				if len(state.SessionNotes) > 0 {
					buf.WriteString("Recent Session Notes:\n")
					start := 0
					if len(state.SessionNotes) > 5 {
						start = len(state.SessionNotes) - 5
					}
					for _, note := range state.SessionNotes[start:] {
						fmt.Fprintf(&buf, "  [%s] %s: %s\n", note.Category, note.Author, app.Truncate(note.Content, 60))
					}
					buf.WriteByte('\n')
				}

				buf.WriteString("Project:\n")
				fmt.Fprintf(&buf, "  Name: %s\n", projectInfo.Name)
				fmt.Fprintf(&buf, "  Path: %s\n", projectInfo.Path)
				if projectInfo.IsGitRepo {
					fmt.Fprintf(&buf, "  Git Branch: %s\n", projectInfo.GitBranch)
					if projectInfo.GitRemote != "" {
						fmt.Fprintf(&buf, "  Git Remote: %s\n", projectInfo.GitRemote)
					}
				} else {
					buf.WriteString("  Git: not a git repository\n")
				}

				if dashURL := registry.DashboardURL(); dashURL != "" {
					buf.WriteByte('\n')
					fmt.Fprintf(&buf, "Dashboard: %s\n", dashURL)
				}

				result = buf.String()
				return nil
			}); err != nil {
				return nil, err
			}

			logger.Printf("Got session context for %s", agent)
			return mcp.NewToolResultText(result), nil
		},
	)
}

// registerSetPresence registers the set_presence tool.
func registerSetPresence(s *server.MCPServer, svc *app.CollabService, logger *log.Logger, registry *app.SessionRegistry) {
	s.AddTool(
		mcp.NewTool("set_presence",
			mcp.WithDescription("Update your presence status. Call this when you start/stop working, change tasks, or go away."),
			mcp.WithString("agent", mcp.Required(), mcp.Description("Agent identifier (e.g., 'cursor', 'claude-code')")),
			mcp.WithString("status", mcp.Required(), mcp.Description("Current status"), mcp.Enum("idle", "working", "reviewing", "away")),
			mcp.WithNumber("current_task_id", mcp.Description("Task ID currently being worked on (optional)")),
			mcp.WithString("note", mcp.Description("Short status note (optional)")),
			mcp.WithString("workspace", mcp.Description("Workspace root path this agent is working in (optional). Dynamically updates the server's workspace root so auto-spawned agents (e.g. claude-code) use this directory, and file path validation follows the new project. Set this when switching to a different project.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := req.GetArguments()
			agent, _ := args["agent"].(string)
			status, _ := args["status"].(string)

			if agent == "" || status == "" {
				return nil, fmt.Errorf("agent and status are required")
			}

			// Associate this session with the agent.
			if session := server.ClientSessionFromContext(ctx); session != nil {
				registry.SetAgent(session.SessionID(), agent)
			}

			var workspace string
			var workspaceChanged bool
			if err := svc.Run(func(state *domain.CollabState) error {
				extra := app.RegisteredAgentNames(state)
				if err := app.ValidateAgent(agent, state, false, false, extra...); err != nil {
					return err
				}

				now := time.Now()
				presence := &domain.Presence{
					Agent:    agent,
					Status:   status,
					LastSeen: now,
				}
				if v, ok := args["current_task_id"].(float64); ok {
					presence.CurrentTaskID = int(v)
				}
				if v, ok := args["note"].(string); ok {
					presence.Note = v
				}
				if v, ok := args["workspace"].(string); ok && v != "" {
					presence.Workspace = v
					old := ""
					if existing, ok := state.Presence[agent]; ok && existing != nil {
						old = existing.Workspace
					}
					workspaceChanged = (v != old)
				} else if existing, ok := state.Presence[agent]; ok && existing != nil {
					presence.Workspace = existing.Workspace
				}
				workspace = presence.Workspace
				state.Presence[agent] = presence

				// Update agent heartbeat so the watchdog knows this agent is alive.
				touchAgentHeartbeat(state, agent, now)
				return nil
			}); err != nil {
				return nil, err
			}

			if workspaceChanged && workspace != "" {
				svc.Policy().SetWorkspaceRoot(workspace)
				logger.Printf("Workspace root updated to %s (set by %s)", workspace, agent)
			}

			msg := fmt.Sprintf("Presence updated: %s is now %s", agent, status)
			if workspace != "" {
				msg += fmt.Sprintf(" (workspace: %s)", workspace)
			}
			logger.Printf("Presence updated for %s: %s workspace=%s", agent, status, workspace)
			return mcp.NewToolResultText(msg), nil
		},
	)
}

// touchAgentHeartbeat updates the AgentInstance.LastHeartbeat for an agent.
// This is called from set_presence and get_session_context so that any agent
// interaction counts as proof of liveness for the watchdog, not just the
// explicit "heartbeat" tool.
func touchAgentHeartbeat(state *domain.CollabState, agent string, now time.Time) {
	// Direct instance lookup
	if inst, ok := state.AgentInstances[agent]; ok && inst != nil {
		inst.LastHeartbeat = now
		if inst.Status == "offline" {
			inst.Status = "idle"
		}
		return
	}
	// Fallback: match by AgentType (e.g. "claude-code" matches instance "claude-code-1")
	for _, inst := range state.AgentInstances {
		if inst != nil && inst.AgentType == agent {
			inst.LastHeartbeat = now
			if inst.Status == "offline" {
				inst.Status = "idle"
			}
			// Don't break: update all instances of this type that might be tracked
		}
	}
}

// registerAppendSessionNote registers the append_session_note tool.
func registerAppendSessionNote(s *server.MCPServer, svc *app.CollabService, logger *log.Logger) {
	s.AddTool(
		mcp.NewTool("append_session_note",
			mcp.WithDescription("Add a shared note or decision to the session context. Use this to record important decisions, blockers, or context for your pair."),
			mcp.WithString("author", mcp.Required(), mcp.Description("Who is adding this note (e.g., 'cursor', 'claude-code')")),
			mcp.WithString("content", mcp.Required(), mcp.Description("Note content")),
			mcp.WithString("category", mcp.Description("Category of the note (default: 'note')"), mcp.Enum("decision", "note", "question", "blocker")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := req.GetArguments()
			author, _ := args["author"].(string)
			content, _ := args["content"].(string)
			category := "note"
			if v, ok := args["category"].(string); ok && v != "" {
				category = v
			}

			if author == "" || content == "" {
				return nil, fmt.Errorf("author and content are required")
			}

			var noteID int
			if err := svc.Run(func(state *domain.CollabState) error {
				extra := app.RegisteredAgentNames(state)
				if err := app.ValidateAgent(author, state, false, false, extra...); err != nil {
					return err
				}

				note := domain.SessionNote{
					ID:        state.NextNoteID,
					Author:    author,
					Content:   content,
					Category:  category,
					Timestamp: time.Now(),
				}
				state.SessionNotes = append(state.SessionNotes, note)
				noteID = note.ID
				state.NextNoteID++
				return nil
			}); err != nil {
				return nil, err
			}

			logger.Printf("Session note #%d added by %s", noteID, author)
			return mcp.NewToolResultText(fmt.Sprintf("Note #%d added [%s]: %s", noteID, category, app.Truncate(content, 50))), nil
		},
	)
}
