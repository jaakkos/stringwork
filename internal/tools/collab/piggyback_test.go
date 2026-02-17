package collab

import (
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/jaakkos/stringwork/internal/app"
	"github.com/jaakkos/stringwork/internal/domain"
)

func newPiggybackTestService() (*app.CollabService, *mockRepository) {
	repo := newMockRepository()
	pol := newMockPolicy()
	logger := log.New(io.Discard, "", 0)
	svc := app.NewCollabService(repo, pol, logger)
	return svc, repo
}

func TestBuildBanner_NoAgent(t *testing.T) {
	svc, _ := newPiggybackTestService()
	banner := buildBanner(svc, "")
	if banner != "" {
		t.Errorf("expected empty banner when no agent, got %q", banner)
	}
}

func TestBuildBanner_NoUnread(t *testing.T) {
	svc, _ := newPiggybackTestService()
	banner := buildBanner(svc, "cursor")
	if banner != "" {
		t.Errorf("expected empty banner when no unread, got %q", banner)
	}
}

func TestBuildBanner_WithUnreadMessages(t *testing.T) {
	svc, repo := newPiggybackTestService()
	repo.state.Messages = []domain.Message{
		{ID: 1, From: "claude-code", To: "cursor", Content: "hello", Timestamp: time.Now(), Read: false},
		{ID: 2, From: "claude-code", To: "cursor", Content: "world", Timestamp: time.Now(), Read: false},
	}
	banner := buildBanner(svc, "cursor")
	if banner == "" {
		t.Fatal("expected banner when unread messages exist")
	}
	if !strings.Contains(banner, "2 unread message(s)") {
		t.Errorf("expected '2 unread message(s)' in banner, got %q", banner)
	}
	if !strings.Contains(banner, "read_messages") {
		t.Error("banner should suggest calling read_messages")
	}
}

func TestBuildBanner_WithPendingTasks(t *testing.T) {
	svc, repo := newPiggybackTestService()
	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "test", AssignedTo: "cursor", Status: "pending"},
	}
	banner := buildBanner(svc, "cursor")
	if banner == "" {
		t.Fatal("expected banner when pending tasks exist")
	}
	if !strings.Contains(banner, "1 pending task(s)") {
		t.Errorf("expected '1 pending task(s)' in banner, got %q", banner)
	}
}

func TestBuildBanner_WithBothUnreadAndPending(t *testing.T) {
	svc, repo := newPiggybackTestService()
	repo.state.Messages = []domain.Message{
		{ID: 1, From: "claude-code", To: "cursor", Content: "hi", Timestamp: time.Now(), Read: false},
	}
	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "task", AssignedTo: "cursor", Status: "pending"},
	}
	banner := buildBanner(svc, "cursor")
	if !strings.Contains(banner, "1 unread message(s)") {
		t.Errorf("expected unread in banner, got %q", banner)
	}
	if !strings.Contains(banner, "1 pending task(s)") {
		t.Errorf("expected pending in banner, got %q", banner)
	}
	if !strings.Contains(banner, " and ") {
		t.Errorf("expected 'and' joining both counts, got %q", banner)
	}
}

func TestBuildBanner_IgnoresReadMessages(t *testing.T) {
	svc, repo := newPiggybackTestService()
	repo.state.Messages = []domain.Message{
		{ID: 1, From: "claude-code", To: "cursor", Content: "old", Timestamp: time.Now(), Read: true},
	}
	banner := buildBanner(svc, "cursor")
	if banner != "" {
		t.Errorf("expected no banner for read messages, got %q", banner)
	}
}

func TestBuildBanner_IgnoresOtherAgentMessages(t *testing.T) {
	svc, repo := newPiggybackTestService()
	repo.state.Messages = []domain.Message{
		{ID: 1, From: "cursor", To: "claude-code", Content: "not mine", Timestamp: time.Now(), Read: false},
	}
	banner := buildBanner(svc, "cursor")
	if banner != "" {
		t.Errorf("expected no banner for messages to other agent, got %q", banner)
	}
}

func TestBuildBanner_IncludesBroadcastMessages(t *testing.T) {
	svc, repo := newPiggybackTestService()
	repo.state.Messages = []domain.Message{
		{ID: 1, From: "claude-code", To: "all", Content: "broadcast", Timestamp: time.Now(), Read: false},
	}
	banner := buildBanner(svc, "cursor")
	if banner == "" {
		t.Fatal("expected banner for broadcast messages")
	}
	if !strings.Contains(banner, "1 unread message(s)") {
		t.Errorf("expected unread count in banner, got %q", banner)
	}
}

func TestBuildBanner_IncludesAnyAssignedTasks(t *testing.T) {
	svc, repo := newPiggybackTestService()
	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "anyone", AssignedTo: "any", Status: "pending"},
	}
	banner := buildBanner(svc, "cursor")
	if banner == "" {
		t.Fatal("expected banner for tasks assigned to 'any'")
	}
}

func TestAppendBannerToResult(t *testing.T) {
	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{Type: "text", Text: "first text"},
			mcp.TextContent{Type: "text", Text: "second text"},
		},
	}

	appendBannerToResult(result, "\n\ntest banner")

	// Should append to the LAST text block
	tc, ok := result.Content[1].(mcp.TextContent)
	if !ok {
		t.Fatal("expected TextContent")
	}
	if !strings.Contains(tc.Text, "test banner") {
		t.Errorf("expected banner on last text block, got %q", tc.Text)
	}
}

func TestAppendBannerToResult_NoTextBlock(t *testing.T) {
	result := &mcp.CallToolResult{
		Content: []mcp.Content{},
	}

	appendBannerToResult(result, "\n\ntest banner")

	if len(result.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(result.Content))
	}
	tc, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatal("expected TextContent")
	}
	if !strings.Contains(tc.Text, "test banner") {
		t.Errorf("expected banner text, got %q", tc.Text)
	}
}

func TestBuildBanner_CancelledTasksInjectStop(t *testing.T) {
	svc, repo := newPiggybackTestService()
	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "T1", AssignedTo: "claude-code", Status: "cancelled"},
	}
	banner := buildBanner(svc, "claude-code")
	if banner == "" {
		t.Fatal("expected STOP banner for cancelled tasks")
	}
	if !strings.Contains(banner, "STOP") {
		t.Errorf("expected STOP in banner, got %q", banner)
	}
	if !strings.Contains(banner, "1 of your task(s) have been cancelled") {
		t.Errorf("expected cancelled count in banner, got %q", banner)
	}
}

func TestBuildBanner_CancelledTakesPriority(t *testing.T) {
	svc, repo := newPiggybackTestService()
	repo.state.Messages = []domain.Message{
		{ID: 1, From: "cursor", To: "claude-code", Content: "hi", Timestamp: time.Now(), Read: false},
	}
	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "T1", AssignedTo: "claude-code", Status: "cancelled"},
		{ID: 2, Title: "T2", AssignedTo: "claude-code", Status: "pending"},
	}
	banner := buildBanner(svc, "claude-code")
	// Cancellation should take priority over unread/pending
	if !strings.Contains(banner, "STOP") {
		t.Errorf("expected STOP banner to take priority, got %q", banner)
	}
	if strings.Contains(banner, "unread") {
		t.Errorf("STOP banner should not mention unread messages, got %q", banner)
	}
}
