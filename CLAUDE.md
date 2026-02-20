# Claude Code Instructions

This project includes an MCP pair-programming server. When working on tasks, follow this collaborative workflow.

> **📚 See Also:**
> - [SETUP_GUIDE.md](docs/SETUP_GUIDE.md) - Complete setup instructions
> - [WORKFLOW.md](docs/WORKFLOW.md) - Detailed collaboration patterns
> - [QUICK_REFERENCE.md](docs/QUICK_REFERENCE.md) - Command examples

## Identity

You are `claude-code` in the pair programming system.

## Before Starting Any Task

1. **Check session context first:**
   ```
   Use get_session_context for 'claude-code'
   ```
   This shows unread messages, tasks, presence status, and session notes.

2. **Set your presence:**
   ```
   Use set_presence agent='claude-code' status='working'
   ```
   Cursor dynamically sets the workspace via its own `set_presence workspace='/path/to/project'`, which updates the server's workspace root at runtime. When auto-spawned, you inherit the workspace directory automatically. You can also set it yourself if needed:
   ```
   Use set_presence agent='claude-code' status='working' workspace='/path/to/project'
   ```

3. **Read any unread messages:**
   ```
   Use read_messages for 'claude-code'
   ```

4. **Check for assigned tasks:**
   ```
   Use list_tasks with assigned_to='claude-code'
   ```

**Piggyback notifications:** Every tool response automatically includes a banner if you have unread messages or pending tasks (e.g. `🔔 You have 2 unread message(s). Call read_messages to see them.`). This means you'll discover new messages on your next tool call without explicit polling. The banner is suppressed on `read_messages` and `get_session_context` since they already show this information.

**Auto-respond:** The server can automatically invoke you via `claude --continue -p "/pair-respond"` when cursor sends you a message. The `--continue` flag preserves your conversation context across auto-respond invocations. This is configured in `mcp/config.yaml` under `auto_respond`. When auto-spawned, follow the instructions in `.claude/commands/pair-respond.md`.

## Working on Tasks

### When You Receive a Task

1. **Claim the task:**
   ```
   Use update_task with id=X, status='in_progress', updated_by='claude-code'
   ```

2. **Do the work** using your native Claude Code tools (file read/write, search, git, commands).

3. **MANDATORY: Report progress every 2-3 minutes** using `report_progress`:
   ```
   Use report_progress agent='claude-code' task_id=X description='Implemented auth middleware, now writing tests (12/15 passing). ~2 min left.' percent_complete=70 eta_seconds=120
   ```

4. **MANDATORY: Call heartbeat every 60-90 seconds** with progress info:
   ```
   Use heartbeat agent='claude-code' progress='writing unit tests for auth' step=3 total_steps=5
   ```

5. **Report findings to your pair:**
   ```
   Use send_message from='claude-code' to='cursor' with your findings
   ```

6. **Mark task complete:**
   ```
   Use update_task with id=X, status='completed', updated_by='claude-code'
   ```

### Progress Reporting (MANDATORY — server-enforced)

The server actively monitors your progress. **Failure to report triggers escalating alerts:**

| Time without `report_progress` | What happens |
|-------------------------------|-------------|
| 3 minutes | ⚠️ WARNING sent to driver |
| 5 minutes | 🔴 CRITICAL alert sent to driver |
| 10 minutes (no activity) | 🔧 Task auto-recovered, you may be cancelled |

**You MUST call these tools while working:**

1. `heartbeat` — every 60-90 seconds with `progress` description
2. `report_progress` — every 2-3 minutes with task_id, description, percent_complete, eta_seconds

**Example progress loop during work:**
```
# After claiming task, every 60-90 seconds:
Use heartbeat agent='claude-code' progress='reading codebase, understanding auth flow' step=1 total_steps=4

# Every 2-3 minutes, structured progress:
Use report_progress agent='claude-code' task_id=5 description='Auth middleware implemented. Writing unit tests (8/15 done). Fixing edge cases next.' percent_complete=50 eta_seconds=180
```

The driver can see all this in `worker_status` — it shows your progress, last report time, process activity, and SLA status.

### Handling Cancellation (STOP signals)

The driver can cancel your work at any time using `cancel_agent`. When this happens:

1. **Piggyback STOP banner** — you will see this on your very next tool call:
   ```
   🛑 STOP: 1 of your task(s) have been cancelled. The driver no longer needs this work. Stop immediately, call read_messages to see details, and exit.
   ```

2. **STOP message** — a message is sent to your inbox explaining the cancellation.

**When you see a STOP signal:**
- **Stop all current work immediately**
- Call `read_messages` to understand why
- Do NOT continue working on cancelled tasks
- Exit cleanly

### When You Need Help or Input

