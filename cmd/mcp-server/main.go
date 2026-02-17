// MCP Stringwork Server
// A local MCP server for pair programming with Cursor and Claude Code.
// Supports stdio (single-client) and HTTP (multi-client, SSE + Streamable HTTP).
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/jaakkos/stringwork/internal/app"
	"github.com/jaakkos/stringwork/internal/dashboard"
	"github.com/jaakkos/stringwork/internal/domain"
	"github.com/jaakkos/stringwork/internal/knowledge"
	"github.com/jaakkos/stringwork/internal/policy"
	"github.com/jaakkos/stringwork/internal/repository"
	"github.com/jaakkos/stringwork/internal/tools/collab"
	"github.com/jaakkos/stringwork/internal/worktree"
)

func main() {
	// Handle CLI subcommands before starting MCP server.
	if len(os.Args) > 1 && os.Args[1] == "status" {
		runStatusCommand()
		return
	}

	// Load config
	tmpLogger := log.New(os.Stderr, "[mcp-pair] ", log.LstdFlags|log.Lshortfile)
	cfg := loadConfig(tmpLogger)
	pol := policy.New(cfg)

	// Set up logging
	logger := setupLogger(pol.LogFile())
	logger.Println("Starting MCP Stringwork server...")
	logger.Printf("Log file: %s", pol.LogFile())
	logger.Printf("Workspace root: %s", cfg.WorkspaceRoot)
	logger.Printf("Transport: %s", cfg.Transport)

	// State repository
	repo, err := repository.NewStateRepository(pol.StateFile())
	if err != nil {
		logger.Fatalf("State repository: %v", err)
	}
	svc := app.NewCollabService(repo, pol, logger)

	// Refresh agent heartbeats on startup so the watchdog doesn't immediately
	// consider persisted agents as stale from a previous server run.
	if err := svc.Run(func(state *domain.CollabState) error {
		app.RefreshHeartbeatsOnStartup(state)
		return nil
	}); err != nil {
		logger.Printf("Warning: failed to refresh heartbeats on startup: %v", err)
	}

	// Session registry for multi-client agent tracking
	registry := app.NewSessionRegistry()

	// Session store for push notifications (holds actual ClientSession objects)
	sessions := newSessionStore()

	// Build the MCPServer
	hooks := &server.Hooks{}
	hooks.AddAfterCallTool(func(ctx context.Context, id any, message *mcp.CallToolRequest, result *mcp.CallToolResult) {
		// Log tool calls
		if message != nil {
			logger.Printf("Calling tool: %s", message.Params.Name)
		}
	})

	// Clean up session registry when clients disconnect.
	// Without this, auto-spawned agents (claude-code, codex) leave stale sessions
	// that prevent future auto-respond from firing.
	hooks.AddOnUnregisterSession(func(ctx context.Context, session server.ClientSession) {
		sid := session.SessionID()
		agent := registry.GetAgent(sid)
		registry.RemoveSession(sid)
		sessions.remove(sid)
		if agent != "" {
			logger.Printf("Client session unregistered: %s (agent=%s)", sid, agent)
		} else {
			logger.Printf("Client session unregistered: %s", sid)
		}
	})

	mcpServer := server.NewMCPServer(
		"mcp-stringwork",
		"1.0.0",
		server.WithInstructions(collab.InstructionsText()),
		server.WithToolHandlerMiddleware(collab.PiggybackMiddleware(svc, registry)),
		server.WithHooks(hooks),
		server.WithResourceCapabilities(false, true), // subscribe=false, listChanged=true
	)

	// Optional task orchestrator (when orchestration config is present)
	var taskOrch *app.TaskOrchestrator
	if o := pol.Orchestration(); o != nil {
		strategy := o.AssignmentStrategy
		if strategy == "" {
			strategy = "least_loaded"
		}
		taskOrch = app.NewTaskOrchestrator(svc, strategy)
	}

	// WorkerManager is created later; register tools after it's available.
	// Placeholder for tool registration — see below after WorkerManager setup.

	// Setup graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Ignore SIGHUP so the server keeps running when daemonized (nohup, launchd, etc.)
	signal.Ignore(syscall.SIGHUP)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		logger.Printf("Received signal %v, shutting down...", sig)
		cancel()
	}()

	// Build push function for the notifier: pushes to all connected sessions.
	pushFunc := func(method string, params any) error {
		agents := registry.ConnectedAgents()
		for _, agent := range agents {
			sid := registry.GetSessionForAgent(agent)
			if sid == "" {
				continue
			}
			session := sessions.get(sid)
			if session == nil {
				continue
			}
			if !session.Initialized() {
				continue
			}
			notification := mcp.JSONRPCNotification{
				JSONRPC: "2.0",
				Notification: mcp.Notification{
					Method: method,
					Params: mcp.NotificationParams{AdditionalFields: map[string]any{"params": params}},
				},
			}
			ch := session.NotificationChannel()
			select {
			case ch <- notification:
			default:
				logger.Printf("Notifier: push to %s dropped (channel full)", agent)
			}
		}
		return nil
	}

	// Build getAgent: returns the first connected agent (for notifier compatibility).
	getAgent := func() string {
		agents := registry.ConnectedAgents()
		if len(agents) > 0 {
			return agents[0]
		}
		return ""
	}

	// Start notifier with WorkerManager (orchestration-driven worker spawn)
	transport := strings.ToLower(cfg.Transport)
	var notifierOpts []app.NotifierOption
	var wm *app.WorkerManager // accessible for cancel_agent tool
	orchCfg := pol.Orchestration()
	if orchCfg != nil {
		wm = app.NewWorkerManager(orchCfg, getAgent, repo, svc.Run, cfg.WorkspaceRoot, logger)
		wm.SetSessionChecker(func(instanceOrType string) bool {
			return registry.HasActiveSession(instanceOrType)
		})
		// When running as HTTP daemon, inject MCP config into spawned workers so they connect without manual "claude mcp add-json".
		if transport == "http" || transport == "sse" {
			port := cfg.HTTPPort
			if port == 0 {
				port = 8943
			}
			wm.SetMCPServerURL(fmt.Sprintf("http://localhost:%d/mcp", port))
		}
		// Pass configured MCP servers to worker manager for auto-registration with worker CLIs.
		if mcpCfg := pol.MCPServers(); len(mcpCfg) > 0 {
			var entries []app.MCPServerEntry
			for name, sc := range mcpCfg {
				entries = append(entries, app.MCPServerEntry{
					Name:    name,
					URL:     sc.URL,
					Command: sc.Command,
					Args:    sc.Args,
					Env:     sc.Env,
				})
			}
			wm.SetMCPServers(entries)
			logger.Printf("WorkerManager: %d additional MCP server(s) configured for workers", len(entries))
		}
		notifierOpts = append(notifierOpts, app.WithWorkerManager(wm))
		logger.Printf("WorkerManager enabled: driver=%s, %d worker type(s)", orchCfg.Driver, len(orchCfg.Workers))
		wm.StartupCheck()
	}

	// Worktree manager (optional, git worktree isolation for workers)
	var wtManager *worktree.Manager
	if wtCfg := pol.WorktreeConfig(); wtCfg != nil && wtCfg.Enabled {
		wtManager = worktree.NewManager(wtCfg, logger)
		if wm != nil {
			wm.SetWorktreeManager(wtManager)
			logger.Printf("WorktreeManager enabled (cleanup=%s, path=%s)", wtCfg.CleanupStrategy, wtCfg.Path)
		}
	}

	// Knowledge indexer (optional, FTS5-based project knowledge)
	var knowledgeStore *knowledge.KnowledgeStore
	if kCfg := pol.KnowledgeConfig(); kCfg != nil && kCfg.Enabled {
		var err error
		knowledgeStore, err = knowledge.NewKnowledgeStore(pol.KnowledgeDBPath())
		if err != nil {
			logger.Printf("Warning: knowledge store init failed: %v (feature disabled)", err)
		} else {
			syncInterval := 60 * time.Second
			if kCfg.WatchIntervalSeconds > 0 {
				syncInterval = time.Duration(kCfg.WatchIntervalSeconds) * time.Second
			}
			indexer := knowledge.NewIndexer(knowledgeStore, knowledge.IndexerConfig{
				WorkspaceRoot:     cfg.WorkspaceRoot,
				IndexGoSource:     kCfg.IndexGoSource,
				WatchEnabled:      true,
				StateSyncInterval: syncInterval,
			}, newKnowledgeStateAdapter(svc), logger)
			go indexer.Start(ctx)
			logger.Printf("Knowledge indexer enabled (go_source=%v, sync=%s, db=%s)", kCfg.IndexGoSource, syncInterval, pol.KnowledgeDBPath())
		}
	}

	// Register all tools and prompts (after WorkerManager so cancel_agent can kill processes)
	var regOpts []collab.RegisterOption
	if wm != nil {
		regOpts = append(regOpts, collab.WithCanceller(wm))
	}
	if knowledgeStore != nil {
		regOpts = append(regOpts, collab.WithKnowledgeStore(knowledgeStore))
	}
	if wtManager != nil {
		regOpts = append(regOpts, collab.WithWorktreeProvider(&worktreeAdapter{mgr: wtManager}))
	}
	if wm != nil {
		regOpts = append(regOpts, collab.WithProcessProvider(&processAdapter{wm: wm}))
	}
	collab.Register(mcpServer, svc, logger, registry, taskOrch, regOpts...)

	notifier := app.NewNotifier(pol.SignalFilePath(), repo, getAgent, pushFunc, logger, notifierOpts...)
	svc.SetNotifier(notifier)
	go notifier.Start(ctx)

	// Start watchdog to monitor agent liveness and recover stuck states
	watchdog := app.NewWatchdog(svc, registry, logger,
		app.WithWatchdogNotifier(notifier),
	)
	go watchdog.Start(ctx)

	// Run transport
	switch transport {
	case "http", "sse":
		runHTTPServer(ctx, cancel, mcpServer, cfg, logger, registry, sessions, hooks, svc, wm)
	default: // "stdio" or empty
		runStdioServer(ctx, mcpServer, logger, registry, sessions, hooks)
	}

	watchdog.Stop()
	notifier.Stop()

	// Cleanup worktrees on shutdown
	if wtManager != nil {
		if err := wtManager.CleanupAll(cfg.WorkspaceRoot); err != nil {
			logger.Printf("Warning: worktree cleanup on shutdown: %v", err)
		}
	}

	if knowledgeStore != nil {
		if err := knowledgeStore.Close(); err != nil {
			logger.Printf("Warning: close knowledge store: %v", err)
		}
	}

	if c, ok := repo.(interface{ Close() error }); ok {
		if err := c.Close(); err != nil {
			logger.Printf("Warning: close state repository: %v", err)
		}
	}

	logger.Println("Server stopped")
}

