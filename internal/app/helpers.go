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

// ConfiguredDriver returns the configured driver agent name from state,
// falling back to "cursor" for backward compatibility.
func ConfiguredDriver(state *domain.CollabState) string {
	if state != nil && state.DriverID != "" {
		return state.DriverID
	}
	return "cursor"
}

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

// IsTaskBoundInstance reports whether the named agent is a task-bound worker
// instance (lifetime tied to a single task) rather than a static pool worker.
//
// Convention used throughout the codebase: a task-bound instance has its
// InstanceID set to "<type>-task-<taskID>" while its AgentType remains the
// pool type. Static pool workers have InstanceID == AgentType (single
// instance) or "<type>-N" (multi-instance pool, but still re-used).
//
// Use this to decide whether an instance row should be deleted on terminal
// task transitions / cancel_agent (true → reap) or just marked idle (false).
func IsTaskBoundInstance(state *domain.CollabState, agent string) bool {
	if state == nil || agent == "" {
		return false
	}
	inst, ok := state.AgentInstances[agent]
	if !ok || inst == nil {
		return false
	}
	if inst.Role != domain.RoleWorker {
		return false
	}
	// "claude-code-task-7" vs AgentType "claude-code".
	return strings.Contains(inst.InstanceID, "-task-")
}

// StripTaskBoundSuffix returns (baseType, true) if agent matches the
// "<base>-task-<digits>" convention. Returns ("", false) otherwise. The
// base is never empty on a true return.
func StripTaskBoundSuffix(agent string) (string, bool) {
	idx := strings.LastIndex(agent, "-task-")
	if idx <= 0 {
		return "", false
	}
	suffix := agent[idx+len("-task-"):]
	if suffix == "" {
		return "", false
	}
	for _, r := range suffix {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	return agent[:idx], true
}

// ResolveParentAgentType returns the canonical parent agent type for any
// agent identifier — a static-pool instance ID ("claude-code-1"), a
// task-bound instance ID ("claude-code-task-7"), the parent type itself
// ("claude-code"), or a registered custom agent name.
//
// Resolution precedence:
//  1. existing AgentInstance.AgentType (when not corrupted with "-task-")
//  2. exact match in RegisteredAgents (for names that are not themselves
//     "-task-N" patterns)
//  3. task-bound ID → stripped base, if the base is a known type
//  4. longest-prefix match against RegisteredAgents + other instances'
//     parent AgentTypes
//  5. stripped "-task-N" suffix as a last resort
//  6. the input unchanged
//
// This is the single source of truth for agent-type resolution across the
// write paths (heartbeat auto-create, resolveWorkerAgent) and the watchdog.
func ResolveParentAgentType(state *domain.CollabState, agent string) string {
	if agent == "" {
		return agent
	}
	base, isTaskBound := StripTaskBoundSuffix(agent)
	if state == nil {
		if isTaskBound {
			return base
		}
		return agent
	}

	if inst, ok := state.AgentInstances[agent]; ok && inst != nil && inst.AgentType != "" && !strings.Contains(inst.AgentType, "-task-") {
		return inst.AgentType
	}

	if !isTaskBound {
		if _, ok := state.RegisteredAgents[agent]; ok {
			return agent
		}
	}

	if isTaskBound {
		if _, ok := state.RegisteredAgents[base]; ok {
			return base
		}
		for _, inst := range state.AgentInstances {
			if inst != nil && inst.AgentType == base {
				return base
			}
		}
	}

	candidate := ""
	for name := range state.RegisteredAgents {
		if strings.HasPrefix(agent, name+"-") && len(name) > len(candidate) {
			candidate = name
		}
	}
	for _, inst := range state.AgentInstances {
		if inst == nil || inst.AgentType == "" {
			continue
		}
		if strings.Contains(inst.AgentType, "-task-") {
			continue
		}
		t := inst.AgentType
		if strings.HasPrefix(agent, t+"-") && len(t) > len(candidate) {
			candidate = t
		}
	}
	if candidate != "" {
		return candidate
	}

	if isTaskBound {
		return base
	}
	return agent
}

// ReapTaskBoundInstance removes a task-bound worker's AgentInstance and
// matching Presence row from state. Returns true if the agent was task-bound
// and was reaped; false otherwise (static pool workers, drivers, or unknown
// names — all left untouched).
//
// Safe to call on any name: it's a no-op for non-task-bound rows. Intended to
// be invoked when the owning task reaches a terminal state (completed /
// cancelled / blocked) or when cancel_agent is invoked on the worker — at
// that point the task-bound instance has no further purpose and otherwise
// piles up as a zombie row.
func ReapTaskBoundInstance(state *domain.CollabState, agent string) bool {
	if !IsTaskBoundInstance(state, agent) {
		return false
	}
	delete(state.AgentInstances, agent)
	delete(state.Presence, agent)
	return true
}

// ReapTaskBoundInstanceForTask deletes the task-bound AgentInstance row
// that was spawned for the given task, if any. The convention is
// "<agentType>-task-<taskID>". Returns the deleted instance ID, or "" if
// nothing was reaped.
//
// Call when a task reaches a terminal state (task.AssignedTo now holds the
// parent agent type, so ReapTaskBoundInstance cannot be used directly).
func ReapTaskBoundInstanceForTask(state *domain.CollabState, agentType string, taskID int) string {
	if state == nil || agentType == "" || taskID <= 0 {
		return ""
	}
	instanceID := fmt.Sprintf("%s-task-%d", agentType, taskID)
	inst, ok := state.AgentInstances[instanceID]
	if !ok || inst == nil {
		return ""
	}
	delete(state.AgentInstances, instanceID)
	delete(state.Presence, instanceID)
	return instanceID
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
// Used for instruction resources and agent lists when state has no instances yet.
// Returns ["cursor"] when orch is nil to ensure default agent resources are registered.
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
		// The driver keeps its status (it may reconnect immediately).
		if inst.Role == domain.RoleWorker {
			inst.Status = "offline"
			inst.CurrentTasks = nil
		}
	}
}

