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
	"github.com/jaakkos/stringwork/internal/policy"
	"github.com/jaakkos/stringwork/internal/repository"
	"github.com/jaakkos/stringwork/internal/repository/sqlite"
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
	ctx       context.Context // cancelled on SIGINT/SIGTERM

	// onMCPSessionOpen / onMCPSessionClose are optional callbacks invoked by
	// the MCP BeforeInitialize / OnUnregisterSession hooks. The daemon wires
	// these to the driverTracker so a TCP-direct MCP client (no unix proxy)
	// still counts as an active driver. Set before the HTTP listener starts
	// accepting connections.
	onMCPSessionOpen  func()
	onMCPSessionClose func()
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "status":
			runStatusCommand()
			return
		case "audit":
			runAuditCommand()
			return
		case "discover":
			runDiscoverCommand()
			return
		case "restart":
			runRestartCommand()
			return
		case "admin":
			runAdminCommand(os.Args[2:])
			return
		case "constitution":
			runConstitutionCommand(os.Args[2:])
			return
		case "--version", "-v", "version":
			fmt.Println("mcp-stringwork " + Version)
			return
		}

		// CLI worker communication subcommands (heartbeat, progress, send, task, etc.)
		if dispatchCLICommand(os.Args) {
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

	// Pre-open inspection MUST happen before NewStateRepository: sql.Open
	// creates the file on first connection, so the "did this exist?" signal
	// is permanently lost the moment we open. Backups are taken between
	// inspection and open so the file we copy is the unmodified prior DB.
	statePath := pol.StateFile()
	initState := sqlite.InspectState(statePath)
	sqlite.RotateBackups(statePath, sqlite.BackupOptions{
		Enabled: pol.BackupEnabled(),
		KeepN:   pol.BackupKeepN(),
	}, logger)

	repo, err := repository.NewStateRepository(statePath)
	if err != nil {
		logger.Fatalf("State repository: %v", err)
	}

	if initState.Fresh {
		logger.Printf("WARNING: no existing state.sqlite at %s — initializing a fresh database", initState.Path)
		logger.Printf("  All prior tasks, messages, agents, plans, and notes are gone.")
		if len(initState.Backups) > 0 {
			logger.Printf("  Found %d nearby backup(s) you can restore from:", len(initState.Backups))
			for _, b := range initState.Backups {
				logger.Printf("    %s  (%d bytes, %s)", b.Path, b.Size, b.ModTime.Format(time.RFC3339))
			}
			logger.Printf("  To restore: stop the server, `cp <chosen-backup> %s`, then restart.", initState.Path)
		} else {
			logger.Printf("  No backup files found in %s — this is normal for a first install.", filepath.Dir(initState.Path))
		}
	}

	svc := app.NewCollabService(repo, pol, logger)

	var auditWriter app.AuditWriter
	if pol.AuditEnabled() {
		if aw, ok := repo.(app.AuditWriter); ok {
			auditWriter = aw
			app.PruneAuditEntries(auditWriter, logger, pol.AuditRetentionDays())
			logger.Printf("Audit logging enabled (args_max_len=%d, retention=%dd)", pol.AuditArgsMaxLen(), pol.AuditRetentionDays())
		}
	} else {
		logger.Println("Audit logging disabled via config")
	}

	if err := svc.Run(func(state *domain.CollabState) error {
		// DaemonStartedAt is the driver-side fallback for the
		// STOP-banner spawn cutoff. Set it immediately on boot so
		// the cursor driver (which never gets an AgentInstance row)
		// never sees STOP banners triggered by tasks that were
		// cancelled before this daemon process started. Per-process
		// state by design — fresh daemon = fresh cutoff.
		state.DaemonStartedAt = time.Now()
		app.RefreshHeartbeatsOnStartup(state)
		report := app.MigrateTaskBoundCorruption(state)
		if report.Total() > 0 {
			logger.Printf("Startup migration repaired %d task-bound row(s): %d task assignees, %d instance types, %d registered agents",
				report.Total(), report.TasksReassigned, report.InstancesRetyped, report.RegisteredAgentsGone)
			for _, m := range report.Mutations {
				logger.Printf("  migrated: %s", m)
			}
		}
		return nil
	}); err != nil {
		logger.Printf("Warning: failed to refresh heartbeats on startup: %v", err)
	}

	registry := app.NewSessionRegistry()
	sessions := newSessionStore()

	// Declare the bundle up front so the MCP lifecycle hooks below can
	// close over its pointer and observe callbacks that are set later
	// (by runDaemon / runHTTP / runStdio) — e.g. the daemon's
	// driverTracker callbacks, wired before the HTTP listener accepts.
	bundle := &serverBundle{}

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
		if cb := bundle.onMCPSessionClose; cb != nil {
			cb()
		}
	})

	hooks.AddBeforeInitialize(func(ctx context.Context, id any, message *mcp.InitializeRequest) {
		if session := server.ClientSessionFromContext(ctx); session != nil {
			sessions.set(session.SessionID(), session)
			logger.Printf("Client session registered: %s", session.SessionID())
		}
		if cb := bundle.onMCPSessionOpen; cb != nil {
			cb()
		}
		if message != nil {
			ci := message.Params.ClientInfo
			logger.Printf("Client: %s %s, Protocol: %s", ci.Name, ci.Version, message.Params.ProtocolVersion)

			agent := collab.AgentNameForClient(ci.Name)
			configuredDriver := "cursor"
			if o := pol.Orchestration(); o != nil && o.Driver != "" {
				configuredDriver = o.Driver
			}
			if agent != configuredDriver && agent != "" {
				clientName, clientVersion := ci.Name, ci.Version
				driverFallback := configuredDriver
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
							recipient = driverFallback
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

	livenessAdapter := &workerLivenessAdapter{}
	mcpServer := server.NewMCPServer(
		"mcp-stringwork",
		Version,
		server.WithInstructions(collab.InstructionsText()),
		server.WithToolHandlerMiddleware(func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
			audit := collab.AuditMiddleware(auditWriter, registry, pol.AuditArgsMaxLen())
			piggy := collab.PiggybackMiddlewareWithLiveness(svc, registry, livenessAdapter)
			return audit(piggy(next))
		}),
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
		// BackoffChecker is wired below after WorkerManager is created.
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

	stateLoader := func() (*domain.CollabState, error) {
		var state *domain.CollabState
		err := svc.Query(func(s *domain.CollabState) error {
			state = s
			return nil
		})
		return state, err
	}

	var notifierOpts []app.NotifierOption
	var wm *app.WorkerManager
	orchCfg := pol.Orchestration()
	if orchCfg != nil {
		collab.SetDriverID(orchCfg.Driver)
		wm = app.NewWorkerManager(orchCfg, getAgent, stateLoader, svc.Run, cfg.WorkspaceRoot, logger)
		wm.SetSessionChecker(func(instanceOrType string) bool {
			return registry.HasActiveSession(instanceOrType)
		})
		// Route CLI-mode workers to THIS daemon's socket (not whatever the
		// global default happens to be on the machine). Without this, a
		// developer running two daemons — e.g. their personal one plus a
		// test daemon — would have the test's workers accidentally dial
		// back into the personal daemon and get "unknown agent" errors.
		wm.SetSocketPath(pol.SocketPath())
		// Bind constitution discovery to the live policy so worker spawn
		// prompts pick up rule changes (and config reloads) on the next
		// task without restarting the daemon.
		wm.SetConstitutionSources(pol.ConstitutionSources)
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
		livenessAdapter.wm = wm
		if taskOrch != nil {
			taskOrch.SetBackoffChecker(wm)
			// Fix A: hand the orchestrator the configured worker types so it
			// can fall back when no live AgentInstance row matches a new task
			// (typical: empty live pool right after server start, before any
			// worker has heartbeat). Without this, AssignTask returns "" and
			// create_task silently skips SpawnForTask — tasks orphan.
			taskOrch.SetKnownTypesProvider(wm)
			taskOrch.SetWorktreeForAssignedTask(func(state *domain.CollabState, task *domain.Task, inst *domain.AgentInstance) {
				for _, w := range orchCfg.Workers {
					if w.Type == inst.AgentType && w.UseClaudeWorktree {
						app.EnsureWorkContextWorktree(state, task, inst.InstanceID)
						break
					}
				}
			})
		}
		logger.Printf("WorkerManager enabled: driver=%s, %d worker type(s)", orchCfg.Driver, len(orchCfg.Workers))
		wm.Preflight()
	}

	var wtManager *worktree.Manager
	if wtCfg := pol.WorktreeConfig(); wtCfg != nil && wtCfg.Enabled {
		wtManager = worktree.NewManager(wtCfg, logger)
		if wm != nil {
			wm.SetWorktreeManager(wtManager)
			logger.Printf("WorktreeManager enabled (cleanup=%s, path=%s)", wtCfg.CleanupStrategy, wtCfg.Path)
		}
	}

	var regOpts []collab.RegisterOption
	if wm != nil {
		regOpts = append(regOpts, collab.WithCanceller(wm))
		regOpts = append(regOpts, collab.WithProcessProvider(&processAdapter{wm: wm}))
		regOpts = append(regOpts, collab.WithTaskSpawner(wm))
		regOpts = append(regOpts, collab.WithSessionIDRecorder(wm))
		regOpts = append(regOpts, collab.WithBackoffProvider(wm))
	}
	if wtManager != nil {
		regOpts = append(regOpts, collab.WithWorktreeProvider(&worktreeAdapter{mgr: wtManager}))
	}
	collab.Register(mcpServer, svc, logger, registry, taskOrch, regOpts...)

	notifier := app.NewNotifier(pol.SignalFilePath(), stateLoader, getAgent, pushFunc, logger, notifierOpts...)
	svc.SetNotifier(notifier)
	go notifier.Start(ctx)

	watchdogOpts := []app.WatchdogOption{
		app.WithWatchdogNotifier(notifier),
		app.WithPolicy(pol),
	}
	if wm != nil {
		watchdogOpts = append(watchdogOpts, app.WithProcessActivity(wm))
		watchdogOpts = append(watchdogOpts, app.WithAutoCanceller(wm))
		// Fix D.2: let the watchdog re-drive worker spawns for pending tasks
		// the immediate create_task → SpawnForTask path missed (server crash
		// between persist and spawn, empty pool at create-time before Fix A,
		// transient backoff with no follow-up event). Gated by spawnSweepGrace
		// from policy so it only fires for tasks the immediate path has had
		// a chance to handle.
		watchdogOpts = append(watchdogOpts, app.WithSpawnDriver(wm))
	}
	watchdog := app.NewWatchdog(svc, registry, logger, watchdogOpts...)
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
		if c, ok := repo.(interface{ Close() error }); ok {
			if err := c.Close(); err != nil {
				logger.Printf("Warning: close state repository: %v", err)
			}
		}
	}

	bundle.mcpServer = mcpServer
	bundle.cfg = cfg
	bundle.pol = pol
	bundle.logger = logger
	bundle.registry = registry
	bundle.sessions = sessions
	bundle.hooks = hooks
	bundle.svc = svc
	bundle.wm = wm
	bundle.notifier = notifier
	bundle.watchdog = watchdog
	bundle.cleanup = cleanupFunc
	bundle.ctx = ctx
	return bundle
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
	if bundle.watchdog != nil {
		dashOpts = append(dashOpts, dashboard.WithGCStatsProvider(bundle.watchdog))
	}
	if bundle.logger != nil {
		dashOpts = append(dashOpts, dashboard.WithLogger(bundle.logger))
	}
	if o := bundle.svc.Policy().Orchestration(); o != nil && o.WorkerTimeoutSeconds > 0 {
		dashOpts = append(dashOpts, dashboard.WithHeartbeatThreshold(time.Duration(o.WorkerTimeoutSeconds)*time.Second))
	}
	dash := dashboard.NewHandler(bundle.svc, bundle.registry, dashOpts...)
	dash.RegisterRoutes(mux)

	wAPI := newWorkerAPI(bundle.svc, bundle.registry, bundle.logger)
	if bundle.wm != nil {
		wAPI.sessionIDRecorder = bundle.wm
		wAPI.spawner = bundle.wm
	}
	wAPI.RegisterRoutes(mux)

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

	if bundle.wm != nil {
		bundle.wm.StartupCheck()
	}

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

	// Also redirect the standard `log` package so callers that use
	// log.Printf (e.g. policy.constitutionProfileSources) reach the
	// same destinations. Without this, daemon-mode log.Printf calls
	// silently disappear when stderr is detached from a terminal.
	mw := io.MultiWriter(writers...)
	log.SetOutput(mw)
	log.SetFlags(log.LstdFlags)
	log.SetPrefix("[mcp-pair] ")

	return log.New(mw, "[mcp-pair] ", log.LstdFlags|log.Lshortfile)
}

// loadConfig loads policy configuration from MCP_CONFIG, falling back to
// ~/.config/stringwork/config.yaml when present, and finally to compiled-in
// defaults. The auto-discovery step matters because bare invocations like
// `mcp-stringwork --daemon` (or admin CLI subcommands) have no MCP launcher
// to pass MCP_CONFIG in their environment, but should still honour the same
// config file the install scripts and dashboard documentation reference.
func loadConfig(logger *log.Logger) *policy.Config {
	cfg := policy.DefaultConfig()
	configPath := os.Getenv("MCP_CONFIG")
	if configPath == "" {
		if defaultPath := policy.DefaultConfigFile(); defaultPath != "" {
			if _, err := os.Stat(defaultPath); err == nil {
				configPath = defaultPath
			}
		}
	}
	if configPath != "" {
		loaded, err := policy.LoadConfig(configPath)
		if err != nil {
			logger.Printf("Warning: failed to load config %s: %v, using defaults", configPath, err)
		} else {
			cfg = loaded
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
			LogPath:      p.LogPath,
		}
	}
	return result
}

func (a *processAdapter) GetRecentOutput(instanceID string) string {
	return a.wm.GetRecentOutput(instanceID)
}

func (a *processAdapter) IsWorkerRunning(instanceID string) bool {
	return a.wm.IsWorkerRunning(instanceID)
}

// workerLivenessAdapter bridges WorkerManager into the
// collab.ProcessLivenessProvider used by the piggyback heartbeat gate.
// It is constructed before the WorkerManager (so the middleware
// closure can capture it) and has its `wm` field populated once the
// WorkerManager exists. When `wm` is nil (no orchestration config) the
// adapter degrades to "unknown worker" and the gate falls through to
// the legacy refresh-on-every-call behavior.
type workerLivenessAdapter struct {
	wm *app.WorkerManager
}

func (a *workerLivenessAdapter) IsWorkerRunning(instanceID string) bool {
	if a.wm == nil {
		return false
	}
	return a.wm.IsWorkerRunning(instanceID)
}

func (a *workerLivenessAdapter) HasWorker(instanceID string) bool {
	if a.wm == nil {
		return false
	}
	return a.wm.HasWorker(instanceID)
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

	agentType := app.ResolveParentAgentType(state, agent)
	pending := 0
	for _, task := range state.Tasks {
		if (task.AssignedTo == agent || task.AssignedTo == agentType || task.AssignedTo == "any") && task.Status == "pending" {
			pending++
		}
	}

	fmt.Printf("unread=%d pending=%d\n", unread, pending)
}

func runAuditCommand() {
	logger := log.New(os.Stderr, "", 0)
	cfg := loadConfig(logger)
	pol := policy.New(cfg)

	repo, err := repository.NewStateRepository(pol.StateFile())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	reader, ok := repo.(app.AuditReader)
	if !ok {
		fmt.Fprintf(os.Stderr, "Audit not supported by repository\n")
		os.Exit(1)
	}

	filter := app.AuditFilter{}
	for i := 2; i < len(os.Args); i++ {
		arg := os.Args[i]
		if strings.HasPrefix(arg, "--agent=") {
			filter.Agent = strings.TrimPrefix(arg, "--agent=")
		} else if strings.HasPrefix(arg, "--tool=") {
			filter.ToolName = strings.TrimPrefix(arg, "--tool=")
		} else if strings.HasPrefix(arg, "--session=") {
			filter.SessionID = strings.TrimPrefix(arg, "--session=")
		}
	}

	entries, err := reader.ReadAudit(filter)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading audit: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("%-20s %-15s %-25s %8s  %s\n", "TIMESTAMP", "AGENT", "TOOL", "MS", "ERROR")
	fmt.Println(strings.Repeat("-", 90))
	for _, e := range entries {
		fmt.Printf("%-20s %-15s %-25s %8d  %s\n",
			e.Timestamp.Format("2006-01-02 15:04:05"),
			e.Agent,
			e.ToolName,
			e.DurationMs,
			e.Error)
	}
}

func runDiscoverCommand() {
	fmt.Println("Scanning for AI agent CLIs...")
	fmt.Println()

	type agentInfo struct {
		name     string
		binaries []string
		desc     string
	}

	agents := []agentInfo{
		{"claude-code", []string{"claude"}, "Anthropic Claude Code CLI"},
		{"codex", []string{"codex"}, "OpenAI Codex CLI"},
		{"gemini", []string{"gemini"}, "Google Gemini CLI"},
	}

	fmt.Printf("%-15s %-10s %-45s %s\n", "AGENT", "STATUS", "PATH", "DESCRIPTION")
	fmt.Println(strings.Repeat("-", 90))

	var foundAgents []agentInfo
	for _, a := range agents {
		found := false
		for _, bin := range a.binaries {
			for _, dir := range strings.Split(os.Getenv("PATH"), string(os.PathListSeparator)) {
				path := filepath.Join(dir, bin)
				if info, err := os.Stat(path); err == nil && !info.IsDir() {
					fmt.Printf("%-15s %-10s %-45s %s\n", a.name, "FOUND", path, a.desc)
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			fmt.Printf("%-15s %-10s %-45s %s\n", a.name, "not found", "", a.desc)
		} else {
			foundAgents = append(foundAgents, a)
		}
	}

	if len(foundAgents) > 0 {
		fmt.Println("\nSuggested orchestration.workers config:")
		fmt.Println()
		fmt.Println("orchestration:")
		fmt.Println("  workers:")
		for _, a := range foundAgents {
			fmt.Printf("    - type: %s\n", a.name)
			fmt.Printf("      instances: 1\n")
			fmt.Printf("      timeout_seconds: 600\n")
		}
	}
}

func runRestartCommand() {
	logger := log.New(os.Stderr, "[mcp-pair] ", log.LstdFlags)
	cfg := loadConfig(logger)
	pol := policy.New(cfg)

	socketPath := pol.SocketPath()
	pidFile := pol.PIDFile()

	stopped := stopDaemon(pidFile, socketPath, logger)
	if !stopped {
		fmt.Println("No running daemon found")
	}

	if !pol.DaemonEnabled() {
		if stopped {
			fmt.Println("Daemon stopped (daemon mode not enabled in config, skipping restart)")
		} else {
			fmt.Println("Daemon mode not enabled in config")
		}
		return
	}

	fmt.Println("Starting daemon with fresh config...")
	if err := startDaemonProcess(socketPath, pidFile, logger); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start daemon: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Daemon restarted successfully")
}

func stopDaemon(pidFile, socketPath string, logger *log.Logger) bool {
	pid, err := readPIDFile(pidFile)
	if err != nil {
		if isDaemonRunning(socketPath) {
			logger.Println("Daemon is running but no PID file found, cannot stop")
			return false
		}
		return false
	}

	if !isPIDAlive(pid) {
		logger.Printf("Stale PID file (pid=%d), cleaning up", pid)
		removePIDFile(pidFile)
		removeStaleSocket(socketPath)
		return false
	}

	fmt.Printf("Stopping daemon (pid=%d)...\n", pid)
	proc, err := os.FindProcess(pid)
	if err != nil {
		logger.Printf("Cannot find process %d: %v", pid, err)
		return false
	}

	if err := proc.Signal(syscall.SIGTERM); err != nil {
		logger.Printf("Failed to send SIGTERM to %d: %v", pid, err)
		return false
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !isPIDAlive(pid) {
			removePIDFile(pidFile)
			removeStaleSocket(socketPath)
			fmt.Println("Daemon stopped")
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}

	logger.Printf("Daemon did not exit within 10s, sending SIGKILL")
	_ = proc.Signal(syscall.SIGKILL)
	time.Sleep(500 * time.Millisecond)
	removePIDFile(pidFile)
	removeStaleSocket(socketPath)
	fmt.Println("Daemon killed")
	return true
}