// runStdioServer runs the MCP server over stdin/stdout (single-client).
func runStdioServer(ctx context.Context, mcpServer *server.MCPServer, logger *log.Logger, registry *app.SessionRegistry, sessions *sessionStore, hooks *server.Hooks) {
	logger.Println("Running in stdio mode")

	// For stdio, register/unregister session hooks
	hooks.AddBeforeInitialize(func(ctx context.Context, id any, message *mcp.InitializeRequest) {
		if session := server.ClientSessionFromContext(ctx); session != nil {
			sessions.set(session.SessionID(), session)
			logger.Printf("Client session registered: %s", session.SessionID())
		}
		if message != nil {
			ci := message.Params.ClientInfo
			logger.Printf("Client: %s %s, Protocol: %s", ci.Name, ci.Version, message.Params.ProtocolVersion)
		}
	})

	stdioSrv := server.NewStdioServer(mcpServer)
	if err := stdioSrv.Listen(ctx, os.Stdin, os.Stdout); err != nil {
		logger.Printf("Stdio server error: %v", err)
	}
}

// runHTTPServer runs the MCP server as a persistent HTTP daemon serving both
// SSE (/sse) and Streamable HTTP (/mcp) on the same port.
func runHTTPServer(ctx context.Context, cancel context.CancelFunc, mcpServer *server.MCPServer, cfg *policy.Config, logger *log.Logger, registry *app.SessionRegistry, sessions *sessionStore, hooks *server.Hooks, svc *app.CollabService, wm *app.WorkerManager) {
	port := cfg.HTTPPort
	if port == 0 {
		port = 8943
	}
	addr := fmt.Sprintf(":%d", port)
	baseURL := fmt.Sprintf("http://localhost:%d", port)

	logger.Printf("Running in HTTP mode on %s", addr)
	logger.Printf("  SSE endpoint:            %s/sse", baseURL)
	logger.Printf("  Streamable HTTP endpoint: %s/mcp", baseURL)

	// Session lifecycle hooks
	hooks.AddBeforeInitialize(func(ctx context.Context, id any, message *mcp.InitializeRequest) {
		if session := server.ClientSessionFromContext(ctx); session != nil {
			sessions.set(session.SessionID(), session)
			logger.Printf("Client session registered: %s", session.SessionID())
		}
		if message != nil {
			ci := message.Params.ClientInfo
			logger.Printf("Client: %s %s, Protocol: %s", ci.Name, ci.Version, message.Params.ProtocolVersion)

			// Send ack message when a non-cursor agent connects (e.g. claude-code, codex via auto-respond)
			agent := collab.AgentNameForClient(ci.Name)
			if agent != "cursor" && agent != "" {
				clientName, clientVersion := ci.Name, ci.Version
				go func() {
					_ = svc.Run(func(state *domain.CollabState) error {
						// Find who sent the most recent unread message to this agent
						recipient := ""
						for i := len(state.Messages) - 1; i >= 0; i-- {
							m := state.Messages[i]
							if (m.To == agent || m.To == "all") && !m.Read && m.From != "system" {
								recipient = m.From
								break
							}
						}
						if recipient == "" {
							recipient = "cursor" // default
						}
						state.Messages = append(state.Messages, domain.Message{
							ID:        state.NextMsgID,
							From:      "system",
							To:        recipient,
							Content:   fmt.Sprintf("✅ **%s** connected and working (%s %s)", agent, clientName, clientVersion),
							Timestamp: time.Now(),
						})
						state.NextMsgID++
						return nil
					})
				}()
			}
		}
	})

	// SSE server (for Cursor and other SSE-capable clients)
	sseSrv := server.NewSSEServer(mcpServer,
		server.WithBaseURL(baseURL),
	)

	// Streamable HTTP server (for Claude Code and other HTTP clients)
	streamSrv := server.NewStreamableHTTPServer(mcpServer)

	// Mock OAuth 2.1 endpoints (so Claude Code's auth flow succeeds without real credentials)
	mockAuth := newMockAuthServer(baseURL, logger)

	// Combined HTTP mux
	mux := http.NewServeMux()
	mux.Handle("/sse", sseSrv)
	mux.Handle("/sse/", sseSrv)
	mux.Handle("/message", sseSrv) // SSE message endpoint
	mux.Handle("/mcp", streamSrv)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","agents":%d}`, registry.AgentCount())
	})

	// Register mock OAuth routes (/.well-known/oauth-authorization-server, /register, /authorize, /token)
	mockAuth.registerRoutes(mux)

	// Dashboard UI and API
	var dashOpts []dashboard.HandlerOption
	if wm != nil {
		dashOpts = append(dashOpts, dashboard.WithWorkerController(wm))
	}
	dash := dashboard.NewHandler(svc, registry, dashOpts...)
	dash.RegisterRoutes(mux)
	logger.Printf("  Dashboard:               %s/dashboard", baseURL)

	httpServer := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	// Start HTTP server in background
	go func() {
		if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
			logger.Fatalf("HTTP server error: %v", err)
		}
	}()

	// Wait for shutdown signal
	<-ctx.Done()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*1e9) // 5 seconds
	defer shutdownCancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Printf("HTTP shutdown error: %v", err)
	}
}

