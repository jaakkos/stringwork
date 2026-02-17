package app

import (
	"os"
	"strings"
	"testing"
)

func TestBuildWorkerEnv_DefaultInheritsAll(t *testing.T) {
	c := WorkerSpawnConfig{
		InstanceID: "claude-code-1",
		AgentType:  "claude-code",
	}

	env := buildWorkerEnv(c, "/tmp/workspace")

	// Should contain all parent env vars + STRINGWORK_AGENT + STRINGWORK_WORKSPACE
	envMap := envToMap(env)

	if envMap["STRINGWORK_AGENT"] != "claude-code-1" {
		t.Errorf("STRINGWORK_AGENT = %q, want 'claude-code-1'", envMap["STRINGWORK_AGENT"])
	}
	if envMap["STRINGWORK_WORKSPACE"] != "/tmp/workspace" {
		t.Errorf("STRINGWORK_WORKSPACE = %q, want '/tmp/workspace'", envMap["STRINGWORK_WORKSPACE"])
	}
	// HOME should be inherited from parent
	if envMap["HOME"] == "" {
		t.Error("expected HOME to be inherited from parent env")
	}
	// PATH should be inherited from parent
	if envMap["PATH"] == "" {
		t.Error("expected PATH to be inherited from parent env")
	}
}

func TestBuildWorkerEnv_InheritNone(t *testing.T) {
	c := WorkerSpawnConfig{
		InstanceID: "claude-code-1",
		AgentType:  "claude-code",
		InheritEnv: []string{"none"},
	}

	env := buildWorkerEnv(c, "/tmp/workspace")
	envMap := envToMap(env)

	// Should only have STRINGWORK_AGENT and STRINGWORK_WORKSPACE
	if envMap["STRINGWORK_AGENT"] != "claude-code-1" {
		t.Errorf("STRINGWORK_AGENT = %q", envMap["STRINGWORK_AGENT"])
	}
	if envMap["STRINGWORK_WORKSPACE"] != "/tmp/workspace" {
		t.Errorf("STRINGWORK_WORKSPACE = %q", envMap["STRINGWORK_WORKSPACE"])
	}
	// HOME should NOT be inherited
	if envMap["HOME"] != "" {
		t.Error("expected HOME NOT to be inherited when inherit_env=['none']")
	}
}

func TestBuildWorkerEnv_InheritPatterns(t *testing.T) {
	// Set test env vars
	os.Setenv("TEST_GH_TOKEN", "ghp_test123")
	os.Setenv("TEST_GITHUB_USER", "testuser")
	os.Setenv("TEST_OTHER_VAR", "should_not_pass")
	defer func() {
		os.Unsetenv("TEST_GH_TOKEN")
		os.Unsetenv("TEST_GITHUB_USER")
		os.Unsetenv("TEST_OTHER_VAR")
	}()

	c := WorkerSpawnConfig{
		InstanceID: "claude-code-1",
		AgentType:  "claude-code",
		InheritEnv: []string{"TEST_GH_*", "TEST_GITHUB_*"},
	}

	env := buildWorkerEnv(c, "/tmp/workspace")
	envMap := envToMap(env)

	if envMap["TEST_GH_TOKEN"] != "ghp_test123" {
		t.Errorf("TEST_GH_TOKEN = %q, want 'ghp_test123'", envMap["TEST_GH_TOKEN"])
	}
	if envMap["TEST_GITHUB_USER"] != "testuser" {
		t.Errorf("TEST_GITHUB_USER = %q, want 'testuser'", envMap["TEST_GITHUB_USER"])
	}
	if envMap["TEST_OTHER_VAR"] != "" {
		t.Errorf("TEST_OTHER_VAR = %q, should not be inherited", envMap["TEST_OTHER_VAR"])
	}
	// PAIR_* should still be injected
	if envMap["STRINGWORK_AGENT"] != "claude-code-1" {
		t.Errorf("STRINGWORK_AGENT = %q", envMap["STRINGWORK_AGENT"])
	}
}

func TestBuildWorkerEnv_CustomEnvVars(t *testing.T) {
	c := WorkerSpawnConfig{
		InstanceID: "claude-code-1",
		AgentType:  "claude-code",
		Env: map[string]string{
			"CUSTOM_VAR": "custom_value",
			"FOO":        "bar",
		},
	}

	env := buildWorkerEnv(c, "/tmp/workspace")
	envMap := envToMap(env)

	if envMap["CUSTOM_VAR"] != "custom_value" {
		t.Errorf("CUSTOM_VAR = %q, want 'custom_value'", envMap["CUSTOM_VAR"])
	}
	if envMap["FOO"] != "bar" {
		t.Errorf("FOO = %q, want 'bar'", envMap["FOO"])
	}
}

