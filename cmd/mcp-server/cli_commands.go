package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jaakkos/stringwork/internal/policy"
)

// cliClient sends HTTP requests to the Stringwork daemon over a unix socket
// (or TCP URL if STRINGWORK_URL is set).
type cliClient struct {
	httpClient *http.Client
	baseURL    string
}

func newCLIClient() *cliClient {
	if url := os.Getenv("STRINGWORK_URL"); url != "" {
		return &cliClient{
			httpClient: &http.Client{Timeout: 10 * time.Second},
			baseURL:    strings.TrimRight(url, "/"),
		}
	}

	socketPath := os.Getenv("STRINGWORK_SOCKET")
	if socketPath == "" {
		socketPath = policy.DefaultSocketPath()
	}

	transport := &http.Transport{
		DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			return net.DialTimeout("unix", socketPath, 5*time.Second)
		},
	}
	return &cliClient{
		httpClient: &http.Client{Transport: transport, Timeout: 10 * time.Second},
		baseURL:    "http://daemon",
	}
}

func (c *cliClient) post(path string, body interface{}) (string, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}
	resp, err := c.httpClient.Post(c.baseURL+path, "application/json", bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("request failed (is the daemon running?): %w", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("%s", out)
	}
	return string(out), nil
}

func (c *cliClient) get(path string) (string, error) {
	resp, err := c.httpClient.Get(c.baseURL + path)
	if err != nil {
		return "", fmt.Errorf("request failed (is the daemon running?): %w", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("%s", out)
	}
	return string(out), nil
}

// dispatchCLICommand handles the CLI subcommands for worker communication.
// Returns true if a subcommand was matched and handled.
func dispatchCLICommand(args []string) bool {
	if len(args) < 2 {
		return false
	}
	switch args[1] {
	case "heartbeat":
		runHeartbeatCLI(args[2:])
	case "progress":
		runProgressCLI(args[2:])
	case "send":
		runSendCLI(args[2:])
	case "task":
		runTaskCLI(args[2:])
	case "read":
		runReadCLI(args[2:])
	case "presence":
		runPresenceCLI(args[2:])
	case "context":
		runContextCLI(args[2:])
	case "work-ctx":
		runWorkCtxCLI(args[2:])
	default:
		return false
	}
	return true
}

func runHeartbeatCLI(args []string) {
	agent := flagValue(args, "--agent")
	if agent == "" {
		agent = os.Getenv("STRINGWORK_AGENT")
	}
	if agent == "" {
		cliDie("--agent is required (or set STRINGWORK_AGENT)")
	}
	progress := flagValue(args, "--progress")
	step, _ := strconv.Atoi(flagValue(args, "--step"))
	totalSteps, _ := strconv.Atoi(flagValue(args, "--total"))
	sessionID := flagValue(args, "--session-id")

	client := newCLIClient()
	body := map[string]interface{}{"agent": agent, "progress": progress}
	if step > 0 {
		body["step"] = step
	}
	if totalSteps > 0 {
		body["total_steps"] = totalSteps
	}
	if sessionID != "" {
		body["session_id"] = sessionID
	}
	result, err := client.post("/api/w/heartbeat", body)
	if err != nil {
		cliDie(err.Error())
	}
	fmt.Print(result)
}

func runProgressCLI(args []string) {
	agent := flagValue(args, "--agent")
	if agent == "" {
		agent = os.Getenv("STRINGWORK_AGENT")
	}
	taskID, _ := strconv.Atoi(flagValue(args, "--task"))
	description := flagValue(args, "--description")
	percent, _ := strconv.Atoi(flagValue(args, "--percent"))

	if agent == "" || taskID == 0 || description == "" {
		cliDie("--agent, --task, and --description are required")
	}

	client := newCLIClient()
	body := map[string]interface{}{
		"agent":       agent,
		"task_id":     taskID,
		"description": description,
	}
	if percent > 0 {
		body["percent_complete"] = percent
	}
	if eta := flagValue(args, "--eta"); eta != "" {
		if n, err := strconv.Atoi(eta); err == nil {
			body["eta_seconds"] = n
		}
	}
	result, err := client.post("/api/w/progress", body)
	if err != nil {
		cliDie(err.Error())
	}
	fmt.Print(result)
}

func runSendCLI(args []string) {
	from := flagValue(args, "--from")
	if from == "" {
		from = os.Getenv("STRINGWORK_AGENT")
	}
	to := flagValue(args, "--to")
	content := flagValue(args, "--content")

	if from == "" || to == "" || content == "" {
		cliDie("--from, --to, and --content are required")
	}

	client := newCLIClient()
	result, err := client.post("/api/w/send", map[string]interface{}{
		"from": from, "to": to, "content": content,
	})
	if err != nil {
		cliDie(err.Error())
	}
	fmt.Print(result)
}

func runTaskCLI(args []string) {
	if len(args) == 0 {
		cliDie("usage: mcp-stringwork task [update|list] ...")
	}
	switch args[0] {
	case "update":
		runTaskUpdateCLI(args[1:])
	case "list":
		runTaskListCLI(args[1:])
	default:
		cliDie("unknown task subcommand: " + args[0])
	}
}

func runTaskUpdateCLI(args []string) {
	id, _ := strconv.Atoi(flagValue(args, "--id"))
	updatedBy := flagValue(args, "--by")
	if updatedBy == "" {
		updatedBy = os.Getenv("STRINGWORK_AGENT")
	}
	status := flagValue(args, "--status")

	if id == 0 || updatedBy == "" {
		cliDie("--id and --by are required")
	}

	client := newCLIClient()
	body := map[string]interface{}{
		"id":         id,
		"updated_by": updatedBy,
	}
	if status != "" {
		body["status"] = status
	}
	result, err := client.post("/api/w/task/update", body)
	if err != nil {
		cliDie(err.Error())
	}
	fmt.Print(result)
}

func runTaskListCLI(args []string) {
	assignedTo := flagValue(args, "--assigned-to")
	status := flagValue(args, "--status")
	agent := flagValue(args, "--agent")
	if agent == "" {
		agent = os.Getenv("STRINGWORK_AGENT")
	}

	qv := url.Values{}
	if assignedTo != "" {
		qv.Set("assigned_to", assignedTo)
	}
	if status != "" {
		qv.Set("status", status)
	}
	if agent != "" {
		qv.Set("agent", agent)
	}

	client := newCLIClient()
	result, err := client.get("/api/w/task/list?" + qv.Encode())
	if err != nil {
		cliDie(err.Error())
	}
	fmt.Print(result)
}

func runReadCLI(args []string) {
	recipient := flagValue(args, "--for")
	if recipient == "" {
		recipient = os.Getenv("STRINGWORK_AGENT")
	}
	if recipient == "" {
		cliDie("--for is required (or set STRINGWORK_AGENT)")
	}
	limit := flagValue(args, "--limit")

	qv := url.Values{}
	qv.Set("for", recipient)
	if limit != "" {
		qv.Set("limit", limit)
	}

	client := newCLIClient()
	result, err := client.get("/api/w/messages?" + qv.Encode())
	if err != nil {
		cliDie(err.Error())
	}
	fmt.Print(result)
}

func runPresenceCLI(args []string) {
	agent := flagValue(args, "--agent")
	if agent == "" {
		agent = os.Getenv("STRINGWORK_AGENT")
	}
	status := flagValue(args, "--status")
	workspace := flagValue(args, "--workspace")
	if workspace == "" {
		workspace = os.Getenv("STRINGWORK_WORKSPACE")
	}

	if agent == "" || status == "" {
		cliDie("--agent and --status are required")
	}

	client := newCLIClient()
	body := map[string]interface{}{"agent": agent, "status": status}
	if workspace != "" {
		body["workspace"] = workspace
	}
	result, err := client.post("/api/w/presence", body)
	if err != nil {
		cliDie(err.Error())
	}
	fmt.Print(result)
}

func runContextCLI(args []string) {
	agent := flagValue(args, "--for")
	if agent == "" {
		agent = os.Getenv("STRINGWORK_AGENT")
	}
	if agent == "" {
		cliDie("--for is required (or set STRINGWORK_AGENT)")
	}

	client := newCLIClient()
	qv := url.Values{}
	qv.Set("for", agent)
	result, err := client.get("/api/w/context?" + qv.Encode())
	if err != nil {
		cliDie(err.Error())
	}
	fmt.Print(result)
}

func runWorkCtxCLI(args []string) {
	taskID := flagValue(args, "--task")
	if taskID == "" {
		cliDie("--task is required")
	}

	client := newCLIClient()
	qv := url.Values{}
	qv.Set("task_id", taskID)
	result, err := client.get("/api/w/work-context?" + qv.Encode())
	if err != nil {
		cliDie(err.Error())
	}
	fmt.Print(result)
}

// flagValue extracts the value for a --key=value or --key value flag.
func flagValue(args []string, key string) string {
	for i, arg := range args {
		if arg == key && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(arg, key+"=") {
			return strings.TrimPrefix(arg, key+"=")
		}
	}
	return ""
}

func cliDie(msg string) {
	fmt.Fprintf(os.Stderr, "error: %s\n", msg)
	os.Exit(1)
}
