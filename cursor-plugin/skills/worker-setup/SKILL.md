---
name: worker-setup
description: Guide through configuring Stringwork workers (Claude Code, Codex, Gemini) in config.yaml. Use when adding, removing, or troubleshooting worker agents.
---

# Worker Setup Guide

## When to Use

- Adding a new worker type (Claude Code, Codex, Gemini, or a custom agent)
- Adjusting worker count, timeouts, or retry settings
- Troubleshooting worker spawn failures
- Configuring environment variables for workers

## Config Location

The worker config lives in the `orchestration.workers` section of your config file:
- Default: `~/.config/stringwork/config.yaml`
- Override: set `MCP_STRINGWORK_CONFIG` env var

## Worker Configuration Fields

Each worker entry supports:

```yaml
- type: claude-code          # agent type name
  instances: 1               # max concurrent instances
  command: ["claude", ...]   # command + args to spawn the worker
  cooldown_seconds: 30       # min seconds between spawns
  timeout_seconds: 600       # kill worker after this long
  max_retries: 2             # retry count on failure
  retry_delay_seconds: 15    # delay between retries
  env:                       # extra env vars (${VAR} expands from parent)
    GH_TOKEN: "${GH_TOKEN}"
  inherit_env: []            # glob patterns for parent env passthrough (empty = inherit all)
```

## Worker Templates

### Claude Code

```yaml
- type: claude-code
  instances: 1
  command: ["claude", "-p", "You are claude-code, a worker in a pair programming session. Workspace: {workspace}.\n\nMANDATORY: heartbeat every 60-90s, report_progress every 2-3min, send_message before finishing.\n\nSteps:\n1) set_presence agent='claude-code' status='working' workspace='{workspace}'\n2) read_messages for 'claude-code'\n3) list_tasks assigned_to='claude-code'\n4) Work on tasks, reporting progress\n5) send_message with detailed findings\n6) update_task status='completed'", "--dangerously-skip-permissions"]
  cooldown_seconds: 30
  timeout_seconds: 600
  max_retries: 2
  env:
    GH_TOKEN: "${GH_TOKEN}"
    SSH_AUTH_SOCK: "${SSH_AUTH_SOCK}"
```

### Codex

```yaml
- type: codex
  instances: 1
  command: ["codex", "exec", "--sandbox", "danger-full-access", "--skip-git-repo-check", "You are codex in a pair programming session. Workspace: {workspace}. Steps: 1) set_presence 2) read_messages 3) list_tasks 4) Work 5) report_progress 6) send_message."]
  cooldown_seconds: 30
  timeout_seconds: 600
  max_retries: 2
  env:
    GH_TOKEN: "${GH_TOKEN}"
```

### Gemini

```yaml
- type: gemini
  instances: 1
  command: ["gemini", "--yolo", "--prompt", "You are gemini in a pair programming session. Workspace: {workspace}. Steps: 1) set_presence 2) read_messages 3) list_tasks 4) Work 5) report_progress 6) send_message."]
  cooldown_seconds: 30
  timeout_seconds: 600
  max_retries: 2
  env:
    GOOGLE_API_KEY: "${GOOGLE_API_KEY}"
```

## Troubleshooting Worker Issues

### Worker not spawning
1. Check that the command is installed: `command -v claude` (or codex, gemini)
2. Verify the command works standalone: run it directly in your terminal
3. Check server logs: `~/.config/stringwork/mcp-stringwork.log`

### Worker fails immediately
- **"command not found"**: Install the CLI tool or fix the PATH
- **"API key expired"**: Renew credentials; check env vars in config
- **"quota exhausted"**: Wait for quota reset; the server will auto-retry if a reset time is detected

### Worker times out
- Increase `timeout_seconds` for long-running tasks
- Set `expected_duration_seconds` on tasks for SLA alerts instead of hard kills

### Adjusting concurrency
- Set `instances: 2` (or more) for a worker type to run multiple instances in parallel
- Each instance gets a unique ID (e.g., `claude-code-1`, `claude-code-2`)
- With git worktree isolation enabled, each instance gets its own checkout
