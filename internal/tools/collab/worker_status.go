package collab

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/jaakkos/stringwork/internal/app"
	"github.com/jaakkos/stringwork/internal/domain"
)

// registerWorkerStatus registers the worker_status tool (driver-oriented: list workers and their status).
func registerWorkerStatus(s *server.MCPServer, svc *app.CollabService, logger *log.Logger, wtp WorktreeInfoProvider, pip ProcessInfoProvider) {
	s.AddTool(
		mcp.NewTool("worker_status",
			mcp.WithDescription("List all worker instances with status, progress, process activity, and worktree info. Shows what each worker is doing, how long since their last progress report, and whether their process is producing output."),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var result string
			err := svc.Query(func(state *domain.CollabState) error {
				now := time.Now()
				result = "=== Worker Status ===\n\n"
				driverID := state.DriverID
				if driverID != "" {
					result += fmt.Sprintf("Driver: %s\n\n", driverID)
				}

				// Collect in-progress task info for enriching worker output
				taskProgress := make(map[string][]taskProgressInfo) // assignedTo -> task progress
				for _, t := range state.Tasks {
					if t.Status == "in_progress" {
						tp := taskProgressInfo{
							ID:          t.ID,
							Title:       t.Title,
							Description: t.ProgressDescription,
							Percent:     t.ProgressPercent,
						}
						if !t.LastProgressAt.IsZero() {
							tp.SinceProgress = now.Sub(t.LastProgressAt).Round(time.Second).String()
						} else {
							tp.SinceProgress = now.Sub(t.UpdatedAt).Round(time.Second).String() + " (no report)"
						}
						if t.ExpectedDurationSec > 0 {
							expected := time.Duration(t.ExpectedDurationSec) * time.Second
							actual := now.Sub(t.UpdatedAt)
							if actual > expected {
								tp.SLAStatus = fmt.Sprintf("OVER by %s", (actual - expected).Round(time.Second))
							} else {
								tp.SLAStatus = fmt.Sprintf("OK (%s remaining)", (expected - actual).Round(time.Second))
							}
						}
						taskProgress[t.AssignedTo] = append(taskProgress[t.AssignedTo], tp)
					}
				}

				result += "Instances:\n"
				for id, inst := range state.AgentInstances {
					if inst == nil || inst.Role == domain.RoleDriver {
						continue
					}
					ago := "never"
					if !inst.LastHeartbeat.IsZero() {
						ago = now.Sub(inst.LastHeartbeat).Round(time.Second).String() + " ago"
					}
					tasks := ""
					if len(inst.CurrentTasks) > 0 {
						tasks = fmt.Sprintf(" (tasks: %v)", inst.CurrentTasks)
					}
					result += fmt.Sprintf("  - %s [%s] %s%s, heartbeat: %s\n", id, inst.AgentType, inst.Status, tasks, ago)

					// Show agent-level progress
					if inst.Progress != "" {
						progressAge := ""
						if !inst.ProgressUpdatedAt.IsZero() {
							progressAge = fmt.Sprintf(" (%s ago)", now.Sub(inst.ProgressUpdatedAt).Round(time.Second))
						}
						stepInfo := ""
						if inst.ProgressTotalSteps > 0 {
							stepInfo = fmt.Sprintf(" [step %d/%d]", inst.ProgressStep, inst.ProgressTotalSteps)
						}
						result += fmt.Sprintf("    Progress%s: %s%s\n", stepInfo, inst.Progress, progressAge)
					}

					// Show task-level progress
					if tps, ok := taskProgress[id]; ok {
						for _, tp := range tps {
							result += fmt.Sprintf("    Task #%d: %s", tp.ID, tp.Title)
							if tp.Percent > 0 {
								result += fmt.Sprintf(" (%d%%)", tp.Percent)
							}
							result += fmt.Sprintf(", last progress: %s", tp.SinceProgress)
							if tp.SLAStatus != "" {
								result += fmt.Sprintf(", SLA: %s", tp.SLAStatus)
							}
							result += "\n"
							if tp.Description != "" {
								result += fmt.Sprintf("      → %s\n", tp.Description)
							}
						}
					} else if tps, ok := taskProgress[inst.AgentType]; ok {
						for _, tp := range tps {
							result += fmt.Sprintf("    Task #%d: %s", tp.ID, tp.Title)
							if tp.Percent > 0 {
								result += fmt.Sprintf(" (%d%%)", tp.Percent)
							}
							result += fmt.Sprintf(", last progress: %s", tp.SinceProgress)
							if tp.SLAStatus != "" {
								result += fmt.Sprintf(", SLA: %s", tp.SLAStatus)
							}
							result += "\n"
							if tp.Description != "" {
								result += fmt.Sprintf("      → %s\n", tp.Description)
							}
						}
					}
				}

				// Process activity
				if pip != nil {
					procs := pip.GetProcessInfo()
					if len(procs) > 0 {
						result += "\nProcess Activity:\n"
						for id, p := range procs {
							outputAge := now.Sub(p.LastOutputAt).Round(time.Second)
							runtime := now.Sub(p.StartedAt).Round(time.Second)
							active := "active"
							if outputAge > 2*time.Minute {
								active = "SILENT"
							}
							result += fmt.Sprintf("  - %s: %s (running: %s, last output: %s ago, bytes: %d)\n",
								id, active, runtime, outputAge, p.OutputBytes)
						}
					}
				}

				// Worktree info
				if wtp != nil {
					wts := wtp.ListWorktrees()
					if len(wts) > 0 {
						result += "\nWorktrees:\n"
						for id, wt := range wts {
							result += fmt.Sprintf("  - %s: %s (branch: %s, base: %s)\n", id, wt.Path, wt.Branch, wt.BaseBranch)
						}
					}
				}

				return nil
			})
			if err != nil {
				return nil, err
			}
			logger.Printf("worker_status")
			return mcp.NewToolResultText(result), nil
		},
	)
}

// taskProgressInfo is an internal struct for rendering task progress in worker_status.
type taskProgressInfo struct {
	ID            int
	Title         string
	Description   string
	Percent       int
	SinceProgress string
	SLAStatus     string
}
