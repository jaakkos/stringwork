#!/bin/bash
# Local development install: build, install binary, install hooks, copy config.
# Run after code changes for a quick test loop.
#
# Usage: ./scripts/dev-install.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_DIR="$(dirname "$SCRIPT_DIR")"
INSTALL_DIR="${HOME}/.local/bin"
CONFIG_DIR="${HOME}/.config/stringwork"
BINARY_NAME="mcp-stringwork"

cd "$REPO_DIR"

echo "=== dev-install: build + install ==="

# 1. Build
echo "Building..."
go build -o "$BINARY_NAME" ./cmd/mcp-server
echo "  ✓ Built $BINARY_NAME"

# 2. Install binary
mkdir -p "$INSTALL_DIR"
mv "$BINARY_NAME" "$INSTALL_DIR/$BINARY_NAME"
echo "  ✓ Installed to $INSTALL_DIR/$BINARY_NAME"

# 3. Ensure config directory exists (don't overwrite user's config.yaml)
mkdir -p "$CONFIG_DIR"
if [ ! -f "$CONFIG_DIR/config.yaml" ]; then
    cp mcp/config.yaml "$CONFIG_DIR/config.yaml"
    echo "  ✓ Copied default config.yaml"
else
    echo "  - Config exists, not overwritten (user-customized)"
fi

# 4. Stop all running mcp-stringwork processes
echo ""
echo "Stopping all mcp-stringwork processes..."
pkill -f "$BINARY_NAME" 2>/dev/null && sleep 1 || true
rm -f "$CONFIG_DIR/daemon.pid" "$CONFIG_DIR/server.sock"
echo "  ✓ All processes stopped"

# 5. Reset state database and WAL sidecars (clean slate for testing)
rm -f "$CONFIG_DIR/state.sqlite" "$CONFIG_DIR/state.sqlite-wal" "$CONFIG_DIR/state.sqlite-shm"
echo "  ✓ Reset state.sqlite"

# 6. Reinstall Claude Code hooks (clean slate so script changes take effect)
echo ""
bash "$SCRIPT_DIR/uninstall-claude-hooks.sh"
echo ""
bash "$SCRIPT_DIR/install-claude-hooks.sh"

echo ""
echo "=== Ready! ==="
echo "Binary:  $INSTALL_DIR/$BINARY_NAME"
echo "Config:  $CONFIG_DIR/config.yaml"
echo "Hooks:   ~/.config/stringwork/hooks/"
