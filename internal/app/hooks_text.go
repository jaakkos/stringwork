package app

// This file centralizes the exact text injected by Stringwork's editor/CLI
// hooks (Claude Code SessionStart/UserPromptSubmit/Stop, Cursor equivalents)
// and the {worker_rules} spawn-prompt placeholder for CLI-spawned workers
// (Codex, Gemini). Keeping one canonical copy means `hooks emit` (the hook
// shims) and the spawn-prompt expansion in worker_manager.go can never drift
// from each other the way the old per-script heredocs in
// scripts/install-claude-hooks.sh could.

// WorkerRulesSessionStart is the full mandatory-rules block injected at
// session start for a session resolved as HookRoleWorker (or HookRoleLegacy).
// Also used to expand the {worker_rules} spawn-prompt placeholder.
const WorkerRulesSessionStart = `## MANDATORY Pair Programming Rules (Stringwork — server-enforced)

You are a worker in the Stringwork pair programming system. These rules are NON-NEGOTIABLE.

### Progress Reporting — REQUIRED while working on ANY task

TRIGGER: You claimed or are working on a task.
ACTION: Call BOTH of these MCP tools at the specified intervals:

1. ` + "`heartbeat`" + ` — every 60-90 seconds with a progress description
   Example: heartbeat agent='claude-code' progress='implementing auth middleware' step=2 total_steps=4

2. ` + "`report_progress`" + ` — every 2-3 minutes with task_id, description, percent_complete
   Example: report_progress agent='claude-code' task_id=5 description='Auth done. Writing tests (8/15).' percent_complete=50 eta_seconds=120

Consequence of NOT reporting:
- 4 min silence → WARNING alert sent to driver
- 7 min silence → CRITICAL alert sent to driver
- 14 min silence → Task auto-recovered, you may be CANCELLED

### Communication — REQUIRED before finishing

TRIGGER: You are about to finish or stop working.
ACTION: Call ` + "`send_message`" + ` from your agent name to the driver with a detailed summary.

### STOP Signals — immediate compliance required

TRIGGER: You see a STOP banner on any tool response.
ACTION: Stop ALL work immediately. Call read_messages. Exit.`

// WorkerRulesUserPrompt is the short per-prompt reminder for a session
// resolved as HookRoleWorker (or HookRoleLegacy).
const WorkerRulesUserPrompt = "MANDATORY: If working on a task, call heartbeat (every 60-90s) and report_progress (every 2-3min). Always send_message with findings before finishing."

// WorkerRulesStop is the pre-stop checklist reminder for a session resolved
// as HookRoleWorker (or HookRoleLegacy).
const WorkerRulesStop = `REMINDER: Before stopping, verify you have:
1. Called send_message to report your findings to the driver
2. Called update_task to mark task status (completed/blocked)
3. Called report_progress with final status
If you haven't done these, continue working and complete them now.`

// DriverSessionStart is injected at session start for a session resolved as
// HookRoleDriver, but only when explicitly enabled in config (the default
// for the driver role is to emit nothing at all — see ShouldEmitHook). Kept
// short: a driver orchestrates via worker_status / create_task, not via the
// worker progress-reporting protocol.
const DriverSessionStart = `## Stringwork Driver Reminder

You are the orchestration DRIVER, not a worker. Use ` + "`worker_status`" + ` to monitor
workers and ` + "`create_task`" + ` to delegate. Do not call ` + "`heartbeat`" + ` or
` + "`report_progress`" + ` unless you are personally executing a task (hybrid mode).`

// DriverUserPrompt is the short per-prompt driver reminder, emitted only
// when explicitly enabled.
const DriverUserPrompt = "Reminder: you are the driver. Monitor workers with worker_status; delegate with create_task. Only call heartbeat/report_progress if you are personally executing work."

// DriverStop is the pre-stop reminder for the driver role, emitted only when
// explicitly enabled.
const DriverStop = `REMINDER: Before stopping, verify you have:
1. Reviewed worker_status for any unfinished delegated work
2. Acknowledged findings sent by workers via send_message
3. Updated task status for anything you completed yourself`

// TextForHook returns the text to inject for the given role and event, or ""
// when there's nothing defined for that combination (callers should treat ""
// as "emit nothing"). ShouldEmitHook decides WHETHER to call this at all;
// TextForHook only decides WHAT to print once that's already yes.
func TextForHook(role HookRole, event HookEvent) string {
	if role == HookRoleDriver {
		switch event {
		case HookEventSessionStart:
			return DriverSessionStart
		case HookEventUserPrompt:
			return DriverUserPrompt
		case HookEventStop:
			return DriverStop
		default:
			return ""
		}
	}
	switch event {
	case HookEventSessionStart, HookEventSpawn:
		return WorkerRulesSessionStart
	case HookEventUserPrompt:
		return WorkerRulesUserPrompt
	case HookEventStop:
		return WorkerRulesStop
	default:
		return ""
	}
}
