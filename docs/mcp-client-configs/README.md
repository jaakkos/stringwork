# MCP Client Configurations

Configuration examples for connecting to the Stringwork server.

**Cursor (driver)** connects via stdio -- add to `.cursor/mcp.json`:

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

The server runs stdio for Cursor and starts an HTTP listener for workers and the dashboard. No daemon needed.

See individual config files:

- [cursor-config.md](cursor-config.md) -- Cursor setup
- [claude-code-config.md](claude-code-config.md) -- Claude Code CLI setup (manual use)
