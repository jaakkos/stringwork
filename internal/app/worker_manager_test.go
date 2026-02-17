package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMcpBaseURL(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{"with /mcp path", "http://localhost:8943/mcp", "http://localhost:8943"},
		{"with /sse path", "http://localhost:8943/sse", "http://localhost:8943"},
		{"no path", "http://localhost:8943", "http://localhost:8943"},
		{"trailing slash", "http://localhost:8943/", "http://localhost:8943"},
		{"with port and path", "http://127.0.0.1:9000/mcp", "http://127.0.0.1:9000"},
		{"https", "https://example.com/mcp", "https://example.com"},
		{"invalid url", "not-a-url", "not-a-url"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mcpBaseURL(tc.input)
			if got != tc.expect {
				t.Errorf("mcpBaseURL(%q) = %q, want %q", tc.input, got, tc.expect)
			}
		})
	}
}

func TestIsClaudeCommand(t *testing.T) {
	tests := []struct {
		exe    string
		expect bool
	}{
		{"claude", true},
		{"/opt/homebrew/bin/claude", true},
		{"/usr/local/bin/claude", true},
		{"codex", false},
		{"/usr/bin/python3", false},
	}
	for _, tc := range tests {
		t.Run(tc.exe, func(t *testing.T) {
			if got := isClaudeCommand(tc.exe); got != tc.expect {
				t.Errorf("isClaudeCommand(%q) = %v, want %v", tc.exe, got, tc.expect)
			}
		})
	}
}

func TestIsCodexCommand(t *testing.T) {
	tests := []struct {
		exe    string
		expect bool
	}{
		{"codex", true},
		{"/opt/homebrew/bin/codex", true},
		{"/usr/local/bin/codex", true},
		{"claude", false},
		{"/usr/bin/python3", false},
	}
	for _, tc := range tests {
		t.Run(tc.exe, func(t *testing.T) {
			if got := isCodexCommand(tc.exe); got != tc.expect {
				t.Errorf("isCodexCommand(%q) = %v, want %v", tc.exe, got, tc.expect)
			}
		})
	}
}

func TestIsClaudeMCPConfigured(t *testing.T) {
	// Create a temp home dir with a .claude.json
	tmpHome := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	target := MCPServerEntry{Name: "stringwork", URL: "http://localhost:8943/mcp"}

	t.Run("no config file", func(t *testing.T) {
		if isClaudeMCPConfigured(target.Name, target) {
			t.Error("expected false when config file is missing")
		}
	})

	t.Run("config with correct URL", func(t *testing.T) {
		cfg := map[string]interface{}{
			"mcpServers": map[string]interface{}{
				"stringwork": map[string]string{
					"type": "http",
					"url":  "http://localhost:8943/mcp",
				},
			},
		}
		writeJSON(t, filepath.Join(tmpHome, ".claude.json"), cfg)
		if !isClaudeMCPConfigured(target.Name, target) {
			t.Error("expected true when URL matches exactly")
		}
	})

	t.Run("config with different path (/sse) is NOT a match", func(t *testing.T) {
		cfg := map[string]interface{}{
			"mcpServers": map[string]interface{}{
				"stringwork": map[string]string{
					"type": "http",
					"url":  "http://localhost:8943/sse",
				},
			},
		}
		writeJSON(t, filepath.Join(tmpHome, ".claude.json"), cfg)
		if isClaudeMCPConfigured(target.Name, target) {
			t.Error("expected false when path differs (/sse vs /mcp use different protocols)")
		}
	})

	t.Run("config with different port", func(t *testing.T) {
		cfg := map[string]interface{}{
			"mcpServers": map[string]interface{}{
				"stringwork": map[string]string{
					"type": "http",
					"url":  "http://localhost:9999/mcp",
				},
			},
		}
		writeJSON(t, filepath.Join(tmpHome, ".claude.json"), cfg)
		if isClaudeMCPConfigured(target.Name, target) {
			t.Error("expected false when different port")
		}
	})

	t.Run("config without stringwork", func(t *testing.T) {
		cfg := map[string]interface{}{
			"mcpServers": map[string]interface{}{
				"other-server": map[string]string{
					"url": "http://localhost:8943/mcp",
				},
			},
		}
		writeJSON(t, filepath.Join(tmpHome, ".claude.json"), cfg)
		if isClaudeMCPConfigured(target.Name, target) {
			t.Error("expected false when stringwork entry is missing")
		}
	})

	t.Run("stdio config with args and env", func(t *testing.T) {
		stdio := MCPServerEntry{
			Name:    "local-stdio",
			Command: "npx",
			Args:    []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"},
			Env:     map[string]string{"NODE_ENV": "test"},
		}
		cfg := map[string]interface{}{
			"mcpServers": map[string]interface{}{
				"local-stdio": map[string]interface{}{
					"type":    "stdio",
					"command": "npx",
					"args":    []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"},
					"env": map[string]string{
						"NODE_ENV": "test",
					},
				},
			},
		}
		writeJSON(t, filepath.Join(tmpHome, ".claude.json"), cfg)
		if !isClaudeMCPConfigured(stdio.Name, stdio) {
			t.Error("expected true when stdio config matches")
		}
	})
}

