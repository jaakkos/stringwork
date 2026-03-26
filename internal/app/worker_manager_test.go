package app

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jaakkos/stringwork/internal/domain"
)

func testLogger(t *testing.T) *log.Logger {
	t.Helper()
	return log.New(os.Stderr, "[test] ", log.LstdFlags)
}

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

func TestInjectClaudeWorktreeFlag(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		instanceID string
		want       []string
	}{
		{
			name:       "inserts -w after exe",
			args:       []string{"claude", "-p", "prompt"},
			instanceID: "claude-code-1",
			want:       []string{"claude", "-w", "claude-code-1", "-p", "prompt"},
		},
		{
			name:       "sanitizes instance id",
			args:       []string{"claude", "-p", "x"},
			instanceID: "agent/slash",
			want:       []string{"claude", "-w", "agentslash", "-p", "x"},
		},
		{
			name:       "empty args unchanged",
			args:       nil,
			instanceID: "claude-code-1",
			want:       nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := injectClaudeWorktreeFlag(tc.args, tc.instanceID)
			if len(got) != len(tc.want) {
				t.Errorf("injectClaudeWorktreeFlag: len = %d, want %d", len(got), len(tc.want))
				return
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("injectClaudeWorktreeFlag: [%d] = %q, want %q", i, got[i], tc.want[i])
				}
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
	wm.recordTerminalFailure("gemini", "", info)

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
	wm.recordTerminalFailure("claude-code", "", info)

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

func TestResolveWorkerBinary_AbsoluteExists(t *testing.T) {
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "myagent")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveWorkerBinary(bin)
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if resolved != bin {
		t.Errorf("expected %s, got %s", bin, resolved)
	}
}

func TestResolveWorkerBinary_AbsoluteMissing(t *testing.T) {
	_, err := resolveWorkerBinary("/nonexistent/path/to/agent")
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got: %v", err)
	}
}

func TestResolveWorkerBinary_AbsoluteNotExecutable(t *testing.T) {
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "noexec")
	if err := os.WriteFile(bin, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := resolveWorkerBinary(bin)
	if err == nil {
		t.Fatal("expected error for non-executable binary")
	}
	if !strings.Contains(err.Error(), "not executable") {
		t.Errorf("expected 'not executable' in error, got: %v", err)
	}
}

func TestResolveWorkerBinary_InPATH(t *testing.T) {
	resolved, err := resolveWorkerBinary("sh")
	if err != nil {
		t.Fatalf("expected 'sh' to be found: %v", err)
	}
	if resolved == "" {
		t.Error("expected non-empty path")
	}
}

func TestResolveWorkerBinary_NotInPATH(t *testing.T) {
	_, err := resolveWorkerBinary("stringwork_nonexistent_binary_xyz")
	if err == nil {
		t.Fatal("expected error for binary not in PATH")
	}
	if !strings.Contains(err.Error(), "not found in PATH") {
		t.Errorf("expected 'not found in PATH' in error, got: %v", err)
	}
}

func TestResolveWorkerBinary_Directory(t *testing.T) {
	tmp := t.TempDir()
	_, err := resolveWorkerBinary(tmp)
	if err == nil {
		t.Fatal("expected error for directory path")
	}
	if !strings.Contains(err.Error(), "directory") {
		t.Errorf("expected 'directory' in error, got: %v", err)
	}
}

func TestPreflight_NoConfigs(t *testing.T) {
	wm := &WorkerManager{configs: nil}
	results := wm.Preflight()
	if results != nil {
		t.Errorf("expected nil results for empty configs, got %v", results)
	}
}