// sessionStore holds active ClientSession objects for push notifications.
type sessionStore struct {
	mu   sync.RWMutex
	data map[string]server.ClientSession
}

func newSessionStore() *sessionStore {
	return &sessionStore{data: make(map[string]server.ClientSession)}
}

func (ss *sessionStore) set(id string, s server.ClientSession) {
	ss.mu.Lock()
	ss.data[id] = s
	ss.mu.Unlock()
}

func (ss *sessionStore) get(id string) server.ClientSession {
	ss.mu.RLock()
	defer ss.mu.RUnlock()
	return ss.data[id]
}

func (ss *sessionStore) remove(id string) {
	ss.mu.Lock()
	delete(ss.data, id)
	ss.mu.Unlock()
}

// setupLogger creates a logger that writes to a log file and optionally stderr.
// When stderr is a terminal (interactive use), logs go to both stderr and the file.
// When stderr is redirected (daemon mode via nohup), logs go only to the file
// to avoid duplicate lines since nohup already redirects stderr to the log file.
func setupLogger(logFilePath string) *log.Logger {
	var writers []io.Writer

	// Only include stderr when it's an interactive terminal (not redirected).
	// This prevents duplicate log lines when running as a daemon with nohup >> log 2>&1.
	stderrIsTerminal := false
	if info, err := os.Stderr.Stat(); err == nil {
		stderrIsTerminal = (info.Mode() & os.ModeCharDevice) != 0
	}

	hasLogFile := false
	lower := strings.ToLower(logFilePath)
	if lower != "none" && lower != "off" && logFilePath != "" {
		if err := os.MkdirAll(filepath.Dir(logFilePath), 0o755); err == nil {
			f, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
			if err == nil {
				writers = append(writers, f)
				hasLogFile = true
			} else {
				fmt.Fprintf(os.Stderr, "[mcp-pair] Warning: cannot open log file %s: %v\n", logFilePath, err)
			}
		} else {
			fmt.Fprintf(os.Stderr, "[mcp-pair] Warning: cannot create log dir %s: %v\n", filepath.Dir(logFilePath), err)
		}
	}

	// Add stderr if it's a terminal, or if there's no log file (always need at least one output).
	if stderrIsTerminal || !hasLogFile {
		writers = append(writers, os.Stderr)
	}

	return log.New(io.MultiWriter(writers...), "[mcp-pair] ", log.LstdFlags|log.Lshortfile)
}

