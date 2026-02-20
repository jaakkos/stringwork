package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestTailBuffer_ShortWrite(t *testing.T) {
	tb := newTailBuffer(32)
	tb.Write([]byte("hello"))
	if got := tb.String(); got != "hello" {
		t.Fatalf("got %q, want %q", got, "hello")
	}
}

func TestTailBuffer_ExactFit(t *testing.T) {
	tb := newTailBuffer(5)
	tb.Write([]byte("abcde"))
	if got := tb.String(); got != "abcde" {
		t.Fatalf("got %q, want %q", got, "abcde")
	}
}

func TestTailBuffer_Overflow(t *testing.T) {
	tb := newTailBuffer(8)
	tb.Write([]byte("hello"))
	tb.Write([]byte(" world!"))
	got := tb.String()
	if got != "o world!" {
		t.Fatalf("got %q, want %q", got, "o world!")
	}
}

func TestTailBuffer_SingleLargeWrite(t *testing.T) {
	tb := newTailBuffer(4)
	tb.Write([]byte("abcdefghij"))
	if got := tb.String(); got != "ghij" {
		t.Fatalf("got %q, want %q", got, "ghij")
	}
}

func TestTailBuffer_ManySmallWrites(t *testing.T) {
	tb := newTailBuffer(6)
	for _, ch := range "abcdefghij" {
		tb.Write([]byte(string(ch)))
	}
	if got := tb.String(); got != "efghij" {
		t.Fatalf("got %q, want %q", got, "efghij")
	}
}

func TestFailureBackoff_Exponential(t *testing.T) {
	wm := &WorkerManager{
		consecutiveFailures: make(map[string]int),
		lastFailure:         make(map[string]time.Time),
		lastSpawn:           make(map[string]time.Time),
		backoffUntil:        make(map[string]time.Time),
	}

	tests := []struct {
		failures int
		expect   time.Duration
	}{
		{0, 0},
		{1, 1 * time.Minute},
		{2, 2 * time.Minute},
		{3, 4 * time.Minute},
		{4, 8 * time.Minute},
		{5, 10 * time.Minute},  // capped
		{10, 10 * time.Minute}, // still capped
	}
	for _, tc := range tests {
		wm.mu.Lock()
		wm.consecutiveFailures["test-worker"] = tc.failures
		wm.mu.Unlock()

		got := wm.failureBackoff("test-worker")
		if got != tc.expect {
			t.Errorf("failureBackoff(failures=%d) = %v, want %v", tc.failures, got, tc.expect)
		}
	}
}

func TestFailureBackoffBlocked_NotBlockedInitially(t *testing.T) {
	wm := &WorkerManager{
		consecutiveFailures: make(map[string]int),
		lastFailure:         make(map[string]time.Time),
		lastSpawn:           make(map[string]time.Time),
		backoffUntil:        make(map[string]time.Time),
	}

	blocked, _ := wm.failureBackoffBlocked("fresh-worker")
	if blocked {
		t.Error("new worker should not be blocked")
	}
}

func TestFailureBackoffBlocked_BlockedDuringBackoff(t *testing.T) {
	wm := &WorkerManager{
		consecutiveFailures: map[string]int{"w": 2},
		lastFailure:         map[string]time.Time{"w": time.Now()},
		lastSpawn:           make(map[string]time.Time),
		backoffUntil:        make(map[string]time.Time),
	}

	blocked, remaining := wm.failureBackoffBlocked("w")
	if !blocked {
		t.Error("should be blocked during backoff period")
	}
	if remaining <= 0 {
		t.Errorf("remaining should be positive, got %v", remaining)
	}
}

func TestFailureBackoffBlocked_UnblockedAfterBackoff(t *testing.T) {
	wm := &WorkerManager{
		consecutiveFailures: map[string]int{"w": 1},
		lastFailure:         map[string]time.Time{"w": time.Now().Add(-2 * time.Minute)},
		lastSpawn:           make(map[string]time.Time),
		backoffUntil:        make(map[string]time.Time),
	}

	blocked, _ := wm.failureBackoffBlocked("w")
	if blocked {
		t.Error("should not be blocked after backoff period elapsed (1 failure = 1 min backoff)")
	}
}

