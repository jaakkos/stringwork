# AI Agent Planning Formats: A Comprehensive Analysis

**Prerequisite:** Complete the [API simplification](.cursor/plans/api_simplification_fc379623.plan.md) (15-tool refactor and consolidated `update_plan`) before implementing the proposed plan schema. The enhancement builds on the consolidated plan API. See [PLAN_ENHANCEMENT_IMPLEMENTATION.md](./PLAN_ENHANCEMENT_IMPLEMENTATION.md) for the phased implementation plan.

---

## 1. Classical AI Planning Standards

### PDDL (Planning Domain Definition Language)

**What it is:** Formal language for specifying planning problems, used since 1998.

```lisp
(:action move
  :parameters (?agent ?from ?to)
  :precondition (and (at ?agent ?from) (connected ?from ?to))
  :effect (and (at ?agent ?to) (not (at ?agent ?from))))
```

| Pros | Cons |
|------|------|
| Mathematically rigorous | Too rigid for LLM agents |
| Proven solvers exist | Doesn't handle uncertainty |
| Good for robotics | Poor natural language fit |

**Verdict:** Not suitable for LLM pair programming.

---

### HTN (Hierarchical Task Networks)

**What it is:** Decomposes high-level tasks into subtasks recursively.

```
Task: deploy_feature
  └── Subtask: write_code
  └── Subtask: write_tests
  └── Subtask: review
  └── Subtask: merge
```

| Pros | Cons |
|------|------|
| Natural task decomposition | Requires predefined methods |
| Good for complex workflows | Inflexible to runtime changes |
| Matches how humans think | Overhead for simple tasks |

**Verdict:** Good conceptual model, but too rigid as formal spec.

---

## 2. Modern LLM Agent Patterns

### ReAct (Reasoning + Acting)

**What it is:** Interleaved thinking and action, popularized by Yao et al. (2022).

```
Thought: I need to find where authentication is handled
Action: grep(pattern="auth", path="src/")
Observation: Found 3 files...
Thought: The main handler is in auth.go, let me read it
Action: read_file(path="src/auth.go")
...
```

| Pros | Cons |
|------|------|
| Captures reasoning chain | Verbose for storage |
| Debuggable | No standard schema |
| Works well with LLMs | Single-agent focused |

**Verdict:** Good for execution traces, adapt for planning.

---

### Plan-and-Execute

**What it is:** Create full plan first, then execute steps. Used by LangChain, BabyAGI.

```json
{
  "goal": "Add user authentication",
  "plan": [
    {"step": 1, "action": "Design auth flow", "status": "done"},
    {"step": 2, "action": "Implement JWT middleware", "status": "in_progress"},
    {"step": 3, "action": "Add login endpoint", "status": "pending"},
    {"step": 4, "action": "Write tests", "status": "pending"}
  ]
}
```

| Pros | Cons |
|------|------|
| Clear upfront structure | Plans become stale |
| Easy progress tracking | Replanning is awkward |
| Good for delegation | Doesn't capture dependencies well |

**Verdict:** Close to what we need, but needs enhancements.

---

## 3. Emerging Standards

### Model Context Protocol (MCP) - Anthropic

**What it is:** Open protocol for connecting AI assistants to tools/data.

```json
{
  "jsonrpc": "2.0",
  "method": "tools/call",
  "params": {
    "name": "read_file",
    "arguments": {"path": "src/main.go"}
  }
}
```

| Pros | Cons |
|------|------|
| Industry backing (Anthropic) | Tool-focused, not planning |
| Growing adoption | No native plan representation |
| Our server already uses it | Need to build planning on top |

**Verdict:** Use as transport layer, build planning schema on top.

---

### Agent Protocol - AI Engineer Foundation

**What it is:** REST API standard for agent interoperability.

```json
{
  "task_id": "abc-123",
  "input": "Implement authentication",
  "steps": [
    {
      "step_id": "step-1",
      "input": "Design auth flow",
      "output": "JWT-based auth with refresh tokens",
      "status": "completed"
    }
  ],
  "artifacts": [
    {"artifact_id": "art-1", "file_name": "auth.go"}
  ]
}
```

| Pros | Cons |
|------|------|
| Open standard | Heavyweight for 2-agent system |
| Supports artifacts | REST-based (we use JSON-RPC) |
| Step-level tracking | No native multi-agent support |

**Verdict:** Borrow concepts (steps, artifacts), don't adopt wholesale.

---

### AutoGPT / BabyAGI Format

**What it is:** Task queue with priorities and dependencies.

```json
{
  "tasks": [
    {
      "id": 1,
      "name": "Research auth libraries",
      "priority": 1,
      "dependencies": [],
      "result": null
    },
    {
      "id": 2,
      "name": "Implement chosen library",
      "priority": 2,
      "dependencies": [1],
      "result": null
    }
  ]
}
```

| Pros | Cons |
|------|------|
| Simple and practical | No reasoning capture |
| Handles dependencies | Single-agent assumption |
| Easy to implement | No acceptance criteria |

**Verdict:** Good baseline, we already have similar.

---

## 4. Recommended Format for Our Tool

### Design Principles

1. **Multi-agent native** - Owner field, handoff support
2. **Reasoning-aware** - Capture why, not just what
3. **Verifiable** - Acceptance criteria for done-ness
4. **Hierarchical** - Support sub-steps without separate tasks
5. **Traceable** - Version history for complex plans
6. **Interoperable** - Export to markdown/standard formats

