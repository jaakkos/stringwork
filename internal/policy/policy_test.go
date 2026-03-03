package policy

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.MessageRetentionMax != 1000 {
		t.Errorf("expected message retention max 1000, got %d", cfg.MessageRetentionMax)
	}

	if cfg.MessageRetentionDays != 30 {
		t.Errorf("expected message retention days 30, got %d", cfg.MessageRetentionDays)
	}

	if cfg.PresenceTTLSeconds != 300 {
		t.Errorf("expected presence TTL 300s, got %d", cfg.PresenceTTLSeconds)
	}

	if cfg.StateFile != "" {
		t.Errorf("expected empty state_file by default, got %q", cfg.StateFile)
	}

	if len(cfg.EnabledTools) != 1 || cfg.EnabledTools[0] != "*" {
		t.Errorf("expected enabled_tools [*], got %v", cfg.EnabledTools)
	}
}

func TestValidatePath(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &Config{WorkspaceRoot: tmpDir}
	pol := New(cfg)

	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{
			name:    "relative path within workspace",
			path:    "subdir/file.go",
			wantErr: false,
		},
		{
			name:    "absolute path within workspace",
			path:    filepath.Join(tmpDir, "file.go"),
			wantErr: false,
		},
		{
			name:    "path escaping workspace",
			path:    "../outside.go",
			wantErr: true,
		},
		{
			name:    "absolute path outside workspace",
			path:    "/etc/passwd",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := pol.ValidatePath(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidatePath(%q) error = %v, wantErr %v", tt.path, err, tt.wantErr)
			}
		})
	}
}

func TestIsToolEnabled(t *testing.T) {
	tests := []struct {
		name         string
		enabledTools []string
		toolName     string
		want         bool
	}{
		{
			name:         "wildcard enables all",
			enabledTools: []string{"*"},
			toolName:     "any_tool",
			want:         true,
		},
		{
			name:         "specific tool enabled",
			enabledTools: []string{"read_file", "write_file"},
			toolName:     "read_file",
			want:         true,
		},
		{
			name:         "tool not in list",
			enabledTools: []string{"read_file"},
			toolName:     "write_file",
			want:         false,
		},
		{
			name:         "empty list",
			enabledTools: []string{},
			toolName:     "any_tool",
			want:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pol := New(&Config{EnabledTools: tt.enabledTools})
			if got := pol.IsToolEnabled(tt.toolName); got != tt.want {
				t.Errorf("IsToolEnabled(%q) = %v, want %v", tt.toolName, got, tt.want)
			}
		})
	}
}

func TestLoadConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := `
workspace_root: /test/workspace
enabled_tools:
  - read_file
  - grep
`

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if cfg.WorkspaceRoot != "/test/workspace" {
		t.Errorf("expected workspace /test/workspace, got %s", cfg.WorkspaceRoot)
	}

	if len(cfg.EnabledTools) != 2 {
		t.Errorf("expected 2 enabled tools, got %d", len(cfg.EnabledTools))
	}
}

func TestCollaborationSettings(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := `
workspace_root: /test
message_retention_max: 500
message_retention_days: 7
presence_ttl_seconds: 120
state_file: state/custom.sqlite
`

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	pol := New(cfg)

	if pol.MessageRetentionMax() != 500 {
		t.Errorf("expected message retention max 500, got %d", pol.MessageRetentionMax())
	}

	if pol.MessageRetentionDays() != 7 {
		t.Errorf("expected message retention days 7, got %d", pol.MessageRetentionDays())
	}

	if pol.PresenceTTLSeconds() != 120 {
		t.Errorf("expected presence TTL 120s, got %d", pol.PresenceTTLSeconds())
	}

	expectedState := filepath.Join("/test", "state/custom.sqlite")
	if pol.StateFile() != expectedState {
		t.Errorf("expected state file %s, got %s", expectedState, pol.StateFile())
	}
}

