package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jaakkos/stringwork/internal/domain"
	"github.com/jaakkos/stringwork/internal/policy"
)

// Truncate truncates s to max runes (Unicode-safe).
func Truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "..."
}

// ValidateAgent returns an error if agent is not allowed.
// Validation uses state.AgentInstances (instance IDs and agent types) plus extraAllowed (e.g. registered agents).
func ValidateAgent(agent string, state *domain.CollabState, allowAny, allowAll bool, extraAllowed ...string) error {
	if agent == "" {
		return fmt.Errorf("agent identifier is required")
	}
	if allowAll && agent == "all" {
		return nil
	}
	if allowAny && agent == "any" {
		return nil
	}
	if state != nil {
		if _, ok := state.AgentInstances[agent]; ok {
			return nil
		}
		for _, inst := range state.AgentInstances {
			if inst != nil && inst.AgentType == agent {
				return nil
			}
		}
	}
	for _, extra := range extraAllowed {
		if agent == extra {
			return nil
		}
	}
	return fmt.Errorf("unknown agent %q", agent)
}

// RegisteredAgentNames returns the names of all dynamically registered agents.
// Pass the result as extraAllowed to ValidateAgent.
func RegisteredAgentNames(state *domain.CollabState) []string {
	if state == nil || len(state.RegisteredAgents) == 0 {
		return nil
	}
	names := make([]string, 0, len(state.RegisteredAgents))
	for name := range state.RegisteredAgents {
		names = append(names, name)
	}
	return names
}

// IsBuiltinAgent returns true if agent is a known instance or agent type in state.AgentInstances.
func IsBuiltinAgent(agent string, state *domain.CollabState) bool {
	if state == nil {
		return false
	}
	if _, ok := state.AgentInstances[agent]; ok {
		return true
	}
	for _, inst := range state.AgentInstances {
		if inst != nil && inst.AgentType == agent {
			return true
		}
	}
	return false
}

// GetBuiltinAgents returns unique agent type names from state.AgentInstances. Returns nil if state is nil or empty.
func GetBuiltinAgents(state *domain.CollabState) []string {
	if state == nil || len(state.AgentInstances) == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	for _, inst := range state.AgentInstances {
		if inst != nil && inst.AgentType != "" {
			seen[inst.AgentType] = struct{}{}
		}
	}
	agents := make([]string, 0, len(seen))
	for t := range seen {
		agents = append(agents, t)
	}
	return agents
}

// OrchestrationAgentTypes returns agent type names from orchestration config (driver + unique worker types).
// Used for instruction resources and agent lists when state has no instances yet. If orch is nil, returns ["cursor"].
func OrchestrationAgentTypes(orch *policy.OrchestrationConfig) []string {
	if orch == nil {
		return []string{"cursor"}
	}
	seen := make(map[string]struct{})
	if orch.Driver != "" {
		seen[orch.Driver] = struct{}{}
	}
	for _, w := range orch.Workers {
		if w.Type != "" {
			seen[w.Type] = struct{}{}
		}
	}
	agents := make([]string, 0, len(seen))
	for t := range seen {
		agents = append(agents, t)
	}
	return agents
}

// EnsureAgentInstances seeds state.AgentInstances from orchestration config or built-in defaults.
// Idempotent: if AgentInstances already populated, does nothing.
func EnsureAgentInstances(state *domain.CollabState, orch *policy.OrchestrationConfig) {
	if state == nil {
		return
	}
	if len(state.AgentInstances) > 0 {
		return
	}
	now := time.Now()
	if orch != nil {
		state.DriverID = orch.Driver
		state.AgentInstances[orch.Driver] = &domain.AgentInstance{
			InstanceID:    orch.Driver,
			AgentType:     orch.Driver,
			Role:          domain.RoleDriver,
			Capabilities:  []string{"orchestrate", "code-edit", "code-review", "search", "terminal"},
			MaxTasks:      0,
			Status:        "idle",
			LastHeartbeat: now,
		}
		for _, w := range orch.Workers {
			n := w.Instances
			if n <= 0 {
				n = 1
			}
			maxTasks := w.MaxConcurrentTasks
			if maxTasks <= 0 {
				maxTasks = 1
			}
			for i := 0; i < n; i++ {
				instanceID := w.Type
				if n > 1 {
					instanceID = fmt.Sprintf("%s-%d", w.Type, i+1)
				}
				state.AgentInstances[instanceID] = &domain.AgentInstance{
					InstanceID:    instanceID,
					AgentType:     w.Type,
					Role:          domain.RoleWorker,
					Capabilities:  w.Capabilities,
					MaxTasks:      maxTasks,
					Status:        "offline",
					CurrentTasks:  []int{},
					LastHeartbeat: now,
				}
			}
		}
		return
	}
	// No orchestration: use default (driver only) is applied in LoadConfig; nothing extra to seed here
}

// RefreshHeartbeatsOnStartup resets LastHeartbeat for all agent instances to "now"
// so that the watchdog doesn't immediately consider them stale after a server restart.
// Worker instances are set to "offline" status since they haven't reconnected yet.
// This should be called once at server startup inside a CollabService.Run.
func RefreshHeartbeatsOnStartup(state *domain.CollabState) {
	if state == nil {
		return
	}
	now := time.Now()
	for _, inst := range state.AgentInstances {
		if inst == nil {
			continue
		}
		// Refresh the heartbeat so the watchdog gives agents time to reconnect.
		inst.LastHeartbeat = now
		// Workers that haven't reconnected yet should start as offline.
		// The driver keeps its status (cursor may reconnect immediately).
		if inst.Role == domain.RoleWorker {
			inst.Status = "offline"
			inst.CurrentTasks = nil
		}
	}
}

// JoinStrings joins strs with sep. Prefer strings.Join for simple cases;
// this exists for API compatibility.
func JoinStrings(strs []string, sep string) string {
	return strings.Join(strs, sep)
}

// EscapeAppleScript escapes s for use in AppleScript.
func EscapeAppleScript(s string) string {
	var buf strings.Builder
	buf.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\\':
			buf.WriteString("\\\\")
		case '"':
			buf.WriteString("\\\"")
		case '\n':
			buf.WriteString("\\n")
		default:
			buf.WriteRune(r)
		}
	}
	return buf.String()
}

// DetectProjectInfo detects project information from the workspace path.
// It checks for git repository info and extracts the project name from the path.
func DetectProjectInfo(workspacePath string) *domain.ProjectInfo {
	info := &domain.ProjectInfo{
		Path:        workspacePath,
		Name:        filepath.Base(workspacePath),
		LastUpdated: time.Now(),
	}

	// Check if this is a git repository
	gitDir := filepath.Join(workspacePath, ".git")
	if stat, err := os.Stat(gitDir); err == nil && stat.IsDir() {
		info.IsGitRepo = true

		// Get current branch
		if branch, err := runGitCommand(workspacePath, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
			info.GitBranch = strings.TrimSpace(branch)
		}

		// Get remote URL (origin)
		if remote, err := runGitCommand(workspacePath, "config", "--get", "remote.origin.url"); err == nil {
			info.GitRemote = strings.TrimSpace(remote)
		}
	}

	return info
}

// runGitCommand runs a git command in the given directory and returns the output.
func runGitCommand(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
