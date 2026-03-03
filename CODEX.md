# Codex CLI Instructions

This project includes an MCP pair-programming server. When working on tasks, follow this collaborative workflow.

> **See Also:**
> - [SETUP_GUIDE.md](docs/SETUP_GUIDE.md) - Complete setup instructions
> - [WORKFLOW.md](docs/WORKFLOW.md) - Detailed collaboration patterns
> - [QUICK_REFERENCE.md](docs/QUICK_REFERENCE.md) - Command examples

## Identity

You are `codex` in the pair programming system.

## Agents

Three agents collaborate in this system:
- **`cursor`** — Cursor IDE agent
- **`claude-code`** — Claude Code CLI agent
- **`codex`** (you) — OpenAI Codex CLI agent

All agents share the same state via `~/.config/stringwork/state.sqlite`.

## Before Starting Any Task

1. **Check session context first:**
   ```
   Use get_session_context for 'codex'
   ```
   This shows unread messages, tasks, presence status, and session notes.

2. **Set your presence:**
   ```
   Use set_presence agent='codex' status='working' workspace='/path/to/project'
   ```

3. **Read any unread messages:**
   ```
   Use read_messages for 'codex'
   ```

4. **Check for assigned tasks:**
   ```
   Use list_tasks with assigned_to='codex'
   ```

**Piggyback notifications:** Every tool response automatically includes a banner if you have unread messages or pending tasks. This means you'll discover new messages on your next tool call without explicit polling.

**Auto-respond:** The server can automatically invoke you via `codex exec resume --last` when cursor or claude-code sends you a message. This preserves your conversation context across invocations. Configured in `mcp/config.yaml` under `auto_respond`.

## Working on Tasks

### When You Receive a Task

1. **Check for constraints:**
   ```
   Use get_work_context with task_id=X
   ```
   Tasks may include constraints set by the driver (e.g. "read-only", scoped file lists). When present, you MUST obey them. Most tasks have no constraints.

2. **Claim the task:**
   ```
   Use update_task with id=X, status='in_progress', updated_by='codex'
   ```

3. **Do the work** using your native Codex tools (file read/write, search, git, commands).

4. **Report findings to your pairs:**
   ```
   Use send_message from='codex' to='cursor' with your findings
   ```

5. **Mark task complete:**
   ```
   Use update_task with id=X, status='completed', updated_by='codex'
   ```

### When You Need Help or Input

Send a message to your pairs:
```
Use send_message from='codex' to='cursor' content='Question or blocker description'
Use send_message from='codex' to='claude-code' content='Question or blocker description'
```

### When Creating Work for Your Pairs

```
Use create_task with title='Task description' assigned_to='cursor' created_by='codex'
Use create_task with title='Task description' assigned_to='claude-code' created_by='codex'
```

## Code Review Workflow

When asked to review code, use your native `/review` capability for structured reviews.

### Review Types

1. **Review against a base branch** — diffs your work against its upstream and highlights risks before a PR:
   ```
   /review
   # Then select "Review against a base branch"
   ```

2. **Review uncommitted changes** — inspects staged, unstaged, and untracked files:
   ```
   /review
   # Then select "Review uncommitted changes"
   ```

3. **Review a specific commit** — reads the exact change set for a SHA:
   ```
   /review
   # Then select "Review a commit"
   ```

4. **Custom review instructions** — run a focused review with a specific prompt:
   ```
   /review
   # Then select "Custom review instructions"
   # Example: "Focus on security issues and error handling"
   ```

### Review in Non-Interactive Mode

When auto-spawned for a review task, use `codex exec review` to run a non-interactive code review:
```bash
codex exec review "Focus on security, error handling, and test coverage"
```

### Sending Review Results

After reviewing, always send findings via the MCP coordination tools:
```
Use send_message from='codex' to='cursor' content='
## Code Review Findings

### Critical Issues
1. ...

### Important
2. ...

### Suggestions
3. ...
'
```

## Web Search

Codex has built-in web search enabled by default. Use it when you need:
- Up-to-date API documentation
- Current best practices for a library or framework
- Information about error messages or known issues

