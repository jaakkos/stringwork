#!/usr/bin/env bash
# MCP Stringwork server – start/stop/status (manual or launchd)
# For HTTP mode: set transport: "http" and http_port in your config (see mcp/config.yaml).

set -e

NAME="mcp-stringwork"
PLIST_LABEL="com.stringwork.mcp"
PLIST_NAME="${PLIST_LABEL}.plist"
LAUNCH_AGENTS="${HOME}/Library/LaunchAgents"
CONFIG_DIR="${HOME}/.config/stringwork"
PID_FILE="${CONFIG_DIR}/${NAME}.pid"
LOG_DIR="${CONFIG_DIR}"

# Resolve script dir and repo root (parent of script dir)
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

# Build binary in repo if Go is available and we have the main package. Used before start/run/restart.
build_binary() {
  if [[ -n "${MCP_STRINGWORK_BINARY}" ]]; then
    return 0
  fi
  if ! command -v go &>/dev/null; then
    return 1
  fi
  if [[ ! -f "${REPO_ROOT}/go.mod" ]]; then
    return 1
  fi
  if [[ ! -d "${REPO_ROOT}/cmd/mcp-server" ]]; then
    return 1
  fi
  (cd "${REPO_ROOT}" && go build -o "${NAME}" ./cmd/mcp-server) || return 1
}

# Binary: MCP_STRINGWORK_BINARY env, or repo binary, or PATH
resolve_binary() {
  if [[ -n "${MCP_STRINGWORK_BINARY}" && -x "${MCP_STRINGWORK_BINARY}" ]]; then
    echo "${MCP_STRINGWORK_BINARY}"
    return
  fi
  local repo_binary="${REPO_ROOT}/${NAME}"
  if [[ -x "${repo_binary}" ]]; then
    echo "${repo_binary}"
    return
  fi
  if command -v "${NAME}" &>/dev/null; then
    command -v "${NAME}"
    return
  fi
  echo ""
}

# Config: MCP_CONFIG env, or global config, or repo config
resolve_config() {
  if [[ -n "${MCP_CONFIG}" && -f "${MCP_CONFIG}" ]]; then
    echo "${MCP_CONFIG}"
    return
  fi
  local global_config="${CONFIG_DIR}/config.yaml"
  if [[ -f "${global_config}" ]]; then
    echo "${global_config}"
    return
  fi
  local repo_config="${REPO_ROOT}/mcp/config.yaml"
  if [[ -f "${repo_config}" ]]; then
    echo "${repo_config}"
    return
  fi
  echo ""
}

usage() {
  echo "Usage: $0 {run|start|stop|status|restart|install-launchd|uninstall-launchd}" >&2
  echo "" >&2
  echo "  run              Run server in foreground (for debugging)" >&2
  echo "  start            Start server in background (writes pid to ${PID_FILE})" >&2
  echo "  stop             Stop server started with start" >&2
  echo "  status           Show if server is running and /health response" >&2
  echo "  restart          stop then start" >&2
  echo "  install-launchd  Install and load launchd user agent (start at login)" >&2
  echo "  uninstall-launchd Unload and remove launchd agent" >&2
  echo "" >&2
  echo "For start/run: set transport: \"http\" (and optional http_port) in config for daemon mode." >&2
  echo "Worker spawn: set orchestration.driver and orchestration.workers in config (see mcp/config.yaml)." >&2
  echo "Binary: start/run/restart always build from source in repo; or set MCP_STRINGWORK_BINARY to skip build." >&2
  echo "Config: MCP_CONFIG or ${CONFIG_DIR}/config.yaml or ${REPO_ROOT}/mcp/config.yaml" >&2
  exit 1
}

cmd_run() {
  if [[ -z "${MCP_STRINGWORK_BINARY}" ]]; then
    echo "Building ${NAME}..."
    build_binary || true
  fi
  local binary
  binary="$(resolve_binary)"
  local config
  config="$(resolve_config)"
  if [[ -z "${binary}" || -z "${config}" ]]; then
    echo "Error: binary or config not found (see usage)" >&2
    exit 1
  fi
  export MCP_CONFIG="${config}"
  exec "${binary}"
}

