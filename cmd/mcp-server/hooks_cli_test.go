package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jaakkos/stringwork/internal/app"
	"github.com/jaakkos/stringwork/internal/policy"
)

func TestRenderHooksEmit_DriverSessionIsSilentByDefault(t *testing.T) {
	cfg := &policy.Config{
		Orchestration: &policy.OrchestrationConfig{Driver: app.DriverAuto},
	}
	var buf bytes.Buffer
	renderHooksEmit(cfg, app.HookEventSessionStart, "claude-code", func(string) string { return "" }, &buf)

	if buf.Len() != 0 {
		t.Errorf("expected no output for a driver session, got %q", buf.String())
	}
}

func TestRenderHooksEmit_WorkerSessionGetsMandatoryRules(t *testing.T) {
	cfg := &policy.Config{
		Orchestration: &policy.OrchestrationConfig{Driver: "cursor"},
	}
	var buf bytes.Buffer
	renderHooksEmit(cfg, app.HookEventSessionStart, "claude-code", func(string) string { return "" }, &buf)

	if !strings.Contains(buf.String(), "MANDATORY Pair Programming Rules") {
		t.Errorf("expected mandatory rules text for a worker session, got %q", buf.String())
	}
}

func TestRenderHooksEmit_SpawnedWorkerIsWorkerEvenIfPlatformIsDriver(t *testing.T) {
	// A spawned worker (STRINGWORK_AGENT set) must get worker rules even
	// when orchestration.driver happens to name its own platform — the env
	// var takes precedence over the platform match (see ResolveHookRole).
	cfg := &policy.Config{
		Orchestration: &policy.OrchestrationConfig{Driver: "claude-code"},
	}
	getenv := func(k string) string {
		if k == app.EnvStringworkAgent {
			return "claude-code-task-7"
		}
		return ""
	}
	var buf bytes.Buffer
	renderHooksEmit(cfg, app.HookEventUserPrompt, "claude-code", getenv, &buf)

	if !strings.Contains(buf.String(), "MANDATORY") {
		t.Errorf("expected worker reminder for a spawned worker, got %q", buf.String())
	}
}

func TestRenderHooksEmit_MasterKillSwitchSilencesWorkerToo(t *testing.T) {
	disabled := false
	cfg := &policy.Config{
		Orchestration: &policy.OrchestrationConfig{Driver: "cursor"},
		Hooks:         &policy.HooksConfig{Enabled: &disabled},
	}
	var buf bytes.Buffer
	renderHooksEmit(cfg, app.HookEventStop, "claude-code", func(string) string { return "" }, &buf)

	if buf.Len() != 0 {
		t.Errorf("expected hooks.enabled=false to silence everything, got %q", buf.String())
	}
}

func TestRenderHooksEmit_ExplicitDriverOverrideEnablesText(t *testing.T) {
	enabled := true
	cfg := &policy.Config{
		Orchestration: &policy.OrchestrationConfig{Driver: app.DriverAuto},
		Hooks: &policy.HooksConfig{
			Platforms: map[string]policy.PlatformHooksConfig{
				"claude-code": {
					Driver: &policy.RoleHooksConfig{SessionStart: &enabled},
				},
			},
		},
	}
	var buf bytes.Buffer
	renderHooksEmit(cfg, app.HookEventSessionStart, "claude-code", func(string) string { return "" }, &buf)

	if !strings.Contains(buf.String(), "Stringwork Driver Reminder") {
		t.Errorf("expected driver reminder text when explicitly enabled, got %q", buf.String())
	}
}

func TestRenderHooksEmit_NilHooksConfigIsLegacyAlwaysInject(t *testing.T) {
	// No orchestration configured at all -> HookRoleLegacy -> always inject,
	// matching the pre-hooks-config behavior of the original bash scripts.
	cfg := &policy.Config{Orchestration: nil}
	var buf bytes.Buffer
	renderHooksEmit(cfg, app.HookEventStop, "claude-code", func(string) string { return "" }, &buf)

	if !strings.Contains(buf.String(), "REMINDER: Before stopping") {
		t.Errorf("expected legacy always-inject stop text, got %q", buf.String())
	}
}

func TestValidHookEvents_CoversAllAppHookEvents(t *testing.T) {
	want := []app.HookEvent{
		app.HookEventSessionStart,
		app.HookEventUserPrompt,
		app.HookEventStop,
		app.HookEventSpawn,
	}
	got := make(map[app.HookEvent]bool)
	for _, v := range validHookEvents {
		got[v] = true
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("validHookEvents is missing a flag string for %q — hooks emit --event would be unreachable for it", w)
		}
	}
}
