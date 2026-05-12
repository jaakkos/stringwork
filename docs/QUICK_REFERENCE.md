# Quick Reference

Command examples for the MCP stringwork coordination tools (24 tools). Use your native IDE/CLI tools for files, search, git, and terminal.

## Session & context

### Get context

```
Use get_context for 'claude-code'
Use get_context for 'cursor'
```

Returns: unread messages summary, assigned tasks, presence, session notes.

### Set presence (with dynamic workspace)

```
Use set_presence agent='cursor' status='working' workspace='/path/to/project'
Use set_presence agent='claude-code' status='working'
Use set_presence agent='cursor' status='idle' note='Waiting for review'
```

The `workspace` parameter dynamically updates the server's workspace root at runtime. When set:
- File path validation follows the new workspace
- Project detection (`get_session_context`) reflects the new project
- Auto-spawned agents use the new directory as their working directory

Set `workspace` when starting a session or switching to a different project.

Optional: `include_all=true` to list all agents' presence.

### Multiple Claude sessions (Git worktrees)

**Manual:** Run several Claude Code sessions in parallel, each on its own branch:

```
claude -w feature-1    # terminal 1
claude -w feature-2    # terminal 2
```

**Spawned workers:** In config, set `use_claude_worktree: true` on claude-code workers so the server injects `-w <instance_id>` (e.g. `claude-code-1`). Worktrees live under `.claude/worktrees/`; no branch or file conflicts. See [SETUP_GUIDE](SETUP_GUIDE.md) for details.

### Add note

```
Use add_note author='claude-code' content='Decision: use JWT for auth' category='decision'
Use add_note author='cursor' content='Blocker: waiting on API key' category='blocker'
```

Categories: `decision`, `note`, `question`, `blocker`.

## Messaging

### Send message

```
Use send_message from='claude-code' to='cursor' content='Your message here'
Use send_message from='cursor' to='claude-code' content='Your message here'
```

Optional: `title='Subject'`, `urgent=true`. Send to all: `to='all'`.

### Read messages

```
Use read_messages for 'claude-code'
Use read_messages for 'cursor'
```

Marks messages as read and returns full content.

## Tasks

### List tasks

```
Use list_tasks
Use list_tasks with assigned_to='claude-code'
Use list_tasks with status='pending'
```

### Create task

```
Use create_task with
  title='Add unit tests for auth module'
  description='Write unit tests covering login, logout, token validation.'
  assigned_to='claude-code'
  created_by='cursor'
```

Assignee is notified automatically.

### Update task

```
Use update_task with id=5 status='in_progress' updated_by='claude-code'
Use update_task with id=5 status='completed' updated_by='claude-code'
Use update_task with id=5 status='cancelled' updated_by='cursor'
```

Completing a task notifies the creator automatically. Task status can be: `pending`, `in_progress`, `completed`, `blocked`, `cancelled`.

### Create task with review gate

```
Use create_task with
  title='Security audit for auth module'
  assigned_to='claude-code'
  created_by='cursor'
  requires_review=true
```

When `requires_review=true`, completion is blocked until a non-assignee approves.

### Approve / reject review

```
Use update_task with id=5 review_status='approved' updated_by='cursor'
Use update_task with id=5 review_status='rejected' updated_by='cursor'
```

Only non-assignees can approve. Rejection sets status back to `pending`.

### Replay a blocked task (DLQ recovery)

```
Use replay_task with id=5 updated_by='cursor'
Use replay_task with id=5 updated_by='cursor' reassign_to='codex'
Use replay_task with id=5 updated_by='cursor' reassign_to='keep'
```

Resets failure count, clears blocked state, and re-queues the task. Default: reassign to `any`. Use `keep` to preserve the current assignee.

## Plans

### Create plan

```
Use create_plan id='feature-x' title='Auth feature' goal='Implement JWT auth' created_by='cursor'
```

### Get plan

```
Use get_plan
Use get_plan id='feature-x'
```

Omit `id` to list all plans.

### Update plan (add or update items)

Add item (optional: reasoning, acceptance list, constraints list):

```
Use update_plan plan_id='feature-x' action='add_item' id='1' title='Add middleware' owner='claude-code'
# With done-when criteria:
Use add_plan_item plan_id='feature-x' id='1' title='JWT middleware' added_by='cursor' acceptance='["Validates signature","401 on invalid token"]' reasoning='Middleware keeps handlers clean'
```

Update item (optional: reasoning, acceptance, constraints):

```
Use update_plan plan_id='feature-x' action='update_item' item_id='2' status='in_progress' owner='claude-code'
Use update_plan_item plan_id='feature-x' item_id='1' status='completed' add_note='Done' updated_by='cursor'
# Set acceptance criteria later:
Use update_plan_item plan_id='feature-x' item_id='1' acceptance='["Tests pass","Docs updated"]' updated_by='cursor'
```

## Workflow

### Handoff

```
Use handoff from='cursor' to='claude-code' summary='Auth middleware done' next_steps='Add tests and wire into router'
```

### Claim next task

See what's next (dry run):

