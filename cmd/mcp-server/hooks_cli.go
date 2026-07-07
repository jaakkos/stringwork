// Hooks CLI subcommand — no daemon required. Prints the text an
// editor/CLI integration hook (Claude Code SessionStart/UserPromptSubmit/
// Stop, Cursor sessionStart/userPromptSubmit) should inject for the current
// session, or nothing at all when the session resolves to the driver role.
// Reads config the same way `constitution`/`quota` do (loadConfig), so it
// works standalone without a running daemon.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/jaakkos/stringwork/internal/app"
	"github.com/jaakkos/stringwork/internal/policy"
)

var validHookEvents = map[string]app.HookEvent{
	"session_start": app.HookEventSessionStart,
	"user_prompt":   app.HookEventUserPrompt,
	"stop":          app.HookEventStop,
	"spawn":         app.HookEventSpawn,
}

// runHooksCommand dispatches `mcp-stringwork hooks <subcommand>`.
func runHooksCommand(args []string) {
	if len(args) == 0 {
		printHooksUsage(os.Stderr)
		os.Exit(1)
	}
	switch args[0] {
	case "emit":
		runHooksEmit(args[1:])
	case "-h", "--help", "help":
		printHooksUsage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "unknown hooks subcommand: %s\n\n", args[0])
		printHooksUsage(os.Stderr)
		os.Exit(1)
	}
}

func printHooksUsage(w io.Writer) {
	fmt.Fprint(w, `usage: mcp-stringwork hooks emit --event <event> --platform <platform>

Print the Stringwork rule-reminder text an editor/CLI integration hook
should inject for the CURRENT session, or nothing when this session
resolves to the driver role. No daemon required — reads config the same
way 'constitution'/'quota' do.

  --event EVENT      One of: session_start, user_prompt, stop, spawn
  --platform NAME    Platform this hook fires for, e.g. claude-code, cursor,
                      codex, gemini. Must match a key under
                      hooks.platforms.<name> in config.yaml to pick up
                      platform-specific overrides.

Role resolution (see internal/app.ResolveHookRole):
  - STRINGWORK_AGENT env var set        -> worker (Stringwork-spawned)
  - orchestration.driver == "auto", or
    orchestration.driver == --platform  -> driver
  - otherwise                            -> worker (manual worker session)
  - no orchestration configured at all  -> legacy (always inject)

Exits 0 in all cases (including config-load errors) so a broken config
never blocks a session — install shims should call this directly:

  exec mcp-stringwork hooks emit --event session_start --platform claude-code
`)
}

func runHooksEmit(args []string) {
	eventFlag := flagValue(args, "--event")
	platform := flagValue(args, "--platform")

	event, ok := validHookEvents[eventFlag]
	if !ok {
		fmt.Fprintf(os.Stderr, "error: --event must be one of session_start, user_prompt, stop, spawn (got %q)\n", eventFlag)
		os.Exit(1)
	}
	if platform == "" {
		fmt.Fprintln(os.Stderr, "error: --platform is required")
		os.Exit(1)
	}

	// loadConfig never returns nil and never exits on a bad/missing config
	// file — falls back to policy.DefaultConfig() with a logged warning.
	// That default always sets Orchestration (driver: cursor), so the
	// "legacy" role path only fires for callers that construct an
	// *policy.OrchestrationConfig-less Policy directly (not this CLI path).
	cfg := loadConfig(adminLogger())
	renderHooksEmit(cfg, event, platform, os.Getenv, os.Stdout)
}

// renderHooksEmit is the testable core of `hooks emit`: given an already-
// loaded config, the validated event/platform, and an injectable getenv,
// it writes the resolved hook text (or nothing) to w. Split out from
// runHooksEmit so tests can exercise every (role, event, config-override)
// combination without touching os.Args/os.Exit or the filesystem-backed
// config loader.
func renderHooksEmit(cfg *policy.Config, event app.HookEvent, platform string, getenv func(string) string, w io.Writer) {
	role := app.ResolveHookRole(getenv, cfg.Orchestration, platform)
	if !app.ShouldEmitHook(cfg.Hooks, platform, role, event) {
		return // print nothing
	}
	text := app.TextForHook(role, event)
	if text == "" {
		return
	}
	fmt.Fprintln(w, text)
}
