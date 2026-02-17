package collab

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/jaakkos/stringwork/internal/app"
)

// testServer creates a MCPServer with all tools registered for testing.
func testServer(svc *app.CollabService, logger *log.Logger) *server.MCPServer {
	s := server.NewMCPServer("test", "1.0.0")
	registry := app.NewSessionRegistry()
	Register(s, svc, logger, registry, nil)
	return s
}

// callTool calls a registered tool via the MCPServer's HandleMessage.
// Returns the parsed CallToolResult or an error.
func callTool(t *testing.T, s *server.MCPServer, name string, args map[string]any) (*mcp.CallToolResult, error) {
	t.Helper()

	reqJSON, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      name,
			"arguments": args,
		},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	respJSON := s.HandleMessage(context.Background(), reqJSON)

	respBytes, marshalErr := json.Marshal(respJSON)
	if marshalErr != nil {
		t.Fatalf("marshal response: %v", marshalErr)
	}

	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("RPC error %d: %s", resp.Error.Code, resp.Error.Message)
	}

	var result mcp.CallToolResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	return &result, nil
}

// resultText extracts the first text content from a CallToolResult.
func resultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if result == nil {
		t.Fatal("result is nil")
	}
	for _, c := range result.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			return tc.Text
		}
	}
	t.Fatal("no text content in result")
	return ""
}