func TestPreflight_MixedBinaries(t *testing.T) {
	tmp := t.TempDir()
	goodBin := filepath.Join(tmp, "good-agent")
	if err := os.WriteFile(goodBin, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}

	wm := &WorkerManager{
		configs: []WorkerSpawnConfig{
			{InstanceID: "good-1", AgentType: "good", Command: []string{goodBin, "-p", "do work"}},
			{InstanceID: "bad-1", AgentType: "bad", Command: []string{"/nonexistent/bad-agent"}},
		},
		logger:              testLogger(t),
		lastSpawn:           make(map[string]time.Time),
		runningWorkers:      make(map[string]context.CancelFunc),
		processRuntime:      make(map[string]*workerRuntime),
		consecutiveFailures: make(map[string]int),
		lastFailure:         make(map[string]time.Time),
		backoffUntil:        make(map[string]time.Time),
	}
	results := wm.Preflight()
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if !results[0].Found {
		t.Errorf("expected good binary to be found")
	}
	if results[0].Path != goodBin {
		t.Errorf("expected path %s, got %s", goodBin, results[0].Path)
	}
	if results[1].Found {
		t.Errorf("expected bad binary to NOT be found")
	}
	if results[1].Error == "" {
		t.Errorf("expected error message for bad binary")
	}
}

func TestPreflight_SendsMessageOnIssues(t *testing.T) {
	var mutated bool
	var msgContent string
	wm := &WorkerManager{
		configs: []WorkerSpawnConfig{
			{InstanceID: "missing-1", AgentType: "missing", Command: []string{"/nonexistent/missing-agent"}},
		},
		logger: testLogger(t),
		getAgent: func() string {
			return "cursor"
		},
		stateMutator: func(fn func(*domain.CollabState) error) error {
			mutated = true
			state := &domain.CollabState{}
			EnsureStateMaps(state)
			if err := fn(state); err != nil {
				return err
			}
			if len(state.Messages) > 0 {
				msgContent = state.Messages[0].Content
			}
			return nil
		},
		lastSpawn:           make(map[string]time.Time),
		runningWorkers:      make(map[string]context.CancelFunc),
		processRuntime:      make(map[string]*workerRuntime),
		consecutiveFailures: make(map[string]int),
		lastFailure:         make(map[string]time.Time),
		backoffUntil:        make(map[string]time.Time),
	}

	wm.Preflight()
	if !mutated {
		t.Fatal("expected stateMutator to be called for preflight issues")
	}
	if !strings.Contains(msgContent, "Preflight") {
		t.Errorf("expected message to contain 'Preflight', got: %s", msgContent)
	}
	if !strings.Contains(msgContent, "missing") {
		t.Errorf("expected message to mention 'missing' agent, got: %s", msgContent)
	}
}

func TestBuildTaskPrompt_Basic(t *testing.T) {
	task := &domain.Task{
		ID:          42,
		Title:       "Add auth middleware",
		Description: "Implement JWT validation",
		Priority:    2,
	}
	prompt := buildTaskPrompt(task, nil, "claude-code-task-42", "/workspace", "cursor")

	if !strings.Contains(prompt, "task #42") {
		t.Error("prompt should contain task ID")
	}
	if !strings.Contains(prompt, "Add auth middleware") {
		t.Error("prompt should contain task title")
	}
	if !strings.Contains(prompt, "JWT validation") {
		t.Error("prompt should contain task description")
	}
	if !strings.Contains(prompt, "high") {
		t.Error("prompt should contain priority name")
	}
	if !strings.Contains(prompt, "update_task id=42 status='in_progress'") {
		t.Error("prompt should contain step to mark in_progress")
	}
	if !strings.Contains(prompt, "update_task id=42 status='completed'") {
		t.Error("prompt should contain step to mark completed")
	}
	if !strings.Contains(prompt, "to='cursor'") {
		t.Error("prompt should contain send_message to driver")
	}
}

func TestBuildTaskPrompt_CustomDriver(t *testing.T) {
	task := &domain.Task{
		ID:       10,
		Title:    "Test task",
		Priority: 3,
	}
	prompt := buildTaskPrompt(task, nil, "codex-task-10", "/workspace", "claude-code")

	if !strings.Contains(prompt, "to='claude-code'") {
		t.Error("prompt should contain send_message to custom driver")
	}
	if strings.Contains(prompt, "to='cursor'") {
		t.Error("prompt should not contain hardcoded cursor when driver is different")
	}
}

