package collab

import (
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/jaakkos/stringwork/internal/domain"
)

func TestSendMessage_ValidMessage(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	result, err := callTool(t, srv, "send_message", map[string]any{
		"from": "cursor", "to": "claude-code", "content": "Hello, this is a test message",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "Message #1 sent to claude-code") {
		t.Errorf("unexpected result text: %s", text)
	}
	if len(repo.state.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(repo.state.Messages))
	}
	msg := repo.state.Messages[0]
	if msg.From != "cursor" || msg.To != "claude-code" || msg.Content != "Hello, this is a test message" {
		t.Errorf("unexpected message: from=%q to=%q content=%q", msg.From, msg.To, msg.Content)
	}
	if msg.Read {
		t.Error("message should be unread")
	}
}

func TestSendMessage_InvalidSender(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	_, err := callTool(t, srv, "send_message", map[string]any{
		"from": "unknown-agent", "to": "claude-code", "content": "test",
	})
	if err == nil {
		t.Fatal("expected error for invalid sender")
	}
}

func TestSendMessage_AllRecipient(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	_, err := callTool(t, srv, "send_message", map[string]any{
		"from": "cursor", "to": "all", "content": "broadcast message",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.state.Messages) != 1 || repo.state.Messages[0].To != "all" {
		t.Errorf("expected broadcast message, got %+v", repo.state.Messages)
	}
}

func TestSendMessage_IncrementsMsgID(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	for i := 0; i < 3; i++ {
		_, err := callTool(t, srv, "send_message", map[string]any{
			"from": "cursor", "to": "claude-code", "content": "msg",
		})
		if err != nil {
			t.Fatalf("unexpected error on message %d: %v", i+1, err)
		}
	}
	if len(repo.state.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(repo.state.Messages))
	}
	for i, msg := range repo.state.Messages {
		if msg.ID != i+1 {
			t.Errorf("message %d has ID %d, expected %d", i, msg.ID, i+1)
		}
	}
}

