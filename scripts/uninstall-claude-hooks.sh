#!/bin/bash
# Uninstall Stringwork hooks from Claude Code.
#
# What this removes:
#   1. Hook scripts from ~/.config/stringwork/hooks/
#   2. Hook config from ~/.claude/settings.json (preserves other settings)
#   3. /pair-respond command from ~/.claude/commands/
#
# Usage: ./scripts/uninstall-claude-hooks.sh

set -euo pipefail

HOOKS_DIR="$HOME/.config/stringwork/hooks"
CLAUDE_SETTINGS="$HOME/.claude/settings.json"

echo "=== Stringwork: Uninstalling Claude Code hooks ==="

# 1. Remove hook scripts
if [ -d "$HOOKS_DIR" ]; then
    rm -f "$HOOKS_DIR/inject-rules.sh" "$HOOKS_DIR/inject-reminder.sh" "$HOOKS_DIR/stop-check.sh"
    # Remove directory only if empty
    rmdir "$HOOKS_DIR" 2>/dev/null && echo "  ✓ Removed $HOOKS_DIR" || echo "  ✓ Removed hook scripts (directory kept — has other files)"
else
    echo "  - No hooks directory found at $HOOKS_DIR"
fi

# 2. Remove hooks from ~/.claude/settings.json
if [ -f "$CLAUDE_SETTINGS" ]; then
    if command -v jq &>/dev/null; then
        EXISTING=$(cat "$CLAUDE_SETTINGS")
        HAS_HOOKS=$(echo "$EXISTING" | jq 'has("hooks")')

        if [ "$HAS_HOOKS" = "true" ]; then
            # Remove just the "hooks" key, keep everything else
            echo "$EXISTING" | jq 'del(.hooks)' > "$CLAUDE_SETTINGS.tmp"

            # Check if the result is just {}
            REMAINING=$(cat "$CLAUDE_SETTINGS.tmp" | jq 'keys | length')
            if [ "$REMAINING" -eq 0 ]; then
                rm -f "$CLAUDE_SETTINGS.tmp" "$CLAUDE_SETTINGS"
                echo "  ✓ Removed $CLAUDE_SETTINGS (was empty after removing hooks)"
            else
                mv "$CLAUDE_SETTINGS.tmp" "$CLAUDE_SETTINGS"
                echo "  ✓ Removed hooks from $CLAUDE_SETTINGS (kept other settings)"
            fi
        else
            echo "  - No hooks found in $CLAUDE_SETTINGS"
        fi
    else
        echo "  ⚠ jq not found. Please manually remove the \"hooks\" key from $CLAUDE_SETTINGS"
    fi
else
    echo "  - No settings file at $CLAUDE_SETTINGS"
fi

# 3. Remove pair-respond command
PAIR_RESPOND="$HOME/.claude/commands/pair-respond.md"
if [ -f "$PAIR_RESPOND" ]; then
    rm -f "$PAIR_RESPOND"
    echo "  ✓ Removed /pair-respond command"
else
    echo "  - No /pair-respond command found"
fi

echo ""
echo "=== Done! ==="
echo "Restart Claude Code for changes to take effect."
