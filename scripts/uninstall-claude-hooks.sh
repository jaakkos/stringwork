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

# 2. Remove OUR entries from ~/.claude/settings.json
#
# IMPORTANT: this removes SURGICALLY by matching command path — only the
# SessionStart/UserPromptSubmit/Stop array entries pointing at our own hook
# scripts. `del(.hooks)` (the old approach) deleted every hook the user had
# configured for ANY tool (rtk, format-on-edit, test-discipline,
# pr-draft-guard, etc.), not just ours.
if [ -f "$CLAUDE_SETTINGS" ]; then
    if command -v jq &>/dev/null; then
        SESSION_START_CMD='$HOME/.config/stringwork/hooks/inject-rules.sh'
        USER_PROMPT_CMD='$HOME/.config/stringwork/hooks/inject-reminder.sh'
        STOP_CMD='$HOME/.config/stringwork/hooks/stop-check.sh'

        HAS_HOOKS=$(jq 'has("hooks")' "$CLAUDE_SETTINGS")
        if [ "$HAS_HOOKS" = "true" ]; then
            jq \
                --arg sessionStartCmd "$SESSION_START_CMD" \
                --arg userPromptCmd "$USER_PROMPT_CMD" \
                --arg stopCmd "$STOP_CMD" \
                '
                .hooks.SessionStart = [(.hooks.SessionStart // [])[] | select((.hooks[0].command // "") != $sessionStartCmd)]
                | .hooks.UserPromptSubmit = [(.hooks.UserPromptSubmit // [])[] | select((.hooks[0].command // "") != $userPromptCmd)]
                | .hooks.Stop = [(.hooks.Stop // [])[] | select((.hooks[0].command // "") != $stopCmd)]
                | if (.hooks.SessionStart | length) == 0 then del(.hooks.SessionStart) else . end
                | if (.hooks.UserPromptSubmit | length) == 0 then del(.hooks.UserPromptSubmit) else . end
                | if (.hooks.Stop | length) == 0 then del(.hooks.Stop) else . end
                | if (.hooks | length) == 0 then del(.hooks) else . end
                ' "$CLAUDE_SETTINGS" > "$CLAUDE_SETTINGS.tmp"
            mv "$CLAUDE_SETTINGS.tmp" "$CLAUDE_SETTINGS"
            echo "  ✓ Removed our hooks from $CLAUDE_SETTINGS (other hooks and settings preserved)"
        else
            echo "  - No hooks found in $CLAUDE_SETTINGS"
        fi
    else
        echo "  ⚠ jq not found. Please manually remove the SessionStart/UserPromptSubmit/Stop"
        echo "    entries referencing ~/.config/stringwork/hooks/*.sh from $CLAUDE_SETTINGS"
        echo "    (leave any other hooks/settings in place)."
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
