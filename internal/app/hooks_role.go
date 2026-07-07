package app

import (
	"os"
	"strings"

	"github.com/jaakkos/stringwork/internal/policy"
)

// EnvStringworkAgent is the environment variable Stringwork sets on every
// spawned worker process (worker_manager.go spawnEnv). Its presence is the
// most reliable, zero-dependency signal that the current process is a
// Stringwork-managed worker rather than a human driving an agent CLI/editor
// directly — spawned workers always have it set; human sessions never do.
const EnvStringworkAgent = "STRINGWORK_AGENT"

// HookRole is the resolved role of a session for the purpose of deciding
// whether to inject Stringwork hook reminders (worker progress-reporting
// rules, driver orchestration notes, or nothing).
type HookRole string

const (
	// HookRoleDriver is a session driving the pair-programming session
	// (human at the keyboard, or the configured orchestration.driver).
	HookRoleDriver HookRole = "driver"
	// HookRoleWorker is a session expected to claim tasks and report
	// progress to a driver — either a Stringwork-spawned worker process,
	// or a human running an agent CLI/editor that isn't the driver.
	HookRoleWorker HookRole = "worker"
	// HookRoleLegacy applies when no orchestration is configured at all
	// (nil *policy.OrchestrationConfig). There is no driver/worker
	// distinction to make, so hook injection falls back to the
	// pre-hooks-config behavior: always inject (see ShouldEmitHook).
	HookRoleLegacy HookRole = "legacy"
)

// ResolveHookRole determines which role a hook-firing session should be
// treated as, using only the environment and static orchestration config —
// no live daemon state or database lookup required, so it's safe to call
// from a SessionStart/UserPromptSubmit/Stop hook shim that runs standalone.
//
// Resolution order:
//  1. No orchestration configured at all -> HookRoleLegacy (always inject,
//     preserving behavior from before this config existed).
//  2. EnvStringworkAgent is set -> HookRoleWorker. Stringwork sets this on
//     every spawned worker process (see worker_manager.go); a human driving
//     an agent CLI/editor directly never has it set.
//  3. orchestration.driver is "auto" (dynamic human-as-driver mode) or
//     equals platform (case-insensitive) -> HookRoleDriver. This session's
//     platform IS the configured driver.
//  4. Otherwise -> HookRoleWorker. A human running the agent CLI/editor for
//     a platform that isn't the configured driver is a manual worker (e.g.
//     Claude Code hooks firing while orchestration.driver: cursor).
//
// getenv is injectable for tests; pass nil in production to use os.Getenv.
func ResolveHookRole(getenv func(string) string, orch *policy.OrchestrationConfig, platform string) HookRole {
	if orch == nil {
		return HookRoleLegacy
	}
	if getenv == nil {
		getenv = os.Getenv
	}
	if strings.TrimSpace(getenv(EnvStringworkAgent)) != "" {
		return HookRoleWorker
	}
	if IsAutoDriver(orch.Driver) || strings.EqualFold(strings.TrimSpace(orch.Driver), strings.TrimSpace(platform)) {
		return HookRoleDriver
	}
	return HookRoleWorker
}

// HookEvent identifies a hook injection point. SessionStart/UserPrompt/Stop
// back the Claude Code and Cursor hook scripts; Spawn backs the
// {worker_rules} spawn-prompt placeholder for CLI-spawned workers.
type HookEvent string

const (
	HookEventSessionStart HookEvent = "session_start"
	HookEventUserPrompt   HookEvent = "user_prompt"
	HookEventStop         HookEvent = "stop"
	HookEventSpawn        HookEvent = "spawn"
)

// ShouldEmitHook reports whether the given (platform, role, event)
// combination should inject Stringwork rule text.
//
// Defaults (used whenever config doesn't explicitly override):
//   - HookRoleDriver: false for every event. A driver orchestrates; it
//     doesn't need "call heartbeat every 60-90s" worker-progress reminders.
//   - HookRoleWorker / HookRoleLegacy: true for every event. Preserves the
//     original always-inject behavior for anyone actually expected to
//     report progress to a driver.
//
// An explicit per-event boolean in hooks.platforms.<platform>.<role> always
// overrides the default. hooks.enabled=false is a master kill switch that
// short-circuits to false regardless of role or platform.
func ShouldEmitHook(h *policy.HooksConfig, platform string, role HookRole, event HookEvent) bool {
	if h != nil && h.Enabled != nil && !*h.Enabled {
		return false
	}

	def := role != HookRoleDriver

	if h == nil {
		return def
	}
	plat, ok := h.Platforms[platform]
	if !ok {
		return def
	}

	var roleCfg *policy.RoleHooksConfig
	if role == HookRoleDriver {
		roleCfg = plat.Driver
	} else {
		roleCfg = plat.Worker
	}
	if roleCfg == nil {
		return def
	}

	var override *bool
	switch event {
	case HookEventSessionStart:
		override = roleCfg.SessionStart
	case HookEventUserPrompt:
		override = roleCfg.UserPrompt
	case HookEventStop:
		override = roleCfg.Stop
	case HookEventSpawn:
		override = roleCfg.Spawn
	}
	if override == nil {
		return def
	}
	return *override
}