func TestBuildTaskPrompt_WithWorkContext(t *testing.T) {
	task := &domain.Task{
		ID:       7,
		Title:    "Fix bug",
		Priority: 3,
	}
	wc := &domain.WorkContext{
		RelevantFiles: []string{"src/auth.go", "src/auth_test.go"},
		Background:    "Auth module uses JWT tokens",
		Constraints:   []string{"do not modify the public API", "read-only investigation"},
	}
	prompt := buildTaskPrompt(task, wc, "codex-task-7", "/project", "cursor")

	if !strings.Contains(prompt, "src/auth.go") {
		t.Error("prompt should contain relevant files")
	}
	if !strings.Contains(prompt, "JWT tokens") {
		t.Error("prompt should contain background")
	}
	if !strings.Contains(prompt, "do not modify the public API") {
		t.Error("prompt should contain constraints")
	}
	if !strings.Contains(prompt, "read-only investigation") {
		t.Error("prompt should contain all constraints")
	}
}

func TestBuildTaskPrompt_NoDescription(t *testing.T) {
	task := &domain.Task{
		ID:       1,
		Title:    "Quick task",
		Priority: 3,
	}
	prompt := buildTaskPrompt(task, nil, "agent", "/ws", "cursor")

	if strings.Contains(prompt, "Description:") {
		t.Error("prompt should not contain Description label when description is empty")
	}
}

func TestAppendPromptToCommand(t *testing.T) {
	base := []string{"claude", "-p", "You are a worker."}
	task := "\n\nTASK: do stuff"
	result := appendPromptToCommand(base, task)

	if len(result) != 3 {
		t.Fatalf("expected 3 args, got %d", len(result))
	}
	if result[0] != "claude" || result[1] != "-p" {
		t.Error("first args should be unchanged")
	}
	if result[2] != "You are a worker.\n\nTASK: do stuff" {
		t.Errorf("prompt arg should have task appended, got: %s", result[2])
	}
	if base[2] != "You are a worker." {
		t.Error("original command should not be modified")
	}
}

func TestAppendPromptToCommand_WithTrailingFlags(t *testing.T) {
	base := []string{"claude", "-p", "You are a worker.", "--dangerously-skip-permissions"}
	task := "\n\nTASK: do stuff"
	result := appendPromptToCommand(base, task)

	if len(result) != 4 {
		t.Fatalf("expected 4 args, got %d", len(result))
	}
	if result[2] != "You are a worker.\n\nTASK: do stuff" {
		t.Errorf("prompt arg (after -p) should have task appended, got: %s", result[2])
	}
	if result[3] != "--dangerously-skip-permissions" {
		t.Errorf("trailing flag should be unchanged, got: %s", result[3])
	}
}

func TestAppendPromptToCommand_GeminiPromptFlag(t *testing.T) {
	base := []string{"gemini", "--yolo", "--prompt", "You are gemini."}
	task := "\n\nTASK: do stuff"
	result := appendPromptToCommand(base, task)

	if result[3] != "You are gemini.\n\nTASK: do stuff" {
		t.Errorf("prompt arg (after --prompt) should have task appended, got: %s", result[3])
	}
	if result[1] != "--yolo" {
		t.Errorf("other flags should be unchanged, got: %s", result[1])
	}
}

func TestAppendPromptToCommand_CodexExec(t *testing.T) {
	base := []string{"codex", "exec", "--sandbox", "danger-full-access", "You are codex."}
	task := "\n\nTASK: do stuff"
	result := appendPromptToCommand(base, task)

	if result[4] != "You are codex.\n\nTASK: do stuff" {
		t.Errorf("last positional arg should have task appended, got: %s", result[4])
	}
}

func TestAppendPromptToCommand_Empty(t *testing.T) {
	result := appendPromptToCommand(nil, "task")
	if len(result) != 0 {
		t.Errorf("expected empty result for nil command, got %d args", len(result))
	}
}

