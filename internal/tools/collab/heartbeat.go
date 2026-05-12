package collab

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/jaakkos/stringwork/internal/app"
	"github.com/jaakkos/stringwork/internal/domain"
)

// registerHeartbeat registers the heartbeat tool (workers call this to signal liveness).
// sessionIDRecorder is optional; when set, session IDs are synced to the WorkerManager
// so that restarted workers can resume their CLI sessions.
func registerHeartbeat(s *server.MCPServer, svc *app.CollabService, logger *log.Logger, sessionIDRecorder SessionIDRecorder) {
	s.AddTool(
		mcp.NewTool("heartbeat",
			mcp.WithDescription(
				"Signal liveness and report progress. MANDATORY: call this every 60-90 seconds while working — no exceptions. "+
					"You MUST include progress details on every call. "+
					"Workers that fail to heartbeat are auto-cancelled after 7 minutes and their tasks are reassigned. "+
					"This is not optional — the server enforces these rules and will terminate non-compliant workers."),
			mcp.WithString("agent", mcp.Required(), mcp.Description("Your agent or instance ID (e.g. claude-code-1, codex)")),
			mcp.WithString("progress", mcp.Description("MANDATORY: What you're currently doing (e.g. 'writing unit tests for auth middleware'). Must be provided on every heartbeat.")),
			mcp.WithNumber("step", mcp.Description("Current step number (e.g. 3 of 5). Use with total_steps.")),
			mcp.WithNumber("total_steps", mcp.Description("Total number of steps in your current work.")),
			mcp.WithString("session_id", mcp.Description("Your CLI session/conversation ID. Report on first heartbeat so the server can resume your session if you get restarted.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := req.GetArguments()
			agent, _ := args["agent"].(string)
			if agent == "" {
				return nil, fmt.Errorf("agent is required")
			}

			progress, _ := args["progress"].(string)
			sessionID, _ := args["session_id"].(string)
			step := 0
			if s, ok := args["step"].(float64); ok {
				step = int(s)
			}
			totalSteps := 0
			if ts, ok := args["total_steps"].(float64); ok {
				totalSteps = int(ts)
			}

			err := svc.Run(func(state *domain.CollabState) error {
				inst, err := resolveOrMaterializeAgentInstance(state, agent)
				if err != nil {
					return err
				}
				if inst == nil {
					return fmt.Errorf("unknown agent %q", agent)
				}
				now := time.Now()
				inst.LastHeartbeat = now
				if inst.Status == "offline" {
					inst.Status = "idle"
				}
				// Update progress metadata
				if progress != "" {
					inst.Progress = progress
					inst.ProgressUpdatedAt = now
				}
				if step > 0 {
					inst.ProgressStep = step
				}
				if totalSteps > 0 {
					inst.ProgressTotalSteps = totalSteps
				}
				if sessionID != "" {
					inst.SessionID = sessionID
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
			if sessionID != "" && sessionIDRecorder != nil {
				sessionIDRecorder.SetWorkerSessionID(agent, sessionID)
			}
			if progress != "" {
				logger.Printf("heartbeat from %s (progress: %s)", agent, progress)
			} else {
				logger.Printf("heartbeat from %s", agent)
			}
			return mcp.NewToolResultText("OK"), nil
		},
	)
}

// resolveOrMaterializeAgentInstance looks up an AgentInstance for the
// given identifier and, when the identifier names a registered worker
// type that has not yet bootstrapped its row, materialises one with a
// fresh LastSpawnedAt.
//
// Lookup precedence:
//
//  1. Exact instance-ID match in state.AgentInstances.
//  2. Exactly one instance whose AgentType equals the identifier
//     (driver/single-instance worker convention). More than one match
//     is ambiguous and the caller is told which concrete instance IDs
//     to pick from.
//  3. Lazy bootstrap: when the identifier resolves to a known parent
//     type via RegisteredAgents, allocate a new AgentInstance with
//     LastSpawnedAt = now so the STOP-banner spawn cutoff in
//     piggyback.BuildBanner has a non-zero reference for CLI /
//     manually-bootstrapped agents that never went through
//     MarkInstanceSpawning. Without this, every cancelled task counts
//     as a reason to STOP — the kill-respawn loop diagnosed in
//     claude-code-task-32.
//
// Returns (nil, nil) when no match exists and no bootstrap fits;
// callers translate that into a "unknown agent" error so phantom
// instance IDs cannot silently leak into AgentInstances.
func resolveOrMaterializeAgentInstance(state *domain.CollabState, agent string) (*domain.AgentInstance, error) {
	if inst, ok := state.AgentInstances[agent]; ok {
		return inst, nil
	}
	var match *domain.AgentInstance
	var candidates []string
	for id, i := range state.AgentInstances {
		if i == nil || i.AgentType != agent {
			continue
		}
		// Skip task-bound siblings — they are ephemeral and never
		// the right reuse target for a parent-type heartbeat.
		if _, taskBound := app.StripTaskBoundSuffix(id); taskBound {
			continue
		}
		match = i
		candidates = append(candidates, id)
	}
	if len(candidates) == 1 {
		return match, nil
	}
	if len(candidates) > 1 {
		// Show the actual instance IDs the caller can pick from
		// instead of guessing "<agent>-1", which may not exist
		// when instances are named differently (e.g. UUID
		// suffixes from a custom spawner).
		sort.Strings(candidates)
		preview := candidates
		if len(preview) > 4 {
			preview = preview[:4]
		}
		return nil, fmt.Errorf(
			"ambiguous agent %q: %d instances exist — use one of %v",
			agent, len(candidates), preview,
		)
	}

	parentType := app.ResolveParentAgentType(state, agent)
	_, hasRegistered := state.RegisteredAgents[parentType]
	_, hasExactRegistered := state.RegisteredAgents[agent]
	if !hasRegistered && !hasExactRegistered {
		return nil, nil
	}
	inst := &domain.AgentInstance{
		InstanceID:    agent,
		AgentType:     parentType,
		Role:          domain.RoleWorker,
		Status:        "idle",
		CurrentTasks:  []int{},
		LastSpawnedAt: time.Now(),
	}
	state.AgentInstances[agent] = inst
	return inst, nil
}
