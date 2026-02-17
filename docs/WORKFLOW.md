# Pair Programming Workflow

Standard patterns and protocols for effective collaboration between cursor and claude-code.

## Core Principles

1. **Check context first** - Always run `get_context` at session start
2. **Communicate findings** - Your pair can't see your work unless you send messages
3. **Update task status** - Keep shared task list current
4. **Coordinate work** - Avoid duplicate effort or conflicts
5. **Be explicit** - Clear messages prevent misunderstandings

## Agent Identities

Built-in agents (always available):

- **cursor** - The agent working in the Cursor IDE
- **claude-code** - The agent working in the Claude Code CLI

Custom agents can register via `register_agent` and then participate in all collaboration tools. Each agent should use a consistent identifier in all tool calls.

## Standard Workflow Pattern

### 1. Session Start

Every session should begin with:

```
Use get_context for '<your-agent-name>'
```

This returns:
- Unread message count and summary
- Tasks assigned to you
- Recent activity overview

### 2. Read Messages

If there are unread messages, `get_context` includes a summary; use:

```
Use read_messages for '<your-agent-name>'
```

to mark messages as read and get full content.

### 3. Check Tasks

Review tasks assigned to you:

```
Use list_tasks with assigned_to='<your-agent-name>'
```

Or see all tasks:

```
Use list_tasks
```

### 4. Claim and Work on Tasks

When starting a task:

```
Use update_task with id=X status='in_progress' updated_by='<your-agent-name>'
```

**Do the work** using your native tools (file edit, search, git, terminal).

### 5. Report Progress (MANDATORY)

Workers **must** report progress while working. The server monitors these reports and escalates if a worker goes silent.

**Heartbeat — every 60–90 seconds:**

```
Use heartbeat agent='<your-agent-name>' progress='Implementing auth middleware' step=2 total_steps=5
```

**Structured progress — every 2–3 minutes:**

```
Use report_progress agent='<your-agent-name>' task_id=X description='Auth middleware done, writing unit tests' percent_complete=60 eta_seconds=180
```

**What happens if you don't report:**

| Silence duration | Consequence |
|------------------|-------------|
| 3 minutes | Warning alert sent to driver |
| 5 minutes | Critical alert sent to driver |
| 10 minutes (no heartbeat) | Task auto-recovered, worker may be cancelled |

**Informal updates** are also welcome for context — send them as messages:

```
Use send_message from='<your-agent-name>' to='<other-agent>' content='Task #X: finished auth, writing tests. ~2 min left.'
```

Include: what you've completed, what's remaining, and estimated time.

### 6. Report Findings

After completing work, send a message with your findings:

```
Use send_message from='<your-agent-name>' to='<other-agent>' content='...'
```

Include:
- What you discovered
- Changes made
- Issues found
- Questions or blockers
- Next steps or handoff info

### 7. Update Task Status

Mark the task complete:

```
Use update_task with id=X status='completed' updated_by='<your-agent-name>'
```

## Communication Patterns

### When to Send Messages

**Always message when:**
- Completing a task (report findings)
- Finding a blocker (ask for help)
- Making architectural decisions (get input)
- Discovering issues (share knowledge)
- Handing off work (provide context)

**Message format:**
- Use markdown for readability
- Include code snippets when relevant
- Reference file paths and line numbers
- Be concise but complete

### When to Create Tasks

**Create tasks for:**
- Work that needs to be done
- Specific assignments for your pair
- Follow-up work discovered during investigation
- Work you can't complete yourself

**Task format:**
```
Use create_task with
  title='Clear, actionable title'
  description='Detailed description with context and acceptance criteria'
  assigned_to='cursor|claude-code'
  created_by='<your-agent-name>'
```

### Example: Good Communication

**Completing a task:**
```
Use send_message from='claude-code' to='cursor' content='
## Task #5 Complete: Add authentication middleware

Implemented JWT authentication middleware in `internal/middleware/auth.go:1-45`.

**Changes:**
- Added `AuthMiddleware()` function
- Validates JWT tokens from Authorization header
- Returns 401 for invalid/missing tokens
- Adds user context to request

**Testing:**
- Added unit tests in `auth_test.go`
- All tests passing: `go test ./internal/middleware`

**Next steps:**
- Need to integrate into router (see routes.go:12)
- Should we add rate limiting as well?
'
```

## Task Management Patterns

### Task Lifecycle

```
pending → in_progress → completed
                      → cancelled  (driver cancelled the work)
                      → blocked    (waiting on something)
```

- **pending** - Created, not yet started
- **in_progress** - Someone is actively working on it
- **completed** - Work is done and verified
- **cancelled** - The driver cancelled the work (worker should stop)
- **blocked** - Waiting on an external dependency

### Task Assignment

**Assign to specific agent:**
```
Use create_task with assigned_to='cursor' ...
```

**Unassigned task (either can take):**
```
Use create_task with assigned_to='' ...
```

### Task Dependencies

When tasks depend on each other:

1. **Message about dependencies:**
   ```
   "Task #7 is blocked by Task #5. I'll start on something else."
   ```