func TestFindConfigForAgent(t *testing.T) {
	wm := &WorkerManager{
		configs: []WorkerSpawnConfig{
			{InstanceID: "claude-code", AgentType: "claude-code", Command: []string{"claude", "-p", "prompt"}},
			{InstanceID: "codex", AgentType: "codex", Command: []string{"codex", "exec", "prompt"}},
		},
	}

	cfg := wm.findConfigForAgent("claude-code")
	if cfg == nil || cfg.AgentType != "claude-code" {
		t.Error("should find claude-code config by instance ID")
	}

	cfg = wm.findConfigForAgent("codex")
	if cfg == nil || cfg.AgentType != "codex" {
		t.Error("should find codex config by agent type")
	}

	cfg = wm.findConfigForAgent("nonexistent")
	if cfg != nil {
		t.Error("should return nil for unknown agent")
	}
}

func TestCountRunningByType(t *testing.T) {
	wm := &WorkerManager{
		configs: []WorkerSpawnConfig{
			{InstanceID: "claude-code", AgentType: "claude-code"},
		},
		runningWorkers: map[string]context.CancelFunc{
			"claude-code-task-1": func() {},
			"claude-code-task-5": func() {},
			"codex-task-3":       func() {},
		},
	}

	count := wm.countRunningByType("claude-code")
	if count != 2 {
		t.Errorf("expected 2 running claude-code workers, got %d", count)
	}

	count = wm.countRunningByType("codex")
	if count != 1 {
		t.Errorf("expected 1 running codex worker, got %d", count)
	}

	count = wm.countRunningByType("gemini")
	if count != 0 {
		t.Errorf("expected 0 running gemini workers, got %d", count)
	}
}

func TestInstanceLimitForType(t *testing.T) {
	wm := &WorkerManager{
		configs: []WorkerSpawnConfig{
			{InstanceID: "claude-code-1", AgentType: "claude-code"},
			{InstanceID: "claude-code-2", AgentType: "claude-code"},
			{InstanceID: "codex", AgentType: "codex"},
		},
	}

	if limit := wm.instanceLimitForType("claude-code"); limit != 2 {
		t.Errorf("expected limit 2 for claude-code, got %d", limit)
	}
	if limit := wm.instanceLimitForType("codex"); limit != 1 {
		t.Errorf("expected limit 1 for codex, got %d", limit)
	}
	if limit := wm.instanceLimitForType("unknown"); limit != 1 {
		t.Errorf("expected default limit 1 for unknown type, got %d", limit)
	}
}

func TestEnqueueAndDrainSpawn(t *testing.T) {
	wm := &WorkerManager{
		pendingSpawns: make(map[string][]pendingSpawn),
	}

	wm.enqueueSpawn("claude-code", 1)
	wm.enqueueSpawn("claude-code", 2)
	wm.enqueueSpawn("claude-code", 1) // duplicate, should be ignored

	if count := wm.PendingSpawnCount("claude-code"); count != 2 {
		t.Errorf("expected 2 queued tasks, got %d", count)
	}

	if count := wm.PendingSpawnCount("codex"); count != 0 {
		t.Errorf("expected 0 queued codex tasks, got %d", count)
	}
}

func TestEnqueueSpawn_FIFOOrder(t *testing.T) {
	wm := &WorkerManager{
		pendingSpawns: make(map[string][]pendingSpawn),
	}

	wm.enqueueSpawn("claude-code", 10)
	wm.enqueueSpawn("claude-code", 20)
	wm.enqueueSpawn("claude-code", 30)

	wm.mu.Lock()
	queue := wm.pendingSpawns["claude-code"]
	wm.mu.Unlock()

	if len(queue) != 3 {
		t.Fatalf("expected 3 items, got %d", len(queue))
	}
	if queue[0].TaskID != 10 || queue[1].TaskID != 20 || queue[2].TaskID != 30 {
		t.Errorf("expected FIFO order [10,20,30], got [%d,%d,%d]",
			queue[0].TaskID, queue[1].TaskID, queue[2].TaskID)
	}
}

