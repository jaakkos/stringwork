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

With daemon mode enabled, the first Cursor window starts a background daemon. Subsequent windows share it as lightweight proxies. Without daemon mode, each window runs its own server.

See individual config files:

- [cursor-config.md](cursor-config.md) -- Cursor setup
- [claude-code-config.md](claude-code-config.md) -- Claude Code CLI setup (manual use)
