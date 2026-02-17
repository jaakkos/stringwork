# Workflow Guide

Patterns and protocols for effective collaboration using the driver/worker model.

## Core Principles

1. **Check context first** -- always run `get_context` at session start
2. **Communicate findings** -- agents can't see each other's work unless you send messages
3. **Report progress** -- workers must heartbeat every 60-90s and report progress every 2-3 min
4. **Update task status** -- keep the shared task list current
5. **Obey cancellation** -- stop immediately when you see a STOP signal

## Roles

### Driver (e.g. Cursor)

The driver creates and assigns work, monitors progress, and intervenes when things go wrong.

Key tools:
- `create_task` with `assigned_to='any'` for auto-assignment to workers
- `worker_status` to see live progress, SLA status, and process activity
- `cancel_agent` to stop stuck workers
- `send_message` to give instructions or feedback

### Workers (e.g. Claude Code, Codex, custom agents)

Workers claim tasks, do the work, and report back.

Key tools:
- `claim_next` to get the next available task
- `heartbeat` every 60-90 seconds with progress info
- `report_progress` every 2-3 minutes with task_id, description, percent_complete
- `send_message` to report findings to the driver
- `update_task` to mark work as completed

### Custom agents

Any MCP client can join by calling `register_agent`. Once registered, it uses the same tools as built-in agents.

## Standard Workflow

### 1. Session start

```
get_context for '<your-agent-name>'
set_presence agent='<you>' status='working' workspace='/path/to/project'
read_messages for '<you>'
list_tasks assigned_to='<you>'
```

### 2. Driver creates work

```
create_task title='Add auth middleware' assigned_to='any' created_by='cursor'
```

Using `assigned_to='any'` lets the server auto-assign to an available worker. Alternatively, assign directly: `assigned_to='claude-code'`.

### 3. Worker claims and works

```
claim_next agent='claude-code'                    # or: update_task id=X status='in_progress'
```

Do the work using native tools (file edit, search, git, terminal).

### 4. Worker reports progress (mandatory)

**Heartbeat -- every 60-90 seconds:**

```
heartbeat agent='claude-code' progress='Implementing auth middleware' step=2 total_steps=5
```

**Structured progress -- every 2-3 minutes:**

```
report_progress agent='claude-code' task_id=5 description='Auth middleware done, writing tests' percent_complete=60 eta_seconds=180
```

**What happens if you don't report:**

| Silence duration | Consequence |
|------------------|-------------|
| 3 minutes | Warning sent to driver |
| 5 minutes | Critical alert sent to driver |
| 10 minutes (no heartbeat) | Task auto-recovered, worker may be cancelled |

### 5. Worker reports findings

```
send_message from='claude-code' to='cursor' content='
## Task #5 Complete: Add auth middleware

**Changes:**
- internal/middleware/auth.go -- JWT validation middleware
- internal/auth/service.go -- Token generation

**Testing:** All tests passing

**Notes:** Consider adding rate limiting next
'
```

### 6. Worker completes task

```
update_task id=5 status='completed' updated_by='claude-code'
```

The driver sees a completion notification via piggyback banner on their next tool call.

## Driver Patterns

### Monitor workers

```
worker_status
```

Shows each worker's: agent progress (heartbeat info), task progress (description, percent, SLA), process activity (runtime, output), and worktree info.

### Cancel a stuck worker

```
cancel_agent agent='claude-code' cancelled_by='cursor' reason='taking too long'
```

This does three things atomically:
1. Cancels all in-progress tasks for the agent
2. Sends a STOP message to the agent
3. Kills the spawned worker process

### Create tasks with SLAs

```
create_task title='Code review' assigned_to='any' created_by='cursor' expected_duration_seconds=300
```

If the task exceeds its SLA, the driver gets an alert.

### Create tasks with work context

```
create_task title='Fix auth bug' assigned_to='any' created_by='cursor' relevant_files='["internal/auth/handler.go","internal/auth/service.go"]' background='JWT tokens expire too quickly' constraints='["Do not change the token format"]'
```

Workers can retrieve this context with `get_work_context`.

## Worker Patterns

### Autonomous loop

For hands-off operation:

```
claim_next agent='claude-code' dry_run=true       # peek at what's next
claim_next agent='claude-code'                     # claim it
# ... do the work, report progress ...
update_task id=X status='completed'                # or handoff
# repeat
```

### Handoff to another agent