func TestRecordTerminalFailure_SetsAgentTypeBackoff(t *testing.T) {
	wm := &WorkerManager{
		configs: []WorkerSpawnConfig{
			{InstanceID: "gemini", AgentType: "gemini"},
		},
		consecutiveFailures: make(map[string]int),
		lastFailure:         make(map[string]time.Time),
		backoffUntil:        make(map[string]time.Time),
	}

	info := workerErrorInfo{
		Class:      workerErrorQuotaExhausted,
		Summary:    "API quota exhausted (resets in 2h)",
		RetryAfter: 2 * time.Hour,
	}
	// Simulate a task worker instance hitting a quota error
	wm.recordTerminalFailure("gemini-task-42", "gemini", info)

	// Both the instance and the agent type should be blocked
	blocked, _ := wm.failureBackoffBlocked("gemini-task-42")
	if !blocked {
		t.Error("instance gemini-task-42 should be blocked")
	}
	blocked, _ = wm.failureBackoffBlocked("gemini")
	if !blocked {
		t.Error("agent type gemini should be blocked (prevents new task spawns)")
	}
}

func TestRecordTerminalFailure_PermanentBlockSetsAgentType(t *testing.T) {
	wm := &WorkerManager{
		configs: []WorkerSpawnConfig{
			{InstanceID: "gemini", AgentType: "gemini"},
		},
		consecutiveFailures: make(map[string]int),
		lastFailure:         make(map[string]time.Time),
		backoffUntil:        make(map[string]time.Time),
	}

	info := workerErrorInfo{
		Class:   workerErrorAuth,
		Summary: "authentication failure",
	}
	wm.recordTerminalFailure("gemini-task-5", "gemini", info)

	blocked, remaining := wm.failureBackoffBlocked("gemini")
	if !blocked {
		t.Error("agent type gemini should be permanently blocked")
	}
	if remaining != 0 {
		t.Error("permanent block should have remaining=0")
	}
}

func TestResetFailureBackoff_ClearsAgentType(t *testing.T) {
	wm := &WorkerManager{
		configs: []WorkerSpawnConfig{
			{InstanceID: "gemini", AgentType: "gemini"},
		},
		consecutiveFailures: map[string]int{"gemini": failureBackoffMaxCount, "gemini-task-42": failureBackoffMaxCount},
		lastFailure:         map[string]time.Time{"gemini": time.Now(), "gemini-task-42": time.Now()},
		lastSpawn:           make(map[string]time.Time),
		backoffUntil:        make(map[string]time.Time),
	}

	wm.ResetFailureBackoff("gemini")

	blocked, _ := wm.failureBackoffBlocked("gemini")
	if blocked {
		t.Error("agent type should not be blocked after reset")
	}
}

func TestBackedOffAgentTypes_ReturnsRateLimited(t *testing.T) {
	wm := &WorkerManager{
		configs: []WorkerSpawnConfig{
			{InstanceID: "claude-code", AgentType: "claude-code"},
			{InstanceID: "gemini", AgentType: "gemini"},
			{InstanceID: "codex", AgentType: "codex"},
		},
		consecutiveFailures: map[string]int{
			"gemini": 1,
			"codex":  failureBackoffMaxCount,
		},
		lastFailure: map[string]time.Time{
			"gemini": time.Now(),
			"codex":  time.Now(),
		},
		backoffUntil: map[string]time.Time{
			"gemini": time.Now().Add(2 * time.Hour),
		},
	}

	backedOff := wm.BackedOffAgentTypes()
	if len(backedOff) != 2 {
		t.Fatalf("expected 2 backed-off types, got %d: %v", len(backedOff), backedOff)
	}
	has := make(map[string]bool)
	for _, a := range backedOff {
		has[a] = true
	}
	if !has["gemini"] {
		t.Error("gemini should be backed off (rate-limited with deadline)")
	}
	if !has["codex"] {
		t.Error("codex should be backed off (permanently blocked)")
	}
	if has["claude-code"] {
		t.Error("claude-code should NOT be backed off")
	}
}

