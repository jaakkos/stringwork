# External Project Analysis: Multi-Agent MCP Systems

**Date:** 2025-02-17
**Analyzed projects:**
- [rinadelph/Agent-MCP](https://github.com/rinadelph/Agent-MCP) (Python/Node, AGPL-3.0, 1.2k stars)
- [grupa-ai/agent-mcp](https://github.com/grupa-ai/agent-mcp) (Python, MIT, 20 stars)

**Our project:** Go MCP stringwork server with driver/worker orchestration

---

## Executive Summary

Both projects tackle multi-agent coordination via MCP but from very different angles. **rinadelph/Agent-MCP** is a full-featured collaboration platform with RAG, dashboards, and git worktree isolation -- closest to our use case. **grupa-ai/agent-mcp** is a network-first framework with strong security abstractions (DID, zero-trust, rate limiting) but weaker on local tooling. Neither is a drop-in replacement for our approach, but both contain patterns and solutions worth adopting.

**Key takeaways ranked by value to our project:**

| Priority | Feature | Source | Effort |
|----------|---------|--------|--------|
| **High** | Per-project state isolation | rinadelph | Medium |
| **High** | RAG-based project context querying | rinadelph | High |
| **High** | Git worktree isolation for workers | rinadelph | High |
| **High** | Rate limiting middleware | grupa-ai | Low |
| **High** | Audit logging | grupa-ai | Medium |
| **Medium** | Health monitoring with history | grupa-ai | Low |
| **Medium** | Task duplication detection | rinadelph | Low |
| **Medium** | Structured tool registry | rinadelph | Medium |
| **Low** | TUI display mode | rinadelph | Medium |
| **Low** | Webhook-based notifications | grupa-ai | Medium |
| **Low** | Scoped JWT tokens | grupa-ai | High |

---

## 1. Architecture Comparison

### rinadelph/Agent-MCP

```
cli.py (Click CLI, uvicorn/anyio)
  ├── app/main_app.py (Starlette web app)
  ├── tools/ (MCP tool handlers with registry pattern)
  ├── db/ (SQLite per-project in .agent/mcp_state.db)
  ├── features/
  │   ├── rag/ (OpenAI embeddings, vector search)
  │   └── worktree_integration.py (git worktree per agent)
  ├── core/ (config, globals, auth)
  ├── tui/ (terminal UI display)
  └── dashboard/ (React/Next.js dashboard)
```

**Key design decisions:**
- **Per-project database**: State lives in `<project>/.agent/mcp_state.db`
- **Admin token auth**: Server generates admin token; workers get derived tokens
- **RAG as first-class**: Every project gets embeddings of its docs + code
- **SSE + stdio transports**: Same as ours
- **Global state via module-level globals**: `core/globals.py` with `g.active_agents`, etc.

### grupa-ai/agent-mcp

```
agent_mcp/
  ├── mcp_agent.py (AutoGen ConversableAgent + context store)
  ├── mcp_decorator.py (@mcp_agent decorator)
  ├── registry.py (multi-language agent registry)
  ├── security.py (DID, zero-trust, rate limiting, audit)
  ├── mcp_transport.py (HTTP transport to cloud server)
  ├── payments.py (Stripe/USDC integration)
  └── *_mcp_adapter.py (framework adapters: LangChain, CrewAI, etc.)
```

**Key design decisions:**
- **Cloud-first**: Default server is `mcp-server-ixlfhxquwq-ew.a.run.app`
- **Decorator integration**: `@mcp_agent(mcp_id="MyAgent")` wraps any class
- **Zero-trust security**: DID-based identity, ABAC authorization, JWT scoped tokens
- **Multi-protocol**: Auto-detects MCP, A2A, OpenAPI, REST, WebSocket, gRPC
- **Framework-agnostic**: Adapters for 10+ frameworks

### Our Project (stringwork)

```
cmd/mcp-server/ (Go binary, stdio/HTTP)
internal/
  ├── app/ (CollabService, orchestrator, worker_manager, watchdog, notifier)
  ├── domain/ (entities: Message, Task, Plan, Presence, WorkContext, etc.)
  ├── tools/collab/ (22 MCP tool handlers)
  ├── repository/sqlite/ (global state.sqlite)
  ├── policy/ (config, path validation, tool enablement)
  └── dashboard/ (embedded web UI)
```

### Comparison Matrix

| Aspect | rinadelph | grupa-ai | Ours |
|--------|-----------|----------|------|
| Language | Python | Python | Go |
| State scope | Per-project | Cloud | Global file |
| Auth | Admin token | DID + JWT | Mock OAuth |
| Agent discovery | DB query | Network registry | Config file |
| Knowledge | RAG + embeddings | Context store dict | SessionNotes |
| File isolation | Git worktrees | N/A | File locks |
| Recovery | N/A | Health monitor | Watchdog |
| Notification | N/A | Webhooks | Signal files |
| Max agents | 10 (enforced) | Unlimited | Config-based |

---

## 2. High-Value Solutions for Our Use Cases

### 2.1 Per-Project State Isolation

**Problem in our project:** State file is global at `~/.config/stringwork/state.sqlite`, shared across all projects. This causes cross-project pollution and makes it impossible to run two pair-programming sessions on different projects simultaneously.

**How rinadelph solves it:** Database lives at `<project-dir>/.agent/mcp_state.db`. The `MCP_PROJECT_DIR` environment variable determines the project root at startup.

```python
# rinadelph approach
def get_db_path() -> Path:
    return get_project_dir() / ".agent" / DB_FILE_NAME
```

**Recommendation for us:**
- Move state to `<workspace>/.stringwork/state.sqlite`
- Keep `~/.config/stringwork/` for global config only
- Use `workspace_root` from policy config to determine the project directory
- Fall back to global state if workspace isn't a project root

**Estimated effort:** Medium -- requires migration logic and config changes in `policy.go` and `repository/sqlite/store.go`.

---

### 2.2 RAG-Based Project Context Querying

**Problem in our project:** Agents share context through `SessionNotes` (simple key-value notes) and `WorkContext` (relevant files list). There's no way to query project architecture, past decisions, or code patterns intelligently.

**How rinadelph solves it:**
- Indexes all markdown files and project context into OpenAI embeddings (text-embedding-3-large)
- Stores vectors in SQLite with virtual search tables
- `ask_project_rag` MCP tool lets agents query the knowledge base
- Two modes: simple (1536-dim, markdown only) and advanced (3072-dim, code + tasks)
- Task placement uses RAG to detect duplicate tasks (TASK_DUPLICATION_THRESHOLD = 0.8)

```python
# rinadelph RAG query flow
async def ask_project_rag_tool_impl(arguments):
    query_text = arguments.get("query")
    answer_text = await query_rag_system(query_text)  # embeddings + similarity search
    return [TextContent(type="text", text=answer_text)]
```

**Recommendation for us:**
- Add optional RAG capability using SQLite FTS5 (no external API dependency)
- Index: `CLAUDE.md`, `AGENTS.md`, `docs/*.md`, `SessionNotes`, completed task summaries
- New MCP tool: `query_project_knowledge` -- agents ask questions, get relevant context
- Make it opt-in via config (`features.rag_enabled: true`)
- Consider supporting external embeddings as an enhancement later

**Why FTS5 over OpenAI embeddings:** Our project is local-first Go; adding an OpenAI dependency for core functionality would be a design mistake. FTS5 provides good-enough full-text search without network calls. For semantic search, we could add optional support for local models (ollama) or external APIs later.

**Estimated effort:** High -- new feature area (indexer, query engine, MCP tool).

---

### 2.3 Git Worktree Isolation for Workers

**Problem in our project:** Workers share the same working directory. File locks (`lock_file` tool) prevent simultaneous edits but are cooperative and coarse-grained. If a worker modifies files without locking, conflicts happen.

**How rinadelph solves it:**

```python
# WorktreeManager creates isolated checkouts per agent
class WorktreeManager:
    def create_agent_worktree(self, agent_id, admin_token_suffix, config):
        worktree_path = generate_worktree_path(agent_id, admin_token_suffix)
        branch_name = generate_branch_name(agent_id, config.branch_name)
        create_git_worktree(path=worktree_path, branch=branch_name, base_branch="main")
        run_setup_commands(worktree_path, detect_project_setup_commands(worktree_path))
```

**Key features:**
- Each agent gets a separate git worktree with its own branch
- Auto-detects project setup commands (npm install, pip install, etc.)
- Cleanup strategies: `manual`, `on_terminate`, `smart`
- Tracks all worktrees in memory for status reporting

**Recommendation for us:**
- Add optional git worktree support in `WorkerManager`
- When spawning a worker, if `worktree.enabled` in config:
  1. Create worktree at `.stringwork/worktrees/<agent-id>/`
  2. Set worker's `cwd` to the worktree path
  3. Auto-detect and run setup commands
  4. On worker termination, clean up worktree
- Set the worker's `STRINGWORK_WORKSPACE` env var to the worktree path
- Worker's `set_presence workspace=` updates accordingly

```yaml
# Proposed config addition
orchestration:
  worktrees:
    enabled: true
    base_branch: main
    cleanup: on_terminate  # manual | on_terminate | smart
    setup_commands: []      # auto-detect if empty
```

**Estimated effort:** High -- involves git operations, worker lifecycle changes, config additions.

---

### 2.4 Rate Limiting Middleware

**Problem in our project:** No protection against runaway agents creating infinite tasks or sending message floods. A misbehaving worker could exhaust state resources.

**How grupa-ai solves it:**

```python
class RateLimiter:
    def check_rate_limit(self, agent_id: str, action: str) -> Dict:
        # Sliding window with per-agent per-action limits
        # Returns: { allowed: bool, remaining: int, reset_time: str }
```

Their rate limiter uses a sliding window (deque of timestamps) per agent per action, with configurable limits and windows.

**Recommendation for us:**
- Add rate limiting in the piggyback middleware layer (`piggyback.go`)
- Simple in-memory sliding window (no persistence needed)
- Default limits:

| Action | Limit | Window |
|--------|-------|--------|
| `send_message` | 50 | 60s |
| `create_task` | 20 | 60s |
| `update_task` | 100 | 60s |
| `lock_file` | 30 | 60s |

- Return `429 Too Many Requests` equivalent in MCP error response
- Make limits configurable in `config.yaml`

```go
// Proposed rate limiter
type RateLimiter struct {
    mu       sync.Mutex
    requests map[string]*slidingWindow // key: "agent:action"
    limits   map[string]RateLimit
}

type RateLimit struct {
    MaxRequests int
    WindowSecs  int
}
```

**Estimated effort:** Low -- isolated middleware, no state changes needed.

---

### 2.5 Audit Logging

**Problem in our project:** No audit trail of agent actions. When debugging coordination issues or reviewing what happened in a session, there's no history beyond current state.

**How grupa-ai solves it:**

```python
@dataclass
class AuditLogEntry:
    timestamp: str
    agent_id: str
    action: str
    target: str
    resource: str
    result: str
    risk_score: float
    metadata: Dict[str, Any]

    def compute_hash(self) -> str:
        # Cryptographic hash for integrity verification
```

Their audit logger:
- Records every agent action with timestamp, agent, action, target, result
- Computes risk scores per action (sensitive actions score higher)
- Stores hash chain for tamper detection
- Supports verification of audit chain integrity

**Recommendation for us:**
- New SQLite table: `audit_log`
- Log all tool invocations: who called what, when, with what args, what result
- Add `audit_log` method to `CollabService` called from tool handlers
- Include in the dashboard for session replay
- Skip risk scoring and hash chains (over-engineering for local use)

```sql
CREATE TABLE audit_log (
    id INTEGER PRIMARY KEY,
    timestamp TEXT NOT NULL,
    agent TEXT NOT NULL,
    tool TEXT NOT NULL,
    action TEXT,
    args_summary TEXT,
    result_summary TEXT,
    session_id TEXT
);
```

**Estimated effort:** Medium -- new table, logging calls in each tool handler.

---

## 3. Medium-Value Solutions

### 3.1 Health Monitoring with History

**Current state:** Our `Watchdog` does binary liveness checks (last heartbeat > timeout = dead). No trending, no response time tracking, no degradation detection.

**grupa-ai's approach:**
- Tracks response times per health check
- Maintains history (last 100 checks per agent)
- Calculates uptime percentage and average response time
- Async monitoring loop with configurable interval

**Recommendation:** Extend `AgentInstance` with `HealthHistory []HealthCheck` (last 20 entries). Track heartbeat response patterns. Add health summary to `worker_status` tool output. This helps the driver decide whether to keep or replace a sluggish worker.

**Estimated effort:** Low.

### 3.2 Task Duplication Detection

**Current state:** No deduplication. Workers or drivers can create semantically identical tasks.

**rinadelph's approach:** Uses embedding similarity with a threshold (0.8) to detect near-duplicate tasks before creation.

**Recommendation:** Simple string-based dedup is sufficient for us:
- On `create_task`, check existing non-completed tasks for title similarity (Levenshtein or exact match)
- If similarity > threshold, return warning + existing task ID
- No embeddings needed -- titles are short enough for string matching

**Estimated effort:** Low.

### 3.3 Structured Tool Registry Pattern

**Current state:** Tools are registered manually in `register.go` with hand-coded schemas. Adding a tool requires touching multiple files.

**rinadelph's approach:**

```python
def register_tool(name, description, input_schema, implementation):
    # Central registry, schema validation, auto-documentation
```

**Recommendation:** Create a `ToolRegistry` that tools self-register into. Benefits:
- Auto-generated tool documentation
- Schema validation at registration time
- Easier to list/enable/disable tools dynamically
- Cleaner separation between tool definition and handler

**Estimated effort:** Medium -- refactor of existing registration code.

---

## 4. Patterns Worth Adopting

### 4.1 Ephemeral Agent Philosophy (rinadelph)

rinadelph enforces a **maximum of 10 active agents** with strict cleanup:
- Agent finishes task -> immediately terminated
- Agent idle 60+ seconds -> killed, task reassigned
- Need more agents -> least productive removed

**Our equivalent:** The watchdog handles some of this, but we don't enforce agent limits or idle cleanup. Adding configurable `max_workers` and `idle_timeout` to the orchestration config would prevent resource exhaustion.

### 4.2 Progress Update Protocol (rinadelph)

Their documentation emphasizes sending progress updates every 2-3 minutes on long tasks. The driver uses these to decide whether to keep waiting or cancel.

**Our equivalent:** We have this in our `CLAUDE.md` instructions but don't enforce it mechanically. Consider adding:
- Auto-warning in piggyback if a task has been `in_progress` for >5 minutes without a message
- Auto-cancellation configurable timeout per task

### 4.3 Main Context Document (MCD) Pattern (rinadelph)

A single, comprehensive project blueprint that all agents reference. Contains architecture, schemas, APIs, task breakdowns.

**Our equivalent:** We have `CLAUDE.md` + `AGENTS.md` + `docs/`. The MCD pattern is essentially what these files are, but rinadelph makes it a first-class indexed artifact. If we add RAG, these would be the seed documents.

---

## 5. Anti-Patterns to Avoid

### 5.1 Global Module State (rinadelph)

rinadelph uses `core/globals.py` with mutable module-level variables (`g.active_agents`, `g.server_running`). This makes testing difficult and creates hidden dependencies.

**Our approach is better:** We use explicit dependency injection through `CollabService` with mutex-protected state. Keep this pattern.

### 5.2 Cloud Dependency for Core Features (grupa-ai)

grupa-ai's default server is a cloud URL. If the cloud service is down, nothing works.

**Our approach is better:** Local-first with `state.sqlite`. No network dependency for core coordination.

### 5.3 Over-Designed Security (grupa-ai)

DID (Decentralized Identifiers), verifiable credentials, ABAC policies, JWT with rotation -- all for what is essentially local agent coordination. The security module is ~600 lines but has bugs (missing `secrets` import in `registry.py`).

**Lesson:** Security should match the threat model. For local pair-programming, transport-level security (Unix socket permissions or localhost-only binding) is sufficient. Don't add DID for agents running on the same machine.

### 5.4 OpenAI Hard Dependency (rinadelph)

The server won't start without `OPENAI_API_KEY`. Even basic features like task management require it because the config check runs at module import time.

**Our approach is better:** Core features work without any external API. If we add RAG, make it optional with graceful fallback.

---

## 6. Feature Gap Analysis

| Capability | rinadelph | grupa-ai | Ours | Gap? |
|-----------|-----------|----------|------|------|
| Task management | Yes | Basic | Yes (22 tools) | No |
| Message passing | Basic | Yes (transport) | Yes | No |
| File conflict prevention | Git worktrees | N/A | File locks | Partial |
| Project knowledge | RAG + embeddings | Context dict | SessionNotes | **Yes** |
| Agent health | N/A | Health monitor | Watchdog | Partial |
| Audit trail | Via RAG indexing | Full audit logger | **None** | **Yes** |
| Rate limiting | N/A | Sliding window | **None** | **Yes** |
| Per-project isolation | .agent/ dir | Cloud | Global file | **Yes** |
| Dashboard | React app | N/A | Embedded web | No |
| Driver/worker model | Admin/worker tokens | N/A | Full orchestration | No |
| Shared planning | N/A | N/A | Plans + items | Ahead |
| Code review | N/A | N/A | request_review | Ahead |
| Auto-spawn workers | N/A | N/A | WorkerManager | Ahead |
| Notification | N/A | Webhooks | Signal files | Partial |

---

## 7. Implementation Roadmap

Based on this analysis, suggested priority order for enhancements:

### Phase 1: Quick Wins (1-2 days each)
1. **Rate limiting middleware** -- protect against runaway agents
2. **Task duplication detection** -- simple string-match guard in `create_task`
3. **Health monitoring history** -- extend AgentInstance with check history

### Phase 2: Infrastructure (3-5 days each)
4. **Audit logging** -- new SQLite table, log all tool invocations
5. **Per-project state isolation** -- move state to `<workspace>/.stringwork/`

### Phase 3: Major Features (1-2 weeks each)
6. **RAG-based project knowledge** -- FTS5 indexer + query tool
7. **Git worktree isolation** -- WorkerManager integration

---

## Appendix: Source Code References

### rinadelph/Agent-MCP Key Files
- `agent_mcp/cli.py` -- CLI entry point, TUI setup, transport selection
- `agent_mcp/core/config.py` -- Configuration with embedding models, project paths
- `agent_mcp/features/worktree_integration.py` -- WorktreeManager, create/cleanup/track
- `agent_mcp/tools/rag_tools.py` -- RAG query tool registration
- `agent_mcp/db/actions/agent_db.py` -- Agent CRUD operations

### grupa-ai/agent-mcp Key Files
- `agent_mcp/mcp_agent.py` -- MCPAgent (AutoGen extension) with context store
- `agent_mcp/mcp_decorator.py` -- @mcp_agent decorator, transport wiring
- `agent_mcp/registry.py` -- MultiLanguageAgentRegistry, health monitor, protocol detection
- `agent_mcp/security.py` -- ZeroTrustSecurityLayer, DID, ABAC, rate limiting, audit

### Our Key Files for Reference
- `internal/app/service.go` -- CollabService (state coordination)
- `internal/app/orchestrator.go` -- Task assignment strategies
- `internal/app/worker_manager.go` -- Worker process lifecycle
- `internal/app/watchdog.go` -- Liveness and recovery
- `internal/tools/collab/piggyback.go` -- Notification middleware
- `internal/domain/entity.go` -- Core domain entities
