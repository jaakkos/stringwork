// MCP Stringwork Server
// Supports three modes: standalone (stdio+HTTP), daemon (HTTP only), and proxy (stdio bridge).
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
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

// Version is set by -ldflags at build time.
var Version = "dev"

// serverBundle holds all initialized server components.
type serverBundle struct {
	mcpServer *server.MCPServer
	cfg       *policy.Config
	pol       *policy.Policy
	logger    *log.Logger
	registry  *app.SessionRegistry
	sessions  *sessionStore
	hooks     *server.Hooks
	svc       *app.CollabService
	wm        *app.WorkerManager
	notifier  *app.Notifier
	watchdog  *app.Watchdog
	cleanup   func()
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "status":
			runStatusCommand()
			return
		case "--version", "-v", "version":
			fmt.Println("mcp-stringwork " + Version)
			return
		}
	}

	daemonFlag := hasFlag("--daemon")
	standaloneFlag := hasFlag("--standalone")

	tmpLogger := log.New(os.Stderr, "[mcp-pair] ", log.LstdFlags|log.Lshortfile)
	cfg := loadConfig(tmpLogger)
	pol := policy.New(cfg)

	if daemonFlag {
		bundle := initializeServer(cfg, pol)
		runDaemon(bundle)
		return
	}

	if standaloneFlag {
		bundle := initializeServer(cfg, pol)
		runStandalone(bundle)
		return
	}

	socketPath := pol.SocketPath()
	pidFile := pol.PIDFile()
	proxyLogger := setupLogger(pol.LogFile())

	// Always connect to an existing daemon if one is running.
	// Only auto-start a new daemon when daemon mode is enabled in config.
	if isDaemonRunning(socketPath) {
		proxyLogger.Println("Daemon already running, connecting as proxy")
	} else if pol.DaemonEnabled() {
		proxyLogger.Println("No daemon found, starting one...")
		if err := startDaemonProcess(socketPath, pidFile, proxyLogger); err != nil {
			proxyLogger.Printf("Failed to start daemon: %v, falling back to standalone", err)
			bundle := initializeServer(cfg, pol)
			runStandalone(bundle)
			return
		}
	} else {
		bundle := initializeServer(cfg, pol)
		runStandalone(bundle)
		return
	}

	if err := runProxy(socketPath, proxyLogger); err != nil {
		proxyLogger.Printf("Proxy error: %v", err)
		os.Exit(1)
	}
}

// hasFlag checks if a flag is present in os.Args and removes it.
func hasFlag(flag string) bool {
	for i, arg := range os.Args {
		if arg == flag {
			os.Args = append(os.Args[:i], os.Args[i+1:]...)
			return true
		}
	}
	return false
}

