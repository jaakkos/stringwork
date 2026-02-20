#!/bin/bash
# Verify mcp-stringwork binary is available on PATH.
# Runs as a Cursor plugin sessionStart hook.

if ! command -v mcp-stringwork &>/dev/null; then
  cat <<'MSG'
Stringwork binary not found. The MCP server requires mcp-stringwork on your PATH.

Install with:
  curl -fsSL https://raw.githubusercontent.com/jaakkos/stringwork/main/scripts/install.sh | sh

Then ensure ~/.local/bin is in your PATH:
  export PATH="$HOME/.local/bin:$PATH"

Restart Cursor after installing.
MSG
  exit 1
fi
