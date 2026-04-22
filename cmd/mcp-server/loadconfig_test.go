package main

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"testing"
)

// silentLogger returns a logger that discards output but is non-nil so
// loadConfig's warning path is exercisable.
func silentLogger() *log.Logger {
	return log.New(&bytes.Buffer{}, "", 0)
}

// TestLoadConfig_AutoDiscoversDefaultPath ensures that when MCP_CONFIG is not
// set in the environment, loadConfig falls back to ~/.config/stringwork/
// config.yaml. This is the path the install scripts and dashboard docs
// advertise; without auto-discovery, bare invocations like
// `mcp-stringwork --daemon` silently use compiled defaults (http_port: 0)
// and bind a random port despite the user having set http_port: 8943 in
// their config.
func TestLoadConfig_AutoDiscoversDefaultPath(t *testing.T) {
	tmpHome := t.TempDir()
	cfgDir := filepath.Join(tmpHome, ".config", "stringwork")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(cfgDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("http_port: 8943\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", tmpHome)
	t.Setenv("MCP_CONFIG", "")

	cfg := loadConfig(silentLogger())
	if cfg.HTTPPort != 8943 {
		t.Fatalf("expected HTTPPort=8943 from auto-discovered config, got %d", cfg.HTTPPort)
	}
}

// TestLoadConfig_MCPConfigEnvWins verifies that an explicit MCP_CONFIG path
// overrides the auto-discovery fallback even if a default file also exists.
// This preserves the existing contract that MCP launchers use.
func TestLoadConfig_MCPConfigEnvWins(t *testing.T) {
	tmpHome := t.TempDir()
	cfgDir := filepath.Join(tmpHome, ".config", "stringwork")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	defaultPath := filepath.Join(cfgDir, "config.yaml")
	if err := os.WriteFile(defaultPath, []byte("http_port: 1111\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	overridePath := filepath.Join(tmpHome, "override.yaml")
	if err := os.WriteFile(overridePath, []byte("http_port: 2222\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", tmpHome)
	t.Setenv("MCP_CONFIG", overridePath)

	cfg := loadConfig(silentLogger())
	if cfg.HTTPPort != 2222 {
		t.Fatalf("expected HTTPPort=2222 from MCP_CONFIG override, got %d", cfg.HTTPPort)
	}
}

// TestLoadConfig_NoConfigFallsBackToDefaults verifies that with no config
// file anywhere, loadConfig still returns a valid Config (using compiled
// defaults) rather than crashing or returning nil.
func TestLoadConfig_NoConfigFallsBackToDefaults(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("MCP_CONFIG", "")

	cfg := loadConfig(silentLogger())
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.HTTPPort != 0 {
		t.Fatalf("expected default HTTPPort=0 when no config file present, got %d", cfg.HTTPPort)
	}
	if cfg.WorkspaceRoot == "" {
		t.Fatal("expected WorkspaceRoot to be populated from cwd fallback")
	}
}
