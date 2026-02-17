# Plan Enhancement Implementation Plan

Implements the schema from [PLANNING_FORMATS_ANALYSIS.md](./PLANNING_FORMATS_ANALYSIS.md). Prerequisite: 15-tool API simplification should be done first so we build on the consolidated `update_plan` API.

---

## Phase 1: Reasoning, acceptance, constraints (backward compatible)

**Goal:** Add optional fields so agents can record why and when-done without changing existing behavior.

### 1.1 Types

**File:** `internal/tools/collab/types.go`

- In `PlanItem`, add:
  - `Reasoning   string   \`json:"reasoning,omitempty"\`` — why this approach
  - `Acceptance  []string \`json:"acceptance,omitempty"\`` — done-when criteria
  - `Constraints []string \`json:"constraints,omitempty"\`` — known limitations

### 1.2 add_plan_item

**File:** `internal/tools/collab/collab.go` (or `planning.go` if split)

- Extend input schema with optional:
  - `reasoning` (string)
  - `acceptance` (array of strings)
  - `constraints` (array of strings)
- In handler: when creating `PlanItem`, set `item.Reasoning`, `item.Acceptance`, `item.Constraints` from args (parse arrays same way as `dependencies`).

### 1.3 update_plan_item

**File:** `internal/tools/collab/collab.go` (or `planning.go` if split)

- Extend input schema with optional:
  - `reasoning` (string) — replace if provided
  - `acceptance` (array of strings) — replace if provided
  - `constraints` (array of strings) — replace if provided
- In handler: if present, assign to `item.Reasoning`, `item.Acceptance`, `item.Constraints` and append to `changes` for the result message.

### 1.4 Plan formatting (get_plan output)

**File:** `internal/tools/collab/collab.go` (where plan string is built for `get_plan`)

- When rendering each plan item, if `Reasoning` or `Acceptance` or `Constraints` are non-empty, append a line or section so agents see them (e.g. “Reasoning: …”, “Acceptance: …”, “Constraints: …”).

### 1.5 Tests

**File:** `internal/tools/collab/collab_test.go`

- Add or extend tests: create plan, add item with reasoning/acceptance/constraints, update item with new reasoning/acceptance/constraints, load state and assert fields are persisted and returned in get_plan.

### 1.6 Docs

- **QUICK_REFERENCE.md:** Document optional `reasoning`, `acceptance`, `constraints` for add_plan_item and update_plan_item.
- **WORKFLOW.md / PLANNING_FORMATS_ANALYSIS.md:** Optional: one short example using acceptance criteria.

---

## Phase 2: Steps sub-structure

**Goal:** Allow sub-steps inside a plan item without creating separate tasks.

### 2.1 Types

**File:** `internal/tools/collab/types.go`

- Add `PlanStep` struct:
  - `ID`, `Action`, `Tool` (optional), `Status`, `Output` (optional)
- In `PlanItem`, add `Steps []PlanStep \`json:"steps,omitempty"\``.

### 2.2 New tools (or extend update_plan)

- **add_plan_step** — plan_id, item_id, step id (e.g. "1.1"), action, optional tool, added_by. Append to item.Steps with status "pending".
- **update_plan_step** — plan_id, item_id, step_id, status (done/failed/skipped), optional output, updated_by. Find step and update.

Alternatively: extend `update_plan` with action `add_step` / `update_step` and parameters (item_id, step_id, action, status, output) to avoid adding two more tools.

### 2.3 get_plan formatting

- When rendering an item, if it has Steps, list them with their status (e.g. "1.1 Create middleware — done", "1.2 Add validation — in_progress").

### 2.4 Tests and docs

- Tests: add item with steps, update step status, verify persistence and display.
- Docs: document add_plan_step / update_plan_step or the new update_plan actions.

---

## Phase 3: Versioning and history

**Goal:** Track plan-level changes for audit and long-running plans.

### 3.1 Types

**File:** `internal/tools/collab/types.go`

- Add `PlanChange` struct: Version (int), ChangedBy, ChangedAt, Summary.
- In `Plan`, add `Version int \`json:"version"\`` (default 1) and `History []PlanChange \`json:"history,omitempty"\``.

### 3.2 Plan mutation points

**File:** wherever plans are created or updated (create_plan, add_plan_item, update_plan_item, and in Phase 2 step tools)

- After modifying a plan (except no-op updates): increment plan.Version, append to plan.History with ChangedBy, ChangedAt, Summary (e.g. "Added item 2", "Updated item 1 status to completed"). Keep history bounded (e.g. last 50 entries) to avoid state bloat.

### 3.3 get_plan_history (optional tool)

- New tool: plan_id → returns plan.Version and plan.History (or last N entries). Enables “what changed” without adding to get_plan payload.

### 3.4 get_plan formatting

- Optionally show “Version: N” and last 1–2 history entries in the plan header.

### 3.5 Tests and docs

- Tests: update plan multiple times, assert Version and History; optional get_plan_history test.
- Docs: document version/history and optional get_plan_history.

---

## Phase 4: Export tools

**Goal:** Human review and interoperability (markdown, JSON-LD).

### 4.1 export_plan tool

- **Input:** plan_id, format (e.g. "markdown" or "json").
- **Behavior:** Load plan (and items with reasoning, acceptance, steps, etc.); if format is markdown, produce a readable markdown string (title, goal, context, version, then each item with title, status, owner, reasoning, acceptance, steps, notes); if format is json, return JSON (or JSON-LD with minimal context). Return as MCP text result.

### 4.2 Implementation notes

- Markdown: simple template or string building; no external deps.
- JSON-LD: optional; can be a second format or later iteration.

### 4.3 Tests and docs

- Test: create plan with items (and optional steps), export as markdown and json, assert structure.
- Docs: document export_plan and example output.

---

## File-level summary

| Phase | File(s) | Changes |
|-------|---------|---------|
| 1 | types.go | PlanItem: Reasoning, Acceptance, Constraints |
| 1 | collab.go (or planning.go) | add_plan_item + update_plan_item schema and handler; get_plan formatting |
| 1 | collab_test.go | Tests for new fields |
| 1 | docs (QUICK_REFERENCE, etc.) | Document new params |
| 2 | types.go | PlanStep struct; PlanItem.Steps |
| 2 | collab.go | add_plan_step, update_plan_step (or update_plan actions); get_plan steps display |
| 2 | collab_test.go, docs | Tests and docs |
| 3 | types.go | PlanChange; Plan.Version, Plan.History |
| 3 | collab.go | Bump version + history on mutations; get_plan_history (optional); get_plan version/history |
| 3 | collab_test.go, docs | Tests and docs |
| 4 | collab.go | export_plan tool |
| 4 | collab_test.go, docs | Tests and docs |

---

## Priority order (from analysis)

1. **P0:** acceptance — Phase 1
2. **P1:** reasoning — Phase 1
3. **P2:** steps — Phase 2
4. **P3:** versioning — Phase 3
5. **P4:** export — Phase 4

Phase 1 is implemented first; Phases 2–4 can follow in order or be scheduled separately.
