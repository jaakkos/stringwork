#!/bin/bash
# Install Stringwork hooks for Claude Code.
#
# Hooks bypass Claude Code's "may or may not be relevant" framing for CLAUDE.md
# by injecting rules as clean system-reminder messages.
#
# What this does:
#   1. Installs thin shim scripts to ~/.config/stringwork/hooks/ that delegate
#      to `mcp-stringwork hooks emit` — role/platform resolution lives in the
#      Go binary (internal/app.ResolveHookRole / ShouldEmitHook), not in bash.
#   2. Merges hook config into ~/.claude/settings.json (preserves existing settings)
#
# The shims have a guard — they only activate when ~/.config/stringwork/state.sqlite
# exists, so they're harmless in non-Stringwork projects.
#
# `mcp-stringwork hooks emit` decides WHETHER to print anything based on the
# resolved role for THIS session:
#   - Driver sessions (orchestration.driver == "auto" or == this platform,
#     and STRINGWORK_AGENT is unset) print nothing by default — a human
#     driving the pair-programming session doesn't need worker
#     progress-reporting reminders.
#   - Worker sessions (STRINGWORK_AGENT set, or this platform isn't the
#     configured driver) print the mandatory rules, same as before this
#     config existed.
# Override per-platform/per-role behavior in ~/.config/stringwork/config.yaml
# under `hooks:` — see docs/CONSTITUTION.md and mcp/config.yaml comments.
#
# Usage:
#   Install:   ./scripts/install-claude-hooks.sh [--platform NAME]
#   Uninstall: ./scripts/uninstall-claude-hooks.sh
#
#   --platform NAME   Platform identity to report to `hooks emit` (default:
#                      claude-code). Only change this if you're adapting
#                      these shims for a different Claude-Code-like CLI that
#                      should be treated as a distinct hooks.platforms.<name>
#                      entry in config.yaml.

set -euo pipefail

PLATFORM="claude-code"
while [ $# -gt 0 ]; do
    case "$1" in
        --platform)
            PLATFORM="$2"
            shift 2
            ;;
        --platform=*)
            PLATFORM="${1#--platform=}"
            shift
            ;;
        *)
            echo "unknown argument: $1" >&2
            exit 1
            ;;
    esac
done

HOOKS_DIR="$HOME/.config/stringwork/hooks"
CLAUDE_SETTINGS="$HOME/.claude/settings.json"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_DIR="$(dirname "$SCRIPT_DIR")"

echo "=== Stringwork: Installing Claude Code hooks (platform: $PLATFORM) ==="

# Resolve the mcp-stringwork binary once at install time and bake the
# resolved path into the shims. Falls back to bare `mcp-stringwork` (resolved
# via PATH at hook-fire time) if we can't find it now — e.g. running this
# script before the binary is built/installed anywhere.
STRINGWORK_BIN="$(command -v mcp-stringwork 2>/dev/null || true)"
if [ -z "$STRINGWORK_BIN" ] && [ -x "$HOME/.local/bin/mcp-stringwork" ]; then
    STRINGWORK_BIN="$HOME/.local/bin/mcp-stringwork"
fi
if [ -z "$STRINGWORK_BIN" ]; then
    echo "  ⚠ mcp-stringwork binary not found on PATH or in ~/.local/bin/ — shims"
    echo "    will resolve it via PATH when they fire. Run dev-install.sh first"
    echo "    if hooks don't produce output after installing."
    STRINGWORK_BIN="mcp-stringwork"
fi

# 1. Install hook shims
echo "Installing hook shims to $HOOKS_DIR ..."
mkdir -p "$HOOKS_DIR"

write_shim() {
    local file="$1" event="$2"
    cat > "$file" <<SHIM
#!/bin/bash
# Thin shim — role/platform decisions live in the Go binary
# (internal/app.ResolveHookRole / ShouldEmitHook). Regenerate with
# scripts/install-claude-hooks.sh, do not hand-edit.
[ -f "\$HOME/.config/stringwork/state.sqlite" ] || exit 0
exec "$STRINGWORK_BIN" hooks emit --event $event --platform $PLATFORM 2>/dev/null
SHIM
}

write_shim "$HOOKS_DIR/inject-rules.sh" "session_start"
write_shim "$HOOKS_DIR/inject-reminder.sh" "user_prompt"
write_shim "$HOOKS_DIR/stop-check.sh" "stop"

