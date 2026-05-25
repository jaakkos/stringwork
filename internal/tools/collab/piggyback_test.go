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

// TestBuildBanner_CancelledBeforeRespawnIsIgnored is the regression test for
// the STOP-banner-tombstone loop. After a cancel_agent fires, the cancelled
// task lingers in state with AssignedTo = "<parent-type>" forever (it isn't
// reassigned or deleted). Every subsequent spawn of that parent type must
// NOT see the stale cancellation, otherwise the new worker reads the STOP
// banner on its first tool call and exits cleanly — leaving the watchdog to
// respawn it again, ad infinitum. The fix: only count cancelled tasks whose
// UpdatedAt is at or after the resolved instance's LastSpawnedAt.
func TestBuildBanner_CancelledBeforeRespawnIsIgnored(t *testing.T) {
	svc, repo := newPiggybackTestService()
	cancelledAt := time.Now().Add(-2 * time.Hour)
	respawnAt := time.Now().Add(-1 * time.Minute)
	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"claude-code-task-30": {
			InstanceID:    "claude-code-task-30",
			AgentType:     "claude-code",
			Role:          domain.RoleWorker,
			Status:        "busy",
			LastSpawnedAt: respawnAt,
			LastHeartbeat: respawnAt,
		},
	}
	repo.state.Tasks = []domain.Task{
		{ID: 26, Title: "old work", AssignedTo: "claude-code", Status: "cancelled", UpdatedAt: cancelledAt},
	}
	banner := BuildBanner(svc, "claude-code-task-30", "presence")
	if strings.Contains(banner, "STOP") {
		t.Errorf("fresh worker must not see STOP for cancellations older than its spawn time; got %q", banner)
	}
}

// TestBuildBanner_CancelledAfterRespawnStillStops verifies that a cancellation
// targeting the currently-running worker still surfaces as STOP. Pairs with
// TestBuildBanner_CancelledBeforeRespawnIsIgnored.
func TestBuildBanner_CancelledAfterRespawnStillStops(t *testing.T) {
	svc, repo := newPiggybackTestService()
	respawnAt := time.Now().Add(-5 * time.Minute)
	cancelledAt := time.Now().Add(-30 * time.Second)
	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"claude-code-task-30": {
			InstanceID:    "claude-code-task-30",
			AgentType:     "claude-code",
			Role:          domain.RoleWorker,
			Status:        "busy",
			LastSpawnedAt: respawnAt,
			LastHeartbeat: respawnAt,
		},
	}
	repo.state.Tasks = []domain.Task{
		{ID: 31, Title: "active work", AssignedTo: "claude-code", Status: "cancelled", UpdatedAt: cancelledAt},
	}
	banner := BuildBanner(svc, "claude-code-task-30", "presence")
	if !strings.Contains(banner, "STOP") {
		t.Errorf("cancellation newer than spawn must surface STOP; got %q", banner)
	}
}

// TestBuildBanner_CancelledForUnknownAgentStillStops keeps the legacy
// behavior for callers that have no AgentInstance row (drivers, HTTP-only
// custom agents): when LastSpawnedAt is zero, every cancellation counts.
// This avoids regressing test fixtures and out-of-band callers that
// legitimately want to see all cancellations.
func TestBuildBanner_CancelledForUnknownAgentStillStops(t *testing.T) {
	svc, repo := newPiggybackTestService()
	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "T", AssignedTo: "claude-code", Status: "cancelled", UpdatedAt: time.Now()},
	}
	banner := BuildBanner(svc, "claude-code", "presence")
	if !strings.Contains(banner, "STOP") {
		t.Errorf("legacy fallback (no AgentInstance row) must still stop; got %q", banner)
	}
}

