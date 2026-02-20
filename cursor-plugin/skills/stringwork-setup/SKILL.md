---
name: stringwork-setup
description: Guide through installing and configuring the Stringwork MCP pair-programming server. Use when setting up Stringwork for the first time or troubleshooting a broken installation.
---

# Stringwork Setup

## When to Use

- First-time Stringwork installation
- Verifying an existing installation
- Troubleshooting MCP connection issues
- Updating the binary to a new version

## Steps

### 1. Check if the binary is installed

Run in the terminal:

```bash
command -v mcp-stringwork && mcp-stringwork --version
```

If not found, install it:

```bash
curl -fsSL https://raw.githubusercontent.com/jaakkos/stringwork/main/scripts/install.sh | sh
```

This installs to `~/.local/bin/`. Use `--dir /usr/local/bin` for a different location. Verify `~/.local/bin` is on your PATH.

### 2. Create a config file

If `~/.config/stringwork/config.yaml` doesn't exist, create it:

```bash
mkdir -p ~/.config/stringwork
curl -fsSL https://raw.githubusercontent.com/jaakkos/stringwork/main/mcp/config.yaml > ~/.config/stringwork/config.yaml
```

Edit the config to set your workspace and workers. Key sections:
- `workspace_root` — your project path (or leave empty for dynamic via `set_presence`)
- `daemon.enabled: true` — recommended for multi-window support
- `orchestration.workers` — configure which agents to use (claude-code, codex, gemini)

### 3. Configure MCP in Cursor

If this plugin is installed, the MCP server is auto-configured. Otherwise, add to `.cursor/mcp.json`:

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

### 4. Verify the connection

Try calling any Stringwork MCP tool:

```
get_session_context for 'cursor'
```

If it works, you should see session context with presence, messages, and tasks.

### 5. Test worker spawning

Create a simple test task:

```
create_task title='Test task: verify worker connectivity' assigned_to='any' created_by='cursor'
```

Watch `worker_status` to see if a worker spawns and claims the task.

## Troubleshooting

- **Binary not found**: Ensure `~/.local/bin` is in your PATH. Add `export PATH="$HOME/.local/bin:$PATH"` to your shell profile.
- **MCP connection failed**: Restart Cursor after adding the MCP config. Check that the config path is correct.
- **Workers not spawning**: Verify worker commands in config.yaml. Test them directly in your terminal (e.g., `claude --version`).
- **Config not found**: Set `MCP_STRINGWORK_CONFIG` env var to your config path, or ensure `~/.config/stringwork/config.yaml` exists.
