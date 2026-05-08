package app

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jaakkos/stringwork/internal/domain"
)

// failureAckTestWM builds a minimal WorkerManager with a single in-memory
// state and a serialised mutator. sendFailureAck is what we exercise; we
// don't need spawn / process plumbing here.
func failureAckTestWM(t *testing.T, state *domain.CollabState) *WorkerManager {
	t.Helper()
	EnsureStateMaps(state)
	if state.NextMsgID == 0 {
		state.NextMsgID = 1
	}
	var mu sync.Mutex
	mutator := func(fn func(*domain.CollabState) error) error {
		mu.Lock()
		defer mu.Unlock()
		return fn(state)
	}
	return &WorkerManager{
		stateMutator: mutator,
		stateLoader:  func() (*domain.CollabState, error) { return state, nil },
		logger:       testLogger(t),
		failureAcks:  make(map[string]*failureAckState),
	}
}

// TestSendFailureAck_FirstFailureAppendsMessage — first call appends one
// message and remembers its ID for future coalescing.
func TestSendFailureAck_FirstFailureAppendsMessage(t *testing.T) {
	state := &domain.CollabState{DriverID: "cursor"}
	wm := failureAckTestWM(t, state)

	wm.sendFailureAck("claude-code-task-1", errors.New("exit status 1"), 3)

	if len(state.Messages) != 1 {
		t.Fatalf("Messages len = %d, want 1", len(state.Messages))
	}
	msg := state.Messages[0]
	if msg.From != "system" {
		t.Errorf("msg.From = %q, want system", msg.From)
	}
	if !strings.Contains(msg.Content, "claude-code-task-1") {
		t.Errorf("msg.Content = %q, want it to mention the instance", msg.Content)
	}
	if !strings.Contains(msg.Content, "exit status 1") {
		t.Errorf("msg.Content = %q, want it to include the underlying error", msg.Content)
	}
	st := wm.failureAcks["claude-code-task-1"]
	if st == nil {
		t.Fatal("failureAcks state missing for instance")
	}
	if st.messageID != msg.ID {
		t.Errorf("failureAcks.messageID = %d, want %d (the appended message ID)", st.messageID, msg.ID)
	}
	if st.count != 3 {
		t.Errorf("failureAcks.count = %d, want 3 (mirrors attempts arg)", st.count)
	}
}

// TestSendFailureAck_RepeatedFailureUpdatesSingleMessage — three calls in
// quick succession produce ONE message in state.Messages, with the
// rolling text. Pre-fix this would have been three separate messages.
func TestSendFailureAck_RepeatedFailureUpdatesSingleMessage(t *testing.T) {
	state := &domain.CollabState{DriverID: "cursor"}
	wm := failureAckTestWM(t, state)

	wm.sendFailureAck("claude-code-task-1", errors.New("exit status 1"), 3)
	wm.sendFailureAck("claude-code-task-1", errors.New("exit status 1"), 3)
	wm.sendFailureAck("claude-code-task-1", errors.New("exit status 2"), 3)

	if len(state.Messages) != 1 {
		t.Fatalf("Messages len = %d, want 1 (coalesced into single rolling message)", len(state.Messages))
	}
	msg := state.Messages[0]
	if !strings.Contains(msg.Content, "9") {
		t.Errorf("msg.Content = %q, want it to mention 9 total failures (3+3+3)", msg.Content)
	}
	if !strings.Contains(msg.Content, "exit status 2") {
		t.Errorf("msg.Content = %q, want last error to be 'exit status 2'", msg.Content)
	}
	if msg.Read {
		t.Error("msg.Read = true; want false (coalesce should re-mark as unread)")
	}
}

// TestSendFailureAck_AppendsNewMessageAfterReset — resetFailureAck (called
// from the runOnce success path) clears the coalesce state. The next
// failure starts a fresh message rather than tacking onto the prior one.
func TestSendFailureAck_AppendsNewMessageAfterReset(t *testing.T) {
	state := &domain.CollabState{DriverID: "cursor"}
	wm := failureAckTestWM(t, state)

	wm.sendFailureAck("claude-code-task-1", errors.New("first burst"), 3)
	wm.resetFailureAck("claude-code-task-1")
	wm.sendFailureAck("claude-code-task-1", errors.New("second burst"), 3)

	if len(state.Messages) != 2 {
		t.Fatalf("Messages len = %d, want 2 (reset starts a fresh message)", len(state.Messages))
	}
	if !strings.Contains(state.Messages[0].Content, "first burst") {
		t.Errorf("Messages[0].Content = %q, want first burst", state.Messages[0].Content)
	}
	if !strings.Contains(state.Messages[1].Content, "second burst") {
		t.Errorf("Messages[1].Content = %q, want second burst", state.Messages[1].Content)
	}
	st := wm.failureAcks["claude-code-task-1"]
	if st == nil || st.messageID != state.Messages[1].ID {
		t.Errorf("failureAcks.messageID = %v, want %d (the new fresh message)", st, state.Messages[1].ID)
	}
}

// TestSendFailureAck_AppendsNewMessageWhenPriorMessageGone — the previous
// message can be pruned by retention sweeps. When the coalesce state's
// messageID no longer exists in state.Messages, we gracefully append a
// new message and re-anchor onto it.
func TestSendFailureAck_AppendsNewMessageWhenPriorMessageGone(t *testing.T) {
	state := &domain.CollabState{DriverID: "cursor"}
	wm := failureAckTestWM(t, state)

	wm.sendFailureAck("claude-code-task-1", errors.New("first"), 3)
	if len(state.Messages) != 1 {
		t.Fatalf("setup: expected 1 message, got %d", len(state.Messages))
	}

	state.Messages = nil
	wm.sendFailureAck("claude-code-task-1", errors.New("after prune"), 1)

	if len(state.Messages) != 1 {
		t.Fatalf("Messages len = %d, want 1 (fresh append after prior message disappeared)", len(state.Messages))
	}
	if !strings.Contains(state.Messages[0].Content, "after prune") {
		t.Errorf("msg.Content = %q, want it to include the new error", state.Messages[0].Content)
	}
	st := wm.failureAcks["claude-code-task-1"]
	if st == nil || st.messageID != state.Messages[0].ID {
		t.Errorf("failureAcks.messageID = %v, want %d (re-anchored on the new message)", st, state.Messages[0].ID)
	}
}

// TestSendFailureAck_NewMessageAfterCoalesceWindow — a failure that lands
// after failureAckCoalesceWindow has elapsed since the last failure
// starts a fresh message, even without an explicit reset. Drivers see
// distinct burst boundaries in the inbox.
func TestSendFailureAck_NewMessageAfterCoalesceWindow(t *testing.T) {
	state := &domain.CollabState{DriverID: "cursor"}
	wm := failureAckTestWM(t, state)

	wm.sendFailureAck("claude-code-task-1", errors.New("first"), 1)

	st := wm.failureAcks["claude-code-task-1"]
	if st == nil {
		t.Fatal("expected coalesce state after first call")
	}
	st.lastAt = time.Now().Add(-2 * failureAckCoalesceWindow)

	wm.sendFailureAck("claude-code-task-1", errors.New("second burst much later"), 1)

	if len(state.Messages) != 2 {
		t.Fatalf("Messages len = %d, want 2 (window expired between failures)", len(state.Messages))
	}
}
