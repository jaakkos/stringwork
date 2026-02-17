package collab

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/jaakkos/stringwork/internal/knowledge"
)

// registerQueryKnowledge registers the query_knowledge MCP tool.
func registerQueryKnowledge(s *server.MCPServer, store *knowledge.KnowledgeStore, logger *log.Logger) {
	s.AddTool(
		mcp.NewTool("query_knowledge",
			mcp.WithDescription(
				"Search the project knowledge base. Indexes markdown docs, Go source code, "+
					"session notes, and completed task summaries. Use this to find architecture decisions, "+
					"code patterns, API documentation, or any project-specific information. "+
					"Returns ranked snippets with file paths."),
			mcp.WithString("query", mcp.Required(), mcp.Description(
				"Natural language search query. Examples: 'how does task assignment work', "+
					"'authentication middleware', 'worker spawn lifecycle'")),
			mcp.WithString("category", mcp.Description(
				"Optional filter by category: markdown, go_source, session_note, task_summary, config. "+
					"Omit to search all categories."),
				mcp.Enum("markdown", "go_source", "session_note", "task_summary", "config")),
			mcp.WithNumber("limit", mcp.Description("Maximum number of results to return (default: 10, max: 50)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := req.GetArguments()

			query, ok := args["query"].(string)
			if !ok || query == "" {
				return nil, fmt.Errorf("query parameter is required")
			}

			category, _ := args["category"].(string)

			limit := 10
			if l, ok := args["limit"].(float64); ok {
				limit = int(l)
				if limit < 1 {
					limit = 1
				}
				if limit > 50 {
					limit = 50
				}
			}

			results, err := store.Query(query, category, limit)
			if err != nil {
				logger.Printf("query_knowledge error: %v", err)
				return nil, fmt.Errorf("knowledge query failed: %w", err)
			}

			if len(results) == 0 {
				return mcp.NewToolResultText("No results found for: " + query), nil
			}

			data, err := json.MarshalIndent(results, "", "  ")
			if err != nil {
				return nil, fmt.Errorf("marshal results: %w", err)
			}

			logger.Printf("query_knowledge: %q returned %d results", query, len(results))
			return mcp.NewToolResultText(string(data)), nil
		},
	)
}
