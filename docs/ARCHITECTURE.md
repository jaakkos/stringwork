# Architecture

Stringwork follows a clean architecture layout. Business logic does not depend on storage, transport, or tool handler details.

## Layers

```
cmd/mcp-server (main, CLI)
    |
    +-- internal/tools/collab (MCP tool handlers: parse args, call app, format response)
    |       |
    +-- internal/dashboard (web UI + REST API)
    |       |
    +-------+-- internal/app (use cases: CollabService, WorkerManager, Watchdog, Orchestrator)
                    |
                internal/domain (entities: Message, Task, Plan, AgentInstance, WorkContext, ...)
                    ^
                internal/repository/sqlite (implements StateRepository)
                internal/policy (config, workspace validation, safety)
                internal/worktree (git worktree manager for worker isolation)
```

## Package responsibilities

| Package | Role |
|---------|------|
| **cmd/mcp-server** | Entrypoint. Loads config, wires dependencies. Supports three modes: **daemon** (HTTP on TCP + unix socket, no stdio), **proxy** (thin stdio-to-HTTP bridge), and **standalone** (legacy stdio + HTTP in one process). CLI subcommands (`status`, `audit`, `discover`, `constitution`, `--version`). |
| **internal/domain** | Core entities and aggregate state. No external dependencies. `Message`, `Task`, `Plan`, `PlanItem`, `AgentInstance`, `WorkContext`, `AuditEntry`, `FileLock`, `Presence`, `CollabState`. |
| **internal/app** | Application services and ports. `CollabService` (all collaboration operations), `WorkerManager` (spawn/kill workers, heartbeat monitoring, Gemini `GEMINI_SYSTEM_MD` generation), `TaskOrchestrator` (auto-assign tasks to workers), `Watchdog` (progress monitoring, SLA alerts, DLQ failure tracking), `SessionRegistry` (multi-client tracking). Defines `StateRepository`, `AuditWriter`, `AuditReader`, and `Policy` interfaces. |
| **internal/repository/sqlite** | Implements `StateRepository` and `AuditWriter`/`AuditReader` using SQLite (via modernc.org/sqlite, pure Go). Full load/save of `CollabState`; separate `audit_log` table for tool call recording. |
| **internal/policy** | Config loading from YAML, workspace path validation, state file and log file paths, global defaults. Includes `AuditConfig` for audit logging settings, and the `constitution:` block (`constitution_config.go` + `constitution_profile.go`) that materialises user- and team-level rule sources into `constitution.Source` instances. |
| **internal/constitution** | Layered prompt resolver. Defines `Source` (`DirSource` for local dirs, `GitSource` for shallow-cloned remotes), `Resolve` for ordered concatenation, `BuildPreamble` for the MCP-response pointer block, and `BuildInline` for the spawn-time inlined body. Also: `TaskKindFromTitle` heuristic and `ScopeFilter` for task-kind / agent-role narrowing. See [CONSTITUTION.md](CONSTITUTION.md). |
| **internal/tools/collab** | 24 MCP tool handlers. Each handler parses `map[string]any` args, calls `CollabService`, and returns `mcp.CallToolResult`. Also: audit middleware, piggyback notifications, MCP resource providers, dynamic instructions. |
| **internal/dashboard** | Web dashboard (embedded HTML) and REST API for viewing tasks, workers, messages, and plans. Served at `/dashboard` in HTTP mode. |
| **internal/worktree** | Git worktree manager. Creates isolated checkouts per worker, runs setup commands, cleans up on cancel/exit. |

## Data flow

### Tool call

1. MCP client sends `tools/call` request
2. Audit middleware records the call (agent, tool, args summary, timing)
3. Tool handler in `internal/tools/collab` parses arguments
4. Handler calls `svc.Run(func(state) { ... })` on `CollabService`
5. `CollabService` does `repo.Load()`, mutates state, `repo.Save()`
6. Handler formats the result as `mcp.CallToolResult`
7. Piggyback middleware appends notification banners (unread messages, pending tasks, STOP signals)
8. Audit middleware writes the entry to the `audit_log` table (synchronous)

