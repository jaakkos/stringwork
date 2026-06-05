package app

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jaakkos/stringwork/internal/constitution"
	"github.com/jaakkos/stringwork/internal/domain"
	"github.com/jaakkos/stringwork/internal/policy"
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
	prompt := buildTaskPrompt(task, nil, "claude-code-task-42", "/workspace", "cursor", "mcp", "")

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
	prompt := buildTaskPrompt(task, nil, "codex-task-10", "/workspace", "claude-code", "mcp", "")

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
	prompt := buildTaskPrompt(task, wc, "codex-task-7", "/project", "cursor", "mcp", "")

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
	prompt := buildTaskPrompt(task, nil, "agent", "/ws", "cursor", "mcp", "")

	if strings.Contains(prompt, "Description:") {
		t.Error("prompt should not contain Description label when description is empty")
	}
}

func TestBuildTaskPrompt_CLIMode(t *testing.T) {
	task := &domain.Task{
		ID:       42,
		Title:    "Add auth middleware",
		Priority: 2,
	}
	prompt := buildTaskPrompt(task, nil, "codex-task-42", "/workspace", "cursor", "cli", "")

	if !strings.Contains(prompt, "COMMUNICATION: Use shell commands") {
		t.Error("CLI mode prompt should contain CLI communication header")
	}
	if !strings.Contains(prompt, "$STRINGWORK_BIN heartbeat") {
		t.Error("CLI mode prompt should reference $STRINGWORK_BIN for heartbeat")
	}
	if !strings.Contains(prompt, "$STRINGWORK_BIN progress") {
		t.Error("CLI mode prompt should reference $STRINGWORK_BIN for progress")
	}
	if !strings.Contains(prompt, "$STRINGWORK_BIN send --from codex-task-42 --to cursor") {
		t.Error("CLI mode prompt should reference send command with correct from/to")
	}
	if !strings.Contains(prompt, "$STRINGWORK_BIN task update --id 42") {
		t.Error("CLI mode prompt should reference task update command")
	}
	if strings.Contains(prompt, "set_presence agent=") {
		t.Error("CLI mode prompt should NOT contain MCP-style set_presence")
	}
	if strings.Contains(prompt, "update_task id=") {
		t.Error("CLI mode prompt should NOT contain MCP-style update_task")
	}
}

func TestBuildTaskPrompt_CLIMode_WithWorkContext(t *testing.T) {
	task := &domain.Task{
		ID:       7,
		Title:    "Fix bug",
		Priority: 3,
	}
	wc := &domain.WorkContext{
		RelevantFiles: []string{"src/auth.go"},
		Constraints:   []string{"read-only"},
	}
	prompt := buildTaskPrompt(task, wc, "codex-task-7", "/project", "cursor", "cli", "")

	if !strings.Contains(prompt, "src/auth.go") {
		t.Error("CLI mode prompt should include relevant files")
	}
	if !strings.Contains(prompt, "read-only") {
		t.Error("CLI mode prompt should include constraints")
	}
	if !strings.Contains(prompt, "$STRINGWORK_BIN") {
		t.Error("CLI mode prompt should use CLI commands")
	}
}

func TestBuildTaskPrompt_EmptyCommDefaultsToCLI(t *testing.T) {
	task := &domain.Task{ID: 1, Title: "Test"}
	prompt := buildTaskPrompt(task, nil, "codex-1", "/ws", "cursor", "", "")
	if !strings.Contains(prompt, "$STRINGWORK_BIN") {
		t.Error("empty communication should default to CLI mode")
	}
	if strings.Contains(prompt, "set_presence agent=") {
		t.Error("empty communication should NOT produce MCP instructions")
	}
}

func TestBuildTaskPrompt_InlinesConstitutionBeforeTaskBlock(t *testing.T) {
	task := &domain.Task{ID: 5, Title: "Implement caching", Priority: 3}
	const inline = "== Constitution ==\nRead these files in order before working. If two files conflict,\nthe earlier file wins.\n\n1. ~/.config/stringwork/constitution/engineering.md\n\n--- ~/.config/stringwork/constitution/engineering.md ---\nAlways ship tests.\n"

	prompt := buildTaskPrompt(task, nil, "codex-task-5", "/workspace", "cursor", "mcp", inline)

	idxConstitution := strings.Index(prompt, "== Constitution ==")
	idxTaskHeader := strings.Index(prompt, "--- YOUR ASSIGNED TASK")
	if idxConstitution < 0 {
		t.Fatalf("constitution header missing in prompt:\n%s", prompt)
	}
	if idxTaskHeader < 0 {
		t.Fatalf("task header missing in prompt:\n%s", prompt)
	}
	if idxConstitution >= idxTaskHeader {
		t.Errorf("constitution must precede task block (constitution=%d, task=%d)", idxConstitution, idxTaskHeader)
	}
	if !strings.Contains(prompt, "Always ship tests.") {
		t.Errorf("inlined file body should be present, got:\n%s", prompt)
	}
}

