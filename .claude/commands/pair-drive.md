---
description: Drive the pair programming session — create tasks, monitor workers, review completions
allowed-tools: mcp__stringwork__*
---

You are Claude Code acting as the **driver** in the Stringwork pair programming system. Follow these steps:

## Step 1: Initialize

1. Call `get_session_context` for 'claude-code'
2. Call `set_presence` agent='claude-code' status='working' workspace='$ARGUMENTS'
3. Call `worker_status` to see available workers and their current state
4. Call `list_tasks` to see all tasks

## Step 2: Drive the session

Based on the current state, take the appropriate action:

### If there are completed tasks to review:
- Read worker messages via `read_messages` for 'claude-code'
- Review the worker's findings and changes
- Acknowledge via `send_message` from='claude-code' to the worker
- Create follow-up tasks if needed

### If there is work to delegate:
- Create tasks with `create_task` using `assigned_to='any'` and `created_by='claude-code'`
- Include `relevant_files`, `background`, and `constraints` for scoped work
- Set `expected_duration_seconds` for SLA monitoring
- Workers will be auto-spawned by the server

### If workers appear stuck:
- Check `worker_status` for progress, SLA status, and process activity
- Send a message: `send_message` from='claude-code' to='<worker>' content='status update?'
- If truly stuck: `cancel_agent` agent='<worker>' cancelled_by='claude-code' reason='...'

### If you want to do work yourself (hybrid mode):
- Claim a task: `claim_next` agent='claude-code' or `update_task` id=X status='in_progress'
- Do the work using your native tools
- Call `heartbeat` and `report_progress` while working (mandatory)
- Mark complete: `update_task` id=X status='completed' updated_by='claude-code'

## Step 3: Monitor continuously

- Use `worker_status` to see real-time worker progress and SLA status
- Read messages as they arrive (piggyback banners notify you)
- Synthesize findings from multiple workers into a coherent summary
- Create new tasks based on worker discoveries

## Key driver tools

| Tool | Purpose |
|------|---------|
| `create_task` | Delegate work (use `assigned_to='any'` for auto-assign) |
| `worker_status` | Live view of workers, progress, SLA |
| `cancel_agent` | Stop a stuck worker |
| `send_message` | Communicate with workers |
| `list_tasks` | See all task status |
| `get_plan` / `create_plan` | Shared planning for complex features |
