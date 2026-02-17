# Cursor MCP Configuration

## Setup

Cursor starts the Stringwork server as a subprocess. The server handles stdio for Cursor (driver) and runs an HTTP listener for workers and the dashboard in the background.

**No daemon or background process needed.**

### Project-specific (`.cursor/mcp.json` in your project)

```json
{
  "mcpServers": {
    "stringwork": {
      "command": "mcp-stringwork",
      "env": {
        "MCP_CONFIG": "/path/to/config.yaml"
      }
    }
  }
}
```

### Global (`~/.cursor/mcp.json`)

```json
{
  "mcpServers": {
    "stringwork": {
      "command": "mcp-stringwork",
      "env": {
        "MCP_CONFIG": "~/.config/stringwork/config.yaml"
      }
    }
  }
}
```

If `mcp-stringwork` is not on your PATH, use the full path (e.g. `~/.local/bin/mcp-stringwork`).

### Multiple Cursor windows

With `http_port: 0` (default), each Cursor window spawns its own server on an auto-assigned port. All instances share the same SQLite state, so tasks and messages work across windows.

Set a fixed `http_port` (e.g. 8943) only if you need a predictable dashboard URL -- but only one Cursor instance can run at a time with a fixed port.

## Available tools (23)

| Tool | Purpose |
|------|---------|
| `get_context` | Session context: messages, tasks, presence, notes |
| `set_presence` | Update status and workspace; changes server project context |
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
| `worker_status` | Live view of workers (driver tool) |
| `heartbeat` | Signal liveness (worker tool) |
| `report_progress` | Report progress on task (worker tool) |
| `cancel_agent` | Cancel a worker (driver tool) |
| `get_work_context` | Get task context |
| `update_work_context` | Add notes to task context |

## Usage tips

- Set `MCP_CONFIG` in `env` to point to your config file.
- Change workspace at runtime: `set_presence agent='cursor' status='working' workspace='/path/to/project'`.

## Notifications

- **Piggyback:** Tool responses include a banner when you have unread messages or pending tasks.
- **Auto-respond:** Configure `auto_respond` in config to wake other agents when they have unread content.

## CLI status

```bash
mcp-stringwork status cursor
# Output: unread=N pending=N
```

## Troubleshooting

- **Server not responding:** Check binary path and execute permission.
- **Path outside workspace:** Use `set_presence` with the correct workspace path.
- **Port conflict:** With `http_port: 0`, each instance gets its own port. With a fixed port, only one instance can run.