func TestBuildTaskPrompt_NoConstitution_NoExtraHeader(t *testing.T) {
	task := &domain.Task{ID: 5, Title: "Implement caching", Priority: 3}
	prompt := buildTaskPrompt(task, nil, "codex-task-5", "/workspace", "cursor", "mcp", "")
	if strings.Contains(prompt, "== Constitution ==") {
		t.Errorf("empty constitution should produce no header, got:\n%s", prompt)
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

// TestIsSpawnQueued_DetectsPendingQueue confirms the watchdog-side double-spawn
// guard sees tasks already enqueued via enqueueSpawn (capacity / backoff path).
func TestIsSpawnQueued_DetectsPendingQueue(t *testing.T) {
	wm := &WorkerManager{
		pendingSpawns:  make(map[string][]pendingSpawn),
		processRuntime: make(map[string]*workerRuntime),
	}
	wm.enqueueSpawn("claude-code", 7)

	if !wm.IsSpawnQueued(7) {
		t.Error("IsSpawnQueued(7) should be true after enqueueSpawn")
	}
	if wm.IsSpawnQueued(8) {
		t.Error("IsSpawnQueued(8) should be false (not enqueued)")
	}
}

// TestIsSpawnQueued_DetectsRunningTaskBoundChild — the spawnTaskWorker path
// registers the worker as "<type>-task-<id>" in processRuntime. The sweep
// must treat that as already-spawned so it doesn't double up.
func TestIsSpawnQueued_DetectsRunningTaskBoundChild(t *testing.T) {
	wm := &WorkerManager{
		pendingSpawns:  make(map[string][]pendingSpawn),
		processRuntime: make(map[string]*workerRuntime),
	}
	wm.processRuntime["claude-code-task-42"] = &workerRuntime{}

	if !wm.IsSpawnQueued(42) {
		t.Error("IsSpawnQueued(42) should be true when claude-code-task-42 is running")
	}
}

// TestIsSpawnQueued_IgnoresUnrelatedRuntime — a running pool worker without
// the "-task-<id>" suffix is NOT a spawn signal for any specific task.
// Otherwise IsSpawnQueued would suppress legitimate sweeps just because
// some other worker happens to be alive.
func TestIsSpawnQueued_IgnoresUnrelatedRuntime(t *testing.T) {
	wm := &WorkerManager{
		pendingSpawns:  make(map[string][]pendingSpawn),
		processRuntime: make(map[string]*workerRuntime),
	}
	wm.processRuntime["claude-code-1"] = &workerRuntime{}       // pool worker, no task suffix
	wm.processRuntime["claude-code-task-99"] = &workerRuntime{} // task-bound for #99
	wm.processRuntime["codex-task-100"] = &workerRuntime{}      // unrelated type+id

	if wm.IsSpawnQueued(42) {
		t.Error("IsSpawnQueued(42) should be false — no -task-42 suffix in runtime or queue")
	}
	if !wm.IsSpawnQueued(99) {
		t.Error("IsSpawnQueued(99) should be true (claude-code-task-99 running)")
	}
	if !wm.IsSpawnQueued(100) {
		t.Error("IsSpawnQueued(100) should be true (codex-task-100 running)")
	}
}

// TestIsSpawnQueued_NonPositiveTaskID is a safety guard so callers passing
// an unset / sentinel ID can't accidentally claim "queued".
func TestIsSpawnQueued_NonPositiveTaskID(t *testing.T) {
	wm := &WorkerManager{
		pendingSpawns:  make(map[string][]pendingSpawn),
		processRuntime: make(map[string]*workerRuntime),
	}
	if wm.IsSpawnQueued(0) {
		t.Error("IsSpawnQueued(0) must be false")
	}
	if wm.IsSpawnQueued(-1) {
		t.Error("IsSpawnQueued(-1) must be false")
	}
}

// newRecoveryTestWorkerManager builds a WorkerManager scaffolded for
// recoverPendingSpawnsOnStartup tests. State has no Presence, so
// SpawnForTask → spawnTaskWorker bails on "no workspace" — we never
// actually exec anything, but the eligibility logic still runs end to end.
func newRecoveryTestWorkerManager(t *testing.T, state *domain.CollabState, configs []WorkerSpawnConfig) *WorkerManager {
	t.Helper()
	return &WorkerManager{
		configs:             configs,
		getAgent:            func() string { return "cursor" },
		stateLoader:         func() (*domain.CollabState, error) { return state, nil },
		stateMutator:        func(fn func(*domain.CollabState) error) error { return fn(state) },
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
}

// TestRecoverPendingSpawnsOnStartup_DrivesOrphanTask covers the happy path
// for Fix D.1: a pending task with a concrete AssignedTo and no live
// owner should result in one spawn being driven.
func TestRecoverPendingSpawnsOnStartup_DrivesOrphanTask(t *testing.T) {
	state := &domain.CollabState{
		DriverID: "cursor",
		Tasks: []domain.Task{
			{ID: 5, Title: "Orphan", Status: "pending", AssignedTo: "claude-code", UpdatedAt: time.Now().Add(-1 * time.Hour)},
		},
	}
	EnsureStateMaps(state)

	wm := newRecoveryTestWorkerManager(t, state, []WorkerSpawnConfig{
		{InstanceID: "claude-code-1", AgentType: "claude-code", Command: []string{"echo", "test"}},
	})

	if got := wm.recoverPendingSpawnsOnStartup(); got != 1 {
		t.Errorf("expected 1 driven spawn, got %d", got)
	}
}

// TestRecoverPendingSpawnsOnStartup_SkipsLiveOwner — when an existing
// AgentInstance owns the task via CurrentTasks AND has a non-zero
// LastHeartbeat AND is not offline, the task is "covered" and should
// not trigger a duplicate spawn.
func TestRecoverPendingSpawnsOnStartup_SkipsLiveOwner(t *testing.T) {
	now := time.Now()
	state := &domain.CollabState{
		DriverID: "cursor",
		Tasks: []domain.Task{
			{ID: 5, Title: "Owned", Status: "pending", AssignedTo: "claude-code", UpdatedAt: now.Add(-1 * time.Hour)},
		},
		AgentInstances: map[string]*domain.AgentInstance{
			"claude-code-1": {
				InstanceID: "claude-code-1", AgentType: "claude-code",
				Role: domain.RoleWorker, Status: "busy",
				LastHeartbeat: now,
				CurrentTasks:  []int{5},
			},
		},
	}
	EnsureStateMaps(state)

	wm := newRecoveryTestWorkerManager(t, state, []WorkerSpawnConfig{
		{InstanceID: "claude-code-1", AgentType: "claude-code", Command: []string{"echo", "test"}},
	})

	if got := wm.recoverPendingSpawnsOnStartup(); got != 0 {
		t.Errorf("expected no spawn when live owner already holds task, got %d", got)
	}
}

// TestRecoverPendingSpawnsOnStartup_OfflineOwnerStillTriggers — owner exists
// but is marked offline. The task IS effectively orphaned and we should
// drive a spawn.
func TestRecoverPendingSpawnsOnStartup_OfflineOwnerStillTriggers(t *testing.T) {
	state := &domain.CollabState{
		DriverID: "cursor",
		Tasks: []domain.Task{
			{ID: 5, Title: "Offline owner", Status: "pending", AssignedTo: "claude-code", UpdatedAt: time.Now().Add(-1 * time.Hour)},
		},
		AgentInstances: map[string]*domain.AgentInstance{
			"claude-code-1": {
				InstanceID: "claude-code-1", AgentType: "claude-code",
				Role: domain.RoleWorker, Status: "offline",
				LastHeartbeat: time.Now().Add(-30 * time.Minute),
				CurrentTasks:  []int{5},
			},
		},
	}
	EnsureStateMaps(state)

	wm := newRecoveryTestWorkerManager(t, state, []WorkerSpawnConfig{
		{InstanceID: "claude-code-1", AgentType: "claude-code", Command: []string{"echo", "test"}},
	})

	if got := wm.recoverPendingSpawnsOnStartup(); got != 1 {
		t.Errorf("offline owner should not block recovery, expected 1 driven, got %d", got)
	}
}

// TestRecoverPendingSpawnsOnStartup_SkipsAnyAndUnassigned — without a
// concrete AssignedTo, recovery has no type to spawn for. The orchestrator
// fallback (Fix A) is responsible for setting a concrete type at create
// time; recovery never invents one.
func TestRecoverPendingSpawnsOnStartup_SkipsAnyAndUnassigned(t *testing.T) {
	state := &domain.CollabState{
		DriverID: "cursor",
		Tasks: []domain.Task{
			{ID: 1, Title: "Empty", Status: "pending", AssignedTo: "", UpdatedAt: time.Now()},
			{ID: 2, Title: "Any", Status: "pending", AssignedTo: "any", UpdatedAt: time.Now()},
		},
	}
	EnsureStateMaps(state)

	wm := newRecoveryTestWorkerManager(t, state, []WorkerSpawnConfig{
		{InstanceID: "claude-code-1", AgentType: "claude-code", Command: []string{"echo", "test"}},
	})

	if got := wm.recoverPendingSpawnsOnStartup(); got != 0 {
		t.Errorf("expected 0 driven for empty/any AssignedTo, got %d", got)
	}
}

// TestRecoverPendingSpawnsOnStartup_SkipsAlreadyQueued — IsSpawnQueued
// should suppress the recovery (e.g. Check() ran first and queued the
// task, or a task-bound child is mid-spawn).
func TestRecoverPendingSpawnsOnStartup_SkipsAlreadyQueued(t *testing.T) {
	state := &domain.CollabState{
		DriverID: "cursor",
		Tasks: []domain.Task{
			{ID: 7, Title: "Queued", Status: "pending", AssignedTo: "claude-code", UpdatedAt: time.Now()},
		},
	}
	EnsureStateMaps(state)

	wm := newRecoveryTestWorkerManager(t, state, []WorkerSpawnConfig{
		{InstanceID: "claude-code-1", AgentType: "claude-code", Command: []string{"echo", "test"}},
	})
	wm.enqueueSpawn("claude-code", 7) // pre-queued, recovery should defer to drainQueue

	if got := wm.recoverPendingSpawnsOnStartup(); got != 0 {
		t.Errorf("expected 0 driven when already queued, got %d", got)
	}
}

// TestLogSpawnSkip_RateLimitedPerReason confirms that the spawn-skip log
// fires once per (instanceID, reason) pair within spawnSkipLogWindow, and
// that distinct reasons or distinct instances are NOT collapsed.
func TestLogSpawnSkip_RateLimitedPerReason(t *testing.T) {
	var buf strings.Builder
	wm := &WorkerManager{
		logger:          log.New(&buf, "", 0),
		spawnSkipLogged: make(map[string]time.Time),
	}

	wm.logSpawnSkip("claude-code-1", "already-running")
	wm.logSpawnSkip("claude-code-1", "already-running") // suppressed (same key, fresh)
	wm.logSpawnSkip("claude-code-1", "in-cooldown")     // distinct reason → emits
	wm.logSpawnSkip("claude-code-2", "already-running") // distinct instance → emits

	out := buf.String()
	count := strings.Count(out, "skipping spawn")
	if count != 3 {
		t.Errorf("expected 3 distinct skip lines (rate-limit collapses dup), got %d:\n%s", count, out)
	}
	if !strings.Contains(out, "claude-code-1 — already-running") {
		t.Error("missing initial 'already-running' line for claude-code-1")
	}
	if !strings.Contains(out, "claude-code-1 — in-cooldown") {
		t.Error("missing 'in-cooldown' line for claude-code-1 (different reason)")
	}
	if !strings.Contains(out, "claude-code-2 — already-running") {
		t.Error("missing 'already-running' line for claude-code-2 (different instance)")
	}
}

// TestLogSpawnSkip_ReemitsAfterWindow simulates the window expiring by
// rewinding the recorded last-logged time and verifies the next call logs
// again. We can't easily wait a full minute in a test, so we mutate the map
// directly — the mechanism under test is the time comparison.
func TestLogSpawnSkip_ReemitsAfterWindow(t *testing.T) {
	var buf strings.Builder
	wm := &WorkerManager{
		logger:          log.New(&buf, "", 0),
		spawnSkipLogged: make(map[string]time.Time),
	}

	wm.logSpawnSkip("claude-code-1", "in-backoff")
	wm.mu.Lock()
	wm.spawnSkipLogged["claude-code-1|in-backoff"] = time.Now().Add(-2 * spawnSkipLogWindow)
	wm.mu.Unlock()
	wm.logSpawnSkip("claude-code-1", "in-backoff") // window elapsed → re-emit

	if c := strings.Count(buf.String(), "in-backoff"); c != 2 {
		t.Errorf("expected re-emit after window expired, got %d 'in-backoff' lines:\n%s", c, buf.String())
	}
}

// TestLogSpawnSkip_NilLoggerSafe — defensive check that the helper doesn't
// panic when no logger has been wired. This matters for the zero-value
// WorkerManager used in some scaffolding paths.
func TestLogSpawnSkip_NilLoggerSafe(t *testing.T) {
	wm := &WorkerManager{}
	wm.logSpawnSkip("anything", "reason")
}

// TestRecoverPendingSpawnsOnStartup_OnlyPending — non-pending statuses
// (in_progress, completed, blocked) must be ignored. Recovery is exclusively
// for resurrecting work that hasn't started yet.
func TestRecoverPendingSpawnsOnStartup_OnlyPending(t *testing.T) {
	state := &domain.CollabState{
		DriverID: "cursor",
		Tasks: []domain.Task{
			{ID: 1, Title: "In progress", Status: "in_progress", AssignedTo: "claude-code", UpdatedAt: time.Now()},
			{ID: 2, Title: "Completed", Status: "completed", AssignedTo: "claude-code", UpdatedAt: time.Now()},
			{ID: 3, Title: "Blocked", Status: "blocked", AssignedTo: "claude-code", UpdatedAt: time.Now()},
		},
	}
	EnsureStateMaps(state)

	wm := newRecoveryTestWorkerManager(t, state, []WorkerSpawnConfig{
		{InstanceID: "claude-code-1", AgentType: "claude-code", Command: []string{"echo", "test"}},
	})

	if got := wm.recoverPendingSpawnsOnStartup(); got != 0 {
		t.Errorf("expected 0 driven for non-pending tasks, got %d", got)
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

// captureCheckAcks builds a stateMutator that records the InstanceIDs that
// Check() decides to spawn (by parsing the "⚡ **<id>** is coming online" ack
// message that sendAck writes synchronously before launching the spawn goroutine).
//
// Production Check() can fan out and call this closure from multiple goroutines
// concurrently (one per pool instance — see Check.gowrap1 / spawn / sendFailureAck
// at worker_manager.go:935 onward). Without serialisation the appends to
// state.Messages and *ackTargets race under -race. The closure-local mutex
// makes the helper safe for the multi-config tests (e.g.
// TestCheck_TaskBoundChildDoesNotBlockPool) without changing call sites.
func captureCheckAcks(state *domain.CollabState, ackTargets *[]string) func(func(*domain.CollabState) error) error {
	var mu sync.Mutex
	return func(fn func(*domain.CollabState) error) error {
		mu.Lock()
		defer mu.Unlock()
		before := len(state.Messages)
		if err := fn(state); err != nil {
			return err
		}
		for _, msg := range state.Messages[before:] {
			if msg.From != "system" || !strings.Contains(msg.Content, "is coming online") {
				continue
			}
			start := strings.Index(msg.Content, "**")
			if start < 0 {
				continue
			}
			rest := msg.Content[start+2:]
			end := strings.Index(rest, "**")
			if end <= 0 {
				continue
			}
			*ackTargets = append(*ackTargets, rest[:end])
		}
		return nil
	}
}

// newCheckTestWorkerManager builds a WorkerManager scaffolded for Check() tests:
// initialised maps, in-process MCP (no URL), and a stateMutator that records ack targets.
func newCheckTestWorkerManager(t *testing.T, state *domain.CollabState, configs []WorkerSpawnConfig, ackTargets *[]string) *WorkerManager {
	t.Helper()
	return &WorkerManager{
		configs:             configs,
		getAgent:            func() string { return "cursor" },
		stateLoader:         func() (*domain.CollabState, error) { return state, nil },
		stateMutator:        captureCheckAcks(state, ackTargets),
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
}

// markRunning simulates a live worker process for the given instanceID without
// actually exec'ing anything.
func markRunning(wm *WorkerManager, instanceID string) {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	wm.processRuntime[instanceID] = &workerRuntime{
		info: &ProcessInfo{InstanceID: instanceID, StartedAt: time.Now(), LastOutputAt: time.Now()},
		tail: newTailBuffer(16384),
	}
}

// cleanupLockfiles removes per-instance lockfiles from os.TempDir() so a previous
// failed test run doesn't poison this one.
func cleanupLockfiles(t *testing.T, wm *WorkerManager, instanceIDs ...string) {
	t.Helper()
	for _, id := range instanceIDs {
		_ = os.Remove(wm.lockfilePath(id))
	}
}

// TestCheck_RespawnsSiblingPoolInstance covers Fix B: when one instance of a
// pool (claude-code-1) is running, a sibling instance (claude-code-2) must
// still be eligible for spawn for unread messages addressed to the agent type.
// The pre-fix call chain `isWorkerProcessRunning(c.AgentType)` prefix-matched
// claude-code-1 and silently blocked claude-code-2.
func TestCheck_RespawnsSiblingPoolInstance(t *testing.T) {
	workspace := t.TempDir()
	state := &domain.CollabState{
		Messages: []domain.Message{
			{ID: 1, From: "cursor", To: "claude-code", Content: "hi pool", Timestamp: time.Now()},
		},
		NextMsgID: 2,
	}
	EnsureStateMaps(state)
	state.Presence["cursor"] = &domain.Presence{Workspace: workspace}

	var acks []string
	wm := newCheckTestWorkerManager(t, state, []WorkerSpawnConfig{
		{InstanceID: "claude-code-1", AgentType: "claude-code", Command: []string{"echo", "test"}},
		{InstanceID: "claude-code-2", AgentType: "claude-code", Command: []string{"echo", "test"}},
	}, &acks)

	markRunning(wm, "claude-code-1")
	cleanupLockfiles(t, wm, "claude-code-1", "claude-code-2")
	t.Cleanup(func() { cleanupLockfiles(t, wm, "claude-code-1", "claude-code-2") })

	wm.Check()

	if len(acks) != 1 {
		t.Fatalf("expected exactly one spawn ack, got %d: %v", len(acks), acks)
	}
	if acks[0] != "claude-code-2" {
		t.Errorf("expected ack for claude-code-2 (sibling instance), got %q", acks[0])
	}
}

// TestCheck_DoesNotRespawnRunningInstance covers the basic invariant that the
// instance-level running check still suppresses double-spawning.
func TestCheck_DoesNotRespawnRunningInstance(t *testing.T) {
	workspace := t.TempDir()
	state := &domain.CollabState{
		Messages: []domain.Message{
			{ID: 1, From: "cursor", To: "claude-code-1", Content: "hi", Timestamp: time.Now()},
		},
		NextMsgID: 2,
	}
	EnsureStateMaps(state)
	state.Presence["cursor"] = &domain.Presence{Workspace: workspace}

	var acks []string
	wm := newCheckTestWorkerManager(t, state, []WorkerSpawnConfig{
		{InstanceID: "claude-code-1", AgentType: "claude-code", Command: []string{"echo", "test"}},
	}, &acks)

	markRunning(wm, "claude-code-1")
	cleanupLockfiles(t, wm, "claude-code-1")
	t.Cleanup(func() { cleanupLockfiles(t, wm, "claude-code-1") })

	wm.Check()

	if len(acks) != 0 {
		t.Errorf("expected no spawn ack when target instance is already running, got %v", acks)
	}
}

// TestMarkInstanceSpawning_FreshlySpawnedInstanceLooksAlive covers Fix C: a
// just-spawned worker must immediately be Status="idle" with current
// LastHeartbeat AND LastSpawnedAt, even before the worker process emits its
// first real heartbeat. The orchestrator's isAssignable filter (which rejects
// "offline") and the watchdog's staleness check both depend on this.
func TestMarkInstanceSpawning_FreshlySpawnedInstanceLooksAlive(t *testing.T) {
	state := &domain.CollabState{}
	EnsureStateMaps(state)
	state.AgentInstances["claude-code-1"] = &domain.AgentInstance{
		InstanceID:    "claude-code-1",
		AgentType:     "claude-code",
		Role:          domain.RoleWorker,
		MaxTasks:      1,
		Status:        "offline",
		LastHeartbeat: time.Now().Add(-2 * time.Hour),
	}

	wm := &WorkerManager{
		logger:       testLogger(t),
		stateMutator: func(fn func(*domain.CollabState) error) error { return fn(state) },
	}

	before := time.Now().Add(-time.Second)
	wm.MarkInstanceSpawning("claude-code-1", "claude-code")

	inst := state.AgentInstances["claude-code-1"]
	if inst == nil {
		t.Fatal("expected AgentInstance to exist after MarkInstanceSpawning")
	}
	if inst.Status != "idle" {
		t.Errorf("expected Status=idle after spawn, got %q", inst.Status)
	}
	if !inst.LastHeartbeat.After(before) {
		t.Errorf("expected LastHeartbeat to be refreshed to ~now, got %v", inst.LastHeartbeat)
	}
	if !inst.LastSpawnedAt.After(before) {
		t.Errorf("expected LastSpawnedAt to be set to ~now, got %v", inst.LastSpawnedAt)
	}
}

// TestMarkInstanceSpawning_CreatesMissingRow ensures that drift between
// configured workers and persisted AgentInstance rows doesn't prevent spawn:
// the row is created on the fly so the orchestrator can see and assign it.
func TestMarkInstanceSpawning_CreatesMissingRow(t *testing.T) {
	state := &domain.CollabState{}
	EnsureStateMaps(state)

	wm := &WorkerManager{
		logger:       testLogger(t),
		stateMutator: func(fn func(*domain.CollabState) error) error { return fn(state) },
	}

	wm.MarkInstanceSpawning("codex-2", "codex")

	inst := state.AgentInstances["codex-2"]
	if inst == nil {
		t.Fatal("expected MarkInstanceSpawning to create missing row")
	}
	if inst.AgentType != "codex" {
		t.Errorf("expected AgentType=codex, got %q", inst.AgentType)
	}
	if inst.Role != domain.RoleWorker {
		t.Errorf("expected Role=worker, got %q", inst.Role)
	}
	if inst.Status != "idle" {
		t.Errorf("expected Status=idle, got %q", inst.Status)
	}
	if inst.LastSpawnedAt.IsZero() {
		t.Error("expected LastSpawnedAt to be set on freshly-created row")
	}
}

// TestMarkInstanceSpawning_DoesNotDowngradeBusyStatus protects against
// clobbering a busy worker's Status during a respawn (e.g., when a worker
// crashes and is restarted while still owning a task in CurrentTasks).
func TestMarkInstanceSpawning_DoesNotDowngradeBusyStatus(t *testing.T) {
	state := &domain.CollabState{}
	EnsureStateMaps(state)
	state.AgentInstances["claude-code-1"] = &domain.AgentInstance{
		InstanceID:   "claude-code-1",
		AgentType:    "claude-code",
		Role:         domain.RoleWorker,
		MaxTasks:     1,
		Status:       "busy",
		CurrentTasks: []int{42},
	}

	wm := &WorkerManager{
		logger:       testLogger(t),
		stateMutator: func(fn func(*domain.CollabState) error) error { return fn(state) },
	}

	wm.MarkInstanceSpawning("claude-code-1", "claude-code")

	inst := state.AgentInstances["claude-code-1"]
	if inst.Status != "busy" {
		t.Errorf("expected Status to remain busy during respawn, got %q", inst.Status)
	}
	if len(inst.CurrentTasks) != 1 || inst.CurrentTasks[0] != 42 {
		t.Errorf("expected CurrentTasks preserved, got %v", inst.CurrentTasks)
	}
	if inst.LastSpawnedAt.IsZero() {
		t.Error("expected LastSpawnedAt to be set even for busy worker")
	}
}

// TestMarkInstanceSpawning_NilStateMutator is a robustness check: scenarios
// without persistence (some tests, dry-run paths) must not panic.
func TestMarkInstanceSpawning_NilStateMutator(t *testing.T) {
	wm := &WorkerManager{logger: testLogger(t)}
	wm.MarkInstanceSpawning("anything", "anything")
}

// TestCheck_BumpsAgentInstanceOnSpawn is the integration-style test for Fix C:
// when Check() decides to spawn, the AgentInstance row is updated so the
// orchestrator and watchdog see the new state immediately.
func TestCheck_BumpsAgentInstanceOnSpawn(t *testing.T) {
	workspace := t.TempDir()
	state := &domain.CollabState{
		Messages: []domain.Message{
			{ID: 1, From: "cursor", To: "claude-code-1", Content: "hi", Timestamp: time.Now()},
		},
		NextMsgID: 2,
	}
	EnsureStateMaps(state)
	state.Presence["cursor"] = &domain.Presence{Workspace: workspace}
	state.AgentInstances["claude-code-1"] = &domain.AgentInstance{
		InstanceID:    "claude-code-1",
		AgentType:     "claude-code",
		Role:          domain.RoleWorker,
		MaxTasks:      1,
		Status:        "offline",
		LastHeartbeat: time.Now().Add(-2 * time.Hour),
	}

	mutator := func(fn func(*domain.CollabState) error) error { return fn(state) }
	wm := &WorkerManager{
		configs: []WorkerSpawnConfig{
			{InstanceID: "claude-code-1", AgentType: "claude-code", Command: []string{"echo", "test"}},
		},
		getAgent:            func() string { return "cursor" },
		stateLoader:         func() (*domain.CollabState, error) { return state, nil },
		stateMutator:        mutator,
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
	cleanupLockfiles(t, wm, "claude-code-1")
	t.Cleanup(func() { cleanupLockfiles(t, wm, "claude-code-1") })

	before := time.Now().Add(-time.Second)
	wm.Check()

	inst := state.AgentInstances["claude-code-1"]
	if inst == nil {
		t.Fatal("expected AgentInstance for claude-code-1")
	}
	if inst.Status != "idle" {
		t.Errorf("expected Status=idle after Check() spawn, got %q", inst.Status)
	}
	if !inst.LastSpawnedAt.After(before) {
		t.Errorf("expected LastSpawnedAt bumped by Check(), got %v", inst.LastSpawnedAt)
	}
}

// TestCheck_TaskBoundChildDoesNotBlockPool covers the regression that Fix B
// repairs end-to-end: even if a task-bound child like claude-code-task-5 is
// running, the regular pool instances must still be spawnable. The pre-fix
// `isWorkerProcessRunning("claude-code")` prefix-matched the task-bound child
// and blocked all pool spawns.
func TestCheck_TaskBoundChildDoesNotBlockPool(t *testing.T) {
	workspace := t.TempDir()
	state := &domain.CollabState{
		Messages: []domain.Message{
			{ID: 1, From: "cursor", To: "claude-code", Content: "hi pool", Timestamp: time.Now()},
		},
		NextMsgID: 2,
	}
	EnsureStateMaps(state)
	state.Presence["cursor"] = &domain.Presence{Workspace: workspace}

	var acks []string
	wm := newCheckTestWorkerManager(t, state, []WorkerSpawnConfig{
		{InstanceID: "claude-code-1", AgentType: "claude-code", Command: []string{"echo", "test"}},
		{InstanceID: "claude-code-2", AgentType: "claude-code", Command: []string{"echo", "test"}},
	}, &acks)

	markRunning(wm, "claude-code-task-5")
	cleanupLockfiles(t, wm, "claude-code-1", "claude-code-2")
	t.Cleanup(func() { cleanupLockfiles(t, wm, "claude-code-1", "claude-code-2") })

	wm.Check()

	if len(acks) != 2 {
		t.Fatalf("expected acks for both pool instances, got %d: %v", len(acks), acks)
	}
	gotIDs := map[string]bool{acks[0]: true, acks[1]: true}
	if !gotIDs["claude-code-1"] || !gotIDs["claude-code-2"] {
		t.Errorf("expected acks for claude-code-1 AND claude-code-2, got %v", acks)
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

func TestWorkerManager_DriverMethod(t *testing.T) {
	tests := []struct {
		name     string
		driverID string
		want     string
	}{
		{"configured claude-code", "claude-code", "claude-code"},
		{"configured cursor", "cursor", "cursor"},
		{"empty falls back to cursor", "", "cursor"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wm := &WorkerManager{driverID: tc.driverID}
			if got := wm.driver(); got != tc.want {
				t.Errorf("driver() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildTaskPrompt_ClaudeCodeDriver(t *testing.T) {
	task := &domain.Task{
		ID:       99,
		Title:    "Test with claude-code driver",
		Priority: 3,
	}
	prompt := buildTaskPrompt(task, nil, "codex-task-99", "/workspace", "claude-code", "mcp", "")

	if !strings.Contains(prompt, "to='claude-code'") {
		t.Error("prompt should contain send_message to claude-code driver")
	}
	if strings.Contains(prompt, "to='cursor'") {
		t.Error("prompt should not reference cursor when driver is claude-code")
	}
}

// --- Session resume injection tests ---

func assertArgs(t *testing.T, label string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: len = %d, want %d\n  got:  %v\n  want: %v", label, len(got), len(want), got, want)
		return
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("%s: [%d] = %q, want %q", label, i, got[i], want[i])
		}
	}
}

func TestInjectSessionResume_Claude(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		sessionID string
		want      []string
	}{
		{
			name:      "inserts --resume before -p",
			args:      []string{"claude", "-p", "You are a worker"},
			sessionID: "abc123",
			want:      []string{"claude", "--resume", "abc123", "-p", "You are a worker"},
		},
		{
			name:      "inserts --resume with full path",
			args:      []string{"/opt/homebrew/bin/claude", "--dangerously-skip-permissions", "-p", "prompt"},
			sessionID: "sess-456",
			want:      []string{"/opt/homebrew/bin/claude", "--resume", "sess-456", "--dangerously-skip-permissions", "-p", "prompt"},
		},
		{
			name:      "works with -w flag already present",
			args:      []string{"claude", "-w", "wt-1", "-p", "prompt"},
			sessionID: "s1",
			want:      []string{"claude", "--resume", "s1", "-w", "wt-1", "-p", "prompt"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := injectSessionResume(tc.args, tc.sessionID)
			assertArgs(t, "injectSessionResume(claude)", got, tc.want)
		})
	}
}

func TestInjectSessionResume_Codex(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		sessionID string
		want      []string
	}{
		{
			name:      "inserts --session after exec",
			args:      []string{"codex", "exec", "--sandbox", "danger-full-access", "prompt"},
			sessionID: "7f7116f0",
			want:      []string{"codex", "exec", "--session", "7f7116f0", "--sandbox", "danger-full-access", "prompt"},
		},
		{
			name:      "fallback when no exec subcommand",
			args:      []string{"codex", "--some-flag", "prompt"},
			sessionID: "sess-x",
			want:      []string{"codex", "--session", "sess-x", "--some-flag", "prompt"},
		},
		{
			name:      "with full path",
			args:      []string{"/usr/local/bin/codex", "exec", "do something"},
			sessionID: "abc",
			want:      []string{"/usr/local/bin/codex", "exec", "--session", "abc", "do something"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := injectSessionResume(tc.args, tc.sessionID)
			assertArgs(t, "injectSessionResume(codex)", got, tc.want)
		})
	}
}

func TestInjectSessionResume_Gemini(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		sessionID string
		want      []string
	}{
		{
			name:      "inserts --resume before --prompt",
			args:      []string{"gemini", "--yolo", "--prompt", "You are a worker"},
			sessionID: "gem-session-1",
			want:      []string{"gemini", "--resume", "gem-session-1", "--yolo", "--prompt", "You are a worker"},
		},
		{
			name:      "with full path",
			args:      []string{"/usr/local/bin/gemini", "--prompt", "do work"},
			sessionID: "g42",
			want:      []string{"/usr/local/bin/gemini", "--resume", "g42", "--prompt", "do work"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := injectSessionResume(tc.args, tc.sessionID)
			assertArgs(t, "injectSessionResume(gemini)", got, tc.want)
		})
	}
}

func TestInjectSessionResume_Empty(t *testing.T) {
	args := []string{"claude", "-p", "prompt"}

	got := injectSessionResume(args, "")
	assertArgs(t, "empty sessionID", got, args)

	got2 := injectSessionResume(nil, "abc")
	if len(got2) != 0 {
		t.Errorf("expected nil/empty args unchanged, got %v", got2)
	}
}

func TestInjectSessionResume_UnknownCLI(t *testing.T) {
	args := []string{"python3", "script.py", "--arg"}
	got := injectSessionResume(args, "session-1")
	assertArgs(t, "unknown CLI", got, args)
}

func TestIsTaskBoundInstance(t *testing.T) {
	tests := []struct {
		instanceID string
		want       bool
	}{
		{"claude-code-task-42", true},
		{"gemini-task-7", true},
		{"codex-task-100", true},
		{"claude-code", false},
		{"claude-code-1", false},
		{"codex", false},
	}
	for _, tc := range tests {
		t.Run(tc.instanceID, func(t *testing.T) {
			if got := isTaskBoundInstance(tc.instanceID); got != tc.want {
				t.Errorf("isTaskBoundInstance(%q) = %v, want %v", tc.instanceID, got, tc.want)
			}
		})
	}
}

func TestSetWorkerSessionID(t *testing.T) {
	wm := &WorkerManager{
		lastSessionID: make(map[string]string),
	}

	wm.SetWorkerSessionID("claude-code", "session-abc")
	if got := wm.lastSessionID["claude-code"]; got != "session-abc" {
		t.Errorf("expected session-abc, got %q", got)
	}

	// Empty values should not be stored
	wm.SetWorkerSessionID("", "session-x")
	wm.SetWorkerSessionID("codex", "")
	if _, exists := wm.lastSessionID["codex"]; exists {
		t.Error("empty session ID should not be stored")
	}
	if _, exists := wm.lastSessionID[""]; exists {
		t.Error("empty instance ID should not create entry")
	}
}

func TestLoadSessionIDsFromState(t *testing.T) {
	wm := &WorkerManager{
		lastSessionID: make(map[string]string),
	}

	state := domain.NewCollabState()
	state.AgentInstances["claude-code"] = &domain.AgentInstance{
		InstanceID: "claude-code",
		AgentType:  "claude-code",
		SessionID:  "state-session-1",
	}
	state.AgentInstances["codex"] = &domain.AgentInstance{
		InstanceID: "codex",
		AgentType:  "codex",
		SessionID:  "state-session-2",
	}
	state.AgentInstances["gemini"] = &domain.AgentInstance{
		InstanceID: "gemini",
		AgentType:  "gemini",
	}

	wm.loadSessionIDsFromState(state)

	if got := wm.lastSessionID["claude-code"]; got != "state-session-1" {
		t.Errorf("expected state-session-1, got %q", got)
	}
	if got := wm.lastSessionID["codex"]; got != "state-session-2" {
		t.Errorf("expected state-session-2, got %q", got)
	}
	if _, exists := wm.lastSessionID["gemini"]; exists {
		t.Error("empty SessionID in state should not create entry")
	}
}

func TestLoadSessionIDsFromState_DoesNotOverwrite(t *testing.T) {
	wm := &WorkerManager{
		lastSessionID: map[string]string{
			"claude-code": "heartbeat-session",
		},
	}

	state := domain.NewCollabState()
	state.AgentInstances["claude-code"] = &domain.AgentInstance{
		InstanceID: "claude-code",
		AgentType:  "claude-code",
		SessionID:  "old-state-session",
	}

	wm.loadSessionIDsFromState(state)

	if got := wm.lastSessionID["claude-code"]; got != "heartbeat-session" {
		t.Errorf("loadSessionIDsFromState should not overwrite existing entry; got %q, want %q", got, "heartbeat-session")
	}
}

func TestRestartWorkers_PreservesSessionID(t *testing.T) {
	state := domain.NewCollabState()
	state.AgentInstances["claude-code"] = &domain.AgentInstance{
		InstanceID: "claude-code",
		AgentType:  "claude-code",
		Role:       domain.RoleWorker,
		Status:     "idle",
		SessionID:  "persisted-session",
	}

	wm := &WorkerManager{
		configs:             []WorkerSpawnConfig{},
		driverID:            "cursor",
		getAgent:            func() string { return "cursor" },
		stateLoader:         func() (*domain.CollabState, error) { return state, nil },
		fallbackDir:         "/tmp",
		logger:              log.New(os.Stderr, "[test] ", log.LstdFlags),
		lastSpawn:           make(map[string]time.Time),
		runningWorkers:      make(map[string]context.CancelFunc),
		mcpRegistered:       make(map[string]bool),
		processRuntime:      make(map[string]*workerRuntime),
		consecutiveFailures: make(map[string]int),
		lastFailure:         make(map[string]time.Time),
		backoffUntil:        make(map[string]time.Time),
		pendingSpawns:       make(map[string][]pendingSpawn),
		lastSessionID: map[string]string{
			"claude-code": "my-session-123",
		},
	}

	killed := wm.RestartWorkers()
	_ = killed

	if got := wm.lastSessionID["claude-code"]; got != "my-session-123" {
		t.Errorf("RestartWorkers should preserve lastSessionID; got %q, want %q", got, "my-session-123")
	}
}

// --- Model flag injection tests ---

func TestInjectModelFlag_Claude(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		model string
		want  []string
	}{
		{
			name:  "inserts --model after exe, before -p",
			args:  []string{"claude", "-p", "You are a worker"},
			model: "opus",
			want:  []string{"claude", "--model", "opus", "-p", "You are a worker"},
		},
		{
			name:  "works with full path",
			args:  []string{"/opt/homebrew/bin/claude", "--dangerously-skip-permissions", "-p", "prompt"},
			model: "sonnet",
			want:  []string{"/opt/homebrew/bin/claude", "--model", "sonnet", "--dangerously-skip-permissions", "-p", "prompt"},
		},
		{
			name:  "accepts full model IDs",
			args:  []string{"claude", "-p", "prompt"},
			model: "claude-opus-4-5-20250929",
			want:  []string{"claude", "--model", "claude-opus-4-5-20250929", "-p", "prompt"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := injectModelFlag(tc.args, tc.model)
			assertArgs(t, "injectModelFlag(claude)", got, tc.want)
		})
	}
}

func TestInjectModelFlag_Codex(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		model string
		want  []string
	}{
		{
			name:  "inserts --model after exec",
			args:  []string{"codex", "exec", "--sandbox", "danger-full-access", "prompt"},
			model: "gpt-5-codex",
			want:  []string{"codex", "exec", "--model", "gpt-5-codex", "--sandbox", "danger-full-access", "prompt"},
		},
		{
			name:  "fallback when no exec subcommand",
			args:  []string{"codex", "--some-flag", "prompt"},
			model: "gpt-5-codex",
			want:  []string{"codex", "--model", "gpt-5-codex", "--some-flag", "prompt"},
		},
		{
			name:  "with full path",
			args:  []string{"/usr/local/bin/codex", "exec", "do something"},
			model: "o4-mini",
			want:  []string{"/usr/local/bin/codex", "exec", "--model", "o4-mini", "do something"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := injectModelFlag(tc.args, tc.model)
			assertArgs(t, "injectModelFlag(codex)", got, tc.want)
		})
	}
}

func TestInjectModelFlag_Gemini(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		model string
		want  []string
	}{
		{
			name:  "inserts --model before --prompt",
			args:  []string{"gemini", "--yolo", "--prompt", "You are a worker"},
			model: "gemini-2.5-pro",
			want:  []string{"gemini", "--model", "gemini-2.5-pro", "--yolo", "--prompt", "You are a worker"},
		},
		{
			name:  "with full path",
			args:  []string{"/usr/local/bin/gemini", "--prompt", "do work"},
			model: "gemini-2.5-flash",
			want:  []string{"/usr/local/bin/gemini", "--model", "gemini-2.5-flash", "--prompt", "do work"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := injectModelFlag(tc.args, tc.model)
			assertArgs(t, "injectModelFlag(gemini)", got, tc.want)
		})
	}
}

func TestInjectModelFlag_Empty(t *testing.T) {
	args := []string{"claude", "-p", "prompt"}

	got := injectModelFlag(args, "")
	assertArgs(t, "empty model", got, args)

	got2 := injectModelFlag(nil, "opus")
	if len(got2) != 0 {
		t.Errorf("expected nil/empty args unchanged, got %v", got2)
	}
}

func TestInjectModelFlag_UnknownCLI(t *testing.T) {
	args := []string{"python3", "script.py", "--arg"}
	got := injectModelFlag(args, "opus")
	assertArgs(t, "unknown CLI", got, args)
}

func TestInjectModelFlag_UserOverride(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{
			name: "claude with hard-coded --model",
			args: []string{"claude", "--model", "haiku", "-p", "prompt"},
		},
		{
			name: "codex with hard-coded --model=value",
			args: []string{"codex", "exec", "--model=gpt-5-codex", "prompt"},
		},
		{
			name: "gemini with hard-coded --model",
			args: []string{"gemini", "--model", "gemini-2.5-flash", "--prompt", "work"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := injectModelFlag(tc.args, "opus")
			assertArgs(t, "user override wins", got, tc.args)
		})
	}
}

func TestResolveModel(t *testing.T) {
	tiers := policy.ExampleModelTiers

	tests := []struct {
		name          string
		task          *domain.Task
		workerType    string
		workerDefault string
		want          string
	}{
		{
			name:          "explicit task model wins",
			task:          &domain.Task{Model: "claude-opus-4-5"},
			workerType:    "claude-code",
			workerDefault: "sonnet",
			want:          "claude-opus-4-5",
		},
		{
			name:          "claude-code fast tier",
			task:          &domain.Task{ModelTier: "fast"},
			workerType:    "claude-code",
			workerDefault: "sonnet",
			want:          "haiku",
		},
		{
			name:          "codex fast tier",
			task:          &domain.Task{ModelTier: "fast"},
			workerType:    "codex",
			workerDefault: "gpt-5-codex",
			want:          "o4-mini",
		},
		{
			name:          "gemini standard tier",
			task:          &domain.Task{ModelTier: "standard"},
			workerType:    "gemini",
			workerDefault: "gemini-2.5-flash",
			want:          "gemini-2.5-pro",
		},
		{
			name:          "tier overrides worker default",
			task:          &domain.Task{ModelTier: "capable"},
			workerType:    "claude-code",
			workerDefault: "sonnet",
			want:          "opus",
		},
		{
			name:          "unknown tier falls back to worker default",
			task:          &domain.Task{ModelTier: "unknown"},
			workerType:    "claude-code",
			workerDefault: "sonnet",
			want:          "sonnet",
		},
		{
			name:          "tier missing worker type falls back",
			task:          &domain.Task{ModelTier: "capable"},
			workerType:    "gemini",
			workerDefault: "gemini-2.5-flash",
			want:          "gemini-2.5-pro",
		},
		{
			name:          "empty task uses worker default",
			task:          &domain.Task{},
			workerType:    "claude-code",
			workerDefault: "sonnet",
			want:          "sonnet",
		},
		{
			name:          "nil task uses worker default",
			task:          nil,
			workerType:    "claude-code",
			workerDefault: "sonnet",
			want:          "sonnet",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveModel(tc.task, tc.workerType, tiers, tc.workerDefault); got != tc.want {
				t.Errorf("resolveModel() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHasModelFlag(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{"no flags", []string{"claude", "-p", "prompt"}, false},
		{"explicit --model", []string{"claude", "--model", "opus", "-p", "prompt"}, true},
		{"equals form --model=opus", []string{"claude", "--model=opus", "-p", "prompt"}, true},
		{"similar but not exact", []string{"claude", "--models", "x", "-p", "prompt"}, false},
		{"empty args", nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasModelFlag(tc.args); got != tc.want {
				t.Errorf("hasModelFlag(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

// TestNewWorkerManager_PropagatesModel verifies the Model field flows from
// policy.WorkerConfig (YAML) into WorkerSpawnConfig used at spawn time.
func TestNewWorkerManager_PropagatesModel(t *testing.T) {
	orch := &policy.OrchestrationConfig{
		Driver: "cursor",
		Workers: []policy.WorkerConfig{
			{Type: "claude-code", Instances: 1, Command: []string{"claude", "-p", "x"}, Model: "opus"},
			{Type: "codex", Instances: 2, Command: []string{"codex", "exec", "x"}, Model: "gpt-5-codex"},
			{Type: "gemini", Instances: 1, Command: []string{"gemini", "--prompt", "x"}},
		},
	}
	wm := NewWorkerManager(orch, func() string { return "cursor" }, nil, nil, "/tmp", testLogger(t))

	got := map[string]string{}
	for _, c := range wm.configs {
		got[c.InstanceID] = c.Model
	}

	want := map[string]string{
		"claude-code": "opus",
		"codex-1":     "gpt-5-codex",
		"codex-2":     "gpt-5-codex",
		"gemini":      "",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d configs, want %d\n  got:  %v\n  want: %v", len(got), len(want), got, want)
	}
	for id, model := range want {
		if g := got[id]; g != model {
			t.Errorf("instance %q: Model = %q, want %q", id, g, model)
		}
	}
}

// erroringSourceForSpawn is a test-only constitution.Source that
// always returns a configured error. Used to verify resolvedConstitution
// preserves the surviving good source's content even when another
// source breaks at spawn time.
type erroringSourceForSpawn struct {
	name string
	err  error
}

func (e *erroringSourceForSpawn) Name() string { return e.name }
func (e *erroringSourceForSpawn) List(constitution.Scope) ([]constitution.File, error) {
	return nil, e.err
}

// TestResolvedConstitution_PartialFailureSurvivesAtSpawn is the
// regression test for codex's MUST_FIX #2 on the spawn-prompt side:
// when one source errors but another resolves cleanly, the spawn
// prompt must still inline the good source's full body. Discarding
// the survivors on any non-nil err — which is what the previous
// implementation did — is the regression that nullified the upstream
// constitution.Resolve fix.
func TestResolvedConstitution_PartialFailureSurvivesAtSpawn(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "alpha.md"),
		[]byte("alpha rule body"), 0o644); err != nil {
		t.Fatalf("write alpha.md: %v", err)
	}

	wm := NewWorkerManager(nil, func() string { return "cursor" }, nil, nil, "/tmp", testLogger(t))
	wm.SetConstitutionSources(func() []constitution.Source {
		return []constitution.Source{
			&constitution.DirSource{SourceName: "good", Path: dir, Include: []string{"*.md"}},
			&erroringSourceForSpawn{name: "broken", err: errors.New("synthetic source failure")},
		}
	})

	got := wm.resolvedConstitution(constitution.Scope{})
	if got == "" {
		t.Fatal("resolvedConstitution returned empty despite one source resolving cleanly")
	}
	if !strings.HasPrefix(got, "== Constitution ==") {
		t.Errorf("expected constitution preamble header, got:\n%s", got)
	}
	if !strings.Contains(got, "alpha.md") {
		t.Errorf("inline output must list the surviving source's display path, got:\n%s", got)
	}
	if !strings.Contains(got, "alpha rule body") {
		t.Errorf("inline output must include the surviving source's body, got:\n%s", got)
	}
}

// TestResolvedConstitution_NoSurvivors_ReturnsEmpty is the inverse —
// when every source errors out, the spawn prompt must NOT include a
// mostly-empty constitution header. Prevents regressions where future
// refactors render preamble headers with zero entries.
func TestResolvedConstitution_NoSurvivors_ReturnsEmpty(t *testing.T) {
	wm := NewWorkerManager(nil, func() string { return "cursor" }, nil, nil, "/tmp", testLogger(t))
	wm.SetConstitutionSources(func() []constitution.Source {
		return []constitution.Source{
			&erroringSourceForSpawn{name: "a", err: errors.New("boom-a")},
			&erroringSourceForSpawn{name: "b", err: errors.New("boom-b")},
		}
	})
	if got := wm.resolvedConstitution(constitution.Scope{}); got != "" {
		t.Errorf("all-failure case must return empty string, got:\n%s", got)
	}
}

// effectiveEnv parses a slice produced by buildWorkerEnv and returns the value
// the child process would actually observe for key. Go's env map is built with
// "first occurrence wins", so when the same key appears twice the earlier
// entry (typically inherited from the parent) overrides any later entry (such
// as an override the daemon intended to inject for the worker).
func effectiveEnv(env []string, key string) string {
	prefix := key + "="
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return strings.TrimPrefix(e, prefix)
		}
	}
	return ""
}

// TestBuildWorkerEnv_StringworkEnvOverrideWinsAgainstParent is a regression
// test for a real bug we hit while writing the end-to-end lifecycle test:
//
// If the parent process already has STRINGWORK_SOCKET (or STRINGWORK_AGENT,
// STRINGWORK_WORKSPACE, STRINGWORK_BIN) set — which is ALWAYS the case on a
// developer machine running the daemon out of `~/.config/stringwork/` —
// buildWorkerEnv used to append its own value instead of replacing the
// inherited one. Go's env map resolves duplicates "first occurrence wins",
// so the parent's value silently won and the spawned worker talked to the
// wrong daemon.
//
// The symptom in the wild: on a dev machine with a running daemon on the
// default socket, running a second daemon (e.g. the e2e test) and spawning a
// CLI worker through it would route the worker's heartbeat calls to the
// FIRST daemon, which replied "unknown agent" because it didn't know about
// the test daemon's tasks. Very hard to diagnose because everything looks
// correct in logs on both sides.
//
// The fix is to have buildWorkerEnv use setEnvVar (replace-or-append) for
// every var it owns, so the daemon's intent is authoritative.
func TestBuildWorkerEnv_StringworkEnvOverrideWinsAgainstParent(t *testing.T) {
	t.Setenv("STRINGWORK_SOCKET", "/parent/should/not/leak.sock")
	t.Setenv("STRINGWORK_AGENT", "parent-agent")
	t.Setenv("STRINGWORK_WORKSPACE", "/parent/workspace")
	t.Setenv("STRINGWORK_BIN", "/parent/bin/mcp-stringwork")

	c := WorkerSpawnConfig{
		InstanceID:    "claude-code-task-42",
		AgentType:     "claude-code",
		Communication: "cli",
	}
	env := buildWorkerEnv(c, "/daemon/workspace", "/daemon/server.sock")

	if got := effectiveEnv(env, "STRINGWORK_AGENT"); got != "claude-code-task-42" {
		t.Errorf("STRINGWORK_AGENT: parent leak — got %q, want %q", got, "claude-code-task-42")
	}
	if got := effectiveEnv(env, "STRINGWORK_WORKSPACE"); got != "/daemon/workspace" {
		t.Errorf("STRINGWORK_WORKSPACE: parent leak — got %q, want %q", got, "/daemon/workspace")
	}
	// The daemon passed "/daemon/server.sock" explicitly — that exact
	// value must be what the child sees, not the parent's leaked one.
	if got := effectiveEnv(env, "STRINGWORK_SOCKET"); got != "/daemon/server.sock" {
		t.Errorf("STRINGWORK_SOCKET: got %q, want %q", got, "/daemon/server.sock")
	}
	// STRINGWORK_BIN should point at the current executable, not whatever
	// the parent had. We can't assert the exact path portably, but it
	// must not equal the parent's intentionally-wrong value.
	if got := effectiveEnv(env, "STRINGWORK_BIN"); got == "/parent/bin/mcp-stringwork" {
		t.Errorf("STRINGWORK_BIN: parent value leaked through — got %q", got)
	}

	// Double-check: the daemon's intended values must be present exactly
	// once in the slice. Duplicates (the old broken behavior) mean the
	// child could read different values depending on whether it uses
	// Go's first-wins map or a last-wins enumerator (some tools do).
	counts := map[string]int{}
	for _, e := range env {
		if k, _, ok := strings.Cut(e, "="); ok {
			counts[k]++
		}
	}
	for _, key := range []string{"STRINGWORK_AGENT", "STRINGWORK_WORKSPACE", "STRINGWORK_SOCKET", "STRINGWORK_BIN"} {
		if counts[key] > 1 {
			t.Errorf("%s appears %d times in worker env; must be exactly 1 so no enumerator sees the parent's value", key, counts[key])
		}
	}
}
