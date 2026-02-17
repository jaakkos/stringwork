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
                internal/knowledge (FTS5 knowledge store, separate from state)
                internal/policy (config, workspace validation, safety)
                internal/worktree (git worktree manager for worker isolation)
```

## Package responsibilities

| Package | Role |
|---------|------|
| **cmd/mcp-server** | Entrypoint. Loads config, wires dependencies, starts MCP server (stdio or HTTP), CLI subcommands (`status`, `--version`). |
| **internal/domain** | Core entities and aggregate state. No external dependencies. `Message`, `Task`, `Plan`, `PlanItem`, `AgentInstance`, `WorkContext`, `FileLock`, `Presence`, `CollabState`. |
| **internal/app** | Application services and ports. `CollabService` (all collaboration operations), `WorkerManager` (spawn/kill workers, heartbeat monitoring), `TaskOrchestrator` (auto-assign tasks to workers), `Watchdog` (progress monitoring, SLA alerts), `SessionRegistry` (multi-client tracking). Defines `StateRepository` and `Policy` interfaces. |
| **internal/repository/sqlite** | Implements `StateRepository` using SQLite (via modernc.org/sqlite, pure Go). Full load/save of `CollabState`. |
| **internal/policy** | Config loading from YAML, workspace path validation, state file and log file paths, global defaults. |
| **internal/tools/collab** | 23 MCP tool handlers. Each handler parses `map[string]any` args, calls `CollabService`, and returns `mcp.CallToolResult`. Also: piggyback notifications, MCP resource providers, dynamic instructions. |
| **internal/dashboard** | Web dashboard (embedded HTML) and REST API for viewing tasks, workers, messages, and plans. Served at `/dashboard` in HTTP mode. |
| **internal/knowledge** | FTS5-powered project knowledge store. Indexes markdown docs, Go source, session notes, and task summaries. Separate SQLite database from main state. |
| **internal/worktree** | Git worktree manager. Creates isolated checkouts per worker, runs setup commands, cleans up on cancel/exit. |

## Data flow

### Tool call

1. MCP client sends `tools/call` request
2. Tool handler in `internal/tools/collab` parses arguments
3. Handler calls `svc.Run(func(state) { ... })` on `CollabService`
4. `CollabService` does `repo.Load()`, mutates state, `repo.Save()`
5. Handler formats the result as `mcp.CallToolResult`
6. Piggyback middleware appends notification banners (unread messages, pending tasks, STOP signals)

### Worker lifecycle

1. Driver creates a task with `assigned_to='any'`
2. `TaskOrchestrator` assigns it to a worker type based on strategy (least_loaded or capability_match)
3. `WorkerManager` spawns the worker process with the configured command
4. Worker connects to MCP server, claims the task, does work
5. `Watchdog` monitors heartbeats and progress reports, escalates if silent
6. Worker completes task and sends findings; process exits
7. `WorkerManager` cleans up (worktree, process resources)

### State management

All state lives in `domain.CollabState`. The repository loads and saves the full aggregate on every operation. There is no partial update -- this keeps the model simple and consistent.

The knowledge store (`internal/knowledge`) uses a separate SQLite database with incremental FTS5 updates (checksums, not full replace), because it indexes project files that shouldn't be destroyed on every state save.

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

## Testing

- **Domain**: Pure types, no dependencies.
- **App**: Table-driven tests with real SQLite (temp files). Tests cover service methods, watchdog alerts, worker env, progress monitoring, and pruning.
- **Repository**: Integration tests with temp SQLite databases.
- **Tools/collab**: Integration tests using a real `CollabService` and in-memory state.
- **Dashboard**: HTTP handler tests with `httptest`.
- **Knowledge**: FTS5 indexing and query tests.
- **Worktree**: Git worktree creation/cleanup tests.

Run all tests:

```bash
go test ./...
go test ./... -race -cover
```