cmd_start() {
  if [[ -z "${MCP_STRINGWORK_BINARY}" ]]; then
    echo "Building ${NAME}..."
    build_binary || true
  fi
  local binary
  binary="$(resolve_binary)"
  if [[ -z "${binary}" ]]; then
    echo "Error: ${NAME} binary not found. Run from repo with Go, or set MCP_STRINGWORK_BINARY." >&2
    exit 1
  fi

  local config
  config="$(resolve_config)"
  if [[ -z "${config}" ]]; then
    echo "Error: No config file found. Set MCP_CONFIG or create ${CONFIG_DIR}/config.yaml or ${REPO_ROOT}/mcp/config.yaml" >&2
    exit 1
  fi

  # Check pid file
  if [[ -f "${PID_FILE}" ]]; then
    local pid
    pid="$(cat "${PID_FILE}")"
    if kill -0 "${pid}" 2>/dev/null; then
      echo "Already running (pid ${pid})"
      return 0
    fi
    rm -f "${PID_FILE}"
  fi

  # Stop any orphan/pidless server first
  local orphan_pids
  orphan_pids="$(find_server_pids)"
  if [[ -n "${orphan_pids}" ]]; then
    echo "Found orphan server process(es), stopping first..."
    while IFS= read -r pid; do
      kill "${pid}" 2>/dev/null || true
      echo "  Stopped orphan (pid ${pid})"
    done <<< "${orphan_pids}"
    sleep 1
    # Force kill any remaining
    orphan_pids="$(find_server_pids)"
    if [[ -n "${orphan_pids}" ]]; then
      while IFS= read -r pid; do
        kill -9 "${pid}" 2>/dev/null || true
      done <<< "${orphan_pids}"
      sleep 1
    fi
  fi

  mkdir -p "${CONFIG_DIR}" "${LOG_DIR}"
  export MCP_CONFIG="${config}"
  echo "Starting ${NAME} (config=${config}) ..."
  nohup "${binary}" </dev/null >> "${LOG_DIR}/${NAME}.log" 2>&1 &
  echo $! > "${PID_FILE}"

  # Wait for process to be alive
  sleep 1
  if ! kill -0 "$(cat "${PID_FILE}")" 2>/dev/null; then
    echo "Process exited immediately. Check log: ${LOG_DIR}/${NAME}.log" >&2
    rm -f "${PID_FILE}"
    exit 1
  fi

  # Poll /health endpoint to confirm MCP is ready (up to 10 seconds)
  local port="${MCP_HTTP_PORT:-8943}"
  local health_url="http://localhost:${port}/health"
  local max_wait=10
  local waited=0
  echo -n "Waiting for MCP endpoint readiness "
  while [[ ${waited} -lt ${max_wait} ]]; do
    if curl -sf "${health_url}" >/dev/null 2>&1; then
      echo ""
      echo "Started (pid $(cat "${PID_FILE}")), MCP ready at ${health_url}. Log: ${LOG_DIR}/${NAME}.log"
      return 0
    fi
    echo -n "."
    sleep 1
    waited=$((waited + 1))
  done
  echo ""
  echo "Warning: process running (pid $(cat "${PID_FILE}")) but /health not responding after ${max_wait}s." >&2
  echo "The server may still be starting. Check log: ${LOG_DIR}/${NAME}.log" >&2
}

# Find running server PIDs by process name and port (fallback when no pid file).
# Only matches actual mcp-stringwork binary processes, not editors/scripts/helpers.
find_server_pids() {
  local pids=""

  # By process name — strict matching on the binary path/name
  local name_pids
  name_pids="$(pgrep -x "${NAME}" 2>/dev/null)" || true
  if [[ -z "${name_pids}" ]]; then
    # Fallback: match command line but verify the binary itself
    name_pids="$(pgrep -f "${NAME}" 2>/dev/null | while read -r p; do
      local comm cmdline
      comm="$(ps -p "$p" -o comm= 2>/dev/null)" || continue
      cmdline="$(ps -p "$p" -o args= 2>/dev/null)" || continue
      # Must be the actual binary, not a script/editor/helper wrapping it
      case "${comm}" in
        *${NAME}*) echo "$p" ;;
        *)
          # Check if the first arg is our binary (e.g. "./mcp-stringwork" or full path)
          local first_arg="${cmdline%% *}"
          case "${first_arg}" in
            *${NAME}*) echo "$p" ;;
          esac
          ;;
      esac
    done)" || true
  fi
  pids="${name_pids}"

  # By port — only LISTEN sockets (server, not clients connecting to it)
  local port="${MCP_HTTP_PORT:-8943}"
  local port_pids
  port_pids="$(lsof -ti ":${port}" -sTCP:LISTEN 2>/dev/null)" || true
  if [[ -n "${port_pids}" ]]; then
    if [[ -n "${pids}" ]]; then
      pids="${pids}"$'\n'"${port_pids}"
    else
      pids="${port_pids}"
    fi
  fi

  # Deduplicate
  if [[ -n "${pids}" ]]; then
    echo "${pids}" | sort -un
  fi
}

cmd_stop() {
  local stopped=false

  # Try pid file first
  if [[ -f "${PID_FILE}" ]]; then
    local pid
    pid="$(cat "${PID_FILE}")"
    if kill -0 "${pid}" 2>/dev/null; then
      kill "${pid}" 2>/dev/null || true
      echo "Stopped (pid ${pid}, from pid file)"
      stopped=true
    else
      echo "Stale pid file (pid ${pid} not running), removing"
    fi
    rm -f "${PID_FILE}"
  fi

  # Fallback: find and kill any orphan server processes (started without pid file)
  local orphan_pids
  orphan_pids="$(find_server_pids)"
  if [[ -n "${orphan_pids}" ]]; then
    while IFS= read -r pid; do
      if kill -0 "${pid}" 2>/dev/null; then
        kill "${pid}" 2>/dev/null || true
        echo "Stopped orphan process (pid ${pid})"
        stopped=true
      fi
    done <<< "${orphan_pids}"
    # Wait for processes to exit
    sleep 1
    # Force kill any stubborn ones
    orphan_pids="$(find_server_pids)"
    if [[ -n "${orphan_pids}" ]]; then
      while IFS= read -r pid; do
        if kill -0 "${pid}" 2>/dev/null; then
          kill -9 "${pid}" 2>/dev/null || true
          echo "Force killed (pid ${pid})"
        fi
      done <<< "${orphan_pids}"
    fi
  fi

  if [[ "${stopped}" == false ]]; then
    echo "Not running"
  fi
  return 0
}

