package collab

import (
	"log"
	"time"

	"github.com/mark3labs/mcp-go/server"

	"github.com/jaakkos/stringwork/internal/app"
	"github.com/jaakkos/stringwork/internal/knowledge"
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

// ProcessInfoProvider can return process activity information for running workers.
type ProcessInfoProvider interface {
	GetProcessInfo() map[string]ProcessInfoSnapshot
}

// ProcessInfoSnapshot is a snapshot of a single worker process's activity.
type ProcessInfoSnapshot struct {
	StartedAt    time.Time `json:"started_at"`
	LastOutputAt time.Time `json:"last_output_at"`
	OutputBytes  int64     `json:"output_bytes"`
	WorkspaceDir string    `json:"workspace_dir"`
}

type registerOpts struct {
	canceller        WorkerCanceller
	knowledgeStore   *knowledge.KnowledgeStore
	worktreeProvider WorktreeInfoProvider
	processProvider  ProcessInfoProvider
}

// WithCanceller sets the WorkerCanceller for the cancel_agent tool.
func WithCanceller(c WorkerCanceller) RegisterOption {
	return func(o *registerOpts) { o.canceller = c }
}

// WithKnowledgeStore enables the query_knowledge tool.
func WithKnowledgeStore(ks *knowledge.KnowledgeStore) RegisterOption {
	return func(o *registerOpts) { o.knowledgeStore = ks }
}

// WithWorktreeProvider enables worktree info in worker_status output.
func WithWorktreeProvider(p WorktreeInfoProvider) RegisterOption {
	return func(o *registerOpts) { o.worktreeProvider = p }
}

// WithProcessProvider enables process activity info in worker_status output.
func WithProcessProvider(p ProcessInfoProvider) RegisterOption {
	return func(o *registerOpts) { o.processProvider = p }
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

	// Task tools (3)
	registerCreateTask(s, svc, logger, orch)
	registerListTasks(s, svc, logger)
	registerUpdateTask(s, svc, logger)

	// Planning tools (3)
	registerCreatePlan(s, svc, logger)
	registerGetPlan(s, svc, logger)
	registerUpdatePlan(s, svc, logger)

	// Session tools (3)
	registerGetSessionContext(s, svc, logger, registry)
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

	// Driver/worker tools (3)
	registerWorkerStatus(s, svc, logger, o.worktreeProvider, o.processProvider)
	registerHeartbeat(s, svc, logger)
	registerCancelAgent(s, svc, logger, o.canceller)

	// Progress monitoring tools (1)
	registerReportProgress(s, svc, logger)

	// Work context tools (2)
	registerGetWorkContext(s, svc, logger)
	registerUpdateWorkContext(s, svc, logger)

	// Knowledge tool (1, optional)
	if o.knowledgeStore != nil {
		registerQueryKnowledge(s, o.knowledgeStore, logger)
	}

	// Prompt templates (pair-respond, code-review, plan-feature)
	registerPrompts(s)

	// Resources and resource templates (agent instructions, workflow guides)
	registerResources(s, svc, logger)
}