### Worker lifecycle

1. Driver creates a task with `assigned_to='any'`
2. `TaskOrchestrator` assigns it to a worker type based on strategy (least_loaded or capability_match), skipping types in failure backoff or **quota preflight block**
3. `WorkerManager.SpawnForTask` reads the **quota cache only** (no HTTP on the synchronous `create_task` path). If the type is explicitly blocked, the task is queued and the driver gets a system message; stale/empty cache fails open and kicks a background refresh
4. `WorkerManager` spawns the worker process with the configured command (includes constraint compliance rules in spawn prompt; for Gemini, auto-generates `GEMINI_SYSTEM_MD`)
5. Worker connects to MCP server, claims the task (constraints surfaced inline), calls `get_work_context` to check constraints, does work
6. `Watchdog` monitors heartbeats and progress reports, escalates if silent, auto-cancels unresponsive workers
7. Worker completes task and sends findings; process exits
8. `WorkerManager` cleans up (worktree, process resources)

**Quota preflight** (`internal/quota`, opt-in via `orchestration.quota_preflight.enabled`): background HTTP checks against stored OAuth tokens (Anthropic usage, ChatGPT wham/usage, Gemini Code Assist) — **0 LLM tokens**. State lives in `quota.Monitor` cache, merged into `BackedOffAgentTypes` / `BackoffInfoForType` at read time (not written to `backoffUntil`). When a type transitions blocked→available, queued spawns are drained. CLI: `mcp-stringwork quota check [--json] [--agent TYPE]` (no daemon).

If a worker is cancelled or crashes mid-task, `WorkerManager` captures its recent output and stores it in the task's `WorkContext.PreviousOutput`. When a replacement worker is spawned, this output is injected into its prompt so no information is lost across restarts.

The watchdog uses task-bound worker instances (e.g. `claude-code-task-3`) as the authoritative liveness signal for each task, preventing false recoveries when idle instances from previous sessions have stale heartbeats.

### State management

All state lives in `domain.CollabState`. The repository loads and saves the full aggregate on every operation. There is no partial update -- this keeps the model simple and consistent.

## Key interfaces

### StateRepository

```go
type StateRepository interface {
    Load() (*domain.CollabState, error)
    Save(*domain.CollabState) error
}
```

### Policy

```go
type Policy interface {
    MessageRetentionMax() int
    MessageRetentionDays() int
    PresenceTTLSeconds() int
    StateFile() string
    WorkspaceRoot() string
    IsToolEnabled(name string) bool
    ValidatePath(path string) (string, error)
}
```

## Server modes

The server binary (`mcp-stringwork`) supports three operational modes:

| Mode | Flag | Transport | Use case |
|------|------|-----------|----------|
| **Daemon** | `--daemon` | HTTP on TCP + unix socket | Background process shared by multiple Cursor windows |
| **Proxy** | (auto) | Stdio ↔ HTTP bridge | Cursor subprocess that connects to the daemon |
| **Standalone** | `--standalone` | Stdio + HTTP in one process | Legacy single-process mode |

With daemon mode enabled in config, the binary auto-detects the mode:
1. If `--daemon` flag: run as daemon
2. If `--standalone` flag: run standalone
3. If a daemon is already running (unix socket responds): connect as proxy
4. If daemon config enabled: start a new daemon, then connect as proxy
5. Otherwise: run standalone

The daemon tracks connected proxies via unix socket connection counting. When the last proxy disconnects, a configurable grace period starts. If no new proxy connects within the grace period, the daemon shuts down cleanly.

## Testing

- **Domain**: Pure types, no dependencies.
- **App**: Table-driven tests with real SQLite (temp files). Tests cover service methods, watchdog alerts, worker env, progress monitoring, and pruning.
- **Repository**: Integration tests with temp SQLite databases.
- **Tools/collab**: Integration tests using a real `CollabService` and in-memory state.
- **Dashboard**: HTTP handler tests with `httptest`.
- **Worktree**: Git worktree creation/cleanup tests.

Run all tests:

```bash
go test ./...
go test ./... -race -cover
```