Web search runs from a cached index by default. For live results, the server can launch Codex with `--search` or set `web_search = "live"` in config.

## Image Inputs

When your pairs share screenshots or design specs, you can process them:
```bash
codex -i screenshot.png "Implement this design"
codex --image mockup.png,spec.png "Build the UI from these specs"
```

## Session Context Preservation

Your sessions are stored locally at `~/.codex/sessions/`. The auto-respond system uses `resume --last` to maintain context across invocations, so you accumulate knowledge about:
- Prior tasks and decisions
- Code you've reviewed or written
- Project architecture understanding
- Conversation history with your pairs

To manually resume a specific session:
```bash
codex resume                    # picker of recent sessions
codex resume --last             # most recent session
codex resume <SESSION_ID>       # specific session
```

## Available MCP Tools (24 coordination tools)

- `get_session_context` - Full session context (messages, tasks, presence, notes)
- `set_presence` - Update status and workspace
- `append_session_note` - Add shared note or decision
- `send_message` - Message your pairs
- `read_messages` - Read messages
- `create_task` - Create task (auto-notifies assignee; supports requires_review for quality gates)
- `list_tasks` - List tasks (shows dependency status, review gates, failure info)
- `update_task` - Update task (auto-notifies on completion; supports review_status for approval gates)
- `replay_task` - Reset failure tracking and re-queue a blocked/pending task (DLQ recovery)
- `create_plan` - Create shared plan
- `get_plan` - View plan(s); omit ID to list all
- `update_plan` - Add or update plan items
- `handoff` - Hand off work with summary and next steps
- `claim_next` - Claim next task (dry_run to peek)
- `report_progress` - Report structured progress on a task
- `request_review` - Request code review from pair
- `lock_file` - Lock, unlock, check, or list file locks
- `register_agent` - Register a custom agent
- `list_agents` - List all available agents
- `worker_status` - (Driver) List workers with progress, process activity, SLA status
- `heartbeat` - (Workers) Signal liveness with progress info
- `cancel_agent` - (Driver) Cancel a worker's tasks, send STOP signal, and kill its process
- `get_work_context` - Get task context (relevant files, background, constraints, shared notes)
- `update_work_context` - Add shared notes to a task's work context

Use your native Codex tools for files, search, git, web search, and commands.

## Approval Modes

When running interactively, control what Codex can do:
- **Auto** (default) — read, edit, and run commands within the workspace
- **Read-only** — browse files only, no changes without approval
- **Full Access** — full machine access including network (use sparingly)

When auto-spawned, `--full-auto` is used (workspace-write sandbox).

## Fully Autonomous Mode

For hands-off collaboration:

1. Call `claim_next agent='codex' dry_run=true` to see what to do
2. Call `claim_next agent='codex'` to claim the task
3. Do the work
4. Call `update_task id=X status='completed'` or `handoff` when done
5. Repeat!

## Setup

### Install Codex CLI

```bash
npm install -g @openai/codex
# or
brew install --cask codex
```

### Add the MCP server (stdio transport)

```bash
codex mcp add stringwork -- /path/to/mcp-stringwork
```

Or for HTTP transport (requires `http_port: 8943` and daemon mode for a stable URL):

```bash
codex mcp add stringwork -- npx -y mcp-remote http://localhost:8943/mcp
```

### Verify MCP connection

```bash
codex mcp list
# Should show: stringwork
```

## Important

- Always check `get_session_context` at the start of a session
- Communicate findings via `send_message` — your pairs can't see your work otherwise
- Update task status so your pairs know what's in progress
- Use `/review` for structured code reviews — it's your strongest differentiator
- Use web search when you need current information
- The collaboration state is stored globally at `~/.config/stringwork/state.sqlite` (shared across all agents on this machine)

## Getting Help

- **Comprehensive guides:** See [docs/](docs/) directory
- **Command examples:** [QUICK_REFERENCE.md](docs/QUICK_REFERENCE.md)
- **Your pairs:** Message cursor or claude-code with questions
- **State file:** `~/.config/stringwork/state.sqlite` (global, shared across all agents)
