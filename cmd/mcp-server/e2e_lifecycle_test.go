package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// e2eBinary is the path to the compiled stringwork binary used by the e2e
// test below. Built once per package test run via ensureE2EBinary so the
// suite stays cheap when the test is filtered out.
var (
	e2eBinaryOnce sync.Once
	e2eBinaryPath string
	e2eBinaryErr  error
)

func ensureE2EBinary(t *testing.T) string {
	t.Helper()
	e2eBinaryOnce.Do(func() {
		if runtime.GOOS == "windows" {
			e2eBinaryErr = fmt.Errorf("e2e test requires a unix shell")
			return
		}
		dir, err := os.MkdirTemp("", "stringwork-e2e-bin-*")
		if err != nil {
			e2eBinaryErr = err
			return
		}
		path := filepath.Join(dir, "mcp-stringwork")
		cmd := exec.Command("go", "build", "-o", path, ".")
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			e2eBinaryErr = fmt.Errorf("go build: %w", err)
			return
		}
		e2eBinaryPath = path
	})
	if e2eBinaryErr != nil {
		t.Skipf("e2e test skipped: %v", e2eBinaryErr)
	}
	return e2eBinaryPath
}

// filteredEnv returns the parent process's environment with any variables
// whose key has the given prefix removed. We use it to keep test daemon
// invocations hermetic: the dev machine almost always has a real
// STRINGWORK_SOCKET / STRINGWORK_URL pointing at the user's running daemon
// at ~/.config/stringwork/server.sock, and inheriting that would silently
// route the spawned worker's CLI calls to the wrong server.
func filteredEnv(stripPrefix string) []string {
	src := os.Environ()
	out := make([]string, 0, len(src))
	for _, e := range src {
		if strings.HasPrefix(e, stripPrefix) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// pickFreeTCPPort opens a listener on :0, reads back the port, and closes it.
// There is a small race between close and the daemon's bind, but it is the
// standard approach used by Go's own integration tests.
func pickFreeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen :0: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// waitFor polls fn until it returns true or timeout elapses. Returns the last
// value reported by fn so the caller can include it in the failure message.
func waitFor(t *testing.T, timeout time.Duration, label string, fn func() (bool, string)) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	last := ""
	for time.Now().Before(deadline) {
		ok, detail := fn()
		last = detail
		if ok {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s; last detail: %s", label, last)
}

// mcpClient is a minimal Streamable HTTP MCP client: it remembers the
// session id returned by `initialize` and re-sends it on every subsequent
// request, which the mcp-go server requires.
type mcpClient struct {
	baseURL   string
	sessionID string
	http      *http.Client
}

func newMCPClient(baseURL string) *mcpClient {
	return &mcpClient{baseURL: baseURL, http: &http.Client{Timeout: 10 * time.Second}}
}

// call posts a single JSON-RPC request to the daemon's /mcp endpoint and
// returns the parsed response body. The Streamable HTTP server returns
// either application/json or text/event-stream depending on negotiation.
func (c *mcpClient) call(t *testing.T, id int, method string, params any) map[string]any {
	t.Helper()
	body := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		body["params"] = params
	}
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, c.baseURL+"/mcp", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if c.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", c.sessionID)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("mcp call %s: %v", method, err)
	}
	defer resp.Body.Close()
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		c.sessionID = sid
	}
	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		t.Fatalf("mcp call %s: status %d body=%s", method, resp.StatusCode, string(bodyBytes))
	}
	if len(bodyBytes) == 0 {
		return nil
	}
	// Streamable HTTP server returns SSE-framed events when the client
	// asks for text/event-stream — strip "data: " prefixes and concatenate
	// the data payloads before unmarshaling.
	bodyText := string(bodyBytes)
	if strings.HasPrefix(strings.TrimSpace(bodyText), "event:") || strings.Contains(bodyText, "\ndata:") || strings.HasPrefix(bodyText, "data:") {
		var dataLines []string
		for _, line := range strings.Split(bodyText, "\n") {
			if strings.HasPrefix(line, "data:") {
				dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
		bodyText = strings.Join(dataLines, "")
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(bodyText), &out); err != nil {
		t.Fatalf("mcp call %s: invalid JSON in body %q: %v", method, bodyText, err)
	}
	if errObj, ok := out["error"]; ok {
		t.Fatalf("mcp call %s: protocol error %v", method, errObj)
	}
	return out
}

// notify posts a JSON-RPC notification (no id) and ignores any response body.
func (c *mcpClient) notify(t *testing.T, method string, params any) {
	t.Helper()
	body := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		body["params"] = params
	}
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, c.baseURL+"/mcp", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if c.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", c.sessionID)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("mcp notify %s: %v", method, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

func (c *mcpClient) toolCall(t *testing.T, id int, tool string, args map[string]any) string {
	t.Helper()
	resp := c.call(t, id, "tools/call", map[string]any{
		"name": tool, "arguments": args,
	})
	result, _ := resp["result"].(map[string]any)
	if isErr, _ := result["isError"].(bool); isErr {
		raw, _ := json.Marshal(result)
		t.Fatalf("tool %s isError=true: %s", tool, string(raw))
	}
	content, _ := result["content"].([]any)
	var sb strings.Builder
	for _, c := range content {
		if m, ok := c.(map[string]any); ok {
			if s, ok := m["text"].(string); ok {
				sb.WriteString(s)
			}
		}
	}
	return sb.String()
}

// TestE2E_LifecycleStartAgentDoTask drives the full real-binary lifecycle:
// build the binary → start it as a daemon → configure a fake worker that
// uses the binary's own CLI subcommands to talk back over the socket →
// create a task assigned to that worker via MCP HTTP → wait for the worker
// to drive the task to "completed" → verify final state in state.sqlite.
//
// This is the test the user asked for: "starting agents and doing tasks
// is working". It exercises everything the unit tests stub out:
//
//   - Real binary, not in-process server.
//   - Real WorkerManager spawning a real OS process.
//   - Real CLI HTTP API endpoints (the worker dialing back).
//   - Real sqlite repository on disk.
//   - Real MCP Streamable HTTP transport for the driver.
//   - Real auto-spawn-on-create-task hook.
//
// The fake worker is `/bin/sh -c <one-liner>` that uses the binary's
// `heartbeat`, `progress`, and `task update` subcommands to advance the
// task to completion. It is fast and deterministic — no real LLM involved.
func TestE2E_LifecycleStartAgentDoTask(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e: skipping in -short mode")
	}
	binary := ensureE2EBinary(t)

	stateDir := t.TempDir()
	workspaceDir := t.TempDir()

	if out, err := exec.Command("git", "-C", workspaceDir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	// Unix socket paths are capped at 104 bytes on macOS / 108 on Linux,
	// well below typical t.TempDir() lengths. Allocate a short companion
	// dir in /tmp just for the socket and pid file.
	shortDir, err := os.MkdirTemp("", "swe2e-")
	if err != nil {
		t.Fatalf("short tmpdir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortDir) })
	socketPath := filepath.Join(shortDir, "s.sock")
	pidFile := filepath.Join(shortDir, "d.pid")
	stateSQL := filepath.Join(stateDir, "state.sqlite")
	logPath := filepath.Join(stateDir, "server.log")

	httpPort := pickFreeTCPPort(t)
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", httpPort)

	// Worker script: pull our task ID out of STRINGWORK_AGENT (which the
	// spawner sets to "<type>-task-<N>"), then drive heartbeat → in_progress
	// → progress → completed via the binary's CLI subcommands. The daemon
	// injects STRINGWORK_SOCKET / STRINGWORK_BIN authoritatively into the
	// worker env, so the CLI calls always reach THIS daemon regardless of
	// whatever the test-runner's shell happens to have set.
	workerScript := strings.Join([]string{
		"set -e",
		`TASK_ID=$(printf '%s' "$STRINGWORK_AGENT" | sed -n 's/.*-task-\([0-9][0-9]*\)$/\1/p')`,
		`if [ -z "$TASK_ID" ]; then echo "no task id in $STRINGWORK_AGENT" >&2; exit 1; fi`,
		`"$STRINGWORK_BIN" heartbeat --agent="$STRINGWORK_AGENT" --progress="e2e fake worker started" >/dev/null`,
		`"$STRINGWORK_BIN" task update --id="$TASK_ID" --by="$STRINGWORK_AGENT" --status=in_progress >/dev/null`,
		`"$STRINGWORK_BIN" progress --agent="$STRINGWORK_AGENT" --task="$TASK_ID" --description="e2e fake worker working" --percent=50 >/dev/null`,
		`"$STRINGWORK_BIN" task update --id="$TASK_ID" --by="$STRINGWORK_AGENT" --status=completed >/dev/null`,
	}, "; ")

	configYAML := fmt.Sprintf(`
workspace_root: %q
state_file: %q
log_file: %q
http_port: %d

audit:
  disabled: true

daemon:
  enabled: true
  socket_path: %q
  pid_file: %q
  grace_period_seconds: 10

orchestration:
  driver: cursor
  assignment_strategy: least_loaded
  heartbeat_interval_seconds: 30
  worker_timeout_seconds: 60
  workers:
    - type: e2e-fake-worker
      instances: 1
      communication: cli
      timeout_seconds: 30
      retry_delay_seconds: 1
      max_retries: 0
      command: ["/bin/sh", "-c", %q]
`, workspaceDir, stateSQL, logPath, httpPort, socketPath, pidFile, workerScript)

	configPath := filepath.Join(stateDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(configYAML), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	daemonCmd := exec.Command(binary, "--daemon")
	// Belt-and-suspenders: strip any STRINGWORK_* vars from the test's
	// own env before launching the daemon. buildWorkerEnv now replaces
	// (rather than appends) its own injected vars, so a leaked parent
	// STRINGWORK_SOCKET no longer silently wins, but we still prefer a
	// hermetic spawn so the test is robust against future env-handling
	// changes and developer shells that tweak STRINGWORK_* for debugging.
	daemonCmd.Env = append(filteredEnv("STRINGWORK_"),
		"MCP_CONFIG="+configPath,
	)
	daemonCmd.Stdout = os.Stderr
	daemonCmd.Stderr = os.Stderr
	daemonCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := daemonCmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			if data, err := os.ReadFile(logPath); err == nil {
				t.Logf("--- daemon log (%s) ---\n%s\n--- end daemon log ---", logPath, string(data))
			}
		}
		if daemonCmd.Process != nil {
			_ = syscall.Kill(-daemonCmd.Process.Pid, syscall.SIGTERM)
			done := make(chan error, 1)
			go func() { done <- daemonCmd.Wait() }()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				_ = syscall.Kill(-daemonCmd.Process.Pid, syscall.SIGKILL)
				<-done
			}
		}
		_ = os.Remove(socketPath)
		_ = os.Remove(pidFile)
	})

	waitFor(t, 10*time.Second, "daemon socket", func() (bool, string) {
		conn, err := net.DialTimeout("unix", socketPath, 200*time.Millisecond)
		if err != nil {
			return false, err.Error()
		}
		_ = conn.Close()
		return true, ""
	})
	waitFor(t, 10*time.Second, "daemon http /health", func() (bool, string) {
		resp, err := http.Get(baseURL + "/health")
		if err != nil {
			return false, err.Error()
		}
		defer resp.Body.Close()
		return resp.StatusCode == 200, fmt.Sprintf("status=%d", resp.StatusCode)
	})

	cli := newMCPClient(baseURL)
	cli.call(t, 1, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "e2e-driver", "version": "0"},
	})
	cli.notify(t, "notifications/initialized", nil)

	preList := cli.toolCall(t, 99, "list_agents", map[string]any{})
	t.Logf("list_agents (before create_task):\n%s", strings.TrimSpace(preList))

	createOut := cli.toolCall(t, 2, "create_task", map[string]any{
		"title":       "e2e fake-worker lifecycle",
		"description": "drive a task through a real spawned worker process",
		"created_by":  "cursor",
		"assigned_to": "e2e-fake-worker",
	})
	t.Logf("create_task: %s", strings.TrimSpace(createOut))

	openDB := func() *sql.DB {
		db, err := sql.Open("sqlite", "file:"+stateSQL+"?mode=ro&_busy_timeout=5000")
		if err != nil {
			t.Fatalf("open state.sqlite: %v", err)
		}
		return db
	}

	var taskID int
	var taskStatus, resultSummary, progressDesc string
	var progressPercent int

	waitFor(t, 30*time.Second, "task transitions to completed via spawned worker", func() (bool, string) {
		db := openDB()
		defer db.Close()
		row := db.QueryRow(`SELECT id, status, result_summary, progress_description, progress_percent
                              FROM tasks ORDER BY id DESC LIMIT 1`)
		if err := row.Scan(&taskID, &taskStatus, &resultSummary, &progressDesc, &progressPercent); err != nil {
			return false, "no task row yet: " + err.Error()
		}
		return taskStatus == "completed", fmt.Sprintf("task #%d status=%s progress=%q (%d%%)",
			taskID, taskStatus, progressDesc, progressPercent)
	})

	if taskID == 0 {
		t.Fatalf("expected exactly one task to exist after create_task")
	}
	if taskStatus != "completed" {
		t.Fatalf("expected task #%d status=completed, got %q (result_summary=%q)", taskID, taskStatus, resultSummary)
	}
	if !strings.Contains(progressDesc, "e2e fake worker working") {
		t.Errorf("expected task progress_description set by spawned worker; got %q", progressDesc)
	}
	if progressPercent != 50 {
		t.Errorf("expected progress_percent=50 set by worker; got %d", progressPercent)
	}

	db := openDB()
	defer db.Close()

	rows, err := db.Query(`SELECT instance_id, agent_type, status FROM agent_instances`)
	if err != nil {
		t.Fatalf("read agent_instances: %v", err)
	}
	defer rows.Close()
	var instanceIDs []string
	var taskBoundLeftover []string
	for rows.Next() {
		var id, atype, status string
		if err := rows.Scan(&id, &atype, &status); err != nil {
			t.Fatalf("scan agent_instances: %v", err)
		}
		instanceIDs = append(instanceIDs, fmt.Sprintf("%s(type=%s,status=%s)", id, atype, status))
		if strings.HasPrefix(id, "e2e-fake-worker-task-") {
			taskBoundLeftover = append(taskBoundLeftover, id)
		}
	}

	pres, err := db.Query(`SELECT agent FROM presence`)
	if err != nil {
		t.Fatalf("read presence: %v", err)
	}
	defer pres.Close()
	var presenceLeftover []string
	for pres.Next() {
		var a string
		_ = pres.Scan(&a)
		if strings.HasPrefix(a, "e2e-fake-worker-task-") {
			presenceLeftover = append(presenceLeftover, a)
		}
	}

	t.Logf("agent_instances after task completion: %v", instanceIDs)
	if len(taskBoundLeftover) > 0 {
		t.Errorf("task-bound AgentInstance rows leaked after completion: %v", taskBoundLeftover)
	}
	if len(presenceLeftover) > 0 {
		t.Errorf("task-bound Presence rows leaked after completion: %v", presenceLeftover)
	}

	var heartbeatTouched int
	row := db.QueryRow(`SELECT COUNT(*) FROM agent_instances
                         WHERE instance_id LIKE 'e2e-fake-worker%'
                           AND last_heartbeat <> ''`)
	_ = row.Scan(&heartbeatTouched)
	t.Logf("e2e-fake-worker rows with non-empty last_heartbeat: %d", heartbeatTouched)
}
