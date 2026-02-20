#!/bin/bash
# Uninstall Stringwork Cursor plugin (user-level).
#
# What this removes:
#   1. Stringwork rules from ~/.cursor/rules/
#   2. Stringwork skills from ~/.cursor/skills/
#   3. Stringwork agents from ~/.cursor/agents/
#   4. Stringwork commands from ~/.cursor/commands/
#   5. Hook scripts from ~/.config/stringwork/cursor-hooks/
#
# Only removes files prefixed with 'stringwork-'. Other rules/skills/etc. are untouched.
#
# Usage: ./scripts/uninstall-cursor-plugin.sh

set -euo pipefail

CURSOR_RULES_DIR="$HOME/.cursor/rules"
CURSOR_SKILLS_DIR="$HOME/.cursor/skills"
CURSOR_AGENTS_DIR="$HOME/.cursor/agents"
CURSOR_COMMANDS_DIR="$HOME/.cursor/commands"
HOOKS_DIR="$HOME/.config/stringwork/cursor-hooks"

PREFIX="stringwork-"

echo "=== Stringwork: Uninstalling Cursor plugin ==="

# 1. Rules
count=0
for f in "$CURSOR_RULES_DIR"/${PREFIX}*.mdc; do
    [ -f "$f" ] || continue
    rm -f "$f"
    count=$((count + 1))
done
if [ "$count" -gt 0 ]; then
    echo "  ✓ Removed $count rules from $CURSOR_RULES_DIR"
else
    echo "  - No Stringwork rules found"
fi

# 2. Skills
count=0
for d in "$CURSOR_SKILLS_DIR"/${PREFIX}*/; do
    [ -d "$d" ] || continue
    rm -rf "$d"
    count=$((count + 1))
done
if [ "$count" -gt 0 ]; then
    echo "  ✓ Removed $count skills from $CURSOR_SKILLS_DIR"
else
    echo "  - No Stringwork skills found"
fi

# 3. Agents
count=0
for f in "$CURSOR_AGENTS_DIR"/${PREFIX}*.md; do
    [ -f "$f" ] || continue
    rm -f "$f"
    count=$((count + 1))
done
if [ "$count" -gt 0 ]; then
    echo "  ✓ Removed $count agents from $CURSOR_AGENTS_DIR"
else
    echo "  - No Stringwork agents found"
fi

# 4. Commands
count=0
for f in "$CURSOR_COMMANDS_DIR"/${PREFIX}*.md; do
    [ -f "$f" ] || continue
    rm -f "$f"
    count=$((count + 1))
done
if [ "$count" -gt 0 ]; then
    echo "  ✓ Removed $count commands from $CURSOR_COMMANDS_DIR"
else
    echo "  - No Stringwork commands found"
fi

# 5. Hook scripts
if [ -d "$HOOKS_DIR" ]; then
    rm -rf "$HOOKS_DIR"
    echo "  ✓ Removed hook scripts from $HOOKS_DIR"
else
    echo "  - No hook scripts found"
fi

echo ""
echo "=== Done! ==="
echo ""
echo "Note: The mcp-stringwork binary and MCP server config were NOT removed."
echo "To remove those:"
echo "  rm -f ~/.local/bin/mcp-stringwork"
echo "  rm -rf ~/.config/stringwork/"
echo ""
echo "Restart Cursor for changes to take effect."
