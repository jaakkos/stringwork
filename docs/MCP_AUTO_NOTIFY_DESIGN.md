# MCP Notification System Architecture

## Overview

The MCP Stringwork server provides three notification mechanisms to keep agents synchronized without requiring them to poll:

1. **Piggyback Notifications** - Banners appended to tool responses
2. **JSON-RPC Push Notifications** - Proactive push to the connected client
3. **Auto-Respond** - Spawns commands to wake up disconnected agents

## Architecture

```
┌─────────────────┐     ┌─────────────────┐
│   Cursor IDE    │     │  Claude Code    │
│  (MCP Client)   │     │  (MCP Client)   │
└────────┬────────┘     └────────┬────────┘
         │                       │
         │ stdio                 │ stdio
         │                       │
┌────────▼────────┐     ┌────────▼────────┐
│  MCP Server A   │     │  MCP Server B   │
│ (cursor session)│     │(claude-code)    │
└────────┬────────┘     └────────┬────────┘
         │                       │
         └──────────┬────────────┘
                    │
            ┌───────▼───────┐
            │  State File   │
            │ (.sqlite)      │
            └───────┬───────┘
                    │
            ┌───────▼───────┐
            │  Signal File  │
            │ (.pair-notify)│
            └───────────────┘
```

Each MCP client runs a separate server process. State is shared via the state file; change detection uses the signal file.

## 1. Piggyback Notifications

**Implementation:** `internal/tools/collab/piggyback.go`

Every successful tool response is wrapped to check if the connected agent has unread messages or pending tasks. If so, a banner is appended:

```
🔔 You have 2 unread message(s) and 1 pending task(s). Call read_messages or get_session_context to see them.
```

### Suppressed Tools

Banners are suppressed for tools that already display this information:
- `read_messages`
- `get_session_context`

### How It Works

1. Tool handler executes normally
2. Wrapper calls `CollabService.Query()` to read state (read-only)
3. Counts unread messages and pending tasks for the connected agent
4. Appends banner to the last text content block if counts > 0

## 2. JSON-RPC Push Notifications

**Implementation:** `internal/app/notifier.go`

The Notifier watches the signal file and pushes `notifications/pair_update` to stdout when the connected agent has new unread content.

### Notification Format

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

### How It Works

1. **Signal file watch**: Uses fsnotify to watch `.stringwork-notify`
2. **Fallback poll**: 60-second polling as backup (handles edge cases)
3. **Debounce**: 200ms debounce to coalesce rapid changes
4. **Revision tracking**: Tracks last-pushed revision to avoid duplicates
5. **Agent identity**: Set when client first calls `get_session_context` or `set_presence`

### Agent Identity Detection

The server tracks the connected agent via two mechanisms:
- `get_session_context for='<agent>'` - Sets connected agent identity
- `set_presence agent='<agent>'` - Also sets connected agent identity; if `workspace` is provided, dynamically updates the server's workspace root (file validation, project detection, auto-respond working directory)

Once identity is set, the notifier knows which agent to check for unread content.

## 3. Auto-Respond

**Implementation:** `internal/app/auto_respond.go`

When the MCP server detects unread messages for an agent that is NOT the currently connected client, it spawns a configured command to wake up that agent.

### Configuration

In `mcp/config.yaml`:

```yaml
auto_respond:
  claude-code:
    command: ["claude", "--continue", "-p", "/pair-respond", "--dangerously-skip-permissions"]
    cooldown_seconds: 30
```

### How It Works

1. **Triggered by Notifier**: Called on every signal file change
2. **Skips connected agent**: Piggyback already covers the connected client
3. **Checks unread/pending**: For each configured agent
4. **Cooldown**: Enforces minimum time between spawns (default: 30s)
5. **File lock**: Prevents overlapping invocations for the same agent
6. **Spawns command**: Runs in background with 5-minute timeout

### Lockfile

Uses `/tmp/pair-auto-respond-<agent>.lock` to prevent concurrent invocations:
- Created atomically with `O_EXCL`
- Contains PID for debugging
- Auto-expires after 5 minutes (stale lock protection)
- Deleted on spawn completion

## CLI Status Subcommand

**Implementation:** `cmd/mcp-server/main.go:runStatusCommand()`

For scripts or debugging:

```bash
mcp-stringwork status claude-code
# Output: unread=2 pending=1
```

Reads state file directly without starting the full MCP server.

## Key Files

| File | Purpose |
|------|---------|
| `internal/app/notifier.go` | Signal file watcher, JSON-RPC push |
| `internal/app/auto_respond.go` | Auto-respond spawner |
| `internal/tools/collab/piggyback.go` | Piggyback banner wrapper |
| `internal/app/collab_service.go` | `Query()` method for read-only state access |
| `.claude/commands/pair-respond.md` | Slash command for auto-respond invocation |

## Design Decisions

### Why Three Mechanisms?

1. **Piggyback**: Zero latency for the connected agent; always sees status
2. **JSON-RPC Push**: Enables proactive UI updates in supporting clients
3. **Auto-Respond**: Wakes up disconnected agents automatically

### Why Signal File?

- Works across separate processes (each MCP client is a separate server)
- Simple atomic touch operation
- fsnotify provides efficient watching
- Fallback poll handles edge cases (network drives, WAL)

### Why Built-in Auto-Respond?

- No external daemon or shell script dependencies
- Single binary deployment
- Integrated cooldown and locking
- Respects config changes without restart

## No External Dependencies

The notification system requires only:
- The `mcp-stringwork` binary
- Configuration in `mcp/config.yaml`

No external scripts, daemons, or tools (fswatch, jq, sqlite3) are needed.
