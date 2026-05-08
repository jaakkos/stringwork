package app

import (
	"testing"
	"time"

	"github.com/jaakkos/stringwork/internal/domain"
)

func TestRecoveryEvents_NewestEntrySyncedToResultSummary(t *testing.T) {
	task := &domain.Task{ID: 1}
	AppendRecoveryEvent(task, domain.RecoveryEvent{
		At:      time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC),
		Source:  RecoverySourceReconciler,
		Reason:  RecoveryReasonWorkerExitWithOwnedTask,
		Summary: "first",
	})
	AppendRecoveryEvent(task, domain.RecoveryEvent{
		At:      time.Date(2026, 5, 4, 12, 1, 0, 0, time.UTC),
		Source:  RecoverySourceWatchdog,
		Reason:  RecoveryReasonAgentHeartbeatStale,
		Summary: "second",
	})
	if len(task.RecoveryEvents) != 2 {
		t.Fatalf("RecoveryEvents len = %d, want 2", len(task.RecoveryEvents))
	}
	if task.ResultSummary != "second" {
		t.Errorf("ResultSummary = %q, want %q (newest event mirrors)", task.ResultSummary, "second")
	}
}

func TestRecoveryEvents_CappedAtConstant(t *testing.T) {
	task := &domain.Task{ID: 1}
	for i := 0; i < recoveryEventLogCap+3; i++ {
		AppendRecoveryEvent(task, domain.RecoveryEvent{
			At:      time.Date(2026, 5, 4, 12, 0, i, 0, time.UTC),
			Source:  RecoverySourceWatchdog,
			Reason:  RecoveryReasonAgentHeartbeatStale,
			Summary: "evt",
		})
	}
	if len(task.RecoveryEvents) != recoveryEventLogCap {
		t.Errorf("RecoveryEvents len = %d, want %d (cap)", len(task.RecoveryEvents), recoveryEventLogCap)
	}
	first := task.RecoveryEvents[0].At.Second()
	if first != 3 {
		t.Errorf("oldest surviving event At.Second() = %d, want 3 (first 3 should have been dropped)", first)
	}
}

func TestRecoveryEvents_NilTaskNoOp(t *testing.T) {
	// Defensive: append to a nil task should not panic. Production callers
	// don't pass nil today but the helper is exported and a defensive
	// check is cheap.
	AppendRecoveryEvent(nil, domain.RecoveryEvent{Summary: "noop"})
}

func TestRecoveryEvents_EmptySummaryDoesNotClobberResultSummary(t *testing.T) {
	// A defensive behaviour: if a caller forgets to set Summary on the
	// event, we should NOT clobber an already-meaningful ResultSummary
	// with the empty string. Real callers always set Summary; this guards
	// against a future regression.
	task := &domain.Task{ID: 1, ResultSummary: "previous meaningful summary"}
	AppendRecoveryEvent(task, domain.RecoveryEvent{
		At:     time.Now(),
		Source: RecoverySourceReconciler,
		Reason: RecoveryReasonWorkerExitWithOwnedTask,
	})
	if task.ResultSummary != "previous meaningful summary" {
		t.Errorf("ResultSummary = %q, want it preserved when event has empty Summary", task.ResultSummary)
	}
	if len(task.RecoveryEvents) != 1 {
		t.Errorf("RecoveryEvents len = %d, want 1 (event still recorded)", len(task.RecoveryEvents))
	}
}