func TestIsCodexMCPConfigured(t *testing.T) {
	tmpHome := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	codexDir := filepath.Join(tmpHome, ".codex")
	os.MkdirAll(codexDir, 0755)
	configPath := filepath.Join(codexDir, "config.toml")
	target := MCPServerEntry{Name: "stringwork", URL: "http://localhost:8943/mcp"}

	t.Run("no config file", func(t *testing.T) {
		os.Remove(configPath)
		if isCodexMCPConfigured(target.Name, target) {
			t.Error("expected false when config file is missing")
		}
	})

	t.Run("config with exact URL match", func(t *testing.T) {
		toml := `[mcp_servers.stringwork]
url = "http://localhost:8943/mcp"
`
		os.WriteFile(configPath, []byte(toml), 0644)
		if !isCodexMCPConfigured(target.Name, target) {
			t.Error("expected true when URL matches exactly")
		}
	})

	t.Run("config with different path (/sse) is NOT a match", func(t *testing.T) {
		toml := `[mcp_servers.stringwork]
url = "http://localhost:8943/sse"
`
		os.WriteFile(configPath, []byte(toml), 0644)
		if isCodexMCPConfigured(target.Name, target) {
			t.Error("expected false when path differs (/sse vs /mcp use different protocols)")
		}
	})

	t.Run("config with different server", func(t *testing.T) {
		toml := `[mcp_servers.stringwork]
url = "http://other-host:9000/sse"
`
		os.WriteFile(configPath, []byte(toml), 0644)
		if isCodexMCPConfigured(target.Name, target) {
			t.Error("expected false when different host")
		}
	})

	t.Run("config without stringwork", func(t *testing.T) {
		toml := `[mcp_servers.other-server]
url = "http://localhost:8943/mcp"
`
		os.WriteFile(configPath, []byte(toml), 0644)
		if isCodexMCPConfigured(target.Name, target) {
			t.Error("expected false when stringwork section is missing")
		}
	})

	t.Run("URL in wrong section (false-positive check)", func(t *testing.T) {
		// The target URL exists in a *different* section — should NOT match.
		toml := `[mcp_servers.other-server]
url = "http://localhost:8943/mcp"

[mcp_servers.stringwork]
url = "http://localhost:9999/different"
`
		os.WriteFile(configPath, []byte(toml), 0644)
		if isCodexMCPConfigured(target.Name, target) {
			t.Error("expected false: URL is in other-server section, not stringwork")
		}
	})

	t.Run("command-based config", func(t *testing.T) {
		stdio := MCPServerEntry{
			Name:    "local-stdio",
			Command: "npx",
			Args:    []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"},
		}
		toml := `[mcp_servers.local-stdio]
command = "npx"
args = ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
`
		os.WriteFile(configPath, []byte(toml), 0644)
		if !isCodexMCPConfigured(stdio.Name, stdio) {
			t.Error("expected true for command-based server")
		}
	})
}

func TestMCPServerEntries(t *testing.T) {
	wm := &WorkerManager{}
	wm.SetMCPServerURL("http://localhost:8943/mcp")
	wm.SetMCPServers([]MCPServerEntry{
		{Name: "stringwork", URL: "http://other-host:9999/mcp"},
		{Name: "local-stdio", Command: "npx", Args: []string{"-y", "foo"}},
		{Name: "", URL: "http://ignore"},
	})
	entries := wm.mcpServerEntries()
	if len(entries) != 2 {
		t.Fatalf("expected 2 deduplicated entries, got %d", len(entries))
	}
	if entries[0].Name != "stringwork" || entries[0].URL != "http://localhost:8943/mcp" {
		t.Fatalf("unexpected first entry: %+v", entries[0])
	}
	if entries[1].Name != "local-stdio" || entries[1].Command != "npx" {
		t.Fatalf("unexpected second entry: %+v", entries[1])
	}
}

func writeJSON(t *testing.T, path string, v interface{}) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal JSON: %v", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
}