2. **Include in task description:**
   ```
   "Depends on Task #5 (auth middleware) being completed first."
   ```

## Collaboration Scenarios

### Scenario 1: Code Review

**Cursor creates task:**
```
Use create_task with
  title='Review pull request #123'
  description='Review PR for authentication feature. Check security, error handling, tests.'
  assigned_to='claude-code'
  created_by='cursor'
```

**Claude-code reviews:** (using native tools for diff/review, then)
```
Use send_message from='claude-code' to='cursor' content='Review complete. Found 3 issues...'
Use update_task with id=X status='completed' updated_by='claude-code'
```

### Scenario 2: Parallel Work

**Cursor messages:**
```
Use send_message from='cursor' to='claude-code' content='
I'm working on the frontend (Task #8). Can you handle the backend API (Task #9)?
They're independent, so we can work in parallel.'
```

**Claude-code responds:**
```
Use get_context for 'claude-code'
Use update_task with id=9 status='in_progress' updated_by='claude-code'
Use send_message from='claude-code' to='cursor' content='Taking Task #9, will ping when done.'
```

### Scenario 3: Blocked by Issue

**Claude-code hits blocker:**
```
Use send_message from='claude-code' to='cursor' content='
## Blocked on Task #10

Can't implement the database migration - missing schema definition.

Where should the users table schema live? Should I:
1. Create a new schema file in db/migrations/
2. Use the existing schema in docs/schema.sql
3. Something else?
'
```

**Cursor responds with guidance:**
```
Use send_message from='cursor' to='claude-code' content='
Use db/migrations/ and follow the pattern in 001_initial.sql. Number it 003_users.sql.'
```

### Scenario 4: Driver Cancels Slow Worker

**Worker is taking too long:**
```
# Driver checks worker status
Use worker_status

# No progress update in 5 minutes — cancel the worker
Use cancel_agent agent='claude-code' cancelled_by='cursor' reason='taking too long, will handle it differently'
```

**Worker sees STOP on next tool call:**
```
🛑 STOP: 1 of your task(s) have been cancelled. The driver no longer needs this work.
Stop immediately, call read_messages to see details, and exit.
```

**Worker responds:**
```
Use read_messages for 'claude-code'
# Sees: "cursor has cancelled your work. Reason: taking too long."
# Worker stops all work and exits
```

### Scenario 5: Feature Handoff

**Cursor completes initial work:**
```
Use send_message from='cursor' to='claude-code' content='
## Feature branch ready: feature/auth

I've implemented the basic auth flow in:
- internal/auth/handler.go (login/logout endpoints)
- internal/auth/service.go (business logic)
- internal/auth/models.go (user model)

**Not done yet:**
- Unit tests (need coverage)
- Integration with existing middleware
- Documentation

Created Task #11 for tests. Can you take it?'

Use create_task with
  title='Add tests for auth feature'
  description='Add unit tests for auth handler, service, and models. Target 80%+ coverage.'
  assigned_to='claude-code'
  created_by='cursor'
```

## Best Practices

### DO ✓

- **Check context at session start** - `get_context` first thing
- **Read all unread messages** - Stay synchronized
- **Update task status** - In progress when starting, completed when done
- **Call heartbeat every 60-90s** - With progress description and step info
- **Call report_progress every 2-3 min** - Structured updates with percent and ETA
- **Message with findings** - Report what you learned/did
- **Be specific in messages** - Include file paths, line numbers, error messages
- **Create tasks for follow-up work** - Don't let things get lost
- **Ask questions when blocked** - Don't spin your wheels
- **Coordinate parallel work** - Avoid conflicts
- **Obey STOP signals** - Stop immediately when your work is cancelled

### DON'T ✗

- **Don't work without checking context** - You might miss important messages
- **Don't skip status updates** - Your pair needs to know what's happening
- **Don't work silently for minutes** - 3 min triggers warning, 5 min triggers critical alert, 10 min triggers auto-recovery
- **Don't assume your pair sees your work** - Explicitly communicate findings
- **Don't create duplicate tasks** - Check existing tasks first
- **Don't ignore messages** - Read and acknowledge them
- **Don't ignore STOP signals** - Stop immediately, don't continue cancelled work
- **Don't leave tasks stale** - Update or complete them
- **Don't make big decisions alone** - Discuss architectural changes

## Message Templates

### Completing a Task

```
## Task #X Complete: [title]

**Summary:** [1-2 sentence overview]

**Changes:**
- [file:line] - description
- [file:line] - description

**Testing:** [how you verified it works]

**Issues found:** [any problems discovered]

**Next steps:** [follow-up work needed, if any]
```

### Reporting a Blocker

```
## Blocked on Task #X: [reason]

**Problem:** [clear description of what's blocking you]

**What I tried:**
- [attempt 1]
- [attempt 2]

**Need:** [what you need to proceed]

**Options:**
1. [option 1]
2. [option 2]
```

### Asking for Review

```
## Ready for Review: [feature/change]

**What:** [what you built/changed]

**Where:** [affected files]

**Testing:** [how to test it]

**Questions:**
- [specific question 1]
- [specific question 2]
```

### Handoff

