# Claude Code MCP Configuration

MCP for Claude Code is configured using the **`claude` CLI** (not by editing config files by hand). Config is stored in `~/.claude.json`.

## How it works

Workers (Claude Code) are **spawned automatically** by the Stringwork server when there's pending work. The server auto-injects the MCP config for spawned workers, so **no manual setup is needed for worker processes**.

Manual configuration is only needed if you want to run Claude Code interactively (not as a spawned worker).

## Claude CLI commands for MCP

| Command | Purpose |
|---------|---------|
| `claude mcp add-json --scope user <name> '<json>'` | Add or update an MCP server (user scope = all projects) |
| `claude mcp list` | List configured MCP servers |
| `claude mcp remove --scope user <name>` | Remove an MCP server |

Use `claude mcp --help` for more options (e.g. `--scope project` for current project only).

## Manual setup (interactive use)

If you want to use Claude Code interactively alongside a running Stringwork server, you need a **fixed** `http_port` in your config (e.g. `http_port: 8943`) so the URL is predictable.

```bash
claude mcp add-json --scope user stringwork '{
  "type": "url",
  "url": "http://localhost:8943/mcp"
}'
```

Verify with `claude mcp list`. Restart Claude Code to load.

With `http_port: 0` (auto-assign), the port changes each time Cursor starts, making manual setup impractical.

## Per-project configuration

- Point `MCP_CONFIG` to a project-specific file so `workspace_root` and other options match the project.
- Change workspace at runtime: `set_presence agent='claude-code' status='working' workspace='/path/to/project'`.

## Available tools (23)

| Tool | Purpose |
|------|---------|
| `get_context` | Session context: messages, tasks, presence, notes |
| `set_presence` | Update status and workspace |
| `add_note` | Add shared note or decision |
| `send_message` | Message your pair |
| `read_messages` | Read and mark messages as read |
| `create_task` | Create task with optional work context |
| `list_tasks` | List tasks, filter by assignment/status |
| `update_task` | Update task status, assignment, priority |
| `create_plan` | Create shared plan |
| `get_plan` | View plan(s); omit ID to list all |
| `update_plan` | Add or update plan items |
| `handoff` | Hand off work with summary and next steps |
| `claim_next` | Claim next task (dry_run to peek) |
| `request_review` | Request code review from pair |
| `lock_file` | Lock, unlock, check, or list file locks |
| `register_agent` | Register a custom agent |
| `list_agents` | List all agents (built-in and registered) |
| `worker_status` | Live view of workers |
| `heartbeat` | Signal liveness |
| `report_progress` | Report progress on task |
| `cancel_agent` | Cancel a worker |
| `get_work_context` | Get task context |
| `update_work_context` | Add notes to task context |

## Pair programming workflow

- Start: `get_context` for `'claude-code'`
- Check tasks: `list_tasks` with `assigned_to='claude-code'`
- Claim: `update_task` with `id=X` `status='in_progress'` `updated_by='claude-code'`
- Report: `send_message` from `'claude-code'` to `'cursor'` with your summary
- Complete: `update_task` with `id=X` `status='completed'` `updated_by='claude-code'`

## Notifications

- **Piggyback:** Tool responses include a banner when you have unread messages or pending tasks.
- **Auto-respond:** Configure `auto_respond` in config so the server can spawn Claude Code when it has unread content.

## CLI status

```bash
mcp-stringwork status claude-code
# Output: unread=N pending=N
```

## Troubleshooting

- **Tool not found:** Restart Claude Code after config changes; check JSON and binary path. Use `claude mcp list` to confirm.
- **Path outside workspace:** Set workspace via `set_presence agent='claude-code' status='working' workspace='/correct/project/path'`.
