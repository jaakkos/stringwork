# Repository Guidelines

## Project Structure & Module Organization
This repository is a Go MCP server for pair-programming coordination. It supports a **driver/worker** model: one configurable driver (e.g. cursor, claude-code, or codex) and N workers that execute tasks with shared context and scope.

- `cmd/mcp-server/`: executable entrypoint (`main.go`, `daemon.go`, `proxy.go`). Supports daemon, proxy, and standalone modes.
- `internal/domain/`: core entities (AgentInstance, WorkContext, Task, Plan, etc.).
- `internal/app/`: application services (CollabService, WorkerManager, TaskOrchestrator).
- `internal/tools/collab/`: MCP tool handlers (tasks, messaging, plans, locks, worker_status, heartbeat, work_context, cancel_agent).
- `internal/repository/`: persistence backend (`sqlite/`).
- `internal/policy/`: workspace and safety policy; orchestration config (driver, workers).
- `mcp/`: runtime config (`config.yaml` with `daemon`, `orchestration` sections).
- `docs/`: setup, architecture, and workflow docs.
- `scripts/`: install script and helpers.

## Build, Test, and Development Commands
- `go build -o mcp-stringwork ./cmd/mcp-server`: build the server binary.
- `go test ./...`: run all unit tests.
- `go test ./... -cover`: run tests with coverage output.
- `go run ./cmd/mcp-server`: run locally without building.
- `./mcp-stringwork status claude-code`: check unread/pending counts for an agent.

## Coding Style & Naming Conventions
- Follow standard Go formatting: run `gofmt` on changed files before opening a PR.
- Use tabs for indentation (Go default); do not align with spaces manually.
- Keep package names short and lowercase (`app`, `policy`, `collab`).
- Exported identifiers: `PascalCase`; internal helpers: `camelCase`.
- Test files must end with `_test.go`; prefer table-driven tests for behavior variants.

## Testing Guidelines
- Primary framework is Go’s built-in `testing` package.
- Keep tests close to code under test in the same package directory.
- Name tests clearly (`Test<Function>_<Scenario>`), e.g. `TestClaimNext_DryRun`.
- Cover success paths, validation failures, and policy/safety edge cases.

## Commit & Pull Request Guidelines
Git history is not available in this workspace, so adopt this convention:
- Commit format: `type(scope): imperative summary` (e.g. `feat(tasks): add dry-run claim`).
- Keep commits focused; separate refactors from behavior changes.
- PRs should include: purpose, key changes, test evidence (`go test ./...` output), and linked issue/task.
- Include config or API examples when behavior changes affect MCP clients.

## Security & Configuration Tips
- Do not commit local secrets or machine-specific paths.
- Prefer `MCP_CONFIG` for local overrides instead of editing shared defaults.
- Keep workspace path validation intact when changing file-lock or policy code.
