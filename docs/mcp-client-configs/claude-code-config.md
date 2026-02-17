# Claude Code MCP Configuration

MCP for Claude Code is configured using the **`claude` CLI** (not by editing config files by hand). Config is stored in `~/.claude.json`.

## Claude CLI commands for MCP

| Command | Purpose |
|---------|---------|
| `claude mcp add-json --scope user <name> '<json>'` | Add or update an MCP server (user scope = all projects) |
| `claude mcp list` | List configured MCP servers |
| `claude mcp remove --scope user <name>` | Remove an MCP server |

Use `claude mcp --help` for more options (e.g. `--scope project` for current project only).

## Setup

1. **Build the server:**
   ```bash
   cd /path/to/my-tiny-liter-helper
   go build -o mcp-stringwork ./cmd/mcp-server
   ```

2. **Choose how to run the server:**

   - **Option A – stdio (recommended):** Claude Code starts the server as a subprocess. Add the server with `claude mcp add-json`.

   - **Option B – HTTP daemon:** Run the server once; add the server with `claude mcp add-json` using the HTTP URL. Use this if Cursor (or other clients) share the same server. When the server spawns Claude Code as a worker, it **auto-injects** MCP config for that run so you don't need to run `claude mcp add-json` for worker processes.

### Option A: stdio (Claude Code starts the server)

Add the stringwork server with the **`claude` CLI** at user scope (available in all projects):

```bash
claude mcp add-json --scope user stringwork '{
  "type": "stdio",
  "command": "/path/to/mcp-stringwork",
  "args": [],
  "env": {
    "MCP_CONFIG": "/path/to/mcp/config.yaml"
  }
}'
```

Replace `/path/to/mcp-stringwork` and `/path/to/mcp/config.yaml` with your actual paths (e.g. `/path/to/workspace/my-tiny-liter-helper/mcp-stringwork` and `.../mcp/config.yaml`).

Verify and restart:

```bash
claude mcp list
# Restart Claude Code so it picks up the new server
```

If you see **"MCP server stringwork already exists in user config"**, the server is already registered. To change the path or config, remove it first then add again: `claude mcp remove --scope user stringwork`, then run `claude mcp add-json` again.

**Scopes:** User scope writes to `~/.claude.json` under `mcpServers`. Local/project scope (same file, keyed by project path) overrides user scope for that project. To remove the server: `claude mcp remove --scope user stringwork`.

### Option B: HTTP daemon + Streamable HTTP (shared server)

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
   Server will listen at `http://localhost:8943/mcp`.

3. Add the server with the **`claude` CLI** (Streamable HTTP):
   ```bash
   claude mcp add-json --scope user stringwork '{
     "type": "http",
     "url": "http://localhost:8943/mcp"
   }'
   ```

4. Restart Claude Code.

**Auto MCP for workers:** When the server runs in HTTP mode and spawns Claude Code (e.g. from `orchestration.workers`), it writes a temporary config and sets `CLAUDE_CONFIG_PATH` so the spawned process connects to the server without you running `claude mcp add-json`. Only the driver (e.g. Cursor) and any manually started Claude Code sessions need the config above.

## Per-project configuration

- Point `MCP_CONFIG` to a project-specific file (e.g. `mcp/config.yaml` in the repo) so `workspace_root` and other options match the project.
- Change workspace at runtime: `set_presence agent='claude-code' status='working' workspace='/path/to/project'`.

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

## Pair programming workflow

- Start: `get_session_context` for `'claude-code'`
- Check tasks: `list_tasks` with `assigned_to='claude-code'`
- Claim: `update_task` with `id=X` `status='in_progress'` `updated_by='claude-code'`
- Report: `send_message` from `'claude-code'` to `'cursor'` with your summary
- Complete: `update_task` with `id=X` `status='completed'` `updated_by='claude-code'`

## Notifications

- **Piggyback:** Tool responses include a banner when you have unread messages or pending tasks. Use `read_messages` or `get_session_context` to see them.
- **Auto-respond:** Configure `auto_respond` in `mcp/config.yaml` so the server can spawn Claude Code (e.g. with a prompt like `/pair-respond`) when it has unread content.

## CLI status

```bash
mcp-stringwork status claude-code
# Output: unread=N pending=N
```

## Security

- The server only manages collaboration state (tasks, messages, plans, locks). File and command access use Claude Code’s own tools.
- State is stored by default at `~/.config/stringwork/state.sqlite`. `workspace_root` (from config or `set_presence`) controls path validation.

## Troubleshooting

- **Tool not found:** Restart Claude Code after config changes; check JSON and binary path. Use `claude mcp list` to confirm the server is present.
- **Path outside workspace:** Set workspace via `set_presence agent='claude-code' status='working' workspace='/correct/project/path'`.

## Claude Desktop app (optional)

The **Claude Desktop** app (separate from Claude Code CLI) does not use the `claude` CLI; its MCP config is in a different file. If you use the Desktop app, edit manually:

- **macOS:** `~/Library/Application Support/Claude/claude_desktop_config.json` — add a `stringwork` entry under `mcpServers` with `command`, `args`, and `env.MCP_CONFIG` as in the stdio example above.
