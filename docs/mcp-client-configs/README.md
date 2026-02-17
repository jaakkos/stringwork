# MCP client configuration

How to connect **Cursor** and **Claude Code** to the stringwork MCP server.

| Client        | Config guide |
|---------------|--------------|
| **Cursor**    | [cursor-config.md](cursor-config.md) – stdio or SSE (HTTP daemon) |
| **Claude Code** | [claude-code-config.md](claude-code-config.md) – stdio or Streamable HTTP (HTTP daemon) |

**This project:**

- **Cursor:** already configured in [`.cursor/mcp.json`](../../.cursor/mcp.json) (stdio, uses `mcp/config.yaml`).
- **Claude Code:** use the **`claude` CLI** to add the server: `claude mcp add-json --scope user stringwork '...'` (see [claude-code-config.md](claude-code-config.md)); use the same binary path and `MCP_CONFIG` as in `.cursor/mcp.json`.

Server config (transport, port, auto_respond, etc.) is in [../../mcp/config.yaml](../../mcp/config.yaml). To run the server as a daemon (HTTP mode), use the [startup script](../DAEMON_SETUP.md) (`scripts/mcp-server-daemon.sh start` or `install-launchd`).
