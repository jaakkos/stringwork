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

// RemoveTaskFromInstance removes taskID from the matching AgentInstance's
// CurrentTasks slice. Tries direct InstanceID lookup first; on miss, scans
// every instance and removes from the first one that lists the task. When
// the match's CurrentTasks becomes empty and Status is "busy", flips
// Status back to "idle". Safe to call when no instance owns the task — it
// becomes a no-op.
//
// This is the single canonical implementation. Replaces the four
// near-identical copies that previously lived in cli_api.go,
// dashboard/api.go, watchdog.go, and tools/collab/tasks.go (some with
// swapped argument order).
func RemoveTaskFromInstance(state *domain.CollabState, taskID int, agent string) {
	if state == nil || agent == "" {
		return
	}
	if inst, ok := state.AgentInstances[agent]; ok && inst != nil {
		removeTaskID(inst, taskID)
		return
	}
	for _, inst := range state.AgentInstances {
		if inst == nil {
			continue
		}
		for _, id := range inst.CurrentTasks {
			if id == taskID {
				removeTaskID(inst, taskID)
				return
			}
		}
	}
}

func removeTaskID(inst *domain.AgentInstance, taskID int) {
	filtered := make([]int, 0, len(inst.CurrentTasks))
	for _, id := range inst.CurrentTasks {
		if id != taskID {
			filtered = append(filtered, id)
		}
	}
	inst.CurrentTasks = filtered
	if len(inst.CurrentTasks) == 0 && inst.Status == "busy" {
		inst.Status = "idle"
	}
}

// AddTaskToInstance adds taskID to the matching AgentInstance's
// CurrentTasks slice. Tries direct InstanceID lookup first; on miss,
// scans every instance and uses the first non-task-bound one whose
// AgentType matches `agent`, preferring the lexicographically lowest
// InstanceID for deterministic selection across map-iteration runs.
// Idempotent: re-adding an already-tracked task is a no-op. On success,
// flips Status to "busy".
//
// Task-bound siblings ("<type>-task-N") are intentionally skipped in
// the fallback path: they exist solely to run their owning task and
// must never accumulate other work, otherwise reaping them on terminal
// transition would silently lose the just-added task.
//
// Safe to call when no matching instance exists (no-op).
func AddTaskToInstance(state *domain.CollabState, taskID int, agent string) {
	if state == nil || agent == "" {
		return
	}
	inst, ok := state.AgentInstances[agent]
	if !ok || inst == nil {
		var bestID string
		for id, candidate := range state.AgentInstances {
			if candidate == nil || candidate.AgentType != agent {
				continue
			}
			if IsTaskBoundInstance(state, id) {
				continue
			}
			if inst == nil || id < bestID {
				inst = candidate
				bestID = id
			}
		}
	}
	if inst == nil {
		return
	}
	for _, id := range inst.CurrentTasks {
		if id == taskID {
			return
		}
	}
	inst.CurrentTasks = append(inst.CurrentTasks, taskID)
	inst.Status = "busy"
}

// ApplyTaskTransitionOpts configures a task status / assignment change
// processed by ApplyTaskTransition. NewStatus and NewAssignee are optional:
// "" leaves the corresponding task field unchanged.
type ApplyTaskTransitionOpts struct {
	NewStatus          string // "" leaves task.Status unchanged
	NewAssignee        string // "" leaves task.AssignedTo unchanged
	UpdatedBy          string // who is making the update (used to skip self-spawn)
	SkipReviewGate     bool   // bypass requires_review check (replay paths)
	SkipDependencyGate bool   // bypass dependency completeness check
}

// ApplyTaskTransitionResult reports the side effects of a task transition
// so callers can drive post-lock work (spawning workers, sending messages).
// The returned Task pointer is into state.Tasks — do not retain past the
// state-lock release.
type ApplyTaskTransitionResult struct {
	Task          *domain.Task
	OldStatus     string
	OldAssignee   string
	IsTerminal    bool   // task ended in completed / cancelled / blocked
	NeedsSpawn    bool   // caller should call spawner.SpawnForTask
	SpawnAssignee string // who to spawn for, when NeedsSpawn
}