func TestFailureBackoffBlocked_PermanentAfterMaxCount(t *testing.T) {
	wm := &WorkerManager{
		consecutiveFailures: map[string]int{"w": failureBackoffMaxCount},
		lastFailure:         map[string]time.Time{"w": time.Now()},
		lastSpawn:           make(map[string]time.Time),
		backoffUntil:        make(map[string]time.Time),
	}

	blocked, remaining := wm.failureBackoffBlocked("w")
	if !blocked {
		t.Error("should be permanently blocked after max consecutive failures")
	}
	if remaining != 0 {
		t.Errorf("permanent block should have remaining=0, got %v", remaining)
	}
}

func TestResetFailureBackoff(t *testing.T) {
	wm := &WorkerManager{
		consecutiveFailures: map[string]int{"w": 5},
		lastFailure:         map[string]time.Time{"w": time.Now()},
		lastSpawn:           make(map[string]time.Time),
		backoffUntil:        map[string]time.Time{"w": time.Now().Add(10 * time.Hour)},
	}

	wm.ResetFailureBackoff("w")

	blocked, _ := wm.failureBackoffBlocked("w")
	if blocked {
		t.Error("should not be blocked after reset")
	}
	wm.mu.Lock()
	if wm.consecutiveFailures["w"] != 0 {
		t.Errorf("consecutive failures should be 0 after reset, got %d", wm.consecutiveFailures["w"])
	}
	wm.mu.Unlock()
}

func TestClassifyWorkerError_QuotaWithResetTime(t *testing.T) {
	output := `TerminalQuotaError: You have exhausted your capacity on this model. Your quota will reset after 17h29m42s.
    at classifyGoogleError (file:///some/path.js:214:28)`

	info := classifyWorkerError(output)
	if info.Class != workerErrorQuotaExhausted {
		t.Fatalf("expected quota_exhausted, got %s", info.Class)
	}
	// 17h29m42s → should be approximately 17h30m
	if info.RetryAfter < 17*time.Hour || info.RetryAfter > 18*time.Hour {
		t.Fatalf("expected ~17h30m retry after, got %v", info.RetryAfter)
	}
	if !strings.Contains(info.Summary, "resets in") {
		t.Fatalf("summary should mention reset time, got %q", info.Summary)
	}
}

func TestClassifyWorkerError_QuotaWithoutResetTime(t *testing.T) {
	output := `Error: 429 Too many requests. You have exceeded your quota.`

	info := classifyWorkerError(output)
	if info.Class != workerErrorQuotaExhausted {
		t.Fatalf("expected quota_exhausted, got %s", info.Class)
	}
	if info.RetryAfter != 0 {
		t.Fatalf("expected zero retry after when no reset time, got %v", info.RetryAfter)
	}
}

func TestClassifyWorkerError_AuthFailure(t *testing.T) {
	tests := []string{
		`Error: API key expired. Please renew at https://console.example.com`,
		`Error: Invalid API key provided`,
		`401 Unauthorized: authentication failed`,
	}
	for _, output := range tests {
		info := classifyWorkerError(output)
		if info.Class != workerErrorAuth {
			t.Errorf("expected auth_failure for %q, got %s", output, info.Class)
		}
	}
}

func TestClassifyWorkerError_NotFound(t *testing.T) {
	tests := []string{
		`/bin/sh: gemini: command not found`,
		`Error: ENOENT: no such file or directory`,
	}
	for _, output := range tests {
		info := classifyWorkerError(output)
		if info.Class != workerErrorNotFound {
			t.Errorf("expected not_found for %q, got %s", output, info.Class)
		}
	}
}