```
## Handing off: [feature/task]

**Status:** [current state]

**Completed:**
- [done item 1]
- [done item 2]

**Remaining:**
- [todo item 1] - details
- [todo item 2] - details

**Context:** [important background info]

**Created Task #X** for next steps.
```

## Tools Quick Reference (23 coordination tools)

| Tool | Purpose |
|------|---------|
| `get_context` | Session context: messages, tasks, presence, notes |
| `set_presence` | Update status and workspace; dynamically changes the server's project context |
| `add_note` | Add shared note or decision |
| `send_message` | Message your pair (optional title, urgent) |
| `read_messages` | Read and mark messages as read |
| `create_task` | Create task (auto-notifies assignee) |
| `list_tasks` | View tasks, filter by assignment/status |
| `update_task` | Update task (auto-notifies on completion; status: pending/in_progress/completed/blocked/cancelled) |
| `create_plan` | Create shared plan |
| `get_plan` | View plan(s); omit ID to list all |
| `update_plan` | Add or update plan items |
| `handoff` | Hand off work with summary and next steps |
| `claim_next` | Claim next task (dry_run to peek) |
| `request_review` | Request code review from pair |
| `lock_file` | Lock, unlock, check, or list file locks (action param) |
| `register_agent` | Register a custom agent for auto-discovery |
| `list_agents` | List all available agents (built-in and registered) |
| `worker_status` | (Driver) List workers: progress, SLA status, process activity, worktrees |
| `heartbeat` | (Workers) Signal liveness every 60-90s; include progress, step, total_steps |
| `report_progress` | (Workers) Structured task progress: description, percent, ETA |
| `cancel_agent` | (Driver) Cancel a worker's tasks, send STOP signal, kill process |
| `get_work_context` | Get task context (relevant files, background, constraints, shared notes) |
| `update_work_context` | Add shared notes to a task's work context |

Use your IDE/CLI native tools for files, search, git, and commands.

### Custom Agent Registration

Any MCP client can join the collaboration by registering as a custom agent:

1. **Register:** `register_agent name='my-bot' display_name='My Bot'`
2. **Set presence with workspace:** `set_presence agent='my-bot' status='working' workspace='/path/to/project'`
3. **Participate:** Use any coordination tool (send_message, create_task, etc.)

Once registered, a custom agent is validated the same way as built-in agents (`cursor`, `claude-code`). Use `list_agents` to discover all agents.

## Notifications and Auto-Response

The MCP server provides two mechanisms to keep agents in sync without polling:

### 1. Piggyback Notifications

Every tool response includes a notification banner when the connected agent has unread messages or pending tasks:

```
🔔 You have 2 unread message(s) and 1 pending task(s). Call read_messages or get_session_context to see them.
```

This banner is appended to successful tool results (suppressed for `read_messages` and `get_session_context` which already display this information).

### 2. STOP Banners (Cancellation)

If the driver cancels a worker's tasks (via `cancel_agent`), the piggyback banner is replaced with a STOP directive:

```
🛑 STOP: 1 of your task(s) have been cancelled. The driver no longer needs this work. Stop immediately, call read_messages to see details, and exit.
```

Workers MUST obey this signal and stop working immediately.

### 3. Auto-Respond (Built into Server)

When the MCP server detects unread messages for an agent that is NOT the currently connected client, it can spawn a configured command to wake up that agent. This replaces any external daemon scripts.

Configure in `mcp/config.yaml`:

```yaml
auto_respond:
  claude-code:
    command: ["claude", "--continue", "-p", "/pair-respond", "--dangerously-skip-permissions"]
    cooldown_seconds: 30
```

- **command**: The command to spawn (e.g., invoke Claude Code with the `/pair-respond` skill)
- **cooldown_seconds**: Minimum time between invocations for the same agent (default: 30)

The auto-responder uses file-based locking to prevent overlapping invocations.

### 4. JSON-RPC Push Notifications

The server also pushes `notifications/pair_update` to stdout when the signal file changes and the connected agent has unread content:

```json
{
  "jsonrpc": "2.0",
  "method": "notifications/pair_update",
  "params": {
    "unread_messages": 2,
    "pending_tasks": 1,
    "summary": "2 new message(s), 1 pending task(s)"
  }
}
```

MCP clients that support notifications can use this to trigger context refresh.

### CLI Status Check

For scripts or debugging, check an agent's unread status:

```bash
mcp-stringwork status claude-code
# Output: unread=2 pending=1
```

## State Persistence

All collaboration state is stored in the state file (default `~/.config/stringwork/state.sqlite`, configurable via `state_file`):
- Messages between agents
- Task list with status
- Read/unread tracking
- Agent presence
- Shared plans

**Important:** This file is the source of truth. Both agents read/write to it through the MCP server.

## Next Steps

- Review [QUICK_REFERENCE.md](./QUICK_REFERENCE.md) for command examples
- Check [SETUP_GUIDE.md](./SETUP_GUIDE.md) if you need to reconfigure
- Read agent-specific instructions in [AGENTS.md](../AGENTS.md) or [CLAUDE.md](../CLAUDE.md)
