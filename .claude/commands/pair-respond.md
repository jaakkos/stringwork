---
description: Process unread messages and pending tasks from your pair programmer
allowed-tools: mcp__stringwork__*
---

You have been auto-spawned to respond to your pair programmer. Follow these steps exactly:

## Step 1: Initialize
1. Call `get_session_context` for 'claude-code'
2. Call `set_presence` agent='claude-code' status='working' workspace='$ARGUMENTS'
3. Call `read_messages` for 'claude-code'
4. Call `list_tasks` assigned_to='claude-code'

## Step 2: Process work
For each unread message or pending task:
- **Question** → Research and answer it
- **Task assignment** → Claim with `update_task` status='in_progress', then do the work
- **Review request** → Review code, send structured findings
- **Update/acknowledgment** → Acknowledge and continue

## Step 3: MANDATORY progress reporting while working

**YOU MUST DO BOTH OF THESE — the server monitors and will cancel you if you don't:**

- Call `heartbeat agent='claude-code' progress='<what you are doing>'` every 60-90 seconds
- Call `report_progress agent='claude-code' task_id=X description='<status>' percent_complete=N` every 2-3 minutes

Failure to report: WARNING at 3 min, CRITICAL at 5 min, CANCELLED at 10 min.

## Step 4: Report back BEFORE finishing
- Call `send_message from='claude-code' to='cursor'` with a detailed summary:
  - What you did
  - Files changed (with paths and line numbers)
  - Test results
  - Remaining work or blockers
- Call `update_task` with final status (completed/blocked)
- Call `report_progress` with percent_complete=100

## STOP signals
If you see 🛑 STOP on any tool response: stop immediately, call read_messages, exit.
