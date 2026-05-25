---
description: Process unread messages and pending tasks from your pair programmer
allowed-tools: mcp__stringwork__*
---

You have been auto-spawned to respond to your pair programmer. Follow these steps exactly:

## Step 1: Initialize
1. Call `get_session_context` for 'claude-code'
2. Call `set_presence` agent='claude-code' status='working' workspace='$ARGUMENTS'
3. Call `read_messages` for 'claude-code'

## Step 2: Branch on orchestration role

Read the **Orchestration Role** line from `get_session_context`.

### If DRIVER
You orchestrate workers — do **not** use the worker progress loop below.

1. Call `worker_status` and `list_tasks` (all tasks, not only assigned to you).
2. For worker messages and completed tasks: review, `update_task`, `send_message` with feedback or follow-ups.
3. Delegate new work with `create_task` (`assigned_to='any'`, `created_by='claude-code'`).
4. Do **not** send routine status updates to workers — use `worker_status` instead.
5. Do **not** call `heartbeat` / `report_progress` unless you own an in_progress task (hybrid mode).
6. Use `cancel_agent` only when a worker is stuck or no longer needed.

If you are the configured driver, prefer `/pair-drive` for a full driver session.

### If WORKER (or legacy peer mode)

1. Call `list_tasks` assigned_to='claude-code'
2. For each unread message or pending task:
   - **Question** → Research and answer it
   - **Task assignment** → Claim with `update_task` status='in_progress', then do the work
   - **Review request** → Review code, send structured findings
   - **Update/acknowledgment** → Acknowledge and continue

**MANDATORY progress reporting while working** (server-enforced):

- `heartbeat agent='claude-code' progress='...'` every 60-90 seconds
- `report_progress agent='claude-code' task_id=X description='...' percent_complete=N` every 2-3 minutes

Failure to report: WARNING at 4 min, CRITICAL at 7 min, CANCELLED at 14 min.

Before finishing: `send_message` to the **driver** with detailed summary (changes, files, tests), then `update_task` with final status.

## STOP signals
If you see 🛑 STOP on any tool response: stop immediately, call read_messages, exit.
