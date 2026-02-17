# Setup Guide

Complete setup instructions for running Stringwork with a driver/worker configuration.

## Overview

Stringwork is an MCP server that coordinates AI coding agents using a **driver/worker** model:

- **Driver** (typically Cursor): creates tasks, monitors workers, cancels stuck agents
- **Workers** (Claude Code, Codex, custom agents): claim tasks, do work, report progress

All agents share state through `~/.config/stringwork/state.sqlite`. The server spawns workers automatically when there's pending work.

## Step 1: Install

### Option A: Pre-built binary (recommended)

```bash
curl -fsSL https://raw.githubusercontent.com/jaakkos/stringwork/main/scripts/install.sh | sh
```

This installs to `~/.local/bin/mcp-stringwork`. Pass `--dir /usr/local/bin` for a system-wide install.

### Option B: Build from source

```bash
git clone https://github.com/jaakkos/stringwork.git
cd stringwork
go build -o mcp-stringwork ./cmd/mcp-server
```

Verify the install:

```bash
mcp-stringwork --version
```

## Step 2: Create a config file

Create `~/.config/stringwork/config.yaml` (or keep it per-project):

```yaml
# Startup default workspace. Clients can change at runtime via set_presence.
workspace_root: "/path/to/your/project"

# Transport: "stdio" for single-client, "http" for multi-client + dashboard
transport: "stdio"
http_port: 8943

enabled_tools: ["*"]
message_retention_max: 1000
message_retention_days: 30
presence_ttl_seconds: 300

# Auto-respond: spawn agents when they have unread messages
auto_respond:
  claude-code:
    command: ["claude", "--continue", "-p", "/pair-respond", "--dangerously-skip-permissions"]
    cooldown_seconds: 30

# Orchestration: driver + workers
orchestration:
  driver: cursor
  assignment_strategy: least_loaded
  workers:
    - type: claude-code
      instances: 1
      command: ["claude", "-p", "You are claude-code in a pair programming session. Your workspace is {workspace}. Steps: 1) set_presence agent='claude-code' status='working' workspace='{workspace}' 2) read_messages for 'claude-code' 3) list_tasks assigned_to='claude-code' 4) Process ALL unread messages and pending tasks. 5) report_progress every 2-3 minutes. 6) send_message from='claude-code' to='cursor' with findings.", "--dangerously-skip-permissions"]
      cooldown_seconds: 30
      timeout_seconds: 600
      max_retries: 2
      env:
        GH_TOKEN: "${GH_TOKEN}"
        GITHUB_TOKEN: "${GITHUB_TOKEN}"
        SSH_AUTH_SOCK: "${SSH_AUTH_SOCK}"
```

See [mcp/config.yaml](../mcp/config.yaml) for a fully annotated example with all options.

## Step 3: Configure MCP clients

### Cursor (driver)

Add to `.cursor/mcp.json` in your project:

```json
{
  "mcpServers": {
    "stringwork": {
      "command": "/path/to/mcp-stringwork",
      "env": {
        "MCP_CONFIG": "/path/to/config.yaml"
      }
    }
  }
}
```

If using HTTP mode (`transport: "http"`), use the SSE URL instead:

```json
{
  "mcpServers": {
    "stringwork": {
      "url": "http://localhost:8943/sse"
    }
  }
}
```

Restart Cursor to load the MCP server.

### Claude Code CLI (worker or peer)

```bash
claude mcp add-json --scope user stringwork '{
  "type": "stdio",
  "command": "/path/to/mcp-stringwork",
  "env": {
    "MCP_CONFIG": "/path/to/config.yaml"
  }
}'
```

Or for HTTP mode:

```bash
claude mcp add-json --scope user stringwork '{
  "type": "url",
  "url": "http://localhost:8943/mcp"
}'
```

Verify with `claude mcp list`. Restart Claude Code to load.

See [docs/mcp-client-configs/](mcp-client-configs/) for detailed client configuration.

## Step 4: Verify setup

### Test from Cursor

Check that MCP tools are available (command palette should show `get_context`, `send_message`, `create_task`, etc.).

Quick test:

```
get_context for 'cursor'
create_task title='Test task' assigned_to='claude-code' created_by='cursor'
```

### Test from Claude Code

```
get_context for 'claude-code'
list_tasks assigned_to='claude-code'
```

### Test the full loop

1. **Cursor** creates a task: `create_task title='Say hello' assigned_to='claude-code' created_by='cursor'`
2. **Claude Code** sees it: `list_tasks assigned_to='claude-code'`
3. **Claude Code** claims it: `update_task id=1 status='in_progress' updated_by='claude-code'`
4. **Claude Code** reports back: `send_message from='claude-code' to='cursor' content='Done!'`
5. **Claude Code** completes it: `update_task id=1 status='completed' updated_by='claude-code'`
6. **Cursor** sees the completion notification via piggyback banner