// initializeServer creates all server components: MCPServer, services, hooks,
// tools, notifier, watchdog, and background goroutines. The returned bundle
// is ready to be wired to a transport (stdio, HTTP, or both).
func initializeServer(cfg *policy.Config, pol *policy.Policy) *serverBundle {
	logger := setupLogger(pol.LogFile())
	logger.Println("Starting MCP Stringwork server...")
	logger.Printf("Log file: %s", pol.LogFile())
	logger.Printf("Workspace root: %s", cfg.WorkspaceRoot)

	repo, err := repository.NewStateRepository(pol.StateFile())
	if err != nil {
		logger.Fatalf("State repository: %v", err)
	}
	svc := app.NewCollabService(repo, pol, logger)

	if err := svc.Run(func(state *domain.CollabState) error {
		app.RefreshHeartbeatsOnStartup(state)
		return nil
	}); err != nil {
		logger.Printf("Warning: failed to refresh heartbeats on startup: %v", err)
	}

	registry := app.NewSessionRegistry()
	sessions := newSessionStore()

	hooks := &server.Hooks{}
	hooks.AddAfterCallTool(func(ctx context.Context, id any, message *mcp.CallToolRequest, result any) {
		if message != nil {
			logger.Printf("Calling tool: %s", message.Params.Name)
		}
	})

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

	hooks.AddBeforeInitialize(func(ctx context.Context, id any, message *mcp.InitializeRequest) {
		if session := server.ClientSessionFromContext(ctx); session != nil {
			sessions.set(session.SessionID(), session)
			logger.Printf("Client session registered: %s", session.SessionID())
		}
		if message != nil {
			ci := message.Params.ClientInfo
			logger.Printf("Client: %s %s, Protocol: %s", ci.Name, ci.Version, message.Params.ProtocolVersion)

			agent := collab.AgentNameForClient(ci.Name)
			if agent != "cursor" && agent != "" {
				clientName, clientVersion := ci.Name, ci.Version
				go func() {
					_ = svc.Run(func(state *domain.CollabState) error {
						recipient := ""
						for i := len(state.Messages) - 1; i >= 0; i-- {
							m := state.Messages[i]
							if (m.To == agent || m.To == "all") && !m.Read && m.From != "system" {
								recipient = m.From
								break
							}
						}
						if recipient == "" {
							recipient = "cursor"
						}
						state.Messages = append(state.Messages, domain.Message{
							ID:        state.NextMsgID,
							From:      "system",
							To:        recipient,
							Content:   fmt.Sprintf("**%s** connected (%s %s)", agent, clientName, clientVersion),
							Timestamp: time.Now(),
						})
						state.NextMsgID++
						return nil
					})
				}()
			}
		}
	})

	mcpServer := server.NewMCPServer(
		"mcp-stringwork",
		Version,
		server.WithInstructions(collab.InstructionsText()),
		server.WithToolHandlerMiddleware(collab.PiggybackMiddleware(svc, registry)),
		server.WithHooks(hooks),
		server.WithResourceCapabilities(false, true),
	)

	var taskOrch *app.TaskOrchestrator
	if o := pol.Orchestration(); o != nil {
		strategy := o.AssignmentStrategy
		if strategy == "" {
			strategy = "least_loaded"
		}
		taskOrch = app.NewTaskOrchestrator(svc, strategy)
	}

	ctx, cancel := context.WithCancel(context.Background())
	signal.Ignore(syscall.SIGHUP)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		logger.Printf("Received signal %v, shutting down...", sig)
		cancel()
	}()

	pushFunc := func(method string, params any) error {
		agents := registry.ConnectedAgents()
		for _, agent := range agents {
			sid := registry.GetSessionForAgent(agent)
			if sid == "" {
				continue
			}
			session := sessions.get(sid)
			if session == nil || !session.Initialized() {
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

	getAgent := func() string {
		agents := registry.ConnectedAgents()
		if len(agents) > 0 {
			return agents[0]
		}
		return ""
	}

	var notifierOpts []app.NotifierOption
	var wm *app.WorkerManager
	orchCfg := pol.Orchestration()
	if orchCfg != nil {
		wm = app.NewWorkerManager(orchCfg, getAgent, repo, svc.Run, cfg.WorkspaceRoot, logger)
		wm.SetSessionChecker(func(instanceOrType string) bool {
			return registry.HasActiveSession(instanceOrType)
		})
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

	var wtManager *worktree.Manager
	if wtCfg := pol.WorktreeConfig(); wtCfg != nil && wtCfg.Enabled {
		wtManager = worktree.NewManager(wtCfg, logger)
		if wm != nil {
			wm.SetWorktreeManager(wtManager)
			logger.Printf("WorktreeManager enabled (cleanup=%s, path=%s)", wtCfg.CleanupStrategy, wtCfg.Path)
		}
	}

	var knowledgeStore *knowledge.KnowledgeStore
	if kCfg := pol.KnowledgeConfig(); kCfg != nil && kCfg.Enabled {
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

	watchdog := app.NewWatchdog(svc, registry, logger,
		app.WithWatchdogNotifier(notifier),
	)
	go watchdog.Start(ctx)

	cleanupFunc := func() {
		cancel()
		watchdog.Stop()
		notifier.Stop()
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
	}

	return &serverBundle{
		mcpServer: mcpServer,
		cfg:       cfg,
		pol:       pol,
		logger:    logger,
		registry:  registry,
		sessions:  sessions,
		hooks:     hooks,
		svc:       svc,
		wm:        wm,
		notifier:  notifier,
		watchdog:  watchdog,
		cleanup:   cleanupFunc,
	}
}

// buildHTTPHandler creates the HTTP handler with all routes (MCP, SSE, dashboard, health, auth).
func buildHTTPHandler(bundle *serverBundle, baseURL string, port int) http.Handler {
	sseSrv := server.NewSSEServer(bundle.mcpServer, server.WithBaseURL(baseURL))
	streamSrv := server.NewStreamableHTTPServer(bundle.mcpServer)
	mockAuth := newMockAuthServer(baseURL, bundle.logger)

	mux := http.NewServeMux()
	mux.Handle("/sse", sseSrv)
	mux.Handle("/sse/", sseSrv)
	mux.Handle("/message", sseSrv)
	mux.Handle("/mcp", streamSrv)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","port":%d,"agents":%d}`, port, bundle.registry.AgentCount())
	})
	mockAuth.registerRoutes(mux)

	var dashOpts []dashboard.HandlerOption
	if bundle.wm != nil {
		dashOpts = append(dashOpts, dashboard.WithWorkerController(bundle.wm))
	}
	dash := dashboard.NewHandler(bundle.svc, bundle.registry, dashOpts...)
	dash.RegisterRoutes(mux)

	return mux
}

// setupAndServeHTTP binds a TCP listener, configures service URLs, builds the
// handler, and starts serving. Returns the base URL and a shutdown function.
func setupAndServeHTTP(bundle *serverBundle) (baseURL string, handler http.Handler, shutdown func()) {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", bundle.cfg.HTTPPort))
	if err != nil {
		bundle.logger.Fatalf("HTTP listen: %v", err)
	}
	actualPort := ln.Addr().(*net.TCPAddr).Port
	baseURL = fmt.Sprintf("http://localhost:%d", actualPort)

	bundle.registry.SetDashboardURL(fmt.Sprintf("%s/dashboard", baseURL))
	if bundle.wm != nil {
		bundle.wm.SetMCPServerURL(fmt.Sprintf("%s/mcp", baseURL))
		bundle.wm.RefreshMCPRegistrations()
	}

	bundle.logger.Printf("HTTP server on :%d", actualPort)
	bundle.logger.Printf("  Workers connect at:      %s/mcp", baseURL)
	bundle.logger.Printf("  Dashboard:               %s/dashboard", baseURL)

	handler = buildHTTPHandler(bundle, baseURL, actualPort)
	httpServer := &http.Server{Handler: handler}

	go func() {
		if err := httpServer.Serve(ln); err != http.ErrServerClosed {
			bundle.logger.Fatalf("HTTP server error: %v", err)
		}
	}()

	return baseURL, handler, func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			bundle.logger.Printf("HTTP shutdown error: %v", err)
		}
	}
}

// runStandalone runs in legacy single-process mode: stdio for the driver, HTTP for workers.
func runStandalone(bundle *serverBundle) {
	_, _, httpShutdown := setupAndServeHTTP(bundle)

	bundle.logger.Println("Stdio ready (driver connection)")
	stdioSrv := server.NewStdioServer(bundle.mcpServer)
	if err := stdioSrv.Listen(context.Background(), os.Stdin, os.Stdout); err != nil {
		bundle.logger.Printf("Stdio server stopped: %v", err)
	}

	httpShutdown()
	bundle.cleanup()
	bundle.logger.Println("Server stopped")
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
func setupLogger(logFilePath string) *log.Logger {
	var writers []io.Writer

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