Send a message to your pair:
```
Use send_message from='claude-code' to='cursor' content='Question or blocker description'
```

### When Creating Work for Your Pair

```
Use create_task with title='Task description' assigned_to='cursor' created_by='claude-code'
```

## Code Review Workflow

When asked to review code:

1. Use your native tools to see git status, diff, and file content
2. Send findings via `send_message`

## Driver / Worker Mode

When the server is configured with `orchestration` (in `mcp/config.yaml`), one agent is the **driver** and others are **workers**.

- **Driver** (e.g. cursor): Creates tasks with `assigned_to='any'` so the server auto-assigns to workers. Use `worker_status` to see real-time progress, process activity, and SLA status. Set `expected_duration_seconds` on tasks for SLA monitoring. Use `cancel_agent` to stop stuck workers. Can also do work (hybrid).
- **Workers** (e.g. claude-code-1, codex, gemini): Use `claim_next` to get tasks, `heartbeat` every 60-90 seconds with progress info, `report_progress` every 2-3 minutes. **The server monitors these — missing reports trigger escalating alerts to the driver (3 min warning, 5 min critical, 10 min auto-recovery).** Report to the driver via `send_message`. **Obey STOP signals immediately.**

If no orchestration is configured, all agents are peers (legacy mode).

## Available MCP Tools (24 coordination tools)

- `get_session_context` - Full session context (messages, tasks, presence, notes)
- `set_presence` - Update status and workspace; dynamically changes the server's project context
- `append_session_note` - Add shared note or decision
- `send_message` - Message your pair (optional title, urgent)
- `read_messages` - Read messages
- `create_task` - Create task (use assigned_to='any' for auto-assign; optional relevant_files, background, constraints for work context)
- `list_tasks` - List tasks
- `update_task` - Update task (auto-notifies on completion; status can be: pending, in_progress, completed, blocked, cancelled)
- `create_plan` - Create shared plan
- `get_plan` - View plan(s); omit ID to list all
- `update_plan` - Add or update plan items
- `handoff` - Hand off work with summary and next steps
- `claim_next` - Claim next task (dry_run to peek)
- `report_progress` - Report structured progress on a task (MANDATORY every 2-3 min while working)
- `request_review` - Request code review from pair
- `lock_file` - Lock, unlock, check, or list file locks (action param)
- `register_agent` - Register a custom agent for auto-discovery
- `list_agents` - List all available agents (built-in and registered)
- `worker_status` - (Driver) List workers with progress, process activity, SLA status
- `heartbeat` - (Workers) Signal liveness with progress info; call every 60-90 seconds
- `cancel_agent` - (Driver) Cancel a worker's tasks, send STOP signal, and kill its process
- `get_work_context` - Get task context (relevant files, background, constraints, shared notes)
- `update_work_context` - Add shared notes to a task's work context
- `query_knowledge` - Search the FTS5-powered project knowledge base

Use your native tools for files, search, git, and commands.

## Agents

- **`cursor`** — Cursor IDE agent (often the driver)
- **`claude-code`** (you) — Claude Code CLI agent (worker; may be claude-code-1, claude-code-2 when multiple instances)
- **`codex`** — OpenAI Codex CLI agent (worker)
- **`gemini`** — Google Gemini CLI agent (worker)

All agents share the same state via `~/.config/stringwork/state.sqlite`. When orchestration is enabled, the driver is configured in `mcp/config.yaml` under `orchestration.driver`.

## Important

- Always check `get_session_context` at the start of a session
- Communicate findings via `send_message` - your pairs can't see your work otherwise
- Update task status so your pairs know what's in progress
- You can delegate to `cursor` or `codex` via `create_task`
- The collaboration state is stored globally at `~/.config/stringwork/state.sqlite` (shared across all agents on this machine)

## Shared Planning Workflow

For complex features, use shared plans:

1. **Create plan**: `create_plan id='feature-x' title='...' goal='...'`
2. **Add items**: `update_plan action='add_item' id='1' title='...' owner='claude-code'`
3. **Claim work**: `update_plan action='update_item' item_id='2' status='in_progress' owner='claude-code'`
4. **Report progress**: `update_plan action='update_item' item_id='1' status='completed' add_note='Done'`
5. **Check status**: `get_plan` to see overall progress

Both agents work from the same plan, staying in sync automatically.

## Fully Autonomous Mode

For hands-off pair programming:

1. Call `claim_next agent='claude-code' dry_run=true` to see what to do
2. Call `claim_next agent='claude-code'` to claim the task
3. Do the work
4. Call `update_task id=X status='completed'` or `handoff` when done
5. Repeat!

