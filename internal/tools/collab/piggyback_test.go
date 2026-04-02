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
	banner := BuildBanner(svc, "", "some_tool")
	if banner != "" {
		t.Errorf("expected empty banner when no agent, got %q", banner)
	}
}

func TestBuildBanner_NoUnread(t *testing.T) {
	svc, _ := newPiggybackTestService()
	banner := BuildBanner(svc, "cursor", "some_tool")
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
	banner := BuildBanner(svc, "cursor", "some_tool")
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
	banner := BuildBanner(svc, "cursor", "some_tool")
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
	banner := BuildBanner(svc, "cursor", "some_tool")
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
	banner := BuildBanner(svc, "cursor", "some_tool")
	if banner != "" {
		t.Errorf("expected no banner for read messages, got %q", banner)
	}
}

func TestBuildBanner_IgnoresOtherAgentMessages(t *testing.T) {
	svc, repo := newPiggybackTestService()
	repo.state.Messages = []domain.Message{
		{ID: 1, From: "cursor", To: "claude-code", Content: "not mine", Timestamp: time.Now(), Read: false},
	}
	banner := BuildBanner(svc, "cursor", "some_tool")
	if banner != "" {
		t.Errorf("expected no banner for messages to other agent, got %q", banner)
	}
}

func TestBuildBanner_IncludesBroadcastMessages(t *testing.T) {
	svc, repo := newPiggybackTestService()
	repo.state.Messages = []domain.Message{
		{ID: 1, From: "claude-code", To: "all", Content: "broadcast", Timestamp: time.Now(), Read: false},
	}
	banner := BuildBanner(svc, "cursor", "some_tool")
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
	banner := BuildBanner(svc, "cursor", "some_tool")
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
	banner := BuildBanner(svc, "claude-code", "some_tool")
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
	banner := BuildBanner(svc, "claude-code", "some_tool")
	// Cancellation should take priority over unread/pending
	if !strings.Contains(banner, "STOP") {
		t.Errorf("expected STOP banner to take priority, got %q", banner)
	}
	if strings.Contains(banner, "unread") {
		t.Errorf("STOP banner should not mention unread messages, got %q", banner)
	}
}

// ========== Progress nudge tests ==========

func TestBuildBanner_ProgressNudge_NoInProgressTasks(t *testing.T) {
	svc, repo := newPiggybackTestService()
	// Only pending tasks — no nudge expected.
	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Pending", AssignedTo: "claude-code", Status: "pending"},
	}
	banner := BuildBanner(svc, "claude-code", "some_tool")
	// Should show pending task banner but no progress nudge.
	if strings.Contains(banner, "report_progress") {
		t.Errorf("should not nudge when no in_progress tasks, got %q", banner)
	}
}

func TestBuildBanner_ProgressNudge_RecentProgress(t *testing.T) {
	svc, repo := newPiggybackTestService()
	now := time.Now()
	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Active", AssignedTo: "claude-code", Status: "in_progress",
			UpdatedAt: now.Add(-60 * time.Second), LastProgressAt: now.Add(-30 * time.Second)},
	}
	banner := BuildBanner(svc, "claude-code", "some_tool")
	// 30s since last progress — below 90s threshold, no nudge.
	if strings.Contains(banner, "report_progress") {
		t.Errorf("should not nudge within 90s, got %q", banner)
	}
}

func TestBuildBanner_ProgressNudge_SoftReminder(t *testing.T) {
	svc, repo := newPiggybackTestService()
	now := time.Now()
	repo.state.Tasks = []domain.Task{
		{ID: 5, Title: "Active", AssignedTo: "claude-code", Status: "in_progress",
			UpdatedAt: now.Add(-120 * time.Second), LastProgressAt: now.Add(-100 * time.Second)},
	}
	banner := BuildBanner(svc, "claude-code", "some_tool")
	if !strings.Contains(banner, "⏰") {
		t.Errorf("expected soft nudge (⏰) at 100s, got %q", banner)
	}
	if !strings.Contains(banner, "Task #5") {
		t.Errorf("expected task ID in nudge, got %q", banner)
	}
	if !strings.Contains(banner, "report_progress") {
		t.Errorf("expected report_progress suggestion, got %q", banner)
	}
	// Should NOT have urgent warning marker.
	if strings.Contains(banner, "⚠️") {
		t.Errorf("should not be urgent at 100s, got %q", banner)
	}
}

func TestBuildBanner_ProgressNudge_UrgentReminder(t *testing.T) {
	svc, repo := newPiggybackTestService()
	now := time.Now()
	repo.state.Tasks = []domain.Task{
		{ID: 7, Title: "Slow", AssignedTo: "claude-code", Status: "in_progress",
			UpdatedAt: now.Add(-200 * time.Second), LastProgressAt: now.Add(-200 * time.Second)},
	}
	banner := BuildBanner(svc, "claude-code", "some_tool")
	if !strings.Contains(banner, "⚠️") {
		t.Errorf("expected urgent nudge (⚠️) at 200s, got %q", banner)
	}
	if !strings.Contains(banner, "Task #7") {
		t.Errorf("expected task ID in nudge, got %q", banner)
	}
	if !strings.Contains(banner, "watchdog WARNING imminent") {
		t.Errorf("expected urgency text, got %q", banner)
	}
	if !strings.Contains(banner, "report_progress NOW") {
		t.Errorf("expected NOW emphasis, got %q", banner)
	}
}

func TestBuildBanner_ProgressNudge_SuppressedOnHeartbeat(t *testing.T) {
	svc, repo := newPiggybackTestService()
	now := time.Now()
	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Active", AssignedTo: "claude-code", Status: "in_progress",
			UpdatedAt: now.Add(-200 * time.Second), LastProgressAt: now.Add(-200 * time.Second)},
	}
	// heartbeat is in suppressNudgeTools — should not show nudge.
	banner := BuildBanner(svc, "claude-code", "heartbeat")
	if strings.Contains(banner, "report_progress") {
		t.Errorf("should not nudge on heartbeat tool, got %q", banner)
	}
}