func TestBuildWorkerEnv_EnvExpansion(t *testing.T) {
	os.Setenv("TEST_EXPAND_SOURCE", "expanded_value")
	defer os.Unsetenv("TEST_EXPAND_SOURCE")

	c := WorkerSpawnConfig{
		InstanceID: "claude-code-1",
		AgentType:  "claude-code",
		Env: map[string]string{
			"DERIVED": "${TEST_EXPAND_SOURCE}",
			"MIXED":   "prefix_${TEST_EXPAND_SOURCE}_suffix",
		},
	}

	env := buildWorkerEnv(c, "/tmp/workspace")
	envMap := envToMap(env)

	if envMap["DERIVED"] != "expanded_value" {
		t.Errorf("DERIVED = %q, want 'expanded_value'", envMap["DERIVED"])
	}
	if envMap["MIXED"] != "prefix_expanded_value_suffix" {
		t.Errorf("MIXED = %q, want 'prefix_expanded_value_suffix'", envMap["MIXED"])
	}
}

func TestBuildWorkerEnv_EnvOverridesInherited(t *testing.T) {
	os.Setenv("TEST_OVERRIDE_ME", "original")
	defer os.Unsetenv("TEST_OVERRIDE_ME")

	c := WorkerSpawnConfig{
		InstanceID: "claude-code-1",
		AgentType:  "claude-code",
		Env: map[string]string{
			"TEST_OVERRIDE_ME": "overridden",
		},
	}

	env := buildWorkerEnv(c, "/tmp/workspace")
	envMap := envToMap(env)

	if envMap["TEST_OVERRIDE_ME"] != "overridden" {
		t.Errorf("TEST_OVERRIDE_ME = %q, want 'overridden'", envMap["TEST_OVERRIDE_ME"])
	}
}

func TestBuildWorkerEnv_MissingExpansionEmpty(t *testing.T) {
	os.Unsetenv("TEST_NONEXISTENT_VAR_12345")

	c := WorkerSpawnConfig{
		InstanceID: "claude-code-1",
		AgentType:  "claude-code",
		Env: map[string]string{
			"MISSING": "${TEST_NONEXISTENT_VAR_12345}",
		},
	}

	env := buildWorkerEnv(c, "/tmp/workspace")
	envMap := envToMap(env)

	if envMap["MISSING"] != "" {
		t.Errorf("MISSING = %q, want empty (unset var)", envMap["MISSING"])
	}
}

func TestMatchEnvGlob(t *testing.T) {
	tests := []struct {
		pattern string
		name    string
		want    bool
	}{
		{"GH_*", "GH_TOKEN", true},
		{"GH_*", "GH_", true},
		{"GH_*", "GITHUB_TOKEN", false},
		{"GITHUB_*", "GITHUB_TOKEN", true},
		{"HOME", "HOME", true},
		{"HOME", "HOMEPATH", false},
		{"*", "ANYTHING", true},
		{"SSH_*", "SSH_AUTH_SOCK", true},
		{"SSH_*", "SSH_AGENT_PID", true},
		{"LC_*", "LC_ALL", true},
		{"LC_*", "LANG", false},
	}

	for _, tt := range tests {
		got := matchEnvGlob(tt.pattern, tt.name)
		if got != tt.want {
			t.Errorf("matchEnvGlob(%q, %q) = %v, want %v", tt.pattern, tt.name, got, tt.want)
		}
	}
}

func TestSetEnvVar_New(t *testing.T) {
	env := []string{"A=1", "B=2"}
	env = setEnvVar(env, "C", "3")

	m := envToMap(env)
	if m["C"] != "3" {
		t.Errorf("C = %q, want '3'", m["C"])
	}
}

func TestSetEnvVar_Override(t *testing.T) {
	env := []string{"A=1", "B=2"}
	env = setEnvVar(env, "A", "override")

	m := envToMap(env)
	if m["A"] != "override" {
		t.Errorf("A = %q, want 'override'", m["A"])
	}
	if len(env) != 2 {
		t.Errorf("expected 2 entries (no duplicates), got %d", len(env))
	}
}

func envToMap(env []string) map[string]string {
	m := make(map[string]string)
	for _, e := range env {
		if k, v, ok := strings.Cut(e, "="); ok {
			m[k] = v
		}
	}
	return m
}