// TestBuildBanner_DriverUsesDaemonStartedAt locks in MUST_FIX #3c. The
// driver agent has no per-instance LastSpawnedAt, so any cancellation
// older than the daemon process itself must NOT trigger a STOP banner
// for cursor — even if the cancellation predates the daemon boot.
// Without DaemonStartedAt, every cancel_agent leaves a tombstone that
// trips a STOP on every cursor tool call across daemon restarts.
func TestBuildBanner_DriverUsesDaemonStartedAt(t *testing.T) {
	svc, repo := newPiggybackTestService()
	daemonStart := time.Now().Add(-1 * time.Minute)
	cancelledAt := daemonStart.Add(-2 * time.Hour)
	repo.state.DriverID = "cursor"
	repo.state.DaemonStartedAt = daemonStart
	// Driver has no AgentInstance row in classic deployments — leave
	// AgentInstances empty so we exercise the DaemonStartedAt fallback
	// path rather than per-instance LastSpawnedAt.
	repo.state.AgentInstances = map[string]*domain.AgentInstance{}
	repo.state.Tasks = []domain.Task{
		{ID: 99, Title: "old", AssignedTo: "cursor", Status: "cancelled", UpdatedAt: cancelledAt},
	}

	banner := BuildBanner(svc, "cursor", "some_tool")
	if strings.Contains(banner, "STOP") {
		t.Errorf("driver must not see STOP for cancellation older than daemon boot; got %q", banner)
	}
}

// TestBuildBanner_DriverStopsForFreshCancellation pairs with the test
// above: a cancellation issued AFTER daemon boot must still surface as
// STOP for the driver. Otherwise the DaemonStartedAt fallback would
// over-suppress and the driver would silently miss real cancellations.
func TestBuildBanner_DriverStopsForFreshCancellation(t *testing.T) {
	svc, repo := newPiggybackTestService()
	daemonStart := time.Now().Add(-2 * time.Hour)
	cancelledAt := time.Now().Add(-30 * time.Second)
	repo.state.DriverID = "cursor"
	repo.state.DaemonStartedAt = daemonStart
	repo.state.AgentInstances = map[string]*domain.AgentInstance{}
	repo.state.Tasks = []domain.Task{
		{ID: 100, Title: "fresh", AssignedTo: "cursor", Status: "cancelled", UpdatedAt: cancelledAt},
	}

	banner := BuildBanner(svc, "cursor", "some_tool")
	if !strings.Contains(banner, "STOP") {
		t.Errorf("driver must see STOP for cancellation newer than daemon boot; got %q", banner)
	}
}

// TestBuildBanner_TombstoneOlderThan24hSuppressed locks in MUST_FIX #3d.
// Cancelled tasks linger in state with AssignedTo = "<parent-type>"
// forever; after 24h they must drop out of the BuildBanner cancellation
// count so they cannot trip new spawns. Pairs with
// TestBuildBanner_TombstoneWithin24hStillStops below.
func TestBuildBanner_TombstoneOlderThan24hSuppressed(t *testing.T) {
	svc, repo := newPiggybackTestService()
	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "ancient", AssignedTo: "claude-code", Status: "cancelled", UpdatedAt: time.Now().Add(-25 * time.Hour)},
	}
	banner := BuildBanner(svc, "claude-code", "some_tool")
	if strings.Contains(banner, "STOP") {
		t.Errorf("cancellation older than 24h must be suppressed by the tombstone TTL; got %q", banner)
	}
}

