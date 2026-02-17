# Pair Programming Setup Guide

Complete setup instructions for claude-code and cursor to collaborate effectively using the MCP stringwork server.

## Overview

This MCP server enables two AI agents to work together by providing:
- Shared task management
- Inter-agent messaging
- Session context, presence, plans, handoffs, and file locks
- Persistent collaboration state (default `~/.config/stringwork/state.sqlite`, configurable via `state_file`)

Agents use their **native tools** (Cursor/Claude) for files, search, git, and terminal; this server is coordination-only.

## Prerequisites

- Go 1.21+ (for building the server)
- Git

## Step 1: Build the MCP Server

```bash
cd /path/to/workspace/my-tiny-liter-helper
go build -o mcp-stringwork ./cmd/mcp-server
chmod +x mcp-stringwork
```

Verify the build:
```bash
./mcp-stringwork --version 2>&1 | head -1
```

## Step 2: Configure Cursor

**Location:** `.cursor/mcp.json` (already configured in this project)

```json
{
  "mcpServers": {
    "stringwork": {
      "command": "/path/to/workspace/my-tiny-liter-helper/mcp-stringwork"
    }
  }
}
```

**Restart Cursor** to load the MCP server.

### Verify Cursor Setup

In Cursor, check that MCP tools are available:
- Open the command palette
- Look for coordination tools like `get_session_context`, `send_message`, `list_tasks`

## Step 3: Configure Claude Code CLI

Claude Code MCP is configured with the **`claude` CLI** (not by editing config files). Use `claude mcp add-json` to register the server at user scope (available across all projects):

```bash
claude mcp add-json --scope user stringwork '{
  "type": "stdio",
  "command": "/path/to/workspace/my-tiny-liter-helper/mcp-stringwork",
  "args": [],
  "env": {
    "MCP_CONFIG": "/path/to/workspace/my-tiny-liter-helper/mcp/config.yaml"
  }
}'
```

Verify with `claude mcp list`.

**Note:** Claude Code CLI stores config in `~/.claude.json` (NOT `~/.claude/mcp.json`). Local-scoped entries (per-project) take precedence over user-scoped ones.

**Restart Claude Code** (quit and relaunch the CLI) to load the MCP server.

### Verify Claude Code Setup

Run a quick test:
```bash
# In Claude Code, try accessing the tools
Use get_session_context for 'claude-code'
```

If successful, you should see the collaboration state or a message indicating no unread messages.

## Step 4: Create Initial Configuration (Optional)

Create `mcp/config.yaml` to customize behavior:

```yaml
# Initial workspace root (startup default). Clients can override at runtime
# via set_presence workspace='/new/path' — the server follows dynamically.
workspace_root: "/path/to/workspace/my-tiny-liter-helper"
state_file: ""  # default: ~/.config/stringwork/state.sqlite
enabled_tools:
  - "*"  # Enable all coordination tools
message_retention_max: 500
message_retention_days: 30
presence_ttl_seconds: 300

# Auto-respond: when an agent has unread messages and is NOT the connected client,
# the server spawns this command to wake them up. No external daemon required.
auto_respond:
  claude-code:
    command: ["claude", "--continue", "-p", "/pair-respond", "--dangerously-skip-permissions"]
    cooldown_seconds: 30
```

Update both MCP configurations to use it:

```json
{
  "mcpServers": {
    "stringwork": {
      "command": "/path/to/mcp-stringwork",
      "args": [],
      "env": {
        "MCP_CONFIG": "/path/to/workspace/my-tiny-liter-helper/mcp/config.yaml"
      }
    }
  }
}
```

### CLI Status Subcommand

Check an agent's unread/pending counts without starting the full server:

```bash
./mcp-stringwork status claude-code
# Output: unread=2 pending=1

./mcp-stringwork status cursor
# Output: unread=0 pending=3
```

This is useful for debugging or external integrations.

## Step 5: Initialize Collaboration State

The state file (default `~/.config/stringwork/state.sqlite`) is created on first use. Override via `state_file` in `mcp/config.yaml`.

## Verification Checklist

Use this checklist to ensure both agents are set up correctly:

### For Cursor

- [ ] Binary built and executable
- [ ] `.cursor/mcp.json` configured
- [ ] Cursor restarted
- [ ] Can run `get_session_context for 'cursor'`
- [ ] Can run `send_message from='cursor' to='claude-code' content='test'`
- [ ] Can run `list_tasks`

### For Claude Code

- [ ] Binary built and executable
- [ ] `claude mcp list` shows stringwork (or Claude Desktop config if using the app)
- [ ] Claude Code CLI restarted
- [ ] Can access `get_context for 'claude-code'`
- [ ] Can run `send_message from='claude-code' to='cursor' content='test'`
- [ ] Can run `list_tasks`