func TestBackedOffAgentTypes_Empty(t *testing.T) {
	wm := &WorkerManager{
		configs: []WorkerSpawnConfig{
			{InstanceID: "claude-code", AgentType: "claude-code"},
		},
		consecutiveFailures: make(map[string]int),
		lastFailure:         make(map[string]time.Time),
		backoffUntil:        make(map[string]time.Time),
	}

	backedOff := wm.BackedOffAgentTypes()
	if len(backedOff) != 0 {
		t.Errorf("expected 0 backed-off types, got %d", len(backedOff))
	}
}

func TestBackoffInfoForType(t *testing.T) {
	wm := &WorkerManager{
		configs: []WorkerSpawnConfig{
			{InstanceID: "gemini", AgentType: "gemini"},
		},
		consecutiveFailures: map[string]int{"gemini": 1},
		lastFailure:         map[string]time.Time{"gemini": time.Now()},
		backoffUntil:        map[string]time.Time{"gemini": time.Now().Add(2 * time.Hour)},
	}

	blocked, remaining, reason := wm.BackoffInfoForType("gemini")
	if !blocked {
		t.Error("gemini should be blocked")
	}
	if remaining < 1*time.Hour || remaining > 3*time.Hour {
		t.Errorf("remaining should be ~2h, got %v", remaining)
	}
	if reason != "rate-limited" {
		t.Errorf("reason should be 'rate-limited', got %q", reason)
	}

	blocked, _, _ = wm.BackoffInfoForType("claude-code")
	if blocked {
		t.Error("claude-code should not be blocked")
	}
}

func TestDrainQueue_RespectsBackoff(t *testing.T) {
	wm := &WorkerManager{
		configs: []WorkerSpawnConfig{
			{InstanceID: "gemini", AgentType: "gemini", Command: []string{"echo", "test"}},
		},
		consecutiveFailures: map[string]int{"gemini": failureBackoffMaxCount},
		lastFailure:         map[string]time.Time{"gemini": time.Now()},
		backoffUntil:        make(map[string]time.Time),
		lastSpawn:           make(map[string]time.Time),
		runningWorkers:      make(map[string]context.CancelFunc),
		pendingSpawns: map[string][]pendingSpawn{
			"gemini": {{TaskID: 1, AgentType: "gemini"}},
		},
		logger: testLogger(t),
	}

	wm.drainQueue("gemini")

	// Task should be re-queued, not spawned
	if count := wm.PendingSpawnCount("gemini"); count != 1 {
		t.Errorf("expected task to be re-queued (1 pending), got %d", count)
	}
}

