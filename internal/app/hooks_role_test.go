package app

import (
	"testing"

	"github.com/jaakkos/stringwork/internal/policy"
)

func envLookup(vals map[string]string) func(string) string {
	return func(key string) string { return vals[key] }
}

func TestResolveHookRole(t *testing.T) {
	tests := []struct {
		name     string
		env      map[string]string
		orch     *policy.OrchestrationConfig
		platform string
		want     HookRole
	}{
		{
			name:     "nil orchestration is legacy regardless of env or platform",
			env:      nil,
			orch:     nil,
			platform: "claude-code",
			want:     HookRoleLegacy,
		},
		{
			name:     "STRINGWORK_AGENT set is always worker, even if platform matches driver",
			env:      map[string]string{EnvStringworkAgent: "claude-code-task-3"},
			orch:     &policy.OrchestrationConfig{Driver: "claude-code"},
			platform: "claude-code",
			want:     HookRoleWorker,
		},
		{
			name:     "STRINGWORK_AGENT set is worker even under auto driver",
			env:      map[string]string{EnvStringworkAgent: "codex-1"},
			orch:     &policy.OrchestrationConfig{Driver: DriverAuto},
			platform: "codex",
			want:     HookRoleWorker,
		},
		{
			name:     "auto driver, no STRINGWORK_AGENT -> this platform is the driver",
			env:      nil,
			orch:     &policy.OrchestrationConfig{Driver: DriverAuto},
			platform: "claude-code",
			want:     HookRoleDriver,
		},
		{
			name:     "auto driver is case-insensitive",
			env:      nil,
			orch:     &policy.OrchestrationConfig{Driver: "AUTO"},
			platform: "cursor",
			want:     HookRoleDriver,
		},
		{
			name:     "fixed driver equal to platform (case-insensitive) -> driver",
			env:      nil,
			orch:     &policy.OrchestrationConfig{Driver: "Claude-Code"},
			platform: "claude-code",
			want:     HookRoleDriver,
		},
		{
			name:     "fixed driver not equal to platform -> worker (manual worker session)",
			env:      nil,
			orch:     &policy.OrchestrationConfig{Driver: "cursor"},
			platform: "claude-code",
			want:     HookRoleWorker,
		},
		{
			name:     "empty STRINGWORK_AGENT (set to blank) does not count as set",
			env:      map[string]string{EnvStringworkAgent: "   "},
			orch:     &policy.OrchestrationConfig{Driver: "claude-code"},
			platform: "claude-code",
			want:     HookRoleDriver,
		},
		{
			name:     "nil getenv falls back to os.Getenv without panicking",
			env:      nil,
			orch:     &policy.OrchestrationConfig{Driver: "cursor"},
			platform: "cursor",
			want:     HookRoleDriver,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var getenv func(string) string
			if tt.env != nil {
				getenv = envLookup(tt.env)
			}
			got := ResolveHookRole(getenv, tt.orch, tt.platform)
			if got != tt.want {
				t.Errorf("ResolveHookRole() = %q, want %q", got, tt.want)
			}
		})
	}
}

func boolPtr(b bool) *bool { return &b }