func TestMCPServers_URLBased(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := `
workspace_root: /test
mcp_servers:
  atlassian:
    url: "https://mcp.atlassian.com/v1/mcp"
    auth: oauth
  github:
    url: "https://mcp.github.com/sse"
    auth: bearer
`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	pol := New(cfg)
	servers := pol.MCPServers()

	if len(servers) != 2 {
		t.Fatalf("expected 2 MCP servers, got %d", len(servers))
	}

	atlassian, ok := servers["atlassian"]
	if !ok {
		t.Fatal("expected 'atlassian' server in config")
	}
	if atlassian.URL != "https://mcp.atlassian.com/v1/mcp" {
		t.Errorf("expected atlassian URL 'https://mcp.atlassian.com/v1/mcp', got %q", atlassian.URL)
	}
	if atlassian.Auth != "oauth" {
		t.Errorf("expected atlassian auth 'oauth', got %q", atlassian.Auth)
	}
	if atlassian.Command != "" {
		t.Errorf("expected empty command for URL-based server, got %q", atlassian.Command)
	}

	github, ok := servers["github"]
	if !ok {
		t.Fatal("expected 'github' server in config")
	}
	if github.URL != "https://mcp.github.com/sse" {
		t.Errorf("expected github URL 'https://mcp.github.com/sse', got %q", github.URL)
	}
	if github.Auth != "bearer" {
		t.Errorf("expected github auth 'bearer', got %q", github.Auth)
	}
}

func TestMCPServers_CommandBased(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := `
workspace_root: /test
mcp_servers:
  sequential-thinking:
    command: npx
    args:
      - "-y"
      - "@modelcontextprotocol/server-sequential-thinking"
  custom-tool:
    command: /usr/local/bin/my-mcp-server
    args:
      - "--port"
      - "8080"
    env:
      API_KEY: "secret123"
      DEBUG: "true"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	pol := New(cfg)
	servers := pol.MCPServers()

	if len(servers) != 2 {
		t.Fatalf("expected 2 MCP servers, got %d", len(servers))
	}

	st, ok := servers["sequential-thinking"]
	if !ok {
		t.Fatal("expected 'sequential-thinking' server in config")
	}
	if st.Command != "npx" {
		t.Errorf("expected command 'npx', got %q", st.Command)
	}
	expectedArgs := []string{"-y", "@modelcontextprotocol/server-sequential-thinking"}
	if !reflect.DeepEqual(st.Args, expectedArgs) {
		t.Errorf("expected args %v, got %v", expectedArgs, st.Args)
	}
	if st.URL != "" {
		t.Errorf("expected empty URL for command-based server, got %q", st.URL)
	}

	ct, ok := servers["custom-tool"]
	if !ok {
		t.Fatal("expected 'custom-tool' server in config")
	}
	if ct.Command != "/usr/local/bin/my-mcp-server" {
		t.Errorf("expected command '/usr/local/bin/my-mcp-server', got %q", ct.Command)
	}
	expectedEnv := map[string]string{"API_KEY": "secret123", "DEBUG": "true"}
	if !reflect.DeepEqual(ct.Env, expectedEnv) {
		t.Errorf("expected env %v, got %v", expectedEnv, ct.Env)
	}
}

func TestMCPServers_Mixed(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := `
workspace_root: /test
mcp_servers:
  atlassian:
    url: "https://mcp.atlassian.com/v1/mcp"
    auth: oauth
  sequential-thinking:
    command: npx
    args:
      - "-y"
      - "@modelcontextprotocol/server-sequential-thinking"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	pol := New(cfg)
	servers := pol.MCPServers()

	if len(servers) != 2 {
		t.Fatalf("expected 2 MCP servers, got %d", len(servers))
	}

	// URL-based
	atlassian := servers["atlassian"]
	if atlassian.URL == "" || atlassian.Command != "" {
		t.Errorf("expected atlassian to be URL-based, got URL=%q Command=%q", atlassian.URL, atlassian.Command)
	}

	// Command-based
	st := servers["sequential-thinking"]
	if st.Command == "" || st.URL != "" {
		t.Errorf("expected sequential-thinking to be command-based, got URL=%q Command=%q", st.URL, st.Command)
	}
}

func TestMCPServers_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := `
workspace_root: /test
`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	pol := New(cfg)
	servers := pol.MCPServers()

	if servers != nil {
		t.Errorf("expected nil MCPServers for config without mcp_servers section, got %v", servers)
	}
}

// ========== Audit config tests ==========

func TestAuditEnabled_DefaultTrue(t *testing.T) {
	pol := New(&Config{})
	if !pol.AuditEnabled() {
		t.Error("AuditEnabled() should default to true when Audit section is nil")
	}
}