func TestBuildBanner_ProgressNudge_SuppressedOnReportProgress(t *testing.T) {
	svc, repo := newPiggybackTestService()
	now := time.Now()
	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Active", AssignedTo: "claude-code", Status: "in_progress",
			UpdatedAt: now.Add(-200 * time.Second), LastProgressAt: now.Add(-200 * time.Second)},
	}
	banner := BuildBanner(svc, "claude-code", "report_progress")
	if strings.Contains(banner, "⚠️") || strings.Contains(banner, "⏰") {
		t.Errorf("should not nudge on report_progress tool, got %q", banner)
	}
}

func TestBuildBanner_ProgressNudge_CancelledTakesPriority(t *testing.T) {
	svc, repo := newPiggybackTestService()
	now := time.Now()
	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "Cancelled", AssignedTo: "claude-code", Status: "cancelled"},
		{ID: 2, Title: "Stale", AssignedTo: "claude-code", Status: "in_progress",
			UpdatedAt: now.Add(-200 * time.Second), LastProgressAt: now.Add(-200 * time.Second)},
	}
	banner := BuildBanner(svc, "claude-code", "some_tool")
	// STOP should take priority — no nudge.
	if !strings.Contains(banner, "STOP") {
		t.Errorf("expected STOP banner, got %q", banner)
	}
	if strings.Contains(banner, "report_progress") {
		t.Errorf("STOP should suppress nudge, got %q", banner)
	}
}

func TestBuildBanner_ProgressNudge_FallsBackToUpdatedAt(t *testing.T) {
	svc, repo := newPiggybackTestService()
	now := time.Now()
	// LastProgressAt is zero — should fall back to UpdatedAt.
	repo.state.Tasks = []domain.Task{
		{ID: 3, Title: "NoProgress", AssignedTo: "claude-code", Status: "in_progress",
			UpdatedAt: now.Add(-120 * time.Second)},
	}
	banner := BuildBanner(svc, "claude-code", "some_tool")
	if !strings.Contains(banner, "⏰") {
		t.Errorf("expected soft nudge using UpdatedAt fallback, got %q", banner)
	}
	if !strings.Contains(banner, "Task #3") {
		t.Errorf("expected task ID in nudge, got %q", banner)
	}
}

// ========== Auto-heartbeat tests ==========

func TestAutoHeartbeat_Debounce(t *testing.T) {
	hbt := newHeartbeatTracker()

	svc, repo := newPiggybackTestService()
	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"claude-code": {
			InstanceID:    "claude-code",
			AgentType:     "claude-code",
			LastHeartbeat: time.Now().Add(-10 * time.Minute),
		},
	}

	// First call should update heartbeat.
	hbt.track(svc, "claude-code")

	var hb1 time.Time
	_ = svc.Query(func(s *domain.CollabState) error {
		hb1 = s.AgentInstances["claude-code"].LastHeartbeat
		return nil
	})
	if time.Since(hb1) > 2*time.Second {
		t.Errorf("first autoHeartbeat should update LastHeartbeat, got %s ago", time.Since(hb1))
	}

	// Second call immediately after should be debounced (no state write).
	// Backdate the heartbeat to detect if it gets overwritten.
	_ = svc.Run(func(s *domain.CollabState) error {
		s.AgentInstances["claude-code"].LastHeartbeat = time.Now().Add(-5 * time.Minute)
		return nil
	})

	hbt.track(svc, "claude-code")

	var hb2 time.Time
	_ = svc.Query(func(s *domain.CollabState) error {
		hb2 = s.AgentInstances["claude-code"].LastHeartbeat
		return nil
	})
	// Should still be the backdated value (5 min ago), not refreshed.
	if time.Since(hb2) < 4*time.Minute {
		t.Errorf("second autoHeartbeat should be debounced, but LastHeartbeat was updated to %s ago", time.Since(hb2))
	}
}

func TestAutoHeartbeat_WritesHeartbeat(t *testing.T) {
	hbt := newHeartbeatTracker()

	svc, repo := newPiggybackTestService()
	oldTime := time.Now().Add(-1 * time.Hour)
	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"claude-code": {
			InstanceID:    "claude-code",
			AgentType:     "claude-code",
			LastHeartbeat: oldTime,
		},
	}

	hbt.track(svc, "claude-code")

	var hb time.Time
	_ = svc.Query(func(s *domain.CollabState) error {
		hb = s.AgentInstances["claude-code"].LastHeartbeat
		return nil
	})
	if time.Since(hb) > 2*time.Second {
		t.Errorf("autoHeartbeat should refresh LastHeartbeat, got %s ago", time.Since(hb))
	}
}

func TestAutoHeartbeat_MatchesByAgentType(t *testing.T) {
	hbt := newHeartbeatTracker()

	svc, repo := newPiggybackTestService()
	oldTime := time.Now().Add(-1 * time.Hour)
	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"claude-code-1": {
			InstanceID:    "claude-code-1",
			AgentType:     "claude-code",
			LastHeartbeat: oldTime,
		},
	}

	// Call with agent type (not instance ID) — should match via AgentType.
	hbt.track(svc, "claude-code")

	var hb time.Time
	_ = svc.Query(func(s *domain.CollabState) error {
		hb = s.AgentInstances["claude-code-1"].LastHeartbeat
		return nil
	})
	if time.Since(hb) > 2*time.Second {
		t.Errorf("autoHeartbeat should match by agent type, got %s ago", time.Since(hb))
	}
}
