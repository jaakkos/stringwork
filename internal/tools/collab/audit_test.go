package collab

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/jaakkos/stringwork/internal/app"
	"github.com/jaakkos/stringwork/internal/domain"
)

// mockAuditWriter records calls for test assertions.
type mockAuditWriter struct {
	mu      sync.Mutex
	entries []domain.AuditEntry
}

func (m *mockAuditWriter) WriteAudit(entry domain.AuditEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = append(m.entries, entry)
	return nil
}

func (m *mockAuditWriter) PruneAudit(olderThan time.Time) (int64, error) {
	return 0, nil
}

func (m *mockAuditWriter) Entries() []domain.AuditEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]domain.AuditEntry, len(m.entries))
	copy(cp, m.entries)
	return cp
}

func TestAuditMiddleware_NilWriter(t *testing.T) {
	called := false
	inner := func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		called = true
		return mcp.NewToolResultText("ok"), nil
	}

	mw := AuditMiddleware(nil, app.NewSessionRegistry(), 0)
	handler := mw(inner)

	_, err := handler(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("inner handler should be called even with nil writer")
	}
}

func TestAuditMiddleware_RecordsToolCall(t *testing.T) {
	writer := &mockAuditWriter{}
	registry := app.NewSessionRegistry()

	inner := func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("result"), nil
	}

	mw := AuditMiddleware(writer, registry, 0)
	handler := mw(inner)

	req := mcp.CallToolRequest{}
	req.Params.Name = "test_tool"
	req.Params.Arguments = map[string]any{"key": "value"}

	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	entries := writer.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(entries))
	}

	entry := entries[0]
	if entry.ToolName != "test_tool" {
		t.Errorf("ToolName = %q, want test_tool", entry.ToolName)
	}
	if !strings.Contains(entry.ArgsSummary, "key") {
		t.Errorf("ArgsSummary should contain 'key', got %q", entry.ArgsSummary)
	}
	if entry.DurationMs < 0 {
		t.Error("DurationMs should be non-negative")
	}
	if entry.Error != "" {
		t.Errorf("Error should be empty for successful call, got %q", entry.Error)
	}
}

func TestAuditMiddleware_RecordsError(t *testing.T) {
	writer := &mockAuditWriter{}
	registry := app.NewSessionRegistry()

	inner := func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result := &mcp.CallToolResult{IsError: true}
		result.Content = []mcp.Content{mcp.NewTextContent("something went wrong")}
		return result, nil
	}

	mw := AuditMiddleware(writer, registry, 0)
	handler := mw(inner)

	req := mcp.CallToolRequest{}
	req.Params.Name = "failing_tool"

	_, _ = handler(context.Background(), req)

	entries := writer.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Error != "something went wrong" {
		t.Errorf("Error = %q, want 'something went wrong'", entries[0].Error)
	}
}

func TestAuditMiddleware_TruncatesLongArgs(t *testing.T) {
	writer := &mockAuditWriter{}
	registry := app.NewSessionRegistry()

	inner := func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	}

	maxLen := 50
	mw := AuditMiddleware(writer, registry, maxLen)
	handler := mw(inner)

	longValue := strings.Repeat("x", 200)
	req := mcp.CallToolRequest{}
	req.Params.Name = "tool"
	req.Params.Arguments = map[string]any{"data": longValue}

	_, _ = handler(context.Background(), req)

	entries := writer.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if len(entries[0].ArgsSummary) > maxLen {
		t.Errorf("ArgsSummary length = %d, want <= %d", len(entries[0].ArgsSummary), maxLen)
	}
	if !strings.HasSuffix(entries[0].ArgsSummary, "...") {
		t.Errorf("truncated ArgsSummary should end with '...', got %q", entries[0].ArgsSummary)
	}
}

func TestAuditMiddleware_TruncatesSmallMaxLen(t *testing.T) {
	tests := []struct {
		name   string
		maxLen int
	}{
		{"maxLen=1", 1},
		{"maxLen=2", 2},
		{"maxLen=3", 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writer := &mockAuditWriter{}
			registry := app.NewSessionRegistry()

			inner := func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return mcp.NewToolResultText("ok"), nil
			}

			mw := AuditMiddleware(writer, registry, tt.maxLen)
			handler := mw(inner)

			req := mcp.CallToolRequest{}
			req.Params.Name = "tool"
			req.Params.Arguments = map[string]any{"data": strings.Repeat("x", 100)}

			// Must not panic
			_, err := handler(context.Background(), req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			entries := writer.Entries()
			if len(entries) != 1 {
				t.Fatalf("expected 1 entry, got %d", len(entries))
			}
			if len(entries[0].ArgsSummary) > tt.maxLen {
				t.Errorf("ArgsSummary length = %d, want <= %d", len(entries[0].ArgsSummary), tt.maxLen)
			}
		})
	}
}

func TestAuditMiddleware_RedactsSensitiveKeys(t *testing.T) {
	writer := &mockAuditWriter{}
	registry := app.NewSessionRegistry()

	inner := func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	}

	mw := AuditMiddleware(writer, registry, 0)
	handler := mw(inner)

	req := mcp.CallToolRequest{}
	req.Params.Name = "tool"
	req.Params.Arguments = map[string]any{
		"api_key":  "super-secret-key",
		"password": "my-password",
		"token":    "bearer-token",
		"secret":   "shh",
		"name":     "visible",
	}

	_, _ = handler(context.Background(), req)

	entries := writer.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	summary := entries[0].ArgsSummary
	if strings.Contains(summary, "super-secret-key") {
		t.Error("api_key should be redacted")
	}
	if strings.Contains(summary, "my-password") {
		t.Error("password should be redacted")
	}
	if strings.Contains(summary, "bearer-token") {
		t.Error("token should be redacted")
	}
	if strings.Contains(summary, "shh") {
		t.Error("secret should be redacted")
	}
	if !strings.Contains(summary, "visible") {
		t.Error("non-sensitive key 'name' should be visible")
	}
	if !strings.Contains(summary, "[REDACTED]") {
		t.Error("redacted keys should show [REDACTED]")
	}
}

func TestAuditMiddleware_DefaultArgsMaxLen(t *testing.T) {
	mw := AuditMiddleware(&mockAuditWriter{}, app.NewSessionRegistry(), 0)
	if mw == nil {
		t.Fatal("middleware should not be nil")
	}

	mw2 := AuditMiddleware(&mockAuditWriter{}, app.NewSessionRegistry(), -1)
	if mw2 == nil {
		t.Fatal("middleware with negative maxLen should not be nil")
	}
}

func TestAuditMiddleware_IdentityWithNilArgs(t *testing.T) {
	writer := &mockAuditWriter{}
	registry := app.NewSessionRegistry()

	inner := server.ToolHandlerFunc(func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})

	mw := AuditMiddleware(writer, registry, 0)
	handler := mw(inner)

	req := mcp.CallToolRequest{}
	req.Params.Name = "no_args_tool"
	// Arguments is nil

	_, _ = handler(context.Background(), req)

	entries := writer.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].ArgsSummary != "" {
		t.Errorf("ArgsSummary should be empty for nil args, got %q", entries[0].ArgsSummary)
	}
}