func TestCheckOnlySpawnsForMessages(t *testing.T) {
	state := &domain.CollabState{
		Tasks: []domain.Task{
			{ID: 1, Title: "Pending task", Status: "pending", AssignedTo: "claude-code", CreatedBy: "cursor"},
		},
		NextTaskID: 2,
	}
	EnsureStateMaps(state)

	spawned := false
	wm := &WorkerManager{
		configs: []WorkerSpawnConfig{
			{InstanceID: "claude-code", AgentType: "claude-code", Command: []string{"echo", "test"}, Cooldown: 0},
		},
		getAgent:    func() string { return "cursor" },
		stateLoader: func() (*domain.CollabState, error) { return state, nil },
		stateMutator: func(fn func(*domain.CollabState) error) error {
			spawned = true
			return fn(state)
		},
		logger:              testLogger(t),
		lastSpawn:           make(map[string]time.Time),
		runningWorkers:      make(map[string]context.CancelFunc),
		mcpRegistered:       make(map[string]bool),
		processRuntime:      make(map[string]*workerRuntime),
		consecutiveFailures: make(map[string]int),
		lastFailure:         make(map[string]time.Time),
		backoffUntil:        make(map[string]time.Time),
		pendingSpawns:       make(map[string][]pendingSpawn),
	}
	state.Presence["cursor"] = &domain.Presence{Workspace: "/workspace"}

	// Check should NOT spawn for pending tasks (only for messages)
	wm.Check()

	if spawned {
		t.Error("Check() should not spawn workers for pending tasks — use SpawnForTask instead")
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

func TestTailBuffer_MutexSafety(t *testing.T) {
	tb := newTailBuffer(1024)
	done := make(chan struct{})

	// Writer goroutine
	go func() {
		for i := 0; i < 1000; i++ {
			tb.Write([]byte("hello world\n"))
		}
		close(done)
	}()

	// Reader goroutine — must not race
	for i := 0; i < 100; i++ {
		_ = tb.String()
		_ = tb.Bytes()
	}
	<-done

	s := tb.String()
	if s == "" {
		t.Error("expected non-empty tail buffer after writes")
	}
}

func TestTailBuffer_Bytes(t *testing.T) {
	tb := newTailBuffer(100)
	if tb.Bytes() != 0 {
		t.Fatalf("empty buffer should report 0 bytes, got %d", tb.Bytes())
	}
	tb.Write([]byte("hello"))
	if tb.Bytes() != 5 {
		t.Fatalf("expected 5 bytes, got %d", tb.Bytes())
	}
	// Overflow: write more than buffer size
	tb.Write(make([]byte, 200))
	if tb.Bytes() != 100 {
		t.Fatalf("expected full buffer size (100), got %d", tb.Bytes())
	}
}

func TestWorkerRuntime_Lifecycle(t *testing.T) {
	wm := &WorkerManager{
		logger:              testLogger(t),
		lastSpawn:           make(map[string]time.Time),
		runningWorkers:      make(map[string]context.CancelFunc),
		processRuntime:      make(map[string]*workerRuntime),
		consecutiveFailures: make(map[string]int),
		lastFailure:         make(map[string]time.Time),
		backoffUntil:        make(map[string]time.Time),
	}

	// Simulate what runOnce does: create runtime, then clean up
	pInfo := &ProcessInfo{
		InstanceID:   "test-worker",
		StartedAt:    time.Now(),
		LastOutputAt: time.Now(),
		WorkspaceDir: "/tmp",
		LogPath:      "/tmp/test.log",
	}
	tail := newTailBuffer(16384)
	tail.Write([]byte("some output"))

	wm.mu.Lock()
	wm.processRuntime["test-worker"] = &workerRuntime{info: pInfo, tail: tail}
	wm.mu.Unlock()

	// GetProcessInfo should return the info
	procs := wm.GetProcessInfo()
	if _, ok := procs["test-worker"]; !ok {
		t.Fatal("expected test-worker in GetProcessInfo")
	}
	if procs["test-worker"].LogPath != "/tmp/test.log" {
		t.Errorf("expected LogPath '/tmp/test.log', got '%s'", procs["test-worker"].LogPath)
	}

	// GetRecentOutput should return the tail
	output := wm.GetRecentOutput("test-worker")
	if output != "some output" {
		t.Errorf("expected 'some output', got '%s'", output)
	}

	// Clean up (simulate runOnce defer)
	wm.mu.Lock()
	delete(wm.processRuntime, "test-worker")
	wm.mu.Unlock()

	// After cleanup, should be empty
	if wm.GetRecentOutput("test-worker") != "" {
		t.Error("expected empty output after cleanup")
	}
	procs = wm.GetProcessInfo()
	if _, ok := procs["test-worker"]; ok {
		t.Error("expected test-worker removed from GetProcessInfo after cleanup")
	}
}

func TestGetRecentOutput_NonexistentWorker(t *testing.T) {
	wm := &WorkerManager{
		processRuntime: make(map[string]*workerRuntime),
	}
	output := wm.GetRecentOutput("nonexistent")
	if output != "" {
		t.Errorf("expected empty string for nonexistent worker, got '%s'", output)
	}
}
