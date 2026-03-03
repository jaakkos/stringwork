package app

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jaakkos/stringwork/internal/domain"
)

func testStateLoader(state *domain.CollabState) func() (*domain.CollabState, error) {
	var mu sync.Mutex
	return func() (*domain.CollabState, error) {
		mu.Lock()
		defer mu.Unlock()
		if state == nil {
			return domain.NewCollabState(), nil
		}
		return state, nil
	}
}

func TestNotifier_CheckOnce_NoPushWhenAgentEmpty(t *testing.T) {
	dir := t.TempDir()
	signalPath := filepath.Join(dir, ".stringwork-notify")
	_ = TouchNotifySignal(signalPath)

	state := domain.NewCollabState()
	state.Messages = append(state.Messages, domain.Message{ID: 1, To: "cursor", Read: false})

	var pushed bool
	pushFunc := func(method string, params any) error {
		pushed = true
		return nil
	}
	getAgent := func() string { return "" }
	n := NewNotifier(signalPath, testStateLoader(state), getAgent, pushFunc, nil)
	n.CheckOnce()
	if pushed {
		t.Error("should not push when agent is empty")
	}
}

func TestNotifier_CheckOnce_PushWhenUnread(t *testing.T) {
	dir := t.TempDir()
	signalPath := filepath.Join(dir, ".stringwork-notify")
	_ = TouchNotifySignal(signalPath)

	state := domain.NewCollabState()
	state.Messages = append(state.Messages, domain.Message{ID: 1, From: "claude-code", To: "cursor", Content: "hi", Read: false})

	var pushMethod string
	var pushParams PairUpdateParams
	pushFunc := func(method string, params any) error {
		pushMethod = method
		if p, ok := params.(PairUpdateParams); ok {
			pushParams = p
		}
		return nil
	}
	getAgent := func() string { return "cursor" }
	n := NewNotifier(signalPath, testStateLoader(state), getAgent, pushFunc, nil)
	n.CheckOnce()
	if pushMethod != "notifications/pair_update" {
		t.Errorf("method = %q, want notifications/pair_update", pushMethod)
	}
	if pushParams.UnreadMessages != 1 {
		t.Errorf("UnreadMessages = %d, want 1", pushParams.UnreadMessages)
	}
	if pushParams.Summary == "" {
		t.Error("Summary should be set")
	}
}

func TestNotifier_CheckOnce_NoPushWhenNoUnread(t *testing.T) {
	dir := t.TempDir()
	signalPath := filepath.Join(dir, ".stringwork-notify")
	_ = TouchNotifySignal(signalPath)

	state := domain.NewCollabState()
	state.Messages = append(state.Messages, domain.Message{ID: 1, To: "cursor", Read: true})

	var pushed bool
	pushFunc := func(method string, params any) error {
		pushed = true
		return nil
	}
	getAgent := func() string { return "cursor" }
	n := NewNotifier(signalPath, testStateLoader(state), getAgent, pushFunc, nil)
	n.CheckOnce()
	if pushed {
		t.Error("should not push when no unread messages or pending tasks")
	}
}

func TestNotifier_CheckOnce_PushWhenPendingTasks(t *testing.T) {
	dir := t.TempDir()
	signalPath := filepath.Join(dir, ".stringwork-notify")
	_ = TouchNotifySignal(signalPath)

	state := domain.NewCollabState()
	state.Tasks = append(state.Tasks, domain.Task{ID: 1, Title: "Do it", AssignedTo: "cursor", Status: "pending"})

	var pushParams PairUpdateParams
	pushFunc := func(method string, params any) error {
		if p, ok := params.(PairUpdateParams); ok {
			pushParams = p
		}
		return nil
	}
	getAgent := func() string { return "cursor" }
	n := NewNotifier(signalPath, testStateLoader(state), getAgent, pushFunc, nil)
	n.CheckOnce()
	if pushParams.PendingTasks != 1 {
		t.Errorf("PendingTasks = %d, want 1", pushParams.PendingTasks)
	}
}

func TestNotifier_CheckOnce_SameRevisionPushedOnce(t *testing.T) {
	dir := t.TempDir()
	signalPath := filepath.Join(dir, ".stringwork-notify")
	_ = TouchNotifySignal(signalPath)

	state := domain.NewCollabState()
	state.Messages = append(state.Messages, domain.Message{ID: 1, To: "cursor", Read: false})

	var pushCount int
	pushFunc := func(method string, params any) error {
		pushCount++
		return nil
	}
	getAgent := func() string { return "cursor" }
	n := NewNotifier(signalPath, testStateLoader(state), getAgent, pushFunc, nil)
	n.CheckOnce()
	n.CheckOnce()
	if pushCount != 1 {
		t.Errorf("push count = %d, want 1 (same revision should not push twice)", pushCount)
	}
}

func TestNotifier_CheckOnce_NoPushWhenSignalFileMissing(t *testing.T) {
	dir := t.TempDir()
	signalPath := filepath.Join(dir, ".stringwork-notify")
	// do not create signal file

	state := domain.NewCollabState()
	state.Messages = append(state.Messages, domain.Message{ID: 1, To: "cursor", Read: false})

	var pushed bool
	pushFunc := func(method string, params any) error {
		pushed = true
		return nil
	}
	getAgent := func() string { return "cursor" }
	n := NewNotifier(signalPath, testStateLoader(state), getAgent, pushFunc, nil)
	n.CheckOnce()
	if pushed {
		t.Error("should not push when signal file does not exist (revision empty)")
	}
}

func TestNotifier_Start_Stop_Graceful(t *testing.T) {
	dir := t.TempDir()
	signalPath := filepath.Join(dir, ".stringwork-notify")
	_ = os.WriteFile(signalPath, []byte("1"), 0644)

	getAgent := func() string { return "cursor" }
	pushFunc := func(method string, params any) error { return nil }
	n := NewNotifier(signalPath, testStateLoader(domain.NewCollabState()), getAgent, pushFunc, nil, WithPollInterval(10*time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		n.Start(ctx)
		close(done)
	}()
	time.Sleep(25 * time.Millisecond)
	cancel()
	n.Stop()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Start did not return after Stop")
	}
}