### Autonomous Tools
- `claim_next` (with dry_run) - What should I do next?
- `claim_next` - Grab next available task
- `handoff` - Pass work to partner with context
- `create_task` - Create task for partner (auto-notifies)
- `update_task status='completed'` - Mark task done (auto-notifies creator)
- `request_review` - Ask partner for code review

## Common Pitfalls

### ❌ DON'T: Start work without checking context
```
# Bad: Immediately start working without checking context
```

### ✅ DO: Check context first
```
# Good: Check for messages and tasks first
Use get_session_context for 'claude-code'
Use list_tasks with assigned_to='claude-code'
```

### ❌ DON'T: Work silently for a long time
```
# Bad: No heartbeat or report_progress for 3+ minutes
# The server will send WARNING (3 min), CRITICAL (5 min), and auto-recover your task (10 min)
```

### ✅ DO: Report progress continuously while working
```
# Good: Heartbeat every 60-90 seconds
Use heartbeat agent='claude-code' progress='implementing JWT validation' step=2 total_steps=4

# Good: Structured progress every 2-3 minutes
Use report_progress agent='claude-code' task_id=5 description='JWT validation done. Writing middleware integration. 3 tests passing.' percent_complete=60 eta_seconds=120
```

### ❌ DON'T: Ignore STOP signals
```
# Bad: See a STOP banner but keep working
# Your tasks have been cancelled — continuing wastes effort
```

### ✅ DO: Stop immediately on cancellation
```
# Good: Obey the STOP signal
Use read_messages for 'claude-code'
# Then stop working and exit cleanly
```

### ❌ DON'T: Complete work without communicating
```
# Bad: Finish task silently
Use update_task id=5 status='completed'
```

### ✅ DO: Report findings with details
```
# Good: Send detailed report
Use send_message from='claude-code' to='cursor' content='
## Task #5 Complete: Add authentication

**Summary:** Implemented JWT authentication middleware

**Changes:**
- internal/middleware/auth.go:1-45 - JWT validation middleware
- internal/auth/service.go:67-89 - Token generation
- Added unit tests with 85% coverage

**Testing:** All tests passing (`go test ./...`)

**Notes:** Consider adding rate limiting next
'
Use update_task id=5 status='completed' updated_by='claude-code'
```

### ❌ DON'T: Ignore unread messages
```
# Bad: Skip reading messages
Use list_tasks  # Missing important context!
```

### ✅ DO: Read messages before proceeding
```
# Good: Stay synchronized
Use get_session_context for 'claude-code'  # Includes messages
Use list_tasks with assigned_to='claude-code'
```

## Quick Setup Check

If MCP tools aren't working:

1. **Verify MCP server is registered:**
   ```bash
   claude mcp list
   # Should show: stringwork: /path/to/mcp-stringwork - ✓ Connected
   ```

2. **If not registered, add it with the `claude` CLI:**
   ```bash
   claude mcp add-json --scope user stringwork '{"type":"stdio","command":"/path/to/mcp-stringwork","args":[],"env":{"MCP_CONFIG":"/path/to/config.yaml"}}'
   ```
   (Use `claude mcp remove --scope user stringwork` to remove it.)

3. **Restart Claude Code CLI** (quit and relaunch)
4. **Test connection:** Try `Use get_session_context for 'claude-code'`
5. **See full setup:** [docs/SETUP_GUIDE.md](docs/SETUP_GUIDE.md)

## Getting Help

- **Comprehensive guides:** See [docs/](docs/) directory
- **Command examples:** [QUICK_REFERENCE.md](docs/QUICK_REFERENCE.md)
- **Your pair:** Message cursor with questions
- **State file:** `~/.config/stringwork/state.sqlite` (global, shared across all agents)

## Code Review Best Practices

When reviewing code (a common task for claude-code):

1. **Be thorough:**
   - Check for security issues (injection, XSS, auth bypass)
   - Verify error handling
   - Look for race conditions
   - Review test coverage

2. **Be specific:**
   - Reference exact file paths and line numbers
   - Quote problematic code snippets
   - Suggest concrete improvements

3. **Prioritize findings:**
   - Critical: Security, data loss, crashes
   - Important: Performance, maintainability
   - Nice-to-have: Style, documentation

4. **Example review message:**
   ```
   Use send_message from='claude-code' to='cursor' content='
   ## Code Review: auth feature branch

   ### Critical Issues
   1. **SQL Injection** (internal/db/user.go:45)
      - Direct string interpolation in query
      - Use parameterized queries instead

   ### Important
   2. **Missing error handling** (internal/auth/handler.go:23)
      - JWT parsing errors not handled
      - Could panic on malformed input

   ### Suggestions
   3. **Test coverage** - auth_test.go missing edge cases
      - Add tests for expired tokens
      - Add tests for malformed JWTs

   Let me know if you want me to fix #1 and #2!
   '
   ```
