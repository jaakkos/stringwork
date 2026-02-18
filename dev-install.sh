#!/usr/bin/env bash
set -euo pipefail

INSTALL_DIR="${HOME}/.local/bin"
BINARY="mcp-stringwork"
VERSION="dev-$(git describe --always --dirty 2>/dev/null || echo local)"

echo "Building ${BINARY} (${VERSION})..."
go build -ldflags="-s -w -X main.Version=${VERSION}" -o "${BINARY}" ./cmd/mcp-server

echo "Installing to ${INSTALL_DIR}/${BINARY}..."
mkdir -p "${INSTALL_DIR}"
rm -f "${INSTALL_DIR}/${BINARY}"
cp "${BINARY}" "${INSTALL_DIR}/${BINARY}"
rm -f "${BINARY}"

# Kill running server so Cursor restarts it with the new binary
PID=$(pgrep -f "${INSTALL_DIR}/${BINARY}" 2>/dev/null || true)
if [ -n "$PID" ]; then
  echo "Stopping running server (PID ${PID})..."
  kill "$PID" 2>/dev/null || true
  sleep 1
  # Force kill if still running
  if kill -0 "$PID" 2>/dev/null; then
    kill -9 "$PID" 2>/dev/null || true
  fi
  echo "Server stopped. Cursor will restart it automatically on next MCP call."
else
  echo "No running server found."
fi

echo ""
echo "Done. ${INSTALL_DIR}/${BINARY} (${VERSION})"
