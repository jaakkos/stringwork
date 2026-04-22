package collab

import (
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/jaakkos/stringwork/internal/domain"
)

// TestGetSessionContext_StaleAgentRendersAsOffline verifies that an agent
// whose presence has gone stale (last_seen older than the presence TTL) is
// rendered as plain "offline" — not the misleading "<status> (offline)" that
// preserved the agent's last self-reported "working".
func TestGetSessionContext_StaleAgentRendersAsOffline(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	// Stale presence: last_seen far older than the 5min mock-policy TTL.
	stale := time.Now().Add(-2 * time.Hour)
	repo.state.Presence = map[string]*domain.Presence{
		"cursor": {
			Agent:    "cursor",
			Status:   "working",
			LastSeen: time.Now(),
		},
		"claude-code-task-7": {
			Agent:         "claude-code-task-7",
			Status:        "working",
			CurrentTaskID: 7,
			Workspace:     "/Users/me/proj",
			LastSeen:      stale,
		},
	}
	// No fresh AgentInstance heartbeat for the worker either.
	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"cursor": {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "idle", LastHeartbeat: time.Now()},
		"claude-code-task-7": {
			InstanceID:    "claude-code-task-7",
			AgentType:     "claude-code",
			Role:          domain.RoleWorker,
			Status:        "offline",
			LastHeartbeat: stale,
		},
	}

	srv := testServer(svc, logger)
	result, err := callTool(t, srv, "get_session_context", map[string]any{"for": "cursor"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)

	// Find the line for the stale worker.
	var line string
	for _, l := range strings.Split(text, "\n") {
		if strings.Contains(l, "claude-code-task-7") {
			line = l
			break
		}
	}
	if line == "" {
		t.Fatalf("expected claude-code-task-7 in pair status; got:\n%s", text)
	}

	// Must show "offline" and NOT "working" (the leftover self-reported status).
	if !strings.Contains(line, "offline") {
		t.Errorf("expected 'offline' in stale agent line, got: %q", line)
	}
	if strings.Contains(line, "working") {
		t.Errorf("did not expect leftover 'working' in stale agent line, got: %q", line)
	}
	// Task ID and workspace should be suppressed for offline rows — they're stale context.
	if strings.Contains(line, "Task #7") {
		t.Errorf("did not expect Task #7 in stale agent line, got: %q", line)
	}
	if strings.Contains(line, "/Users/me/proj") {
		t.Errorf("did not expect workspace path in stale agent line, got: %q", line)
	}
}

// TestGetSessionContext_FreshAgentKeepsRichDisplay verifies that an agent
// with a recent set_presence (within TTL) keeps showing its self-reported
// status, task ID, and workspace — only stale rows get stripped.
func TestGetSessionContext_FreshAgentKeepsRichDisplay(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	now := time.Now()
	repo.state.Presence = map[string]*domain.Presence{
		"cursor": {
			Agent:         "cursor",
			Status:        "working",
			CurrentTaskID: 42,
			Workspace:     "/Users/me/proj",
			LastSeen:      now,
		},
	}
	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"cursor": {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "idle", LastHeartbeat: now},
	}

	srv := testServer(svc, logger)
	result, err := callTool(t, srv, "get_session_context", map[string]any{"for": "cursor"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)

	var line string
	for _, l := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "cursor:") {
			line = l
			break
		}
	}
	if line == "" {
		t.Fatalf("expected cursor in pair status; got:\n%s", text)
	}
	if !strings.Contains(line, "working") {
		t.Errorf("expected fresh agent to keep 'working' status, got: %q", line)
	}
	if strings.Contains(line, "offline") {
		t.Errorf("did not expect 'offline' on fresh agent, got: %q", line)
	}
	if !strings.Contains(line, "Task #42") {
		t.Errorf("expected Task #42 on fresh agent, got: %q", line)
	}
	if !strings.Contains(line, "/Users/me/proj") {
		t.Errorf("expected workspace on fresh agent, got: %q", line)
	}
}

// TestWorkerStatus_OfflineSuppressesStaleProgress verifies that worker_status
// hides the "Progress: ..." line for offline workers when the progress was
// reported long ago — these lines were the source of the "stale progress
// from days-old sessions" complaint.
func TestWorkerStatus_OfflineSuppressesStaleProgress(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	staleProgress := time.Now().Add(-48 * time.Hour)
	staleHeartbeat := time.Now().Add(-2 * time.Hour)
	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"cursor": {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "idle"},
		"claude-code-task-5": {
			InstanceID:        "claude-code-task-5",
			AgentType:         "claude-code",
			Role:              domain.RoleWorker,
			Status:            "offline",
			LastHeartbeat:     staleHeartbeat,
			Progress:          "Compiling findings report and sending to cursor",
			ProgressUpdatedAt: staleProgress,
		},
	}

	srv := testServer(svc, logger)
	result, err := callTool(t, srv, "worker_status", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)

	if strings.Contains(text, "Compiling findings report") {
		t.Errorf("stale Progress line should be suppressed for offline worker, got:\n%s", text)
	}
	if !strings.Contains(text, "claude-code-task-5") {
		t.Errorf("worker entry should still appear, got:\n%s", text)
	}
}

// TestWorkerStatus_FreshOfflineProgressShown ensures that a worker which
// just transitioned to offline (e.g. heartbeat went stale moments ago) keeps
// its recent progress line — only multi-minute-old progress is hidden.
func TestWorkerStatus_FreshOfflineProgressShown(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)

	now := time.Now()
	repo.state.AgentInstances = map[string]*domain.AgentInstance{
		"cursor": {InstanceID: "cursor", AgentType: "cursor", Role: domain.RoleDriver, Status: "idle"},
		"claude-code": {
			InstanceID:        "claude-code",
			AgentType:         "claude-code",
			Role:              domain.RoleWorker,
			Status:            "offline",
			LastHeartbeat:     now.Add(-30 * time.Second),
			Progress:          "Reviewing files",
			ProgressUpdatedAt: now.Add(-30 * time.Second),
		},
	}

	srv := testServer(svc, logger)
	result, err := callTool(t, srv, "worker_status", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)

	if !strings.Contains(text, "Reviewing files") {
		t.Errorf("recent Progress line should still appear even when offline, got:\n%s", text)
	}
}