```
handoff from='claude-code' to='cursor' summary='Auth middleware implemented' next_steps='Wire into router and add rate limiting'
```

### Handle cancellation

When the driver cancels your work, you'll see on your next tool call:

```
STOP: 1 of your task(s) have been cancelled. Stop immediately, call read_messages, and exit.
```

You must:
1. Stop all current work immediately
2. Call `read_messages` to understand why
3. Exit cleanly -- do NOT continue working on cancelled tasks

## Collaboration Scenarios

### Parallel work

**Driver distributes tasks:**

```
create_task title='Frontend auth UI' assigned_to='claude-code' created_by='cursor'
create_task title='Backend auth API' assigned_to='codex' created_by='cursor'
```

Both workers execute in parallel. Driver monitors via `worker_status`.

### Code review

```
request_review from='cursor' to='claude-code' files='["internal/auth/handler.go"]' description='Review JWT handling for security issues'
```

The reviewer sends findings via `send_message`.

### Shared planning

For complex features:

```
create_plan id='auth' title='Auth feature' goal='JWT auth with rate limiting' created_by='cursor'
update_plan action='add_item' id='1' title='JWT middleware' owner='claude-code'
update_plan action='add_item' id='2' title='Rate limiter' owner='codex'
update_plan action='add_item' id='3' title='Integration tests' owner='cursor' dependencies='["1","2"]'
```

Workers update their items as they progress:

```
update_plan action='update_item' item_id='1' status='completed' add_note='Implemented with RS256'
```

Check progress: `get_plan id='auth'`

### Blocked worker

```
send_message from='claude-code' to='cursor' content='
## Blocked on Task #10

Missing database schema definition. Should I create db/migrations/003_users.sql?
'
update_task id=10 status='blocked' blocked_by='Waiting for schema decision' updated_by='claude-code'
```

Driver unblocks:

```
send_message from='cursor' to='claude-code' content='Yes, follow the pattern in 001_initial.sql'
update_task id=10 status='in_progress' blocked_by='' updated_by='cursor'
```

## Notifications

### Piggyback notifications

Every tool response includes a banner when you have unread content:

```
You have 2 unread message(s) and 1 pending task(s). Call read_messages or get_session_context.
```

This means agents discover new messages on their very next tool call without polling.

### STOP banners

If the driver cancels a worker, the piggyback is replaced with:

```
STOP: 1 of your task(s) have been cancelled. Stop immediately.
```

### Auto-respond

When an agent has unread messages and isn't the currently connected client, the server can spawn a command to wake them. Configured in `config.yaml`:

```yaml
auto_respond:
  claude-code:
    command: ["claude", "--continue", "-p", "/pair-respond", "--dangerously-skip-permissions"]
    cooldown_seconds: 30
```

### JSON-RPC push notifications

The server can push `notifications/pair_update` when the connected agent has new content:

```json
{
  "jsonrpc": "2.0",
  "method": "notifications/pair_update",
  "params": { "unread_messages": 2, "pending_tasks": 1 }
}
```

## Best Practices

### DO

- Check `get_context` at session start
- Set `workspace` in `set_presence` when starting or switching projects
- Report progress continuously (heartbeat + report_progress)
- Send detailed messages with file paths and line numbers
- Create tasks for follow-up work
- Ask questions when blocked
- Obey STOP signals immediately

### DON'T

- Work without checking context first
- Stay silent for more than 2 minutes while working
- Assume your pair can see your work -- communicate explicitly
- Create duplicate tasks -- check existing tasks first
- Ignore STOP signals -- stop immediately
- Make big architectural decisions without discussing first

## Message Templates

### Task completion

```
## Task #X Complete: [title]

**Summary:** [1-2 sentence overview]

**Changes:**
- [file:line] - description

**Testing:** [how you verified]

**Next steps:** [follow-up needed]
```

### Blocker report

```
## Blocked on Task #X: [reason]

**Problem:** [description]
**Tried:** [what you attempted]
**Need:** [what would unblock you]
```

### Handoff

```
## Handing off: [feature]

**Completed:** [done items]
**Remaining:** [todo items]
**Context:** [important background]
```

## See Also

- [QUICK_REFERENCE.md](QUICK_REFERENCE.md) -- tool usage examples
- [SETUP_GUIDE.md](SETUP_GUIDE.md) -- installation and configuration
- [ARCHITECTURE.md](ARCHITECTURE.md) -- system design
