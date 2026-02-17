# MCP Stringwork

A local MCP (Model Context Protocol) server written in Go that enables **coordination** between AI tools (Cursor and Claude Code). It provides only collaboration tools—messaging, tasks, plans, presence, and file locks—so each AI uses its own native file, search, git, and terminal capabilities.

## Features

- **Session**: Get full context, set presence, add shared notes
- **Messaging**: Send and read messages between agents (with optional urgency)
- **Tasks**: Create, list, update tasks; auto-notify on assign and completion
- **Planning**: Create plans, view plans, add/update plan items
- **Workflow**: Hand off work, claim next task, request code review
- **File locks**: Lock/unlock/check files to avoid simultaneous edits
- **Agent registration**: Register custom agents beyond built-in cursor/claude-code
- **Auto-respond**: Built-in agent wake-up — spawns commands when non-connected agents have unread messages
- **Piggyback notifications**: Tool responses include a banner when the agent has unread messages or pending tasks
- **Safety**: Workspace path validation, message pruning (TTL + max count)

## Quick Start

```bash
# Build the server
go build -o mcp-stringwork ./cmd/mcp-server

# Run tests
go test ./...

# Check agent status (used by auto-respond)
./mcp-stringwork status claude-code
```

## Configuration

### Environment Variables

- `MCP_CONFIG`: Path to a YAML configuration file

### Configuration File

Copy `mcp/config.yaml` to your project and customize:

```yaml
# Initial workspace root (startup default). Clients can change it at runtime
# via set_presence workspace='/new/path' — the server follows dynamically.
workspace_root: "/path/to/your/project"

# State file: leave empty for global default (~/.config/stringwork/state.sqlite).
# All agents on the machine share this file regardless of working directory.
# Set an absolute path to override, or relative (joined with workspace_root).
state_file: ""

enabled_tools:
  - "*"  # Enable all coordination tools
message_retention_max: 1000
message_retention_days: 30
presence_ttl_seconds: 300

# Auto-respond: wake agents when they have unread messages
auto_respond:
  claude-code:
    command: ["claude", "--continue", "-p", "/pair-respond", "--dangerously-skip-permissions"]
    cooldown_seconds: 30
```

## Client Setup

### Cursor

Add to your `.cursor/mcp.json`:

```json
{
  "mcpServers": {
    "stringwork": {
      "command": "/path/to/mcp-stringwork",
      "args": [],
      "env": {
        "MCP_CONFIG": "/path/to/config.yaml"
      }
    }
  }
}
```

See [docs/mcp-client-configs/cursor-config.md](docs/mcp-client-configs/cursor-config.md) for details.

### Claude Code CLI

MCP is configured with the **`claude` CLI** (e.g. `claude mcp add-json`, `claude mcp list`). Add the server:

```bash
claude mcp add-json --scope user stringwork '{
  "type": "stdio",
  "command": "/path/to/mcp-stringwork",
  "args": [],
  "env": {
    "MCP_CONFIG": "/path/to/config.yaml"
  }
}'
```

See [docs/mcp-client-configs/claude-code-config.md](docs/mcp-client-configs/claude-code-config.md) for details.

## Available Tools (17 coordination tools)

| Tool | Description |
|------|-------------|
| `get_context` | Full session context (messages, tasks, presence, plans, notifications) |
| `set_presence` | Update your status; optionally list all presence |
| `add_note` | Add shared note or decision |
| `send_message` | Send message to pair (optional title, urgent) |
| `read_messages` | Read messages from pair |
| `create_task` | Create shared task (auto-notifies assignee) |
| `list_tasks` | List tasks with filters |
| `update_task` | Update task (auto-notifies on completion) |
| `create_plan` | Create a shared plan |
| `get_plan` | View plan(s); omit ID to list all |
| `update_plan` | Add or update plan items |
| `handoff` | Hand off work with summary and next steps |
| `claim_next` | Claim next task (or dry_run to peek) |
| `request_review` | Request code review from pair |
| `lock_file` | Lock, unlock, check, or list file locks (action param) |
| `register_agent` | Register a custom agent for auto-discovery |
| `list_agents` | List all available agents (built-in and registered) |

Use Cursor or Claude Code's native tools for files, search, git, and terminal.

### Custom Agent Registration

Beyond the built-in agents (`cursor`, `claude-code`), any MCP client can register as a custom agent:

```
register_agent name='my-bot' display_name='My Bot' capabilities='["testing","linting"]'
```

Once registered, the custom agent can use **all** coordination tools (send/read messages, create tasks, set presence, etc.) just like the built-in agents. Use `list_agents` to discover all available agents.

## Auto-Respond

The server can automatically wake up agents when they have unread messages. When Cursor's MCP server detects unread content for `claude-code`, it spawns the configured command (e.g. `claude --continue -p "/pair-respond"`) to let Claude Code process the messages and reply — no external daemon needed. The `--continue` flag preserves conversation context so claude-code retains memory of prior work across auto-respond invocations.

- **Workspace-aware**: The spawned agent inherits the workspace from the connected agent's presence (set via `set_presence workspace='/path/to/project'`). Cursor sets this automatically so claude-code always works in the right project directory.
- Cooldown prevents rapid-fire invocations (default 30s)
- Lockfile prevents concurrent spawns for the same agent
- Only triggers for agents that are NOT the currently connected client (piggyback covers the connected agent)

## Global State

By default, the state file is stored at `~/.config/stringwork/state.sqlite`, shared across all agents on the machine. This means:

- **No per-project state file** — agents always find each other regardless of cwd
- **Cursor sets the working context** via `set_presence workspace='/path/to/project'`
- **Auto-spawned agents** (like claude-code) inherit the workspace from the calling agent's presence

## Dynamic Workspace

The `workspace_root` in `config.yaml` is only the startup default. Clients can change it at runtime:

```
set_presence agent='cursor' status='working' workspace='/path/to/new/project'
```

When the workspace is updated via `set_presence`:
- **File path validation** follows the new workspace
- **Project detection** (`get_session_context`) reflects the new project
- **Auto-spawned agents** use the new directory as their working directory

This lets you switch projects mid-session without restarting the MCP server.

## CLI Subcommand

```bash
# Check unread/pending counts for any agent
./mcp-stringwork status claude-code
# Output: unread=2 pending=1
```

## Security

- All file paths are validated to stay within the workspace root (dynamically updatable via `set_presence workspace=...`)
- Message retention and presence TTL limit state growth

## AI Agent Instructions

The MCP server sends tailored instructions to each connecting client during initialization (via the MCP `initialize` response). This replaces the need for static instruction files in most setups.

For reference, the project also includes static instruction files:

- **`AGENTS.md`** - Instructions for Cursor (identifies as `cursor`)
- **`CLAUDE.md`** - Instructions for Claude Code (identifies as `claude-code`)

These define the default pair programming workflow: check context, set workspace, read messages, claim tasks, do work, communicate findings.

## Project Structure

```
.
├── cmd/mcp-server/          # Server entrypoint
├── internal/
│   ├── domain/              # Collaboration entities (Message, Task, Plan, etc.)
│   ├── app/                 # Application service, interfaces, helpers
│   ├── repository/          # State persistence (sqlite)
│   ├── mcp/                 # MCP protocol types and transport
│   ├── policy/              # Security policy enforcement
│   ├── server/              # MCP server and tool registry
│   └── tools/
│       └── collab/          # 17 coordination tools (messaging, tasks, plans, presence, locks, agents)
├── mcp/                     # Configuration files
└── docs/                    # Documentation
```

## License

MIT