// loadConfig loads policy configuration from MCP_CONFIG or defaults.
func loadConfig(logger *log.Logger) *policy.Config {
	cfg := policy.DefaultConfig()
	if configPath := os.Getenv("MCP_CONFIG"); configPath != "" {
		var err error
		cfg, err = policy.LoadConfig(configPath)
		if err != nil {
			logger.Printf("Warning: failed to load config %s: %v, using defaults", configPath, err)
			cfg = policy.DefaultConfig()
		}
	}
	if cfg.WorkspaceRoot == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to get working directory: %v\n", err)
			os.Exit(1)
		}
		cfg.WorkspaceRoot = cwd
	}
	return cfg
}

// processAdapter bridges WorkerManager to the collab.ProcessInfoProvider interface.
type processAdapter struct {
	wm *app.WorkerManager
}

func (a *processAdapter) GetProcessInfo() map[string]collab.ProcessInfoSnapshot {
	procs := a.wm.GetProcessInfo()
	result := make(map[string]collab.ProcessInfoSnapshot, len(procs))
	for id, p := range procs {
		result[id] = collab.ProcessInfoSnapshot{
			StartedAt:    p.StartedAt,
			LastOutputAt: p.LastOutputAt,
			OutputBytes:  p.OutputBytes,
			WorkspaceDir: p.WorkspaceDir,
		}
	}
	return result
}