func TestAuditEnabled_ExplicitlyDisabled(t *testing.T) {
	pol := New(&Config{
		Audit: &AuditConfig{Disabled: true},
	})
	if pol.AuditEnabled() {
		t.Error("AuditEnabled() should be false when Disabled=true")
	}
}

func TestAuditEnabled_SectionPresentNotDisabled(t *testing.T) {
	pol := New(&Config{
		Audit: &AuditConfig{},
	})
	if !pol.AuditEnabled() {
		t.Error("AuditEnabled() should be true when Audit section exists but Disabled is zero-value (false)")
	}
}

func TestAuditEnabled_FromYAML(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name   string
		yaml   string
		wantOn bool
	}{
		{
			name:   "no audit section",
			yaml:   "workspace_root: /test\n",
			wantOn: true,
		},
		{
			name:   "empty audit section",
			yaml:   "workspace_root: /test\naudit: {}\n",
			wantOn: true,
		},
		{
			name:   "disabled true",
			yaml:   "workspace_root: /test\naudit:\n  disabled: true\n",
			wantOn: false,
		},
		{
			name:   "disabled false",
			yaml:   "workspace_root: /test\naudit:\n  disabled: false\n",
			wantOn: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(tmpDir, tt.name+".yaml")
			if err := os.WriteFile(path, []byte(tt.yaml), 0644); err != nil {
				t.Fatalf("write config: %v", err)
			}
			cfg, err := LoadConfig(path)
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			pol := New(cfg)
			if pol.AuditEnabled() != tt.wantOn {
				t.Errorf("AuditEnabled() = %v, want %v", pol.AuditEnabled(), tt.wantOn)
			}
		})
	}
}

func TestAuditArgsMaxLen_Default(t *testing.T) {
	pol := New(&Config{})
	if pol.AuditArgsMaxLen() != 1000 {
		t.Errorf("AuditArgsMaxLen() = %d, want 1000", pol.AuditArgsMaxLen())
	}
}

func TestAuditArgsMaxLen_Custom(t *testing.T) {
	pol := New(&Config{
		Audit: &AuditConfig{ArgsMaxLen: 500},
	})
	if pol.AuditArgsMaxLen() != 500 {
		t.Errorf("AuditArgsMaxLen() = %d, want 500", pol.AuditArgsMaxLen())
	}
}

func TestAuditRetentionDays_Default(t *testing.T) {
	pol := New(&Config{})
	if pol.AuditRetentionDays() != 7 {
		t.Errorf("AuditRetentionDays() = %d, want 7", pol.AuditRetentionDays())
	}
}

func TestAuditRetentionDays_Custom(t *testing.T) {
	pol := New(&Config{
		Audit: &AuditConfig{RetentionDays: 30},
	})
	if pol.AuditRetentionDays() != 30 {
		t.Errorf("AuditRetentionDays() = %d, want 30", pol.AuditRetentionDays())
	}
}

func TestMaxTaskFailures_Default(t *testing.T) {
	pol := New(&Config{})
	if pol.MaxTaskFailures() != 3 {
		t.Errorf("MaxTaskFailures() = %d, want 3", pol.MaxTaskFailures())
	}
}

func TestMaxTaskFailures_Custom(t *testing.T) {
	pol := New(&Config{
		Orchestration: &OrchestrationConfig{MaxTaskFailures: 5},
	})
	if pol.MaxTaskFailures() != 5 {
		t.Errorf("MaxTaskFailures() = %d, want 5", pol.MaxTaskFailures())
	}
}

func TestMaxTaskFailures_FromYAML(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.yaml")

	yaml := `
workspace_root: /test
orchestration:
  driver: cursor
  max_task_failures: 10
  workers:
    - type: claude-code
      instances: 1
`
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	pol := New(cfg)
	if pol.MaxTaskFailures() != 10 {
		t.Errorf("MaxTaskFailures() = %d, want 10", pol.MaxTaskFailures())
	}
}

func TestMCPServers_EmptyMap(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := `
workspace_root: /test
mcp_servers: {}
`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	pol := New(cfg)
	servers := pol.MCPServers()

	if len(servers) != 0 {
		t.Errorf("expected empty MCPServers map, got %v", servers)
	}
}