## Orchestration Setup

### How worker spawning works

When orchestration is configured:

1. The driver creates a task with `assigned_to='any'`
2. The server's orchestrator assigns it to an available worker type
3. The worker manager spawns the worker process (e.g. `claude -p "..."`)
4. The worker claims the task, does the work, and reports progress
5. The server monitors heartbeats and escalates if a worker goes silent

### Worker types

#### Claude Code workers

Claude Code with `--dangerously-skip-permissions` has full filesystem and network access:

```yaml
workers:
  - type: claude-code
    instances: 2
    command: ["claude", "-p", "...prompt...", "--dangerously-skip-permissions"]
    timeout_seconds: 600
```

#### Codex workers

Codex blocks network by default. Use `--sandbox danger-full-access` for full capabilities:

```yaml
workers:
  - type: codex
    instances: 1
    command: ["codex", "exec", "--sandbox", "danger-full-access", "--skip-git-repo-check", "...prompt..."]
```

| Codex sandbox mode | Filesystem | Network | Use when |
|--------------------|-----------|---------|----------|
| `workspace-write` (default) | Write in workspace | Blocked | Untrusted tasks |
| `workspace-write` + `network_access=true` | Write in workspace | Allowed | Trusted tasks needing APIs |
| `danger-full-access` | Full system | Full | Trusted worker agents |

### Worker environment variables

Workers inherit the server's environment by default. You can customize:

```yaml
workers:
  - type: claude-code
    command: [...]
    env:
      GH_TOKEN: "${GH_TOKEN}"          # expand from server env
      SSH_AUTH_SOCK: "${SSH_AUTH_SOCK}"
      MY_API_KEY: "literal-value"
    # Restrict what's inherited (default: everything)
    # inherit_env: ["HOME", "PATH", "GH_*", "SSH_*"]
    # inherit_env: ["none"]             # clean environment
```

Spawned workers always receive `STRINGWORK_AGENT` and `STRINGWORK_WORKSPACE` automatically.

### Progress monitoring

Workers must report progress while working. The server monitors and escalates:

| Duration without `report_progress` | What happens |
|-------------------------------------|-------------|
| 3 minutes | Warning sent to driver |
| 5 minutes | Critical alert sent to driver |
| 10 minutes (no heartbeat) | Task auto-recovered, worker may be cancelled |

Workers call:
- `heartbeat` every 60-90 seconds with a progress description
- `report_progress` every 2-3 minutes with task_id, description, percent_complete

The driver monitors all workers via `worker_status` and can cancel stuck ones with `cancel_agent`.

### Task SLAs

Set expected duration when creating tasks:

```
create_task title='Code review' assigned_to='any' created_by='cursor' expected_duration_seconds=300
```

If the task exceeds its SLA, the driver gets an alert. `worker_status` shows SLA status for all running tasks.

### Git worktree isolation (optional)

Give each worker its own git checkout to prevent file conflicts:

```yaml
orchestration:
  worktrees:
    enabled: true
    base_branch: ""                    # empty = current HEAD
    cleanup_strategy: "on_cancel"      # on_cancel | on_exit | manual
    path: ".stringwork/worktrees"
```

Requires the workspace to be a git repository.

## HTTP mode and daemon setup

For multi-client access (Cursor + Claude Code + dashboard connected to one server):

```yaml
transport: "http"
http_port: 8943
```

See [DAEMON_SETUP.md](DAEMON_SETUP.md) for running as a background service or macOS launchd agent.

When running in HTTP mode, the dashboard is available at `http://localhost:8943/dashboard`.

## Common Issues

### "Tool not found" errors

1. Verify JSON syntax in config files
2. Ensure binary path is absolute
3. Restart the MCP client completely
4. Check agent logs

### "Path outside workspace" errors

Update the workspace dynamically:

```
set_presence agent='cursor' status='working' workspace='/path/to/correct/project'
```

### Workers not spawning

1. Verify `orchestration` section exists in config
2. Check that the worker command works standalone (e.g. `claude -p "hello"`)
3. Check worker logs: `~/.config/stringwork/stringwork-worker-<instance>.log`
4. Ensure auth tokens are available (GH_TOKEN, SSH_AUTH_SOCK)

### Worker verification checklist

- [ ] Worker command works standalone
- [ ] `gh auth status` works inside the worker (if needed)
- [ ] Worker can read/write files in the workspace
- [ ] Worker can call MCP tools (`heartbeat`, `report_progress`, `send_message`)
- [ ] Worker logs show activity: `~/.config/stringwork/stringwork-worker-*.log`

## Next Steps

- [WORKFLOW.md](WORKFLOW.md) -- collaboration patterns and best practices
- [QUICK_REFERENCE.md](QUICK_REFERENCE.md) -- tool usage examples
- [DAEMON_SETUP.md](DAEMON_SETUP.md) -- HTTP mode and background service