### Test Collaboration

1. **From Cursor:** Create a test task
   ```
   Use create_task with title='Test task' description='Verify setup works' assigned_to='claude-code' created_by='cursor'
   ```

2. **From Claude Code:** Check for the task
   ```
   Use list_tasks
   Use get_context for 'claude-code'
   ```

3. **From Claude Code:** Update the task
   ```
   Use update_task with id=X status='completed' updated_by='claude-code'
   ```

4. **From Cursor:** Verify completion
   ```
   Use list_tasks
   ```

If all steps work, your setup is complete!

## Common Issues

### "Tool not found" errors

**Problem:** MCP tools aren't available in the agent

**Solution:**
1. Verify JSON syntax in config files (use `jq` or JSON validator)
2. Ensure binary path is absolute and correct
3. Restart the agent application completely
4. Check for errors in the agent's logs

### "Path outside workspace" errors

**Problem:** Trying to access files outside the current workspace

**Solution:**
1. Update the workspace dynamically: `set_presence agent='cursor' status='working' workspace='/path/to/correct/project'`
2. Or ensure `workspace_root` in config.yaml includes all needed directories (startup default)
3. Use relative paths when possible

### Slow responses

**Problem:** MCP server or state file is slow

**Solution:**
1. Ensure state file is on a fast disk and not in a synced folder
2. Reduce `message_retention_max` or `message_retention_days` in config if the state file is large

### State file corruption

**Problem:** State file has invalid JSON

**Solution:**
1. Backup the state file (adjust path if you set `state_file`): `cp ~/.config/stringwork/state.sqlite ~/.config/stringwork/state.sqlite.bak`
2. Fix JSON syntax or delete and restart
3. The file will be recreated automatically

### MCP server not starting

**Problem:** Server binary doesn't execute

**Solution:**
1. Verify binary is executable: `chmod +x mcp-stringwork`
2. Test running directly: `./mcp-stringwork` (should wait for stdin)
3. Check Go build for errors: `go build -o mcp-stringwork ./cmd/mcp-server`

## Next Steps

Once setup is verified:

1. Read [WORKFLOW.md](./WORKFLOW.md) for collaboration patterns
2. Check [QUICK_REFERENCE.md](./QUICK_REFERENCE.md) for command examples
3. Review [AGENTS.md](../AGENTS.md) and [CLAUDE.md](../CLAUDE.md) for agent-specific instructions

## Custom Agent Registration

Beyond the built-in agents, any MCP client can register and participate:

### Register a Custom Agent

```
register_agent name='my-bot' display_name='My Bot' capabilities='["testing","linting"]'
```

Parameters:
- `name` (required) - Unique agent identifier (e.g. `my-bot`, `lint-runner`)
- `display_name` (optional) - Human-friendly name
- `capabilities` (optional) - JSON array of capability strings
- `workspace` (optional) - Workspace path the agent is working in
- `project` (optional) - Project name

### After Registration

Once registered, the agent must:

1. **Set presence:** `set_presence agent='my-bot' status='working' workspace='/path/to/project'`
2. **Check context:** `get_context for='my-bot'`
3. Use any coordination tool normally (send_message, create_task, etc.)

### Discover Available Agents

```
list_agents                     # All agents (built-in + registered)
list_agents active_only=true    # Only agents seen recently
list_agents include_builtin=false  # Only custom-registered agents
```

### Important Notes

- Registration persists in the state file for the session lifetime
- Registered agents are validated identically to built-in agents for all tools
- If a registered agent is not recognized, ensure it was registered in the **same** state file instance

## Worker Setup (Orchestration Mode)

When running with orchestration enabled (`orchestration:` in config.yaml), the server spawns worker processes (Claude Code, Codex) automatically. Each worker type has its own requirements for full functionality.

### Codex Workers

**Critical: Codex blocks network access by default.** Even with `--full-auto`, the Codex CLI sandbox prevents network calls (`gh`, `curl`, API requests, etc.).

#### Fix: Enable network access

Option A — **CLI flag** (recommended, configured per-worker in config.yaml):

```yaml
orchestration:
  workers:
    - type: codex
      command: ["codex", "exec", "--sandbox", "danger-full-access", "--skip-git-repo-check", "...prompt..."]
```

Option B — **Global Codex config** (`~/.codex/config.toml`):

```toml
sandbox_mode = "danger-full-access"
```

Option C — **Keep workspace-write but enable network only**:

```yaml
command: ["codex", "exec", "--full-auto", "-c", "sandbox_workspace_write.network_access=true", "--skip-git-repo-check", "...prompt..."]
```