chmod +x "$HOOKS_DIR"/*.sh
echo "  ✓ Hook shims installed (delegating to: $STRINGWORK_BIN)"

# 2. Merge hooks into ~/.claude/settings.json
#
# IMPORTANT: this merges SURGICALLY at the individual hook-entry level, not
# by replacing the whole "hooks" key. `. + {hooks: $hooks}` (the old
# approach) silently destroyed any unrelated hooks the user had configured
# under SessionStart/UserPromptSubmit/Stop, and entirely wiped out other
# event keys (PreToolUse, PostToolUse, etc.) since those aren't in our
# HOOKS_JSON at all. Every jq step below touches only the three entries we
# own (matched by command path), leaving everything else in settings.json
# untouched — including other SessionStart/UserPromptSubmit/Stop entries
# from other tools.
echo "Merging hooks into $CLAUDE_SETTINGS ..."
mkdir -p "$(dirname "$CLAUDE_SETTINGS")"

# Literal $HOME (not shell-expanded) — Claude Code expands this itself at
# hook-execution time, matching how the shims are referenced in the JSON.
SESSION_START_CMD='$HOME/.config/stringwork/hooks/inject-rules.sh'
USER_PROMPT_CMD='$HOME/.config/stringwork/hooks/inject-reminder.sh'
STOP_CMD='$HOME/.config/stringwork/hooks/stop-check.sh'

if [ -f "$CLAUDE_SETTINGS" ]; then
    if command -v jq &>/dev/null; then
        jq \
            --arg sessionStartCmd "$SESSION_START_CMD" \
            --arg userPromptCmd "$USER_PROMPT_CMD" \
            --arg stopCmd "$STOP_CMD" \
            '
            .hooks.SessionStart as $ss
            | .hooks.UserPromptSubmit as $ups
            | .hooks.Stop as $stop
            | .hooks.SessionStart = (
                [($ss // [])[] | select((.hooks[0].command // "") != $sessionStartCmd)]
                + [{"matcher": "", "hooks": [{"type": "command", "command": $sessionStartCmd, "timeout": 10}]}]
              )
            | .hooks.UserPromptSubmit = (
                [($ups // [])[] | select((.hooks[0].command // "") != $userPromptCmd)]
                + [{"hooks": [{"type": "command", "command": $userPromptCmd, "timeout": 5}]}]
              )
            | .hooks.Stop = (
                [($stop // [])[] | select((.hooks[0].command // "") != $stopCmd)]
                + [{"hooks": [{"type": "command", "command": $stopCmd, "timeout": 10}]}]
              )
            ' "$CLAUDE_SETTINGS" > "$CLAUDE_SETTINGS.tmp"
        mv "$CLAUDE_SETTINGS.tmp" "$CLAUDE_SETTINGS"
        echo "  ✓ Merged hooks into existing settings (other hooks preserved)"
    else
        echo "  ⚠ jq not found. Please manually add these entries to the SessionStart,"
        echo "    UserPromptSubmit, and Stop arrays under \"hooks\" in $CLAUDE_SETTINGS"
        echo "    (do not replace the whole \"hooks\" key — merge into existing arrays):"
        echo '    SessionStart:     {"matcher": "", "hooks": [{"type": "command", "command": "'"$SESSION_START_CMD"'", "timeout": 10}]}'
        echo '    UserPromptSubmit: {"hooks": [{"type": "command", "command": "'"$USER_PROMPT_CMD"'", "timeout": 5}]}'
        echo '    Stop:             {"hooks": [{"type": "command", "command": "'"$STOP_CMD"'", "timeout": 10}]}'
        exit 1
    fi
else
    jq -n \
        --arg sessionStartCmd "$SESSION_START_CMD" \
        --arg userPromptCmd "$USER_PROMPT_CMD" \
        --arg stopCmd "$STOP_CMD" \
        '{
            hooks: {
                SessionStart: [{"matcher": "", "hooks": [{"type": "command", "command": $sessionStartCmd, "timeout": 10}]}],
                UserPromptSubmit: [{"hooks": [{"type": "command", "command": $userPromptCmd, "timeout": 5}]}],
                Stop: [{"hooks": [{"type": "command", "command": $stopCmd, "timeout": 10}]}]
            }
        }' > "$CLAUDE_SETTINGS"
    echo "  ✓ Created $CLAUDE_SETTINGS with hooks"
fi

# 3. Install pair-respond command
if [ -f "$REPO_DIR/.claude/commands/pair-respond.md" ]; then
    mkdir -p "$HOME/.claude/commands"
    cp "$REPO_DIR/.claude/commands/pair-respond.md" "$HOME/.claude/commands/pair-respond.md"
    echo "  ✓ Installed /pair-respond command"
fi

echo ""
echo "=== Done! ==="
echo ""
echo "Hooks installed (platform: $PLATFORM):"
echo "  SessionStart     → mcp-stringwork hooks emit --event session_start"
echo "  UserPromptSubmit → mcp-stringwork hooks emit --event user_prompt"
echo "  Stop             → mcp-stringwork hooks emit --event stop"
echo ""
echo "Each fires only when ~/.config/stringwork/state.sqlite exists, and prints"
echo "nothing when this session resolves to the orchestration DRIVER role (see"
echo "internal/app.ResolveHookRole). Set orchestration.driver in"
echo "~/.config/stringwork/config.yaml to 'auto' or '$PLATFORM' to make Claude"
echo "Code sessions you drive directly silent; leave it as another platform"
echo "(e.g. cursor) and Claude Code stays a worker that gets full reminders."
echo ""
echo "Override individual events/roles under hooks.platforms.$PLATFORM in"
echo "config.yaml if the defaults don't fit — see mcp/config.yaml comments."
echo ""
echo "Restart Claude Code for hooks to take effect."