func TestShouldEmitHook(t *testing.T) {
	tests := []struct {
		name     string
		hooks    *policy.HooksConfig
		platform string
		role     HookRole
		event    HookEvent
		want     bool
	}{
		{
			name:     "nil hooks config: driver defaults to silent",
			hooks:    nil,
			platform: "claude-code",
			role:     HookRoleDriver,
			event:    HookEventSessionStart,
			want:     false,
		},
		{
			name:     "nil hooks config: worker defaults to emit",
			hooks:    nil,
			platform: "claude-code",
			role:     HookRoleWorker,
			event:    HookEventUserPrompt,
			want:     true,
		},
		{
			name:     "nil hooks config: legacy defaults to emit (same as worker)",
			hooks:    nil,
			platform: "claude-code",
			role:     HookRoleLegacy,
			event:    HookEventStop,
			want:     true,
		},
		{
			name:     "master kill switch overrides everything, including worker",
			hooks:    &policy.HooksConfig{Enabled: boolPtr(false)},
			platform: "claude-code",
			role:     HookRoleWorker,
			event:    HookEventSessionStart,
			want:     false,
		},
		{
			name: "platform present but role config absent falls back to role default",
			hooks: &policy.HooksConfig{
				Platforms: map[string]policy.PlatformHooksConfig{
					"claude-code": {}, // no Driver/Worker set
				},
			},
			platform: "claude-code",
			role:     HookRoleDriver,
			event:    HookEventSessionStart,
			want:     false, // driver default
		},
		{
			name: "explicit driver override enables session_start",
			hooks: &policy.HooksConfig{
				Platforms: map[string]policy.PlatformHooksConfig{
					"claude-code": {
						Driver: &policy.RoleHooksConfig{SessionStart: boolPtr(true)},
					},
				},
			},
			platform: "claude-code",
			role:     HookRoleDriver,
			event:    HookEventSessionStart,
			want:     true,
		},
		{
			name: "explicit driver override does not affect a different event (falls back to default)",
			hooks: &policy.HooksConfig{
				Platforms: map[string]policy.PlatformHooksConfig{
					"claude-code": {
						Driver: &policy.RoleHooksConfig{SessionStart: boolPtr(true)},
					},
				},
			},
			platform: "claude-code",
			role:     HookRoleDriver,
			event:    HookEventUserPrompt,
			want:     false,
		},
		{
			name: "explicit worker override disables stop",
			hooks: &policy.HooksConfig{
				Platforms: map[string]policy.PlatformHooksConfig{
					"claude-code": {
						Worker: &policy.RoleHooksConfig{Stop: boolPtr(false)},
					},
				},
			},
			platform: "claude-code",
			role:     HookRoleWorker,
			event:    HookEventStop,
			want:     false,
		},
		{
			name: "override for one platform does not leak to another platform",
			hooks: &policy.HooksConfig{
				Platforms: map[string]policy.PlatformHooksConfig{
					"claude-code": {
						Driver: &policy.RoleHooksConfig{SessionStart: boolPtr(true)},
					},
				},
			},
			platform: "cursor",
			role:     HookRoleDriver,
			event:    HookEventSessionStart,
			want:     false,
		},
		{
			name: "spawn event honors worker.spawn=false override",
			hooks: &policy.HooksConfig{
				Platforms: map[string]policy.PlatformHooksConfig{
					"codex": {
						Worker: &policy.RoleHooksConfig{Spawn: boolPtr(false)},
					},
				},
			},
			platform: "codex",
			role:     HookRoleWorker,
			event:    HookEventSpawn,
			want:     false,
		},
		{
			name:     "spawn event defaults to emit for worker when unconfigured",
			hooks:    nil,
			platform: "gemini",
			role:     HookRoleWorker,
			event:    HookEventSpawn,
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShouldEmitHook(tt.hooks, tt.platform, tt.role, tt.event)
			if got != tt.want {
				t.Errorf("ShouldEmitHook() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTextForHook(t *testing.T) {
	tests := []struct {
		name      string
		role      HookRole
		event     HookEvent
		wantEmpty bool
	}{
		{"driver session_start has text", HookRoleDriver, HookEventSessionStart, false},
		{"driver user_prompt has text", HookRoleDriver, HookEventUserPrompt, false},
		{"driver stop has text", HookRoleDriver, HookEventStop, false},
		{"driver spawn has no text (spawn is worker-only)", HookRoleDriver, HookEventSpawn, true},
		{"worker session_start has text", HookRoleWorker, HookEventSessionStart, false},
		{"worker spawn reuses session_start text", HookRoleWorker, HookEventSpawn, false},
		{"legacy user_prompt has text (treated as worker)", HookRoleLegacy, HookEventUserPrompt, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TextForHook(tt.role, tt.event)
			if tt.wantEmpty && got != "" {
				t.Errorf("TextForHook() = %q, want empty", got)
			}
			if !tt.wantEmpty && got == "" {
				t.Errorf("TextForHook() = empty, want non-empty")
			}
		})
	}
}

// TestWorkerSpawnTextMatchesSessionStartText locks in that the spawn-prompt
// placeholder ({worker_rules}) and the SessionStart hook inject byte-identical
// text — the whole point of centralizing WorkerRulesSessionStart is that a
// spawned worker (Codex/Gemini via spawn prompt) and a hook-based worker
// (Claude Code via SessionStart) see the exact same rules.
func TestWorkerSpawnTextMatchesSessionStartText(t *testing.T) {
	spawn := TextForHook(HookRoleWorker, HookEventSpawn)
	sessionStart := TextForHook(HookRoleWorker, HookEventSessionStart)
	if spawn != sessionStart {
		t.Errorf("spawn text and session_start text diverged:\nspawn=%q\nsessionStart=%q", spawn, sessionStart)
	}
	if spawn != WorkerRulesSessionStart {
		t.Errorf("spawn text does not match WorkerRulesSessionStart constant")
	}
}
