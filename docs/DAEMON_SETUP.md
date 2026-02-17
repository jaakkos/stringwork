# MCP server daemon setup

When you run the MCP server in **HTTP mode** (so Cursor and Claude Code connect to one shared process), you need to start the server and keep it running. You can run it manually in the background or install a macOS launchd agent to start it at login.

## Prerequisites

1. **Config with HTTP transport**

   In your config file (e.g. `mcp/config.yaml` or `~/.config/stringwork/config.yaml`) set:

   ```yaml
   transport: "http"
   http_port: 8943
   ```

2. **Binary**

   Build the server:

   ```bash
   go build -o mcp-stringwork ./cmd/mcp-server
   ```

   Either run the script from the repo (so it finds `./mcp-stringwork`) or set `MCP_STRINGWORK_BINARY` to the full path.

## Startup script

Use the script in the repo:

```bash
./scripts/mcp-server-daemon.sh
```

### Commands

| Command | Description |
|---------|-------------|
| `run` | Run server in **foreground** (for debugging). Ctrl+C to stop. |
| `start` | Start server in **background**. Writes PID to `~/.config/stringwork/mcp-stringwork.pid`. |
| `stop` | Stop the server started with `start`. |
| `status` | Show whether the server is running and, if possible, HTTP /health. |
| `restart` | Stop then start. |
| `install-launchd` | Install a macOS user LaunchAgent so the server starts at login and restarts if it exits. |
| `uninstall-launchd` | Remove the LaunchAgent. |

### How the script finds binary and config

- **Binary:** `MCP_STRINGWORK_BINARY` env, or `./mcp-stringwork` in the repo root (parent of `scripts/`), or `mcp-stringwork` in your PATH.
- **Config:** `MCP_CONFIG` env, or `~/.config/stringwork/config.yaml`, or `mcp/config.yaml` in the repo.

### Examples

```bash
# From repo root (after: go build -o mcp-stringwork ./cmd/mcp-server)
./scripts/mcp-server-daemon.sh start
./scripts/mcp-server-daemon.sh status
./scripts/mcp-server-daemon.sh stop

# Run in foreground to see logs
./scripts/mcp-server-daemon.sh run

# Install launchd agent (macOS) – server starts at login
./scripts/mcp-server-daemon.sh install-launchd

# Uninstall launchd agent
./scripts/mcp-server-daemon.sh uninstall-launchd
```

### Global config (all projects)

To use one server for all projects, put config in the global directory:

```bash
mkdir -p ~/.config/stringwork
cp mcp/config.yaml ~/.config/stringwork/config.yaml
# Edit and set transport: "http" and http_port: 8943
./scripts/mcp-server-daemon.sh install-launchd
```

Then point Cursor (SSE URL) and Claude Code (Streamable HTTP URL) at `http://localhost:8943/sse` and `http://localhost:8943/mcp` as in [mcp-client-configs](mcp-client-configs/README.md).

## Logs

- **Manual start:** stdout/stderr when using `run`; when using `start`, output goes to `~/.config/stringwork/mcp-stringwork.log`.
- **launchd:** `~/Library/LaunchAgents/com.stringwork.mcp.plist` points StandardOutPath/StandardErrorPath to `~/.config/stringwork/mcp-stringwork.log`.

View logs:

```bash
tail -f ~/.config/stringwork/mcp-stringwork.log
```
