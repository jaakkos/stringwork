package collab

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/jaakkos/stringwork/internal/app"
	"github.com/jaakkos/stringwork/internal/domain"
	"github.com/jaakkos/stringwork/internal/policy"
)

const (
	defaultMaxBytes = 2048
	hardMaxBytes    = 16384
)

func registerWorkerOutput(s *server.MCPServer, svc *app.CollabService, logger *log.Logger, pip ProcessInfoProvider) {
	s.AddTool(
		mcp.NewTool("worker_output",
			mcp.WithDescription("Read recent output from a worker process. Use to diagnose whether a worker is stuck or actively working. Accepts one selector: instance_id, task_id, or agent."),
			mcp.WithString("instance_id", mcp.Description("Worker instance ID (e.g. 'codex-task-9', 'claude-code-1')")),
			mcp.WithNumber("task_id", mcp.Description("Task ID — resolves to the worker instance spawned for this task")),
			mcp.WithString("agent", mcp.Description("Agent type (e.g. 'codex', 'claude-code'). If multiple instances match, returns a disambiguation list.")),
			mcp.WithNumber("max_bytes", mcp.Description("Maximum bytes of output to return (default: 2048, max: 16384)")),
			mcp.WithString("source", mcp.Description("Where to read output from"), mcp.Enum("auto", "memory", "log")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := req.GetArguments()
			instanceID, _ := args["instance_id"].(string)
			agent, _ := args["agent"].(string)
			source, _ := args["source"].(string)
			if source == "" {
				source = "auto"
			}

			var taskID int
			if v, ok := args["task_id"].(float64); ok {
				taskID = int(v)
			}

			maxBytes := defaultMaxBytes
			if v, ok := args["max_bytes"].(float64); ok {
				maxBytes = int(v)
			}
			if maxBytes <= 0 {
				maxBytes = defaultMaxBytes
			}
			if maxBytes > hardMaxBytes {
				maxBytes = hardMaxBytes
			}

			selectors := 0
			if instanceID != "" {
				selectors++
			}
			if taskID > 0 {
				selectors++
			}
			if agent != "" {
				selectors++
			}
			if selectors == 0 {
				return mcp.NewToolResultError("Exactly one selector required: instance_id, task_id, or agent"), nil
			}
			if selectors > 1 {
				return mcp.NewToolResultError("Provide exactly one selector (instance_id, task_id, or agent), not multiple"), nil
			}

			if pip == nil {
				return mcp.NewToolResultError("Process monitoring is not available (no process provider configured)"), nil
			}

			// Resolve task_id to instance_id
			if taskID > 0 {
				resolved := resolveTaskInstance(svc, taskID, pip)
				if resolved == "" {
					return mcp.NewToolResultError(fmt.Sprintf("No worker instance found for task #%d", taskID)), nil
				}
				instanceID = resolved
			}

			// Resolve agent type to instance_id (with disambiguation)
			if agent != "" {
				resolved, ambiguous := resolveAgentInstance(agent, pip)
				if len(ambiguous) > 1 {
					return mcp.NewToolResultText(fmt.Sprintf("Multiple instances match agent '%s'. Specify one:\n%s",
						agent, formatInstanceList(ambiguous))), nil
				}
				if resolved == "" {
					return mcp.NewToolResultError(fmt.Sprintf("No running instance found for agent '%s'. Try source='log' with instance_id.", agent)), nil
				}
				instanceID = resolved
			}

			running := pip.IsWorkerRunning(instanceID)
			procs := pip.GetProcessInfo()
			proc, hasProc := procs[instanceID]

			var output string
			var outputSource string
			var truncated bool

			switch source {
			case "memory":
				if !running {
					return mcp.NewToolResultError(fmt.Sprintf("Worker '%s' is not running. Use source='log' or source='auto' to read from log file.", instanceID)), nil
				}
				output = pip.GetRecentOutput(instanceID)
				outputSource = "memory"
			case "log":
				logPath := resolveLogPath(instanceID, proc, hasProc)
				var err error
				output, err = readLogTail(logPath, maxBytes)
				if err != nil {
					return mcp.NewToolResultError(fmt.Sprintf("Cannot read log file: %v", err)), nil
				}
				outputSource = "log_file"
			default: // "auto"
				if running {
					output = pip.GetRecentOutput(instanceID)
					outputSource = "memory"
				} else {
					logPath := resolveLogPath(instanceID, proc, hasProc)
					var err error
					output, err = readLogTail(logPath, maxBytes)
					if err != nil {
						return mcp.NewToolResultError(fmt.Sprintf("Worker '%s' is not running and log file not found: %v", instanceID, err)), nil
					}
					outputSource = "log_file"
				}
			}

			output = sanitizeOutput(output)
			if len(output) > maxBytes {
				output = output[len(output)-maxBytes:]
				truncated = true
			}

			var b strings.Builder
			b.WriteString(fmt.Sprintf("Worker: %s\n", instanceID))
			b.WriteString(fmt.Sprintf("Source: %s\n", outputSource))
			b.WriteString(fmt.Sprintf("Running: %v\n", running))
			if hasProc {
				outputAge := time.Since(proc.LastOutputAt).Round(time.Second)
				b.WriteString(fmt.Sprintf("Last output: %s ago\n", outputAge))
				b.WriteString(fmt.Sprintf("Total output: %d bytes\n", proc.OutputBytes))
			}
			returnedBytes := len(output)
			if truncated {
				b.WriteString(fmt.Sprintf("Returned: %d bytes (truncated)\n", returnedBytes))
			} else {
				b.WriteString(fmt.Sprintf("Returned: %d bytes\n", returnedBytes))
			}

			if output == "" {
				b.WriteString("\n(no output captured)")
			} else {
				b.WriteString("\n--- output ---\n")
				b.WriteString(output)
			}

			logger.Printf("worker_output instance=%s source=%s bytes=%d", instanceID, outputSource, returnedBytes)
			return mcp.NewToolResultText(b.String()), nil
		},
	)
}

func resolveTaskInstance(svc *app.CollabService, taskID int, pip ProcessInfoProvider) string {
	procs := pip.GetProcessInfo()
	// Check running processes for a task-specific instance
	for id := range procs {
		if strings.HasSuffix(id, fmt.Sprintf("-task-%d", taskID)) {
			return id
		}
	}
	// Fall back to the task's assignee
	var assignee string
	_ = svc.Query(func(state *domain.CollabState) error {
		for _, t := range state.Tasks {
			if t.ID == taskID {
				assignee = t.AssignedTo
				break
			}
		}
		return nil
	})
	if assignee != "" {
		if _, ok := procs[assignee]; ok {
			return assignee
		}
	}
	return assignee
}

func resolveAgentInstance(agent string, pip ProcessInfoProvider) (resolved string, ambiguous []string) {
	procs := pip.GetProcessInfo()
	var matches []string
	for id := range procs {
		if id == agent || strings.HasPrefix(id, agent+"-") {
			matches = append(matches, id)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return "", matches
	}
	return "", nil
}

func formatInstanceList(instances []string) string {
	var b strings.Builder
	for _, id := range instances {
		b.WriteString(fmt.Sprintf("  - instance_id='%s'\n", id))
	}
	return b.String()
}

func resolveLogPath(instanceID string, proc ProcessInfoSnapshot, hasProc bool) string {
	if hasProc && proc.LogPath != "" {
		return proc.LogPath
	}
	safe := strings.ReplaceAll(instanceID, "/", "-")
	return fmt.Sprintf("%s/stringwork-worker-%s.log", policy.GlobalStateDir(), safe)
}

func readLogTail(path string, maxBytes int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return "", err
	}

	size := info.Size()
	if size == 0 {
		return "", nil
	}

	readSize := int64(maxBytes)
	if readSize > size {
		readSize = size
	}

	buf := make([]byte, readSize)
	_, err = f.ReadAt(buf, size-readSize)
	if err != nil {
		return "", err
	}
	return string(buf), nil
}

func sanitizeOutput(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || r == '\r' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}