```
Use claim_next agent='claude-code' dry_run=true
```

Claim the task:

```
Use claim_next agent='claude-code'
```

### Request review

```
Use request_review from='cursor' to='claude-code' files='["internal/auth/handler.go"]' description='Review JWT handling'
```

## File locks

Single tool with `action`: lock, unlock, check, list.

```
Use lock_file action='lock' agent='cursor' path='internal/config.go' reason='Editing config'
Use lock_file action='unlock' agent='cursor' path='internal/config.go'
Use lock_file action='check' path='internal/config.go'
Use lock_file action='list' agent='cursor'
```

## Cancellation

### Cancel an agent (driver only)

```
Use cancel_agent agent='claude-code' cancelled_by='cursor' reason='no longer needed'
Use cancel_agent agent='codex' cancelled_by='cursor' reason='taking too long'
```

This does three things in one call:
1. Cancels all in-progress tasks for the agent
2. Sends a STOP message to the agent
3. Kills the spawned worker process (if running)

The agent will see a `🛑 STOP` banner on its next tool call.

## Progress monitoring

### Heartbeat (REQUIRED for workers, every 60–90 seconds)

```
Use heartbeat agent='claude-code'
Use heartbeat agent='claude-code' progress='Implementing auth middleware' step=2 total_steps=5
Use heartbeat agent='codex' progress='Running test suite'
Use heartbeat agent='claude-code' progress='starting work' session_id='YOUR_CLI_SESSION_ID'
```

Parameters:
- `agent` (required) — Your agent name
- `progress` (optional) — Short description of what you're doing now
- `step` / `total_steps` (optional) — Current step number and total steps
- `session_id` (optional) — Your CLI session/conversation ID. Report on first heartbeat so the server can resume your session if you get restarted.

Missing heartbeats trigger: 4 min warning, 7 min critical alert, 14 min auto-recovery.

**Session continuation:** When a stuck worker is cancelled and respawned, the server injects `--resume` (Claude/Gemini) or `--session` (Codex) with the stored session ID, so the new process continues the previous conversation context.

**CLI equivalent:**
```bash
mcp-stringwork heartbeat --agent claude-code --progress 'starting' --session-id YOUR_SESSION_ID
```

### Report progress (REQUIRED for workers, every 2–3 minutes)

```
Use report_progress agent='claude-code' task_id=5 description='Auth middleware done, writing unit tests' percent_complete=60
Use report_progress agent='claude-code' task_id=5 description='Tests passing, writing docs' percent_complete=85 eta_seconds=120
```

Parameters:
- `agent` (required) — Your agent name
- `task_id` (required) — The task you're working on
- `description` (required) — What you've done / are doing
- `percent_complete` (optional, 0–100) — Estimated completion percentage
- `eta_seconds` (optional) — Estimated seconds until done

This also refreshes your heartbeat automatically.

### Worker status (driver view)

```
Use worker_status
```

Shows: each worker's agent progress, task progress (with SLA status), process activity (runtime, output bytes), and worktree info.

### Task SLAs

Set expected duration when creating tasks:

```
Use create_task title='Code review' assigned_to='any' created_by='cursor' expected_duration_seconds=300
```

The watchdog alerts the driver if a task exceeds its SLA.

### Send informal progress (alternative)

```
Use send_message from='claude-code' to='cursor' content='Task #5 progress: middleware done, writing tests now. ~2 min left.'
```

For quick updates that don't need structured tracking. Workers should still prefer `report_progress` for formal tracking.

## Common workflows

### Start of session

```
Use get_context for 'cursor'
Use set_presence agent='cursor' status='working' workspace='/path/to/project'
Use list_tasks with assigned_to='cursor'
```

### Switch to a different project

```
Use set_presence agent='cursor' status='working' workspace='/path/to/other/project'
```

The server's workspace root updates immediately — no restart needed.

### Complete and report

```
Use send_message from='claude-code' to='cursor' content='Task #5 done. Summary: ...'
Use update_task with id=5 status='completed' updated_by='claude-code'
```

### Coordinate in parallel

```
# Cursor
Use send_message from='cursor' to='claude-code' content='I take Task #7, you take #8'
Use update_task with id=7 status='in_progress' updated_by='cursor'

# Claude Code
Use get_context for 'claude-code'
Use update_task with id=8 status='in_progress' updated_by='claude-code'
Use send_message from='claude-code' to='cursor' content='Taking Task #8.'
```

### Shared plan

```
Use create_plan id='auth' title='Auth' goal='JWT auth' created_by='cursor'
Use update_plan plan_id='auth' action='add_item' id='1' title='Middleware' owner='claude-code'
Use update_plan plan_id='auth' action='update_item' item_id='1' status='in_progress' owner='claude-code'
Use get_plan id='auth'
```

## Agent registration

### Register a custom agent

```
Use register_agent name='my-bot' display_name='My Bot' capabilities='["testing","code-review"]'
Use register_agent name='my-bot' workspace='/path/to/project' project='my-project'
```