cmd_status() {
  local port="${MCP_HTTP_PORT:-8943}"
  local found=false

  # Check pid file
  if [[ -f "${PID_FILE}" ]]; then
    local pid
    pid="$(cat "${PID_FILE}")"
    if kill -0 "${pid}" 2>/dev/null; then
      echo "Running (pid ${pid}, managed)"
      found=true
    else
      echo "Stale pid file (pid ${pid} not running), removing"
      rm -f "${PID_FILE}"
    fi
  fi

  # Check for orphan/pidless processes
  if [[ "${found}" == false ]]; then
    local orphan_pids
    orphan_pids="$(find_server_pids)"
    if [[ -n "${orphan_pids}" ]]; then
      echo "Running WITHOUT pid file (orphan):"
      while IFS= read -r pid; do
        local cmdline
        cmdline="$(ps -p "$pid" -o args= 2>/dev/null)" || cmdline="(unknown)"
        echo "  pid ${pid}: ${cmdline}"
      done <<< "${orphan_pids}"
      echo "  Tip: run '$0 restart' to adopt with pid file"
      found=true
    fi
  fi

  # Health check
  if [[ "${found}" == true ]] && command -v curl &>/dev/null; then
    local health body
    health="$(curl -s -o /dev/null -w "%{http_code}" "http://127.0.0.1:${port}/health" 2>/dev/null)" || true
    if [[ "${health}" == "200" ]]; then
      body="$(curl -s "http://127.0.0.1:${port}/health" 2>/dev/null)" || true
      echo "  Health: OK (${body})"
    elif [[ -n "${health}" ]]; then
      echo "  Health: HTTP ${health}"
    fi
  fi

  if [[ "${found}" == false ]]; then
    echo "Not running"
  fi
  return 0
}

cmd_restart() {
  cmd_stop
  sleep 1
  cmd_start
}

# Generate plist content. Uses CONFIG_DIR and binary path.
generate_plist() {
  local binary
  binary="$(resolve_binary)"
  local config
  config="$(resolve_config)"
  if [[ -z "${binary}" || -z "${config}" ]]; then
    echo "Error: Could not resolve binary or config for launchd" >&2
    exit 1
  fi
  mkdir -p "${CONFIG_DIR}"
  cat <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>${PLIST_LABEL}</string>
  <key>ProgramArguments</key>
  <array>
    <string>${binary}</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>MCP_CONFIG</key>
    <string>${config}</string>
  </dict>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>${LOG_DIR}/${NAME}.log</string>
  <key>StandardErrorPath</key>
  <string>${LOG_DIR}/${NAME}.log</string>
</dict>
</plist>
EOF
}

cmd_install_launchd() {
  if [[ "$(uname -s)" != "Darwin" ]]; then
    echo "launchd is only supported on macOS" >&2
    exit 1
  fi
  local binary
  binary="$(resolve_binary)"
  local config
  config="$(resolve_config)"
  if [[ -z "${binary}" || -z "${config}" ]]; then
    echo "Error: Resolve binary and config first (see usage)" >&2
    exit 1
  fi
  mkdir -p "${LAUNCH_AGENTS}"
  local plist_path="${LAUNCH_AGENTS}/${PLIST_NAME}"
  generate_plist > "${plist_path}"
  echo "Installed ${plist_path}"
  launchctl unload "${plist_path}" 2>/dev/null || true
  launchctl load "${plist_path}"
  echo "Loaded. Server will start at login and restart if it exits."
}

cmd_uninstall_launchd() {
  if [[ "$(uname -s)" != "Darwin" ]]; then
    echo "launchd is only supported on macOS" >&2
    exit 1
  fi
  local plist_path="${LAUNCH_AGENTS}/${PLIST_NAME}"
  if [[ -f "${plist_path}" ]]; then
    launchctl unload "${plist_path}" 2>/dev/null || true
    rm -f "${plist_path}"
    echo "Uninstalled ${plist_path}"
  else
    echo "Not installed (no ${plist_path})"
  fi
}

case "${1:-}" in
  run)     cmd_run ;;
  start)   cmd_start ;;
  stop)    cmd_stop ;;
  status)  cmd_status ;;
  restart) cmd_restart ;;
  install-launchd)   cmd_install_launchd ;;
  uninstall-launchd) cmd_uninstall_launchd ;;
  *)       usage ;;
esac