func TestClassifyWorkerError_Transient(t *testing.T) {
	output := `some random error that doesn't match any pattern`
	info := classifyWorkerError(output)
	if info.Class != workerErrorTransient {
		t.Fatalf("expected transient, got %s", info.Class)
	}
}

func TestClassifyWorkerError_EmptyOutput(t *testing.T) {
	info := classifyWorkerError("")
	if info.Class != workerErrorTransient {
		t.Fatalf("expected transient for empty output, got %s", info.Class)
	}
}

func TestBackoffUntil_BlockedUntilDeadline(t *testing.T) {
	deadline := time.Now().Add(2 * time.Hour)
	wm := &WorkerManager{
		consecutiveFailures: map[string]int{"w": 1},
		lastFailure:         map[string]time.Time{"w": time.Now()},
		lastSpawn:           make(map[string]time.Time),
		backoffUntil:        map[string]time.Time{"w": deadline},
	}

	blocked, remaining := wm.failureBackoffBlocked("w")
	if !blocked {
		t.Error("should be blocked until deadline")
	}
	if remaining < 1*time.Hour || remaining > 3*time.Hour {
		t.Errorf("remaining should be ~2h, got %v", remaining)
	}
}

func TestBackoffUntil_UnblockedAfterDeadline(t *testing.T) {
	wm := &WorkerManager{
		consecutiveFailures: map[string]int{"w": 1},
		lastFailure:         map[string]time.Time{"w": time.Now().Add(-1 * time.Hour)},
		lastSpawn:           make(map[string]time.Time),
		backoffUntil:        map[string]time.Time{"w": time.Now().Add(-1 * time.Minute)},
	}

	blocked, _ := wm.failureBackoffBlocked("w")
	if blocked {
		t.Error("should not be blocked after deadline has passed")
	}
}

func TestRecordTerminalFailure_WithRetryAfter(t *testing.T) {
	wm := &WorkerManager{
		consecutiveFailures: make(map[string]int),
		lastFailure:         make(map[string]time.Time),
		backoffUntil:        make(map[string]time.Time),
	}

	info := workerErrorInfo{
		Class:      workerErrorQuotaExhausted,
		Summary:    "API quota exhausted (resets in 17h30m)",
		RetryAfter: 17*time.Hour + 30*time.Minute,
	}
	wm.recordTerminalFailure("gemini", info)

	wm.mu.Lock()
	failures := wm.consecutiveFailures["gemini"]
	until := wm.backoffUntil["gemini"]
	wm.mu.Unlock()

	if failures != 1 {
		t.Errorf("expected consecutiveFailures=1, got %d", failures)
	}
	if until.Before(time.Now().Add(17 * time.Hour)) {
		t.Errorf("backoffUntil should be ~17h30m from now, got %v", until)
	}
}

func TestRecordTerminalFailure_WithoutRetryAfter(t *testing.T) {
	wm := &WorkerManager{
		consecutiveFailures: make(map[string]int),
		lastFailure:         make(map[string]time.Time),
		backoffUntil:        make(map[string]time.Time),
	}

	info := workerErrorInfo{
		Class:   workerErrorAuth,
		Summary: "authentication failure",
	}
	wm.recordTerminalFailure("claude-code", info)

	wm.mu.Lock()
	failures := wm.consecutiveFailures["claude-code"]
	wm.mu.Unlock()

	if failures != failureBackoffMaxCount {
		t.Errorf("auth failure without retry-after should set max failures, got %d", failures)
	}
}

func TestWorkerErrorClass_Terminal(t *testing.T) {
	if workerErrorTransient.Terminal() {
		t.Error("transient should not be terminal")
	}
	if !workerErrorQuotaExhausted.Terminal() {
		t.Error("quota_exhausted should be terminal")
	}
	if !workerErrorAuth.Terminal() {
		t.Error("auth_failure should be terminal")
	}
	if !workerErrorNotFound.Terminal() {
		t.Error("not_found should be terminal")
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