Once registered, a custom agent can use all coordination tools (messaging, tasks, plans, etc.) just like `cursor` and `claude-code`.

### List all agents

```
Use list_agents
Use list_agents include_builtin=false
Use list_agents active_only=true
```

### Custom agent workflow

```
# 1. Register
Use register_agent name='my-bot'

# 2. Set presence with workspace
Use set_presence agent='my-bot' status='working' workspace='/path/to/project'

# 3. Use any coordination tool
Use send_message from='my-bot' to='cursor' content='Hello from custom agent'
Use create_task title='Automated lint check' created_by='my-bot' assigned_to='cursor'
Use read_messages for='my-bot'
```

## Parameter reference

| Parameter   | Type    | Example                          |
|------------|---------|----------------------------------|
| `agent`    | string  | `'cursor'`, `'claude-code'`      |
| `status`   | string  | `'pending'`, `'in_progress'`, `'completed'` |
| `assigned_to` | string | `'cursor'`, `'claude-code'`, `''` |
| `workspace` | string | `'/path/to/project'` (dynamic, updates server's workspace root) |
| `action`   | string  | `'lock'`, `'unlock'`, `'check'`, `'list'` (lock_file); `'add_item'`, `'update_item'` (update_plan) |
| `path`     | string  | File path (lock_file, relative to workspace) |

## CLI Commands

### Status check

```bash
# Check unread/pending for claude-code (default)
mcp-stringwork status

# Check for a specific agent
mcp-stringwork status claude-code
mcp-stringwork status cursor

# Output format: unread=N pending=N
```

### Agent auto-discovery

```bash
# Scan PATH for known agent CLIs (claude, codex, gemini)
mcp-stringwork discover

# Outputs a table of found agents with versions and suggested config
```

### Audit log

```bash
# View recent audit entries
mcp-stringwork audit

# Filter by agent or tool
mcp-stringwork audit --agent cursor
mcp-stringwork audit --tool create_task

# Last N entries
mcp-stringwork audit --limit 50
```

### Constitution (layered worker rules)

```bash
# Scaffold the per-user constitution dir (~/.config/stringwork/constitution)
mcp-stringwork constitution init

# Preview the resolved preamble for a default-scope task
mcp-stringwork constitution show

# Preview with scope filters (review-only sources, role-targeted sources)
mcp-stringwork constitution show --task-kind review
mcp-stringwork constitution show --agent claude-code

# Inline the full file contents (mirrors what is injected at spawn time)
mcp-stringwork constitution show --inline

# Pull every configured 'git'-typed source into its cache_dir
mcp-stringwork constitution sync
mcp-stringwork constitution sync regfin-rules    # single source by name

# Validate every configured source (path exists, files match include globs,
# git remote responds). Exit non-zero only on hard errors; warnings allow 0.
mcp-stringwork constitution doctor
```

See [CONSTITUTION.md](CONSTITUTION.md) for the file format and resolution
order, and [TEAM_RULES.md](TEAM_RULES.md) for shipping team-wide rules from a
shared devtools repo.

### Server mode

```bash
# Start server (auto-detects daemon/proxy/standalone)
mcp-stringwork

# Force daemon mode (background process, serves multiple Cursor windows)
mcp-stringwork --daemon

# Force standalone mode (legacy single-process, no daemon)
mcp-stringwork --standalone

# With custom config
MCP_CONFIG=/path/to/config.yaml mcp-stringwork
```

### Daemon management

```bash
# Check if daemon is running
ls ~/.config/stringwork/daemon.pid && echo "Daemon PID: $(cat ~/.config/stringwork/daemon.pid)"

# Find dashboard URL
grep "Dashboard:" ~/.config/stringwork/mcp-stringwork.log | tail -1

# Stop daemon (Cursor will restart it on next MCP call)
kill "$(cat ~/.config/stringwork/daemon.pid)"
```

## Auto-Respond Configuration

Configure in `mcp/config.yaml`:

```yaml
auto_respond:
  claude-code:
    command: ["claude", "--continue", "-p", "/pair-respond", "--dangerously-skip-permissions"]
    cooldown_seconds: 30
```

- **command**: Command to spawn when agent has unread messages
- **cooldown_seconds**: Minimum time between invocations (default: 30)

The auto-responder is built into the MCP server—no external daemon required.

## Piggyback Notifications

Every tool response includes a banner when you have unread content:

```
🔔 You have 2 unread message(s) and 1 pending task(s). Call read_messages or get_session_context to see them.
```

If your tasks have been cancelled, the banner is replaced with a STOP directive:

```
🛑 STOP: 1 of your task(s) have been cancelled. The driver no longer needs this work. Stop immediately, call read_messages to see details, and exit.
```

Suppressed for `read_messages` and `get_session_context` which already show this info.

## See Also

- [SETUP_GUIDE.md](./SETUP_GUIDE.md) - Setup instructions
- [WORKFLOW.md](./WORKFLOW.md) - Collaboration patterns
- [AGENTS.md](../AGENTS.md) - Cursor instructions
- [CLAUDE.md](../CLAUDE.md) - Claude Code instructions
