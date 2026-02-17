# Internal package review (dead code & tests)

One-time deep review of `internal/` for dead code and redundant tests. Use this as a reference for future pair programming (e.g. Claude Code / Codex) when doing similar cleanups.

## Removed (this pass)

### Dead code
- **`internal/policy/policy.go`** – `Policy.Config()` was never called; removed.
- **`internal/app/notifier.go`** – `WithDebounce()` was never used; removed. (Debounce still works via default in `NewNotifier`; only `WithPollInterval` is used in tests.)
- **`internal/app/orchestrator.go`** – `TaskOrchestrator.UnassignTask()` was never called; removed.
- **`internal/app/service.go`** – `CollabService.Logger()` was never called; removed.

### Redundant tests
- **`internal/tools/collab/collab_test.go`** – Removed duplicate tests that only exercised `app`/`domain` and are already covered elsewhere:
  - `TestTruncate` → covered in `app/helpers_test.go`
  - `TestPruneMessages` → covered in `app/pruning_test.go`
  - `TestValidateAgent` → covered in `app/helpers_test.go`
  - `TestJoinStrings` → covered in `app/helpers_test.go`
  - `TestNewState` → covered in `domain/entity_test.go` (as `TestNewCollabState`)

## Checked and kept

- **`Policy.Config()`** – Not in `app.Policy` interface; no callers; removed.
- **`AssignmentStrategy` interface and strategy funcs** – Used via `NewTaskOrchestrator` (strategy name → function); kept.
- **`PairUpdateParams`** – Used in notifier and notifier tests; kept.
- **`WorkerSpawnConfig`** – Used only inside `WorkerManager`; struct is part of internal spawn flow; kept.
- **`DynamicInstructionsForClient`** – Used in `instructions_test.go` and by register; kept.
- **`escapeJSON`** – Used in `workflow.go` and `file_lock.go`; kept.
- **`TouchNotifySignal`** – Used from `service.go` and notifier tests; kept.
- **`GlobalStateDir` / `GlobalStateFile`** – Used from policy and worker_manager; kept.

## Suggestions for future reviews

1. **Exported but unused** – Search for exported symbols (e.g. `Policy.Config`, `Logger()`) and grep the repo for callers before removing.
2. **Duplicate tests** – Prefer one place per behavior: `app` and `domain` tests for core logic; `collab` tests for tool/handler behavior and integration.
3. **Repository** – No tests in `internal/repository` (only in `repository/sqlite`); acceptable. Add repository-level tests if you introduce a new backend.
4. **Orchestrator** – `AssignTask` is used from `tasks.go`; `UnassignTask` had no callers (e.g. when a task is completed or cancelled, callers could use it in a future change if needed).