### Proposed Schema

```go
type Plan struct {
    ID          string        `json:"id"`
    Title       string        `json:"title"`
    Goal        string        `json:"goal"`
    Context     string        `json:"context"`
    Version     int           `json:"version"`
    Status      string        `json:"status"`      // active, completed, archived
    Items       []PlanItem    `json:"items"`
    History     []PlanChange  `json:"history,omitempty"`
    CreatedBy   string        `json:"created_by"`
    CreatedAt   time.Time     `json:"created_at"`
    UpdatedAt   time.Time     `json:"updated_at"`
}

type PlanItem struct {
    ID           string     `json:"id"`
    Title        string     `json:"title"`
    Description  string     `json:"description,omitempty"`

    // NEW: Reasoning & verification
    Reasoning    string     `json:"reasoning,omitempty"`    // Why this approach
    Acceptance   []string   `json:"acceptance,omitempty"`   // Done-when criteria
    Constraints  []string   `json:"constraints,omitempty"`  // Known limitations

    // Execution tracking
    Status       string     `json:"status"`
    Owner        string     `json:"owner"`
    Steps        []PlanStep `json:"steps,omitempty"`        // Sub-steps for complex items

    // Dependencies & blockers
    Dependencies []string   `json:"dependencies"`
    Blockers     []string   `json:"blockers"`

    // Collaboration
    Notes        []string   `json:"notes"`
    Priority     int        `json:"priority"`
    UpdatedBy    string     `json:"updated_by"`
    UpdatedAt    time.Time  `json:"updated_at"`
}

type PlanStep struct {
    ID       string `json:"id"`        // "1.1", "1.2"
    Action   string `json:"action"`    // What to do
    Tool     string `json:"tool,omitempty"`     // Suggested tool
    Status   string `json:"status"`    // pending, done, failed, skipped
    Output   string `json:"output,omitempty"`   // Result summary
}

type PlanChange struct {
    Version   int       `json:"version"`
    ChangedBy string    `json:"changed_by"`
    ChangedAt time.Time `json:"changed_at"`
    Summary   string    `json:"summary"`
}
```

### Example Usage

```json
{
  "id": "auth-feature",
  "title": "Add JWT Authentication",
  "goal": "Secure API endpoints with JWT-based auth",
  "context": "Using Go stdlib, no external auth services",
  "version": 2,
  "status": "active",
  "items": [
    {
      "id": "1",
      "title": "Implement JWT middleware",
      "reasoning": "Middleware pattern keeps handlers clean and allows easy testing",
      "acceptance": [
        "Validates JWT signature",
        "Extracts user ID to context",
        "Returns 401 on invalid/expired token"
      ],
      "status": "in_progress",
      "owner": "cursor",
      "steps": [
        {"id": "1.1", "action": "Create middleware function", "status": "done"},
        {"id": "1.2", "action": "Add token validation", "status": "in_progress"},
        {"id": "1.3", "action": "Write unit tests", "status": "pending"}
      ],
      "dependencies": [],
      "priority": 1
    },
    {
      "id": "2",
      "title": "Add login endpoint",
      "reasoning": "POST /login returns JWT on valid credentials",
      "acceptance": [
        "Accepts email/password",
        "Returns JWT + refresh token",
        "Rate limited to 5 attempts/min"
      ],
      "status": "pending",
      "owner": "claude-code",
      "dependencies": ["1"],
      "priority": 2
    }
  ],
  "history": [
    {"version": 1, "changed_by": "cursor", "summary": "Initial plan"},
    {"version": 2, "changed_by": "claude-code", "summary": "Added acceptance criteria"}
  ]
}
```

### New Tools Needed

| Tool | Purpose |
|------|---------|
| add_plan_step | Add sub-step to an item |
| update_plan_step | Mark step done/failed |
| set_acceptance | Define done criteria |
| export_plan | Export as markdown/JSON-LD |
| get_plan_history | View version changes |

### Migration Path

1. **Phase 1:** Add reasoning, acceptance, constraints fields (backward compatible)
2. **Phase 2:** Add steps sub-structure
3. **Phase 3:** Add versioning and history
4. **Phase 4:** Add export tools

---

## 5. Summary Comparison

| Format | Multi-Agent | Reasoning | Verification | Complexity |
|--------|-------------|------------|---------------|------------|
| PDDL | ❌ | ❌ | ✅ | High |
| HTN | ❌ | ❌ | ❌ | High |
| ReAct | ❌ | ✅ | ❌ | Low |
| Agent Protocol | ❌ | ❌ | ❌ | Medium |
| AutoGPT/BabyAGI | ❌ | ❌ | ❌ | Low |
| Our Current | ✅ | ❌ | ❌ | Low |
| **Proposed** | ✅ | ✅ | ✅ | Medium |

---

## 6. Recommendation

Adopt the proposed hybrid format because:

1. Builds on our existing structure (minimal migration)
2. Adds reasoning for better agent coordination
3. Acceptance criteria solve "when is it done?" problem
4. Steps enable granular tracking without task explosion
5. Versioning provides audit trail for complex plans
6. Export tools enable human review and interoperability

**Priority order:**

1. **P0:** acceptance field - Critical for task completion
2. **P1:** reasoning field - Improves handoffs
3. **P2:** steps sub-structure - Better granularity
4. **P3:** Versioning - Nice for long-running plans
5. **P4:** Export tools - Future interoperability