#### Codex sandbox modes

| Mode | Filesystem | Network | Use when |
|------|-----------|---------|----------|
| `workspace-write` (default with `--full-auto`) | Write in workspace | **Blocked** | Untrusted tasks |
| `workspace-write` + `network_access=true` | Write in workspace | Allowed | Trusted tasks needing APIs |
| `danger-full-access` | Full system | Full | Trusted worker agents (recommended) |

### Claude Code Workers

Claude Code with `--dangerously-skip-permissions` has full filesystem and network access. No additional sandbox configuration needed.

```yaml
orchestration:
  workers:
    - type: claude-code
      command: ["claude", "-p", "...prompt...", "--dangerously-skip-permissions"]
```

### Worker Environment Variables

Workers inherit the server process's environment by default, but you can customize what they receive using `env` and `inherit_env` in the worker config.

#### Passing auth tokens

If workers need to run `gh`, `git push`, or call APIs, ensure tokens are available:

```yaml
orchestration:
  workers:
    - type: codex
      command: [...]
      env:
        # Expand from server's environment
        GH_TOKEN: "${GH_TOKEN}"
        GITHUB_TOKEN: "${GITHUB_TOKEN}"
        SSH_AUTH_SOCK: "${SSH_AUTH_SOCK}"
        # Or set a literal value
        MY_API_KEY: "sk-abc123"
```

Values support `${VAR}` syntax to expand from the server's own environment. This is important when the server runs as a daemon — the daemon may not have the same env vars as your interactive shell.

#### Restricting inherited env vars

By default, ALL parent env vars are inherited. To restrict:

```yaml
orchestration:
  workers:
    - type: codex
      inherit_env:
        - "HOME"
        - "PATH"
        - "SHELL"
        - "USER"
        - "GH_*"          # glob patterns supported
        - "GITHUB_*"
        - "SSH_*"
        - "GOPATH"
        - "GOROOT"
        - "NVM_*"
      env:
        CUSTOM_VAR: "value"
```

Set `inherit_env: ["none"]` for a completely clean environment (only `STRINGWORK_AGENT`, `STRINGWORK_WORKSPACE`, and your `env` map).

### Worker Verification Checklist

After configuring workers, verify they can run everything they need:

- [ ] **Network access**: Worker can reach external APIs (`gh api`, `curl https://api.github.com`)
- [ ] **Git operations**: Worker can `git push`, `git fetch` (needs SSH_AUTH_SOCK or HTTPS token)
- [ ] **Auth tokens**: `gh auth status` works inside the worker
- [ ] **MCP tools**: Worker can call `heartbeat`, `report_progress`, `send_message`
- [ ] **File operations**: Worker can read/write files in the workspace

Check worker logs at `~/.config/stringwork/stringwork-worker-<instance>.log`.

### Progress Monitoring

Workers are required to report progress while working. The server monitors these reports and alerts the driver if a worker goes silent:

| Duration without `report_progress` | What happens |
|-------------------------------------|-------------|
| 3 minutes | Warning sent to driver |
| 5 minutes | Critical alert sent to driver |
| 10 minutes (no heartbeat) | Task auto-recovered, worker may be cancelled |

Workers must call:
- `heartbeat` every 60–90 seconds with a `progress` description
- `report_progress` every 2–3 minutes with `task_id`, `description`, `percent_complete`

This is enforced in the worker spawn prompt and in the MCP server instructions. See [QUICK_REFERENCE.md](./QUICK_REFERENCE.md) for examples.

### Task SLAs

When creating tasks, set `expected_duration_seconds` to enable SLA monitoring:

```
Use create_task title='Code review' assigned_to='any' created_by='cursor' expected_duration_seconds=300
```

If the task exceeds its expected duration, the driver gets an SLA alert. The `worker_status` tool shows SLA status for all running tasks.

## Advanced Configuration

### Per-Project Setup

For different projects, you can:

1. **Build separate binaries** for each project
2. **Use environment variables** to point to project-specific configs (e.g. `MCP_CONFIG`)
3. **Symlink the binary** from a global location

### Tool Restrictions

Disable specific coordination tools if needed:

```yaml
enabled_tools:
  - get_context
  - send_message
  - list_tasks
  - create_task
  - update_task
  # ... list only the tools you want
```

## Security Notes

- The server only provides coordination tools (tasks, messages, plans, locks). File and command access are handled by each agent’s native environment.
- State is stored globally at `~/.config/stringwork/state.sqlite` by default. The `workspace_root` (from config or dynamically set via `set_presence`) controls file path validation. Ensure the state file is not exposed publicly.
