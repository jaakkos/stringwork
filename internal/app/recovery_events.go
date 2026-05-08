package app

import (
	"github.com/jaakkos/stringwork/internal/domain"
)

// recoveryEventLogCap bounds Task.RecoveryEvents so a task that bounces
// repeatedly through the recovery path doesn't grow an unbounded log on
// disk. Newest entries win; older entries fall off the front.
//
// 8 was picked empirically — large enough to capture a typical
// reconciler -> watchdog -> reconciler again sequence with the
// suppressed-post-reconcile breadcrumb in between, small enough that the
// JSON column stays well under 1 KiB even with verbose summaries.
const recoveryEventLogCap = 8

// Recovery event Source codes. Stable strings — used by the dashboard /
// chrome-extension to render distinct icons per source. Don't rename
// without coordinating with consumers.
const (
	RecoverySourceReconciler = "reconciler"
	RecoverySourceWatchdog   = "watchdog"
	RecoverySourceAutoCancel = "auto_cancel"
)

// Recovery event Reason codes. Machine-readable, stable. The Summary on
// the event is the human-readable form; consumers that need to filter or
// classify events do it on Reason.
const (
	RecoveryReasonWorkerExitWithOwnedTask       = "worker_exit_with_owned_task"
	RecoveryReasonWorkerExitUnclaimedAssignment = "worker_exit_unclaimed_assignment"
	RecoveryReasonAgentHeartbeatStale           = "agent_heartbeat_stale"
	RecoveryReasonTaskProgressStuck             = "task_progress_stuck"
	RecoveryReasonAutoBlockedAtMaxFailures      = "auto_blocked_at_max_failures"
	RecoveryReasonSuppressedPostReconcile       = "suppressed_post_reconcile"
)

// AppendRecoveryEvent records ev on t.RecoveryEvents (newest last) and
// keeps t.ResultSummary in sync with the most recent event.Summary so
// existing readers (dashboard rows, MCP `list_tasks` output, chrome
// extension popup) keep showing a meaningful one-liner without needing
// to render the structured log.
//
// The log is capped at recoveryEventLogCap entries; once exceeded, the
// oldest entries are dropped (FIFO). Callers do not need to gate on
// "is the summary already set" — that was the bug that produced the
// "first writer wins" behaviour where reconciler and watchdog silently
// overwrote each other.
func AppendRecoveryEvent(t *domain.Task, ev domain.RecoveryEvent) {
	if t == nil {
		return
	}
	t.RecoveryEvents = append(t.RecoveryEvents, ev)
	if len(t.RecoveryEvents) > recoveryEventLogCap {
		t.RecoveryEvents = t.RecoveryEvents[len(t.RecoveryEvents)-recoveryEventLogCap:]
	}
	if ev.Summary != "" {
		t.ResultSummary = ev.Summary
	}
}
