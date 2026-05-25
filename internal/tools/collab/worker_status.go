package collab

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/jaakkos/stringwork/internal/app"
	"github.com/jaakkos/stringwork/internal/domain"
)

// registerWorkerStatus registers the worker_status tool (driver-oriented: list workers and their status).
func registerWorkerStatus(s *server.MCPServer, svc *app.CollabService, logger *log.Logger, wtp WorktreeInfoProvider, pip ProcessInfoProvider, bip BackoffInfoProvider) {
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
					// Unified liveness verdict: combine heartbeat, session, and process output.
					verdict := livenessVerdict(id, inst, now, pip)
					result += fmt.Sprintf("  - %s [%s] %s%s, heartbeat: %s  %s\n", id, inst.AgentType, inst.Status, tasks, ago, verdict)

					// Show agent-level progress, but suppress lines that are
					// stale on an offline instance — the watchdog has already
					// concluded the worker is dead, so its last self-reported
					// progress (often "Sending findings to cursor", days old)
					// is misleading rather than informative.
					const progressStaleOnOffline = 2 * time.Minute
					showProgress := inst.Progress != ""
					if showProgress && inst.Status == "offline" && !inst.ProgressUpdatedAt.IsZero() &&
						now.Sub(inst.ProgressUpdatedAt) > progressStaleOnOffline {
						showProgress = false
					}
					if showProgress {
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

				// Backoff / rate-limit info
				if bip != nil {
					backedOff := bip.BackedOffAgentTypes()
					if len(backedOff) > 0 {
						result += "\nRate-Limited Workers:\n"
						for _, agentType := range backedOff {
							_, remaining, reason := bip.BackoffInfoForType(agentType)
							if remaining > 0 {
								result += fmt.Sprintf("  - %s: %s (auto-retry in %s)\n", agentType, reason, remaining.Round(time.Second))
							} else {
								result += fmt.Sprintf("  - %s: %s (manual restart required)\n", agentType, reason)
							}
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

// livenessVerdict returns a short string like "ALIVE (heartbeat 30s ago)",
// "UNRESPONSIVE (no signal for 5m12s)", "IDLE — wake on demand" (dormant
// pool slot that SpawnForTask will spawn when work arrives), or
// "UNKNOWN — no signals" by checking the best available signal.
//
// The IDLE verdict exists so a worker pool that is operationally healthy
// but currently has no running processes does not read as "the pool is
// dead" to driver agents. A pool slot whose Status=="offline", Role==Worker,
// has no CurrentTasks, no task-bound process tracked by the worker manager,
// and no task-bound suffix on its InstanceID is a dormant slot — see
// WorkerManager.SpawnForTask, which only checks countRunningByType (running
// OS processes) and is happy to spawn into such a slot. Reporting it as
// UNRESPONSIVE drove the regfin-review skill's Phase 3a Step 1 to fall back
// to native subagents instead of creating the task and letting SpawnForTask
// wake a fresh worker on demand.
func livenessVerdict(id string, inst *domain.AgentInstance, now time.Time, pip ProcessInfoProvider) string {
	if isDormantPoolSlot(id, inst, pip) {
		return "[IDLE — wake on demand]"
	}

	type signal struct {
		source string
		age    time.Duration
	}
	var best *signal

	if !inst.LastHeartbeat.IsZero() {
		age := now.Sub(inst.LastHeartbeat)
		best = &signal{"heartbeat", age}
	}

	if pip != nil {
		procs := pip.GetProcessInfo()
		prefix := id + "-"
		for pid, p := range procs {
			if pid == id || strings.HasPrefix(pid, prefix) {
				if !p.LastOutputAt.IsZero() {
					age := now.Sub(p.LastOutputAt)
					if best == nil || age < best.age {
						best = &signal{"process output", age}
					}
				}
			}
		}
	}

	if best == nil {
		return "[UNKNOWN — no signals]"
	}
	if best.age < 2*time.Minute {
		return fmt.Sprintf("[ALIVE — %s %s ago]", best.source, best.age.Round(time.Second))
	}
	return fmt.Sprintf("[UNRESPONSIVE — last signal: %s %s ago]", best.source, best.age.Round(time.Second))
}

// isDormantPoolSlot reports whether inst is a worker pool slot that is
// currently dormant but will be spawned on demand when SpawnForTask is
// called. The four required signals are:
//
//  1. Role is RoleWorker (drivers and other roles never spawn on demand).
//  2. Status is "offline" — either bootstrap-default (helpers.go pool
//     seeding) or watchdog-flipped after the previous process exited.
//  3. No CurrentTasks — a pool slot that thinks it owns work is in a
//     half-broken state and should keep showing UNRESPONSIVE so the
//     driver investigates.
//  4. No task-bound process is tracked for this InstanceID, AND the
//     InstanceID itself is not a task-bound row. A pool slot whose
//     process is still tracked is mid-spawn / mid-shutdown, not dormant;
//     a task-bound row (<type>-task-N) is ephemeral and "offline + no
//     process" means the worker died mid-task, not "dormant".
func isDormantPoolSlot(id string, inst *domain.AgentInstance, pip ProcessInfoProvider) bool {
	if inst == nil || inst.Role != domain.RoleWorker || inst.Status != "offline" {
		return false
	}
	if len(inst.CurrentTasks) > 0 {
		return false
	}
	if _, isTaskBound := app.StripTaskBoundSuffix(id); isTaskBound {
		return false
	}
	if pip != nil {
		procs := pip.GetProcessInfo()
		prefix := id + "-"
		for pid := range procs {
			if pid == id || strings.HasPrefix(pid, prefix) {
				return false
			}
		}
	}
	return true
}
