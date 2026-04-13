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

# Daemon mode: multiple Cursor windows share one server process.
daemon:
  enabled: true
  grace_period_seconds: 10

# Fixed port for a stable dashboard/worker URL (http://localhost:8943/dashboard).
# Use 0 for auto-assign (stable within a daemon session, changes on daemon restart).
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
      command: ["claude", "-p", "You are claude-code, a worker. Workspace: {workspace}. MANDATORY: heartbeat every 60-90s, report_progress every 2-3min, send_message before finishing. Steps: 1) set_presence 2) read_messages 3) list_tasks 4) Do the work 5) report_progress 6) send_message with findings.", "--dangerously-skip-permissions"]
      cooldown_seconds: 30
      timeout_seconds: 600
      max_retries: 2
      env:
        GH_TOKEN: "${GH_TOKEN}"
        GITHUB_TOKEN: "${GITHUB_TOKEN}"
        SSH_AUTH_SOCK: "${SSH_AUTH_SOCK}"
```

See [mcp/config.yaml](../mcp/config.yaml) for a fully annotated example with all options.

## Step 3: Configure Cursor (driver)

Add to `.cursor/mcp.json` in your project:

```json
{
  "mcpServers": {
    "stringwork": {
      "command": "mcp-stringwork",
      "env": { "MCP_CONFIG": "/path/to/config.yaml" }
    }
  }
}
```

Cursor starts the server as a subprocess via stdio.

### Daemon mode (recommended for multiple Cursor windows)

With daemon mode enabled, multiple Cursor windows share a **single** server process:

```yaml
# In ~/.config/stringwork/config.yaml
daemon:
  enabled: true
  grace_period_seconds: 10  # how long to wait after last window closes
```

How it works:
1. The first Cursor window starts a background daemon and connects as a proxy
2. Subsequent Cursor windows detect the running daemon and connect as proxies
3. Workers, notifier, and watchdog run once in the daemon (no duplicates)
4. When the last Cursor window closes, the daemon waits for the grace period then shuts down

Each proxy is a thin stdio-to-HTTP bridge. The daemon serves HTTP on both a TCP port (for workers/dashboard) and a unix socket (for proxies). The HTTP port and dashboard URL stay stable across Cursor reconnects.

Use `--standalone` to bypass daemon mode and run the legacy single-process mode.

### Standalone mode (legacy)

Without daemon mode, each Cursor window spawns its own server. The server runs stdio for the driver and HTTP for workers. When Cursor closes, its server shuts down.

With `http_port: 0` (default), each window gets an auto-assigned port. All instances share the same SQLite state file, so tasks and messages are visible across windows.

### Claude Code CLI (manual use)

Workers are spawned automatically, but to use Claude Code interactively you can connect via HTTP. With daemon mode and a fixed `http_port` (e.g. 8943), the URL is permanently available:

```bash
claude mcp add-json --scope user stringwork '{
  "type": "url",
  "url": "http://localhost:8943/mcp"
}'
```

The daemon keeps the HTTP endpoint alive across Cursor reconnects, so this registration stays valid as long as the daemon is running.

See [docs/mcp-client-configs/](mcp-client-configs/) for detailed client configuration.

### Running multiple Claude Code sessions in parallel (Git worktrees)

Claude Code supports **Git worktrees natively** via the `-w` flag. You can run several Claude sessions at once, each on its own branch, without conflicts.

**Manual use (e.g. two terminals):**

```bash
# Terminal 1: work on feature-1
claude -w feature-1

# Terminal 2: work on feature-2
claude -w feature-2
```

Each session uses a separate worktree under `.claude/worktrees/`, so branches and files stay isolated. This is especially useful in monorepos (e.g. Pedregal), where you can run multiple streams of work simultaneously.

You can combine this with Stringwork: run `claude -w <branch>` in each terminal and connect to the same MCP server; each session can set its presence and claim tasks independently.

**Spawned workers:** To give each *spawned* Claude Code worker its own native worktree, set `use_claude_worktree: true` on the worker in your config:

```yaml
workers:
  - type: claude-code
    instances: 2
    use_claude_worktree: true   # each worker gets -w <instance_id>, e.g. claude-code-1, claude-code-2
    command: ["claude", "-p", "...", "--dangerously-skip-permissions"]
