# Clean Architecture

This project follows a clean architecture layout so that business logic does not depend on storage or transport details.

## Layers

```
cmd/mcp-server (main)
    ↓
internal/adapter (MCP tool handlers: parse args, call app, format MCP response)
    ↓
internal/app (use cases: CollabService, StateRepository interface, Policy interface)
    ↓
internal/domain (entities: Message, Task, Plan, CollabState, ...)
    ↑
internal/repository (sqlite — implements StateRepository)
internal/policy (config — implements app.Policy)
internal/server, internal/mcp (transport — used by adapter)
```

## Directory layout

| Path | Role |
|------|------|
| **internal/domain** | Entities and aggregate state. No dependencies. |
| **internal/app** | Use cases and ports. Defines `StateRepository`, `Policy`; provides `CollabService` with all collaboration operations. Depends only on domain. |
| **internal/repository** | Implements `StateRepository` (SQLite). Depends on domain and app (interface). |
| **internal/adapter/mcp** | MCP tool handlers: parse `map[string]any`, call `CollabService`, return `mcp.CallToolResult`. Depends on app, server, mcp. |
| **internal/policy** | Config loading and validation. Implements `app.Policy`. |
| **internal/server** | MCP server (stdio, tool registry). Framework. |
| **internal/mcp** | MCP protocol types and transport. |

## Data flow

1. **main**: Load config → create `StateRepository` (SQLite at `policy.StateFile()`) → create `CollabService(repo, policy, logger)` → register tools → run server.
2. **Tool call**: Server receives `tools/call` → adapter handler parses args → handler calls `svc.SendMessage(...)` (or other method) → service does `repo.Load()`, mutate state, `repo.Save()` → handler formats and returns MCP result.
3. **State**: All state lives in `domain.CollabState`. Repository loads/saves the full aggregate. No direct DB or file access from app or adapter.

## Migration from legacy layout

- **internal/tools/collab** (legacy): Monolithic package with global state, file/sqlite logic, and MCP handlers in one place.
- **Target**: Types in **domain**; persistence behind **StateRepository** in **repository**; use cases in **app.CollabService**; MCP handlers in **adapter/mcp** calling the service. Legacy `collab` is removed or reduced to a thin wrapper that delegates to app + adapter.

## Key interfaces

### StateRepository (app)

```go
type StateRepository interface {
    Load() (*domain.CollabState, error)
    Save(*domain.CollabState) error
}
```

### Policy (app)

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

- **Domain**: Pure types, easy to test.
- **App**: Mock `StateRepository` and `Policy`; unit-test `CollabService` methods.
- **Repository**: Integration tests with a real SQLite file or temp JSON file.
- **Adapter**: Test with mock `CollabService` or integration test with real service + in-memory repo.
