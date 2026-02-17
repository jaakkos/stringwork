# Cursor MCP Configuration

## Setup

1. **Build the server:**
   ```bash
   cd /path/to/my-tiny-liter-helper
   go build -o mcp-stringwork ./cmd/mcp-server
   ```

2. **Choose how to run the server:**

   - **Option A – stdio (recommended for single Cursor instance):** Cursor starts the server as a subprocess. Edit Cursor’s MCP config and add the server.

   - **Option B – HTTP daemon:** Run the server once and connect Cursor via SSE. Use this if you also run Claude Code (or other clients) so they share one server.

### Option A: stdio (Cursor starts the server)

Open **Cursor Settings → Features → MCP → Add New MCP Server** (or edit the config file directly).

**Project-specific** (`.cursor/mcp.json` in your project):

```json
{
  "mcpServers": {
    "stringwork": {
      "command": "/path/to/my-tiny-liter-helper/mcp-stringwork",
      "args": [],
      "env": {
        "MCP_CONFIG": "/path/to/your/project/mcp/config.yaml"
      }
    }
  }
}
```

**Global** (`~/.cursor/mcp.json`):

```json
{
  "mcpServers": {
    "stringwork": {
      "command": "/path/to/mcp-stringwork",
      "args": [],
      "env": {
        "MCP_CONFIG": "/path/to/mcp/config.yaml"
      }
    }
  }
}
```

Replace `/path/to/...` with your actual paths (e.g. `/path/to/workspace/my-tiny-liter-helper/mcp-stringwork` and `.../mcp/config.yaml`).

In this repo, Cursor is already set up for Option A in `.cursor/mcp.json` (stdio + `MCP_CONFIG` pointing to `mcp/config.yaml`).

### Option B: HTTP daemon + SSE (shared server)

1. In `mcp/config.yaml` set:
   ```yaml
   transport: "http"
   http_port: 8943
   ```

2. Start the server using the [startup script](../../docs/DAEMON_SETUP.md):
   ```bash
   ./scripts/mcp-server-daemon.sh start
   # Or install to start at login (macOS): ./scripts/mcp-server-daemon.sh install-launchd
   ```
   Server will listen at `http://localhost:8943/sse`.

3. In Cursor, add an MCP server with **Type: SSE** and **URL:**
   ```
   http://localhost:8943/sse
   ```

   Or in `.cursor/mcp.json` (if your Cursor version supports `url` + type):
   ```json
   {
     "mcpServers": {
       "stringwork": {
         "url": "http://localhost:8943/sse"
       }
     }
   }
   ```

4. Restart Cursor to load the MCP server.

## Available tools (17)

| Tool | Purpose |
|------|---------|
| `get_session_context` | Session context: messages, tasks, presence, notes |
| `set_presence` | Update status and workspace; changes server project context |
| `append_session_note` | Add shared note or decision |
| `send_message` | Message your pair |
| `read_messages` | Read and mark messages as read |
| `create_task` | Create task |
| `list_tasks` | List tasks, filter by assignment/status |
| `update_task` | Update task |
| `create_plan` | Create shared plan |
| `get_plan` | View plan(s); omit ID to list all |
| `update_plan` | Add or update plan items |
| `handoff` | Hand off work with summary and next steps |
| `claim_next` | Claim next task (dry_run to peek) |
| `request_review` | Request code review from pair |
| `lock_file` | Lock, unlock, check, or list file locks (action param) |
| `register_agent` | Register a custom agent |
| `list_agents` | List all agents (built-in and registered) |

## Usage tips

- Set `MCP_CONFIG` in `env` to point to your `mcp/config.yaml` (workspace_root, state_file, auto_respond, etc.).
- Change workspace at runtime: `set_presence agent='cursor' status='working' workspace='/path/to/project'`.

## Notifications

- **Piggyback:** Tool responses include a banner when you have unread messages or pending tasks. Use `read_messages` or `get_session_context` to see them.
- **Auto-respond:** Configure `auto_respond` in `mcp/config.yaml` to wake other agents (e.g. Claude Code) when they have unread content.

## CLI status

```bash
mcp-stringwork status cursor
# Output: unread=N pending=N
```

## Troubleshooting

- **Server not responding:** Check binary path and execute permission; check stderr or `log_file` in config.
- **Path outside workspace:** Use `set_presence` with `workspace='/correct/project/path'` so the server’s path checks match your project.