// worktreeAdapter bridges worktree.Manager to the collab.WorktreeInfoProvider interface.
type worktreeAdapter struct {
	mgr *worktree.Manager
}

func (a *worktreeAdapter) ListWorktrees() map[string]collab.WorktreeInfo {
	wts := a.mgr.ListWorktrees()
	result := make(map[string]collab.WorktreeInfo, len(wts))
	for id, wt := range wts {
		result[id] = collab.WorktreeInfo{
			Path:       wt.Path,
			Branch:     wt.Branch,
			BaseBranch: wt.BaseBranch,
		}
	}
	return result
}

// knowledgeStateAdapter bridges CollabService to the knowledge.StateProvider interface.
type knowledgeStateAdapter struct {
	svc *app.CollabService
}

func newKnowledgeStateAdapter(svc *app.CollabService) *knowledgeStateAdapter {
	return &knowledgeStateAdapter{svc: svc}
}

func (a *knowledgeStateAdapter) SessionNotes() []knowledge.SessionNoteData {
	var notes []knowledge.SessionNoteData
	_ = a.svc.Query(func(state *domain.CollabState) error {
		for _, n := range state.SessionNotes {
			notes = append(notes, knowledge.SessionNoteData{
				ID:       n.ID,
				Author:   n.Author,
				Content:  n.Content,
				Category: n.Category,
			})
		}
		return nil
	})
	return notes
}

func (a *knowledgeStateAdapter) CompletedTasks() []knowledge.TaskData {
	var tasks []knowledge.TaskData
	_ = a.svc.Query(func(state *domain.CollabState) error {
		for _, t := range state.Tasks {
			if t.Status == "completed" {
				tasks = append(tasks, knowledge.TaskData{
					ID:            t.ID,
					Title:         t.Title,
					Description:   t.Description,
					AssignedTo:    t.AssignedTo,
					ResultSummary: t.ResultSummary,
				})
			}
		}
		return nil
	})
	return tasks
}

// runStatusCommand implements "mcp-stringwork status [agent]".
func runStatusCommand() {
	agent := "claude-code"
	if len(os.Args) > 2 {
		agent = os.Args[2]
	}

	logger := log.New(os.Stderr, "", 0)
	cfg := loadConfig(logger)
	pol := policy.New(cfg)

	repo, err := repository.NewStateRepository(pol.StateFile())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		if c, ok := repo.(interface{ Close() error }); ok {
			_ = c.Close()
		}
	}()

	state, err := repo.Load()
	if err != nil {
		state = domain.NewCollabState()
	}

	unread := 0
	for _, msg := range state.Messages {
		if (msg.To == agent || msg.To == "all") && !msg.Read {
			unread++
		}
	}

	pending := 0
	for _, task := range state.Tasks {
		if (task.AssignedTo == agent || task.AssignedTo == "any") && task.Status == "pending" {
			pending++
		}
	}

	fmt.Printf("unread=%d pending=%d\n", unread, pending)
}
