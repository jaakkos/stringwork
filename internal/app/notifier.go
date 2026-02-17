package app

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	defaultDebounceMs   = 200
	defaultPollInterval = 10 * time.Second
)

// PairUpdateParams is the payload for notifications/pair_update.
type PairUpdateParams struct {
	UnreadMessages int    `json:"unread_messages"`
	PendingTasks   int    `json:"pending_tasks"`
	Summary        string `json:"summary"`
}

// SpawnChecker is implemented by WorkerManager. Notifier calls Check() when the signal file changes.
type SpawnChecker interface {
	Check()
}

// Notifier watches the signal file and pushes pair_update notifications when
// the connected agent has new unread messages or pending tasks.
// If a SpawnChecker (WorkerManager) is attached, it also triggers spawning for workers with unread content.
type Notifier struct {
	signalPath   string
	repo         StateRepository
	getAgent     func() string
	pushFunc     func(method string, params any) error
	logger       *log.Logger
	debounceMs   int
	pollInterval time.Duration

	spawnChecker SpawnChecker // optional; nil disables auto-spawn

	mu            sync.Mutex
	lastPushedRev string
	debounceTimer *time.Timer
	watcher       *fsnotify.Watcher
	useFsnotify   bool
	stopCh        chan struct{}
	doneCh        chan struct{}
	pushMu        sync.Mutex // serializes checkAndPush to prevent duplicate pushes/spawns
}

// NotifierOption configures the notifier.
type NotifierOption func(*Notifier)

// WithPollInterval sets the fallback poll interval (default 60s).
func WithPollInterval(d time.Duration) NotifierOption {
	return func(n *Notifier) {
		n.pollInterval = d
	}
}

// WithWorkerManager attaches a WorkerManager that spawns worker instances.
func WithWorkerManager(wm *WorkerManager) NotifierOption {
	return func(n *Notifier) {
		n.spawnChecker = wm
	}
}

// NewNotifier creates a notifier. getAgent returns the connected agent (e.g. "cursor"); if empty, push is skipped.
// pushFunc is called with method "notifications/pair_update" and params PairUpdateParams when the agent has unread content.
func NewNotifier(signalPath string, repo StateRepository, getAgent func() string, pushFunc func(method string, params any) error, logger *log.Logger, opts ...NotifierOption) *Notifier {
	n := &Notifier{
		signalPath:   signalPath,
		repo:         repo,
		getAgent:     getAgent,
		pushFunc:     pushFunc,
		logger:       logger,
		debounceMs:   defaultDebounceMs,
		pollInterval: defaultPollInterval,
		stopCh:       make(chan struct{}),
		doneCh:       make(chan struct{}),
	}
	for _, o := range opts {
		o(n)
	}
	return n
}

// Start starts the file watcher and fallback poll. Returns when ctx is cancelled.
// If fsnotify fails to initialize, falls back to poll-only mode.
func (n *Notifier) Start(ctx context.Context) {
	defer close(n.doneCh)

	watchDir := filepath.Dir(n.signalPath)
	signalName := filepath.Base(n.signalPath)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		n.logger.Printf("Notifier: fsnotify init failed (%v), using poll-only", err)
		n.useFsnotify = false
	} else {
		n.watcher = watcher
		n.useFsnotify = true
		if err := watcher.Add(watchDir); err != nil {
			n.logger.Printf("Notifier: fsnotify add %s failed (%v), using poll-only", watchDir, err)
			_ = watcher.Close()
			n.watcher = nil
			n.useFsnotify = false
		}
	}

	if n.useFsnotify {
		defer n.watcher.Close()
		go n.watchLoop(ctx, signalName)
	}

	n.pollLoop(ctx)
}

// Stop signals the notifier to stop. Call after cancelling the context passed to Start.
func (n *Notifier) Stop() {
	close(n.stopCh)
	<-n.doneCh
}

