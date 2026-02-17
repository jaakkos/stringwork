# Daemon Setup (Deprecated)

The daemon script (`scripts/mcp-server-daemon.sh`) is no longer needed.

Stringwork now runs as a **Cursor subprocess**:
- Cursor starts the server via stdio when you open a project
- The server automatically starts an HTTP listener for workers and the dashboard
- When Cursor closes, the server shuts down

See [SETUP_GUIDE.md](SETUP_GUIDE.md) for current setup instructions.