```

The **orchestrator** sets the worktree as part of scope when it assigns a task: the task's work context gets `worktree_name` set to the assigned instance ID (e.g. `claude-code-1`). The server then injects `-w <worktree_name>` when spawning that task's worker, so each worker runs in its own Claude worktree. Workers can see the scope (including `worktree_name`) via `get_work_context`. This complements (or replaces) Stringwork's optional git worktree isolation (`.stringwork/worktrees/`).

**Codex and Gemini** do not have a native `-w` worktree flag. For them, use **orchestration.worktrees** (see below): the server creates a git worktree per worker and runs the process with that directory as cwd, so each Codex or Gemini worker gets an isolated checkout. Codex also supports worktrees in its GUI (under `$CODEX_HOME/worktrees`); the CLI uses `--cd` for working directory. Gemini must be launched from within the worktree directory for tool access — which is exactly what the server does when worktrees are enabled.

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

> **Important:** The server spawns workers as child processes without sourcing shell
> profiles (`.bashrc`, `.zshrc`). If your CLI tools are installed via version managers
> (nvm, pyenv, etc.), use **full paths** in the `command` array (e.g.
> `/Users/you/.local/bin/claude` instead of `claude`). Find the path with `which <tool>`.

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

#### Gemini CLI workers

Install via npm: `npm install -g @google/gemini-cli`. Requires `GOOGLE_API_KEY` for auth.

```yaml
workers:
  - type: gemini
    instances: 1
    command: ["gemini", "--yolo", "--prompt", "...prompt..."]
    env:
      GOOGLE_API_KEY: "${GOOGLE_API_KEY}"
```

`--yolo` auto-approves all tool executions (no interactive prompts). `--prompt` runs in non-interactive headless mode.

> **nvm users:** The Gemini CLI is a Node.js script (`#!/usr/bin/env node`). Since the
> server spawns workers without sourcing shell profiles, both `gemini` and `node` must
> be resolvable. Use the full path to the binary **and** prepend the nvm bin directory
> to PATH:
>
> ```yaml
> command: ["/Users/you/.nvm/versions/node/v22.x.x/bin/gemini", "--yolo", "--prompt", "..."]
> env:
>   PATH: "/Users/you/.nvm/versions/node/v22.x.x/bin:${PATH}"
>   GOOGLE_API_KEY: "${GOOGLE_API_KEY}"
> ```
>
> Find your path with `which gemini` in a terminal where nvm is loaded.

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
| 5 minutes | Critical alert sent to driver, auto-cancellation imminent |
| 10 minutes (no heartbeat) | Worker auto-cancelled, output captured, task reset to pending |

Workers call:
- `heartbeat` every 60-90 seconds with a progress description. On first call, include `session_id` (CLI session/conversation ID) for session continuation on restart.
- `report_progress` every 2-3 minutes with task_id, description, percent_complete

The driver monitors all workers via `worker_status` and can cancel stuck ones with `cancel_agent`.

### Session continuation on restart

When a worker reports its CLI session ID via heartbeat, the server stores it. If the worker is cancelled and respawned (stuck, failed, manual restart), the server injects the appropriate resume flag into the spawn command:

| CLI | Flag injected |
|-----|--------------|
| Claude Code | `--resume <session-id>` |
| Codex | `--session <session-id>` |
| Gemini | `--resume <session-id>` |

If the resumed session fails (e.g. session expired), the server clears the stored ID and retries with a fresh session.

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

Requires the workspace to be a git repository. This applies to **all** worker types: the server sets each worker's process cwd to the worktree path. For **Claude Code**, you can additionally set `use_claude_worktree: true` so the Claude CLI gets `-w <name>` (Claude's own worktrees under `.claude/worktrees/`). Codex and Gemini have no equivalent CLI flag; they rely on this server-side worktree (cwd) for isolation.

## Dashboard

The web dashboard is available on the HTTP listener. The URL is logged on startup:

```
HTTP server on :54321
  Dashboard: http://localhost:54321/dashboard
```

With a fixed `http_port` (e.g. 8943), the URL is always `http://localhost:8943/dashboard`.

**Daemon mode advantage**: even with `http_port: 0`, the port is allocated once when the daemon starts and stays stable across Cursor window reconnects. The dashboard URL is printed in the log on daemon startup and remains accessible as long as the daemon is running.

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

## Step 5: Install Claude Code hooks (recommended)

Claude Code's `CLAUDE.md` instructions get a "may or may not be relevant" framing that weakens compliance. Stringwork ships hooks that bypass this by injecting rules as clean system-reminder messages.

```bash
./scripts/install-claude-hooks.sh    # install
./scripts/uninstall-claude-hooks.sh  # clean removal
```

This installs:
- Hook scripts to `~/.config/stringwork/hooks/`
- Hook config into `~/.claude/settings.json` (user level — works across all projects)
- `/pair-respond` command to `~/.claude/commands/`

The scripts have a guard: they only activate when `~/.config/stringwork/state.sqlite` exists, so they're harmless in non-Stringwork projects.

### What the hooks do

| Hook | Event | Purpose |
|------|-------|---------|
| `inject-rules.sh` | `SessionStart` | Injects mandatory rules at startup and after every context compaction |
| `inject-reminder.sh` | `UserPromptSubmit` | Short reminder on every prompt (~30 tokens) |
| `stop-check.sh` | `Stop` | Reminds Claude to report findings before finishing |

### For development

```bash
./scripts/dev-install.sh   # build + install binary + reset db + reinstall hooks + restart daemon
```

## Next Steps

- [WORKFLOW.md](WORKFLOW.md) -- collaboration patterns and best practices
- [QUICK_REFERENCE.md](QUICK_REFERENCE.md) -- tool usage examples