func TestReadMessages_NoMessages(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	result, err := callTool(t, srv, "read_messages", map[string]any{"for": "claude-code"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "No messages") {
		t.Errorf("expected 'No messages', got %q", text)
	}
}

func TestReadMessages_ValidRecipient(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	repo.state.Messages = []domain.Message{
		{ID: 1, From: "cursor", To: "claude-code", Content: "msg1", Timestamp: time.Now()},
		{ID: 2, From: "claude-code", To: "cursor", Content: "msg2", Timestamp: time.Now()},
		{ID: 3, From: "cursor", To: "claude-code", Content: "msg3", Timestamp: time.Now()},
	}
	repo.state.NextMsgID = 4

	result, err := callTool(t, srv, "read_messages", map[string]any{"for": "claude-code"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "msg1") || !strings.Contains(text, "msg3") {
		t.Errorf("expected msg1 and msg3 in result: %s", text)
	}
	if strings.Contains(text, "msg2") {
		t.Error("msg2 should not be in result (it's to cursor, not claude-code)")
	}
}

func TestReadMessages_UnreadOnly(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	repo.state.Messages = []domain.Message{
		{ID: 1, From: "cursor", To: "claude-code", Content: "already-read-content", Timestamp: time.Now(), Read: true},
		{ID: 2, From: "cursor", To: "claude-code", Content: "fresh-unread-content", Timestamp: time.Now(), Read: false},
	}
	repo.state.NextMsgID = 3

	result, err := callTool(t, srv, "read_messages", map[string]any{"for": "claude-code", "unread_only": true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)
	if strings.Contains(text, "already-read-content") {
		t.Error("read message should be excluded with unread_only")
	}
	if !strings.Contains(text, "fresh-unread-content") {
		t.Error("unread message should be included")
	}
}

func TestReadMessages_MarkRead(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	repo.state.Messages = []domain.Message{
		{ID: 1, From: "cursor", To: "claude-code", Content: "msg", Timestamp: time.Now(), Read: false},
	}
	repo.state.NextMsgID = 2

	_, err := callTool(t, srv, "read_messages", map[string]any{"for": "claude-code"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !repo.state.Messages[0].Read {
		t.Error("message should be marked as read")
	}
}

func TestSendMessage_RegisteredAgentCanSend(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	repo.state.RegisteredAgents["my-bot"] = &domain.RegisteredAgent{Name: "my-bot", DisplayName: "My Bot"}

	result, err := callTool(t, srv, "send_message", map[string]any{
		"from": "my-bot", "to": "cursor", "content": "hello from registered agent",
	})
	if err != nil {
		t.Fatalf("registered agent should be allowed to send: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "Message #1 sent to cursor") {
		t.Errorf("unexpected result: %s", text)
	}
}

func TestRegisteredAgent_FullRoundtrip(t *testing.T) {
	svc, _ := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	// Register
	_, err := callTool(t, srv, "register_agent", map[string]any{"name": "test-agent", "display_name": "Test Agent"})
	if err != nil {
		t.Fatalf("register failed: %v", err)
	}

	// Send
	_, err = callTool(t, srv, "send_message", map[string]any{
		"from": "test-agent", "to": "cursor", "content": "I am the test agent",
	})
	if err != nil {
		t.Fatalf("registered agent send failed: %v", err)
	}

	// Reply
	_, err = callTool(t, srv, "send_message", map[string]any{
		"from": "cursor", "to": "test-agent", "content": "Welcome, test agent!",
	})
	if err != nil {
		t.Fatalf("reply to registered agent failed: %v", err)
	}

	// Read
	result, err := callTool(t, srv, "read_messages", map[string]any{"for": "test-agent"})
	if err != nil {
		t.Fatalf("registered agent read failed: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "Welcome, test agent!") {
		t.Errorf("registered agent should see reply: %s", text)
	}
}

// TestPingPong verifies pair programming messaging: cursor (driver) sends "ping" to claude-code and codex;
// each worker reads and replies "pong" to cursor; cursor reads and sees both pongs.
func TestPingPong(t *testing.T) {
	svc, repo := newTestService()
	logger := log.New(io.Discard, "", 0)
	srv := testServer(svc, logger)

	// Cursor -> claude-code: ping
	_, err := callTool(t, srv, "send_message", map[string]any{
		"from": "cursor", "to": "claude-code", "content": "ping",
	})
	if err != nil {
		t.Fatalf("cursor send ping to claude-code: %v", err)
	}

	// Cursor -> codex: ping
	_, err = callTool(t, srv, "send_message", map[string]any{
		"from": "cursor", "to": "codex", "content": "ping",
	})
	if err != nil {
		t.Fatalf("cursor send ping to codex: %v", err)
	}

	// Claude-code reads (sees ping), replies pong to cursor
	result, err := callTool(t, srv, "read_messages", map[string]any{"for": "claude-code"})
	if err != nil {
		t.Fatalf("claude-code read_messages: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "ping") {
		t.Errorf("claude-code should see ping: %s", text)
	}
	_, err = callTool(t, srv, "send_message", map[string]any{
		"from": "claude-code", "to": "cursor", "content": "pong",
	})
	if err != nil {
		t.Fatalf("claude-code send pong: %v", err)
	}

	// Codex reads (sees ping), replies pong to cursor
	result, err = callTool(t, srv, "read_messages", map[string]any{"for": "codex"})
	if err != nil {
		t.Fatalf("codex read_messages: %v", err)
	}
	text = resultText(t, result)
	if !strings.Contains(text, "ping") {
		t.Errorf("codex should see ping: %s", text)
	}
	_, err = callTool(t, srv, "send_message", map[string]any{
		"from": "codex", "to": "cursor", "content": "pong",
	})
	if err != nil {
		t.Fatalf("codex send pong: %v", err)
	}

	// Cursor reads messages (sees both pongs)
	result, err = callTool(t, srv, "read_messages", map[string]any{"for": "cursor"})
	if err != nil {
		t.Fatalf("cursor read_messages: %v", err)
	}
	text = resultText(t, result)
	if !strings.Contains(text, "pong") {
		t.Errorf("cursor should see pong(s): %s", text)
	}
	// Should see two pong messages (from claude-code and codex)
	pongCount := strings.Count(text, "pong")
	if pongCount < 2 {
		t.Errorf("cursor should see 2 pongs, got %d in: %s", pongCount, text)
	}

	if len(repo.state.Messages) != 4 {
		t.Errorf("expected 4 messages (2 ping, 2 pong), got %d", len(repo.state.Messages))
	}
}