// TaskBoundCorruptionReport summarises the mutations made by
// MigrateTaskBoundCorruption during a single run.
type TaskBoundCorruptionReport struct {
	TasksReassigned      int
	InstancesRetyped     int
	RegisteredAgentsGone int
	Mutations            []string
}

// Total returns the total number of rows mutated.
func (r TaskBoundCorruptionReport) Total() int {
	return r.TasksReassigned + r.InstancesRetyped + r.RegisteredAgentsGone
}

// MigrateTaskBoundCorruption repairs the state invariants relied upon by the
// watchdog and the type-aware write paths:
//
//  1. tasks.AssignedTo must hold the parent agent type (e.g. "claude-code"),
//     never a concrete instance ID ("claude-code-1") or task-bound ID
//     ("claude-code-task-5").
//  2. AgentInstance.AgentType must be a parent type, never contain the
//     "-task-N" fragment. (Task-bound IDs go in InstanceID.)
//  3. RegisteredAgents must not contain any "-task-N" entries — those are
//     ephemeral worker instances, not top-level agent types.
//
// Historical bugs in heartbeat auto-create and register_agent let these
// invariants drift; this pass repairs existing rows on startup. Idempotent.
func MigrateTaskBoundCorruption(state *domain.CollabState) TaskBoundCorruptionReport {
	var report TaskBoundCorruptionReport
	if state == nil {
		return report
	}

	for i := range state.Tasks {
		t := &state.Tasks[i]
		if t.AssignedTo == "" || t.AssignedTo == "any" {
			continue
		}
		canonical := ResolveParentAgentType(state, t.AssignedTo)
		if canonical != "" && canonical != t.AssignedTo {
			report.Mutations = append(report.Mutations,
				fmt.Sprintf("task #%d: AssignedTo %q -> %q", t.ID, t.AssignedTo, canonical))
			t.AssignedTo = canonical
			report.TasksReassigned++
		}
	}

	for id, inst := range state.AgentInstances {
		if inst == nil {
			continue
		}
		if !strings.Contains(inst.AgentType, "-task-") {
			continue
		}
		canonical := ResolveParentAgentType(state, id)
		if canonical == "" || canonical == inst.AgentType {
			if base, ok := StripTaskBoundSuffix(inst.AgentType); ok {
				canonical = base
			} else {
				continue
			}
		}
		report.Mutations = append(report.Mutations,
			fmt.Sprintf("instance %q: AgentType %q -> %q", id, inst.AgentType, canonical))
		inst.AgentType = canonical
		report.InstancesRetyped++
	}

	for name := range state.RegisteredAgents {
		if _, ok := StripTaskBoundSuffix(name); !ok {
			continue
		}
		report.Mutations = append(report.Mutations,
			fmt.Sprintf("registered_agent %q: removed (task-bound IDs are not top-level agents)", name))
		delete(state.RegisteredAgents, name)
		report.RegisteredAgentsGone++
	}

	return report
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
