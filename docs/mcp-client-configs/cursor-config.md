# Cursor MCP Configuration

## Setup

Cursor starts the Stringwork server as a subprocess. With daemon mode enabled (recommended), the first Cursor window starts a background daemon and subsequent windows connect as lightweight proxies.

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

### Daemon mode (recommended)

Enable daemon mode in your config for the best multi-window experience:

```yaml
daemon:
  enabled: true
  grace_period_seconds: 10
```

With daemon mode:
- Multiple Cursor windows share a single server process
- Workers, notifier, and watchdog run once (no duplicates)
- The HTTP port and dashboard URL stay stable across reconnects
- When the last window closes, the daemon shuts down after the grace period

Set a fixed `http_port` (e.g. 8943) for a permanent dashboard URL. With `http_port: 0`, the port is assigned once when the daemon starts and stays stable until the daemon restarts.

### Without daemon mode

Each Cursor window spawns its own server. With `http_port: 0`, each gets an auto-assigned port. All instances share the same SQLite state, so tasks and messages work across windows. Set a fixed port only for a predictable dashboard URL, but only one instance can use a given port.

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
- **Port conflict:** With daemon mode, all Cursor windows share one port. Without daemon mode, use `http_port: 0` so each instance gets its own port.
