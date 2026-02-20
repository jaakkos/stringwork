#!/bin/bash
# Install Stringwork Cursor plugin globally (user-level).
#
# What this does:
#   1. Copies .mdc rules to ~/.cursor/rules/
#   2. Copies skills to ~/.cursor/skills/
#   3. Copies agents to ~/.cursor/agents/
#   4. Copies commands to ~/.cursor/commands/
#   5. Copies hooks + scripts to ~/.config/stringwork/cursor-hooks/
#   6. Verifies mcp-stringwork binary is on PATH
#
# This installs at user level so the plugin applies to all Cursor projects.
# For project-scoped installation, copy .cursor-plugin/ and cursor-plugin/
# directly into your project root instead.
#
# Usage:
#   Install:   ./scripts/install-cursor-plugin.sh
#   Uninstall: ./scripts/uninstall-cursor-plugin.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_DIR="$(dirname "$SCRIPT_DIR")"
PLUGIN_DIR="$REPO_DIR/cursor-plugin"

CURSOR_RULES_DIR="$HOME/.cursor/rules"
CURSOR_SKILLS_DIR="$HOME/.cursor/skills"
CURSOR_AGENTS_DIR="$HOME/.cursor/agents"
CURSOR_COMMANDS_DIR="$HOME/.cursor/commands"
HOOKS_DIR="$HOME/.config/stringwork/cursor-hooks"

PREFIX="stringwork-"

echo "=== Stringwork: Installing Cursor plugin (user-level) ==="

if [ ! -d "$PLUGIN_DIR" ]; then
    echo "Error: cursor-plugin/ directory not found at $PLUGIN_DIR"
    echo "Run this script from the stringwork repo root: ./scripts/install-cursor-plugin.sh"
    exit 1
fi

# 1. Rules
echo "Installing rules to $CURSOR_RULES_DIR ..."
mkdir -p "$CURSOR_RULES_DIR"
for f in "$PLUGIN_DIR"/rules/*.mdc; do
    [ -f "$f" ] || continue
    name=$(basename "$f")
    cp "$f" "$CURSOR_RULES_DIR/${PREFIX}${name}"
done
echo "  ✓ $(ls "$PLUGIN_DIR"/rules/*.mdc 2>/dev/null | wc -l | tr -d ' ') rules installed"

# 2. Skills
echo "Installing skills to $CURSOR_SKILLS_DIR ..."
for skill_dir in "$PLUGIN_DIR"/skills/*/; do
    [ -d "$skill_dir" ] || continue
    name=$(basename "$skill_dir")
    target="$CURSOR_SKILLS_DIR/${PREFIX}${name}"
    mkdir -p "$target"
    cp "$skill_dir"SKILL.md "$target/SKILL.md"
done
echo "  ✓ $(find "$PLUGIN_DIR"/skills -name SKILL.md 2>/dev/null | wc -l | tr -d ' ') skills installed"

# 3. Agents
echo "Installing agents to $CURSOR_AGENTS_DIR ..."
mkdir -p "$CURSOR_AGENTS_DIR"
for f in "$PLUGIN_DIR"/agents/*.md; do
    [ -f "$f" ] || continue
    name=$(basename "$f")
    cp "$f" "$CURSOR_AGENTS_DIR/${PREFIX}${name}"
done
echo "  ✓ $(ls "$PLUGIN_DIR"/agents/*.md 2>/dev/null | wc -l | tr -d ' ') agents installed"

# 4. Commands
echo "Installing commands to $CURSOR_COMMANDS_DIR ..."
mkdir -p "$CURSOR_COMMANDS_DIR"
for f in "$PLUGIN_DIR"/commands/*.md; do
    [ -f "$f" ] || continue
    name=$(basename "$f")
    cp "$f" "$CURSOR_COMMANDS_DIR/${PREFIX}${name}"
done
echo "  ✓ $(ls "$PLUGIN_DIR"/commands/*.md 2>/dev/null | wc -l | tr -d ' ') commands installed"

# 5. Hook scripts
echo "Installing hook scripts to $HOOKS_DIR ..."
mkdir -p "$HOOKS_DIR"
if [ -f "$PLUGIN_DIR/scripts/check-binary.sh" ]; then
    cp "$PLUGIN_DIR/scripts/check-binary.sh" "$HOOKS_DIR/check-binary.sh"
    chmod +x "$HOOKS_DIR/check-binary.sh"
    echo "  ✓ Hook scripts installed"
else
    echo "  - No hook scripts found"
fi

# 6. Check binary
echo ""
if command -v mcp-stringwork &>/dev/null; then
    echo "  ✓ mcp-stringwork found: $(command -v mcp-stringwork)"
    echo "  Version: $(mcp-stringwork --version 2>/dev/null || echo 'unknown')"
else
    echo "  ⚠ mcp-stringwork not found on PATH"
    echo "  Install with: curl -fsSL https://raw.githubusercontent.com/jaakkos/stringwork/main/scripts/install.sh | sh"
fi

echo ""
echo "=== Done! ==="
echo ""
echo "Installed to:"
echo "  Rules:    $CURSOR_RULES_DIR/${PREFIX}*.mdc"
echo "  Skills:   $CURSOR_SKILLS_DIR/${PREFIX}*/"
echo "  Agents:   $CURSOR_AGENTS_DIR/${PREFIX}*.md"
echo "  Commands: $CURSOR_COMMANDS_DIR/${PREFIX}*.md"
echo "  Hooks:    $HOOKS_DIR/"
echo ""
echo "All files are prefixed with '${PREFIX}' for easy identification."
echo "Restart Cursor for changes to take effect."