// TestBuildBanner_TombstoneWithin24hStillStops verifies the TTL only
// trims tombstones beyond 24h, so a cancellation issued within the last
// day still surfaces as STOP — matching real-world driver intent.
func TestBuildBanner_TombstoneWithin24hStillStops(t *testing.T) {
	svc, repo := newPiggybackTestService()
	repo.state.Tasks = []domain.Task{
		{ID: 1, Title: "recent", AssignedTo: "claude-code", Status: "cancelled", UpdatedAt: time.Now().Add(-23 * time.Hour)},
	}
	banner := BuildBanner(svc, "claude-code", "some_tool")
	if !strings.Contains(banner, "STOP") {
		t.Errorf("cancellation within 24h must still surface as STOP; got %q", banner)
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
	// 30s since last progress — below progressNudgeThreshold (120s), no nudge.
	if strings.Contains(banner, "report_progress") {
		t.Errorf("should not nudge within progressNudgeThreshold, got %q", banner)
	}
}

// TestBuildBanner_DriverSkipsNudgeForWorkerOwnedInProgress verifies the
// orchestrating driver is not told to report_progress for in_progress tasks
// assigned to the parent type while a worker instance owns them in CurrentTasks.
func TestBuildBanner_DriverSkipsNudgeForWorkerOwnedInProgress(t *testing.T) {
	svc, repo := newPiggybackTestService()
	now := time.Now().Add(-5 * time.Minute)
	repo.state.DriverID = "claude-code"
	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"claude-code": {
			InstanceID: "claude-code", AgentType: "claude-code",
			Role: domain.RoleDriver, Status: "working",
		},
		"claude-code-1": {
			InstanceID: "claude-code-1", AgentType: "claude-code",
			Role: domain.RoleWorker, Status: "working",
			CurrentTasks: []int{9},
		},
	}
	repo.state.Tasks = []domain.Task{
		{
			ID: 9, Title: "worker task", AssignedTo: "claude-code", Status: "in_progress",
			UpdatedAt: now, LastProgressAt: now,
		},
	}

	banner := BuildBanner(svc, "claude-code", "create_task")
	if strings.Contains(banner, "report_progress") {
		t.Errorf("driver must not be nudged for worker-owned in_progress task; got %q", banner)
	}
}

// TestBuildBanner_DriverNudgesHybridOwnedTask verifies hybrid drivers still
// get progress nudges for tasks they own via CurrentTasks.
func TestBuildBanner_DriverNudgesHybridOwnedTask(t *testing.T) {
	svc, repo := newPiggybackTestService()
	now := time.Now().Add(-5 * time.Minute)
	repo.state.DriverID = "claude-code"
	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"claude-code": {
			InstanceID: "claude-code", AgentType: "claude-code",
			Role: domain.RoleDriver, Status: "working",
			CurrentTasks: []int{3},
		},
	}
	repo.state.Tasks = []domain.Task{
		{
			ID: 3, Title: "driver hybrid", AssignedTo: "claude-code", Status: "in_progress",
			UpdatedAt: now, LastProgressAt: now,
		},
	}

	banner := BuildBanner(svc, "claude-code", "some_tool")
	if !strings.Contains(banner, "report_progress") {
		t.Errorf("hybrid driver should be nudged for owned in_progress task; got %q", banner)
	}
}

func TestBuildBanner_ProgressNudge_SoftReminder(t *testing.T) {
	svc, repo := newPiggybackTestService()
	now := time.Now()
	// 150s: above progressNudgeThreshold (120s) but below progressUrgentThreshold (240s).
	repo.state.Tasks = []domain.Task{
		{ID: 5, Title: "Active", AssignedTo: "claude-code", Status: "in_progress",
			UpdatedAt: now.Add(-150 * time.Second), LastProgressAt: now.Add(-150 * time.Second)},
	}
	banner := BuildBanner(svc, "claude-code", "some_tool")
	if !strings.Contains(banner, "⚠️") {
		t.Errorf("expected REQUIRED nudge (⚠️) at 150s, got %q", banner)
	}
	if !strings.Contains(banner, "Task #5") {
		t.Errorf("expected task ID in nudge, got %q", banner)
	}
	if !strings.Contains(banner, "report_progress") {
		t.Errorf("expected report_progress directive, got %q", banner)
	}
	if !strings.Contains(banner, "MANDATORY") {
		t.Errorf("expected MANDATORY language, got %q", banner)
	}
	// Should NOT have the critical-level marker yet.
	if strings.Contains(banner, "⛔") {
		t.Errorf("should not be critical-level at 150s, got %q", banner)
	}
}

