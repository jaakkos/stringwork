package collab

import (
	"log"
	"time"

	"github.com/mark3labs/mcp-go/server"

	"github.com/jaakkos/stringwork/internal/app"
	"github.com/jaakkos/stringwork/internal/quota"
)

// RegisterOption configures optional dependencies for tool registration.
type RegisterOption func(*registerOpts)

// WorktreeInfoProvider can return worktree information for worker instances.
type WorktreeInfoProvider interface {
	ListWorktrees() map[string]WorktreeInfo
}

// WorktreeInfo is a snapshot of a single worktree's metadata (matches worktree.WorktreeInfo).
type WorktreeInfo struct {
	Path       string `json:"path"`
	Branch     string `json:"branch"`
	BaseBranch string `json:"base_branch"`
}

// ProcessInfoProvider can return process activity and output for running workers.
type ProcessInfoProvider interface {
	GetProcessInfo() map[string]ProcessInfoSnapshot
	GetRecentOutput(instanceID string) string
	IsWorkerRunning(instanceID string) bool
}

// ProcessInfoSnapshot is a snapshot of a single worker process's activity.
type ProcessInfoSnapshot struct {
	StartedAt    time.Time `json:"started_at"`
	LastOutputAt time.Time `json:"last_output_at"`
	OutputBytes  int64     `json:"output_bytes"`
	WorkspaceDir string    `json:"workspace_dir"`
	LogPath      string    `json:"log_path"`
}

// TaskSpawner spawns a fresh worker process for a specific task.
type TaskSpawner interface {
	SpawnForTask(taskID int, assignedTo string)
}

// BackoffInfoProvider reports which agent types are currently rate-limited
// or otherwise backed off from receiving new work.
type BackoffInfoProvider interface {
	BackedOffAgentTypes() []string
	BackoffInfoForType(agentType string) (blocked bool, remaining time.Duration, reason string)
}

// ModelTierProvider exposes orchestration.model_tiers for driver visibility.
type ModelTierProvider interface {
	ModelTierMap() map[string]map[string]string
	WorkerAgentTypes() []string
}

// SessionIDRecorder records a worker's CLI session ID for session resume.
type SessionIDRecorder interface {
	SetWorkerSessionID(instanceID, sessionID string)
}

// QuotaSnapshotProvider exposes cached quota state for worker_status.
type QuotaSnapshotProvider interface {
	QuotaSnapshot() []quota.SnapshotEntry
}

type registerOpts struct {
	canceller         WorkerCanceller
	worktreeProvider  WorktreeInfoProvider
	processProvider   ProcessInfoProvider
	taskSpawner       TaskSpawner
	backoffProvider   BackoffInfoProvider
	quotaProvider     QuotaSnapshotProvider
	modelTierProvider ModelTierProvider
	sessionIDRecorder SessionIDRecorder
}

// WithCanceller sets the WorkerCanceller for the cancel_agent tool.
func WithCanceller(c WorkerCanceller) RegisterOption {
	return func(o *registerOpts) { o.canceller = c }
}

// WithWorktreeProvider enables worktree info in worker_status output.
func WithWorktreeProvider(p WorktreeInfoProvider) RegisterOption {
	return func(o *registerOpts) { o.worktreeProvider = p }
}

// WithProcessProvider enables process activity info in worker_status output.
func WithProcessProvider(p ProcessInfoProvider) RegisterOption {
	return func(o *registerOpts) { o.processProvider = p }
}

// WithTaskSpawner enables spawn-per-task: when a task is assigned, a fresh
// worker process is spawned for it immediately.
func WithTaskSpawner(s TaskSpawner) RegisterOption {
	return func(o *registerOpts) { o.taskSpawner = s }
}

// WithBackoffProvider enables rate-limit/backoff visibility in worker_status output.
func WithBackoffProvider(p BackoffInfoProvider) RegisterOption {
	return func(o *registerOpts) { o.backoffProvider = p }
}

// WithQuotaProvider enables cached quota preflight status in worker_status output.
func WithQuotaProvider(p QuotaSnapshotProvider) RegisterOption {
	return func(o *registerOpts) { o.quotaProvider = p }
}

// WithModelTierProvider shows configured model_tiers in worker_status output.
func WithModelTierProvider(p ModelTierProvider) RegisterOption {
	return func(o *registerOpts) { o.modelTierProvider = p }
}

// WithSessionIDRecorder enables session ID synchronization from heartbeat to WorkerManager.
func WithSessionIDRecorder(r SessionIDRecorder) RegisterOption {
	return func(o *registerOpts) { o.sessionIDRecorder = r }
}

// Register registers the collaboration tools, prompt templates,
// and piggyback middleware with the mcp-go server.
// orch is optional; when set, create_task from the driver will auto-assign to workers.
func Register(s *server.MCPServer, svc *app.CollabService, logger *log.Logger, registry *app.SessionRegistry, orch *app.TaskOrchestrator, opts ...RegisterOption) {
	var o registerOpts
	for _, opt := range opts {
		opt(&o)
	}

	// Messaging tools (2)
	registerSendMessage(s, svc, logger)
	registerReadMessages(s, svc, logger)

	// Task tools (4)
	registerCreateTask(s, svc, logger, orch, o.taskSpawner)
	registerListTasks(s, svc, logger)
	registerUpdateTask(s, svc, logger, orch, o.taskSpawner)
	registerReplayTask(s, svc, logger, o.taskSpawner)

	// Planning tools (3)
	registerCreatePlan(s, svc, logger)
	registerGetPlan(s, svc, logger)
	registerUpdatePlan(s, svc, logger)

	// Session tools (3)
	registerGetSessionContext(s, svc, logger, registry, o.processProvider)
	registerSetPresence(s, svc, logger, registry)
	registerAppendSessionNote(s, svc, logger)

	// Workflow tools (3)
	registerHandoff(s, svc, logger)
	registerClaimNext(s, svc, logger)
	registerRequestReview(s, svc, logger)

	// File lock tool (1)
	registerLockFile(s, svc, logger)

	// Agent registration tools (2)
	registerRegisterAgent(s, svc, logger)
	registerListAgents(s, svc, logger)

	// Driver/worker tools (4)
	registerWorkerStatus(s, svc, logger, o.worktreeProvider, o.processProvider, o.backoffProvider, o.quotaProvider, o.modelTierProvider)
	registerWorkerOutput(s, svc, logger, o.processProvider)
	registerHeartbeat(s, svc, logger, o.sessionIDRecorder)
	registerCancelAgent(s, svc, logger, o.canceller, o.processProvider)

	// Progress monitoring tools (1)
	registerReportProgress(s, svc, logger)

	// Work context tools (2)
	registerGetWorkContext(s, svc, logger)
	registerUpdateWorkContext(s, svc, logger)

	// Task template planner (1)
	registerTaskPlan(s, svc, logger)

	// Model selection guide for drivers (1)
	registerListModelOptions(s, svc, logger)

	// Prompt templates (pair-respond, code-review, plan-feature)
	registerPrompts(s)

	// Resources and resource templates (agent instructions, workflow guides)
	registerResources(s, svc, logger)
}