// ApplyTaskTransition applies a status/assignment change to a task with
// uniform validation and bookkeeping across the MCP and CLI write paths.
// Caller must hold the state lock (typically inside CollabService.Run).
//
// Side effects performed when validation passes:
//   - validates dependency completeness when transitioning to in_progress
//     (override with SkipDependencyGate)
//   - validates review approval when transitioning to completed
//     (override with SkipReviewGate)
//   - rejects regression from a terminal state (completed/cancelled/blocked)
//     back to an active state — use replay_task to re-open
//   - removes taskID from the old assignee's CurrentTasks when leaving
//     in_progress or when the assignee changes
//   - adds taskID to the new assignee's CurrentTasks when entering
//     in_progress
//   - reaps the task-bound AgentInstance (and its Presence row) on
//     terminal transitions (completed, cancelled, blocked) — for the
//     parent agent type only, matching "<type>-task-<taskID>"
//   - sets task.UpdatedAt to time.Now()
//
// Returns an error for invalid transitions; in that case state is not
// modified. Returns the result describing what changed when successful.
func ApplyTaskTransition(state *domain.CollabState, taskID int, opts ApplyTaskTransitionOpts) (*ApplyTaskTransitionResult, error) {
	if state == nil {
		return nil, fmt.Errorf("ApplyTaskTransition: state is nil")
	}

	var task *domain.Task
	for i := range state.Tasks {
		if state.Tasks[i].ID == taskID {
			task = &state.Tasks[i]
			break
		}
	}
	if task == nil {
		return nil, fmt.Errorf("task #%d not found", taskID)
	}

	oldStatus := task.Status
	oldAssignee := task.AssignedTo

	terminal := func(s string) bool {
		return s == "completed" || s == "cancelled" || s == "blocked"
	}

	newStatus := opts.NewStatus
	if newStatus == "" {
		newStatus = oldStatus
	}

	if newStatus != oldStatus && terminal(oldStatus) && !terminal(newStatus) {
		return nil, fmt.Errorf("cannot transition task #%d from %q to %q: terminal states are write-once (use replay_task to re-open)", taskID, oldStatus, newStatus)
	}

	if opts.NewStatus != "" && opts.NewStatus == "in_progress" && oldStatus != "in_progress" && !opts.SkipDependencyGate {
		var incomplete []int
		for _, depID := range task.Dependencies {
			for _, t := range state.Tasks {
				if t.ID == depID && t.Status != "completed" {
					incomplete = append(incomplete, depID)
					break
				}
			}
		}
		if len(incomplete) > 0 {
			return nil, fmt.Errorf("cannot start: dependencies not complete: %v", incomplete)
		}
	}

	if opts.NewStatus == "completed" && task.RequiresReview && task.ReviewStatus != "approved" && !opts.SkipReviewGate {
		return nil, fmt.Errorf("cannot complete task: review required (current status: %s)", task.ReviewStatus)
	}

	if opts.NewStatus != "" {
		task.Status = opts.NewStatus
	}

	newAssignee := oldAssignee
	if opts.NewAssignee != "" {
		task.AssignedTo = opts.NewAssignee
		newAssignee = opts.NewAssignee
	}

	leavingInProgress := oldStatus == "in_progress" && task.Status != "in_progress"
	assigneeChanged := oldAssignee != "" && newAssignee != oldAssignee
	if (leavingInProgress || assigneeChanged) && oldAssignee != "" {
		RemoveTaskFromInstance(state, taskID, oldAssignee)
	}

	enteringInProgress := task.Status == "in_progress" && (oldStatus != "in_progress" || assigneeChanged)
	if enteringInProgress && newAssignee != "" && newAssignee != "any" {
		AddTaskToInstance(state, taskID, newAssignee)
	}

	isTerminal := terminal(task.Status)
	if isTerminal {
		if oldAssignee != "" {
			ReapTaskBoundInstanceForTask(state, oldAssignee, taskID)
		}
		if newAssignee != "" && newAssignee != "any" && newAssignee != oldAssignee {
			ReapTaskBoundInstanceForTask(state, newAssignee, taskID)
		}
	}

	task.UpdatedAt = time.Now()

	res := &ApplyTaskTransitionResult{
		Task:        task,
		OldStatus:   oldStatus,
		OldAssignee: oldAssignee,
		IsTerminal:  isTerminal,
	}

	// Spawn semantics: a real worker (not "any") was newly assigned (or
	// re-assigned) and the task is in an active state. Don't self-spawn
	// when the updater themselves owns the new assignment.
	if newAssignee != "" && newAssignee != "any" && newAssignee != oldAssignee &&
		newAssignee != opts.UpdatedBy && (task.Status == "pending" || task.Status == "in_progress") {
		res.NeedsSpawn = true
		res.SpawnAssignee = newAssignee
	}

	return res, nil
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
