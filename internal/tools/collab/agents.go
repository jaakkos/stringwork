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

// registerRegisterAgent registers the register_agent tool.
func registerRegisterAgent(s *server.MCPServer, svc *app.CollabService, logger *log.Logger) {
	s.AddTool(
		mcp.NewTool("register_agent",
			mcp.WithDescription("Register an agent with the pair programming system. Allows custom agents to join the collaboration. Built-in agents (cursor, claude-code, codex) are always available."),
			mcp.WithString("name", mcp.Required(), mcp.Description("Unique agent identifier (e.g., 'cursor', 'claude-code', 'my-agent')")),
			mcp.WithString("display_name", mcp.Description("Human-readable name for the agent (optional)")),
			mcp.WithString("workspace", mcp.Description("Workspace root path this agent is working in (optional)")),
			mcp.WithString("project", mcp.Description("Project name or identifier (optional)")),
			mcp.WithArray("capabilities", mcp.Description("Agent capabilities (e.g., 'code-edit', 'search', 'terminal')")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := req.GetArguments()
			name, _ := args["name"].(string)
			displayName, _ := args["display_name"].(string)
			workspace, _ := args["workspace"].(string)
			project, _ := args["project"].(string)

			if name == "" {
				return nil, fmt.Errorf("agent name is required")
			}
			// Block reserved names that have special meaning in the system.
			switch name {
			case "all", "any", "system":
				return nil, fmt.Errorf("agent name %q is reserved", name)
			}

			var capabilities []string
			if caps, ok := args["capabilities"].([]any); ok {
				for _, c := range caps {
					if s, ok := c.(string); ok {
						capabilities = append(capabilities, s)
					}
				}
			}

			var isNew bool
			if err := svc.Run(func(state *domain.CollabState) error {
				now := time.Now()

				existing, exists := state.RegisteredAgents[name]
				if exists {
					existing.LastSeen = now
					if displayName != "" {
						existing.DisplayName = displayName
					}
					if len(capabilities) > 0 {
						existing.Capabilities = capabilities
					}
					if workspace != "" {
						existing.Workspace = workspace
					}
					if project != "" {
						existing.Project = project
					}
					isNew = false
				} else {
					state.RegisteredAgents[name] = &domain.RegisteredAgent{
						Name:         name,
						DisplayName:  displayName,
						Capabilities: capabilities,
						Workspace:    workspace,
						Project:      project,
						RegisteredAt: now,
						LastSeen:     now,
					}
					isNew = true
				}
				return nil
			}); err != nil {
				return nil, err
			}

			if isNew {
				logger.Printf("Agent registered: %s", name)
				return mcp.NewToolResultText(fmt.Sprintf("Agent '%s' registered successfully", name)), nil
			}
			logger.Printf("Agent updated: %s", name)
			return mcp.NewToolResultText(fmt.Sprintf("Agent '%s' registration updated", name)), nil
		},
	)
}

// registerListAgents registers the list_agents tool.
func registerListAgents(s *server.MCPServer, svc *app.CollabService, logger *log.Logger) {
	s.AddTool(
		mcp.NewTool("list_agents",
			mcp.WithDescription("List all registered agents and their capabilities. Useful for discovering available pair programming partners."),
			mcp.WithBoolean("include_builtin", mcp.Description("Include built-in agents (cursor, claude-code, codex) in the list (default: true)")),
			mcp.WithBoolean("active_only", mcp.Description("Only show agents seen in the last hour (default: false)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := req.GetArguments()
			includeBuiltin := true
			if v, ok := args["include_builtin"].(bool); ok {
				includeBuiltin = v
			}
			activeOnly := false
			if v, ok := args["active_only"].(bool); ok {
				activeOnly = v
			}

			var result string
			oneHourAgo := time.Now().Add(-time.Hour)

			if err := svc.Query(func(state *domain.CollabState) error {
				result = "=== Available Agents ===\n\n"

				if includeBuiltin {
					result += "Built-in Agents:\n"
					for _, name := range app.GetBuiltinAgents(state) {
						status := "available"
						if presence, ok := state.Presence[name]; ok {
							status = presence.Status
							if presence.Note != "" {
								status += " (" + presence.Note + ")"
							}
						}
						result += fmt.Sprintf("  - %s [%s]\n", name, status)
					}
					result += "\n"
				}

				if len(state.RegisteredAgents) > 0 {
					result += "Registered Agents:\n"
					for _, agent := range state.RegisteredAgents {
						if activeOnly && agent.LastSeen.Before(oneHourAgo) {
							continue
						}
						line := fmt.Sprintf("  - %s", agent.Name)
						if agent.DisplayName != "" {
							line += fmt.Sprintf(" (%s)", agent.DisplayName)
						}
						if len(agent.Capabilities) > 0 {
							line += fmt.Sprintf("\n      Capabilities: %s", app.JoinStrings(agent.Capabilities, ", "))
						}
						if agent.Project != "" {
							line += fmt.Sprintf("\n      Project: %s", agent.Project)
						}
						if agent.Workspace != "" {
							line += fmt.Sprintf("\n      Workspace: %s", agent.Workspace)
						}
						line += fmt.Sprintf("\n      Last seen: %s", agent.LastSeen.Format("2006-01-02 15:04:05"))
						result += line + "\n"
					}
				} else {
					result += "Registered Agents: (none)\n"
				}

				return nil
			}); err != nil {
				return nil, err
			}

			logger.Println("Listed agents")
			return mcp.NewToolResultText(result), nil
		},
	)
}