func TestBuildBanner_ProgressNudge_UrgentReminder(t *testing.T) {
	svc, repo := newPiggybackTestService()
	now := time.Now()
	// 250s: above progressUrgentThreshold (240s).
	repo.state.Tasks = []domain.Task{
		{ID: 7, Title: "Slow", AssignedTo: "claude-code", Status: "in_progress",
			UpdatedAt: now.Add(-250 * time.Second), LastProgressAt: now.Add(-250 * time.Second)},
	}
	banner := BuildBanner(svc, "claude-code", "some_tool")
	if !strings.Contains(banner, "⛔") {
		t.Errorf("expected MANDATORY nudge (⛔) at 250s, got %q", banner)
	}
	if !strings.Contains(banner, "Task #7") {
		t.Errorf("expected task ID in nudge, got %q", banner)
	}
	if !strings.Contains(banner, "AUTO-CANCELLED") {
		t.Errorf("expected auto-cancel warning, got %q", banner)
	}
	if !strings.Contains(banner, "IMMEDIATELY") {
		t.Errorf("expected IMMEDIATELY emphasis, got %q", banner)
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
	if strings.Contains(banner, "⚠️") || strings.Contains(banner, "⏰") || strings.Contains(banner, "⛔") {
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
	if !strings.Contains(banner, "⚠️") {
		t.Errorf("expected REQUIRED nudge using UpdatedAt fallback, got %q", banner)
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

// fakeProcessLiveness implements ProcessLivenessProvider for tests.
//
// The map distinguishes three cases:
//   - key present, value true  → registered AND alive
//   - key present, value false → registered AND dead
//   - key absent               → not registered (HTTP-only, no spawn row)
type fakeProcessLiveness struct {
	alive map[string]bool
}

func (f *fakeProcessLiveness) IsWorkerRunning(instanceID string) bool {
	return f.alive[instanceID]
}

func (f *fakeProcessLiveness) HasWorker(instanceID string) bool {
	_, ok := f.alive[instanceID]
	return ok
}

// TestAutoHeartbeat_DebounceDoesNotMaskDeadWorker (M4) — when a
// ProcessLivenessProvider is registered, the auto-heartbeat refresh
// must NOT bump LastHeartbeat for a worker whose underlying process is
// dead. Otherwise stray tool calls (replays, late-arriving messages)
// silently extend the agent's effective heartbeat past the watchdog
// staleness threshold and prevent recovery.
//
// Workers without registered process info (HTTP-only) must continue to
// behave as before: refresh on every (debounced) tool call.
func TestAutoHeartbeat_DebounceDoesNotMaskDeadWorker(t *testing.T) {
	svc, repo := newPiggybackTestService()
	oldTime := time.Now().Add(-1 * time.Hour)
	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"claude-code": {
			InstanceID:    "claude-code",
			AgentType:     "claude-code",
			LastHeartbeat: oldTime,
		},
		"codex": {
			InstanceID:    "codex",
			AgentType:     "codex",
			LastHeartbeat: oldTime,
		},
	}

	// claude-code has a registered process and it is DEAD.
	// codex has NO registered process (HTTP-only, fall through path).
	provider := &fakeProcessLiveness{alive: map[string]bool{
		"claude-code": false,
	}}
	hbt := newHeartbeatTrackerWithLiveness(provider)

	hbt.track(svc, "claude-code")
	hbt.track(svc, "codex")

	_ = svc.Query(func(s *domain.CollabState) error {
		hbDead := s.AgentInstances["claude-code"].LastHeartbeat
		if !hbDead.Equal(oldTime) {
			t.Errorf("dead worker's LastHeartbeat must NOT be refreshed by piggyback; was %s", hbDead)
		}
		hbHTTP := s.AgentInstances["codex"].LastHeartbeat
		if time.Since(hbHTTP) > 2*time.Second {
			t.Errorf("HTTP-only worker (no registered process) should still get refresh; got %s ago", time.Since(hbHTTP))
		}
		return nil
	})
}

// TestAutoHeartbeat_RefreshesAliveWorker (M4) — sanity check: when a
// ProcessLivenessProvider says the worker IS alive, piggyback still
// refreshes LastHeartbeat as normal.
func TestAutoHeartbeat_RefreshesAliveWorker(t *testing.T) {
	svc, repo := newPiggybackTestService()
	oldTime := time.Now().Add(-1 * time.Hour)
	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"claude-code": {
			InstanceID:    "claude-code",
			AgentType:     "claude-code",
			LastHeartbeat: oldTime,
		},
	}

	provider := &fakeProcessLiveness{alive: map[string]bool{
		"claude-code": true,
	}}
	hbt := newHeartbeatTrackerWithLiveness(provider)

	hbt.track(svc, "claude-code")

	_ = svc.Query(func(s *domain.CollabState) error {
		hb := s.AgentInstances["claude-code"].LastHeartbeat
		if time.Since(hb) > 2*time.Second {
			t.Errorf("alive worker's LastHeartbeat should refresh; got %s ago", time.Since(hb))
		}
		return nil
	})
}

// TestPiggybackAutoHeartbeat_PrefersAliveOverDead (H3) — a parent-type
// ping like "claude-code" must NEVER refresh the LastHeartbeat of a
// task-bound sibling like "claude-code-task-7". The fallback was a
// single-pass map iteration that could land on the task-bound row
// first, silently reviving a dead task-bound instance and preventing
// the watchdog from recovering its task.
//
// Two-pass contract:
//  1. exact InstanceID match (preferred)
//  2. AgentType match excluding task-bound siblings (fallback)
func TestPiggybackAutoHeartbeat_PrefersAliveOverDead(t *testing.T) {
	hbt := newHeartbeatTracker()

	svc, repo := newPiggybackTestService()
	deadHB := time.Now().Add(-1 * time.Hour)
	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"claude-code-1": {
			InstanceID:    "claude-code-1",
			AgentType:     "claude-code",
			Role:          domain.RoleWorker,
			Status:        "idle",
			LastHeartbeat: deadHB,
		},
		"claude-code-task-7": {
			InstanceID:    "claude-code-task-7",
			AgentType:     "claude-code",
			Role:          domain.RoleWorker,
			Status:        "busy",
			CurrentTasks:  []int{7},
			LastHeartbeat: deadHB,
		},
	}

	hbt.track(svc, "claude-code")

	_ = svc.Query(func(s *domain.CollabState) error {
		tb := s.AgentInstances["claude-code-task-7"]
		if !tb.LastHeartbeat.Equal(deadHB) {
			t.Errorf("task-bound sibling MUST NOT be refreshed by parent-type ping; was %s, want %s", tb.LastHeartbeat, deadHB)
		}
		pool := s.AgentInstances["claude-code-1"]
		if pool.LastHeartbeat.Equal(deadHB) {
			t.Errorf("static-pool sibling SHOULD have been refreshed; heartbeat unchanged")
		}
		return nil
	})
}

// TestPiggybackAutoHeartbeat_MultiInstanceDeterministic (H3) — when
// multiple non-task-bound instances of the same parent type are
// registered, the fallback must update ALL of them deterministically
// (not just whichever the map iteration happens to land on first).
// This avoids races where one ping refreshes "claude-code-1" and the
// next refreshes "claude-code-2", leaving each individual instance
// looking stale to the watchdog.
func TestPiggybackAutoHeartbeat_MultiInstanceDeterministic(t *testing.T) {
	hbt := newHeartbeatTracker()

	svc, repo := newPiggybackTestService()
	deadHB := time.Now().Add(-1 * time.Hour)
	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"claude-code-1": {
			InstanceID: "claude-code-1", AgentType: "claude-code",
			Role: domain.RoleWorker, Status: "idle", LastHeartbeat: deadHB,
		},
		"claude-code-2": {
			InstanceID: "claude-code-2", AgentType: "claude-code",
			Role: domain.RoleWorker, Status: "idle", LastHeartbeat: deadHB,
		},
		"claude-code-task-9": {
			InstanceID: "claude-code-task-9", AgentType: "claude-code",
			Role: domain.RoleWorker, Status: "busy", CurrentTasks: []int{9},
			LastHeartbeat: deadHB,
		},
	}

	hbt.track(svc, "claude-code")

	_ = svc.Query(func(s *domain.CollabState) error {
		for _, id := range []string{"claude-code-1", "claude-code-2"} {
			inst := s.AgentInstances[id]
			if inst.LastHeartbeat.Equal(deadHB) {
				t.Errorf("static-pool sibling %s SHOULD have been refreshed", id)
			}
		}
		tb := s.AgentInstances["claude-code-task-9"]
		if !tb.LastHeartbeat.Equal(deadHB) {
			t.Errorf("task-bound sibling MUST be skipped; was refreshed to %s", tb.LastHeartbeat)
		}
		return nil
	})
}
