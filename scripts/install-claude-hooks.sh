#!/bin/bash
# Install Stringwork hooks for Claude Code.
#
# Hooks bypass Claude Code's "may or may not be relevant" framing for CLAUDE.md
# by injecting rules as clean system-reminder messages.
#
# What this does:
#   1. Copies hook scripts to ~/.config/stringwork/hooks/
#   2. Merges hook config into ~/.claude/settings.json (preserves existing settings)
#
# The hooks have a guard — they only activate when ~/.config/stringwork/state.sqlite
# exists, so they're harmless in non-Stringwork projects.
#
# Usage:
#   Install:   ./scripts/install-claude-hooks.sh
#   Uninstall: ./scripts/uninstall-claude-hooks.sh

set -euo pipefail

HOOKS_DIR="$HOME/.config/stringwork/hooks"
CLAUDE_SETTINGS="$HOME/.claude/settings.json"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_DIR="$(dirname "$SCRIPT_DIR")"

echo "=== Stringwork: Installing Claude Code hooks ==="

# 1. Copy hook scripts
echo "Installing hook scripts to $HOOKS_DIR ..."
mkdir -p "$HOOKS_DIR"

cat > "$HOOKS_DIR/inject-rules.sh" << 'EOF'
#!/bin/bash
[ -f "$HOME/.config/stringwork/state.sqlite" ] || exit 0
cat <<'RULES'
## MANDATORY Pair Programming Rules (Stringwork — server-enforced)

You are a worker in the Stringwork pair programming system. These rules are NON-NEGOTIABLE.

### Progress Reporting — REQUIRED while working on ANY task

TRIGGER: You claimed or are working on a task.
ACTION: Call BOTH of these MCP tools at the specified intervals:

1. `heartbeat` — every 60-90 seconds with a progress description
   Example: heartbeat agent='claude-code' progress='implementing auth middleware' step=2 total_steps=4

2. `report_progress` — every 2-3 minutes with task_id, description, percent_complete
   Example: report_progress agent='claude-code' task_id=5 description='Auth done. Writing tests (8/15).' percent_complete=50 eta_seconds=120

Consequence of NOT reporting:
- 3 min silence → WARNING alert sent to driver
- 5 min silence → CRITICAL alert sent to driver
- 10 min silence → Task auto-recovered, you may be CANCELLED

### Communication — REQUIRED before finishing

TRIGGER: You are about to finish or stop working.
ACTION: Call `send_message` from your agent name to the driver with a detailed summary.

### STOP Signals — immediate compliance required

TRIGGER: You see a STOP banner on any tool response.
ACTION: Stop ALL work immediately. Call read_messages. Exit.
RULES
EOF

# inject-reminder.sh
cat > "$HOOKS_DIR/inject-reminder.sh" << 'EOF'
#!/bin/bash
[ -f "$HOME/.config/stringwork/state.sqlite" ] || exit 0
echo "MANDATORY: If working on a task, call heartbeat (every 60-90s) and report_progress (every 2-3min). Always send_message with findings before finishing."
EOF

# stop-check.sh
cat > "$HOOKS_DIR/stop-check.sh" << 'EOF'
#!/bin/bash
[ -f "$HOME/.config/stringwork/state.sqlite" ] || exit 0
echo "REMINDER: Before stopping, verify you have:"
echo "1. Called send_message to report your findings to the driver"
echo "2. Called update_task to mark task status (completed/blocked)"
echo "3. Called report_progress with final status"
echo "If you haven't done these, continue working and complete them now."
EOF

chmod +x "$HOOKS_DIR"/*.sh
echo "  ✓ Hook scripts installed"

# 2. Merge hooks into ~/.claude/settings.json
echo "Merging hooks into $CLAUDE_SETTINGS ..."
mkdir -p "$(dirname "$CLAUDE_SETTINGS")"

HOOKS_JSON='{
  "hooks": {
    "SessionStart": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "$HOME/.config/stringwork/hooks/inject-rules.sh",
            "timeout": 10
          }
        ]
      }
    ],
    "UserPromptSubmit": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "$HOME/.config/stringwork/hooks/inject-reminder.sh",
            "timeout": 5
          }
        ]
      }
    ],
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "$HOME/.config/stringwork/hooks/stop-check.sh",
            "timeout": 10
          }
        ]
      }
    ]
  }
}'

if [ -f "$CLAUDE_SETTINGS" ]; then
    if command -v jq &>/dev/null; then
        # Merge with existing settings using jq
        EXISTING=$(cat "$CLAUDE_SETTINGS")
        echo "$EXISTING" | jq --argjson hooks "$(echo "$HOOKS_JSON" | jq '.hooks')" '. + {hooks: $hooks}' > "$CLAUDE_SETTINGS.tmp"
        mv "$CLAUDE_SETTINGS.tmp" "$CLAUDE_SETTINGS"
        echo "  ✓ Merged hooks into existing settings"
    else
        echo "  ⚠ jq not found. Please manually add hooks to $CLAUDE_SETTINGS"
        echo "  Hooks JSON:"
        echo "$HOOKS_JSON"
        exit 1
    fi
else
    echo "$HOOKS_JSON" > "$CLAUDE_SETTINGS"
    echo "  ✓ Created $CLAUDE_SETTINGS with hooks"
fi

# 3. Install pair-respond command (if .claude/commands exists or we create it)
if [ -d "$HOME/.claude/commands" ]; then
    if [ -f "$REPO_DIR/.claude/commands/pair-respond.md" ]; then
        cp "$REPO_DIR/.claude/commands/pair-respond.md" "$HOME/.claude/commands/pair-respond.md"
        echo "  ✓ Installed /pair-respond command"
    fi
fi

echo ""
echo "=== Done! ==="
echo ""
echo "Hooks installed:"
echo "  SessionStart    → Injects mandatory rules (survives context compaction)"
echo "  UserPromptSubmit → Short reminder on every prompt (~30 tokens)"
echo "  Stop            → Reminds to report findings before finishing"
echo ""
echo "Scripts have a guard: they only activate when Stringwork state exists."
echo "Restart Claude Code for hooks to take effect."