// CheckOnce runs one check-and-push cycle (for testing or manual trigger).
func (n *Notifier) CheckOnce() {
	n.checkAndPush()
}

// Trigger forces a check-and-push cycle, bypassing the revision dedup.
// Call after a state write (e.g. from CollabService.Run) to ensure the
// auto-responder fires even if fsnotify misses the event (same-process write).
func (n *Notifier) Trigger() {
	n.mu.Lock()
	n.lastPushedRev = "" // reset so checkAndPush won't skip
	n.mu.Unlock()
	n.triggerDebounced()
}

func (n *Notifier) watchLoop(ctx context.Context, signalName string) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-n.stopCh:
			return
		case event, ok := <-n.watcher.Events:
			if !ok {
				return
			}
			if filepath.Base(event.Name) != signalName {
				continue
			}
			if event.Op&(fsnotify.Write|fsnotify.Create) == 0 {
				continue
			}
			n.triggerDebounced()
		case _, ok := <-n.watcher.Errors:
			if !ok {
				return
			}
		}
	}
}

func (n *Notifier) triggerDebounced() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.debounceTimer != nil {
		n.debounceTimer.Stop()
	}
	n.debounceTimer = time.AfterFunc(time.Duration(n.debounceMs)*time.Millisecond, func() {
		n.checkAndPush()
	})
}

func (n *Notifier) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(n.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-n.stopCh:
			return
		case <-ticker.C:
			n.checkAndPush()
		}
	}
}

func (n *Notifier) checkAndPush() {
	// Serialize the entire check-and-push cycle. Without this, the debounce timer
	// goroutine and the poll loop can both pass the revision dedup check concurrently,
	// causing duplicate push notifications and duplicate worker spawns.
	n.pushMu.Lock()
	defer n.pushMu.Unlock()

	rev := n.readSignalRevision()
	if rev == "" {
		return
	}
	n.mu.Lock()
	if rev == n.lastPushedRev {
		n.mu.Unlock()
		return
	}
	n.mu.Unlock()

	// Spawn workers / auto-respond: wake up non-connected agents that have unread content.
	if n.spawnChecker != nil {
		n.spawnChecker.Check()
	}

	// Push notification to the connected agent (if any).
	agent := n.getAgent()
	if agent == "" {
		// Still update rev so we don't re-run auto-respond for the same signal.
		n.mu.Lock()
		n.lastPushedRev = rev
		n.mu.Unlock()
		return
	}

	state, err := n.repo.Load()
	if err != nil {
		return
	}

	unread := 0
	for _, m := range state.Messages {
		if (m.To == agent || m.To == "all") && !m.Read {
			unread++
		}
	}
	pending := 0
	for _, t := range state.Tasks {
		if (t.AssignedTo == agent || t.AssignedTo == "any") && t.Status == "pending" {
			pending++
		}
	}
	if unread == 0 && pending == 0 {
		n.mu.Lock()
		n.lastPushedRev = rev
		n.mu.Unlock()
		return
	}

	summary := n.buildSummary(unread, pending)
	params := PairUpdateParams{
		UnreadMessages: unread,
		PendingTasks:   pending,
		Summary:        summary,
	}
	if err := n.pushFunc("notifications/pair_update", params); err != nil {
		n.logger.Printf("Notifier: push failed: %v", err)
		return
	}
	n.mu.Lock()
	n.lastPushedRev = rev
	n.mu.Unlock()
}

func (n *Notifier) readSignalRevision() string {
	data, err := os.ReadFile(n.signalPath)
	if err != nil {
		return ""
	}
	return string(data)
}

func (n *Notifier) buildSummary(unread, pending int) string {
	if unread > 0 && pending > 0 {
		return fmt.Sprintf("%d new message(s), %d pending task(s)", unread, pending)
	}
	if unread > 0 {
		return fmt.Sprintf("%d new message(s)", unread)
	}
	return fmt.Sprintf("%d pending task(s)", pending)
}
