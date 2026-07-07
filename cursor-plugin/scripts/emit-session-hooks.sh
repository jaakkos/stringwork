#!/bin/bash
# Runs as a Cursor plugin sessionStart hook. Delegates the "should this
# session get worker progress-reporting reminders" decision to
# `mcp-stringwork hooks emit`, which resolves role (driver vs worker) from
# STRINGWORK_AGENT + orchestration.driver — see internal/app.ResolveHookRole.
#
# Cursor is the default driver in most Stringwork setups (see
# cursor-plugin/rules/pair-programming-workflow.mdc, alwaysApply: true),
# in which case this prints nothing. When orchestration.driver names a
# different platform (e.g. "claude-code") and Cursor is running as a manual
# worker instead, this prints the same mandatory worker rules Claude Code
# gets — a gap today, since cursor-plugin/rules/progress-reporting.mdc is
# alwaysApply: false and easy for the model to skip.
#
# Only sessionStart is wired here. Cursor's hooks.json does not have a
# confirmed userPromptSubmit/stop equivalent in this plugin's schema (see
# cursor-plugin/hooks/hooks.json) — add matching entries once that's
# verified against Cursor's current hook documentation.

[ -f "$HOME/.config/stringwork/state.sqlite" ] || exit 0

STRINGWORK_BIN="$(command -v mcp-stringwork 2>/dev/null || true)"
if [ -z "$STRINGWORK_BIN" ] && [ -x "$HOME/.local/bin/mcp-stringwork" ]; then
    STRINGWORK_BIN="$HOME/.local/bin/mcp-stringwork"
fi
[ -n "$STRINGWORK_BIN" ] || exit 0

exec "$STRINGWORK_BIN" hooks emit --event session_start --platform cursor 2>/dev/null
